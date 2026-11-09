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

const testToken = "synthetic-redmine-token"

type fakeRedmine struct {
	srv       *httptest.Server
	mu        sync.Mutex
	paths     []string
	creates   []map[string]any
	failWrite bool
}

func sampleIssue() issue {
	return issue{ID: 42, Subject: "Preserve reading context", Description: "Keep source context beside the next step.", Project: namedResource{ID: 7, Name: "Reading"}, Tracker: namedResource{ID: 1, Name: "Feature"}, Status: namedResource{ID: 1, Name: "New"}, Priority: namedResource{ID: 2, Name: "Normal"}, DueDate: "2026-11-30", DoneRatio: 20, UpdatedOn: "2026-11-10T10:00:00Z", Journals: []journal{{ID: 1, User: namedResource{ID: 3, Name: "Example Reviewer"}, Notes: "Keep the original context.", CreatedOn: "2026-11-10T11:00:00Z"}}}
}

func newFakeRedmine(t *testing.T) *fakeRedmine {
	f := &fakeRedmine{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Redmine-API-Key") != testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/issues.json":
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
				item := sampleIssue()
				item.ID = 43
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(map[string]any{"issue": item})
				return
			}
			if r.URL.Query().Get("assigned_to_id") != "me" || r.URL.Query().Get("status_id") != "open" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			items := []issue{sampleIssue()}
			if r.URL.Query().Get("offset") != "0" {
				items = nil
			}
			_ = json.NewEncoder(w).Encode(issuePage{Issues: items, TotalCount: 51, Limit: 50})
		case "/issues/42.json":
			if r.URL.Query().Get("include") != "journals" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"issue": sampleIssue()})
		case "/projects.json":
			_ = json.NewEncoder(w).Encode(projectPage{Projects: []namedResource{{ID: 7, Name: "Reading"}}, TotalCount: 1, Limit: 100})
		case "/projects/7.json":
			_ = json.NewEncoder(w).Encode(map[string]any{"project": namedResource{ID: 7, Name: "Reading"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRedmine) app() *app {
	return &app{client: newClient(f.srv.URL, testToken), receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeRedmine(t).app())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "redmine" || len(m.Secrets) != 1 || m.Secrets[0].Name != "api_key" {
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
	for raw, want := range map[string]string{"https://redmine.example.com": "https://redmine.example.com", "http://127.0.0.1:3000/": "http://127.0.0.1:3000"} {
		got, err := instanceURL(raw)
		if err != nil || got != want {
			t.Fatalf("%q: %v %q", raw, err, got)
		}
	}
	for _, raw := range []string{"http://redmine.example.com", "https://me@example.com", "not-a-url"} {
		if _, err := instanceURL(raw); err == nil {
			t.Fatal("accepted " + raw)
		}
	}
}

func TestAssignedIssuesJournalsProjectsAndPagination(t *testing.T) {
	f := newFakeRedmine(t)
	a := f.app()
	text, err := a.callTool(t.Context(), "redmine_issues", json.RawMessage(`{"offset":50}`))
	if err != nil || !strings.Contains(text, "No open issues") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "redmine_issue", json.RawMessage(`{"issue_id":42}`))
	if err != nil || !strings.Contains(text, "Keep the original context") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "redmine_projects", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(text, "Project #7") {
		t.Fatalf("%v %s", err, text)
	}
	f.mu.Lock()
	paths := strings.Join(f.paths, "\n")
	f.mu.Unlock()
	for _, want := range []string{"assigned_to_id=me", "status_id=open", "offset=50", "include=journals"} {
		if !strings.Contains(paths, want) {
			t.Fatalf("missing %q in %s", want, paths)
		}
	}
}

func TestCreateReplayAndPayload(t *testing.T) {
	f := newFakeRedmine(t)
	a := f.app()
	args := []byte(`{"project_id":7,"subject":"Keep reading context","description":"Preserve source context."}`)
	for range 2 {
		if _, err := a.doAction(t.Context(), "create-1", "redmine_create_issue", args); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.creates) != 1 {
		t.Fatal(f.creates)
	}
	issue, ok := f.creates[0]["issue"].(map[string]any)
	if !ok || issue["project_id"] != float64(7) || issue["subject"] != "Keep reading context" {
		t.Fatal(f.creates)
	}
	if _, err := a.doAction(t.Context(), "create-1", "redmine_create_issue", []byte(`{"project_id":7,"subject":"Different"}`)); err == nil {
		t.Fatal("receipt accepted a different issue")
	}
	if _, err := describeAction("redmine_create_issue", args); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentReplayAndUnknownOutcome(t *testing.T) {
	f := newFakeRedmine(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"project_id":7,"subject":"Keep reading context","description":"Preserve source context."}`)
	if _, err := a.doAction(t.Context(), "persist-1", "redmine_create_issue", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persist-1", "redmine_create_issue", args); err != nil {
		t.Fatal(err)
	}
	if len(f.creates) != 1 {
		t.Fatal("restart repeated creation")
	}
	key, _ := json.Marshal(createArgs{ProjectID: 7, Subject: "Keep reading context", Description: "Preserve source context."})
	raw, _ := json.Marshal(map[string]writeReceipt{"persist-1": {Key: string(key), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "persist-1", "redmine_create_issue", args); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown create was retried")
	}
	if len(f.creates) != 1 {
		t.Fatal("unknown create caused another write")
	}
}

func TestLocalizedViewsAndCreateFlow(t *testing.T) {
	f := newFakeRedmine(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			for _, mode := range []string{"", "detail", "projects", "create", "review-create"} {
				r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
				st := pageState{Mode: mode, IssueID: 42, ProjectID: 7, ProjectName: "Reading", DraftID: "draft-1", Subject: "Keep context", Description: "Preserve the source."}
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
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6, Values: map[string]string{"subject": "Keep context", "description": "保留来源上下文。"}}}
	result, err := a.action(t.Context(), withAction(r, "review-create", pageState{Mode: "create", ProjectID: 7, DraftID: "draft-1"}))
	if err != nil || result.View == nil || wire.CheckView(result.View) != nil {
		t.Fatalf("review: %v %+v", err, result)
	}
	result, err = a.action(t.Context(), withAction(r, "confirm-create", pageState{Mode: "review-create", ProjectID: 7, DraftID: "draft-1", Subject: "Keep context", Description: "保留来源上下文。"}))
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
	item := sampleIssue()
	item.Description = strings.Repeat("context\n", 2000)
	v := detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{IssueID: 42}, item)
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

func TestFailedFinalWriteIsUnknown(t *testing.T) {
	f := newFakeRedmine(t)
	f.failWrite = true
	a := f.app()
	if _, err := a.doAction(t.Context(), "network-1", "redmine_create_issue", []byte(`{"project_id":7,"subject":"Keep context"}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
	if len(f.creates) != 0 {
		t.Fatal("failed write was counted as successful")
	}
	if _, err := fmt.Fprint(os.Stderr, ""); err != nil {
		t.Fatal(err)
	}
}
