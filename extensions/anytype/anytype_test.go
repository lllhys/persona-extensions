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

const testToken = "synthetic-anytype-key"

type fakeAnytype struct {
	srv       *httptest.Server
	mu        sync.Mutex
	paths     []string
	creates   []map[string]any
	failWrite bool
}

func sampleObject() knowledgeObject {
	return knowledgeObject{ID: "object-1", Name: "Reading context", Snippet: "Keep the source", Markdown: "# Reading context\n\nKeep source context beside the next step.", SpaceID: "space-1", Layout: "basic", Type: objectType{ID: "type-page", TypeKey: "page", Name: "Page", Layout: "basic"}}
}

func pageOf[T any](items []T, more bool) page[T] {
	var out page[T]
	out.Data = items
	out.Pagination.HasMore = more
	out.Pagination.Total = len(items)
	return out
}

func newFakeAnytype(t *testing.T) *fakeAnytype {
	f := &fakeAnytype{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken || r.Header.Get("Anytype-Version") != "2025-11-08" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/spaces":
			items := []space{{ID: "space-1", Name: "Personal"}}
			if r.URL.Query().Get("offset") != "0" {
				items = nil
			}
			_ = json.NewEncoder(w).Encode(pageOf(items, false))
		case "/v1/spaces/space-1/objects":
			if r.Method == http.MethodPost {
				f.mu.Lock()
				fail := f.failWrite
				f.mu.Unlock()
				if fail {
					http.Error(w, "synthetic interrupted write", http.StatusServiceUnavailable)
					return
				}
				var in map[string]any
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in["type_key"] != "page" && in["type_key"] != "note" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				f.mu.Lock()
				f.creates = append(f.creates, in)
				f.mu.Unlock()
				item := sampleObject()
				item.ID, item.Name = "created-object", in["name"].(string)
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(item)
				return
			}
			_ = json.NewEncoder(w).Encode(pageOf([]knowledgeObject{sampleObject()}, false))
		case "/v1/spaces/space-1/objects/object-1":
			_ = json.NewEncoder(w).Encode(sampleObject())
		case "/v1/spaces/space-1/types":
			_ = json.NewEncoder(w).Encode(pageOf([]objectType{{ID: "type-page", TypeKey: "page", Name: "Page", Layout: "basic"}, {ID: "type-note", TypeKey: "note", Name: "Note", Layout: "note"}}, false))
		case "/v1/search":
			var in map[string]any
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in["query"] == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(pageOf([]knowledgeObject{sampleObject()}, false))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAnytype) app() *app {
	return &app{client: newClient(f.srv.URL, testToken), receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeAnytype(t).app())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "anytype" || len(m.Secrets) != 1 || m.Secrets[0].Name != "api_key" {
		t.Fatalf("%+v", m)
	}
	if len(m.Contributes.Connectors[0].ConfirmAlways) != 1 {
		t.Fatal("create must be confirmed")
	}
	if !slices.Equal(m.Permissions.Network, []string{}) {
		t.Fatal(m.Permissions.Network)
	}
}

func TestInstanceURLValidation(t *testing.T) {
	for raw, want := range map[string]string{"http://127.0.0.1:31009": "http://127.0.0.1:31009", "https://anytype.example.com": "https://anytype.example.com"} {
		got, err := instanceURL(raw)
		if err != nil || got != want {
			t.Fatalf("%q: %v %q", raw, err, got)
		}
	}
	for _, raw := range []string{"http://anytype.example.com", "https://me@example.com", "not-a-url"} {
		if _, err := instanceURL(raw); err == nil {
			t.Fatal("accepted " + raw)
		}
	}
}

func TestSpacesObjectsSearchAndBody(t *testing.T) {
	f := newFakeAnytype(t)
	a := f.app()
	text, err := a.callTool(t.Context(), "anytype_spaces", json.RawMessage(`{"offset":50}`))
	if err != nil || !strings.Contains(text, "No accessible spaces") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "anytype_objects", json.RawMessage(`{"space_id":"space-1"}`))
	if err != nil || !strings.Contains(text, "object-1") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "anytype_search", json.RawMessage(`{"query":"context"}`))
	if err != nil || !strings.Contains(text, "Reading context") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "anytype_object", json.RawMessage(`{"space_id":"space-1","object_id":"object-1"}`))
	if err != nil || !strings.Contains(text, "Keep source context") {
		t.Fatalf("%v %s", err, text)
	}
	f.mu.Lock()
	paths := strings.Join(f.paths, "\n")
	f.mu.Unlock()
	for _, want := range []string{"offset=50", "limit=50", "/v1/search"} {
		if !strings.Contains(paths, want) {
			t.Fatalf("missing %q in %s", want, paths)
		}
	}
}

