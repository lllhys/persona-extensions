package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

const (
	pageBudget        = 17 * time.Second
	tileTitleMost     = 40
	tileSubMost       = 80
	playlistPageSize  = 50
	maxPlaylistOffset = 100000
	// allSource is the tab that merges every configured service.
	allSource = "all"
)

type pageState struct {
	Source         string `json:"s,omitempty"`  // all, or one service's connector id (applemusic)
	Provider       string `json:"p,omitempty"`  // the endpoint an action or a pushed page names (mac, iphone)
	EndpointSource string `json:"es,omitempty"` // the endpoint's source when the page is All sources and ids may repeat
	Mode           string `json:"m,omitempty"`  // "", "search", "playlist" or "player"
	Query          string `json:"q,omitempty"`
	PlaylistID     string `json:"d,omitempty"`
	Offset         int    `json:"o,omitempty"` // the first track of the playlist page
}

func readState(r *ext.UIRequest) (pageState, error) {
	var st pageState
	if err := r.DecodeState(&st); err != nil || len(st.Source) > 64 || len(st.Provider) > 64 || len(st.EndpointSource) > 64 ||
		len(st.Mode) > 16 || len([]rune(st.Query)) > 200 || len(st.PlaylistID) > 200 || st.Offset < 0 || st.Offset > maxPlaylistOffset {
		return pageState{}, errors.New("invalid page state")
	}
	return st, nil
}

// endpoint is one configured endpoint (a Mac, a phone, a cloud account) and the source it belongs to.
type endpoint struct {
	src *source
	p   provider
}

// label names the endpoint on a page: the service too when more than one service is shown.
func (e endpoint) label(multiSource bool) string {
	if e.src == nil {
		return e.p.Name()
	}
	if multiSource {
		return e.src.short + " " + e.p.Name()
	}
	return e.p.Name()
}

func (a *app) render(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	st, err := readState(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, pageBudget)
	defer cancel()
	return a.page(ctx, r, st), nil
}

func (a *app) action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	st, err := readState(r)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, pageBudget)
	defer cancel()
	switch r.Action {
	case "source":
		id := strings.TrimSpace(r.Values["source"])
		if id != allSource && (a.sources[id] == nil || !a.sourceActive(id)) {
			id = allSource
		}
		st.Source = id
		st.Provider, st.Mode, st.Query, st.PlaylistID, st.Offset = "", "", "", "", 0
	case "provider":
		// An older page's endpoint switch: the new page's tabs are sources, and the endpoint is a detail of its
		// source. Keep answering it until the app has the new page.
		id := strings.TrimSpace(r.Values["source"])
		st.Provider, st.Source = "", allSource
		if id == appleMusicID {
			st.Source = appleMusicID
		}
		for _, s := range a.activeSources() {
			if _, ok := s.providers[id]; ok {
				st.Source, st.Provider = s.id, id
				break
			}
		}
		st.Mode, st.Query, st.PlaylistID, st.Offset = "", "", "", 0
	case "search":
		query := strings.TrimSpace(r.Values["q"])
		if len([]rune(query)) > 200 {
			return nil, errors.New("search is too long")
		}
		st.Query = query
		st.Offset = 0
		if query == "" {
			st.Mode = ""
			break
		}
		e := a.searchEndpoint(ctx, st)
		if e == nil {
			return nil, errors.New("no music endpoint can search")
		}
		st.Provider = e.p.ID()
		st.Mode = "search"
	case "player":
		if e := a.endpointFromValues(st, r.Values); e != nil {
			st.Provider, st.EndpointSource = e.p.ID(), e.src.id
		}
		st.Mode, st.Query, st.PlaylistID, st.Offset = "player", "", "", 0
	case "playlist":
		id := strings.TrimSpace(r.Values["id"])
		if id == "" || len(id) > 200 {
			return nil, errors.New("invalid playlist id")
		}
		e := a.endpointFromValues(st, r.Values)
		if e == nil {
			e = a.pageEndpoint(st)
		}
		if e == nil {
			return nil, errors.New("no music endpoint")
		}
		if _, ok := e.p.(playlistProvider); !ok {
			return nil, errors.New("this endpoint does not show playlists")
		}
		st.Source, st.Provider = e.src.id, e.p.ID()
		st.PlaylistID, st.Mode, st.Offset = id, "playlist", 0
	case "page_tracks":
		offset, err := strconv.Atoi(strings.TrimSpace(r.Values["offset"]))
		if err != nil || offset < 0 || offset > maxPlaylistOffset {
			return nil, errors.New("invalid track offset")
		}
		if st.Mode != "playlist" {
			return nil, errors.New("no playlist page is open")
		}
		st.Offset = offset
	case "back":
		if st.Mode == "playlist" || st.Mode == "player" {
			return &wire.ActionResult{Pop: true}, nil
		}
		st.Mode, st.Query, st.PlaylistID, st.Offset = "", "", "", 0
	case "refresh":
		st.Mode, st.Query, st.PlaylistID, st.Offset = "", "", "", 0
		for _, s := range a.activeSources() {
			for _, p := range s.list() {
				if r, ok := p.(refresher); ok {
					r.Refresh()
				}
			}
		}
	case "control":
		e := a.endpointFromValues(st, r.Values)
		if e == nil {
			e = a.pageEndpoint(st)
		}
		if e == nil {
			return nil, errors.New("no music endpoint")
		}
		cp, ok := e.p.(playbackController)
		if !ok {
			return nil, errors.New("this endpoint cannot control playback")
		}
		do := strings.TrimSpace(r.Values["do"])
		if do == "play_track" || do == "play_playlist" {
			id := strings.TrimSpace(r.Values["id"])
			if id == "" || len(id) > 200 {
				return nil, errors.New("invalid id")
			}
			do = do + ":" + id
		}
		if err := cp.Control(ctx, do); err != nil {
			return nil, err
		}
	case "open":
		e := a.endpointFromValues(st, r.Values)
		if e == nil {
			e = a.pageEndpoint(st)
		}
		if e == nil {
			return nil, errors.New("no music endpoint")
		}
		link, err := validOpenURL(r.Values["url"], e.p.OpenHosts())
		if err != nil {
			return nil, err
		}
		return &wire.ActionResult{OpenURL: link}, nil
	default:
		return nil, fmt.Errorf("unknown page action %q", r.Action)
	}
	v := a.page(ctx, r, st)
	return &wire.ActionResult{View: v}, nil
}

