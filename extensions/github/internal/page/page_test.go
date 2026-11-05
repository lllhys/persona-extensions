package page

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/github/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/github/internal/github"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

type memSecrets struct {
	mu   sync.Mutex
	data []byte
}

func (m *memSecrets) Load(context.Context) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data, nil
}
func (m *memSecrets) Save(_ context.Context, d []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = d
	return nil
}
func (m *memSecrets) Delete(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = nil
	return nil
}

var now = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// fakeGitHub answers what the page reads, for the user octo; it counts requests by path.
func fakeGitHub(t *testing.T) (*httptest.Server, map[string]int) {
	var mu sync.Mutex
	seen := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path]++
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer gho_example" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		at := now.Add(-2 * time.Hour).Format(time.RFC3339)
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"login":"octo"}`))
		case "/graphql":
			body, _ := io.ReadAll(r.Body)
			var q struct {
				Variables struct{ Q string } `json:"variables"`
			}
			_ = json.Unmarshal(body, &q)
			if strings.Contains(q.Variables.Q, "review-requested") {
				_, _ = w.Write([]byte(`{"data":{"search":{"nodes":[
{"number":12,"title":"Add the cache","url":"https://github.com/example-org/example-repo/pull/12","updatedAt":"` + at + `","isDraft":false,"reviewDecision":"REVIEW_REQUIRED",
 "repository":{"nameWithOwner":"example-org/example-repo"},"author":{"login":"alice","avatarUrl":"https://avatars.githubusercontent.com/u/1?v=4"},
 "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}},
{"number":13,"title":"Fix the build","url":"https://github.com/example-org/example-repo/pull/13","updatedAt":"` + at + `","isDraft":false,"reviewDecision":null,
 "repository":{"nameWithOwner":"example-org/example-repo"},"author":{"login":"bob","avatarUrl":"https://avatars.githubusercontent.com/u/2?v=4"},
 "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"FAILURE"}}}]}}]}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"errors":[{"message":"not here"}]}`)) // my PRs: REST instead
		case "/search/issues":
			_, _ = w.Write([]byte(`{"items":[{"number":7,"title":"Crash on start","state":"open","html_url":"https://github.com/example-org/example-repo/issues/7",
"repository_url":"https://api.github.com/repos/example-org/example-repo","updated_at":"` + at + `","user":{"login":"carol","avatar_url":"https://avatars.githubusercontent.com/u/3?v=4"}}]}`))
		case "/notifications":
			_, _ = w.Write([]byte(`[{"id":"1","reason":"review_requested","unread":true,"updated_at":"` + at + `",
"subject":{"title":"Add the cache","url":"https://api.github.com/repos/example-org/example-repo/pulls/12","type":"PullRequest"},
"repository":{"full_name":"example-org/example-repo","html_url":"https://github.com/example-org/example-repo"}},
{"id":"2","reason":"subscribed","unread":true,"updated_at":"` + at + `",
"subject":{"title":"v1.2.0","url":"https://api.github.com/repos/example-org/example-repo/releases/1","type":"Release"},
"repository":{"full_name":"example-org/example-repo","html_url":"https://github.com/example-org/example-repo"}}]`))
		case "/users/octo/received_events":
			_, _ = w.Write([]byte(`[{"type":"PushEvent","actor":{"login":"alice","avatar_url":"https://avatars.githubusercontent.com/u/1?v=4"},"repo":{"name":"example-org/example-repo"},
"payload":{"ref":"refs/heads/main","size":3},"created_at":"` + at + `"},
{"type":"PullRequestEvent","actor":{"login":"bob"},"repo":{"name":"example-org/example-repo"},
"payload":{"action":"closed","pull_request":{"number":9,"title":"Tidy up","html_url":"https://github.com/example-org/example-repo/pull/9","merged":true}},"created_at":"` + at + `"},
{"type":"GollumEvent","actor":{"login":"bob"},"repo":{"name":"example-org/example-repo"},"payload":{},"created_at":"` + at + `"}]`))
		case "/repos/example-org/example-repo/issues/12":
			_, _ = w.Write([]byte(`{"title":"Add the cache","state":"open","user":{"login":"alice","avatar_url":"https://avatars.githubusercontent.com/u/1?v=4"},
"body":"Caches **answers**.","html_url":"https://github.com/example-org/example-repo/pull/12","created_at":"` + at + `","updated_at":"` + at + `",
"labels":[{"name":"perf"}],"comments":2,"pull_request":{}}`))
		case "/repos/example-org/example-repo/pulls/12":
			_, _ = w.Write([]byte(`{"draft":false,"merged":false,"head":{"ref":"cache","sha":"abc123"},"base":{"ref":"main"},"requested_reviewers":[{"login":"octo"}]}`))
		case "/repos/example-org/example-repo/pulls/12/reviews":
			_, _ = w.Write([]byte(`[{"user":{"login":"dave"},"state":"APPROVED","submitted_at":"` + at + `"},{"user":{"login":"dave"},"state":"COMMENTED","submitted_at":"` + at + `"}]`))
		case "/repos/example-org/example-repo/commits/abc123/check-runs":
			_, _ = w.Write([]byte(`{"check_runs":[{"name":"test","status":"completed","conclusion":"success"},{"name":"lint","status":"completed","conclusion":"failure"},{"name":"e2e","status":"in_progress"}]}`))
		case "/repos/example-org/example-repo/issues/12/comments":
			_, _ = w.Write([]byte(`[{"user":{"login":"dave"},"body":"Looks good.","created_at":"` + at + `"},{"user":{"login":"alice"},"body":"Thanks!","created_at":"` + at + `"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

// signedIn is a connector signed in (the device flow's token) against the fake, its page fixed at now.
func signedIn(t *testing.T) (*Page, map[string]int) {
	srv, seen := fakeGitHub(t)
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
	return p, seen
}

func req(version int, s state, action string, values map[string]string) *ext.UIRequest {
	return &ext.UIRequest{UIParams: wire.UIParams{Contribution: ID, State: ext.EncodeState(s), Action: action, Values: values,
		Placement: wire.PlacementNavPage, UIVersion: version, TZ: "UTC"}}
}

// checked is v as the apps would read it, and its text for the test to look at.
func checked(t *testing.T, v *wire.View) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := exttest.CheckView(raw, false); err != nil {
		t.Fatalf("view: %v\n%s", err, raw)
	}
	return string(raw)
}

func TestEveryTabDraws(t *testing.T) {
	p, _ := signedIn(t)
	ctx := context.Background()
	for _, tc := range []struct {
		tab  string
		want []string
	}{
		{github.TabReview, []string{"Add the cache", "CI 通过", "CI 失败", "example-org/example-repo#12", `"avatar"`, "等你 review 的 PR · 2"}},
		{github.TabMine, []string{"Crash on start", "example-org/example-repo#7"}},          // GraphQL refused: REST's search
		{github.TabInvolved, []string{"Crash on start", "指派给你"}},                            // the same one assigned and mentioning: once
		{github.TabNotify, []string{"Add the cache", "请你 review", "v1.2.0", "关注的仓库", "未读"}}, // a release opens on GitHub
		{github.TabActivity, []string{"alice 推送了 3 个提交到 main", "bob 合并了 PR：Tidy up"}},
	} {
		v, err := p.Render(ctx, req(3, state{Tab: tc.tab}, "", nil))
		if err != nil {
			t.Fatal(err)
		}
		got := checked(t, v)
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: no %q in\n%s", tc.tab, w, got)
			}
		}
	}
	if v, _ := p.Render(ctx, req(3, state{Tab: github.TabInvolved}, "", nil)); strings.Count(checked(t, v), `"title":"Crash on start"`) != 1 {
		t.Error("an issue both assigned and mentioning is listed twice")
	}
	if v, _ := p.Render(ctx, req(3, state{Tab: github.TabActivity}, "", nil)); strings.Contains(checked(t, v), "Gollum") {
		t.Error("an event kind without a line is listed")
	}
}

func TestSearchAndTabsAndCache(t *testing.T) {
	p, seen := signedIn(t)
	ctx := context.Background()
	res, err := p.Action(ctx, req(3, state{}, "search", map[string]string{"q": "build"}))
	if err != nil {
		t.Fatal(err)
	}
	got := checked(t, res.View)
	if !strings.Contains(got, "Fix the build") || strings.Contains(got, "Add the cache") || !strings.Contains(got, "「build」1 / 2") {
		t.Fatalf("search: %s", got)
	}
	res, _ = p.Action(ctx, req(3, state{Q: "build"}, "tab", map[string]string{"tab": github.TabNotify}))
	var s state
	_ = json.Unmarshal([]byte(res.View.State), &s)
	if s.Tab != github.TabNotify || s.Q != "" {
		t.Fatalf("a tab starts without the search: %+v", s)
	}
	before := seen["/graphql"]
	_, _ = p.Render(ctx, req(3, state{}, "", nil))
	if seen["/graphql"] != before {
		t.Fatal("a list read again within a minute")
	}
	if res, _ = p.Action(ctx, req(3, state{}, "refresh", nil)); res.Toast != "已刷新" || seen["/graphql"] != before+1 {
		t.Fatalf("refresh: %q, %d reads", res.Toast, seen["/graphql"]-before)
	}
}

func TestDetail(t *testing.T) {
	p, _ := signedIn(t)
	ctx := context.Background()
	v, _ := p.Render(ctx, req(3, state{}, "", nil))
	var open *wire.Action
	for _, c := range v.Sections[1].Items {
		if c.Title == "Add the cache" {
			open = c.Action
		}
	}
	if open == nil || open.Push == nil {
		t.Fatalf("the row does not open the PR on top: %+v", open)
	}
	var s state
	_ = json.Unmarshal([]byte(open.Push.State), &s)
	d, _ := p.Render(ctx, req(3, s, "", nil))
	got := checked(t, d)
	for _, w := range []string{"Add the cache", "开着", "cache → main", "perf", `"label":"通过","value":"1"`, `"label":"失败","value":"1"`,
		"lint", "dave", "批准了", "等 TA review", "Caches **answers**.", "Looks good.", "共 2 条", "https://github.com/example-org/example-repo/pull/12",
		"github-example-org-example-repo-12"} {
		if !strings.Contains(got, w) {
			t.Errorf("no %q in\n%s", w, got)
		}
	}
	if strings.Contains(got, "回到列表") {
		t.Error("a back button where the app has its own")
	}
	old, _ := p.Render(ctx, req(1, s, "", nil))
	if !strings.Contains(checked(t, old), "回到列表") {
		t.Error("no way back for an app before UI version 3")
	}
}

func TestSignedOut(t *testing.T) {
	c := github.New(github.Config{}, "", nil)
	c.UseSecrets(&memSecrets{})
	p := New(c)
	for _, s := range []state{{}, {Repo: "example-org/example-repo", Number: 12}} {
		v, err := p.Render(context.Background(), req(3, s, "", nil))
		if err != nil {
			t.Fatal(err)
		}
		if got := checked(t, v); !strings.Contains(got, "还没连上 GitHub") || !strings.Contains(got, "设置 › 连接器 › GitHub") {
			t.Fatalf("signed out: %s", got)
		}
	}
}

func TestOlderApps(t *testing.T) {
	p, _ := signedIn(t)
	v, _ := p.Render(context.Background(), req(1, state{}, "", nil))
	got := checked(t, v)
	if strings.Contains(got, `"segmented"`) || !strings.Contains(got, "正在看") || strings.Contains(got, `"avatar"`) || !strings.Contains(got, `"name":"q"`) {
		t.Fatalf("UI version 1: %s", got)
	}
	res, _ := p.Action(context.Background(), req(1, state{}, "tab:"+github.TabMine, nil))
	if !strings.Contains(checked(t, res.View), "Crash on start") {
		t.Fatal("a tab's row does not switch")
	}
}
