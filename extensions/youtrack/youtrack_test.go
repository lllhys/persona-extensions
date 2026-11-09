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

const testToken = "synthetic-youtrack-token"

type fakeYouTrack struct {
	srv       *httptest.Server
	mu        sync.Mutex
	paths     []string
	creates   []map[string]any
	failWrite bool
}

func sampleIssue() issue {
	return issue{ID: "2-42", IDReadable: "READ-42", Summary: "Preserve reading context", Description: "Keep source context beside the next step.", Updated: 1762710000000, Project: project{ID: "0-7", Name: "Reading", ShortName: "READ"}, Reporter: &user{ID: "1-1", Name: "Example Author"}, CommentsCount: 1}
}

func newFakeYouTrack(t *testing.T) *fakeYouTrack {
	f := &fakeYouTrack{}
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
		case "/api/issues":
			if r.URL.Query().Get("query") != "for: me #Unresolved" || r.URL.Query().Get("$top") != "50" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			items := []issue{sampleIssue()}
			if r.URL.Query().Get("$skip") != "0" {
				items = nil
			}
			_ = json.NewEncoder(w).Encode(items)
		case "/api/issues/2-42":
			_ = json.NewEncoder(w).Encode(sampleIssue())
		case "/api/issues/2-42/comments":
			_ = json.NewEncoder(w).Encode([]comment{{ID: "4-1", Text: "Keep the original context.", Author: &user{Name: "Example Reviewer"}, Created: 1762710000000}, {ID: "4-2", Text: "Deleted text", Deleted: true}})
		case "/api/admin/projects":
			_ = json.NewEncoder(w).Encode([]project{{ID: "0-7", Name: "Reading", ShortName: "READ"}, {ID: "0-8", Name: "Archived", ShortName: "OLD", Archived: true}})
		case "/api/admin/projects/0-7":
			_ = json.NewEncoder(w).Encode(project{ID: "0-7", Name: "Reading", ShortName: "READ"})
		case "/api/admin/projects/0-7/issues":
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
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in["summary"] == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			f.creates = append(f.creates, in)
			f.mu.Unlock()
			item := sampleIssue()
			item.IDReadable, item.Summary = "READ-43", in["summary"].(string)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(item)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeYouTrack) app() *app {
	return &app{client: newClient(f.srv.URL, testToken), receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeYouTrack(t).app())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "youtrack" || len(m.Secrets) != 1 || m.Secrets[0].Name != "access_token" {
		t.Fatalf("%+v", m)
	}
	if len(m.Contributes.Connectors[0].ConfirmAlways) != 1 || m.Contributes.Connectors[0].ConfirmAlways[0] != "youtrack_create_issue" {
		t.Fatal("create must be confirmed")
	}
	if !slices.Equal(m.Permissions.Network, []string{}) {
		t.Fatal(m.Permissions.Network)
	}
}

func TestInstanceURLValidation(t *testing.T) {
	for raw, want := range map[string]string{"https://example.youtrack.cloud": "https://example.youtrack.cloud", "http://127.0.0.1:8080/": "http://127.0.0.1:8080"} {
		got, err := instanceURL(raw)
		if err != nil || got != want {
			t.Fatalf("%q: %v %q", raw, err, got)
		}
	}
	for _, raw := range []string{"http://example.youtrack.cloud", "https://me@example.com", "not-a-url"} {
		if _, err := instanceURL(raw); err == nil {
			t.Fatal("accepted " + raw)
		}
	}
}

