package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func req(version int, st *pageState) *ext.UIRequest {
	r := &ext.UIRequest{}
	r.Contribution, r.Placement, r.Size, r.UIVersion, r.TZ, r.Locale = navUI, wire.PlacementNavPage, wire.SizeFull, version, "Asia/Shanghai", "en"
	if st != nil {
		r.State = ext.EncodeState(*st)
	}
	return r
}

func checked(t *testing.T, v *wire.View) *wire.View {
	t.Helper()
	if err := wire.CheckView(v); err != nil {
		t.Fatalf("CheckView: %v\n%s", err, viewText(v))
	}
	raw, _ := json.Marshal(v)
	if err := exttest.CheckView(raw, false); err != nil {
		t.Fatalf("exttest.CheckView: %v\n%s", err, viewText(v))
	}
	return v
}

func draw(t *testing.T, a *app, st *pageState) *wire.View {
	t.Helper()
	v, err := a.render(context.Background(), req(6, st))
	if err != nil {
		t.Fatal(err)
	}
	return checked(t, v)
}

func tap(t *testing.T, a *app, v *wire.View, id string, values map[string]string) *wire.View {
	t.Helper()
	r := req(6, nil)
	r.State, r.Action, r.Values = v.State, id, values
	res, err := a.action(context.Background(), r)
	if err != nil || res == nil || res.View == nil {
		t.Fatalf("action %s: %+v %v", id, res, err)
	}
	return checked(t, res.View)
}

