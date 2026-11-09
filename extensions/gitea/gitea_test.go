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

const testToken = "synthetic-gitea-token"

type fakeGitea struct {
	srv       *httptest.Server
	mu        sync.Mutex
	paths     []string
	approves  int
	comments  []map[string]any
	failWrite bool
}

func samplePR() pullRequest {
	item := pullRequest{ID: 42, Number: 42, Title: "Preserve reading context", Body: "Keep source context beside the next step.", State: "open", HTMLURL: "https://gitea.example.com/reading/notes/pulls/42", CreatedAt: "2026-11-10T10:00:00Z", User: user{ID: 1, Login: "author", FullName: "Example Author"}}
	item.Head.Ref = "feature/reading"
	item.Base.Ref = "main"
	return item
}

func newFakeGitea(t *testing.T) *fakeGitea {
	f := &fakeGitea{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/repos/reading/notes/pulls":
			items := []pullRequest{samplePR()}
			if r.URL.Query().Get("state") != "open" || r.URL.Query().Get("limit") != "50" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if r.URL.Query().Get("page") != "1" {
				items = nil
			}
			_ = json.NewEncoder(w).Encode(items)
		case "/api/v1/repos/reading/notes/pulls/42":
			_ = json.NewEncoder(w).Encode(samplePR())
		case "/api/v1/repos/reading/notes/issues/42/comments":
			if r.Method == http.MethodPost {
				f.mu.Lock()
				fail := f.failWrite
				f.mu.Unlock()
				if fail {
					http.Error(w, "synthetic interrupted write", http.StatusServiceUnavailable)
					return
				}
				var in map[string]any
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in["body"] == "" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				f.mu.Lock()
				f.comments = append(f.comments, in)
				f.mu.Unlock()
				_ = json.NewEncoder(w).Encode(comment{ID: 5, Body: in["body"].(string), User: user{Login: "reader", FullName: "Reader"}})
				return
			}
			_ = json.NewEncoder(w).Encode([]comment{{ID: 1, Body: "Keep the original context.", User: user{Login: "reviewer", FullName: "Example Reviewer"}}})
		case "/api/v1/repos/reading/notes/pulls/42/reviews":
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			var in map[string]any
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in["state"] != "APPROVED" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.mu.Lock()
			f.approves++
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "APPROVED"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitea) app() *app {
	return &app{client: newClient(f.srv.URL, testToken), receipts: newWriteStore("")}
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newFakeGitea(t).app())))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "gitea" || len(m.Secrets) != 1 || m.Secrets[0].Name != "access_token" {
		t.Fatalf("%+v", m)
	}
	if len(m.Contributes.Connectors[0].ConfirmAlways) != 2 {
		t.Fatal("approve and comment must be confirmed")
	}
	if !slices.Equal(m.Permissions.Network, []string{}) {
		t.Fatal(m.Permissions.Network)
	}
}

func TestInstanceURLValidation(t *testing.T) {
	for raw, want := range map[string]string{"https://gitea.example.com": "https://gitea.example.com", "http://127.0.0.1:3000/": "http://127.0.0.1:3000"} {
		got, err := instanceURL(raw)
		if err != nil || got != want {
			t.Fatalf("%q: %v %q", raw, err, got)
		}
	}
	for _, raw := range []string{"http://gitea.example.com", "https://me@example.com", "not-a-url"} {
		if _, err := instanceURL(raw); err == nil {
			t.Fatal("accepted " + raw)
		}
	}
}

func TestPRReadCommentsAndPagination(t *testing.T) {
	f := newFakeGitea(t)
	a := f.app()
	text, err := a.callTool(t.Context(), "gitea_prs", json.RawMessage(`{"owner":"reading","repo":"notes","page":1}`))
	if err != nil || !strings.Contains(text, "PR #42") {
		t.Fatalf("%v %s", err, text)
	}
	text, err = a.callTool(t.Context(), "gitea_pr", json.RawMessage(`{"owner":"reading","repo":"notes","number":42}`))
	if err != nil || !strings.Contains(text, "Keep the original context") {
		t.Fatalf("%v %s", err, text)
	}
	f.mu.Lock()
	paths := strings.Join(f.paths, "\n")
	f.mu.Unlock()
	for _, want := range []string{"state=open", "limit=50", "page=1", "/issues/42/comments"} {
		if !strings.Contains(paths, want) {
			t.Fatalf("missing %q in %s", want, paths)
		}
	}
	if _, err := a.callTool(t.Context(), "gitea_prs", json.RawMessage(`{"owner":"reading","repo":"bad/repo"}`)); err == nil {
		t.Fatal("accepted a repository path injection")
	}
}

