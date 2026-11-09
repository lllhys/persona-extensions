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

const testToken = "synthetic-goodlinks-token"

type fakeGoodLinks struct {
	srv       *httptest.Server
	mu        sync.Mutex
	paths     []string
	updates   []map[string]any
	failWrite bool
}

func sampleLink() link {
	return link{ID: "link-1", URL: "https://articles.example.com/context", Title: "Reading context", Summary: "Keep source context", Author: "Example Author", Tags: []string{"reading", "later"}, WordCount: 1200, Highlighted: true, AddedAt: "2026-11-09T10:00:00Z"}
}

func newFakeGoodLinks(t *testing.T) *fakeGoodLinks {
	f := &fakeGoodLinks{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/lists/unread", "/api/v1/lists/starred":
			items := []link{sampleLink()}
			if r.URL.Query().Get("offset") != "0" {
				items = nil
			}
			_ = json.NewEncoder(w).Encode(linkPage{Data: items, HasMore: true})
		case "/api/v1/links":
			if r.URL.Query().Get("search") == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(linkPage{Data: []link{sampleLink()}})
		case "/api/v1/links/link-1":
			if r.Method == http.MethodPatch {
				f.mu.Lock()
				fail := f.failWrite
				f.mu.Unlock()
				if fail {
					http.Error(w, "synthetic interrupted write", http.StatusServiceUnavailable)
					return
				}
				var in map[string]any
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				f.mu.Lock()
				f.updates = append(f.updates, in)
				f.mu.Unlock()
				item := sampleLink()
				if starred, ok := in["starred"].(bool); ok {
					item.Starred = starred
				}
				if read, ok := in["read"].(bool); ok && read {
					now := "2026-11-10T10:00:00Z"
					item.ReadAt = &now
				}
				_ = json.NewEncoder(w).Encode(item)
				return
			}
			_ = json.NewEncoder(w).Encode(sampleLink())
		case "/api/v1/links/link-1/content":
			if r.URL.Query().Get("format") != "plaintext" || r.URL.Query().Get("autoDownload") != "false" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, "Reading context\n\nKeep source context beside the next step.")
		case "/api/v1/highlights":
			if r.URL.Query().Get("linkID") != "link-1" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			note := "Keep this for the next review."
			_ = json.NewEncoder(w).Encode(highlightPage{Data: []highlight{{ID: "highlight-1", LinkID: "link-1", Content: "Keep the source context.", Note: &note, CreatedAt: "2026-11-10T10:00:00Z"}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoodLinks) app() *app {
	return &app{client: newClient(f.srv.URL, testToken), receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeGoodLinks(t).app())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "goodlinks" || len(m.Secrets) != 1 || m.Secrets[0].Name != "api_token" {
		t.Fatalf("%+v", m)
	}
	if len(m.Contributes.Connectors[0].ConfirmAlways) != 2 {
		t.Fatal("read and star must be confirmed")
	}
	if !slices.Equal(m.Permissions.Network, []string{}) {
		t.Fatal(m.Permissions.Network)
	}
}

func TestLoopbackURLValidation(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:9428", "http://localhost:9428", "https://[::1]:9428"} {
		if _, err := instanceURL(raw); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{"https://goodlinks.example.com", "http://192.168.1.2:9428", "https://me@example.com", "not-a-url"} {
		if _, err := instanceURL(raw); err == nil {
			t.Fatal("accepted " + raw)
		}
	}
}

func TestReadingSearchContentAndHighlights(t *testing.T) {
	f := newFakeGoodLinks(t)
	a := f.app()
	text, err := a.callTool(t.Context(), "goodlinks_links", json.RawMessage(`{"view":"starred","offset":50}`))
	if err != nil || !strings.Contains(text, "No links") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "goodlinks_links", json.RawMessage(`{"view":"search","query":"context"}`))
	if err != nil || !strings.Contains(text, "link-1") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "goodlinks_link", json.RawMessage(`{"link_id":"link-1"}`))
	if err != nil || !strings.Contains(text, "Keep source context") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "goodlinks_highlights", json.RawMessage(`{"link_id":"link-1"}`))
	if err != nil || !strings.Contains(text, "Keep this for the next review") {
		t.Fatalf("%v %s", err, text)
	}
	f.mu.Lock()
	paths := strings.Join(f.paths, "\n")
	f.mu.Unlock()
	for _, want := range []string{"offset=50", "search=context", "format=plaintext", "autoDownload=false", "linkID=link-1"} {
		if !strings.Contains(paths, want) {
			t.Fatalf("missing %q in %s", want, paths)
		}
	}
}