// pageSource is one tab: a service, or all of them, with the endpoints under it.
type pageSource struct {
	id        string
	title     string
	src       *source
	endpoints []endpoint
}

func sourceEndpoints(s *source) []endpoint {
	out := make([]endpoint, 0, len(s.order))
	for _, id := range s.order {
		if p, ok := s.providers[id]; ok {
			out = append(out, endpoint{src: s, p: p})
		}
	}
	return out
}

func (a *app) pageSource(id string, active []*source, t texts) pageSource {
	if s, ok := a.sources[id]; ok && a.sourceActive(id) {
		return pageSource{id: id, title: s.name, src: s, endpoints: sourceEndpoints(s)}
	}
	all := pageSource{id: allSource, title: t.allSources}
	for _, s := range active {
		all.endpoints = append(all.endpoints, sourceEndpoints(s)...)
	}
	return all
}

// sourceSelector is the switch between All sources and each on service. The in-page segmented control is used while
// every label fits (at most 5 choices of 12 characters); with more or longer services it becomes the page's tabs,
// then a filter, so a new service can never make the whole page fail the view check.
func (a *app) sourceSelector(v *wire.View, st pageState, t texts, active []*source) *wire.Component {
	value := st.Source
	if value != allSource && (a.sources[value] == nil || !a.sourceActive(value)) {
		value = allSource
	}
	short := []wire.Choice{{Value: allSource, Title: t.allSources}}
	full := []wire.Tab{{Value: allSource, Title: t.allSources}}
	for _, s := range active {
		short = append(short, wire.Choice{Value: s.id, Title: s.short})
		full = append(full, wire.Tab{Value: s.id, Title: s.name})
	}
	fits := len(short) <= 5
	for _, c := range short {
		if utf8.RuneCountInString(c.Title) > 12 {
			fits = false
		}
	}
	if fits {
		return &wire.Component{Type: wire.ComponentSegmented, Name: "source", Value: value, Choices: short,
			Action: &wire.Action{ID: "source"}}
	}
	if len(full) <= 8 {
		v.Tabs = &wire.Tabs{Name: "source", Value: value, Items: full, Action: wire.Action{ID: "source"}}
		return nil
	}
	choices := make([]wire.Choice, 0, len(full))
	for _, tab := range full {
		choices = append(choices, wire.Choice{Value: tab.Value, Title: tab.Title})
	}
	v.Filter = &wire.Filter{Name: "source", Title: t.sourceLabel, Choices: choices, Value: value,
		Action: wire.Action{ID: "source"}}
	return nil
}

// endpointFromValues resolves the endpoint an action names: its source first, then All sources. A page under All
// sources always sends both, because two services may have endpoints with the same id.
func (a *app) endpointFromValues(st pageState, values map[string]string) *endpoint {
	srcID := strings.TrimSpace(values["source"])
	if srcID == "" {
		srcID = st.Source
		if (srcID == "" || srcID == allSource) && st.EndpointSource != "" {
			srcID = st.EndpointSource
		}
	}
	epID := strings.TrimSpace(values["provider"])
	if epID == "" {
		epID = st.Provider
	}
	if srcID != "" && srcID != allSource {
		s, ok := a.sources[srcID]
		if !ok || !a.sourceActive(srcID) {
			return nil
		}
		if p, ok := s.providers[epID]; ok {
			return &endpoint{src: s, p: p}
		}
		return nil
	}
	for _, s := range a.activeSources() {
		if p, ok := s.providers[epID]; ok {
			return &endpoint{src: s, p: p}
		}
	}
	return nil
}

// pageEndpoint is the endpoint a pushed page names, or the first one of its source.
func (a *app) pageEndpoint(st pageState) *endpoint {
	if st.EndpointSource != "" {
		if s, ok := a.sources[st.EndpointSource]; ok && a.sourceActive(st.EndpointSource) {
			if p, ok := s.providers[st.Provider]; ok {
				return &endpoint{src: s, p: p}
			}
		}
	}
	if e := a.endpointFromValues(st, map[string]string{}); e != nil {
		return e
	}
	if st.Source != "" && st.Source != allSource {
		s, ok := a.sources[st.Source]
		if !ok || !a.sourceActive(st.Source) {
			return nil
		}
		if len(s.order) > 0 {
			return &endpoint{src: s, p: s.providers[s.order[0]]}
		}
		return nil
	}
	for _, s := range a.activeSources() {
		if len(s.order) > 0 {
			return &endpoint{src: s, p: s.providers[s.order[0]]}
		}
	}
	return nil
}

