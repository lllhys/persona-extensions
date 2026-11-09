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

const testToken = "synthetic-memos-token"

type fakeMemos struct {
	srv     *httptest.Server
	mu      sync.Mutex
	paths   []string
	creates []map[string]any
	updates map[string][]map[string]any
}

func sampleMemo() memo {
	return memo{Name: "memos/memo-1", Creator: "users/reader", Content: "# Reading notes\n\nKeep source context beside the next step.", Visibility: "PRIVATE", State: "NORMAL", Pinned: false, Tags: []string{"reading"}, Snippet: "Reading notes", CreateTime: "2026-11-09T10:00:00Z", UpdateTime: "2026-11-10T10:00:00Z"}
}

func newFakeMemos(t *testing.T) *fakeMemos {
	f := &fakeMemos{updates: map[string][]map[string]any{}}
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
		case "/api/memos":
			if r.Method == http.MethodPost {
				var in map[string]any
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in["visibility"] != "PRIVATE" || in["state"] != "NORMAL" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				f.mu.Lock()
				f.creates = append(f.creates, in)
				f.mu.Unlock()
				item := sampleMemo()
				item.Name = "memos/created-1"
				item.Content, _ = in["content"].(string)
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(item)
				return
			}
			filter := r.URL.Query().Get("filter")
			if !strings.Contains(filter, `creator == "users/reader"`) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			page := memoPage{Memos: []memo{sampleMemo()}, NextPageToken: "next-page"}
			if r.URL.Query().Get("pageToken") == "next-page" {
				page.Memos = nil
				page.NextPageToken = ""
			}
			_ = json.NewEncoder(w).Encode(page)
		case "/api/memos/memo-1":
			if r.Method == http.MethodPatch {
				var in map[string]any
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				f.mu.Lock()
				f.updates[r.URL.Query().Get("updateMask")] = append(f.updates[r.URL.Query().Get("updateMask")], in)
				f.mu.Unlock()
				item := sampleMemo()
				if content, _ := in["content"].(string); content != "" {
					item.Content = content
				}
				if pinned, ok := in["pinned"].(bool); ok {
					item.Pinned = pinned
				}
				_ = json.NewEncoder(w).Encode(item)
				return
			}
			_ = json.NewEncoder(w).Encode(sampleMemo())
		case "/api/memos/public-memo":
			item := sampleMemo()
			item.Name, item.Creator = "memos/public-memo", "users/someone-else"
			_ = json.NewEncoder(w).Encode(item)
		case "/api/users/reader":
			_ = json.NewEncoder(w).Encode(user{Name: "users/reader", Username: "reader", DisplayName: "Example User", State: "NORMAL"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMemos) app() *app {
	return &app{client: newClient(f.srv.URL, testToken), username: "reader", creator: "users/reader", receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeMemos(t).app())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "memos" || len(m.Secrets) != 1 || m.Secrets[0].Name != "access_token" {
		t.Fatalf("%+v", m)
	}
	if len(m.Contributes.Connectors[0].ConfirmAlways) != 3 {
		t.Fatal("create, update and pin must be confirmed")
	}
	if !slices.Equal(m.Permissions.Network, []string{}) {
		t.Fatal(m.Permissions.Network)
	}
}

func TestInstanceAndUsernameValidation(t *testing.T) {
	for raw, want := range map[string]string{"https://memos.example.com": "https://memos.example.com", "http://127.0.0.1:5230/": "http://127.0.0.1:5230"} {
		got, err := instanceURL(raw)
		if err != nil || got != want {
			t.Fatalf("%q: %v %q", raw, err, got)
		}
	}
	for _, raw := range []string{"http://memos.example.com", "https://me@example.com", "not-a-url"} {
		if _, err := instanceURL(raw); err == nil {
			t.Fatal("accepted " + raw)
		}
	}
	for _, value := range []string{"", "reader/name", `reader"name`, "reader\nname"} {
		if err := validUsername(value); err == nil {
			t.Fatal("accepted " + value)
		}
	}
}

func TestPersonalFilteringSearchPaginationAndDetail(t *testing.T) {
	f := newFakeMemos(t)
	a := f.app()
	text, err := a.callTool(t.Context(), "memos_list", json.RawMessage(`{"view":"search","query":"context"}`))
	if err != nil || !strings.Contains(text, "Next page token: next-page") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "memos_list", json.RawMessage(`{"view":"pinned","page_token":"next-page"}`))
	if err != nil || !strings.Contains(text, "No notes") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "memos_get", json.RawMessage(`{"memo_id":"memo-1"}`))
	if err != nil || !strings.Contains(text, "Keep source context") {
		t.Fatalf("%v %s", err, text)
	}
	if _, err := a.callTool(t.Context(), "memos_get", json.RawMessage(`{"memo_id":"public-memo"}`)); err == nil {
		t.Fatal("read another user's note as a personal note")
	}
	f.mu.Lock()
	paths := strings.Join(f.paths, "\n")
	f.mu.Unlock()
	for _, want := range []string{"content.contains%28%22context%22%29", "pinned+%3D%3D+true", "pageToken=next-page", "pinned+desc%2C+create_time+desc"} {
		if !strings.Contains(paths, want) {
			t.Fatalf("missing %q in %s", want, paths)
		}
	}
}

