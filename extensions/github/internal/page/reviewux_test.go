package page

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/github/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/github/internal/github"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The review page for UI version 4 and 5 (review.go) against a fake GitHub: fakeGitHub's PR, plus what review.go reads
// and writes (viewed marks, the pending review, a file at the head, GraphQL's mutations). Nothing real is written.

type reviewFake struct {
	mu   sync.Mutex
	sent []string // "what body": GraphQL mutations by name, REST writes by method and path
}

func (f *reviewFake) note(s string) {
	f.mu.Lock()
	f.sent = append(f.sent, s)
	f.mu.Unlock()
}

func (f *reviewFake) last(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		t.Fatal("nothing sent")
	}
	return f.sent[len(f.sent)-1]
}

const headFile = "package cache\n\nvar x = 2\nvar y = 3\n\nfunc a() {}\nfunc b() {}\n"

func reviewSignedIn(t *testing.T) (*Page, *reviewFake) {
	base, _ := fakeGitHub(t)
	up, _ := url.Parse(base.URL)
	proxy := httputil.NewSingleHostReverseProxy(up)
	f := &reviewFake{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		b := string(body)
		switch {
		case p == "/graphql" && strings.Contains(b, "viewerViewedState"):
			_, _ = w.Write([]byte(`{"data":{"repository":{"pullRequest":{"files":{"pageInfo":{"hasNextPage":false},"nodes":[{"path":"cache.go","viewerViewedState":"VIEWED"}]}}}}}`))
		case p == "/graphql" && strings.Contains(b, "mutation"):
			for _, op := range []string{"markFileAsViewed", "unmarkFileAsViewed", "addPullRequestReviewThread", "resolveReviewThread", "unresolveReviewThread"} {
				if strings.Contains(b, op+"(") {
					f.note(op + " " + b)
				}
			}
			_, _ = w.Write([]byte(`{"data":{}}`))
		case p == "/graphql" && strings.Contains(b, "reviewThreads(first: 100) { nodes { id"):
			_, _ = w.Write([]byte(`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"id":"T_100","comments":{"nodes":[{"databaseId":100}]}}]}}}}}`))
		case p == repoAPI+"/pulls/12" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"node_id":"PR_12","draft":false,"merged":false,"head":{"ref":"cache","sha":"abc123"},"base":{"ref":"main"},"requested_reviewers":[{"login":"octo"}],
"additions":2,"deletions":1,"commits":1,"changed_files":2,"mergeable":true,"mergeable_state":"clean"}`))
		case p == repoAPI+"/pulls/12/reviews" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[{"id":5,"user":{"login":"dave"},"state":"APPROVED","submitted_at":"2026-11-05T10:00:00Z"},{"id":77,"node_id":"PRR_77","user":{"login":"octo"},"state":"PENDING"}]`))
		case p == repoAPI+"/pulls/12/reviews/77/comments":
			_, _ = w.Write([]byte(`[{"id":900,"path":"cache.go","line":3,"side":"RIGHT","body":"Is 2 right?","user":{"login":"octo"}},
{"id":901,"path":"cache.go","line":3,"side":"RIGHT","in_reply_to_id":100,"body":"+1","user":{"login":"octo"}}]`))
		case p == repoAPI+"/contents/cache.go" && r.URL.Query().Get("ref") == "abc123":
			_, _ = w.Write([]byte(`{"type":"file","path":"cache.go","size":60,"encoding":"base64","content":"` + b64(headFile) + `","html_url":"https://github.com/example-org/example-repo/blob/abc123/cache.go"}`))
		case r.Method != http.MethodGet && p != "/graphql":
			f.note(r.Method + " " + p + " " + b)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"html_url":"https://github.com/example-org/example-repo/pull/12#x"}`))
		default:
			proxy.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	c := github.New(github.Config{}, "", nil)
	c.UseAPI(srv.URL, "https://github.com")
	c.UseSecrets(&memSecrets{data: []byte(`{"token":"gho_example"}`)})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go c.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for c.Status(ctx).State != connector.StateOK {
		if time.Now().After(deadline) {
			t.Fatalf("not signed in: %+v", c.Status(ctx))
		}
		time.Sleep(10 * time.Millisecond)
	}
	p := New(c)
	p.now = func() time.Time { return now }
	return p, f
}

func diffs(v *wire.View) map[string]wire.Component {
	out := map[string]wire.Component{}
	for _, s := range v.Sections {
		for _, c := range s.Items {
			if c.Type == wire.ComponentDiff {
				out[c.Path] = c
			}
		}
	}
	return out
}

func TestReviewTabV4(t *testing.T) {
	p, _ := reviewSignedIn(t)
	ctx := context.Background()
	pr := state{Repo: "example-org/example-repo", Number: 12}
	res, _ := p.Action(ctx, req(4, pr, "tab", map[string]string{"tab": tabFiles}))
	got := checked(t, res.View)
	if res.View.Layout != wire.LayoutSplit {
		t.Fatalf("layout %q", res.View.Layout)
	}
	has(t, "tree", got, `"type":"file_tree"`, `"path":"cache.go"`, `"status":"modified"`, `"comments":3`)
	d := diffs(res.View)["cache.go"]
	if len(d.Hunks) != 1 || d.Hunks[0].OldStart != 1 || d.Hunks[0].NewStart != 1 || d.Hunks[0].Lines[1] != "-var x = 1" {
		t.Fatalf("hunks %+v", d.Hunks)
	}
	if len(d.Threads) != 2 || !d.Threads[0].Resolved || d.Threads[0].Line != 3 || !d.Threads[1].Outdated {
		t.Fatalf("threads %+v", d.Threads)
	}
	if d.Context != nil || d.MarkViewed != nil || d.Draft != nil || res.View.Review != nil {
		t.Fatal("UI version 5 for an app of 4")
	}
	if !diffs(res.View)["logo.png"].Binary {
		t.Fatal("a file without a patch is binary")
	}
	// an older app keeps its rows
	old, _ := p.Action(ctx, req(3, pr, "tab", map[string]string{"tab": tabFiles}))
	hasNot(t, "v3", checked(t, old.View), `"type":"diff"`, `"type":"file_tree"`)
}

func TestReviewTabV5(t *testing.T) {
	p, f := reviewSignedIn(t)
	ctx := context.Background()
	pr := state{Repo: "example-org/example-repo", Number: 12, Tab: tabFiles}
	v, _ := p.Render(ctx, req(5, pr, "", nil))
	checked(t, v)
	d := diffs(v)["cache.go"]
	if !d.Viewed || d.MarkViewed == nil || d.Draft == nil || d.Context == nil || !d.Suggestions {
		t.Fatalf("v5 fields: %+v", d)
	}
	var pending, reply bool
	for _, th := range d.Threads {
		if th.Pending && th.Comments[0].Body == "Is 2 right?" {
			pending = true
		}
		for _, c := range th.Comments {
			if c.Body == "+1" && c.Badge != nil && c.Badge.Text == "草稿" && th.ID == "100" {
				reply = true
			}
		}
	}
	if !pending || !reply {
		t.Fatalf("drafts not at their lines: %+v", d.Threads)
	}
	if v.Review == nil || v.Review.Count != 2 || v.Review.Discard == nil || len(v.Review.Buttons) != 3 {
		t.Fatalf("review bar %+v", v.Review)
	}

	// hidden lines: the file at the head, whole
	res, _ := p.Action(ctx, req(5, pr, "context", map[string]string{"path": "cache.go", "side": "new", "from": "5", "to": "end"}))
	if res.Lines == nil || res.Lines.Start != 1 || res.Lines.Total != 7 || res.Lines.Lines[5] != "func a() {}" || res.View != nil {
		t.Fatalf("lines %+v", res.Lines)
	}
	// a diff loaded in place
	res, _ = p.Action(ctx, req(5, pr, "load_diff", map[string]string{"path": "cache.go"}))
	if len(res.Items) != 1 || res.Items[0].Key != "diff:cache.go" || len(res.Items[0].Hunks) == 0 || res.View != nil {
		t.Fatalf("load %+v", res)
	}
	// writes, to the fake only
	if res, _ = p.Action(ctx, req(5, pr, "mark_viewed", map[string]string{"path": "cache.go", "viewed": "false"})); res.View != nil || res.Toast != "" {
		t.Fatalf("mark viewed answers %+v", res)
	}
	has(t, "unmark", f.last(t), "unmarkFileAsViewed", `"path":"cache.go"`, `"id":"PR_12"`)
	p.Action(ctx, req(5, pr, "draft_comment", map[string]string{"path": "cache.go", "side": "new", "line": "4", "start_line": "3", "body": "why"}))
	has(t, "draft", f.last(t), "addPullRequestReviewThread", `"pullRequestReviewId":"PRR_77"`, `"startLine":3`, `"line":4`, `"side":"RIGHT"`)
	p.Action(ctx, req(5, pr, "inline_comment", map[string]string{"path": "cache.go", "side": "old", "line": "2", "body": "gone?"}))
	has(t, "comment", f.last(t), "POST "+repoAPI+"/pulls/12/comments", `"side":"LEFT"`, `"line":2`, `"commit_id":"abc123"`)
	p.Action(ctx, req(5, pr, "resolve", map[string]string{"thread": "100", "resolved": "true"}))
	has(t, "resolve", f.last(t), "resolveReviewThread", `"id":"T_100"`)
	res, _ = p.Action(ctx, req(5, pr, "submit_review", map[string]string{"event": "APPROVE", "body": "LGTM"}))
	has(t, "submit", f.last(t), "POST "+repoAPI+"/pulls/12/reviews/77/events", `"event":"APPROVE"`, `"body":"LGTM"`)
	if res.Toast != "评审已提交" {
		t.Fatalf("submit toast %q", res.Toast)
	}
	p.Action(ctx, req(5, pr, "discard_review", nil))
	has(t, "discard", f.last(t), "DELETE "+repoAPI+"/pulls/12/reviews/77")
}

func TestReviewFileAndDiffPages(t *testing.T) {
	p, _ := reviewSignedIn(t)
	ctx := context.Background()
	v, _ := p.Render(ctx, req(5, state{V: viewDiff, Repo: "example-org/example-repo", Number: 12, Path: "cache.go"}, "", nil))
	checked(t, v)
	if d := diffs(v)["cache.go"]; len(d.Hunks) != 1 || d.Context == nil {
		t.Fatalf("diff page %+v", d)
	}
	file, _ := p.Render(ctx, req(4, state{V: viewFile, Repo: "example-org/example-repo", Ref: "main", Path: "src/main.go"}, "", nil))
	raw, _ := json.Marshal(file)
	has(t, "file", checked(t, file), `"type":"code"`, `"language":"go"`, `"line_url":"https://github.com/example-org/example-repo/blob/main/src/main.go#L{start}-L{end}"`)
	_ = raw
}

func TestHunksOf(t *testing.T) {
	h := hunksOf("@@ -10,3 +12,4 @@ func f() {\n a\n-b\n+c\n+d\n\\ No newline at end of file\n@@ -40 +42 @@\n-x\n+y")
	if len(h) != 2 || h[0].OldStart != 10 || h[0].NewStart != 12 || h[0].Header != "@@ -10,3 +12,4 @@ func f() {" || len(h[0].Lines) != 5 || h[1].OldStart != 40 || h[1].Lines[1] != "+y" {
		t.Fatalf("%+v", h)
	}
	v := &wire.View{Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentDiff, Path: "a", Hunks: h}}}}}
	if err := wire.CheckView(v); err != nil {
		t.Fatal(err)
	}
}

func TestLinesAround(t *testing.T) {
	lines := make([]string, 30000)
	for i := range lines {
		lines[i] = "x"
	}
	got := linesAround("a", lines, "100", "end")
	if got.Start != 100 || len(got.Lines) != 20000 || got.Total != 30000 {
		t.Fatalf("a big file: from %d, %d lines of %d", got.Start, len(got.Lines), got.Total)
	}
	if got = linesAround("a", lines[:10], "3", "5"); got.Start != 1 || len(got.Lines) != 10 {
		t.Fatalf("a small file whole: %+v", got)
	}
}
