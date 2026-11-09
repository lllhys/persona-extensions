// Package page is the extension's X page (ui contribution "x", a nav_page): the organized digest of the last 24
// hours or 3 days (internal/x/digest.go), not a timeline. A header (the account, today's budget, the last sync, how
// the connector is doing), a 24h / 3d switch, and the digest's capped sections; each post shown as X asks: the
// author's name and @username, the full unaltered text, its counts and a link to the post on x.com.
package page

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/x/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/x/internal/x"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// ID is the ui contribution's (persona-extension.json).
const ID = "x"

// Limits of a view (the hub refuses one over them).
const (
	componentsMost = 300
	chunkMost      = 1800 // characters of one text item (the apps take 2000)
	postChunksMost = 6    // text items of one post: about 10,000 characters, the rest on X
	refreshAfter   = 300
	syncingRefresh = 30
)

// Page draws the X page for the connector.
type Page struct {
	c   *x.Connector
	now func() time.Time
}

// New is the page of c.
func New(c *x.Connector) *Page { return &Page{c: c, now: time.Now} }

// state is the page's: the window.
type state struct {
	W string `json:"w,omitempty"`
}

// Render draws the page.
func (p *Page) Render(_ context.Context, r *ext.UIRequest) (*wire.View, error) {
	var s state
	_ = r.DecodeState(&s)
	return p.view(r, s), nil
}

// Action answers a tap: the window, refresh, a followed account's profile.
func (p *Page) Action(_ context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	var s state
	_ = r.DecodeState(&s)
	t := texts(r.Locale)
	kind, arg, _ := strings.Cut(r.Action, ":")
	switch kind {
	case "window":
		if arg == "" { // the segmented control: the choice is in the values
			arg = r.Values["w"]
		}
		s.W, _ = x.WindowOf(arg)
		return &wire.ActionResult{View: p.view(r, s)}, nil
	case "refresh":
		toast := ""
		if o := p.c.Overview(); o.Via == x.SourceGrok && o.GrokFound {
			toast = t.grokRefreshing
			if !p.c.RefreshGrokSoon() {
				toast = t.grokFresh
			}
		} else if o.SignedIn {
			p.c.Poke()
			toast = t.refreshing
		}
		return &wire.ActionResult{View: p.view(r, s), Toast: toast}, nil
	case "profile":
		if validUsername(arg) {
			return &wire.ActionResult{OpenURL: "https://x.com/" + arg}, nil
		}
	}
	return nil, nil
}

// linkable says whether u can be a link component: the hub's view check (wire.CheckView) takes only an https URL
// with a host and no user info, and refuses the whole page over one that is not.
func linkable(u string) bool {
	if len(u) > wire.StringMost {
		return false
	}
	p, err := url.Parse(u)
	return err == nil && p.Scheme == "https" && p.Hostname() != "" && p.User == nil
}