// searchEndpoint picks a ready endpoint that can search within the page's source, else the first one that can.
func (a *app) searchEndpoint(ctx context.Context, st pageState) *endpoint {
	var fallback *endpoint
	for _, s := range a.activeSources() {
		if st.Source != allSource && s.id != st.Source {
			continue
		}
		for _, p := range s.list() {
			if _, ok := p.(searchProvider); !ok {
				continue
			}
			e := &endpoint{src: s, p: p}
			if fallback == nil {
				fallback = e
			}
			if p.Status(ctx, "").State == "ok" {
				return e
			}
		}
	}
	return fallback
}

func (a *app) anySearchProvider() bool {
	for _, s := range a.activeSources() {
		for _, p := range s.list() {
			if _, ok := p.(searchProvider); ok {
				return true
			}
		}
	}
	return false
}

func (a *app) page(ctx context.Context, r *ext.UIRequest, st pageState) *wire.View {
	t := textsFor(r.Locale)
	active := a.activeSources()
	if len(active) == 0 {
		return emptyView(t.title, t.connectorOff, t.connectorOffDetail)
	}
	src := a.pageSource(st.Source, active, t)
	st.Source = src.id
	if len(src.endpoints) == 0 {
		if src.src != nil {
			return emptyView(t.title, fmt.Sprintf(t.connectNamed, src.src.name), strings.Join(src.src.guide, " "))
		}
		if len(active) == 1 { // one service, just not set up yet: name it
			return emptyView(t.title, fmt.Sprintf(t.connectNamed, active[0].name), strings.Join(active[0].guide, " "))
		}
		return emptyView(t.title, t.connectMusic, t.connectDetail)
	}
	switch st.Mode {
	case "playlist":
		return a.playlistPage(ctx, r, st, t, src)
	case "search":
		return a.searchPage(ctx, r, st, t, src)
	case "player":
		return a.playerPage(ctx, r, st, t)
	default:
		return a.listPage(ctx, r, st, src, t, active)
	}
}

// endpointView is one endpoint's read for the page: what it is, its state and the sections it can fill.
type endpointView struct {
	e            endpoint
	status       providerStatus
	now          nowPlaying
	nowErr       error
	recent       []item
	recentErr    error
	playlists    []playlist
	playlistsErr error
	stats        libraryStats
	statsErr     error
}

func (ev *endpointView) label(multiSource bool) string { return ev.e.label(multiSource) }

func (a *app) sourceData(ctx context.Context, endpoints []endpoint, locale string) []endpointView {
	out := make([]endpointView, len(endpoints))
	var wg sync.WaitGroup
	for i, e := range endpoints {
		wg.Add(1)
		go func(i int, e endpoint) {
			defer wg.Done()
			ev := &out[i]
			ev.e = e
			ev.status = e.p.Status(ctx, locale)
			if ev.status.State != "ok" {
				return
			}
			if np, ok := e.p.(nowPlayingProvider); ok {
				ev.now, ev.nowErr = np.NowPlaying(ctx)
			}
			if rp, ok := e.p.(recentProvider); ok {
				ev.recent, ev.recentErr = rp.Recent(ctx, 12)
			}
			if pp, ok := e.p.(playlistsProvider); ok {
				ev.playlists, ev.playlistsErr = pp.Playlists(ctx, 25)
			}
			if sp, ok := e.p.(statsProvider); ok {
				ev.stats, ev.statsErr = sp.Stats(ctx)
			}
		}(i, e)
	}
	wg.Wait()
	return out
}

func (a *app) listPage(ctx context.Context, r *ext.UIRequest, st pageState, src pageSource, t texts, active []*source) *wire.View {
	views := a.sourceData(ctx, src.endpoints, r.Locale)
	multiSource := multipleSources(views)
	v := &wire.View{Title: t.title, State: ext.EncodeState(st), RefreshAfter: 60}
	if a.anySearchProvider() {
		v.Search = &wire.Search{Placeholder: t.search, Action: wire.Action{ID: "search"}}
	}

	head := []wire.Component{{Type: wire.ComponentHeader, Title: src.title, Subtitle: clip(sourceSubtitle(views, t, multiSource), 500)}}
	if selector := a.sourceSelector(v, st, t, active); selector != nil {
		head = append(head, *selector)
	}
	for i := range views {
		ev := &views[i]
		if ev.status.State != "ok" {
			detail := firstNonEmpty(ev.status.Error, t.sourceNeedsAttention)
			head = append(head, textLine(ev.label(multiSource)+": "+oneLine(detail), wire.TextStyleSecondary))
		}
		for _, guide := range ev.status.Guide {
			head = append(head, textLine(guide, wire.TextStyleSecondary))
		}
	}
	if len(views) > 1 {
		for i := range views {
			if views[i].status.LastSync.IsZero() {
				continue
			}
			line := fmt.Sprintf(t.lastSynced, views[i].status.LastSync.Local().Format("2006-01-02 15:04"))
			head = append(head, textLine(views[i].label(multiSource)+" · "+line, wire.TextStyleSecondary))
		}
	}
	v.Sections = append(v.Sections, wire.Section{Items: head})

	v.Sections = append(v.Sections, a.nowSection(views, st, t, multiSource))
	if section := a.recentSection(views, t, multiSource); section != nil {
		v.Sections = append(v.Sections, *section)
	}
	if section := a.playlistsSection(views, t, multiSource); section != nil {
		v.Sections = append(v.Sections, *section)
	}
	v.Sections = append(v.Sections, a.statsSections(views, t, multiSource)...)
	v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{{
		Type: wire.ComponentButton, Title: t.refresh, Action: &wire.Action{ID: "refresh"},
	}}})
	return v
}