func TestReadStarReplayAndPartialUpdates(t *testing.T) {
	f := newFakeGoodLinks(t)
	a := f.app()
	for range 2 {
		if _, err := a.doAction(t.Context(), "read-1", "goodlinks_mark_read", []byte(`{"link_id":"link-1"}`)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := a.doAction(t.Context(), "star-1", "goodlinks_toggle_star", []byte(`{"link_id":"link-1","starred":true}`)); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.updates) != 2 {
		t.Fatal(f.updates)
	}
	if len(f.updates[0]) != 1 || f.updates[0]["read"] != true || len(f.updates[1]) != 1 || f.updates[1]["starred"] != true {
		t.Fatal("partial update changed other fields")
	}
	if _, err := a.doAction(t.Context(), "star-1", "goodlinks_toggle_star", []byte(`{"link_id":"link-1","starred":false}`)); err == nil {
		t.Fatal("receipt accepted a different star state")
	}
}

func TestPersistentReplayAndUnknownOutcome(t *testing.T) {
	f := newFakeGoodLinks(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"link_id":"link-1"}`)
	if _, err := a.doAction(t.Context(), "persist-1", "goodlinks_mark_read", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persist-1", "goodlinks_mark_read", args); err != nil {
		t.Fatal(err)
	}
	if len(f.updates) != 1 {
		t.Fatal("restart repeated read")
	}
	key, _ := json.Marshal(readArgs{LinkID: "link-1"})
	raw, _ := json.Marshal(map[string]writeReceipt{"persist-1": {Key: string(key), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "persist-1", "goodlinks_mark_read", args); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown read was retried")
	}
	if len(f.updates) != 1 {
		t.Fatal("unknown read caused another write")
	}
}

func TestLocalizedViewsAndActions(t *testing.T) {
	f := newFakeGoodLinks(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			for _, mode := range []string{"", "detail"} {
				r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
				st := pageState{Mode: mode, View: "unread", LinkID: "link-1"}
				v, err := a.page(t.Context(), r, st)
				if err != nil {
					t.Fatalf("%s %d %s: %v", locale, ui, mode, err)
				}
				if err := wire.CheckView(v); err != nil {
					t.Fatalf("%s %d %s: %v", locale, ui, mode, err)
				}
			}
		}
	}
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6}}
	result, err := a.action(t.Context(), withAction(r, "read", pageState{Mode: "detail", LinkID: "link-1", DraftID: "draft-1"}))
	if err != nil || !result.Pop || len(f.updates) != 1 {
		t.Fatalf("%v %+v %v", err, result, f.updates)
	}
}

func withAction(r *ext.UIRequest, action string, st pageState) *ext.UIRequest {
	r.Action = action
	r.State = ext.EncodeState(st)
	return r
}

func TestBoundsAndNoConfig(t *testing.T) {
	item := sampleLink()
	v := detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{LinkID: "link-1", DraftID: "draft-1"}, item, strings.Repeat("context\n", 2000), nil)
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
	item.URL = "http://articles.example.com/insecure"
	v = detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{LinkID: "link-1"}, item, "text", nil)
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	if a.status()["state"] != "not_configured" {
		t.Fatal(a.status())
	}
	v, err := a.render(t.Context(), &ext.UIRequest{})
	if err != nil || !strings.Contains(v.Sections[0].Items[0].Title, "API token") {
		t.Fatalf("%v %+v", err, v)
	}
}

func TestFailedFinalWriteIsUnknown(t *testing.T) {
	f := newFakeGoodLinks(t)
	f.failWrite = true
	a := f.app()
	if _, err := a.doAction(t.Context(), "network-1", "goodlinks_mark_read", []byte(`{"link_id":"link-1"}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
	if len(f.updates) != 0 {
		t.Fatal("failed write was counted as successful")
	}
	if _, err := fmt.Fprint(os.Stderr, ""); err != nil {
		t.Fatal(err)
	}
}
