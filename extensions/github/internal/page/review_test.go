package page

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/github/internal/github"
	"github.com/nwverse/persona/sdk/go/wire"
)

const repoAPI = "/repos/example-org/example-repo"

const readme = "# Example\n\n![logo](docs/logo.png)\n\nSee [the guide](docs/guide.md) and <a href=\"https://example.com\">site</a>, " +
	"[bad](javascript:alert(1)), <http://insecure.example.com>.\n<p align=\"center\"><img src=\"x.png\" alt=\"shot\"></p>\n" +
	"[ref link][g]\n\n[g]: https://example.com/g\n\n```js\nconst a = '<b>' + \"](javascript:x)\";\n```\n"

const patch = "@@ -1,3 +1,4 @@ package cache\n package cache\n-var x = 1\n+var x = 2\n+var y = 3\n \n"

const failedLog = "2026-11-05T12:00:00.0000000Z Run golangci-lint\n2026-11-05T12:00:01.0000000Z ##[error]cache.go:3: y is unused\n" +
	"2026-11-05T12:00:02.0000000Z ##[error]Process completed with exit code 1.\n"

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// fakeMore answers the repository side and a PR's files, conversations, commits and checks (fakeGitHub's other
// paths).
// slowGate, when set, holds the PR's events until it is closed (a slow first read).
var slowGate atomic.Pointer[chan struct{}]