func (a *app) nowSection(views []endpointView, st pageState, t texts, multiSource bool) wire.Section {
	section := wire.Section{Title: t.nowPlaying}
	now := pickNow(views)
	if now == nil {
		if err := firstNowError(views); err != nil {
			section.Items = []wire.Component{errorText(t, err)}
		} else {
			section.Items = []wire.Component{{Type: wire.ComponentEmpty, Title: t.nothingPlaying}}
		}
		return section
	}
	row := wire.Component{
		Type:     wire.ComponentRow,
		Key:      "now-playing",
		Title:    clip(now.now.Title, 2000),
		Subtitle: clip(nowMeta(now.now), 2000),
		Value:    nowValue(now.now, t),
		Action: &wire.Action{ID: "player", Push: &wire.Push{
			State: ext.EncodeState(pageState{Source: st.Source, Provider: now.e.p.ID(), EndpointSource: now.e.src.id, Mode: "player"}),
			Title: clip(now.now.Title, 40),
		}},
	}
	row.Thumbnail = a.thumbnail(now.e, now.now.ArtworkURL, "track", now.now.ID)
	if len(views) > 1 {
		badge := now.e.p.Name()
		if multiSource && now.e.src != nil && now.e.src.badge != "" {
			badge = now.e.src.badge
		}
		row.Badge = &wire.Badge{Text: clip(badge, 8)}
	}
	if _, ok := now.e.p.(playbackController); ok {
		row.Actions = []wire.RowAction{
			{Title: playPauseTitle(now.now.State, t), Action: wire.Action{ID: "control", Values: map[string]string{"do": "playpause", "source": now.e.src.id, "provider": now.e.p.ID()}}},
			{Title: t.previous, Action: wire.Action{ID: "control", Values: map[string]string{"do": "previous", "source": now.e.src.id, "provider": now.e.p.ID()}}},
			{Title: t.next, Action: wire.Action{ID: "control", Values: map[string]string{"do": "next", "source": now.e.src.id, "provider": now.e.p.ID()}}},
		}
	} else if link := validOpenGently(now.now.URL, now.e.p.OpenHosts()); link != "" {
		row.Actions = []wire.RowAction{{Title: t.open, Action: wire.Action{ID: "open", Values: map[string]string{"url": link, "source": now.e.src.id, "provider": now.e.p.ID()}}}}
	}
	section.Items = []wire.Component{row}
	return section
}

func (a *app) recentSection(views []endpointView, t texts, multiSource bool) *wire.Section {
	items := mergeRecent(views, 12)
	section := wire.Section{Title: t.recentlyPlayed}
	switch {
	case len(items) > 0:
		tiles := make([]wire.Tile, 0, len(items))
		for i, it := range items {
			tiles = append(tiles, a.tile(it.e, it.it, i, len(views) > 1, multiSource))
		}
		section.Items = []wire.Component{{Type: wire.ComponentCarousel, Tiles: tiles, Width: wire.TileWidthRegular}}
	case firstRecentError(views) != nil:
		section.Items = []wire.Component{errorText(t, firstRecentError(views))}
	default:
		section.Items = []wire.Component{{Type: wire.ComponentEmpty, Title: t.noRecent}}
	}
	return &section
}

func (a *app) playlistsSection(views []endpointView, t texts, multiSource bool) *wire.Section {
	type sourced struct {
		pl playlist
		e  endpoint
	}
	var all []sourced
	// Round-robin, not one endpoint first: with several services the last ones must not vanish behind the first
	// endpoint's 25 playlists.
	for i := 0; len(all) < 25; i++ {
		progress := false
		for j := range views {
			if i >= len(views[j].playlists) {
				continue
			}
			progress = true
			all = append(all, sourced{pl: views[j].playlists[i], e: views[j].e})
			if len(all) >= 25 {
				break
			}
		}
		if !progress {
			break
		}
	}
	section := wire.Section{Title: t.playlists}
	switch {
	case len(all) > 0:
		for i, it := range all {
			section.Items = append(section.Items, a.playlistRow(it.e, it.pl, i, len(views) > 1, multiSource, t))
		}
	case firstPlaylistsError(views) != nil:
		section.Items = []wire.Component{errorText(t, firstPlaylistsError(views))}
	default:
		section.Items = []wire.Component{{Type: wire.ComponentEmpty, Title: t.noPlaylists}}
	}
	return &section
}

func (a *app) statsSections(views []endpointView, t texts, multiSource bool) []wire.Section {
	var out []wire.Section
	for i := range views {
		ev := &views[i]
		component, ok := statGrid(ev.stats, t)
		if !ok {
			continue
		}
		title := t.library
		if len(views) > 1 {
			title = ev.label(multiSource)
		}
		out = append(out, wire.Section{Title: title, Items: []wire.Component{component}})
	}
	return out
}

type sourcedItem struct {
	it item
	e  endpoint
}

// mergeRecent joins the endpoints' recent lists: each track once per source, interleaved across the sources so a
// later service is not hidden behind the first one's list.
func mergeRecent(views []endpointView, limit int) []sourcedItem {
	seen := map[string]bool{}
	out := make([]sourcedItem, 0, limit)
	for i := 0; ; i++ {
		progress := false
		for j := range views {
			if i >= len(views[j].recent) {
				continue
			}
			progress = true
			it := views[j].recent[i]
			sourceID := ""
			if views[j].e.src != nil {
				sourceID = views[j].e.src.id
			}
			key := sourceID + "|" + strings.ToLower(strings.TrimSpace(it.Title)+"|"+strings.TrimSpace(it.Artist))
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, sourcedItem{it: it, e: views[j].e})
			if len(out) >= limit {
				return out
			}
		}
		if !progress {
			return out
		}
	}
}

