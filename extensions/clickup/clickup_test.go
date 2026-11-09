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

const testToken = "pk_synthetic_clickup_token"

type fakeClickUp struct {
	srv       *httptest.Server
	mu        sync.Mutex
	paths     []string
	creates   []map[string]any
	updates   []map[string]any
	failWrite bool
}

func sampleTask() task {
	item := task{ID: "task-1", Name: "Review reading notes", Description: "Keep source context beside the next step.", DueDate: "1764547200000", DateUpdated: "1762710000000", URL: "https://app.clickup.com/t/task-1"}
	item.Status.Status, item.Status.Type = "to do", "open"
	item.List.ID, item.List.Name = "list-1", "Reading"
	item.Assignees = []user{{ID: 42, Username: "reader"}}
	return item
}

func newFakeClickUp(t *testing.T) *fakeClickUp {
	f := &fakeClickUp{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user":
			_ = json.NewEncoder(w).Encode(map[string]any{"user": user{ID: 42, Username: "reader"}})
		case "/team":
			_ = json.NewEncoder(w).Encode(teamPage{Teams: []team{{ID: "team-1", Name: "Personal"}}})
		case "/team/team-1/space":
			_ = json.NewEncoder(w).Encode(map[string]any{"spaces": []map[string]string{{"id": "space-1", "name": "Knowledge"}}})
		case "/team/team-1/task":
			if r.URL.Query().Get("assignees[]") != "42" || r.URL.Query().Get("order_by") != "updated" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			items := []task{sampleTask()}
			if r.URL.Query().Get("page") != "0" {
				items = nil
			}
			_ = json.NewEncoder(w).Encode(taskPage{Tasks: items, Last: len(items) == 0})
		case "/space/space-1/list":
			_ = json.NewEncoder(w).Encode(listPage{Lists: []taskList{{ID: "list-1", Name: "Reading"}, {ID: "list-2", Name: "Archived", Archived: true}}})
		case "/list/list-1":
			_ = json.NewEncoder(w).Encode(taskList{ID: "list-1", Name: "Reading"})
		case "/list/list-1/task":
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
			f.creates = append(f.creates, in)
			f.mu.Unlock()
			item := sampleTask()
			item.ID, item.Name = "created-task", in["name"].(string)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(item)
		case "/task/task-1":
			if r.Method == http.MethodPut {
				var in map[string]any
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				f.mu.Lock()
				f.updates = append(f.updates, in)
				f.mu.Unlock()
				item := sampleTask()
				item.Status.Status, _ = in["status"].(string)
				_ = json.NewEncoder(w).Encode(item)
				return
			}
			_ = json.NewEncoder(w).Encode(sampleTask())
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeClickUp) app() *app {
	return &app{client: newClient(f.srv.URL, testToken), receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeClickUp(t).app())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "clickup" || len(m.Secrets) != 1 || m.Secrets[0].Name != "api_token" {
		t.Fatalf("%+v", m)
	}
	if len(m.Contributes.Connectors[0].ConfirmAlways) != 2 {
		t.Fatal("create and status must be confirmed")
	}
	if !slices.Equal(m.Permissions.Network, []string{"api.clickup.com"}) {
		t.Fatal(m.Permissions.Network)
	}
}

func TestUserTeamSpacesListsAndAssignedTasks(t *testing.T) {
	f := newFakeClickUp(t)
	a := f.app()
	text, err := a.callTool(t.Context(), "clickup_teams", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(text, "ID 42") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "clickup_spaces", json.RawMessage(`{"team_id":"team-1"}`))
	if err != nil || !strings.Contains(text, "space-1") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "clickup_lists", json.RawMessage(`{"space_id":"space-1"}`))
	if err != nil || !strings.Contains(text, "list-1") || strings.Contains(text, "Archived") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "clickup_tasks", json.RawMessage(`{"team_id":"team-1","page":1}`))
	if err != nil || !strings.Contains(text, "No assigned open tasks") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "clickup_task", json.RawMessage(`{"task_id":"task-1"}`))
	if err != nil || !strings.Contains(text, "Keep source context") {
		t.Fatalf("%v %s", err, text)
	}
	f.mu.Lock()
	paths := strings.Join(f.paths, "\n")
	f.mu.Unlock()
	for _, want := range []string{"assignees%5B%5D=42", "page=1", "order_by=updated"} {
		if !strings.Contains(paths, want) {
			t.Fatalf("missing %q in %s", want, paths)
		}
	}
}