func fakeMore(w http.ResponseWriter, r *http.Request, at string) bool {
	if g := slowGate.Load(); g != nil && r.URL.Path == repoAPI+"/issues/12/events" {
		<-*g
	}
	repo := `{"full_name":"example-org/example-repo","description":"An example","language":"Go","stargazers_count":1200,"forks_count":3,
"default_branch":"main","pushed_at":"` + at + `","html_url":"https://github.com/example-org/example-repo",
"owner":{"login":"example-org","avatar_url":"https://avatars.githubusercontent.com/u/9?v=4"}}`
	body := map[string]string{
		"/user/repos":                 "[" + repo + "]",
		"/user/orgs":                  `[{"login":"example-org"}]`,
		"/search/repositories":        `{"total_count":1,"items":[{"full_name":"other/thing","description":"Found","owner":{"login":"other"}}]}`,
		repoAPI:                       repo,
		repoAPI + "/branches":         `[{"name":"main","protected":true},{"name":"cache"}]`,
		repoAPI + "/contents":         `[{"name":"src","path":"src","type":"dir"},{"name":"README.md","path":"README.md","type":"file","size":120}]`,
		repoAPI + "/contents/src":     `[{"name":"lib","path":"src/lib","type":"dir"},{"name":"main.go","path":"src/main.go","type":"file","size":30}]`,
		repoAPI + "/contents/src/lib": `[{"name":"util.go","path":"src/lib/util.go","type":"file","size":10}]`,
		repoAPI + "/contents/src/main.go": `{"type":"file","path":"src/main.go","size":30,"encoding":"base64","content":"` + b64("package main\n\nfunc main() {}\n") +
			`","html_url":"https://github.com/example-org/example-repo/blob/main/src/main.go"}`,
		repoAPI + "/readme":           `{"path":"README.md","encoding":"base64","content":"` + b64(readme) + `"}`,
		repoAPI + "/issues/12/events": `[{"id":5,"event":"labeled","actor":{"login":"alice"},"label":{"name":"perf"},"created_at":"` + at + `"}]`,
		repoAPI + "/pulls/12/comments": `[{"id":100,"user":{"login":"erin"},"body":"Why **3**?","path":"cache.go","line":3,"side":"RIGHT","created_at":"` + at + `"},
{"id":101,"user":{"login":"alice"},"body":"Tuned.","path":"cache.go","line":3,"side":"RIGHT","in_reply_to_id":100,"created_at":"` + at + `"},
{"id":200,"user":{"login":"erin"},"body":"Old note","path":"cache.go","line":null,"original_line":9,"diff_hunk":"@@ -7,3 +7,3 @@\n a\n-b\n+c","created_at":"` + at + `"}]`,
		repoAPI + "/pulls/12/files": `[{"filename":"cache.go","status":"modified","additions":2,"deletions":1,"patch":` + quote(patch) +
			`,"blob_url":"https://github.com/example-org/example-repo/blob/abc123/cache.go"},{"filename":"logo.png","status":"added"}]`,
		repoAPI + "/commits":               `[{"sha":"fed9876543210","commit":{"message":"Bump the version","author":{"name":"Bob","date":"` + at + `"}},"html_url":"https://github.com/example-org/example-repo/commit/fed9876543210"}]`,
		repoAPI + "/pulls":                 `[{"number":12,"title":"Add the cache","html_url":"https://github.com/example-org/example-repo/pull/12","updated_at":"` + at + `","user":{"login":"alice"}}]`,
		repoAPI + "/pulls/12/commits":      `[{"sha":"abc1234567890","commit":{"message":"Add cache\n\nThe body","author":{"name":"Alice","date":"` + at + `"}},"author":{"login":"alice"}}]`,
		repoAPI + "/commits/abc123/status": `{"statuses":[{"context":"ci/legacy","state":"success","description":"ok","target_url":"https://ci.example.com/1"}]}`,
		repoAPI + "/check-runs/31": `{"id":31,"name":"lint","status":"completed","conclusion":"failure","html_url":"https://github.com/example-org/example-repo/runs/31",
"started_at":"` + at + `","completed_at":"` + at + `","app":{"slug":"github-actions"},"output":{"title":"2 problems","summary":"golangci-lint found **2** problems"}}`,
		repoAPI + "/check-runs/31/annotations": `[{"path":"cache.go","start_line":3,"annotation_level":"failure","title":"unused","message":"y is unused"}]`,
		repoAPI + "/actions/jobs/31":           `{"steps":[{"number":1,"name":"Set up","status":"completed","conclusion":"success"},{"number":2,"name":"Lint","status":"completed","conclusion":"failure"}]}`,
		repoAPI + "/actions/jobs/31/logs":      failedLog,
	}[r.URL.Path]
	if body == "" {
		return false
	}
	_, _ = w.Write([]byte(body))
	return true
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// writes are what the page sent to the fake.
var writes struct {
	mu   sync.Mutex
	sent []string // "METHOD path body"
}

func fakeWrite(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	writes.mu.Lock()
	writes.sent = append(writes.sent, r.Method+" "+r.URL.Path+" "+string(body))
	writes.mu.Unlock()
	if r.Method == http.MethodPut {
		_, _ = w.Write([]byte(`{"merged":true,"message":"Pull Request successfully merged","sha":"def"}`))
		return
	}
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"html_url":"https://github.com/example-org/example-repo/pull/12#x"}`))
}

func lastWrite(t *testing.T) string {
	t.Helper()
	writes.mu.Lock()
	defer writes.mu.Unlock()
	if len(writes.sent) == 0 {
		t.Fatal("nothing was sent")
	}
	return writes.sent[len(writes.sent)-1]
}

func has(t *testing.T, what, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s: no %q in\n%s", what, w, got)
		}
	}
}

func hasNot(t *testing.T, what, got string, not ...string) {
	t.Helper()
	for _, w := range not {
		if strings.Contains(got, w) {
			t.Errorf("%s: %q in\n%s", what, w, got)
		}
	}
}

func pushed(t *testing.T, v *wire.View, title string) state {
	t.Helper()
	for _, sec := range v.Sections {
		for _, c := range sec.Items {
			if c.Title == title && c.Action != nil && c.Action.Push != nil {
				var s state
				if err := json.Unmarshal([]byte(c.Action.Push.State), &s); err != nil {
					t.Fatal(err)
				}
				return s
			}
		}
	}
	t.Fatalf("nothing titled %q opens a page", title)
	return state{}
}

func actionOf(t *testing.T, v *wire.View, title string) string {
	t.Helper()
	for _, sec := range v.Sections {
		for _, c := range sec.Items {
			if c.Title == title && c.Action != nil {
				return c.Action.ID
			}
		}
	}
	t.Fatalf("nothing titled %q", title)
	return ""
}

func TestRepositories(t *testing.T) {
	p, _ := signedIn(t)
	ctx := context.Background()
	v, _ := p.Render(ctx, req(3, state{Tab: tabRepos}, "", nil))
	got := checked(t, v)
	has(t, "repos", got, "example-org/example-repo", "An example", "Go", "★ 1.2k", `"value":"org:example-org"`, "最近的仓库")
	res, _ := p.Action(ctx, req(3, state{Tab: tabRepos}, "search", map[string]string{"q": "thing"}))
	has(t, "search", checked(t, res.View), "other/thing", "搜索「thing」")

	// the repository: its header, its root, its README with what the hub refuses taken out
	s := pushed(t, v, "example-org/example-repo")
	repo, _ := p.Render(ctx, req(3, s, "", nil))
	got = checked(t, repo)
	has(t, "repo", got, `"title":"PR"`, `"title":"Issue"`, `"title":"提交"`, `"label":"星标","value":"1.2k"`, "src", "README.md", `"name":"ref"`, `"value":"cache"`,
		"[logo](https://github.com/example-org/example-repo/blob/main/docs/logo.png)",
		"[the guide](https://github.com/example-org/example-repo/blob/main/docs/guide.md)", "[site](https://example.com)",
		"[ref link](https://example.com/g)", "shot", "const a = ")
	hasNot(t, "repo", got, "javascript:alert", "<p align", "<img", "[g]:", "<http://")

	// a directory in place, a file on top, up again
	res, _ = p.Action(ctx, req(3, s, actionOf(t, repo, "src"), nil))
	got = checked(t, res.View)
	has(t, "src", got, "main.go", "上一级", "/src")
	var in state
	_ = json.Unmarshal([]byte(res.View.State), &in)
	file, _ := p.Render(ctx, req(3, pushed(t, res.View, "main.go"), "", nil))
	has(t, "file", checked(t, file), "```go\\npackage main", "func main() {}", "在 GitHub 打开")
	deeper, _ := p.Action(ctx, req(3, in, actionOf(t, res.View, "lib"), nil)) // a directory in a directory
	has(t, "src/lib", checked(t, deeper.View), "util.go", "/src/lib")
	res, _ = p.Action(ctx, req(3, in, "cd:..", nil))
	has(t, "up", checked(t, res.View), "README.md")

	// another branch, from the filter; the branches, the commits, the PRs
	res, _ = p.Action(ctx, req(3, s, "ref", map[string]string{"ref": "cache"}))
	_ = json.Unmarshal([]byte(res.View.State), &in)
	if in.Ref != "cache" || in.Tab != tabCode {
		t.Fatalf("the branch: %+v", in)
	}
	b, _ := p.Action(ctx, req(3, s, "tab", map[string]string{"tab": tabBranches}))
	has(t, "branches", checked(t, b.View), "main", "默认", "cache", "2 个分支")
	_ = json.Unmarshal([]byte(b.View.State), &in)
	res, _ = p.Action(ctx, req(3, in, actionOf(t, b.View, "cache"), nil))
	_ = json.Unmarshal([]byte(res.View.State), &in)
	if in.Ref != "cache" || in.Tab != tabCode {
		t.Fatalf("a branch's row: %+v", in)
	}
	k, _ := p.Action(ctx, req(3, s, "tab", map[string]string{"tab": tabCommits}))
	has(t, "commits", checked(t, k.View), "Bump the version", "fed9876", "Bob")
	pr, _ := p.Action(ctx, req(3, s, "tab", map[string]string{"tab": tabPulls}))
	has(t, "pulls", checked(t, pr.View), "Add the cache", "开着", "已关闭")

	// a pasted link opens what it names
	res, _ = p.Action(ctx, req(3, state{}, "search", map[string]string{"q": "https://github.com/example-org/example-repo/pull/12"}))
	if res.Push == nil || !strings.Contains(res.Push.State, `"n":12`) {
		t.Fatalf("a pasted link: %+v", res.Push)
	}
}