// pickNow picks what the page shows as now playing: a playing endpoint, else a paused one, else the first with a track.
func pickNow(views []endpointView) *endpointView {
	var paused, anyTrack *endpointView
	for i := range views {
		ev := &views[i]
		if ev.now.Title == "" {
			continue
		}
		switch ev.now.State {
		case "playing":
			return ev
		case "paused":
			if paused == nil {
				paused = ev
			}
		}
		if anyTrack == nil {
			anyTrack = ev
		}
	}
	if paused != nil {
		return paused
	}
	return anyTrack
}

func firstNowError(views []endpointView) error {
	for i := range views {
		if views[i].nowErr != nil {
			return views[i].nowErr
		}
	}
	return nil
}

func firstRecentError(views []endpointView) error {
	for i := range views {
		if views[i].recentErr != nil {
			return views[i].recentErr
		}
	}
	return nil
}

func firstPlaylistsError(views []endpointView) error {
	for i := range views {
		if views[i].playlistsErr != nil {
			return views[i].playlistsErr
		}
	}
	return nil
}

// multipleSources says whether the endpoints on this page come from more than one service.
func multipleSources(views []endpointView) bool {
	endpoints := make([]endpoint, 0, len(views))
	for i := range views {
		endpoints = append(endpoints, views[i].e)
	}
	return multipleSourceEndpoints(endpoints)
}

func multipleSourceEndpoints(endpoints []endpoint) bool {
	seen := ""
	for _, e := range endpoints {
		if e.src == nil {
			continue
		}
		if seen == "" {
			seen = e.src.id
		} else if seen != e.src.id {
			return true
		}
	}
	return false
}

func sourceSubtitle(views []endpointView, t texts, multiSource bool) string {
	names := make([]string, 0, len(views))
	for i := range views {
		names = append(names, views[i].label(multiSource))
	}
	sub := strings.Join(names, " + ")
	if len(views) == 1 && !views[0].status.LastSync.IsZero() {
		sub += " · " + fmt.Sprintf(t.lastSynced, views[0].status.LastSync.Local().Format("2006-01-02 15:04"))
	}
	return sub
}

func (a *app) searchPage(ctx context.Context, r *ext.UIRequest, st pageState, t texts, src pageSource) *wire.View {
	e := a.searchEndpoint(ctx, st)
	if e == nil {
		v := emptyView(t.title, t.noResults, "")
		v.State = ext.EncodeState(st)
		return v
	}
	v := &wire.View{Title: fmt.Sprintf(t.searchTitle, st.Query), State: ext.EncodeState(st)}
	v.Search = &wire.Search{Placeholder: t.search, Value: st.Query, Action: wire.Action{ID: "search"}}
	head := []wire.Component{{Type: wire.ComponentHeader, Title: src.title, Subtitle: e.p.Name()}}
	v.Sections = append(v.Sections, wire.Section{Items: head})
	sp := e.p.(searchProvider)
	section := wire.Section{Title: t.searchResults}
	kinds := searchKinds(e.p)
	items, err := sp.Search(ctx, searchRequest{Query: st.Query, Kind: kinds[0], Limit: 20})
	switch {
	case err != nil:
		section.Items = []wire.Component{errorText(t, err)}
	case len(items) == 0:
		section.Items = []wire.Component{{Type: wire.ComponentEmpty, Title: t.noResults}}
	default:
		for i, it := range items {
			row := wire.Component{
				Type:     wire.ComponentRow,
				Key:      fmt.Sprintf("result-%d", i),
				Title:    clip(it.Title, 2000),
				Subtitle: clip(metaLine(it), 2000),
				Value:    durationValue(it.DurationMS),
				Action:   a.trackAction(*e, it),
			}
			row.Thumbnail = a.thumbnail(*e, it.ArtworkURL, "track", it.ID)
			section.Items = append(section.Items, row)
		}
	}
	v.Sections = append(v.Sections, section, wire.Section{Items: []wire.Component{backButton(t)}})
	return v
}

