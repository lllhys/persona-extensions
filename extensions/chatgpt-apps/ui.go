package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The plugin pages: contributes.ui "plugins", an extension_page under the extension's details (Settings › Extensions
// › ChatGPT 应用 › 插件). The marketplace with search and load more, a plugin's details with install and uninstall, the
// installed plugins, and the connected apps with their tools. Only components of UI version 3, which every app draws
// (the iPhone's full pages send 4); apps of UI version 6 also get a swipe to uninstall in the installed list.
//
// Installing and uninstalling from these pages is the user's own tap, asked once more by the app's confirm dialog; the
// assistant's calls still get the hub's card every time (confirm_always). What other people wrote (descriptions,
// names) is drawn as plain text, clipped: a page with an image or an http link in Markdown would be refused whole.

const (
	uiPlugins     = "plugins"
	pageRows      = 20  // rows one load adds to the marketplace
	mostRows      = 240 // a page holds at most 300 components
	installedRows = 5   // installed plugins on the marketplace page before "all"
	listTTL       = 10 * time.Minute
	installedTTL  = time.Minute
	mostListings  = 32 // marketplace listings (searches) kept
)

// uiState is a page's state: which page, and what it shows.
type uiState struct {
	Page string `json:"p,omitempty"`  // "": the marketplace; plugin, installed, apps, app
	ID   string `json:"id,omitempty"` // the plugin's or the app's id
	Q    string `json:"q,omitempty"`  // the marketplace's search
	N    int    `json:"n,omitempty"`  // marketplace rows shown
}

const (
	pagePlugin    = "plugin"
	pageInstalled = "installed"
	pageApps      = "apps"
	pageApp       = "app"
)

// listing is a marketplace listing read so far (all plugins, or one search), for load more.
type listing struct {
	items []pluginDetail
	token string
	done  bool
	at    time.Time
}

func (a *App) pluginsPage() ext.Page {
	return ext.Page{
		Render: func(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
			var st uiState
			_ = r.DecodeState(&st)
			return a.view(ctx, r, st), nil
		},
		Action: a.pageAction,
	}
}