func TestPullRequestTabs(t *testing.T) {
	p, _ := signedIn(t)
	ctx := context.Background()
	pr := state{Repo: "example-org/example-repo", Number: 12}
	files, _ := p.Action(ctx, req(3, pr, "tab", map[string]string{"tab": tabFiles}))
	has(t, "files", checked(t, files.View), "cache.go", "logo.png", "新增", "+2 −1", "3 条评论", "2 个文件 · +2 −1")

	notes, _ := p.Render(ctx, req(3, pr, "", nil))
	has(t, "notes", checked(t, notes), "Looks good.", "加了标签 perf", "abc1234 Add cache", "cache.go:3", "Why **3**?", "评论")
	commits, _ := p.Action(ctx, req(3, pr, "tab", map[string]string{"tab": tabPRCommit}))
	has(t, "pr commits", checked(t, commits.View), "Add cache", "abc1234", "1 个提交")

	checks, _ := p.Action(ctx, req(3, pr, "tab", map[string]string{"tab": tabChecks}))
	got := checked(t, checks.View)
	has(t, "checks", got, "lint", "失败", "ci/legacy", "github-actions")
	if strings.Index(got, `"title":"lint"`) > strings.Index(got, `"title":"test"`) {
		t.Error("a failed check is not first")
	}
	ck, _ := p.Render(ctx, req(3, pushed(t, checks.View, "lint"), "", nil))
	has(t, "check", checked(t, ck), "cache.go:3: y is unused", "Process completed with exit code 1.", "y is unused", "Lint", "日志的最后一段", "golangci-lint found **2** problems")
}