func validUsername(u string) bool {
	if u == "" || len(u) > 15 {
		return false
	}
	for _, r := range u {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// view is the page for s.
func (p *Page) view(r *ext.UIRequest, s state) *wire.View {
	t := texts(r.Locale)
	window, _ := x.WindowOf(s.W)
	s.W = window
	o := p.c.Overview()
	v := &wire.View{Title: "X", State: ext.EncodeState(s), RefreshAfter: refreshAfter}
	if o.Syncing || o.GrokBusy {
		v.RefreshAfter = syncingRefresh
	}
	if o.Via == x.SourceGrok {
		return p.grokView(r, t, o, v)
	}
	if !o.SignedIn {
		if o.Via == "" { // neither way is set up
			v.Sections = []wire.Section{{Items: []wire.Component{grokSetUp(r, t, o)}}}
			return v
		}
		v.Sections = []wire.Section{{Items: []wire.Component{signInPrompt(r, t)}}}
		return v
	}
	loc := location(r, p.c.Location())
	d := p.c.Digest(window)
	b := &builder{left: componentsMost}

	v.Sections = append(v.Sections, p.header(r, t, o, loc, b))
	v.Sections = append(v.Sections, windowControl(r, t, window, b))

	top := wire.Section{Title: t.top}
	for _, rk := range d.Top {
		extra := ""
		if len(rk.RepostedBy) > 0 {
			extra = fmt.Sprintf(t.repostedBy, len(rk.RepostedBy))
		}
		top.Items = append(top.Items, b.post("top", rk.Post, extra, t, loc)...)
	}
	v.Sections = append(v.Sections, filled(top, t, b))

	links := wire.Section{Title: t.links}
	for i, l := range d.Links {
		title := l.Display
		if title == "" {
			title = l.URL
		}
		title = clip(title, 300) + " · " + fmt.Sprintf(t.sharedBy, len(l.Authors))
		if linkable(l.URL) {
			links.Items = b.add(links.Items, wire.Component{Type: wire.ComponentLink, Key: fmt.Sprintf("link:%d", i), Title: title, URL: l.URL})
		} else {
			links.Items = b.add(links.Items, wire.Component{Type: wire.ComponentText, Key: fmt.Sprintf("link:%d", i), Text: clip(title+"\n"+l.URL, chunkMost), Style: wire.TextStyleSecondary})
		}
	}
	v.Sections = append(v.Sections, filled(links, t, b))

	threads := wire.Section{Title: t.threads}
	for _, th := range d.Threads {
		threads.Items = append(threads.Items, b.post("thread", th.Root, fmt.Sprintf(t.threadOf, th.Count), t, loc)...)
	}
	v.Sections = append(v.Sections, filled(threads, t, b))

	act := wire.Section{Title: t.active}
	for _, a := range d.Active {
		row := wire.Component{Type: wire.ComponentRow, Key: "active:" + a.ID, Title: clip(name(a.Name, a.Username), 300), Value: fmt.Sprintf(t.postsN, a.Posts)}
		if validUsername(a.Username) {
			row.Action = &wire.Action{ID: "profile:" + a.Username}
		}
		act.Items = b.add(act.Items, row)
	}
	v.Sections = append(v.Sections, filled(act, t, b))

	foot := fmt.Sprintf(t.totals, d.Seen, d.BudgetUsed, d.Budget)
	tail := []wire.Component{{Type: wire.ComponentText, Key: "totals", Text: foot, Style: wire.TextStyleCaption},
		{Type: wire.ComponentButton, Key: "refresh", Title: t.refresh, Action: &wire.Action{ID: "refresh"}}}
	v.Sections = append(v.Sections, wire.Section{Footer: t.footer, Items: tail})
	return v
}

// builder keeps a view within componentsMost: what does not fit is left out (each section ends with what fits).
type builder struct{ left int }

// reserve is what the sections after the digest's need (empty notes, the totals, the refresh button).
const reserve = 8

func (b *builder) add(items []wire.Component, c ...wire.Component) []wire.Component {
	if b.left-len(c) < reserve {
		return items
	}
	b.left -= len(c)
	return append(items, c...)
}

// post is one post as X asks it to be shown: "Name @username", the full text, its counts and when, the permalink.
func (b *builder) post(section string, p x.Post, extra string, t text, loc *time.Location) []wire.Component {
	sub := []string{fmt.Sprintf(t.counts, p.Likes, p.Reposts, p.Replies, p.Quotes), p.CreatedAt.In(loc).Format("01-02 15:04")}
	if extra != "" {
		sub = append(sub, extra)
	}
	key := section + ":" + p.ID
	out := []wire.Component{{Type: wire.ComponentRow, Key: key, Title: clip(name(p.Name, p.Username), 300), Subtitle: strings.Join(sub, " · ")}}
	cs := chunks(p.Text, chunkMost)
	cut := false
	room := min(postChunksMost, b.left-reserve-len(out)-2) // the row, the link, maybe a note
	if len(cs) > room {
		cs, cut = cs[:max(room, 0)], true
	}
	for i, c := range cs {
		out = append(out, wire.Component{Type: wire.ComponentText, Key: fmt.Sprintf("%s:t%d", key, i), Text: c, Style: wire.TextStyleBody})
	}
	if cut {
		out = append(out, wire.Component{Type: wire.ComponentText, Key: key + ":more", Text: t.moreOnX, Style: wire.TextStyleCaption})
	}
	out = append(out, wire.Component{Type: wire.ComponentLink, Key: key + ":link", Title: t.openOnX, URL: p.Permalink()})
	if b.left-len(out) < reserve {
		return nil
	}
	b.left -= len(out)
	return out
}

// filled is s, with a note when it has nothing.
func filled(s wire.Section, t text, b *builder) wire.Section {
	if len(s.Items) == 0 {
		s.Items = []wire.Component{{Type: wire.ComponentText, Key: "none:" + s.Title, Text: t.none, Style: wire.TextStyleSecondary}}
		b.left--
	}
	return s
}

// header is the account, today's budget, the last sync and the state.
func (p *Page) header(r *ext.UIRequest, t text, o x.Overview, loc *time.Location, b *builder) wire.Section {
	last := t.never
	if !o.LastSync.IsZero() {
		last = o.LastSync.In(loc).Format("01-02 15:04")
	}
	state, tone := t.stateOK, wire.TonePositive
	switch o.Status.State {
	case connector.StateRateLimited:
		state, tone = t.stateRateLimited, wire.ToneCaution
	case connector.StateError:
		state, tone = t.stateError, wire.ToneDanger
	}
	if o.Syncing {
		state = t.syncing
	}
	budget := fmt.Sprintf("%d / %d", o.BudgetUsed, o.Budget)
	if o.FeedSource == x.FeedOff {
		budget += " · " + t.feedOff
	}
	var items []wire.Component
	if r.UIVersion >= 2 {
		items = []wire.Component{{Type: wire.ComponentStatGrid, Key: "header", Stats: []wire.Stat{
			{Label: t.account, Value: o.Status.Account}, {Label: t.budget, Value: budget}, {Label: t.lastSync, Value: last},
			{Label: t.state, Value: state, Tone: tone}}}}
	} else {
		items = []wire.Component{
			{Type: wire.ComponentRow, Key: "account", Title: t.account, Value: o.Status.Account},
			{Type: wire.ComponentRow, Key: "budget", Title: t.budget, Value: budget},
			{Type: wire.ComponentRow, Key: "last", Title: t.lastSync, Value: last},
			{Type: wire.ComponentRow, Key: "state", Title: t.state, Badge: &wire.Badge{Text: state, Tone: tone}},
		}
	}
	if o.Status.Error != "" && o.Status.State != connector.StateOK {
		items = append(items, wire.Component{Type: wire.ComponentText, Key: "error", Text: clip(o.Status.Error, chunkMost), Style: wire.TextStyleSecondary})
		for i, g := range o.Status.Guide {
			items = append(items, wire.Component{Type: wire.ComponentText, Key: fmt.Sprintf("guide:%d", i), Text: clip(fmt.Sprintf("%d. %s", i+1, g), chunkMost), Style: wire.TextStyleCaption})
		}
	}
	b.left -= len(items)
	return wire.Section{Items: items}
}

// windowControl is 24h / 3d: a segmented control, or a row each for an app before UI version 2.
func windowControl(r *ext.UIRequest, t text, cur string, b *builder) wire.Section {
	choices := []wire.Choice{{Value: x.Window24h, Title: t.w24h}, {Value: x.Window3d, Title: t.w3d}}
	if r.UIVersion >= 2 {
		b.left--
		return wire.Section{Items: []wire.Component{{Type: wire.ComponentSegmented, Key: "window", Name: "w", Value: cur, Choices: choices, Action: &wire.Action{ID: "window"}}}}
	}
	var rows []wire.Component
	for _, c := range choices {
		row := wire.Component{Type: wire.ComponentRow, Key: "window:" + c.Value, Title: c.Title, Action: &wire.Action{ID: "window:" + c.Value}}
		if c.Value == cur {
			row.Badge = &wire.Badge{Text: t.current, Tone: wire.TonePositive}
		}
		rows = append(rows, row)
	}
	b.left -= len(rows)
	return wire.Section{Title: t.window, Items: rows}
}

// signInPrompt is the page while not signed in.
func signInPrompt(r *ext.UIRequest, t text) wire.Component {
	if r.UIVersion >= 3 {
		return wire.Component{Type: wire.ComponentEmpty, Key: "signin", Title: t.signInTitle, Detail: t.signInDetail, Icon: wire.NavIconPerson}
	}
	return wire.Component{Type: wire.ComponentText, Key: "signin", Text: t.signInTitle + "\n" + t.signInDetail}
}

// location is the user's time zone as the app says it, else the connector's.
func location(r *ext.UIRequest, fallback *time.Location) *time.Location {
	if r.TZ != "" {
		if l, err := time.LoadLocation(r.TZ); err == nil {
			return l
		}
	}
	return fallback
}

func name(display, username string) string {
	if display == "" {
		return "@" + username
	}
	return display + " @" + username
}

// chunks cuts s into pieces of at most n characters, at a line break when there is one in the second half.
func chunks(s string, n int) []string {
	var out []string
	r := []rune(s)
	for len(r) > n {
		cut := n
		for i := n; i > n/2; i-- {
			if r[i-1] == '\n' {
				cut = i
				break
			}
		}
		if piece := strings.TrimRight(string(r[:cut]), "\n"); strings.TrimSpace(piece) != "" {
			out = append(out, piece)
		}
		r = r[cut:]
	}
	if rest := strings.TrimSpace(string(r)); rest != "" {
		out = append(out, rest)
	}
	return out
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
