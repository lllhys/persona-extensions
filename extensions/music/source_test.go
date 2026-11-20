package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// fakeCloud is a plausible second service: catalog search, recent and playlists, no player and no writes.
type fakeCloud struct {
	id, name string
	searches int
}

func (f *fakeCloud) ID() string   { return f.id }
func (f *fakeCloud) Name() string { return f.name }
func (f *fakeCloud) Status(context.Context, string) providerStatus {
	return providerStatus{State: "ok", Account: f.name}
}
func (f *fakeCloud) OpenHosts() []string { return []string{"example.com"} }
func (f *fakeCloud) Search(context.Context, searchRequest) ([]item, error) {
	f.searches++
	return []item{{Provider: f.id, ID: "s1", Kind: "song", Title: "Cloud Song", Artist: "Cloud Artist", URL: "https://example.com/song"}}, nil
}
func (f *fakeCloud) Recent(context.Context, int) ([]item, error) {
	return []item{{Provider: f.id, ID: "s1", Kind: "song", Title: "Cloud Recent", Artist: "Cloud Artist"}}, nil
}
func (f *fakeCloud) Playlists(context.Context, int) ([]playlist, error) {
	return []playlist{{item: item{Provider: f.id, ID: "cp1", Kind: "playlist", Title: "Cloud List"}, TrackCount: 1}}, nil
}

// fakeNoSearch is a service with recent plays but no search: its tab must not offer a search box.
type fakeNoSearch struct{ id, name string }

func (f *fakeNoSearch) ID() string   { return f.id }
func (f *fakeNoSearch) Name() string { return f.name }
func (f *fakeNoSearch) Status(context.Context, string) providerStatus {
	return providerStatus{State: "ok", Account: f.name}
}
func (f *fakeNoSearch) OpenHosts() []string { return nil }
func (f *fakeNoSearch) Recent(context.Context, int) ([]item, error) {
	return []item{{Provider: f.id, ID: "q1", Kind: "song", Title: "Quiet Song"}}, nil
}

// appWithSources is an Apple Music source (a fake Mac) and a second cloud source.
func appWithSources(t *testing.T) (*app, *fakeCloud) {
	t.Helper()
	a := newApp()
	a.logf = t.Logf

	apple := newAppleMusicSource()
	if err := apple.add(newMacProvider("m1", macFixture())); err != nil {
		t.Fatal(err)
	}
	a.addSource(apple)

	cloud := &fakeCloud{id: "web", name: "Spotify"}
	spotify := newSource("spotify", "Spotify", "Spotify", "Spotify")
	if err := spotify.add(cloud); err != nil {
		t.Fatal(err)
	}
	a.addSource(spotify)
	return a, cloud
}