func (a *app) playlistPage(ctx context.Context, r *ext.UIRequest, st pageState, t texts, src pageSource) *wire.View {
	e := a.pageEndpoint(st)
	if e == nil {
		v := emptyView(t.playlist, t.noPlaylists, "")
		v.State = ext.EncodeState(st)
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{backButton(t)}})
		return v
	}
	pp, ok := e.p.(playlistProvider)
	if !ok {
		v := emptyView(t.playlist, t.noPlaylists, "")
		v.State = ext.EncodeState(st)
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{backButton(t)}})
		return v
	}
	pl, tracks, err := pp.Playlist(ctx, st.PlaylistID, st.Offset, playlistPageSize)
	if err != nil {
		v := emptyView(t.playlist, t.sourceError, clip(oneLine(err.Error()), 500))
		v.State = ext.EncodeState(st)
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{backButton(t)}})
		return v
	}
	multiSource := multipleSourceEndpoints(src.endpoints)
	subtitle := firstNonEmpty(pl.Descr, pl.Owner)
	if len(src.endpoints) > 1 {
		subtitle = strings.TrimSpace(subtitle + " · " + e.label(multiSource))
	}
	head := []wire.Component{{
		Type:     wire.ComponentHeader,
		Title:    clip(pl.Title, 2000),
		Subtitle: clip(subtitle, 2000),
		Facts:    playlistFacts(pl, t),
	}}
	if link := validOpenGently(pl.URL, e.p.OpenHosts()); link != "" {
		head = append(head, wire.Component{Type: wire.ComponentLink, Title: t.open, URL: link})
	}
	head = append(head, backButton(t))

	section := wire.Section{Title: t.tracks}
	if len(tracks) == 0 {
		section.Items = []wire.Component{{Type: wire.ComponentEmpty, Title: t.noTracks}}
	}
	for i, track := range tracks {
		row := wire.Component{
			Type:     wire.ComponentRow,
			Key:      fmt.Sprintf("track-%d", st.Offset+i),
			Title:    clip(track.Title, 2000),
			Subtitle: clip(metaLine(track), 2000),
			Value:    durationValue(track.DurationMS),
			Action:   a.trackAction(*e, track),
		}
		// One window of at most playlistPageSize tracks keeps the covers under the page's picture limit.
		row.Thumbnail = a.thumbnail(*e, track.ArtworkURL, "track", track.ID)
		section.Items = append(section.Items, row)
	}
	from, to := st.Offset+1, st.Offset+len(tracks)
	if pl.TrackCount > 0 && to > pl.TrackCount {
		to = pl.TrackCount
	}
	if len(tracks) > 0 {
		if pl.TrackCount > 0 {
			section.Footer = fmt.Sprintf(t.showingTracks, from, to, pl.TrackCount)
		} else {
			section.Footer = fmt.Sprintf(t.showingRange, from, to)
		}
	}
	if st.Offset > 0 {
		section.Items = append(section.Items, wire.Component{
			Type: wire.ComponentButton, Key: "page-prev", Title: t.previousTracks,
			Action: &wire.Action{ID: "page_tracks", Values: map[string]string{"offset": strconv.Itoa(max(st.Offset-playlistPageSize, 0))}},
		})
	}
	if len(tracks) == playlistPageSize && (pl.TrackCount == 0 || to < pl.TrackCount) {
		section.Items = append(section.Items, wire.Component{
			Type: wire.ComponentButton, Key: "page-next", Title: t.nextTracks,
			Action: &wire.Action{ID: "page_tracks", Values: map[string]string{"offset": strconv.Itoa(st.Offset + playlistPageSize)}},
		})
	}
	title := clip(pl.Title, 2000)
	if title == "" {
		title = t.playlist
	}
	return &wire.View{Title: title, State: ext.EncodeState(st), Sections: []wire.Section{{Items: head}, section}}
}

// playerPage is the pushed now-playing page: the big picture, the controls and the lyrics. The page schema has no
// bottom bar; this is the closest the extension side can get to a player.
func (a *app) playerPage(ctx context.Context, r *ext.UIRequest, st pageState, t texts) *wire.View {
	e := a.pageEndpoint(st)
	v := &wire.View{Title: t.player, State: ext.EncodeState(st)}
	var items []wire.Component
	if e == nil {
		v.Sections = []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: t.nothingPlaying}, backButton(t)}}}
		return v
	}
	np, ok := e.p.(nowPlayingProvider)
	if !ok {
		v.Sections = []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: t.nothingPlaying}, backButton(t)}}}
		return v
	}
	now, err := np.NowPlaying(ctx)
	if err != nil {
		v.Sections = []wire.Section{{Items: []wire.Component{errorText(t, err), backButton(t)}}}
		return v
	}
	if now.Title == "" {
		v.Sections = []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: t.nothingPlaying}, backButton(t)}}}
		return v
	}
	v.Title = clip(now.Title, 200)
	v.RefreshAfter = 60
	if src := a.imageSource(*e, now.ArtworkURL, "track", now.ID); src != nil {
		items = append(items, wire.Component{
			Type: wire.ComponentImage, Key: "player-art", Src: src,
			Alt: clip(now.Title, 200), Aspect: wire.ImageAspectSquare, Fit: wire.ImageFitFit,
		})
	}
	items = append(items, wire.Component{Type: wire.ComponentText, Text: clip(now.Title, 200), Style: wire.TextStyleTitle})
	if meta := nowMeta(now); meta != "" {
		items = append(items, textLine(meta, wire.TextStyleSecondary))
	}
	if _, ok := e.p.(playbackController); ok {
		controls := wire.Component{
			Type: wire.ComponentRow, Key: "player-controls",
			Title: stateWord(now.State, t), Subtitle: nowValue(now, t),
			Action: &wire.Action{ID: "control", Values: map[string]string{"do": "playpause", "source": e.src.id, "provider": e.p.ID()}},
			Actions: []wire.RowAction{
				{Title: t.previous, Action: wire.Action{ID: "control", Values: map[string]string{"do": "previous", "source": e.src.id, "provider": e.p.ID()}}},
				{Title: t.next, Action: wire.Action{ID: "control", Values: map[string]string{"do": "next", "source": e.src.id, "provider": e.p.ID()}}},
			},
		}
		items = append(items, controls)
	} else {
		note := ""
		if np, ok := e.p.(playbackNoteProvider); ok {
			note = np.PlaybackNote(r.Locale)
		}
		if strings.TrimSpace(note) == "" {
			note = fmt.Sprintf(t.playbackElsewhere, e.p.Name())
		}
		items = append(items, textLine(note, wire.TextStyleSecondary))
	}
	items = append(items, backButton(t))
	v.Sections = append(v.Sections, wire.Section{Items: items})
	v.Sections = append(v.Sections, a.lyricsSection(ctx, e.p, now, t))
	return v
}