func TestDiff(t *testing.T) {
	p, _ := signedIn(t)
	ctx := context.Background()
	v, _ := p.Render(ctx, req(3, state{V: viewDiff, Repo: "example-org/example-repo", Number: 12, Path: "cache.go"}, "", nil))
	got := checked(t, v)
	has(t, "diff", got, "```diff", "+    3 │ var y = 3", "-    2 │ var x = 1", "Why **3**?", "Tuned.", "已解决", "回复", "第 3 行",
		"不在现在的改动里的评论", "Old note", "评论一行")
	// the conversation is right under its line, the rest of the hunk after it
	if i, j, k := strings.Index(got, "var y = 3"), strings.Index(got, "Why **3**?"), strings.Index(got, "    4 │"); !(i < j && j < k) {
		t.Errorf("the conversation is not under its line: %d %d %d", i, j, k)
	}
	bin, _ := p.Render(ctx, req(3, state{V: viewDiff, Repo: "example-org/example-repo", Number: 12, Path: "logo.png"}, "", nil))
	has(t, "binary", checked(t, bin), "这里看不到改动")
	old, _ := p.Render(ctx, req(2, state{V: viewDiff, Repo: "example-org/example-repo", Number: 12, Path: "cache.go"}, "", nil))
	hasNot(t, "UI 2", checked(t, old), `"markdown"`)
}

