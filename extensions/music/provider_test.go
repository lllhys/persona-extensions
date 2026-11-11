package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

type fakeNode struct {
	calls []string
	// params is the last parameters sent per method, so a test can check what the node would receive.
	params map[string]any
	out    map[string]any
	err    error
}

func (f *fakeNode) Call(_ context.Context, machine, method string, in any, out any) error {
	f.calls = append(f.calls, method)
	if f.params == nil {
		f.params = map[string]any{}
	}
	f.params[method] = in
	if f.err != nil {
		return f.err
	}
	v, ok := f.out[method]
	if !ok {
		return fmt.Errorf("fake node has no %s", method)
	}
	b, _ := json.Marshal(v)
	return json.Unmarshal(b, out)
}

// controlParams is the parameters of the last control call the node received.
func (f *fakeNode) controlParams(t *testing.T) map[string]any {
	t.Helper()
	m, ok := f.params["control"].(map[string]any)
	if !ok {
		t.Fatalf("control sent %#v", f.params["control"])
	}
	return m
}

func (f *fakeNode) count(method string) int {
	n := 0
	for _, m := range f.calls {
		if m == method {
			n++
		}
	}
	return n
}

func macFixture() *fakeNode {
	return &fakeNode{out: map[string]any{
		"status":          macStatus{Running: true, Host: "macbook", Version: "1.5", Tracks: 120, Playlists: 2},
		"now":             macNow{State: "playing", ID: "m1", Title: "Midnight City", Artist: "M83", Album: "Hurry Up", PositionMS: 42000, DurationMS: 243000},
		"recent":          []macTrack{{ID: "m1", Title: "Midnight City", Artist: "M83", Album: "Hurry Up", DurationMS: 243000, PlayedAt: "2026-11-09T10:00:00"}, {ID: "m2", Title: "Outro", Artist: "M83", Album: "Hurry Up", DurationMS: 264000, PlayedAt: "2026-11-08T10:00:00"}},
		"search":          []macTrack{{ID: "m1", Title: "Midnight City", Artist: "M83", DurationMS: 243000}},
		"playlists":       []macPlaylist{{ID: "p.mac", Title: "Mac Focus", Descr: "A local playlist", TrackCount: 2}},
		"playlist":        macDetail{Playlist: macPlaylist{ID: "p.mac", Title: "Mac Focus", TrackCount: 1}, Tracks: []macTrack{{ID: "m1", Title: "Midnight City", Artist: "M83", DurationMS: 243000}}},
		"stats":           libraryStats{Tracks: 120, Playlists: 2, DurationMS: 3600000},
		"control":         "playing",
		"create_playlist": macCreate{Playlist: macPlaylist{ID: "p.new", Title: "New list", TrackCount: 1}, Added: 1},
		"artwork":         macArtwork{Data: onePixelPNG},
		"lyrics":          "Midnight city lights\nand the long drive home",
	}}
}