func TestAssignedIssuesCommentsProjectsAndPagination(t *testing.T) {
	f := newFakeYouTrack(t)
	a := f.app()
	text, err := a.callTool(t.Context(), "youtrack_issues", json.RawMessage(`{"skip":50}`))
	if err != nil || !strings.Contains(text, "No unresolved issues") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "youtrack_issue", json.RawMessage(`{"issue_id":"2-42"}`))
	if err != nil || !strings.Contains(text, "Keep the original context") || strings.Contains(text, "Deleted text") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "youtrack_projects", json.RawMessage(`{}`))
	if err != nil || !strings.Contains(text, "0-7") || strings.Contains(text, "Archived") {
		t.Fatalf("%v %s", err, text)
	}
	f.mu.Lock()
	paths := strings.Join(f.paths, "\n")
	f.mu.Unlock()
	for _, want := range []string{"query=for%3A+me+%23Unresolved", "%24skip=50", "%24top=50", "fields=", "comments"} {
		if !strings.Contains(paths, want) {
			t.Fatalf("missing %q in %s", want, paths)
		}
	}
}

func TestCreateReplayAndArchivedProject(t *testing.T) {
	f := newFakeYouTrack(t)
	a := f.app()
	args := []byte(`{"project_id":"0-7","summary":"Keep reading context","description":"Preserve source context."}`)
	for range 2 {
		if _, err := a.doAction(t.Context(), "create-1", "youtrack_create_issue", args); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.creates) != 1 {
		t.Fatal(f.creates)
	}
	if _, err := a.doAction(t.Context(), "create-1", "youtrack_create_issue", []byte(`{"project_id":"0-7","summary":"Different"}`)); err == nil {
		t.Fatal("receipt accepted a different issue")
	}
	if _, err := describeAction("youtrack_create_issue", args); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentReplayAndUnknownOutcome(t *testing.T) {
	f := newFakeYouTrack(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"project_id":"0-7","summary":"Keep reading context","description":"Preserve source context."}`)
	if _, err := a.doAction(t.Context(), "persist-1", "youtrack_create_issue", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persist-1", "youtrack_create_issue", args); err != nil {
		t.Fatal(err)
	}
	if len(f.creates) != 1 {
		t.Fatal("restart repeated creation")
	}
	key, _ := json.Marshal(createArgs{ProjectID: "0-7", Summary: "Keep reading context", Description: "Preserve source context."})
	raw, _ := json.Marshal(map[string]writeReceipt{"persist-1": {Key: string(key), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "persist-1", "youtrack_create_issue", args); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown create was retried")
	}
	if len(f.creates) != 1 {
		t.Fatal("unknown create caused another write")
	}
}

func TestLocalizedViewsAndCreateFlow(t *testing.T) {
	f := newFakeYouTrack(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			for _, mode := range []string{"", "detail", "projects", "create", "review-create"} {
				r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
				st := pageState{Mode: mode, IssueID: "2-42", ProjectID: "0-7", ProjectName: "Reading", DraftID: "draft-1", Summary: "Keep context", Description: "Preserve the source."}
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
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6, Values: map[string]string{"project_id": "0-7", "summary": "Keep context", "description": "Preserve the source."}}}
	result, err := a.action(t.Context(), withAction(r, "review-create", pageState{Mode: "create", ProjectID: "0-7", DraftID: "draft-1"}))
	if err != nil || result.View == nil || wire.CheckView(result.View) != nil {
		t.Fatalf("review: %v %+v", err, result)
	}
	result, err = a.action(t.Context(), withAction(r, "confirm-create", pageState{Mode: "review-create", ProjectID: "0-7", DraftID: "draft-1", Summary: "Keep context", Description: "Preserve the source."}))
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
	v := detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{IssueID: "2-42"}, item, []comment{{Text: "Keep context"}})
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
	f := newFakeYouTrack(t)
	f.failWrite = true
	a := f.app()
	if _, err := a.doAction(t.Context(), "network-1", "youtrack_create_issue", []byte(`{"project_id":"0-7","summary":"Keep context"}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
	if len(f.creates) != 0 {
		t.Fatal("failed write was counted as successful")
	}
	if _, err := fmt.Fprint(os.Stderr, ""); err != nil {
		t.Fatal(err)
	}
}