// A second connector is its own status, tool namespace and call target; it is not an endpoint of Apple Music.
func TestSecondSourceIsItsOwnConnector(t *testing.T) {
	a, cloud := appWithSources(t)
	ctx := context.Background()

	toolsForSource := func(contribution string) []string {
		t.Helper()
		res, err := a.connector(ctx, "tools.list", json.RawMessage(`{"contribution":"`+contribution+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		var out struct{ Tools []tool }
		b, _ := json.Marshal(res)
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(out.Tools))
		for _, tool := range out.Tools {
			names = append(names, tool.Name)
		}
		return names
	}

	apple := toolsForSource(appleMusicID)
	if !slices.Contains(apple, "applemusic_mac_search") || slices.Contains(apple, "spotify_web_search") {
		t.Fatalf("apple tools %v", apple)
	}
	spotify := toolsForSource("spotify")
	if !slices.Contains(spotify, "spotify_web_search") || !slices.Contains(spotify, "spotify_web_recent") {
		t.Fatalf("spotify tools %v", spotify)
	}
	for _, name := range spotify {
		if strings.HasPrefix(name, appleMusicID+"_") {
			t.Fatalf("a spotify tool is prefixed as Apple Music: %v", spotify)
		}
	}

	// The status is the connector's own, not one Apple Music account for everything.
	for contribution, want := range map[string]string{appleMusicID: "Apple Music", "spotify": "Spotify"} {
		res, err := a.connector(ctx, "status", json.RawMessage(`{"contribution":"`+contribution+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(res)
		var st providerStatus
		if err := json.Unmarshal(b, &st); err != nil {
			t.Fatal(err)
		}
		if st.State != "ok" || !strings.Contains(st.Account, want) {
			t.Fatalf("%s status %+v", contribution, st)
		}
	}

	// A call runs on the provider of the connector it names.
	res, err := a.connector(ctx, "tools.call", json.RawMessage(`{"contribution":"spotify","name":"spotify_web_search","args":{"query":"cloud"}}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), "Cloud Song") {
		t.Fatalf("spotify search %s", b)
	}
	if cloud.searches != 1 {
		t.Fatalf("the spotify provider was searched %d times", cloud.searches)
	}

	if _, err := a.connector(ctx, "status", json.RawMessage(`{"contribution":"unknown"}`)); err == nil {
		t.Fatal("an unknown connector answered")
	}
}

// The page's source switch really filters: Spotify's tab has no Apple Music rows, and its search runs on Spotify.
func TestSecondSourcePageTabAndSearch(t *testing.T) {
	a, cloud := appWithSources(t)
	v := draw(t, a, nil)
	says(t, v, "<all:All sources> <applemusic:Apple Music> <spotify:Spotify>", "Midnight City", "Cloud Recent")

	v = tap(t, a, v, "source", map[string]string{"source": "spotify"})
	says(t, v, "[segmented] spotify", "Cloud Recent", "Cloud List")
	saysNot(t, v, "Midnight City", "Mac Focus", "## Mac")

	v = tap(t, a, v, "search", map[string]string{"q": "cloud"})
	says(t, v, "Results for", "Cloud Song")
	if cloud.searches != 1 {
		t.Fatalf("the spotify tab searched %d times", cloud.searches)
	}

	v = tap(t, a, v, "source", map[string]string{"source": appleMusicID})
	says(t, v, "Midnight City", "Mac Focus")
	saysNot(t, v, "Cloud Recent")
}

// The search box belongs to the source on screen: the search action stays inside the tab it was typed on, so a tab
// whose endpoints cannot search must not offer a box that can only fail.
func TestSearchBoxFollowsTheShownSource(t *testing.T) {
	a, _ := appWithSources(t)
	quiet := newSource("quiet", "Quiet", "Quiet", "Quiet")
	if err := quiet.add(&fakeNoSearch{id: "web", name: "Quiet"}); err != nil {
		t.Fatal(err)
	}
	a.addSource(quiet)

	v := draw(t, a, nil)
	if v.Search == nil {
		t.Fatal("All sources lost its search box")
	}
	v = tap(t, a, v, "source", map[string]string{"source": "quiet"})
	says(t, v, "Quiet Song")
	if v.Search != nil {
		t.Fatalf("the quiet tab offers a search box: %+v", v.Search)
	}
	r := req(6, nil)
	r.State, r.Action, r.Values = v.State, "search", map[string]string{"q": "song"}
	if res, err := a.action(context.Background(), r); err == nil || res != nil {
		t.Fatalf("searching the quiet tab returned %+v, %v", res, err)
	}
	v = tap(t, a, v, "source", map[string]string{"source": appleMusicID})
	if v.Search == nil {
		t.Fatal("the Apple Music tab lost its search box")
	}
}

// Turning Apple Music off leaves the other connector's endpoints and tools alone.
func TestOneConnectorOffKeepsTheOthers(t *testing.T) {
	a, _ := appWithSources(t)
	a.enabled = map[string]bool{"spotify": true}
	v := draw(t, a, nil)
	says(t, v, "Cloud Recent")
	saysNot(t, v, "Midnight City")
	if _, err := a.connector(context.Background(), "status", json.RawMessage(`{"contribution":"applemusic"}`)); err == nil {
		t.Fatal("the off connector answered")
	}
	res, err := a.connector(context.Background(), "status", json.RawMessage(`{"contribution":"spotify"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), "Spotify") {
		t.Fatalf("spotify status %s", b)
	}
}

// A service whose label does not fit the in-page segmented control moves the switch to the page's tabs, so the view
// still passes the wire check instead of failing it.
func TestLongSourceLabelUsesTabs(t *testing.T) {
	a, _ := appWithSources(t)
	long := newSource("netease", "NetEase Cloud Music", "NetEase Cloud Music", "NetEase")
	if err := long.add(&fakeCloud{id: "web", name: "NetEase"}); err != nil {
		t.Fatal(err)
	}
	a.addSource(long)
	v := draw(t, a, nil)
	if v.Tabs == nil {
		t.Fatalf("a long label did not move the switch to tabs:\n%s", viewText(v))
	}
	if v.Tabs.Name != "source" || len(v.Tabs.Items) != 4 || v.Tabs.Items[3].Title != "NetEase Cloud Music" {
		t.Fatalf("tabs %+v", v.Tabs)
	}
	saysNot(t, v, "[segmented]")
}

// With more than eight services the switch becomes a filter, which has no short label limit and keeps the full name.
func TestManySourcesUseFilter(t *testing.T) {
	a, _ := appWithSources(t)
	for i := 0; i < 8; i++ {
		id := "svc" + string(rune('a'+i))
		s := newSource(id, "Service "+strings.ToUpper(id), "S"+strings.ToUpper(id), "S")
		if err := s.add(&fakeCloud{id: "web", name: "Service"}); err != nil {
			t.Fatal(err)
		}
		a.addSource(s)
	}
	long := strings.Repeat("Long Service ", 5) // 60 characters, past the tab limit
	s := newSource("longsvc", long, long, "Long")
	if err := s.add(&fakeCloud{id: "web", name: "Long"}); err != nil {
		t.Fatal(err)
	}
	a.addSource(s)
	v := draw(t, a, nil)
	if v.Filter == nil || v.Filter.Name != "source" || len(v.Filter.Choices) != 12 {
		t.Fatalf("filter %+v tabs %+v", v.Filter, v.Tabs)
	}
	full := false
	for _, c := range v.Filter.Choices {
		if c.Title == long {
			full = true
		}
	}
	if !full {
		t.Fatalf("the filter lost the full source name: %+v", v.Filter.Choices)
	}
}

// A service name longer than the tab schema allows is clipped, so a new service cannot make the whole page fail the
// view check; draw() validates the view.
func TestVeryLongSourceNameFitsTabs(t *testing.T) {
	a, _ := appWithSources(t)
	name := strings.Repeat("A very long service name ", 3) // 75 characters
	s := newSource("longsvc", name, name, "Long")
	if err := s.add(&fakeCloud{id: "web", name: "Long"}); err != nil {
		t.Fatal(err)
	}
	a.addSource(s)
	v := draw(t, a, nil)
	if v.Tabs == nil || len(v.Tabs.Items) != 4 {
		t.Fatalf("tabs %+v", v.Tabs)
	}
	for _, tab := range v.Tabs.Items {
		if utf8.RuneCountInString(tab.Title) > tabTitleMost {
			t.Fatalf("tab %q is longer than %d characters", tab.Title, tabTitleMost)
		}
	}
}

// The player opened from a service's own tab stays on that service: an endpoint left over from another page (state
// an app can restore) must not decide what is playing.
func TestPlayerFromSourceTabKeepsTheSource(t *testing.T) {
	a, _ := appWithSources(t)
	st := pageState{Source: "spotify", Provider: "mac", EndpointSource: appleMusicID}
	v, err := a.render(context.Background(), req(6, &st))
	if err != nil {
		t.Fatal(err)
	}
	checked(t, v)
	v = tap(t, a, v, "player", nil)
	saysNot(t, v, "Midnight City")
	says(t, v, "Nothing is playing.")
	if got := stateOf(t, v.State); got.Source != "spotify" || got.Provider != "" || got.EndpointSource != "spotify" {
		t.Fatalf("state %+v", got)
	}
}

// Switching the source drops the endpoint of the source that was open.
func TestSourceSwitchClearsTheEndpoint(t *testing.T) {
	a, _ := appWithSources(t)
	st := pageState{Source: appleMusicID, Provider: "mac", EndpointSource: appleMusicID}
	v, err := a.render(context.Background(), req(6, &st))
	if err != nil {
		t.Fatal(err)
	}
	v = tap(t, a, v, "source", map[string]string{"source": "spotify"})
	if got := stateOf(t, v.State); got.Source != "spotify" || got.Provider != "" || got.EndpointSource != "" {
		t.Fatalf("state %+v", got)
	}
}

// Two services may both call an endpoint "web": a pushed page from All sources resolves it by the source it recorded,
// not by the first source that has that id.
func TestDuplicateEndpointIDsResolveBySource(t *testing.T) {
	a, _ := appWithSources(t)
	a.addSource(func() *source {
		s := newSource("netease", "NetEase", "NetEase", "NetEase")
		if err := s.add(&fakeCloud{id: "web", name: "NetEase"}); err != nil {
			t.Fatal(err)
		}
		return s
	}())
	got := a.pageEndpoint(pageState{Source: allSource, Provider: "web", EndpointSource: "netease"})
	if got == nil || got.src.id != "netease" || got.p.Name() != "NetEase" {
		t.Fatalf("resolved %+v", got)
	}
	// Without the recorded source, All sources keeps its old fallback.
	if got := a.pageEndpoint(pageState{Source: allSource, Provider: "web"}); got == nil || got.src.id != "spotify" {
		t.Fatalf("fallback %+v", got)
	}
}

// A write that happened but whose follow-up failed stays a receipt: the same request_id answers the same failure and
// never creates a second playlist, and a replay never turns that failure into success.
func TestWriteStoreKeepsAWrittenFollowUpFailure(t *testing.T) {
	s := newWriteStore("")
	runs := 0
	text, err := s.commit("id", "k", func() (string, error) {
		runs++
		return "", &writtenError{text: "Created the playlist, but playing it failed.", err: errors.New("playing it failed")}
	})
	if err == nil || text != "" {
		t.Fatalf("commit %q %v", text, err)
	}
	if got := s.entries["id"]; got.Status != "done" || !got.Failed || !strings.Contains(got.Text, "Created the playlist") {
		t.Fatalf("receipt %+v", got)
	}
	for i := 0; i < 2; i++ {
		text, err = s.commit("id", "k", func() (string, error) {
			runs++
			return "second create", nil
		})
		var written *writtenError
		if text != "" || !errors.As(err, &written) || !strings.Contains(err.Error(), "Created the playlist") {
			t.Fatalf("replay %d: %q %v", i, text, err)
		}
	}
	if runs != 1 {
		t.Fatalf("the write ran %d times", runs)
	}
}