func viewText(v *wire.View) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", v.Title)
	if v.Tabs != nil {
		fmt.Fprintf(&b, "tabs %s=%s:", v.Tabs.Name, v.Tabs.Value)
		for _, tab := range v.Tabs.Items {
			fmt.Fprintf(&b, " <%s:%s>", tab.Value, tab.Title)
		}
		b.WriteString("\n")
	}
	if v.Search != nil {
		fmt.Fprintf(&b, "search %s\n", v.Search.Placeholder)
	}
	for _, s := range v.Sections {
		if s.Title != "" {
			fmt.Fprintf(&b, "## %s\n", s.Title)
		}
		if s.Footer != "" {
			fmt.Fprintf(&b, "footer %s\n", s.Footer)
		}
		for _, c := range s.Items {
			fmt.Fprintf(&b, "[%s]", c.Type)
			for _, part := range []string{c.Title, c.Subtitle, c.Text, c.Value, c.Label, c.Detail, c.URL} {
				if part != "" {
					b.WriteString(" " + part)
				}
			}
			for _, ch := range c.Choices {
				fmt.Fprintf(&b, " <%s:%s>", ch.Value, ch.Title)
			}
			if c.Badge != nil {
				fmt.Fprintf(&b, " {%s}", c.Badge.Text)
			}
			if c.Ratio != nil {
				fmt.Fprintf(&b, " %.2f", *c.Ratio)
			}
			for _, f := range c.Facts {
				fmt.Fprintf(&b, " {%s=%s}", f.Label, f.Value)
			}
			if c.Thumbnail != nil {
				fmt.Fprintf(&b, " (thumb %s%s)", c.Thumbnail.URL, c.Thumbnail.Ref)
			}
			if c.Src != nil {
				fmt.Fprintf(&b, " (src %s%s)", c.Src.URL, c.Src.Ref)
			}
			for _, t := range c.Tiles {
				image := ""
				if t.Image != nil {
					image = t.Image.URL + t.Image.Ref
				}
				fmt.Fprintf(&b, " (tile %s | %s | %s)", t.Title, t.Subtitle, image)
			}
			if c.Action != nil {
				fmt.Fprintf(&b, " →%s", c.Action.ID)
				if c.Action.Push != nil {
					b.WriteString(" push")
				}
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

func says(t *testing.T, v *wire.View, parts ...string) {
	t.Helper()
	all := viewText(v)
	for _, p := range parts {
		if !strings.Contains(all, p) {
			t.Errorf("the page lacks %q:\n%s", p, all)
		}
	}
}

func saysNot(t *testing.T, v *wire.View, parts ...string) {
	t.Helper()
	all := viewText(v)
	for _, p := range parts {
		if strings.Contains(all, p) {
			t.Errorf("the page has %q:\n%s", p, all)
		}
	}
}

func stateOf(t *testing.T, state string) pageState {
	t.Helper()
	var st pageState
	if state != "" {
		if err := json.Unmarshal([]byte(state), &st); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func toolNamesFor(t *testing.T, tools []tool) []string {
	t.Helper()
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Name)
	}
	return out
}

func TestMacPage(t *testing.T) {
	a := appWith(t, newMacProvider("m1", macFixture()))
	v := draw(t, a, nil)
	says(t, v,
		"# Music", "[segmented] all <all:All sources> <applemusic:Apple Music>",
		"[header] All sources Mac", "## Now playing", "Midnight City", "4:03",
		"## Recently played", "[carousel]", "## Your playlists", "Mac Focus",
		"## Library", "Refresh", "search Search music")
	saysNot(t, v, "[progress]")
	if !strings.Contains(viewText(v), "mac:track:m1") {
		t.Fatalf("the Mac page has no artwork ref:\n%s", viewText(v))
	}
	if st := stateOf(t, v.State); st.Source != allSource {
		t.Fatalf("state %+v", st)
	}
	if v.RefreshAfter != 60 {
		t.Fatalf("the page refreshes every 60 s, not %d", v.RefreshAfter)
	}
	saysNot(t, v, "## Sources", "Connect this source")
}

func TestIPhonePage(t *testing.T) {
	p := newIPhoneProvider(openSnapshotStore(t.TempDir()))
	if _, err := p.Sync(context.Background(), json.RawMessage(`{
		"now_playing":{"title":"Midnight City","artist":"M83","state":"playing","artwork_url":"https://is1-ssl.mzstatic.com/image/thumb/cover.jpg"},
		"recent":[{"id":"r1","title":"Song","artist":"Artist","artwork_url":"https://is1-ssl.mzstatic.com/image/thumb/song.jpg"}],
		"playlists":[{"id":"p1","title":"List","track_count":2}],
		"stats":{"tracks":10,"playlists":1}
	}`)); err != nil {
		t.Fatal(err)
	}
	a := appWith(t, p)
	v := draw(t, a, nil)
	says(t, v, "# Music", "[header] All sources iPhone", "Last synced", "## Now playing", "Midnight City",
		"## Recently played", "Song", "## Your playlists", "List", "## Library",
		"https://is1-ssl.mzstatic.com/image/thumb/cover.jpg")
	saysNot(t, v, "search Search music", "## Sources")
}

func TestIPhoneArtworkFromAnotherHostIsDropped(t *testing.T) {
	p := newIPhoneProvider(openSnapshotStore(t.TempDir()))
	if _, err := p.Sync(context.Background(), json.RawMessage(`{
		"now_playing":{"title":"Song","artwork_url":"https://example.test/cover.jpg"}
	}`)); err != nil {
		t.Fatal(err)
	}
	v := draw(t, appWith(t, p), nil)
	saysNot(t, v, "example.test")
}

func TestBothEndpointsMergeUnderOneSource(t *testing.T) {
	p := newIPhoneProvider(openSnapshotStore(t.TempDir()))
	if _, err := p.Sync(context.Background(), json.RawMessage(`{
		"recent":[{"id":"r2","title":"Phone Song","artist":"Phone Artist","artwork_url":"https://is1-ssl.mzstatic.com/x.jpg"}],
		"playlists":[{"id":"p1","title":"Phone List","track_count":3}],
		"stats":{"tracks":10}
	}`)); err != nil {
		t.Fatal(err)
	}
	a := appWith(t, newMacProvider("m1", macFixture()), p)
	v := draw(t, a, nil)
	says(t, v, "[segmented] all <all:All sources> <applemusic:Apple Music>",
		"[header] All sources Mac + iPhone", "Last synced", "Midnight City", "{Mac}",
		"Phone Song", "Phone Artist · iPhone", "Mac Focus", "Phone List", "## Mac", "## iPhone")
	v = tap(t, a, v, "source", map[string]string{"source": appleMusicID})
	says(t, v, "[segmented] applemusic", "Mac + iPhone", "Phone Song")
	v = tap(t, a, v, "source", map[string]string{"source": "nope"})
	says(t, v, "[segmented] all")
}

func TestFallbackWhenNothingConfigured(t *testing.T) {
	a := appWith(t)
	v := draw(t, a, nil)
	says(t, v, "# Music", "Connect Apple Music", "Choose a Mac")
	if st := stateOf(t, v.State); st.Source != "" || st.Provider != "" {
		t.Fatalf("state %+v", st)
	}
}

// The first-run card speaks the request's language too, not only the rest of the page.
func TestConnectGuideFollowsTheLocale(t *testing.T) {
	a := appWith(t)
	r := req(6, nil)
	r.Locale = "zh-Hans"
	v, err := a.render(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	checked(t, v)
	says(t, v, "在「设置 › 扩展 › 音乐」里完成配置。", "选择一台 Mac")
	saysNot(t, v, "Open Settings")
}

func TestConnectorOffPage(t *testing.T) {
	a := newApp()
	a.logf = t.Logf
	a.enabled = map[string]bool{} // every connector is off
	v := draw(t, a, nil)
	says(t, v, "Music is off", "Turn on a music connector")
}

func TestConnectorActive(t *testing.T) {
	if got := activeConnectors(nil); got != nil {
		t.Fatalf("a nil init must mean an older hub: %v", got)
	}
	in := &ext.Init{InitializeParams: wire.InitializeParams{Active: map[string][]string{}}}
	if activeConnectors(in)[appleMusicID] {
		t.Fatal("an active map without the connector means it is off")
	}
	in.Active["ui"] = []string{navUI}
	if activeConnectors(in)[appleMusicID] {
		t.Fatal("only the ui contribution is on")
	}
	in.Active["connectors"] = []string{appleMusicID}
	if !activeConnectors(in)[appleMusicID] {
		t.Fatal("the connector is on")
	}
}

func TestMacPlaylistPageAndBack(t *testing.T) {
	a := appWith(t, newMacProvider("m1", macFixture()))
	v := draw(t, a, nil)
	v = tap(t, a, v, "playlist", map[string]string{"id": "p.mac", "provider": "mac"})
	says(t, v, "# Mac Focus", "## Tracks", "Midnight City", "Back")
	res, err := a.action(context.Background(), func() *ext.UIRequest {
		r := req(6, nil)
		r.State, r.Action = v.State, "back"
		return r
	}())
	if err != nil || res == nil || !res.Pop {
		t.Fatalf("back from a pushed playlist: %+v %v", res, err)
	}
}

func TestMacSearchAndControl(t *testing.T) {
	a := appWith(t, newMacProvider("m1", macFixture()))
	v := draw(t, a, nil)
	v = tap(t, a, v, "search", map[string]string{"q": "midnight"})
	says(t, v, "Results for", "Midnight City", "Back")
	v = tap(t, a, v, "back", nil)
	says(t, v, "## Recently played")
	res, err := a.action(context.Background(), func() *ext.UIRequest {
		r := req(6, nil)
		r.State, r.Action, r.Values = v.State, "control", map[string]string{"do": "playpause"}
		return r
	}())
	if err != nil || res == nil || res.View == nil {
		t.Fatalf("control: %+v %v", res, err)
	}
	checked(t, res.View)
}

// restrictedMac is an endpoint that declares only some of its control actions, the way a service with a subset would.
type restrictedMac struct {
	*macProvider
	actions []string
}

func (r restrictedMac) ControlActions() []string { return r.actions }

func rowByKey(t *testing.T, v *wire.View, key string) wire.Component {
	t.Helper()
	for _, s := range v.Sections {
		for _, c := range s.Items {
			if c.Key == key {
				return c
			}
		}
	}
	t.Fatalf("no %q row in\n%s", key, viewText(v))
	return wire.Component{}
}

func rowActions(c wire.Component) []string {
	out := make([]string, 0, len(c.Actions))
	for _, a := range c.Actions {
		out = append(out, a.Action.Values["do"])
	}
	return out
}

// The page offers only the controls an endpoint declares, in the now-playing row and on the player page.
func TestPageControlsFollowControlActions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions []string
		want    []string
	}{
		{"every action", []string{"play", "pause", "playpause", "next", "previous", "stop", "play_track", "play_playlist"}, []string{"playpause", "previous", "next"}},
		{"playpause only", []string{"playpause"}, []string{"playpause"}},
		{"next only", []string{"next"}, []string{"next"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := appWith(t, restrictedMac{macProvider: newMacProvider("m1", macFixture()), actions: tc.actions})
			v := draw(t, a, nil)
			if got := rowActions(rowByKey(t, v, "now-playing")); !slices.Equal(got, tc.want) {
				t.Fatalf("now-playing actions %v, want %v", got, tc.want)
			}
			pv := tap(t, a, v, "player", map[string]string{"source": appleMusicID, "provider": macID})
			controls := rowByKey(t, pv, "player-controls")
			got := rowActions(controls)
			if controls.Action != nil {
				got = append([]string{controls.Action.Values["do"]}, got...)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("player controls %v, want %v", got, tc.want)
			}
		})
	}
}

// A declared control still runs; an undeclared one is refused before it reaches the endpoint, from the page and from
// the control tool.
func TestControlHandlersRefuseUndeclaredActions(t *testing.T) {
	fn := macFixture()
	a := appWith(t, restrictedMac{macProvider: newMacProvider("m1", fn), actions: []string{"playpause"}})
	run := func(do string) error {
		t.Helper()
		r := req(6, nil)
		r.State, r.Action, r.Values = ext.EncodeState(pageState{Source: appleMusicID}), "control",
			map[string]string{"do": do, "id": "m1", "source": appleMusicID, "provider": macID}
		_, err := a.action(context.Background(), r)
		return err
	}
	if err := run("playpause"); err != nil {
		t.Fatalf("playpause: %v", err)
	}
	if err := run("next"); err == nil {
		t.Fatal("the page ran an undeclared action")
	}
	if err := run("play_track"); err == nil {
		t.Fatal("the page ran play_track, which this endpoint does not declare")
	}
	if _, err := a.connector(context.Background(), "tools.call",
		json.RawMessage(`{"contribution":"applemusic","name":"applemusic_mac_control","args":{"action":"next"}}`)); err == nil {
		t.Fatal("the control tool ran an undeclared action")
	}
	if fn.count("control") != 1 {
		t.Fatalf("control reached the endpoint %d times", fn.count("control"))
	}
}

// A track offers play only when the endpoint declares play_track.
func TestTrackActionNeedsDeclaredPlayTrack(t *testing.T) {
	a := appWith(t, restrictedMac{macProvider: newMacProvider("m1", macFixture()), actions: []string{"playpause"}})
	e := sourceEndpoints(a.sources[appleMusicID])[0]
	if act := a.trackAction(e, item{ID: "m1", Kind: "song"}); act != nil {
		t.Fatalf("a play_track action on an endpoint without it: %+v", act)
	}
	b := appWith(t, newMacProvider("m1", macFixture()))
	e = sourceEndpoints(b.sources[appleMusicID])[0]
	act := b.trackAction(e, item{ID: "m1", Kind: "song"})
	if act == nil || act.Values["do"] != "play_track" || act.Values["id"] != "m1" {
		t.Fatalf("track action %+v", act)
	}
}

func TestMacImageRef(t *testing.T) {
	a := appWith(t, newMacProvider("m1", macFixture()))
	img, err := a.image(context.Background(), &ext.ImageRequest{UIImageParams: wire.UIImageParams{Ref: "applemusic:mac:track:m1", Width: 320}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wire.CheckImage(img); err != nil {
		t.Fatalf("CheckImage: %v", err)
	}
	if _, err := a.image(context.Background(), &ext.ImageRequest{UIImageParams: wire.UIImageParams{Ref: "other:track:m1"}}); err == nil {
		t.Fatal("an unknown ref answered")
	}
}

// A connector the user switched off must not answer, not even for a picture an old page still references.
func TestDisabledSourceServesNoArtwork(t *testing.T) {
	a := appWith(t, newMacProvider("m1", macFixture()))
	a.enabled = map[string]bool{}
	if _, err := a.image(context.Background(), &ext.ImageRequest{
		UIImageParams: wire.UIImageParams{Ref: "applemusic:mac:track:m1", Width: 320},
	}); err == nil {
		t.Fatal("a disabled source served artwork")
	}
}

func TestIPhoneEmptyPageExplainsTheSync(t *testing.T) {
	a := appWith(t, newIPhoneProvider(openSnapshotStore(t.TempDir())))
	v := draw(t, a, nil)
	says(t, v, "[header] All sources iPhone", "Open Persona on your iPhone", "## Recently played", "Nothing played recently.")
	if got := toolNamesFor(t, toolsFor(a.sources[appleMusicID], a.sources[appleMusicID].providers[iphoneID])); !slices.Contains(got, "iphone_sync") {
		t.Fatalf("tools %v", got)
	}
}

func TestMacPlayerPageShowsLyrics(t *testing.T) {
	a := appWith(t, newMacProvider("m1", macFixture()))
	v := draw(t, a, nil)
	v = tap(t, a, v, "player", nil)
	says(t, v, "# Midnight City", "[image]", "(src applemusic:mac:track:m1)", "## Lyrics",
		"Midnight city lights", "[row] Playing 4:03", "Back")
	saysNot(t, v, "[progress]")
}

func TestIPhonePlayerPageHasNoLyrics(t *testing.T) {
	p := newIPhoneProvider(openSnapshotStore(t.TempDir()))
	if _, err := p.Sync(context.Background(), json.RawMessage(`{"now_playing":{"title":"Song","artist":"Artist","state":"playing"}}`)); err != nil {
		t.Fatal(err)
	}
	a := appWith(t, p)
	v := draw(t, a, nil)
	v = tap(t, a, v, "player", nil)
	says(t, v, "## Lyrics", "Lyrics aren't available from this device.", "Playback stays in the Music app")
}

type longProvider struct{}

func (longProvider) ID() string   { return "long" }
func (longProvider) Name() string { return "Long Music" }
func (longProvider) Status(context.Context, string) providerStatus {
	return providerStatus{State: "ok", Account: "Long Music"}
}
func (longProvider) OpenHosts() []string { return nil }
func (longProvider) Recent(context.Context, int) ([]item, error) {
	return []item{
		{Provider: "long", ID: "same", Title: strings.Repeat("A very long track title ", 4), Artist: "Artist"},
		{Provider: "long", ID: "same", Title: "Second", Artist: "Artist"},
	}, nil
}
func (longProvider) Playlists(context.Context, int) ([]playlist, error) {
	return []playlist{
		{item: item{Provider: "long", ID: "same", Kind: "playlist", Title: strings.Repeat("A very long playlist name ", 4)}, TrackCount: 2},
		{item: item{Provider: "long", ID: "same", Kind: "playlist", Title: "Second list"}, TrackCount: 1},
	}, nil
}
func (longProvider) NowPlaying(context.Context) (nowPlaying, error) {
	return nowPlaying{Title: strings.Repeat("Now ", 100), Artist: "Artist", State: "playing", DurationMS: 1000}, nil
}
func (longProvider) Stats(context.Context) (libraryStats, error) {
	return libraryStats{Tracks: 2, Playlists: 2}, nil
}

func TestLongTitlesAndDuplicateIDs(t *testing.T) {
	a := appWith(t, longProvider{})
	v := draw(t, a, nil)
	if !strings.Contains(viewText(v), "…") {
		t.Fatalf("the page did not clip a long tile title:\n%s", viewText(v))
	}
}

type bigPlaylistProvider struct{}

func (bigPlaylistProvider) ID() string   { return "big" }
func (bigPlaylistProvider) Name() string { return "Big Music" }
func (bigPlaylistProvider) Status(context.Context, string) providerStatus {
	return providerStatus{State: "ok"}
}
func (bigPlaylistProvider) OpenHosts() []string    { return nil }
func (bigPlaylistProvider) ArtworkHosts() []string { return []string{"mzstatic.com"} }
func (bigPlaylistProvider) Playlists(context.Context, int) ([]playlist, error) {
	return nil, nil
}
func (bigPlaylistProvider) Playlist(_ context.Context, id string, offset, limit int) (playlist, []item, error) {
	const total = 150
	var tracks []item
	for i := offset; i < total && i < offset+limit; i++ {
		tracks = append(tracks, item{
			Provider: "big", ID: fmt.Sprintf("t%d", i), Kind: "song", Title: fmt.Sprintf("Track %d", i),
			ArtworkURL: fmt.Sprintf("https://is1-ssl.mzstatic.com/image/thumb/t%d.jpg", i),
		})
	}
	return playlist{item: item{Provider: "big", ID: id, Kind: "playlist", Title: "Big list"}, TrackCount: total}, tracks, nil
}

// A playlist longer than the page's picture limit pages through windows of rows that each carry a cover.
func TestPlaylistPageWithManyTracks(t *testing.T) {
	a := appWith(t, bigPlaylistProvider{})
	v, err := a.render(context.Background(), req(6, &pageState{Provider: "big", Mode: "playlist", PlaylistID: "p1"}))
	if err != nil {
		t.Fatal(err)
	}
	checked(t, v)
	says(t, v, "# Big list", "## Tracks", "Track 0", "Track 49", "Showing 1–50 of 150.", "Next tracks")
	saysNot(t, v, "Track 50", "Previous tracks")
	if got := strings.Count(viewText(v), "(thumb"); got != 50 {
		t.Fatalf("the first window has %d covers, want 50:\n%s", got, viewText(v))
	}
	v = tap(t, a, v, "page_tracks", map[string]string{"offset": "100"})
	says(t, v, "Track 100", "Track 149", "Showing 101–150 of 150.", "Previous tracks")
	saysNot(t, v, "Next tracks", "Track 150")
}