func (a *App) uiText(r *ext.UIRequest) uiTexts {
	if r.Locale != "" {
		return textsFor(r.Locale).ui
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.text.ui
}

func (a *App) pageAction(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	var st uiState
	_ = r.DecodeState(&st)
	tx := a.uiText(r)
	switch r.Action {
	case "search":
		st.Q, st.N = strings.TrimSpace(r.Values["q"]), 0
	case "more":
		st.N = min(max(st.N, pageRows)+pageRows, mostRows)
	case "retry":
		a.forgetListings()
	case "install", "uninstall":
		id := st.ID
		if v := r.Values["id"]; v != "" { // the installed list's swipe
			id = v
		}
		toast, err := a.pageWrite(ctx, tx, r.Action, id)
		if err != nil {
			toast = fmt.Sprintf(tx.writeFailed, short(statusOnly(err), 200))
		}
		return &wire.ActionResult{Toast: toast, View: a.view(ctx, r, st)}, nil
	}
	return &wire.ActionResult{View: a.view(ctx, r, st)}, nil
}

// pageWrite installs or uninstalls a plugin for the user's tap (after the app's confirm dialog): the toast.
func (a *App) pageWrite(ctx context.Context, tx uiTexts, what, id string) (string, error) {
	if !isPluginID(normalizeRef(id)) {
		return "", errors.New("no plugin on this page")
	}
	if what == "uninstall" {
		p, _, err := a.uninstallPlugin(ctx, id)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(tx.uninstalledToast, label(p.title())), nil
	}
	p, res, err := a.installPlugin(ctx, id)
	if err != nil {
		return "", err
	}
	if n := len(res.AppIDsNeedingAuth); n > 0 {
		return fmt.Sprintf(tx.connectToast, label(p.title()), n), nil
	}
	return fmt.Sprintf(tx.installedToast, label(p.title())), nil
}

// view draws the page st is: signed out, a problem reading ChatGPT, or the page.
func (a *App) view(ctx context.Context, r *ext.UIRequest, st uiState) *wire.View {
	tx := a.uiText(r)
	if _, err := a.access(ctx); err != nil {
		return a.problemView(tx, st, err)
	}
	var v *wire.View
	var err error
	switch st.Page {
	case pagePlugin:
		v, err = a.pluginView(ctx, tx, st)
	case pageInstalled:
		v, err = a.installedView(ctx, tx, st, r.UIVersion)
	case pageApps:
		v, err = a.appsView(ctx, tx, st)
	case pageApp:
		v, err = a.appView(ctx, tx, st)
	default:
		st.Page, st.ID = "", ""
		v, err = a.marketView(ctx, tx, st)
	}
	if err != nil {
		return a.problemView(tx, st, err)
	}
	v.State = ext.EncodeState(st)
	return v
}

func (a *App) problemView(tx uiTexts, st uiState, err error) *wire.View {
	empty := wire.Component{Type: wire.ComponentEmpty, Key: "problem", Icon: "warning", Title: tx.problem,
		Detail: short(statusOnly(err), 300), Button: &wire.EmptyButton{Title: tx.retry, Action: wire.Action{ID: "retry"}}}
	switch {
	case errors.Is(err, errNotSignedIn):
		empty = wire.Component{Type: wire.ComponentEmpty, Key: "signed-out", Icon: "person", Title: tx.signedOut, Detail: tx.signedOutHint}
	case errors.Is(err, errExpired):
		empty = wire.Component{Type: wire.ComponentEmpty, Key: "signed-out", Icon: "person", Title: tx.expired, Detail: tx.expiredHint}
	}
	return &wire.View{Title: tx.title, State: ext.EncodeState(st), Sections: []wire.Section{{Items: []wire.Component{empty}}}}
}

// ---- the marketplace

func (a *App) marketView(ctx context.Context, tx uiTexts, st uiState) (*wire.View, error) {
	want := min(max(st.N, pageRows), mostRows)
	var (
		wg               sync.WaitGroup
		installed        []pluginDetail
		list             []pluginDetail
		more             bool
		instErr, listErr error
		apps             []appSummary
		appsErr          error
	)
	wg.Add(2)
	go func() { defer wg.Done(); installed, instErr = a.installedList(ctx) }()
	go func() { defer wg.Done(); list, more, listErr = a.market(ctx, st.Q, want) }()
	if st.Q == "" {
		wg.Add(1)
		go func() { defer wg.Done(); apps, appsErr = a.appSummaries(ctx) }()
	}
	wg.Wait()
	if err := errors.Join(instErr, listErr); err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, p := range installed {
		ids[p.ID] = true
	}
	v := &wire.View{Title: tx.title, Search: &wire.Search{Placeholder: tx.search, Value: st.Q, Action: wire.Action{ID: "search"}}}
	rows := func(ps []pluginDetail, prefix string, badges bool) []wire.Component {
		out := []wire.Component{}
		for _, p := range ps {
			out = append(out, pluginRow(tx, p, badges && ids[p.ID], prefix))
		}
		return out
	}
	var moreRow *wire.More
	if more {
		moreRow = &wire.More{Title: tx.more, Action: wire.Action{ID: "more"}}
	}
	if st.Q != "" {
		if len(list) == 0 {
			v.Sections = []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Key: "no-results", Icon: "search",
				Title: fmt.Sprintf(tx.noResults, label(st.Q)), Detail: tx.noResultsDetail}}}}
			return v, nil
		}
		v.Sections = []wire.Section{{Title: fmt.Sprintf(tx.results, label(st.Q)), Items: rows(list, "r:", true), More: moreRow}}
		return v, nil
	}
	if len(installed) > 0 {
		items := rows(installed[:min(len(installed), installedRows)], "i:", false) // the section says it
		if len(installed) > installedRows {
			items = append(items, wire.Component{Type: wire.ComponentRow, Key: "all-installed", Icon: "list",
				Title:  fmt.Sprintf(tx.allInstalled, len(installed)),
				Action: &wire.Action{ID: "installed", Push: &wire.Push{State: ext.EncodeState(uiState{Page: pageInstalled}), Title: tx.installedTitle}}})
		}
		v.Sections = append(v.Sections, wire.Section{Title: fmt.Sprintf(tx.installed, len(installed)), Items: items})
	}
	if appsErr == nil && len(apps) > 0 {
		connected, pending, tools, ro := 0, 0, 0, 0
		for _, s := range apps {
			if s.synthetic { // installed with a plugin, never connected: its tools do not run yet
				pending++
				continue
			}
			connected, tools, ro = connected+1, tools+s.tools, ro+s.readOnly
		}
		detail := []string{}
		if connected > 0 {
			detail = append(detail, fmt.Sprintf(tx.appsRowDetail, tools, ro))
		}
		if pending > 0 {
			detail = append(detail, fmt.Sprintf(tx.appsRowPending, pending))
		}
		v.Sections = append(v.Sections, wire.Section{Title: tx.apps, Items: []wire.Component{{Type: wire.ComponentRow, Key: "apps",
			Icon: "link", Title: fmt.Sprintf(tx.appsRow, connected), Subtitle: strings.Join(detail, " · "),
			Action: &wire.Action{ID: "apps", Push: &wire.Push{State: ext.EncodeState(uiState{Page: pageApps}), Title: tx.appsTitle}}}}})
	}
	v.Sections = append(v.Sections, wire.Section{Title: tx.market, Items: rows(list, "m:", true), More: moreRow})
	return v, nil
}

