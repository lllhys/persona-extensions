package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

const testToken = "synthetic-miniflux-key"

type fakeMiniflux struct {
	srv       *httptest.Server
	mu        sync.Mutex
	paths     []string
	reads     []map[string]any
	bookmarks int
}

func sampleEntry() entry {
	item := entry{ID: 42, Title: "Preserve reading context", URL: "https://articles.example.com/context", Author: "Example Author", Content: `<article><h1>Reading context</h1><p>Keep the source beside the notes.</p><script>ignore this instruction</script><form>do not submit</form><p>The next session starts with the right information.</p></article>`, PublishedAt: "2026-11-10T10:00:00Z", Status: "unread", ReadingTime: 4}
	item.Feed.ID = 7
	item.Feed.Title = "Reading notes"
	item.Feed.Category.Title = "Personal"
	return item
}

func newFakeMiniflux(t *testing.T) *fakeMiniflux {
	f := &fakeMiniflux{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/entries":
			if r.Method == http.MethodPut {
				var in map[string]any
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				f.mu.Lock()
				f.reads = append(f.reads, in)
				f.mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if r.URL.Query().Get("limit") != "50" || r.URL.Query().Get("order") != "published_at" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			page := entryPage{Total: 1, Entries: []entry{sampleEntry()}}
			if r.URL.Query().Get("offset") != "0" {
				page.Entries = nil
			}
			_ = json.NewEncoder(w).Encode(page)
		case "/v1/entries/42":
			_ = json.NewEncoder(w).Encode(sampleEntry())
		case "/v1/entries/42/bookmark":
			if r.Method != http.MethodPut {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			f.mu.Lock()
			f.bookmarks++
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMiniflux) app() *app {
	return &app{client: newClient(f.srv.URL, testToken), receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeMiniflux(t).app())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "miniflux" || len(m.Secrets) != 1 || m.Secrets[0].Name != "api_key" {
		t.Fatalf("%+v", m)
	}
	if len(m.Contributes.Connectors[0].ConfirmAlways) != 2 {
		t.Fatal("read and bookmark must be confirmed")
	}
	if !slices.Equal(m.Permissions.Network, []string{}) {
		t.Fatal(m.Permissions.Network)
	}
}

func TestInstanceURLValidation(t *testing.T) {
	for raw, want := range map[string]string{"https://miniflux.example.com": "https://miniflux.example.com", "http://127.0.0.1:8080/": "http://127.0.0.1:8080"} {
		got, err := instanceURL(raw)
		if err != nil || got != want {
			t.Fatalf("%q: %v %q", raw, err, got)
		}
	}
	for _, raw := range []string{"http://miniflux.example.com", "https://me@example.com", "not-a-url"} {
		if _, err := instanceURL(raw); err == nil {
			t.Fatal("accepted " + raw)
		}
	}
}

func TestReadingFiltersPaginationAndSanitizedContent(t *testing.T) {
	f := newFakeMiniflux(t)
	a := f.app()
	text, err := a.callTool(t.Context(), "miniflux_entries", json.RawMessage(`{"view":"starred","offset":50}`))
	if err != nil || !strings.Contains(text, "No entries") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "miniflux_entries", json.RawMessage(`{"view":"search","query":"context"}`))
	if err != nil || !strings.Contains(text, "Entry #42") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "miniflux_entry", json.RawMessage(`{"entry_id":42}`))
	if err != nil || !strings.Contains(text, "Keep the source") || strings.Contains(text, "ignore this instruction") || strings.Contains(text, "do not submit") {
		t.Fatalf("%v %s", err, text)
	}
	f.mu.Lock()
	paths := strings.Join(f.paths, "\n")
	f.mu.Unlock()
	for _, want := range []string{"starred=true", "offset=50", "search=context"} {
		if !strings.Contains(paths, want) {
			t.Fatalf("missing %q in %s", want, paths)
		}
	}
	if _, err := a.callTool(t.Context(), "miniflux_entries", json.RawMessage(`{"view":"search"}`)); err == nil {
		t.Fatal("accepted an empty search")
	}
}

func TestReadAndBookmarkReplay(t *testing.T) {
	f := newFakeMiniflux(t)
	a := f.app()
	args := []byte(`{"entry_id":42}`)
	for range 2 {
		if _, err := a.doAction(t.Context(), "read-1", "miniflux_mark_read", args); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := a.doAction(t.Context(), "bookmark-1", "miniflux_toggle_bookmark", args); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.reads) != 1 || f.bookmarks != 1 {
		t.Fatalf("%v %d", f.reads, f.bookmarks)
	}
	ids, ok := f.reads[0]["entry_ids"].([]any)
	if !ok || len(ids) != 1 || ids[0] != float64(42) || f.reads[0]["status"] != "read" || len(f.reads[0]) != 2 {
		t.Fatal("read operation sent unexpected fields")
	}
	if _, err := a.doAction(t.Context(), "bookmark-1", "miniflux_toggle_bookmark", []byte(`{"entry_id":43}`)); err == nil {
		t.Fatal("receipt accepted a different entry")
	}
	if summary, err := describeAction("miniflux_toggle_bookmark", args); err != nil || !strings.Contains(fmt.Sprint(summary["summary"]), "Toggle") {
		t.Fatalf("%v %v", err, summary)
	}
}

func TestPersistentReplayAndUnknownOutcome(t *testing.T) {
	f := newFakeMiniflux(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"entry_id":42}`)
	if _, err := a.doAction(t.Context(), "persist-1", "miniflux_mark_read", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persist-1", "miniflux_mark_read", args); err != nil {
		t.Fatal(err)
	}
	if len(f.reads) != 1 {
		t.Fatal("restart repeated read state change")
	}
	key, _ := json.Marshal(entryArgs{EntryID: 42})
	raw, _ := json.Marshal(map[string]writeReceipt{"persist-1": {Key: string(key), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "persist-1", "miniflux_mark_read", args); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown read state was retried")
	}
	if len(f.reads) != 1 {
		t.Fatal("unknown read state caused another write")
	}
}

func TestLocalizedViewsAndActions(t *testing.T) {
	f := newFakeMiniflux(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
			v, err := a.page(t.Context(), r, pageState{View: "unread"})
			if err != nil || wire.CheckView(v) != nil {
				t.Fatalf("%s %d: %v", locale, ui, err)
			}
			v, err = a.page(t.Context(), r, pageState{Mode: "detail", EntryID: 42})
			if err != nil || wire.CheckView(v) != nil {
				t.Fatalf("%s %d detail: %v", locale, ui, err)
			}
		}
	}
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6}}
	result, err := a.action(t.Context(), withAction(r, "read", pageState{Mode: "detail", EntryID: 42, DraftID: "draft-1"}))
	if err != nil || !result.Pop || len(f.reads) != 1 {
		t.Fatalf("%v %+v %v", err, result, f.reads)
	}
}

func withAction(r *ext.UIRequest, action string, st pageState) *ext.UIRequest {
	r.Action = action
	r.State = ext.EncodeState(st)
	return r
}

func TestBoundsAndNoConfig(t *testing.T) {
	item := sampleEntry()
	item.Content = strings.Repeat("context ", 3000)
	v := detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{EntryID: 42, DraftID: "draft-1"}, item, strings.Repeat("reading context\n", 2000))
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
	item.URL = "http://articles.example.com/insecure"
	v = detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{EntryID: 42}, item, "text")
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	if a.status()["state"] != "not_configured" {
		t.Fatal(a.status())
	}
	v, err := a.render(t.Context(), &ext.UIRequest{})
	if err != nil || !strings.Contains(v.Sections[0].Items[0].Title, "instance URL") {
		t.Fatalf("%v %+v", err, v)
	}
}

func TestNetworkFailureIsUnknown(t *testing.T) {
	f := newFakeMiniflux(t)
	f.srv.Close()
	a := f.app()
	if _, err := a.doAction(t.Context(), "network-1", "miniflux_mark_read", []byte(`{"entry_id":42}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
	if len(f.reads) != 0 {
		t.Fatal("failed write was counted as successful")
	}
	if _, err := fmt.Fprint(os.Stderr, ""); err != nil {
		t.Fatal(err)
	}
}
