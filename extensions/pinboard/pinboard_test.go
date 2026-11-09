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

const testToken = "reader:synthetic-token"

type fakePinboard struct {
	srv       *httptest.Server
	mu        sync.Mutex
	paths     []string
	adds      int
	failWrite bool
	badCode   bool
}

func sampleBookmarks() bookmarkList {
	return bookmarkList{{Href: "https://articles.example.com/context", Description: "Reading context", Extended: "Keep source context.", Tags: "reading later", Time: "2026-11-10T10:00:00Z", Shared: "no", ToRead: "yes"}}
}

func newFakePinboard(t *testing.T) *fakePinboard {
	f := &fakePinboard{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("auth_token") != testToken || r.URL.Query().Get("format") != "json" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/posts/recent":
			_ = json.NewEncoder(w).Encode(sampleBookmarks())
		case "/posts/all":
			items := sampleBookmarks()
			if r.URL.Query().Get("start") != "0" {
				items = nil
			}
			_ = json.NewEncoder(w).Encode(items)
		case "/tags/get":
			_ = json.NewEncoder(w).Encode(map[string]int{"reading": 4, "later": 2})
		case "/posts/add":
			f.mu.Lock()
			fail, bad := f.failWrite, f.badCode
			f.mu.Unlock()
			if fail {
				http.Error(w, "synthetic interrupted write", http.StatusServiceUnavailable)
				return
			}
			if r.URL.Query().Get("replace") != "no" || r.URL.Query().Get("shared") != "no" || r.URL.Query().Get("toread") != "yes" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			f.adds++
			f.mu.Unlock()
			if bad {
				_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]string{"code": "something went wrong"}})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]string{"code": "done"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePinboard) app() *app {
	return &app{client: newClient(f.srv.URL, testToken), receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakePinboard(t).app())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "pinboard" || len(m.Secrets) != 1 || m.Secrets[0].Name != "api_token" {
		t.Fatalf("%+v", m)
	}
	if len(m.Contributes.Connectors[0].ConfirmAlways) != 1 {
		t.Fatal("add must be confirmed")
	}
	if !slices.Equal(m.Permissions.Network, []string{"api.pinboard.in"}) {
		t.Fatal(m.Permissions.Network)
	}
}

func TestRecentArchiveTagsAndPagination(t *testing.T) {
	f := newFakePinboard(t)
	a := f.app()
	text, err := a.callTool(t.Context(), "pinboard_recent", json.RawMessage(`{"tags":"reading later"}`))
	if err != nil || !strings.Contains(text, "Reading context") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "pinboard_all", json.RawMessage(`{"start":50}`))
	if err != nil || !strings.Contains(text, "No bookmarks") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "pinboard_tags", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(text, "reading: 4") {
		t.Fatalf("%v %s", err, text)
	}
	f.mu.Lock()
	paths := strings.Join(f.paths, "\n")
	f.mu.Unlock()
	for _, want := range []string{"tag=reading+later", "start=50", "results=50", "format=json"} {
		if !strings.Contains(paths, want) {
			t.Fatalf("missing %q in %s", want, paths)
		}
	}
	if _, err := a.callTool(t.Context(), "pinboard_recent", json.RawMessage(`{"tags":"one two three four"}`)); err == nil {
		t.Fatal("accepted more than three filters")
	}
}

func TestAddReplayAndServiceError(t *testing.T) {
	f := newFakePinboard(t)
	a := f.app()
	args := []byte(`{"url":"https://articles.example.com/context","title":"Reading context","notes":"Keep context.","tags":"reading later"}`)
	for range 2 {
		if _, err := a.doAction(t.Context(), "create-1", "pinboard_add", args); err != nil {
			t.Fatal(err)
		}
	}
	if f.adds != 1 {
		t.Fatal(f.adds)
	}
	if _, err := a.doAction(t.Context(), "create-1", "pinboard_add", []byte(`{"url":"https://articles.example.com/other","title":"Different"}`)); err == nil {
		t.Fatal("receipt accepted a different bookmark")
	}
	if _, err := describeAction("pinboard_add", args); err != nil {
		t.Fatal(err)
	}
	f.badCode = true
	if _, err := a.doAction(t.Context(), "service-error", "pinboard_add", args); err == nil {
		t.Fatal("accepted a failed service result")
	}
}