func TestCreateStatusReplayAndPartialWrites(t *testing.T) {
	f := newFakeClickUp(t)
	a := f.app()
	create := []byte(`{"list_id":"list-1","name":"Keep reading context","description":"Preserve source context."}`)
	for range 2 {
		if _, err := a.doAction(t.Context(), "create-1", "clickup_create_task", create); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := a.doAction(t.Context(), "status-1", "clickup_update_status", []byte(`{"task_id":"task-1","status":"in progress"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.creates) != 1 || len(f.updates) != 1 {
		t.Fatalf("%v %v", f.creates, f.updates)
	}
	assignees, ok := f.creates[0]["assignees"].([]any)
	if !ok || len(assignees) != 1 || assignees[0] != float64(42) {
		t.Fatal("created task is not assigned to current user")
	}
	if len(f.updates[0]) != 1 || f.updates[0]["status"] != "in progress" {
		t.Fatal("status update changed other fields")
	}
	if _, err := a.doAction(t.Context(), "status-1", "clickup_update_status", []byte(`{"task_id":"task-1","status":"done"}`)); err == nil {
		t.Fatal("receipt accepted another status")
	}
	if _, err := describeAction("clickup_create_task", create); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentReplayAndUnknownOutcome(t *testing.T) {
	f := newFakeClickUp(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"task_id":"task-1","status":"in progress"}`)
	if _, err := a.doAction(t.Context(), "persist-1", "clickup_update_status", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persist-1", "clickup_update_status", args); err != nil {
		t.Fatal(err)
	}
	if len(f.updates) != 1 {
		t.Fatal("restart repeated status")
	}
	key, _ := json.Marshal(statusArgs{TaskID: "task-1", Status: "in progress"})
	raw, _ := json.Marshal(map[string]writeReceipt{"persist-1": {Key: string(key), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "persist-1", "clickup_update_status", args); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown status was retried")
	}
	if len(f.updates) != 1 {
		t.Fatal("unknown status caused another write")
	}
}

func TestLocalizedViewsAndEditFlow(t *testing.T) {
	f := newFakeClickUp(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			for _, mode := range []string{"", "tasks", "detail", "spaces", "lists", "create", "review-create", "edit", "review-edit"} {
				r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
				st := pageState{Mode: mode, TeamID: "team-1", TeamName: "Personal", SpaceID: "space-1", SpaceName: "Knowledge", ListID: "list-1", ListName: "Reading", TaskID: "task-1", DraftID: "draft-1", Name: "Keep context", Description: "Preserve the source.", Status: "in progress"}
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
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6, Values: map[string]string{"status": "in progress"}}}
	result, err := a.action(t.Context(), withAction(r, "review-edit", pageState{Mode: "edit", TaskID: "task-1", DraftID: "draft-1"}))
	if err != nil || result.View == nil || wire.CheckView(result.View) != nil {
		t.Fatalf("review: %v %+v", err, result)
	}
	result, err = a.action(t.Context(), withAction(r, "confirm-edit", pageState{Mode: "review-edit", TaskID: "task-1", DraftID: "draft-1", Status: "in progress"}))
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
	item := sampleTask()
	item.Description = strings.Repeat("context\n", 2000)
	v := detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{TaskID: "task-1", DraftID: "draft-1"}, item)
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
	item.URL = "http://app.clickup.com/t/task-1"
	v = detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{TaskID: "task-1"}, item)
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
	f := newFakeClickUp(t)
	f.failWrite = true
	a := f.app()
	if _, err := a.doAction(t.Context(), "network-1", "clickup_create_task", []byte(`{"list_id":"list-1","name":"Keep context"}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
	if len(f.creates) != 0 {
		t.Fatal("failed write was counted as successful")
	}
	if _, err := fmt.Fprint(os.Stderr, ""); err != nil {
		t.Fatal(err)
	}
}