func (a *app) lyricsSection(ctx context.Context, p provider, now nowPlaying, t texts) wire.Section {
	section := wire.Section{Title: t.lyrics}
	lp, ok := p.(lyricsProvider)
	if !ok {
		section.Items = []wire.Component{textLine(t.lyricsUnavailable, wire.TextStyleSecondary)}
		return section
	}
	if now.ID == "" {
		section.Items = []wire.Component{textLine(t.noLyrics, wire.TextStyleSecondary)}
		return section
	}
	lyrics, err := lp.Lyrics(ctx, now.ID)
	switch {
	case err != nil:
		section.Items = []wire.Component{errorText(t, err)}
	case strings.TrimSpace(lyrics) == "":
		section.Items = []wire.Component{textLine(t.noLyrics, wire.TextStyleSecondary)}
	default:
		section.Items = []wire.Component{{Type: wire.ComponentText, Text: clip(lyrics, 8000)}}
	}
	return section
}

func (a *app) tile(e endpoint, it item, i int, withEndpoint, multiSource bool) wire.Tile {
	subtitle := metaLine(it)
	if withEndpoint {
		subtitle = strings.TrimSpace(subtitle + " · " + e.label(multiSource))
	}
	tile := wire.Tile{
		Key:      fmt.Sprintf("recent-%d", i),
		Title:    clip(it.Title, tileTitleMost),
		Subtitle: clip(subtitle, tileSubMost),
		Action:   a.trackAction(e, it),
	}
	if src := a.imageSource(e, it.ArtworkURL, "track", it.ID); src != nil {
		tile.Image = src
	}
	return tile
}

func (a *app) playlistRow(e endpoint, pl playlist, i int, withEndpoint, multiSource bool, t texts) wire.Component {
	subtitle := firstNonEmpty(pl.Descr, pl.Owner)
	if withEndpoint {
		subtitle = strings.TrimSpace(subtitle + " · " + e.label(multiSource))
	}
	row := wire.Component{
		Type:     wire.ComponentRow,
		Key:      fmt.Sprintf("playlist-%s-%s-%d", e.src.id, e.p.ID(), i),
		Title:    clip(pl.Title, 2000),
		Subtitle: clip(subtitle, 2000),
	}
	if pl.TrackCount > 0 {
		format := t.trackCount
		if pl.TrackCount == 1 {
			format = t.trackCountOne
		}
		row.Value = fmt.Sprintf(format, pl.TrackCount)
	}
	row.Thumbnail = a.thumbnail(e, pl.ArtworkURL, "playlist", pl.ID)
	if _, ok := e.p.(playlistProvider); ok && pl.ID != "" {
		row.Action = &wire.Action{ID: "playlist", Values: map[string]string{"id": pl.ID, "source": e.src.id, "provider": e.p.ID()},
			Push: &wire.Push{State: ext.EncodeState(pageState{Source: e.src.id, Provider: e.p.ID(), EndpointSource: e.src.id, Mode: "playlist", PlaylistID: pl.ID}), Title: clip(pl.Title, 40)}}
	} else if link := validOpenGently(pl.URL, e.p.OpenHosts()); link != "" {
		row.Action = &wire.Action{ID: "open", Values: map[string]string{"url": link, "source": e.src.id, "provider": e.p.ID()}}
	}
	return row
}

// imageSource is a picture for a page component: an https URL the source vouches for, else a ui.image ref the source
// serves itself.
func (a *app) imageSource(e endpoint, artworkURL, kind, id string) *wire.ImageSource {
	if link := a.imageURLFor(e.p, artworkURL); link != "" {
		return &wire.ImageSource{URL: link}
	}
	if ref := a.artworkRef(e, kind, id); ref != "" {
		return &wire.ImageSource{Ref: ref}
	}
	return nil
}

func (a *app) thumbnail(e endpoint, artworkURL, kind, id string) *wire.Thumbnail {
	src := a.imageSource(e, artworkURL, kind, id)
	if src == nil {
		return nil
	}
	if src.URL != "" {
		return &wire.Thumbnail{URL: src.URL, Aspect: wire.ThumbnailAspectSquare}
	}
	return &wire.Thumbnail{Ref: src.Ref, Aspect: wire.ThumbnailAspectSquare}
}

// imageURLFor lets a source's https artwork through only on the hosts it names: the snapshot is written from the
// assistant, so any other host could leak a page view to someone else's server.
func (a *app) imageURLFor(p provider, raw string) string {
	hp, ok := p.(artworkHostsProvider)
	if !ok {
		return ""
	}
	return imageURL(raw, hp.ArtworkHosts())
}

// artworkRef is a picture the endpoint serves through ui.image, for a source without https artwork URLs.
func (a *app) artworkRef(e endpoint, kind, id string) string {
	if e.p == nil || e.src == nil || id == "" {
		return ""
	}
	if _, ok := e.p.(artworkProvider); !ok {
		return ""
	}
	return fmt.Sprintf("%s:%s:%s:%s", e.src.id, e.p.ID(), kind, id)
}

func (a *app) trackAction(e endpoint, it item) *wire.Action {
	if e.p == nil {
		return nil
	}
	if _, ok := e.p.(playbackController); ok && it.ID != "" {
		return &wire.Action{ID: "control", Values: map[string]string{"do": "play_track", "id": it.ID, "source": e.src.id, "provider": e.p.ID()}}
	}
	if link := validOpenGently(it.URL, e.p.OpenHosts()); link != "" {
		return &wire.Action{ID: "open", Values: map[string]string{"url": link, "source": e.src.id, "provider": e.p.ID()}}
	}
	return nil
}