func TestMacProvider(t *testing.T) {
	fn := macFixture()
	p := newMacProvider("m1", fn)
	ctx := context.Background()
	st := p.Status(ctx, "")
	if st.State != "ok" || !strings.Contains(st.Account, "macbook") {
		t.Fatalf("status %+v", st)
	}
	recent, err := p.Recent(ctx, 10)
	if err != nil || len(recent) != 2 || recent[0].Title != "Midnight City" || recent[0].Provider != macID {
		t.Fatalf("recent %+v %v", recent, err)
	}
	// The second read comes from the provider's cache, not a second scan on the Mac.
	if _, err := p.Recent(ctx, 10); err != nil || fn.count("recent") != 1 {
		t.Fatalf("recent calls %v %v", fn.calls, err)
	}
	playlists, err := p.Playlists(ctx, 10)
	if err != nil || len(playlists) != 1 || playlists[0].Title != "Mac Focus" {
		t.Fatalf("playlists %+v %v", playlists, err)
	}
	pl, tracks, err := p.Playlist(ctx, "p.mac", 0, 10)
	if err != nil || pl.Title != "Mac Focus" || len(tracks) != 1 {
		t.Fatalf("playlist %+v %+v %v", pl, tracks, err)
	}
	now, err := p.NowPlaying(ctx)
	if err != nil || now.Title != "Midnight City" || now.ID != "m1" || now.State != "playing" {
		t.Fatalf("now %+v %v", now, err)
	}
	search, err := p.Search(ctx, searchRequest{Query: "midnight", Limit: 5})
	if err != nil || len(search) != 1 || search[0].Title != "Midnight City" {
		t.Fatalf("search %+v %v", search, err)
	}
	if got := p.SearchKinds(); len(got) != 1 || got[0] != "song" {
		t.Fatalf("search kinds %v", got)
	}
	if err := p.Control(ctx, "playpause"); err != nil {
		t.Fatal(err)
	}
	if got := fn.controlParams(t); len(got) != 1 || got["action"] != "playpause" {
		t.Fatalf("playpause sent %v", got)
	}
	// The two item actions travel as a bare action name plus the id: the node's whitelist only knows the bare names.
	if err := p.Control(ctx, "play_track:m1"); err != nil {
		t.Fatal(err)
	}
	if got := fn.controlParams(t); len(got) != 2 || got["action"] != "play_track" || got["id"] != "m1" {
		t.Fatalf("play_track sent %v", got)
	}
	if err := p.Control(ctx, "play_playlist:p.mac"); err != nil {
		t.Fatal(err)
	}
	if got := fn.controlParams(t); len(got) != 2 || got["action"] != "play_playlist" || got["id"] != "p.mac" {
		t.Fatalf("play_playlist sent %v", got)
	}
	art, err := p.Artwork(ctx, "track", "m1", 600)
	if err != nil || len(art) == 0 {
		t.Fatalf("artwork %d %v", len(art), err)
	}
	lyrics, err := p.Lyrics(ctx, "m1")
	if err != nil || !strings.Contains(lyrics, "Midnight city lights") {
		t.Fatalf("lyrics %q %v", lyrics, err)
	}
	// The words are cached by track: asking again for the same track reads the Mac once.
	if _, err := p.Lyrics(ctx, "m1"); err != nil || fn.count("lyrics") != 1 {
		t.Fatalf("lyrics calls %v %v", fn.calls, err)
	}
	created, err := p.CreatePlaylist(ctx, createPlaylistRequest{Name: "New list", TrackIDs: []string{"m1"}})
	if err != nil || created.ID != "p.new" {
		t.Fatalf("create %+v %v", created, err)
	}
	p.Refresh()
	if len(fn.calls) == 0 || fn.calls[len(fn.calls)-1] != "refresh" {
		t.Fatalf("calls %v", fn.calls)
	}
}

func TestMacProviderWithoutMachine(t *testing.T) {
	p := newMacProvider("", nil)
	if st := p.Status(context.Background(), ""); st.State != "not_configured" || len(st.Guide) == 0 {
		t.Fatalf("status %+v", st)
	}
}

func TestMacProviderOffline(t *testing.T) {
	fn := macFixture()
	fn.err = errors.New("the machine is offline")
	p := newMacProvider("m1", fn)
	st := p.Status(context.Background(), "")
	if st.State != "error" || !strings.Contains(st.Error, "offline") || len(st.Guide) == 0 {
		t.Fatalf("status %+v", st)
	}
}

type fakeRunner func(script string, args []string) (string, error)

func (f fakeRunner) Run(_ context.Context, script string, args ...string) (string, error) {
	return f(script, args)
}

func testMacNode(fn fakeRunner) *macNode {
	return &macNode{run: fn, host: "macbook", now: func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }, cache: map[string]macCache{}}
}

func TestMacNodeRecent(t *testing.T) {
	n := testMacNode(func(script string, args []string) (string, error) {
		if !strings.Contains(script, "played date") {
			t.Fatalf("unexpected script")
		}
		return strings.Join([]string{
			"a" + fieldSep + "Older" + fieldSep + "A" + fieldSep + "Al" + fieldSep + "180" + fieldSep + "2" + fieldSep + "2026-11-01T10:00:00" + fieldSep + "80",
			"b" + fieldSep + "Newer" + fieldSep + "B" + fieldSep + "Al" + fieldSep + "200" + fieldSep + "5" + fieldSep + "2026-11-09T10:00:00" + fieldSep + "20",
		}, recordSep), nil
	})
	got, err := n.recent(context.Background(), 1)
	if err != nil || len(got) != 1 || got[0].Title != "Newer" || got[0].DurationMS != 200000 {
		t.Fatalf("recent %+v %v", got, err)
	}
}