// pluginRow is a plugin in a list: its name, developer and what it does; tapping opens its details.
func pluginRow(tx uiTexts, p pluginDetail, installed bool, keyPrefix string) wire.Component {
	sub := []string{}
	if d := p.Release.Interface.DeveloperName; d != "" {
		sub = append(sub, label(d))
	}
	if d := oneLine(p.Release.Description); d != "" {
		sub = append(sub, short(d, 120))
	}
	row := wire.Component{Type: wire.ComponentRow, Key: keyPrefix + p.ID, Title: label(p.title()), Subtitle: strings.Join(sub, " · "),
		Avatar: &wire.Avatar{Text: initials(p.title())},
		Action: &wire.Action{ID: "open", Push: &wire.Push{State: ext.EncodeState(uiState{Page: pagePlugin, ID: p.ID}), Title: label(p.title())}}}
	if installed {
		row.Badge = &wire.Badge{Text: tx.installedBadge, Tone: wire.ToneNeutral}
	}
	return row
}

// market is the first want plugins of the marketplace (q "") or of a search, read page by page as load more asks, and
// whether there are more.
func (a *App) market(ctx context.Context, q string, want int) ([]pluginDetail, bool, error) {
	a.listMu.Lock()
	defer a.listMu.Unlock()
	now := a.now()
	a.mu.Lock()
	for k, l := range a.listings {
		if now.Sub(l.at) > listTTL {
			delete(a.listings, k)
		}
	}
	l := a.listings[q]
	if l == nil {
		if len(a.listings) >= mostListings {
			clear(a.listings)
		}
		l = &listing{at: now}
		a.listings[q] = l
	}
	a.mu.Unlock()
	for len(l.items) < want && !l.done {
		path, params := "/ps/plugins/list", url.Values{"scope": {"GLOBAL"}, "limit": {"50"}}
		if q != "" {
			path, params = "/ps/plugins/search", url.Values{"q": {q}, "limit": {"50"}}
		}
		if l.token != "" {
			params.Set("pageToken", l.token)
		}
		var res struct {
			Plugins    []pluginDetail `json:"plugins"`
			Pagination struct {
				NextPageToken string `json:"next_page_token"`
			} `json:"pagination"`
		}
		if err := a.backend(ctx, http.MethodGet, path+"?"+params.Encode(), nil, &res); err != nil {
			return nil, false, err
		}
		for _, p := range res.Plugins {
			a.remember(p)
		}
		l.items = append(l.items, res.Plugins...)
		next := res.Pagination.NextPageToken
		l.done = next == "" || next == l.token || len(res.Plugins) == 0
		l.token = next
	}
	n := min(want, len(l.items))
	return slices.Clone(l.items[:n]), len(l.items) > n || !l.done, nil
}

func (a *App) forgetListings() {
	a.mu.Lock()
	clear(a.listings)
	a.inst, a.instAt = nil, time.Time{}
	a.mu.Unlock()
}

// installedList is the installed plugins, read again after a minute or after an install or uninstall.
func (a *App) installedList(ctx context.Context) ([]pluginDetail, error) {
	a.mu.Lock()
	if a.inst != nil && a.now().Sub(a.instAt) < installedTTL {
		ps := a.inst
		a.mu.Unlock()
		return ps, nil
	}
	a.mu.Unlock()
	ps, err := a.installed(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		a.remember(p)
	}
	if ps == nil {
		ps = []pluginDetail{}
	}
	a.mu.Lock()
	a.inst, a.instAt = ps, a.now()
	a.mu.Unlock()
	return ps, nil
}

// ---- one plugin