func TestPersistentReplayAndUnknownOutcome(t *testing.T) {
	f := newFakePinboard(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"url":"https://articles.example.com/context","title":"Reading context","notes":"Keep context.","tags":"reading later"}`)
	if _, err := a.doAction(t.Context(), "persist-1", "pinboard_add", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persist-1", "pinboard_add", args); err != nil {
		t.Fatal(err)
	}
	if f.adds != 1 {
		t.Fatal("restart repeated creation")
	}
	key, _ := json.Marshal(addArgs{URL: "https://articles.example.com/context", Title: "Reading context", Notes: "Keep context.", Tags: "reading later"})
	raw, _ := json.Marshal(map[string]writeReceipt{"persist-1": {Key: string(key), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "persist-1", "pinboard_add", args); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown add was retried")
	}
	if f.adds != 1 {
		t.Fatal("unknown add caused another write")
	}
}

func TestLocalizedViewsAndCreateFlow(t *testing.T) {
	f := newFakePinboard(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			for _, mode := range []string{"", "archive", "create", "review-create"} {
				r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
				st := pageState{Mode: mode, Tags: "reading", DraftID: "draft-1", URL: "https://articles.example.com/context", Title: "Reading context", Notes: "Keep context.", NewTags: "reading later"}
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
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6, Values: map[string]string{"url": "https://articles.example.com/context", "title": "Reading context", "notes": "保留来源上下文。", "tags": "reading later"}}}
	result, err := a.action(t.Context(), withAction(r, "review-create", pageState{Mode: "create", DraftID: "draft-1"}))
	if err != nil || result.View == nil || wire.CheckView(result.View) != nil {
		t.Fatalf("review: %v %+v", err, result)
	}
	result, err = a.action(t.Context(), withAction(r, "confirm-create", pageState{Mode: "review-create", DraftID: "draft-1", URL: "https://articles.example.com/context", Title: "Reading context", Notes: "保留来源上下文。", NewTags: "reading later"}))
	if err != nil || !result.Pop || f.adds != 1 {
		t.Fatalf("confirm: %v %+v %d", err, result, f.adds)
	}
}

func withAction(r *ext.UIRequest, action string, st pageState) *ext.UIRequest {
	r.Action = action
	r.State = ext.EncodeState(st)
	return r
}

func TestBoundsAndNoConfig(t *testing.T) {
	a := newApp()
	if a.status()["state"] != "not_configured" {
		t.Fatal(a.status())
	}
	v, err := a.render(t.Context(), &ext.UIRequest{})
	if err != nil || !strings.Contains(v.Sections[0].Items[0].Title, "API token") {
		t.Fatalf("%v %+v", err, v)
	}
	for _, raw := range []string{"http://articles.example.com/context", "https://me@example.com/context", "not-a-url"} {
		if err := (addArgs{URL: raw, Title: "Bad"}).validate(); err == nil {
			t.Fatal("accepted " + raw)
		}
	}
}

func TestFailedFinalWriteIsUnknown(t *testing.T) {
	f := newFakePinboard(t)
	f.failWrite = true
	a := f.app()
	if _, err := a.doAction(t.Context(), "network-1", "pinboard_add", []byte(`{"url":"https://articles.example.com/context","title":"Reading context"}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
	if f.adds != 0 {
		t.Fatal("failed write was counted as successful")
	}
	if _, err := fmt.Fprint(os.Stderr, ""); err != nil {
		t.Fatal(err)
	}
}