func TestMacNodePlaylistsSkipsFolders(t *testing.T) {
	n := testMacNode(func(string, []string) (string, error) {
		return strings.Join([]string{
			"p1" + fieldSep + "Focus" + fieldSep + "2" + fieldSep + "300" + fieldSep + "A test" + fieldSep + "user playlist",
			"p2" + fieldSep + "Folder" + fieldSep + "0" + fieldSep + "0" + fieldSep + "" + fieldSep + "folder playlist",
		}, recordSep), nil
	})
	got, err := n.playlists(context.Background())
	if err != nil || len(got) != 1 || got[0].Title != "Focus" || got[0].TrackCount != 2 {
		t.Fatalf("playlists %+v %v", got, err)
	}
}

func TestMacNodePlaylistAndControlAndCreate(t *testing.T) {
	n := testMacNode(func(script string, args []string) (string, error) {
		switch {
		case len(args) > 0 && args[0] == "play_playlist":
			if len(args) != 2 || args[1] != "p1" {
				t.Fatalf("control args %v", args)
			}
			return "playing", nil
		case strings.Contains(script, "play_track"):
			if len(args) != 2 || args[0] != "play_track" || args[1] != "t1" {
				t.Fatalf("control args %v", args)
			}
			return "playing", nil
		case strings.Contains(script, "make new user playlist"):
			if len(args) != 3 || args[0] != "New list" || args[2] != "t1" {
				t.Fatalf("create args %v", args)
			}
			return "p2" + fieldSep + "New list" + fieldSep + "1" + fieldSep + "1", nil
		case strings.Contains(script, "set pid"):
			if len(args) != 3 || args[0] != "p1" || args[1] != "0" || args[2] != "10" {
				t.Fatalf("playlist args %v", args)
			}
			return strings.Join([]string{
				"P" + fieldSep + "p1" + fieldSep + "Focus" + fieldSep + "1" + fieldSep + "300" + fieldSep + "A test",
				"T" + fieldSep + "t1" + fieldSep + "Song" + fieldSep + "Artist" + fieldSep + "Album" + fieldSep + "240" + fieldSep + "3" + fieldSep + "50",
			}, recordSep), nil
		}
		t.Fatalf("unexpected script")
		return "", nil
	})
	d, err := n.playlist(context.Background(), "p1", 0, 10)
	if err != nil || d.Playlist.Title != "Focus" || len(d.Tracks) != 1 || d.Tracks[0].Title != "Song" {
		t.Fatalf("playlist %+v %v", d, err)
	}
	if _, err := n.control(context.Background(), "play_track", "t1", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := n.control(context.Background(), "play_playlist", "p1", 0); err != nil {
		t.Fatal(err)
	}
	created, err := n.createPlaylist(context.Background(), "New list", "", []string{"t1"})
	if err != nil || created.Playlist.ID != "p2" || created.Added != 1 {
		t.Fatalf("create %+v %v", created, err)
	}
}

func TestMacNodeStatusAndStats(t *testing.T) {
	n := testMacNode(func(script string, _ []string) (string, error) {
		switch {
		case strings.Contains(script, "isRunning"):
			return "1" + fieldSep + "1.5" + fieldSep + "120" + fieldSep + "2", nil
		case strings.Contains(script, "duration of lib"):
			return "120" + fieldSep + "3600" + fieldSep + "2", nil
		}
		t.Fatalf("unexpected script")
		return "", nil
	})
	st, err := n.status(context.Background())
	if err != nil || !st.Running || st.Tracks != 120 || st.Host == "" {
		t.Fatalf("status %+v %v", st, err)
	}
	stats, err := n.stats(context.Background())
	if err != nil || stats.Tracks != 120 || stats.DurationMS != 3600000 || stats.Playlists != 2 {
		t.Fatalf("stats %+v %v", stats, err)
	}
}

func TestMacNodeSearchAndNow(t *testing.T) {
	n := testMacNode(func(script string, _ []string) (string, error) {
		switch {
		case strings.Contains(script, "name contains q"):
			return "t1" + fieldSep + "Midnight City" + fieldSep + "M83" + fieldSep + "Hurry Up" + fieldSep + "243" + fieldSep + "4" + fieldSep + "80", nil
		case strings.Contains(script, "player state"):
			return "playing" + fieldSep + "t1" + fieldSep + "Midnight City" + fieldSep + "M83" + fieldSep + "Hurry Up" + fieldSep + "42" + fieldSep + "243", nil
		}
		t.Fatalf("unexpected script")
		return "", nil
	})
	got, err := n.search(context.Background(), "mid", 5)
	if err != nil || len(got) != 1 || got[0].Title != "Midnight City" || got[0].DurationMS != 243000 {
		t.Fatalf("search %+v %v", got, err)
	}
	now, err := n.nowPlaying(context.Background())
	if err != nil || now.State != "playing" || now.ID != "t1" || now.PositionMS != 42000 || now.DurationMS != 243000 {
		t.Fatalf("now %+v %v", now, err)
	}
}

func TestMacNodeArtwork(t *testing.T) {
	n := testMacNode(func(script string, args []string) (string, error) {
		if !strings.Contains(script, "raw data of artwork") || len(args) != 3 {
			t.Fatalf("artwork args %v", args)
		}
		data, err := base64.StdEncoding.DecodeString(onePixelPNG)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(args[2], data, 0600); err != nil {
			t.Fatal(err)
		}
		return "ok", nil
	})
	got, err := n.artwork(context.Background(), "track", "t1", 320)
	if err != nil || got.Data == "" {
		t.Fatalf("artwork %+v %v", got, err)
	}
}

func TestMacNodeArtworkMissing(t *testing.T) {
	n := testMacNode(func(script string, args []string) (string, error) {
		if !strings.Contains(script, "raw data of artwork") || len(args) != 3 || args[0] != "track" || args[1] != "t1" {
			t.Fatalf("artwork args %v", args)
		}
		return "none", nil
	})
	if _, err := n.artwork(context.Background(), "track", "t1", 600); err == nil || !strings.Contains(err.Error(), "no artwork") {
		t.Fatalf("artwork without a picture: %v", err)
	}
}

func TestMacError(t *testing.T) {
	err := macError(context.Background(), errors.New(`Not authorized to send Apple events to Music. (-1743)`))
	var e *ext.Error
	if !errors.As(err, &e) || e.Code != "macos_privacy" {
		t.Fatalf("error %v", err)
	}
}

func TestIPhoneProvider(t *testing.T) {
	p := newIPhoneProvider(openSnapshotStore(t.TempDir()))
	if st := p.Status(context.Background(), ""); st.State != "ok" || len(st.Guide) == 0 {
		t.Fatalf("status %+v", st)
	}
	snapshot := `{"now_playing":{"title":"Midnight City","artist":"M83","state":"playing"},"recent":[{"id":"r1","title":"Song","artist":"Artist"},{"id":"r2","title":"Other"}],"playlists":[{"id":"p1","title":"List","track_count":2}],"stats":{"tracks":10,"playlists":1}}`
	if _, err := p.Sync(context.Background(), json.RawMessage(snapshot)); err != nil {
		t.Fatal(err)
	}
	st := p.Status(context.Background(), "")
	if st.State != "ok" || st.LastSync.IsZero() {
		t.Fatalf("status %+v", st)
	}
	recent, err := p.Recent(context.Background(), 1)
	if err != nil || len(recent) != 1 || recent[0].Title != "Song" {
		t.Fatalf("recent %+v %v", recent, err)
	}
	playlists, err := p.Playlists(context.Background(), 1)
	if err != nil || len(playlists) != 1 || playlists[0].TrackCount != 2 {
		t.Fatalf("playlists %+v %v", playlists, err)
	}
	now, err := p.NowPlaying(context.Background())
	if err != nil || now.Title != "Midnight City" {
		t.Fatalf("now %+v %v", now, err)
	}
	stats, err := p.Stats(context.Background())
	if err != nil || stats.Tracks != 10 {
		t.Fatalf("stats %+v %v", stats, err)
	}
	if _, err := p.Sync(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("an empty snapshot was accepted")
	}
}

func TestRecordsDropMissingValue(t *testing.T) {
	rows := records("a" + fieldSep + "missing value" + fieldSep + "b" + recordSep + "c" + fieldSep + "d")
	if len(rows) != 2 || rows[0][1] != "" || rows[1][1] != "d" {
		t.Fatalf("records %#v", rows)
	}
}

func TestIPhoneSnapshotDropsEmptyTitlesAndStampsTheHub(t *testing.T) {
	p := newIPhoneProvider(openSnapshotStore(t.TempDir()))
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return at }
	if _, err := p.Sync(context.Background(), json.RawMessage(`{
		"synced_at":"2099-01-01T00:00:00Z",
		"recent":[{"id":"r1","title":""},{"id":"r2","title":"Real"}],
		"playlists":[{"id":"p1","title":"  "},{"id":"p2","title":"List"}]
	}`)); err != nil {
		t.Fatal(err)
	}
	snap := p.store.get()
	if !snap.SyncedAt.Equal(at) {
		t.Fatalf("synced_at %v, want the hub's %v", snap.SyncedAt, at)
	}
	if len(snap.Recent) != 1 || snap.Recent[0].Title != "Real" {
		t.Fatalf("recent %+v", snap.Recent)
	}
	if len(snap.Playlists) != 1 || snap.Playlists[0].Title != "List" {
		t.Fatalf("playlists %+v", snap.Playlists)
	}
	if st := p.Status(context.Background(), ""); !st.LastSync.Equal(at) {
		t.Fatalf("status %+v", st)
	}
}