func TestWrites(t *testing.T) {
	p, _ := signedIn(t)
	ctx := context.Background()
	w := state{V: viewWrite, W: "comment", Repo: "example-org/example-repo", Number: 12}
	v, _ := p.Render(ctx, req(3, w, "", nil))
	has(t, "form", checked(t, v), "Add the cache", "预览", `"submit":true`)
	res, _ := p.Action(ctx, req(3, w, "preview", map[string]string{"b": "  "}))
	has(t, "empty", checked(t, res.View), "还没写内容")
	res, _ = p.Action(ctx, req(3, w, "preview", map[string]string{"b": "LGTM, **thanks**"}))
	got := checked(t, res.View)
	has(t, "preview", got, "LGTM, **thanks**", "能看到 example-org/example-repo 的人都能看到", `"confirm":"评论发到 example-org/example-repo#12？"`)
	_ = json.Unmarshal([]byte(res.View.State), &w)
	writes.mu.Lock()
	before := len(writes.sent)
	writes.mu.Unlock()
	if res, _ := p.Action(ctx, req(3, state{V: viewWrite, W: "comment", Repo: w.Repo, Number: 12, Body: "x"}, "send", nil)); res.View == nil {
		t.Fatal("a send without its preview went out")
	}
	writes.mu.Lock()
	if len(writes.sent) != before {
		t.Fatal("a send without its preview went out")
	}
	writes.mu.Unlock()
	res, _ = p.Action(ctx, req(3, w, "send", nil))
	if !res.Pop || res.Toast != "已发出" {
		t.Fatalf("send: %+v", res)
	}
	has(t, "comment", lastWrite(t), "POST "+repoAPI+"/issues/12/comments", "LGTM, **thanks**")

	line := state{V: viewWrite, W: "line", Repo: w.Repo, Number: 12, Path: "cache.go"}
	res, _ = p.Action(ctx, req(3, line, "preview", map[string]string{"b": "Why?", "l": "9", "s": "RIGHT"}))
	has(t, "line out of the diff", checked(t, res.View), "第 9 行不在这次的改动里")
	res, _ = p.Action(ctx, req(3, line, "preview", map[string]string{"b": "Why?", "l": "2", "s": "LEFT"}))
	_ = json.Unmarshal([]byte(res.View.State), &line)
	if res, _ = p.Action(ctx, req(3, line, "send", nil)); !res.Pop {
		t.Fatalf("line: %+v", res)
	}
	has(t, "line", lastWrite(t), "POST "+repoAPI+"/pulls/12/comments", `"line":2`, `"side":"LEFT"`, `"commit_id":"abc123"`, `"path":"cache.go"`)

	reply := state{V: viewWrite, W: "reply", Repo: w.Repo, Number: 12, Path: "cache.go", ID: 100, Body: "Sure", Preview: true}
	if res, _ = p.Action(ctx, req(3, reply, "send", nil)); !res.Pop {
		t.Fatalf("reply: %+v", res)
	}
	has(t, "reply", lastWrite(t), "POST "+repoAPI+"/pulls/12/comments/100/replies", "Sure")

	review := state{V: viewWrite, W: "review", Repo: w.Repo, Number: 12}
	res, _ = p.Action(ctx, req(3, review, "preview", map[string]string{"e": "REQUEST_CHANGES", "b": ""}))
	has(t, "review without words", checked(t, res.View), "要写总评")
	res, _ = p.Action(ctx, req(3, review, "preview", map[string]string{"e": "APPROVE", "b": ""}))
	has(t, "approve", checked(t, res.View), "批准", `"confirm":"在 example-org/example-repo#12 上批准？"`)
	_ = json.Unmarshal([]byte(res.View.State), &review)
	if res, _ = p.Action(ctx, req(3, review, "send", nil)); res.Toast != "review 已提交" {
		t.Fatalf("review: %+v", res)
	}
	has(t, "review", lastWrite(t), "POST "+repoAPI+"/pulls/12/reviews", `"event":"APPROVE"`)

	merge := state{V: viewWrite, W: "merge", Repo: w.Repo, Number: 12}
	v, _ = p.Render(ctx, req(3, merge, "", nil))
	has(t, "merge form", checked(t, v), "Squash 合并", "cache → main", "可以合并")
	res, _ = p.Action(ctx, req(3, merge, "preview", map[string]string{"e": "squash"}))
	has(t, "merge preview", checked(t, res.View), "撤不回来", "把 example-org/example-repo#12 合并进 main")
	_ = json.Unmarshal([]byte(res.View.State), &merge)
	if res, _ = p.Action(ctx, req(3, merge, "send", nil)); res.Toast != "已合并" {
		t.Fatalf("merge: %+v", res)
	}
	has(t, "merge", lastWrite(t), "PUT "+repoAPI+"/pulls/12/merge", `"merge_method":"squash"`, `"sha":"abc123"`)
	merge.Ref = "moved"
	res, _ = p.Action(ctx, req(3, merge, "send", nil))
	has(t, "a head that moved", checked(t, res.View), "又有人推了新提交")
}

func TestEveryPageFitsEveryApp(t *testing.T) {
	p, _ := signedIn(t)
	ctx := context.Background()
	repo := "example-org/example-repo"
	pages := []state{
		{}, {Tab: github.TabMine}, {Tab: github.TabInvolved}, {Tab: github.TabInvolved, F: github.TabNotify}, {Tab: github.TabInvolved, F: github.TabActivity},
		{Tab: github.TabNotify}, {Tab: tabRepos},
		{V: viewRepo, Repo: repo}, {V: viewRepo, Repo: repo, Path: "src"}, {V: viewFile, Repo: repo, Ref: "main", Path: "src/main.go"},
		{V: viewFile, Repo: repo, Ref: "main", Path: "README.md"}, {V: viewFile, Repo: repo, Ref: "main", Path: "README.md", Src: true},
		{V: viewRepo, Repo: repo, Tab: tabBranches}, {V: viewRepo, Repo: repo, Tab: tabPulls}, {V: viewRepo, Repo: repo, Tab: tabIssues, F: "closed"},
		{V: viewRepo, Repo: repo, Tab: tabCommits},
		{Repo: repo, Number: 12}, {Repo: repo, Number: 12, Tab: tabFiles}, {Repo: repo, Number: 12, Tab: tabPRCommit}, {Repo: repo, Number: 12, Tab: tabChecks},
		{V: viewDiff, Repo: repo, Number: 12, Path: "cache.go"}, {V: viewCheck, Repo: repo, Number: 12, ID: 31},
		{V: viewWrite, W: "comment", Repo: repo, Number: 12}, {V: viewWrite, W: "review", Repo: repo, Number: 12, Event: "APPROVE", Preview: true},
		{V: viewWrite, W: "line", Repo: repo, Number: 12, Path: "cache.go"}, {V: viewWrite, W: "merge", Repo: repo, Number: 12},
	}
	for _, version := range []int{1, 2, 3} {
		for _, s := range pages {
			v, err := p.Render(ctx, req(version, s, "", nil))
			if err != nil {
				t.Fatalf("%d %+v: %v", version, s, err)
			}
			if err := wire.CheckView(v); err != nil {
				t.Errorf("UI %d %+v: %v", version, s, err)
			}
			checked(t, v)
		}
	}
}