func statGrid(s libraryStats, t texts) (wire.Component, bool) {
	var stats []wire.Stat
	if s.Tracks > 0 {
		stats = append(stats, wire.Stat{Label: countLabel(s.Tracks, t.tracksLabel), Value: strconv.Itoa(s.Tracks)})
	}
	if s.Albums > 0 {
		stats = append(stats, wire.Stat{Label: countLabel(s.Albums, t.albumsLabel), Value: strconv.Itoa(s.Albums)})
	}
	if s.Artists > 0 {
		stats = append(stats, wire.Stat{Label: countLabel(s.Artists, t.artistsLabel), Value: strconv.Itoa(s.Artists)})
	}
	if s.Playlists > 0 {
		stats = append(stats, wire.Stat{Label: countLabel(s.Playlists, t.playlistsLabel), Value: strconv.Itoa(s.Playlists)})
	}
	if s.DurationMS > 0 {
		stats = append(stats, wire.Stat{Label: t.totalTime, Value: formatLongDuration(s.DurationMS)})
	}
	if len(stats) == 0 {
		return wire.Component{}, false
	}
	return wire.Component{Type: wire.ComponentStatGrid, Stats: stats}, true
}

func statsLine(s libraryStats, t texts) string {
	var parts []string
	if s.Tracks > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", s.Tracks, strings.ToLower(countLabel(s.Tracks, t.tracksLabel))))
	}
	if s.Albums > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", s.Albums, strings.ToLower(countLabel(s.Albums, t.albumsLabel))))
	}
	if s.Artists > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", s.Artists, strings.ToLower(countLabel(s.Artists, t.artistsLabel))))
	}
	if s.Playlists > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", s.Playlists, strings.ToLower(countLabel(s.Playlists, t.playlistsLabel))))
	}
	if s.DurationMS > 0 {
		parts = append(parts, formatLongDuration(s.DurationMS))
	}
	return strings.Join(parts, " · ")
}

func playlistFacts(pl playlist, t texts) []wire.Fact {
	var facts []wire.Fact
	if pl.TrackCount > 0 {
		facts = append(facts, wire.Fact{Label: countLabel(pl.TrackCount, t.tracks), Value: strconv.Itoa(pl.TrackCount)})
	}
	if pl.DurationMS > 0 {
		facts = append(facts, wire.Fact{Label: t.totalTime, Value: formatLongDuration(pl.DurationMS)})
	}
	return facts
}

// countLabel writes an English label in the singular for one; Chinese and Japanese labels do not end in s.
func countLabel(n int, label string) string {
	if n == 1 && strings.HasSuffix(label, "s") {
		return strings.TrimSuffix(label, "s")
	}
	return label
}

func nowMeta(now nowPlaying) string {
	var parts []string
	if now.Artist != "" {
		parts = append(parts, now.Artist)
	}
	if now.Album != "" && now.Album != now.Artist {
		parts = append(parts, now.Album)
	}
	return strings.Join(parts, " · ")
}

// nowValue is a track's length, never the position: a page redraws at best every 30 seconds, so a moving progress
// reading would be stale between draws.
func nowValue(now nowPlaying, t texts) string {
	if now.DurationMS > 0 {
		return formatDuration(now.DurationMS)
	}
	return stateWord(now.State, t)
}

func stateWord(state string, t texts) string {
	switch state {
	case "playing":
		return t.playing
	case "paused":
		return t.paused
	case "stopped":
		return t.stopped
	}
	return t.unknown
}

func playPauseTitle(state string, t texts) string {
	if state == "playing" {
		return t.pause
	}
	return t.play
}

func emptyView(title, emptyTitle, detail string) *wire.View {
	return &wire.View{Title: title, Sections: []wire.Section{{Items: []wire.Component{{
		Type: wire.ComponentEmpty, Title: emptyTitle, Detail: detail,
	}}}}}
}

func textLine(text string, style string) wire.Component {
	return wire.Component{Type: wire.ComponentText, Text: clip(text, 2000), Style: style}
}

func errorText(t texts, err error) wire.Component {
	return textLine(t.sourceError+": "+oneLine(err.Error()), wire.TextStyleSecondary)
}

func backButton(t texts) wire.Component {
	return wire.Component{Type: wire.ComponentButton, Title: t.back, Action: &wire.Action{ID: "back"}}
}

func durationValue(ms int) string {
	if ms <= 0 {
		return ""
	}
	return formatDuration(ms)
}

func metaLine(it item) string {
	var parts []string
	if it.Artist != "" {
		parts = append(parts, it.Artist)
	}
	if it.Album != "" && it.Album != it.Artist {
		parts = append(parts, it.Album)
	}
	if len(parts) == 0 {
		return it.Kind
	}
	return strings.Join(parts, " · ")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func imageURL(raw string, hosts []string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	for _, suffix := range hosts {
		suffix = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(suffix), "."))
		if suffix == "" {
			continue
		}
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return raw
		}
	}
	return ""
}

func validOpenGently(raw string, hosts []string) string {
	link, err := validOpenURL(raw, hosts)
	if err != nil {
		return ""
	}
	return link
}

func validOpenURL(raw string, hosts []string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return "", errors.New("invalid music link")
	}
	for _, host := range hosts {
		if strings.EqualFold(u.Hostname(), host) {
			return u.String(), nil
		}
	}
	return "", errors.New("that link is not from this music service")
}