func TestWriteStoreKeepsPendingOnlyWhenTheOutcomeIsUnknown(t *testing.T) {
	s := newWriteStore("")
	definite := &ext.Error{Code: "music_not_running", Message: "Music is not running on this Mac."}
	if _, err := s.commit("a", "k", func() (string, error) { return "", definite }); !errors.Is(err, definite) {
		t.Fatalf("a definite failure came back as %v", err)
	}
	if _, ok := s.entries["a"]; ok {
		t.Fatal("a definite failure left a pending receipt")
	}
	text, err := s.commit("a", "k", func() (string, error) { return "done", nil })
	if err != nil || text != "done" {
		t.Fatalf("retry %q %v", text, err)
	}
	if _, err := s.commit("b", "k", func() (string, error) { return "", context.DeadlineExceeded }); err == nil ||
		!strings.Contains(err.Error(), "unknown") {
		t.Fatalf("a timeout came back as %v", err)
	}
	if s.entries["b"].Status != "pending" {
		t.Fatalf("a timeout lost its pending receipt: %+v", s.entries["b"])
	}
}

func TestWriteStorePrunesFinishedReceipts(t *testing.T) {
	s := newWriteStore("")
	for i := 0; i < 256; i++ {
		s.entries[fmt.Sprintf("id-%d", i)] = writeReceipt{Key: "k", Status: "done", StartedAt: time.Unix(int64(i), 0)}
	}
	text, err := s.commit("new", "k", func() (string, error) { return "ok", nil })
	if err != nil || text != "ok" {
		t.Fatalf("commit %q %v", text, err)
	}
	if len(s.entries) > 200 {
		t.Fatalf("the store kept %d receipts", len(s.entries))
	}
}