func TestCreateUpdatePinReplayAndOwnership(t *testing.T) {
	f := newFakeMemos(t)
	a := f.app()
	for range 2 {
		if _, err := a.doAction(t.Context(), "create-1", "memos_create", []byte(`{"content":"# Reading notes\n\nKeep context."}`)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := a.doAction(t.Context(), "update-1", "memos_update", []byte(`{"memo_id":"memo-1","content":"Updated context"}`)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := a.doAction(t.Context(), "pin-1", "memos_toggle_pin", []byte(`{"memo_id":"memo-1","pinned":true}`)); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.creates) != 1 || len(f.updates["content"]) != 1 || len(f.updates["pinned"]) != 1 {
		t.Fatalf("%v %v", f.creates, f.updates)
	}
	if len(f.updates["content"][0]) != 1 || len(f.updates["pinned"][0]) != 1 {
		t.Fatal("partial update sent unrelated fields")
	}
	for _, name := range []string{"memos_update", "memos_toggle_pin"} {
		args := []byte(`{"memo_id":"public-memo","content":"overwrite"}`)
		if name == "memos_toggle_pin" {
			args = []byte(`{"memo_id":"public-memo","pinned":true}`)
		}
		if _, err := a.doAction(t.Context(), "foreign-"+name, name, args); err == nil {
			t.Fatal("accepted another user's memo " + name)
		}
	}
	if len(f.creates) != 1 || len(f.updates["content"]) != 1 || len(f.updates["pinned"]) != 1 {
		t.Fatal("foreign write reached remote API")
	}
}

func TestPersistentReplayAndUnknownOutcome(t *testing.T) {
	f := newFakeMemos(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"memo_id":"memo-1","pinned":true}`)
	if _, err := a.doAction(t.Context(), "persist-1", "memos_toggle_pin", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persist-1", "memos_toggle_pin", args); err != nil {
		t.Fatal(err)
	}
	if len(f.updates["pinned"]) != 1 {
		t.Fatal("restart repeated pin")
	}
	key, _ := json.Marshal(pinArgs{MemoID: "memo-1", Pinned: true})
	raw, _ := json.Marshal(map[string]writeReceipt{"persist-1": {Key: string(key), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "persist-1", "memos_toggle_pin", args); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown pin was retried")
	}
	if len(f.updates["pinned"]) != 1 {
		t.Fatal("unknown pin caused another write")
	}
}

func TestLocalizedViewsAndEditFlow(t *testing.T) {
	f := newFakeMemos(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			for _, mode := range []string{"", "detail", "create", "review-create", "edit", "review-edit"} {
				r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
				st := pageState{Mode: mode, MemoID: "memo-1", DraftID: "draft-1", Content: "# Notes\n\nKeep context."}
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
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6, Values: map[string]string{"content": "保留来源上下文。"}}}
	result, err := a.action(t.Context(), withAction(r, "review-edit", pageState{Mode: "edit", MemoID: "memo-1", DraftID: "draft-1"}))
	if err != nil || result.View == nil || wire.CheckView(result.View) != nil {
		t.Fatalf("review: %v %+v", err, result)
	}
	result, err = a.action(t.Context(), withAction(r, "confirm-edit", pageState{Mode: "review-edit", MemoID: "memo-1", DraftID: "draft-1", Content: "保留来源上下文。"}))
	if err != nil || !result.Pop || len(f.updates["content"]) != 1 {
		t.Fatalf("confirm: %v %+v %v", err, result, f.updates)
	}
}

func withAction(r *ext.UIRequest, action string, st pageState) *ext.UIRequest {
	r.Action = action
	r.State = ext.EncodeState(st)
	return r
}

func TestBoundsAndNoConfig(t *testing.T) {
	item := sampleMemo()
	item.Content = strings.Repeat("context\n", 2000)
	v := detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{MemoID: "memo-1", DraftID: "draft-1"}, item)
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
	f := newFakeMemos(t)
	f.srv.Close()
	a := f.app()
	if _, err := a.doAction(t.Context(), "network-1", "memos_create", []byte(`{"content":"Note"}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
	if len(f.creates) != 0 {
		t.Fatal("failed write was counted as successful")
	}
	if _, err := fmt.Fprint(os.Stderr, ""); err != nil {
		t.Fatal(err)
	}
}
