package main

import (
	"encoding/json"
	"fmt"
	"io"
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

const testToken = "synthetic-todoist-token"

type fakeTodoist struct {
	srv      *httptest.Server
	mu       sync.Mutex
	paths    []string
	bodies   []map[string]any
	closeIDs []string
}

func newFakeTodoist(t *testing.T) *fakeTodoist {
	t.Helper()
	// Page and tool requests in these tests use Asia/Shanghai, not the runner's local calendar date.
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeTodoist{}
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
		case "/tasks":
			if r.Method == http.MethodPost {
				var in map[string]any
				_ = json.NewDecoder(io.LimitReader(r.Body, 65536)).Decode(&in)
				f.mu.Lock()
				f.bodies = append(f.bodies, in)
				f.mu.Unlock()
				fmt.Fprint(w, `{"id":"created-opaque-id","content":"`+fmt.Sprint(in["content"])+`"}`)
				return
			}
			cursor := r.URL.Query().Get("cursor")
			if cursor == "" {
				fmt.Fprint(w, `{"results":[{"id":"opaque-long-task-1","content":"Prepare weekly review","description":"Full task context","project_id":"project-work","section_id":"section-planning","labels":["deep-work"],"priority":1,"due":{"date":"`+time.Now().In(loc).Format("2006-01-02")+`","recurring":true}},{"id":"task-overdue","content":"Finish earlier draft","due":{"date":"2000-01-01","recurring":false}}],"next_cursor":"next-page"}`)
			} else {
				fmt.Fprint(w, `{"results":[],"next_cursor":""}`)
			}
		case "/projects":
			fmt.Fprint(w, `{"results":[{"id":"project-work","name":"Work"},{"id":"project-personal","name":"Personal"}],"next_cursor":""}`)
		case "/sections":
			fmt.Fprint(w, `{"results":[{"id":"section-planning","project_id":"project-work","name":"Planning"}],"next_cursor":""}`)
		case "/labels":
			fmt.Fprint(w, `{"results":[{"id":"label-1","name":"deep-work"}],"next_cursor":""}`)
		case "/tasks/opaque-long-task-1":
			if r.Method == http.MethodPost {
				var in map[string]any
				_ = json.NewDecoder(io.LimitReader(r.Body, 65536)).Decode(&in)
				f.mu.Lock()
				f.bodies = append(f.bodies, in)
				f.mu.Unlock()
				fmt.Fprint(w, `{"id":"opaque-long-task-1","content":"Updated task"}`)
				return
			}
			fmt.Fprint(w, `{"id":"opaque-long-task-1","content":"Prepare weekly review","description":"Full task context","project_id":"project-work","section_id":"section-planning","labels":["deep-work"],"priority":1,"due":{"date":"2026-11-10","recurring":true}}`)
		case "/tasks/opaque-long-task-1/close":
			f.mu.Lock()
			f.closeIDs = append(f.closeIDs, "opaque-long-task-1")
			f.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTodoist) app() *app {
	return &app{client: newClient(f.srv.URL, testToken), receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeTodoist(t).app())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "todoist" || len(m.Secrets) != 1 || m.Secrets[0].Name != "api_token" {
		t.Fatalf("manifest %+v", m)
	}
	if !slices.Equal(m.Permissions.Network, []string{"api.todoist.com"}) {
		t.Fatal(m.Permissions.Network)
	}
	if len(m.Contributes.Connectors[0].ConfirmAlways) != 3 {
		t.Fatal("create, update and complete must all require confirmation")
	}
}

func TestCursorPaginationUsesOneRequestPerPage(t *testing.T) {
	f := newFakeTodoist(t)
	items, err := f.app().client.tasks(t.Context(), "", "", 6)
	if err != nil || len(items) != 2 {
		t.Fatalf("%v %+v", err, items)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.paths) != 2 || f.paths[0] != "/tasks?limit=50" || !strings.Contains(f.paths[1], "cursor=next-page") {
		t.Fatal(f.paths)
	}
}

func TestTasksAndTimeZoneFiltering(t *testing.T) {
	f := newFakeTodoist(t)
	a := f.app()
	r := &ext.UIRequest{UIParams: wire.UIParams{TZ: "Asia/Shanghai"}}
	text, err := a.callTool(t.Context(), r, "todoist_tasks", json.RawMessage(`{"view":"today"}`))
	if err != nil || !strings.Contains(text, "Prepare weekly review") || strings.Contains(text, "Finish earlier draft") {
		t.Fatalf("%v %s", err, text)
	}
	if !strings.Contains(text, "opaque-long-task-1") || !strings.Contains(text, "Work") || !strings.Contains(text, "Planning") || !strings.Contains(text, "recurring") {
		t.Fatal(text)
	}
	now := time.Date(2026, 11, 10, 0, 30, 0, 0, time.FixedZone("UTC+8", 8*3600))
	got := filterTasks([]task{{Content: "no date"}, {Content: "today", Due: &dueDate{Date: "2026-11-10"}}, {Content: "later", Due: &dueDate{Date: "2026-11-11"}}}, "today", "", now)
	if len(got) != 1 || got[0].Content != "today" {
		t.Fatal(got)
	}
}

func TestTaskDetailPreservesOpaqueID(t *testing.T) {
	f := newFakeTodoist(t)
	text, err := f.app().callTool(t.Context(), &ext.UIRequest{}, "todoist_task", json.RawMessage(`{"id":"opaque-long-task-1"}`))
	if err != nil || !strings.Contains(text, "Full task context") {
		t.Fatalf("%v %s", err, text)
	}
}

func TestPartialUpdateDoesNotEraseOtherFields(t *testing.T) {
	title := "New title"
	patch, err := (updateArgs{ID: "opaque-long-task-1", Content: &title}).patch()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(patch)
	if string(data) != `{"content":"New title"}` {
		t.Fatal(string(data))
	}
	f := newFakeTodoist(t)
	if _, err := f.app().doAction(t.Context(), "update-1", "todoist_update", []byte(`{"id":"opaque-long-task-1","content":"New title"}`)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) != 1 || len(f.bodies[0]) != 1 || f.bodies[0]["content"] != "New title" {
		t.Fatal(f.bodies)
	}
}

func TestCreateUsesTypedFieldsAndReceipt(t *testing.T) {
	f := newFakeTodoist(t)
	a := f.app()
	args := []byte(`{"content":"Plan next week","description":"Review milestones","priority":1,"due_date":"2026-11-15","labels":["planning"],"project_id":"project-work"}`)
	for range 2 {
		out, err := a.doAction(t.Context(), "create-1", "todoist_create", args)
		if err != nil || !strings.Contains(out["text"].(string), "created-opaque-id") {
			t.Fatalf("%v %+v", err, out)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) != 1 || f.bodies[0]["priority"] != float64(1) || f.bodies[0]["due_date"] != "2026-11-15" {
		t.Fatal(f.bodies)
	}
	if _, ok := f.bodies[0]["due_datetime"]; ok {
		t.Fatal("an omitted due field was sent")
	}
}

func TestCompleteConfirmationAndWrite(t *testing.T) {
	f := newFakeTodoist(t)
	a := f.app()
	description, err := a.describe("todoist_complete", []byte(`{"id":"opaque-long-task-1","content":"Prepare weekly review"}`))
	if err != nil || !strings.Contains(description["summary"].(string), "Prepare weekly review") {
		t.Fatalf("%v %+v", err, description)
	}
	before := len(f.paths)
	if _, err := a.describe("todoist_complete", []byte(`{"id":"opaque-long-task-1"}`)); err != nil {
		t.Fatal(err)
	}
	if len(f.paths) != before {
		t.Fatal("confirmation description fetched a task over the network")
	}
	if _, err := a.doAction(t.Context(), "close-1", "todoist_complete", []byte(`{"id":"opaque-long-task-1"}`)); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.closeIDs) != 1 {
		t.Fatal(f.closeIDs)
	}
}

func TestInvalidWritesDoNotCallAPI(t *testing.T) {
	f := newFakeTodoist(t)
	a := f.app()
	for _, tc := range []struct{ name, args string }{
		{"todoist_create", `{"content":""}`},
		{"todoist_create", `{"content":"Valid","priority":5}`},
		{"todoist_create", `{"content":"Valid","due_date":"2026-02-30"}`},
		{"todoist_update", `{"id":"opaque-long-task-1"}`},
		{"todoist_update", `{"id":"opaque-long-task-1","clear_due":true,"due_date":"2026-11-15"}`},
		{"todoist_update", `{"id":"opaque-long-task-1","due_date":"2026-11-15","due_datetime":"2026-11-15T09:00:00Z"}`},
		{"todoist_update", `{"id":"../other","content":"Wrong"}`},
		{"todoist_complete", `{"id":""}`},
	} {
		if _, err := a.doAction(t.Context(), "bad-"+tc.args, tc.name, []byte(tc.args)); err == nil {
			t.Fatal("accepted " + tc.args)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bodies) != 0 || len(f.closeIDs) != 0 {
		t.Fatal("invalid write reached API")
	}
}

func TestPersistentReceiptAndUnknownOutcome(t *testing.T) {
	f := newFakeTodoist(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"content":"Persistent task"}`)
	if _, err := a.doAction(t.Context(), "persistent-1", "todoist_create", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persistent-1", "todoist_create", args); err != nil {
		t.Fatal(err)
	}
	if len(f.bodies) != 1 {
		t.Fatal("restart repeated task creation")
	}
	if _, err := a.doAction(t.Context(), "persistent-1", "todoist_create", []byte(`{"content":"Different"}`)); err == nil {
		t.Fatal("receipt accepted different task content")
	}
	key, _ := json.Marshal(map[string]any{"action": "todoist_create", "args": createArgs{Content: "Unknown"}.body()})
	raw, _ := json.Marshal(map[string]writeReceipt{"unknown-1": {Key: string(key), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "unknown-1", "todoist_create", []byte(`{"content":"Unknown"}`)); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown result was silently repeated")
	}
	if len(f.bodies) != 1 {
		t.Fatal("unknown result caused a second POST")
	}
}

func TestLocalizedViewsAndCreateReview(t *testing.T) {
	f := newFakeTodoist(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui, TZ: "Asia/Shanghai"}}
			v, err := a.render(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}
			if err := wire.CheckView(v); err != nil {
				t.Fatalf("%s %d: %v", locale, ui, err)
			}
		}
	}
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6, Values: map[string]string{
		"content": "Plan next week", "description": "Read notes", "due_date": "2026-11-15", "priority": "1", "project_id": "project-work", "labels": "planning,review",
	}}}
	r.Action = "submit-create"
	res, err := a.action(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if res.View == nil || !strings.Contains(res.View.State, "review") {
		t.Fatal("create did not enter review")
	}
	if err := wire.CheckView(res.View); err != nil {
		t.Fatal(err)
	}
	r.State = res.View.State
	r.Action = "confirm-create"
	res, err = a.action(t.Context(), r)
	if err != nil || !res.Pop || res.Toast == "" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestDetailBoundsAndNoToken(t *testing.T) {
	a := newFakeTodoist(t).app()
	v := detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{TaskID: "opaque-long-task-1", Mode: "detail"}, task{ID: "opaque-long-task-1", Content: "Task", Description: strings.Repeat("note ", 1500)}, contextData{})
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
	a.client.token = ""
	v, err := a.render(t.Context(), &ext.UIRequest{})
	if err != nil || !strings.Contains(v.Sections[0].Items[0].Title, "token") {
		t.Fatalf("%v %+v", err, v)
	}
}