func TestCreateReplayAndTypeValidation(t *testing.T) {
	f := newFakeAnytype(t)
	a := f.app()
	args := []byte(`{"space_id":"space-1","type_key":"page","name":"Reading note","body":"# Note\n\nKeep context."}`)
	for range 2 {
		if _, err := a.doAction(t.Context(), "create-1", "anytype_create_object", args); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.creates) != 1 {
		t.Fatal(f.creates)
	}
	if _, err := a.doAction(t.Context(), "create-1", "anytype_create_object", []byte(`{"space_id":"space-1","type_key":"page","name":"Different","body":"Other"}`)); err == nil {
		t.Fatal("receipt accepted a different object")
	}
	if _, err := describeAction("anytype_create_object", []byte(`{"space_id":"space-1","type_key":"task","name":"Bad","body":"Bad"}`)); err == nil {
		t.Fatal("accepted arbitrary type key")
	}
	if _, err := describeAction("anytype_create_object", args); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentReplayAndUnknownOutcome(t *testing.T) {
	f := newFakeAnytype(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"space_id":"space-1","type_key":"page","name":"Reading note","body":"# Note\n\nKeep context."}`)
	if _, err := a.doAction(t.Context(), "persist-1", "anytype_create_object", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persist-1", "anytype_create_object", args); err != nil {
		t.Fatal(err)
	}
	if len(f.creates) != 1 {
		t.Fatal("restart repeated creation")
	}
	key, _ := json.Marshal(createArgs{SpaceID: "space-1", TypeKey: "page", Name: "Reading note", Body: "# Note\n\nKeep context."})
	raw, _ := json.Marshal(map[string]writeReceipt{"persist-1": {Key: string(key), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "persist-1", "anytype_create_object", args); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown create was retried")
	}
	if len(f.creates) != 1 {
		t.Fatal("unknown create caused another write")
	}
}

func TestLocalizedViewsAndCreateFlow(t *testing.T) {
	f := newFakeAnytype(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			for _, mode := range []string{"", "objects", "detail", "search", "create", "review-create"} {
				r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
				st := pageState{Mode: mode, SpaceID: "space-1", SpaceName: "Personal", ObjectID: "object-1", Query: "context", DraftID: "draft-1", Name: "Reading note", Body: "# Note\n\nKeep context.", TypeKey: "page"}
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
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6, Values: map[string]string{"name": "Reading note", "body": "保留来源上下文。", "type_key": "page"}}}
	result, err := a.action(t.Context(), withAction(r, "review-create", pageState{Mode: "create", SpaceID: "space-1", DraftID: "draft-1"}))
	if err != nil || result.View == nil || wire.CheckView(result.View) != nil {
		t.Fatalf("review: %v %+v", err, result)
	}
	result, err = a.action(t.Context(), withAction(r, "confirm-create", pageState{Mode: "review-create", SpaceID: "space-1", DraftID: "draft-1", Name: "Reading note", Body: "保留来源上下文。", TypeKey: "page"}))
	if err != nil || !result.Pop || len(f.creates) != 1 {
		t.Fatalf("confirm: %v %+v %v", err, result, f.creates)
	}
}

func withAction(r *ext.UIRequest, action string, st pageState) *ext.UIRequest {
	r.Action = action
	r.State = ext.EncodeState(st)
	return r
}

func TestBoundsAndNoConfig(t *testing.T) {
	item := sampleObject()
	item.Markdown = strings.Repeat("context\n", 2000)
	v := detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{SpaceID: "space-1", ObjectID: "object-1"}, item)
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
	a := newApp()
	if a.status()["state"] != "not_configured" {
		t.Fatal(a.status())
	}
	v, err := a.render(t.Context(), &ext.UIRequest{})
	if err != nil || !strings.Contains(v.Sections[0].Items[0].Title, "API key") {
		t.Fatalf("%v %+v", err, v)
	}
}

func TestFailedFinalWriteIsUnknown(t *testing.T) {
	f := newFakeAnytype(t)
	f.failWrite = true
	a := f.app()
	if _, err := a.doAction(t.Context(), "network-1", "anytype_create_object", []byte(`{"space_id":"space-1","type_key":"page","name":"Note","body":"Context"}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
	if len(f.creates) != 0 {
		t.Fatal("failed write was counted as successful")
	}
	if _, err := fmt.Fprint(os.Stderr, ""); err != nil {
		t.Fatal(err)
	}
}
