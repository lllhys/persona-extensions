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

const testToken = "synthetic-shortcut-token"

type fakeShortcut struct {
	srv       *httptest.Server
	mu        sync.Mutex
	paths     []string
	creates   []map[string]any
	updates   []map[string]any
	failWrite bool
}

func sampleStory() story {
	return story{ID: 42, Name: "Preserve reading context", Description: "Keep source context beside the next step.", AppURL: "https://app.shortcut.com/example/story/42", StoryType: "feature", Deadline: "2026-11-30", WorkflowID: 1, WorkflowStateID: 10, OwnerIDs: []string{"member-1"}, Comments: []comment{{ID: 1, Text: "Keep the original context.", AuthorID: "member-2"}, {ID: 2, Text: "Deleted text", Deleted: true}}}
}

func sampleWorkflows() []workflow {
	return []workflow{{ID: 1, Name: "Default", DefaultStateID: 10, States: []state{{ID: 10, Name: "Backlog", Type: "unstarted"}, {ID: 11, Name: "In Progress", Type: "started"}, {ID: 12, Name: "Done", Type: "done"}}}, {ID: 2, Name: "Other", DefaultStateID: 20, States: []state{{ID: 20, Name: "Other Backlog", Type: "unstarted"}}}}
}

func newFakeShortcut(t *testing.T) *fakeShortcut {
	f := &fakeShortcut{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Shortcut-Token") != testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/member":
			_ = json.NewEncoder(w).Encode(member{ID: "member-1", MentionName: "reader", Name: "Example User"})
		case "/search/stories":
			if r.URL.Query().Get("query") != "owner:reader is:story !is:done" || r.URL.Query().Get("detail") != "slim" || r.URL.Query().Get("entity_types") != "story" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			page := storyPage{Data: []story{sampleStory()}, Next: "next-token"}
			if r.URL.Query().Get("next") == "next-token" {
				page.Data = nil
				page.Next = ""
			}
			_ = json.NewEncoder(w).Encode(page)
		case "/stories/42":
			if r.Method == http.MethodPut {
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
				item := sampleStory()
				stateID, _ := in["workflow_state_id"].(float64)
				item.WorkflowStateID = int64(stateID)
				_ = json.NewEncoder(w).Encode(item)
				return
			}
			_ = json.NewEncoder(w).Encode(sampleStory())
		case "/stories":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			var in map[string]any
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			owners, ok := in["owner_ids"].([]any)
			if !ok || len(owners) != 1 || owners[0] != "member-1" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			f.creates = append(f.creates, in)
			f.mu.Unlock()
			item := sampleStory()
			item.ID = 43
			item.Name, _ = in["name"].(string)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(item)
		case "/workflows":
			_ = json.NewEncoder(w).Encode(sampleWorkflows())
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeShortcut) app() *app {
	return &app{client: newClient(f.srv.URL, testToken), receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeShortcut(t).app())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "shortcut" || len(m.Secrets) != 1 || m.Secrets[0].Name != "api_token" {
		t.Fatalf("%+v", m)
	}
	if len(m.Contributes.Connectors[0].ConfirmAlways) != 2 {
		t.Fatal("create and update must be confirmed")
	}
	if !slices.Equal(m.Permissions.Network, []string{"api.app.shortcut.com"}) {
		t.Fatal(m.Permissions.Network)
	}
}

func TestStoriesSearchPaginationAndDetails(t *testing.T) {
	f := newFakeShortcut(t)
	a := f.app()
	text, err := a.callTool(t.Context(), "shortcut_stories", json.RawMessage(`{"next":"next-token"}`))
	if err != nil || !strings.Contains(text, "No incomplete stories") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "shortcut_story", json.RawMessage(`{"story_id":42}`))
	if err != nil || !strings.Contains(text, "Keep the original context") || strings.Contains(text, "Deleted text") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "shortcut_workflows", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(text, "State 11") {
		t.Fatalf("%v %s", err, text)
	}
	f.mu.Lock()
	paths := strings.Join(f.paths, "\n")
	f.mu.Unlock()
	for _, want := range []string{"owner%3Areader+is%3Astory+%21is%3Adone", "next=next-token", "detail=slim", "entity_types=story"} {
		if !strings.Contains(paths, want) {
			t.Fatalf("missing %q in %s", want, paths)
		}
	}
}