func TestApproveCommentReplay(t *testing.T) {
	f := newFakeGitea(t)
	a := f.app()
	for range 2 {
		if _, err := a.doAction(t.Context(), "approve-1", "gitea_approve", []byte(`{"owner":"reading","repo":"notes","number":42}`)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if _, err := a.doAction(t.Context(), "comment-1", "gitea_comment", []byte(`{"owner":"reading","repo":"notes","number":42,"body":"Looks good with the added context."}`)); err != nil {
			t.Fatal(err)
		}
	}
	if f.approves != 1 || len(f.comments) != 1 {
		t.Fatalf("%d %v", f.approves, f.comments)
	}
	if _, err := a.doAction(t.Context(), "approve-1", "gitea_approve", []byte(`{"owner":"reading","repo":"notes","number":43}`)); err == nil {
		t.Fatal("receipt accepted a different PR")
	}
	if _, err := describeAction("gitea_comment", []byte(`{"owner":"reading","repo":"notes","number":42,"body":"A concise review."}`)); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentReplayAndUnknownOutcome(t *testing.T) {
	f := newFakeGitea(t)
	a := f.app()
	store, err := openWriteStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.receipts = store
	args := []byte(`{"owner":"reading","repo":"notes","number":42}`)
	if _, err := a.doAction(t.Context(), "persist-1", "gitea_approve", args); err != nil {
		t.Fatal(err)
	}
	a.receipts, err = openWriteStore(filepath.Dir(store.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.doAction(t.Context(), "persist-1", "gitea_approve", args); err != nil {
		t.Fatal(err)
	}
	if f.approves != 1 {
		t.Fatal("restart repeated approval")
	}
	key, _ := json.Marshal(reviewArgs{Owner: "reading", Repo: "notes", Number: 42})
	raw, _ := json.Marshal(map[string]writeReceipt{"persist-1": {Key: string(key), Status: "pending", StartedAt: time.Now()}})
	if err := os.WriteFile(store.path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a.receipts, _ = openWriteStore(filepath.Dir(store.path))
	if _, err := a.doAction(t.Context(), "persist-1", "gitea_approve", args); err == nil || !strings.Contains(err.Error(), "inspect") {
		t.Fatal("unknown approval was retried")
	}
	if f.approves != 1 {
		t.Fatal("unknown approval caused another write")
	}
}

func TestLocalizedViewsAndCommentFlow(t *testing.T) {
	f := newFakeGitea(t)
	a := f.app()
	for _, locale := range []string{"en", "ja", "zh-Hans"} {
		for _, ui := range []int{1, 3, 6} {
			for _, mode := range []string{"list", "detail", "comment", "review-comment"} {
				r := &ext.UIRequest{UIParams: wire.UIParams{Locale: locale, UIVersion: ui}}
				st := pageState{Mode: mode, Owner: "reading", Repo: "notes", PRID: 42, DraftID: "draft-1", Body: "Keep source context."}
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
	r := &ext.UIRequest{UIParams: wire.UIParams{Locale: "zh-Hans", UIVersion: 6, Values: map[string]string{"body": "保留来源上下文。"}}}
	result, err := a.action(t.Context(), withAction(r, "review-comment", pageState{Mode: "comment", Owner: "reading", Repo: "notes", PRID: 42, DraftID: "draft-1"}))
	if err != nil || result.View == nil || wire.CheckView(result.View) != nil {
		t.Fatalf("review: %v %+v", err, result)
	}
	result, err = a.action(t.Context(), withAction(r, "confirm-comment", pageState{Mode: "review-comment", Owner: "reading", Repo: "notes", PRID: 42, DraftID: "draft-1", Body: "保留来源上下文。"}))
	if err != nil || !result.Pop || len(f.comments) != 1 {
		t.Fatalf("confirm: %v %+v %v", err, result, f.comments)
	}
}

func withAction(r *ext.UIRequest, action string, st pageState) *ext.UIRequest {
	r.Action = action
	r.State = ext.EncodeState(st)
	return r
}

func TestBoundsAndNoConfig(t *testing.T) {
	item := samplePR()
	item.Body = strings.Repeat("context\n", 2000)
	v := detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{Owner: "reading", Repo: "notes", PRID: 42, Mode: "detail", DraftID: "draft-1"}, item, nil)
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
	item.HTMLURL = "http://gitea.example.com/pulls/42"
	v = detailView(&ext.UIRequest{UIParams: wire.UIParams{Locale: "en", UIVersion: 6}}, pageState{PRID: 42}, item, nil)
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
	f := newFakeGitea(t)
	f.failWrite = true
	a := f.app()
	if _, err := a.doAction(t.Context(), "network-1", "gitea_comment", []byte(`{"owner":"reading","repo":"notes","number":42,"body":"Review"}`)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatal(err)
	}
	if len(f.comments) != 0 {
		t.Fatal("failed write was counted as successful")
	}
	if _, err := fmt.Fprint(os.Stderr, ""); err != nil {
		t.Fatal(err)
	}
}
