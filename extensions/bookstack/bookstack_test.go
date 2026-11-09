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

const testTokenID = "synthetic-bookstack-id"
const testTokenSecret = "synthetic-bookstack-secret"

type fakeBookStack struct {
	srv       *httptest.Server
	mu        sync.Mutex
	paths     []string
	creates   []map[string]any
	updates   []map[string]any
	failWrite bool
}

func samplePage() page {
	return page{ID: 42, BookID: 7, Name: "Reading context", Slug: "reading-context", Markdown: "# Reading context\n\nKeep source context beside the next step.", UpdatedAt: "2026-11-10T10:00:00Z", URL: "https://docs.example.com/books/reading/page/context"}
}

func newFakeBookStack(t *testing.T) *fakeBookStack {
	f := &fakeBookStack{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token "+testTokenID+":"+testTokenSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/books":
			page := listPage[book]{Data: []book{{ID: 7, Name: "Reading", Slug: "reading", Description: "Personal documentation"}}, Total: 1}
			if r.URL.Query().Get("offset") != "0" {
				page.Data = nil
			}
			_ = json.NewEncoder(w).Encode(page)
		case "/api/books/7":
			_ = json.NewEncoder(w).Encode(book{ID: 7, Name: "Reading", Slug: "reading"})
		case "/api/pages":
			if r.Method == http.MethodPost {
				f.mu.Lock()
				fail := f.failWrite
				f.mu.Unlock()
				if fail {
					http.Error(w, "synthetic interrupted write", http.StatusServiceUnavailable)
					return
				}
				var in map[string]any
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in["book_id"] != float64(7) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				f.mu.Lock()
				f.creates = append(f.creates, in)
				f.mu.Unlock()
				item := samplePage()
				item.ID, item.Name = 43, in["name"].(string)
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(item)
				return
			}
			if r.URL.Query().Get("filter[book_id]") != "7" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(listPage[page]{Data: []page{samplePage()}, Total: 1})
		case "/api/pages/42":
			if r.Method == http.MethodPut {
				var in map[string]any
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				f.mu.Lock()
				f.updates = append(f.updates, in)
				f.mu.Unlock()
				item := samplePage()
				item.Markdown, _ = in["markdown"].(string)
				_ = json.NewEncoder(w).Encode(item)
				return
			}
			_ = json.NewEncoder(w).Encode(samplePage())
		case "/api/pages/42/export/plaintext":
			fmt.Fprint(w, "Reading context\n\nKeep source context.")
		case "/api/pages/html-page":
			w.WriteHeader(http.StatusNotFound)
		case "/api/pages/99":
			item := samplePage()
			item.ID, item.Markdown, item.HTML = 99, "", "<p>HTML only</p>"
			_ = json.NewEncoder(w).Encode(item)
		case "/api/pages/99/export/plaintext":
			fmt.Fprint(w, "HTML only as plain text")
		case "/api/search":
			if r.URL.Query().Get("query") == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(listPage[searchResult]{Data: []searchResult{{ID: 42, BookID: 7, Name: "Reading context", Type: "page", URL: "https://docs.example.com/books/reading/page/context"}}, Total: 1})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBookStack) app() *app {
	return &app{client: newClient(f.srv.URL, testTokenID, testTokenSecret), receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeBookStack(t).app())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "bookstack" || len(m.Secrets) != 2 || m.Secrets[0].Name != "token_id" || m.Secrets[1].Name != "token_secret" {
		t.Fatalf("%+v", m)
	}
	if len(m.Contributes.Connectors[0].ConfirmAlways) != 2 {
		t.Fatal("create and update must be confirmed")
	}
	if !slices.Equal(m.Permissions.Network, []string{}) {
		t.Fatal(m.Permissions.Network)
	}
}

func TestInstanceURLValidation(t *testing.T) {
	for raw, want := range map[string]string{"https://docs.example.com": "https://docs.example.com", "http://127.0.0.1:8080/": "http://127.0.0.1:8080"} {
		got, err := instanceURL(raw)
		if err != nil || got != want {
			t.Fatalf("%q: %v %q", raw, err, got)
		}
	}
	for _, raw := range []string{"http://docs.example.com", "https://me@example.com", "not-a-url"} {
		if _, err := instanceURL(raw); err == nil {
			t.Fatal("accepted " + raw)
		}
	}
}

func TestBooksPagesSearchAndPlaintextFallback(t *testing.T) {
	f := newFakeBookStack(t)
	a := f.app()
	text, err := a.callTool(t.Context(), "bookstack_books", json.RawMessage(`{"offset":50}`))
	if err != nil || !strings.Contains(text, "No visible books") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "bookstack_pages", json.RawMessage(`{"book_id":7}`))
	if err != nil || !strings.Contains(text, "Page #42") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "bookstack_search", json.RawMessage(`{"query":"context","page":1}`))
	if err != nil || !strings.Contains(text, "Reading context") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "bookstack_page", json.RawMessage(`{"page_id":42}`))
	if err != nil || !strings.Contains(text, "Keep source context") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "bookstack_page", json.RawMessage(`{"page_id":99}`))
	if err != nil || !strings.Contains(text, "HTML only as plain text") {
		t.Fatalf("%v %s", err, text)
	}
	f.mu.Lock()
	paths := strings.Join(f.paths, "\n")
	f.mu.Unlock()
	for _, want := range []string{"offset=50", "filter%5Bbook_id%5D=7", "query=context", "page=1"} {
		if !strings.Contains(paths, want) {
			t.Fatalf("missing %q in %s", want, paths)
		}
	}
}