func (a *App) pluginView(ctx context.Context, tx uiTexts, st uiState) (*wire.View, error) {
	var (
		wg         sync.WaitGroup
		p          pluginDetail
		installed  []pluginDetail
		pErr, iErr error
	)
	wg.Add(2)
	go func() { defer wg.Done(); p, pErr = a.resolve(ctx, st.ID) }()
	go func() { defer wg.Done(); installed, iErr = a.installedList(ctx) }()
	wg.Wait()
	if err := errors.Join(pErr, iErr); err != nil {
		return nil, err
	}
	in := slices.ContainsFunc(installed, func(x pluginDetail) bool { return x.ID == p.ID })
	apps, err := a.appStates(ctx, p.Release.AppIDs)
	if err != nil {
		return nil, err
	}
	title := label(p.title())
	head := wire.Component{Type: wire.ComponentRow, Key: "head", Title: title, Avatar: &wire.Avatar{Text: initials(p.title())},
		Subtitle: label(p.Release.Interface.DeveloperName)}
	if in {
		head.Badge = &wire.Badge{Text: tx.installedBadge, Tone: wire.TonePositive}
	}
	about := wire.Section{Items: []wire.Component{head}}
	desc := strings.TrimSpace(p.Release.Interface.LongDescription)
	if desc == "" {
		desc = strings.TrimSpace(p.Release.Description)
	}
	if desc != "" {
		about.Items = append(about.Items, wire.Component{Type: wire.ComponentText, Key: "about", Text: short(desc, 1900)})
	}
	if p.Release.Version != "" {
		about.Footer = fmt.Sprintf(tx.version, label(p.Release.Version))
	}
	v := &wire.View{Title: title, Sections: []wire.Section{about}}

	if len(apps) > 0 {
		sec := wire.Section{Title: fmt.Sprintf(tx.pluginApps, len(apps))}
		unconnected := false
		for _, s := range apps {
			name := s.Name
			if name == "" {
				name = s.ID
			}
			row := wire.Component{Type: wire.ComponentRow, Key: "app:" + s.ID, Icon: "link", Title: label(name)}
			switch {
			case s.State == "connected":
				row.Subtitle = fmt.Sprintf(tx.connectedDetail, len(s.Tools))
				row.Badge = &wire.Badge{Text: tx.connected, Tone: wire.TonePositive}
			case in:
				unconnected = true
				row.Subtitle = tx.needsDetail
				row.Badge = &wire.Badge{Text: tx.needsConnect, Tone: wire.ToneCaution}
			case slices.Contains(s.Auth, "OAUTH"):
				row.Subtitle = tx.afterInstall
			}
			sec.Items = append(sec.Items, row)
		}
		if unconnected {
			sec.Footer = tx.connectFooter
			sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentLink, Key: "connect", Title: tx.connectLink, URL: p.connectURL()})
		}
		v.Sections = append(v.Sections, sec)
	}
	if len(p.Release.Skills) > 0 {
		sec := wire.Section{Title: fmt.Sprintf(tx.pluginSkills, len(p.Release.Skills))}
		for i, s := range p.Release.Skills {
			sec.Items = append(sec.Items, wire.Component{Type: wire.ComponentRow, Key: fmt.Sprintf("skill:%d", i), Icon: "doc",
				Title: label(s.Name), Subtitle: short(oneLine(s.Description), 200)})
		}
		v.Sections = append(v.Sections, sec)
	}
	button := wire.Component{Type: wire.ComponentButton, Key: "install", Title: tx.install,
		Action: &wire.Action{ID: "install", Confirm: fmt.Sprintf(tx.installConfirm, title, len(p.Release.AppIDs), len(p.Release.Skills))}}
	if in {
		button = wire.Component{Type: wire.ComponentButton, Key: "uninstall", Title: tx.uninstall, Role: wire.ButtonRoleDestructive,
			Action: &wire.Action{ID: "uninstall", Confirm: fmt.Sprintf(tx.uninstallConfirm, title)}}
	}
	v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{button}})
	return v, nil
}

// ---- the installed plugins

func (a *App) installedView(ctx context.Context, tx uiTexts, st uiState, uiVersion int) (*wire.View, error) {
	installed, err := a.installedList(ctx)
	if err != nil {
		return nil, err
	}
	v := &wire.View{Title: tx.installedTitle}
	if len(installed) == 0 {
		v.Sections = []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Key: "none", Icon: "extension",
			Title: tx.noInstalled, Detail: tx.noInstalledHint}}}}
		return v, nil
	}
	sec := wire.Section{}
	for _, p := range installed[:min(len(installed), mostRows)] {
		row := pluginRow(tx, p, false, "i:")
		if uiVersion >= 6 { // a swipe to uninstall: a shortcut for the details' button
			row.Actions = []wire.RowAction{{Title: tx.uninstall, Role: wire.ButtonRoleDestructive, Swipe: true,
				Action: wire.Action{ID: "uninstall", Confirm: fmt.Sprintf(tx.uninstallConfirm, label(p.title())), Values: map[string]string{"id": p.ID}}}}
		}
		sec.Items = append(sec.Items, row)
	}
	v.Sections = []wire.Section{sec}
	return v, nil
}

// ---- the connected apps