// A created playlist can start playing in the same confirmed write.
func TestCreatePlaylistPlayArgument(t *testing.T) {
	req, play, err := parseCreatePlaylist(json.RawMessage(`{"name":"Mix","track_ids":["t1"],"play":true}`))
	if err != nil || !play || req.Name != "Mix" || len(req.TrackIDs) != 1 {
		t.Fatalf("parse %+v %v %v", req, play, err)
	}
	if _, play, err := parseCreatePlaylist(json.RawMessage(`{"name":"Mix","track_ids":["t1"]}`)); err != nil || play {
		t.Fatalf("play defaults to false: %v %v", play, err)
	}
	if _, _, err := parseCreatePlaylist(json.RawMessage(`{"name":"","track_ids":["t1"]}`)); err == nil {
		t.Fatal("an empty name was accepted")
	}
}

// The assistant sees the playlist playback: the control tool names it and create_playlist takes play.
func TestToolsExposePlaylistPlayback(t *testing.T) {
	p := newMacProvider("m1", macFixture())
	src := newSource(appleMusicID, appleMusicName, appleMusicName, "Apple")
	if err := src.add(p); err != nil {
		t.Fatal(err)
	}
	var control, create *tool
	for _, list := range [][]tool{toolsFor(src, p), actionToolsFor(src, p)} {
		for i := range list {
			switch list[i].Name {
			case p.ID() + "_control":
				control = &list[i]
			case p.ID() + "_create_playlist":
				create = &list[i]
			}
		}
	}
	if control == nil || create == nil {
		t.Fatalf("control %v create %v", control, create)
	}
	enum := control.InputSchema["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string)
	if !slices.Contains(enum, "play_playlist") {
		t.Fatalf("control actions %v", enum)
	}
	if _, ok := create.InputSchema["properties"].(map[string]any)["play"]; !ok {
		t.Fatal("create_playlist has no play argument")
	}
}
