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

const testToken = "synthetic-jira-token"

type fakeJira struct {
	srv      *httptest.Server
	mu       sync.Mutex
	paths    []string
	creates  []map[string]any
	comments []map[string]any
}

func newFakeJira(t *testing.T) *fakeJira {
	f := &fakeJira{}
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
		case "/search/jql":
			// Like Jira Cloud's enhanced search: no total, startAt ignored, pages by nextPageToken.
			if r.URL.Query().Get("nextPageToken") == "page-2" {
				fmt.Fprint(w, `{"issues":[{"id":"10003","key":"ENG-125","self":"https://jira.example.com/browse/ENG-125","fields":{"summary":"Second page issue","status":{"id":"1","name":"To Do"},"project":{"id":"project-eng","key":"ENG","name":"Engineering"},"priority":{"id":"3","name":"Medium"},"issuetype":{"id":"task","name":"Task"},"updated":"2026-11-08T10:00:00Z"}}],"isLast":true}`)
				break
			}
			fmt.Fprint(w, `{"issues":[{"id":"10001","key":"ENG-123","self":"https://jira.example.com/browse/ENG-123","fields":{"summary":"Review reading context","status":{"id":"2","name":"In Progress"},"assignee":{"accountId":"user-1","displayName":"Example User"},"project":{"id":"project-eng","key":"ENG","name":"Engineering"},"priority":{"id":"2","name":"High"},"issuetype":{"id":"task","name":"Task"},"updated":"2026-11-09T10:00:00Z"}}],"nextPageToken":"page-2","isLast":false}`)
		case "/issue/ENG-123":
			fmt.Fprint(w, `{"id":"10001","key":"ENG-123","self":"https://jira.example.com/browse/ENG-123","fields":{"summary":"Review reading context","description":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Keep the original context"}]}]},"status":{"id":"2","name":"In Progress"},"assignee":{"accountId":"user-1","displayName":"Example User"},"project":{"id":"project-eng","key":"ENG","name":"Engineering"},"priority":{"id":"2","name":"High"},"issuetype":{"id":"task","name":"Task"},"updated":"2026-11-09T10:00:00Z","comment":{"comments":[{"id":"comment-1","body":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Please preserve it"}]}]},"author":{"accountId":"user-2","displayName":"Reviewer"},"created":"2026-11-09T11:00:00Z"}],"total":1}}}`)
		case "/project":
			fmt.Fprint(w, `[{"id":"project-eng","key":"ENG","name":"Engineering"}]`)
		case "/issuetype":
			fmt.Fprint(w, `[{"id":"task","name":"Task","subtask":false},{"id":"subtask","name":"Subtask","subtask":true}]`)
		case "/issue":
			var in map[string]any
			_ = json.NewDecoder(io.LimitReader(r.Body, 65536)).Decode(&in)
			f.mu.Lock()
			f.creates = append(f.creates, in)
			f.mu.Unlock()
			fmt.Fprint(w, `{"id":"10002","key":"ENG-124","self":"https://jira.example.com/browse/ENG-124"}`)
		case "/issue/ENG-123/comment":
			var in map[string]any
			_ = json.NewDecoder(io.LimitReader(r.Body, 65536)).Decode(&in)
			f.mu.Lock()
			f.comments = append(f.comments, in)
			f.mu.Unlock()
			fmt.Fprint(w, `{"id":"comment-new"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeJira) app() *app {
	return &app{client: newClient(f.srv.URL, testToken), receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeJira(t).app())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "jira" || len(m.Secrets) != 1 || m.Secrets[0].Name != "access_token" {
		t.Fatalf("%+v", m)
	}
	if len(m.Contributes.Connectors[0].ConfirmAlways) != 2 {
		t.Fatal("create and comment must be confirmed")
	}
	if !slices.Equal(m.Permissions.Network, []string{}) {
		t.Fatal(m.Permissions.Network)
	}
}

func TestInstanceURLValidation(t *testing.T) {
	for raw, want := range map[string]string{
		"https://jira.example.com":      "https://jira.example.com/rest/api/3",
		"https://jira.example.com/jira": "https://jira.example.com/jira/rest/api/3",
		"http://127.0.0.1:8080":         "http://127.0.0.1:8080/rest/api/3",
	} {
		got, err := instanceURL(raw)
		if err != nil || got != want {
			t.Fatalf("%q: %v %q", raw, err, got)
		}
	}
	for _, raw := range []string{"http://jira.example.com", "https://me@example.com", "not-a-url"} {
		if _, err := instanceURL(raw); err == nil {
			t.Fatal("accepted " + raw)
		}
	}
}

func TestAssignedFixedQueryAndRichComments(t *testing.T) {
	f := newFakeJira(t)
	text, err := f.app().callTool(t.Context(), "jira_assigned", json.RawMessage(`{"query":"reading"}`))
	if err != nil || !strings.Contains(text, "ENG-123") || !strings.Contains(text, "In Progress") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = f.app().callTool(t.Context(), "jira_issue", json.RawMessage(`{"key":"ENG-123"}`))
	if err != nil || !strings.Contains(text, "Keep the original context") || !strings.Contains(text, "Please preserve it") {
		t.Fatalf("%v %s", err, text)
	}
}

func TestCreateValidatesProjectTypeAndReplay(t *testing.T) {
	f := newFakeJira(t)
	a := f.app()
	args := []byte(`{"project_id":"project-eng","issuetype_id":"task","summary":"New issue","description":"Details"}`)
	for range 2 {
		out, err := a.doAction(t.Context(), "create-1", "jira_create", args)
		if err != nil || !strings.Contains(out["text"].(string), "ENG-124") {
			t.Fatalf("%v %+v", err, out)
		}
	}
	if len(f.creates) != 1 {
		t.Fatal("replay created another issue")
	}
	if _, err := a.doAction(t.Context(), "create-2", "jira_create", []byte(`{"project_id":"project-eng","issuetype_id":"subtask","summary":"Wrong"}`)); err == nil {
		t.Fatal("subtask was accepted as a normal issue")
	}
}

func TestCommentReplayAndInvalidWrites(t *testing.T) {
	f := newFakeJira(t)
	a := f.app()
	for range 2 {
		if _, err := a.doAction(t.Context(), "comment-1", "jira_comment", []byte(`{"key":"ENG-123","body":"Keep context"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.comments) != 1 || f.comments[0]["body"] != "Keep context" {
		t.Fatal(f.comments)
	}
	for _, tc := range []struct{ name, args string }{
		{"jira_comment", `{"key":"../other","body":"Wrong"}`},
		{"jira_comment", `{"key":"ENG-123","body":""}`},
		{"jira_create", `{"project_id":"project-eng","issuetype_id":"task","summary":""}`},
	} {
		if _, err := a.doAction(t.Context(), "bad-"+tc.args, tc.name, []byte(tc.args)); err == nil {
			t.Fatal("accepted " + tc.args)
		}
	}
}

func TestPersistentReplayAndUnknownOutcome(t *testing.T) {
	f := newFakeJira(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"project_id":"project-eng","issuetype_id":"task","summary":"Persistent issue"}`)
	if _, err := a.doAction(t.Context(), "persist-1", "jira_create", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persist-1", "jira_create", args); err != nil {
		t.Fatal(err)
	}
	if len(f.creates) != 1 {
		t.Fatal("restart repeated the create")
	}
	key, _ := json.Marshal(map[string]any{"action": "jira_create", "fields": map[string]any{"project": map[string]string{"id": "project-eng"}, "issuetype": map[string]string{"id": "task"}, "summary": "Persistent issue"}})
	raw, _ := json.Marshal(map[string]writeReceipt{"persist-1": {Key: string(key), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "persist-1", "jira_create", args); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown create was retried")
	}
	if len(f.creates) != 1 {
		t.Fatal("unknown outcome caused another create")
	}
}

func TestLocalizedViewsAndCreateReview(t *testing.T) {
	f := newFakeJira(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
			v, err := a.render(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}
			if err := wire.CheckView(v); err != nil {
				t.Fatalf("%s %d: %v", locale, ui, err)
			}
		}
	}
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6, State: ext.EncodeState(pageState{Mode: "create", DraftID: "draft-1"}), Action: "review-create", Values: map[string]string{"project_id": "project-eng", "issuetype_id": "task", "summary": "New issue", "description": "Details"}}}
	res, err := a.action(t.Context(), r)
	if err != nil || res.View == nil || !strings.Contains(res.View.State, "review") {
		t.Fatalf("%v %+v", err, res)
	}
	if err := wire.CheckView(res.View); err != nil {
		t.Fatal(err)
	}
	r.State, r.Action = res.View.State, "confirm-create"
	res, err = a.action(t.Context(), r)
	if err != nil || !res.Pop || res.Toast == "" {
		t.Fatalf("%v %+v", err, res)
	}
}

// /search/jql answers no total and ignores startAt; it pages with nextPageToken, as the fake does. The page's Load more
// and jira_assigned's page_token reach the second page.
func TestPaginationFollowsNextPageToken(t *testing.T) {
	f := newFakeJira(t)
	a := f.app()
	rows := func(s wire.Section) (keys []string) {
		for _, c := range s.Items {
			if c.Type == wire.ComponentRow {
				keys = append(keys, c.Key)
			}
		}
		return keys
	}
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}
	v, err := a.render(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	more := v.Sections[0].More
	if more == nil || more.Action.Values["token"] != "page-2" {
		t.Fatalf("no Load more for the next page: %+v", v.Sections[0])
	}
	r.State, r.Action, r.Values = v.State, more.Action.ID, more.Action.Values
	res, err := a.action(t.Context(), r)
	if err != nil || res.View == nil {
		t.Fatalf("%v %+v", err, res)
	}
	if s := res.View.Sections[0]; s.More != nil || !slices.Equal(rows(s), []string{"issue-ENG-125"}) {
		t.Fatalf("second page: %v, more %+v", rows(s), s.More)
	}
	text, err := a.callTool(t.Context(), "jira_assigned", nil)
	if err != nil || !strings.Contains(text, `page_token "page-2"`) {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "jira_assigned", json.RawMessage(`{"page_token":"page-2"}`))
	if err != nil || !strings.Contains(text, "ENG-125") || strings.Contains(text, "ENG-123") || strings.Contains(text, "page_token") {
		t.Fatalf("%v %s", err, text)
	}
}

func TestReceiptConflict(t *testing.T) {
	f := newFakeJira(t)
	a := f.app()
	args := []byte(`{"project_id":"project-eng","issuetype_id":"task","summary":"Persistent issue"}`)
	if _, err := a.doAction(t.Context(), "create-1", "jira_create", args); err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "create-1", "jira_create", []byte(`{"project_id":"project-eng","issuetype_id":"task","summary":"Different"}`)); err == nil {
		t.Fatal("receipt accepted different summary")
	}
}

func TestDetailBoundsAndNoConfig(t *testing.T) {
	var item issue
	item.Key, item.Self = "ENG-123", "https://jira.example.com/browse/ENG-123"
	item.Fields.Summary = "Task"
	item.Fields.Description = json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Details"}]}]}`)
	v := detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{IssueKey: "ENG-123", Mode: "detail"}, item)
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