// appSummary is one app connected in ChatGPT (or installed with a plugin and not connected yet), from the tool list.
type appSummary struct {
	id, name        string
	tools, readOnly int
	synthetic       bool
	toolList        []mcpTool
}

func (a *App) appSummaries(ctx context.Context) ([]appSummary, error) {
	tools, err := a.listTools(ctx, false)
	if err != nil {
		return nil, err
	}
	by := map[string]*appSummary{}
	for _, t := range tools {
		id, _ := t.Meta["connector_id"].(string)
		if id == "" {
			id = t.app()
		}
		s := by[id]
		if s == nil {
			s = &appSummary{id: id, name: t.app()}
			by[id] = s
		}
		s.tools++
		if t.readOnly() {
			s.readOnly++
		}
		if t.synthetic() {
			s.synthetic = true
		}
		s.toolList = append(s.toolList, t)
	}
	out := make([]appSummary, 0, len(by))
	for _, s := range by {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].synthetic != out[j].synthetic {
			return !out[i].synthetic
		}
		return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
	})
	return out, nil
}

func (a *App) appsView(ctx context.Context, tx uiTexts, st uiState) (*wire.View, error) {
	apps, err := a.appSummaries(ctx)
	if err != nil {
		return nil, err
	}
	v := &wire.View{Title: tx.appsTitle}
	if len(apps) == 0 {
		v.Sections = []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Key: "none", Icon: "link",
			Title: tx.noApps, Detail: tx.noAppsHint}}}}
		return v, nil
	}
	sec := wire.Section{Footer: tx.appsFooter}
	for _, s := range apps[:min(len(apps), mostRows)] {
		row := wire.Component{Type: wire.ComponentRow, Key: "app:" + s.id, Avatar: &wire.Avatar{Text: initials(s.name)}, Title: label(s.name),
			Value:    fmt.Sprintf(tx.appTools, s.tools),
			Subtitle: fmt.Sprintf(tx.appToolsDetail, s.readOnly, s.tools-s.readOnly),
			Action:   &wire.Action{ID: "app", Push: &wire.Push{State: ext.EncodeState(uiState{Page: pageApp, ID: s.id}), Title: label(s.name)}}}
		if s.synthetic { // its tools do not run yet: what to do, not a count
			row.Value, row.Subtitle = "", tx.needsDetail
			row.Badge = &wire.Badge{Text: tx.needsConnect, Tone: wire.ToneCaution}
		}
		sec.Items = append(sec.Items, row)
	}
	v.Sections = []wire.Section{sec}
	return v, nil
}

func (a *App) appView(ctx context.Context, tx uiTexts, st uiState) (*wire.View, error) {
	apps, err := a.appSummaries(ctx)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(apps, func(s appSummary) bool { return s.id == st.ID })
	if i < 0 {
		return a.appsView(ctx, tx, uiState{Page: pageApps})
	}
	s := apps[i]
	v := &wire.View{Title: label(s.name)}
	if s.synthetic {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{Type: wire.ComponentText, Key: "not-connected",
			Style: wire.TextStyleSecondary, Text: tx.notConnectedApp}}})
	}
	tools := slices.Clone(s.toolList)
	sort.SliceStable(tools, func(i, j int) bool {
		if tools[i].readOnly() != tools[j].readOnly() {
			return tools[i].readOnly()
		}
		return tools[i].title() < tools[j].title()
	})
	sec := wire.Section{Title: fmt.Sprintf(tx.appTools, len(tools)), Footer: tx.appsFooter}
	for k, t := range tools[:min(len(tools), mostRows)] {
		row := wire.Component{Type: wire.ComponentRow, Key: fmt.Sprintf("tool:%d", k), Title: label(t.title()),
			Subtitle: short(oneLine(t.Description), 160)}
		if t.readOnly() {
			row.Badge = &wire.Badge{Text: tx.readOnly, Tone: wire.ToneNeutral}
		} else {
			row.Badge = &wire.Badge{Text: tx.changes, Tone: wire.ToneCaution}
		}
		sec.Items = append(sec.Items, row)
	}
	v.Sections = append(v.Sections, sec)
	return v, nil
}

// ---- text helpers

// label is a name for a title or a badge: one line, at most 120 characters.
func label(s string) string { return short(oneLine(s), 120) }

// oneLine is the first line of s that has text.
func oneLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// initials are one or two characters for an initials avatar: the first letters of the first two words, or the first
// character of a name in Chinese, Japanese or Korean.
func initials(name string) string {
	var out []rune
	for _, w := range strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		r, _ := utf8.DecodeRuneInString(w)
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
			if len(out) == 0 {
				return string(r)
			}
			break
		}
		out = append(out, unicode.ToUpper(r))
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}
