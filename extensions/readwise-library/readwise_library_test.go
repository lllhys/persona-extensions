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

const testToken = "synthetic-readwise-library-token"

type fakeLibrary struct {
	srv       *httptest.Server
	mu        sync.Mutex
	paths     []string
	creates   []map[string]any
	updates   []map[string]any
	tags      []map[string]any
	failWrite bool
}

func sampleBook() book {
	return book{ID: 7, Title: "Reading context", Author: "Example Author", Category: "articles", Source: "reader", NumHighlights: 2, LastHighlightAt: "2026-11-10T10:00:00Z", HighlightsURL: "https://readwise.io/bookreview/7"}
}

func sampleHighlight() highlight {
	return highlight{ID: 42, Text: "Keep source context beside the next step.", Note: "Use this in the next review.", Location: 3, LocationType: "order", Updated: "2026-11-10T10:00:00Z", BookID: 7, ReadwiseURL: "https://readwise.io/open/42", Tags: []tag{{ID: 1, Name: "reading"}}}
}

func newFakeLibrary(t *testing.T) *fakeLibrary {
	f := &fakeLibrary{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/books/":
			items := []book{sampleBook()}
			if r.URL.Query().Get("page") != "1" {
				items = nil
			}
			_ = json.NewEncoder(w).Encode(page[book]{Count: 1, Next: "https://readwise.io/api/v2/books/?page=2", Results: items})
		case "/api/v2/books/7/":
			_ = json.NewEncoder(w).Encode(sampleBook())
		case "/api/v2/books/7/tags/":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			f.mu.Lock()
			fail := f.failWrite
			f.mu.Unlock()
			if fail {
				http.Error(w, "synthetic interrupted write", http.StatusServiceUnavailable)
				return
			}
			var in map[string]any
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in["name"] == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			f.tags = append(f.tags, in)
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(tag{ID: 2, Name: in["name"].(string)})
		case "/api/v2/highlights/":
			if r.Method == http.MethodPost {
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
				f.creates = append(f.creates, in)
				f.mu.Unlock()
				_ = json.NewEncoder(w).Encode(sampleHighlight())
				return
			}
			if r.URL.Query().Get("book_id") != "7" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			items := []highlight{sampleHighlight()}
			if r.URL.Query().Get("page") != "1" {
				items = nil
			}
			_ = json.NewEncoder(w).Encode(page[highlight]{Count: 1, Next: "https://readwise.io/api/v2/highlights/?page=2", Results: items})
		case "/api/v2/highlights/42/":
			if r.Method == http.MethodPatch {
				var in map[string]any
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				f.mu.Lock()
				f.updates = append(f.updates, in)
				f.mu.Unlock()
				item := sampleHighlight()
				item.Note, _ = in["note"].(string)
				_ = json.NewEncoder(w).Encode(item)
				return
			}
			_ = json.NewEncoder(w).Encode(sampleHighlight())
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLibrary) app() *app {
	return &app{client: newClient(f.srv.URL, testToken), receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeLibrary(t).app())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "readwise-library" || len(m.Secrets) != 1 || m.Secrets[0].Name != "access_token" {
		t.Fatalf("%+v", m)
	}
	if len(m.Contributes.Connectors[0].ConfirmAlways) != 3 {
		t.Fatal("create, note and tag must be confirmed")
	}
	if !slices.Equal(m.Permissions.Network, []string{"readwise.io"}) {
		t.Fatal(m.Permissions.Network)
	}
}

func TestBooksHighlightsAndNextPagination(t *testing.T) {
	f := newFakeLibrary(t)
	a := f.app()
	text, err := a.callTool(t.Context(), "readwise_library_books", json.RawMessage(`{"category":"articles","page":1}`))
	t.Logf("books result %v %s paths=%v", err, text, f.paths)
	text, err = a.callTool(t.Context(), "readwise_library_highlights", json.RawMessage(`{"book_id":7,"page":1}`))
	if err != nil || !strings.Contains(text, "Keep source context") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "readwise_library_highlight", json.RawMessage(`{"highlight_id":42}`))
	if err != nil || !strings.Contains(text, "Use this in the next review") {
		t.Fatalf("%v %s", err, text)
	}
	f.mu.Lock()
	paths := strings.Join(f.paths, "\n")
	f.mu.Unlock()
	for _, want := range []string{"category=articles", "page_size=50", "book_id=7"} {
		if !strings.Contains(paths, want) {
			t.Fatalf("missing %q in %s", want, paths)
		}
	}
	if next := nextPage("https://readwise.io/api/v2/highlights/?page=2", 1); next != 2 {
		t.Fatal(next)
	}
}