func TestCleanMarkdown(t *testing.T) {
	for _, tc := range []struct{ in, base, want string }{
		{"![a](https://x.example/a.png)", "", "[a](https://x.example/a.png)"},
		{"![a](a.png)", "", "a"},
		{"[![build](https://img.example/b.svg)](https://ci.example/b)", "", "[build](https://ci.example/b)"},
		{"[x](../y.md)", "https://github.com/o/r/blob/main/docs/", "[x](https://github.com/o/r/blob/main/y.md)"},
		{"[top](#install)", "https://github.com/o/r/blob/main/", "top"},
		{"[x](http://insecure.example)", "", "x"},
		{"a <!-- hidden --> b", "", "a  b"},
		{"<!--\n Thanks!\n-->\n<!--\n```\n%h\n```\n-->\n\nReal **words**", "", "Real **words**"},
		{"```html\n<!-- kept -->\n```", "", "```html\n<!-- kept -->\n```"},
		{"```html\n<!-- kept -->\n```\n\n<!-- template -->\nText", "", "```html\n<!-- kept -->\n```\n\nText"},
		{"```html\n<!-- kept -->\n<!-- and me -->\n```\n<!-- a\n```\nb\n```\n-->", "", "```html\n<!-- kept -->\n<!-- and me -->\n```"},
		{"use `<!--` to start", "", "use `<!--` to start"},
		{"before <!-- never closed", "", "before"},
		{"`[x](javascript:y)`", "", "`[x](javascript:y)`"},
	} {
		got := cleanMarkdown(tc.in, tc.base)
		if got != tc.want {
			t.Errorf("cleanMarkdown(%q) = %q, want %q", tc.in, got, tc.want)
		}
		v := &wire.View{Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentMarkdown, Text: got}}}}}
		if err := wire.CheckView(v); err != nil {
			t.Errorf("%q: the hub refuses it: %v", got, err)
		}
	}
}

func TestParsePatch(t *testing.T) {
	hs := parsePatch(patch)
	if len(hs) != 1 || len(hs[0].lines) != 5 {
		t.Fatalf("%+v", hs)
	}
	l := hs[0].lines
	if l[1].kind != '-' || l[1].old != 2 || l[3].kind != '+' || l[3].new != 3 || l[4].new != 4 || l[4].old != 3 {
		t.Fatalf("numbers: %+v", l)
	}
	if !hasLine(patch, 3, "RIGHT") || !hasLine(patch, 2, "LEFT") || hasLine(patch, 4, "LEFT") || hasLine(patch, 9, "RIGHT") {
		t.Fatal("hasLine")
	}
}

// A page that takes longer than the budget is a loading page now and the page itself once read.
func TestSlowPageLoadsInTheBackground(t *testing.T) {
	p, _ := signedIn(t)
	ctx := context.Background()
	release := make(chan struct{})
	slowGate.Store(&release)
	defer slowGate.Store(nil)
	p.budget = 50 * time.Millisecond
	s := state{Repo: "example-org/example-repo", Number: 12}
	v, _ := p.Render(ctx, req(3, s, "", nil))
	got := checked(t, v)
	has(t, "loading", got, `"type":"loading"`, "正在从 GitHub 读取", `"refresh_after":30`, "example-org/example-repo#12")
	again, _ := p.Render(ctx, req(3, s, "", nil)) // still reading: the same read, not another
	has(t, "again", checked(t, again), `"type":"loading"`)
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		v, _ = p.Render(ctx, req(3, s, "", nil))
		if !strings.Contains(checked(t, v), `"type":"loading","key":"loading"`) || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	has(t, "read", checked(t, v), "Caches **answers**.", "概览")
}