func TestCreateAndStateUpdateReplay(t *testing.T) {
	f := newFakeShortcut(t)
	a := f.app()
	create := []byte(`{"name":"Keep reading context","description":"Preserve the source.","workflow_state_id":10,"story_type":"feature","deadline":"2026-11-30"}`)
	for range 2 {
		if _, err := a.doAction(t.Context(), "create-1", "shortcut_create_story", create); err != nil {
			t.Fatal(err)
		}
	}
	update := []byte(`{"story_id":42,"workflow_state_id":11}`)
	for range 2 {
		if _, err := a.doAction(t.Context(), "update-1", "shortcut_update_story_state", update); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.creates) != 1 || len(f.updates) != 1 {
		t.Fatalf("%v %v", f.creates, f.updates)
	}
	if len(f.updates[0]) != 1 || f.updates[0]["workflow_state_id"] != float64(11) {
		t.Fatal("state update changed other fields")
	}
	if _, err := a.doAction(t.Context(), "update-1", "shortcut_update_story_state", []byte(`{"story_id":42,"workflow_state_id":12}`)); err == nil {
		t.Fatal("receipt accepted another state")
	}
	if _, err := a.doAction(t.Context(), "wrong-state", "shortcut_update_story_state", []byte(`{"story_id":42,"workflow_state_id":20}`)); err == nil || len(f.updates) != 1 {
		t.Fatal("cross-workflow state was accepted")
	}
	if _, err := a.doAction(t.Context(), "wrong-create", "shortcut_create_story", []byte(`{"name":"bad state","workflow_state_id":999}`)); err == nil || len(f.creates) != 1 {
		t.Fatal("unknown state was accepted")
	}
	if _, err := describeAction("shortcut_create_story", create); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"name":"bad","workflow_state_id":10,"deadline":"2026-02-30"}`, `{"name":"bad","workflow_state_id":10,"story_type":"other"}`} {
		if _, err := describeAction("shortcut_create_story", []byte(raw)); err == nil {
			t.Fatal("accepted invalid create " + raw)
		}
	}
}

func TestPersistentReplayAndUnknownOutcome(t *testing.T) {
	f := newFakeShortcut(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"story_id":42,"workflow_state_id":11}`)
	if _, err := a.doAction(t.Context(), "persist-1", "shortcut_update_story_state", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persist-1", "shortcut_update_story_state", args); err != nil {
		t.Fatal(err)
	}
	if len(f.updates) != 1 {
		t.Fatal("restart repeated state update")
	}
	key, _ := json.Marshal(updateArgs{StoryID: 42, WorkflowStateID: 11})
	raw, _ := json.Marshal(map[string]writeReceipt{"persist-1": {Key: string(key), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "persist-1", "shortcut_update_story_state", args); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown state update was retried")
	}
	if len(f.updates) != 1 {
		t.Fatal("unknown state update caused another write")
	}
}

func TestLocalizedViewsAndActions(t *testing.T) {
	f := newFakeShortcut(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			for _, mode := range []string{"", "detail", "create", "review-create", "edit", "review-edit"} {
				r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
				st := pageState{Mode: mode, StoryID: 42, DraftID: "draft-1", Name: "Keep context", Description: "Preserve the source.", WorkflowStateID: 11, StoryType: "feature", Deadline: "2026-11-30"}
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
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6, Values: map[string]string{"state_id": "11"}}}
	result, err := a.action(t.Context(), withAction(r, "review-edit", pageState{Mode: "edit", StoryID: 42, WorkflowStateID: 10, DraftID: "draft-1"}))
	if err != nil || result.View == nil || wire.CheckView(result.View) != nil {
		t.Fatalf("review: %v %+v", err, result)
	}
	result, err = a.action(t.Context(), withAction(r, "confirm-edit", pageState{Mode: "review-edit", StoryID: 42, WorkflowStateID: 11, DraftID: "draft-1"}))
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
	item := sampleStory()
	item.Description = strings.Repeat("context ", 3000)
	v := detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{StoryID: 42, DraftID: "draft-1"}, item, sampleWorkflows())
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
	item.AppURL = "http://app.shortcut.com/story/42"
	v = detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{StoryID: 42}, item, sampleWorkflows())
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

func TestNetworkFailureIsUnknown(t *testing.T) {
	f := newFakeShortcut(t)
	f.failWrite = true
	a := f.app()
	if _, err := a.doAction(t.Context(), "network-1", "shortcut_update_story_state", []byte(`{"story_id":42,"workflow_state_id":11}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
	if len(f.updates) != 0 {
		t.Fatal("failed write was counted as successful")
	}
	if _, err := fmt.Fprint(os.Stderr, ""); err != nil {
		t.Fatal(err)
	}
}