func TestCreateNoteTagReplayAndPartialWrites(t *testing.T) {
	f := newFakeLibrary(t)
	a := f.app()
	create := []byte(`{"text":"Keep source context.","title":"Reading context","author":"Example Author","note":"Review later.","source_url":"https://articles.example.com/context"}`)
	for range 2 {
		if _, err := a.doAction(t.Context(), "create-1", "readwise_library_create_highlight", create); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := a.doAction(t.Context(), "note-1", "readwise_library_update_note", []byte(`{"highlight_id":42,"note":"Updated note"}`)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := a.doAction(t.Context(), "tag-1", "readwise_library_add_book_tag", []byte(`{"book_id":7,"name":"review"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.creates) != 1 || len(f.updates) != 1 || len(f.tags) != 1 {
		t.Fatalf("%v %v %v", f.creates, f.updates, f.tags)
	}
	items, ok := f.creates[0]["highlights"].([]any)
	if !ok || len(items) != 1 {
		t.Fatal("create payload did not contain one highlight")
	}
	if len(f.updates[0]) != 1 || f.updates[0]["note"] != "Updated note" {
		t.Fatal("note update changed other fields")
	}
	if _, err := a.doAction(t.Context(), "note-1", "readwise_library_update_note", []byte(`{"highlight_id":42,"note":"Different"}`)); err == nil {
		t.Fatal("receipt accepted a different note")
	}
	if _, err := describeAction("readwise_library_create_highlight", create); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentReplayAndUnknownOutcome(t *testing.T) {
	f := newFakeLibrary(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"highlight_id":42,"note":"Updated note"}`)
	if _, err := a.doAction(t.Context(), "persist-1", "readwise_library_update_note", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persist-1", "readwise_library_update_note", args); err != nil {
		t.Fatal(err)
	}
	if len(f.updates) != 1 {
		t.Fatal("restart repeated note")
	}
	key, _ := json.Marshal(noteArgs{HighlightID: 42, Note: "Updated note"})
	raw, _ := json.Marshal(map[string]writeReceipt{"persist-1": {Key: string(key), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "persist-1", "readwise_library_update_note", args); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown note was retried")
	}
	if len(f.updates) != 1 {
		t.Fatal("unknown note caused another write")
	}
}

func TestLocalizedViewsAndNoteFlow(t *testing.T) {
	f := newFakeLibrary(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			for _, mode := range []string{"", "highlights", "detail", "create", "review-create", "edit-note", "review-note", "add-tag", "review-tag"} {
				r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
				st := pageState{Mode: mode, BookID: 7, BookName: "Reading context", HighlightID: 42, DraftID: "draft-1", Text: "Keep context.", Title: "Reading context", Author: "Example Author", Note: "Review later.", SourceURL: "https://articles.example.com/context", TagName: "review"}
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
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6, Values: map[string]string{"note": "保留来源上下文。"}}}
	result, err := a.action(t.Context(), withAction(r, "review-note", pageState{Mode: "edit-note", HighlightID: 42, DraftID: "draft-1"}))
	if err != nil || result.View == nil || wire.CheckView(result.View) != nil {
		t.Fatalf("review: %v %+v", err, result)
	}
	result, err = a.action(t.Context(), withAction(r, "confirm-note", pageState{Mode: "review-note", HighlightID: 42, DraftID: "draft-1", Note: "保留来源上下文。"}))
	if err != nil || !result.Pop || len(f.updates) != 1 {
		t.Fatalf("confirm: %v %+v %v", err, result, f.updates)
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
		if err := (createArgs{Text: "text", Title: "title", SourceURL: raw}).validate(); err == nil {
			t.Fatal("accepted " + raw)
		}
	}
}

func TestFailedFinalWriteIsUnknown(t *testing.T) {
	f := newFakeLibrary(t)
	f.failWrite = true
	a := f.app()
	if _, err := a.doAction(t.Context(), "network-1", "readwise_library_create_highlight", []byte(`{"text":"Keep context.","title":"Reading context"}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
	if len(f.creates) != 0 {
		t.Fatal("failed write was counted as successful")
	}
	if _, err := fmt.Fprint(os.Stderr, ""); err != nil {
		t.Fatal(err)
	}
}