func TestCreateUpdateReplayAndNoHTMLConversion(t *testing.T) {
	f := newFakeBookStack(t)
	a := f.app()
	create := []byte(`{"book_id":7,"name":"New reading note","markdown":"# Note\n\nKeep context."}`)
	for range 2 {
		if _, err := a.doAction(t.Context(), "create-1", "bookstack_create_page", create); err != nil {
			t.Fatal(err)
		}
	}
	update := []byte(`{"page_id":42,"name":"Reading context","markdown":"Updated context"}`)
	for range 2 {
		if _, err := a.doAction(t.Context(), "update-1", "bookstack_update_page", update); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.creates) != 1 || len(f.updates) != 1 {
		t.Fatalf("%v %v", f.creates, f.updates)
	}
	if _, hasHTML := f.creates[0]["html"]; hasHTML {
		t.Fatal("creation sent raw HTML")
	}
	if _, err := a.doAction(t.Context(), "html-1", "bookstack_update_page", []byte(`{"page_id":99,"markdown":"converted"}`)); err == nil || len(f.updates) != 1 {
		t.Fatal("implicit HTML conversion was allowed")
	}
	if _, err := a.doAction(t.Context(), "update-1", "bookstack_update_page", []byte(`{"page_id":42,"markdown":"different"}`)); err == nil {
		t.Fatal("receipt accepted different content")
	}
	if _, err := describeAction("bookstack_create_page", create); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentReplayAndUnknownOutcome(t *testing.T) {
	f := newFakeBookStack(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"page_id":42,"name":"Reading context","markdown":"Updated context"}`)
	if _, err := a.doAction(t.Context(), "persist-1", "bookstack_update_page", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persist-1", "bookstack_update_page", args); err != nil {
		t.Fatal(err)
	}
	if len(f.updates) != 1 {
		t.Fatal("restart repeated update")
	}
	key, _ := json.Marshal(updateArgs{PageID: 42, Name: "Reading context", Markdown: "Updated context"})
	raw, _ := json.Marshal(map[string]writeReceipt{"persist-1": {Key: string(key), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "persist-1", "bookstack_update_page", args); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown update was retried")
	}
	if len(f.updates) != 1 {
		t.Fatal("unknown update caused another write")
	}
}

func TestLocalizedViewsAndEditFlow(t *testing.T) {
	f := newFakeBookStack(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			for _, mode := range []string{"", "pages", "detail", "search", "create", "review-create", "edit", "review-edit"} {
				r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
				st := pageState{Mode: mode, BookID: 7, BookName: "Reading", PageID: 42, Search: "context", SearchPage: 1, DraftID: "draft-1", Name: "Reading context", Markdown: "# Context\n\nKeep the source."}
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
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6, Values: map[string]string{"name": "Reading context", "markdown": "保留来源上下文。"}}}
	result, err := a.action(t.Context(), withAction(r, "review-edit", pageState{Mode: "edit", PageID: 42, DraftID: "draft-1"}))
	if err != nil || result.View == nil || wire.CheckView(result.View) != nil {
		t.Fatalf("review: %v %+v", err, result)
	}
	result, err = a.action(t.Context(), withAction(r, "confirm-edit", pageState{Mode: "review-edit", PageID: 42, DraftID: "draft-1", Name: "Reading context", Markdown: "保留来源上下文。"}))
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
	item := samplePage()
	item.Markdown = strings.Repeat("context\n", 2000)
	v := detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{PageID: 42, DraftID: "draft-1"}, item, item.Markdown)
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
	item.URL = "http://docs.example.com/insecure"
	v = detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{PageID: 42}, item, "text")
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
	f := newFakeBookStack(t)
	f.failWrite = true
	a := f.app()
	if _, err := a.doAction(t.Context(), "network-1", "bookstack_create_page", []byte(`{"book_id":7,"name":"Note","markdown":"Context"}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
	if len(f.creates) != 0 {
		t.Fatal("failed write was counted as successful")
	}
	if _, err := fmt.Fprint(os.Stderr, ""); err != nil {
		t.Fatal(err)
	}
}
