package x

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain: no test ever finds or runs a real grok (this machine may have one, signed in or not). The test binary is
// also the fake grok: run with X_FAKE_GROK set, it acts as grok would (fakeGrok).
func TestMain(m *testing.M) {
	if mode := os.Getenv("X_FAKE_GROK"); mode != "" {
		fakeGrok(mode)
		return
	}
	LookupGrok = func(string) string { return "" }
	os.Exit(m.Run())
}

// fakeGrok is grok as the tests need it.
func fakeGrok(mode string) {
	if out := os.Getenv("X_FAKE_GROK_OUT"); out != "" {
		wd, _ := os.Getwd()
		var cwd string
		for i, a := range os.Args {
			if a == "--cwd" && i+1 < len(os.Args) {
				cwd = os.Args[i+1]
			}
		}
		entries, _ := os.ReadDir(cwd)
		raw, _ := json.Marshal(map[string]any{"args": os.Args[1:], "env": os.Environ(), "wd": wd, "cwd": cwd, "entries": len(entries)})
		_ = os.WriteFile(out, raw, 0o600)
	}
	switch mode {
	case "ok":
		raw, _ := json.Marshal(map[string]any{"text": os.Getenv("X_FAKE_GROK_TEXT"), "stopReason": "EndTurn", "sessionId": "s", "requestId": "r"})
		os.Stdout.Write(raw)
	case "auth":
		os.Stdout.WriteString(`{"type":"error","message":"Unauthenticated: your session expired, run grok login"}`)
		os.Exit(1)
	case "rate":
		os.Stderr.WriteString("starting\nError: 429 Too Many Requests: rate limit reached\n")
		os.Exit(1)
	case "secret":
		os.Stderr.WriteString("failed reading " + os.Getenv("HOME") + "/.grok/auth.json with token abcdefghijklmnopqrstuvwxyz0123456789\n")
		os.Exit(2)
	case "big":
		chunk := strings.Repeat("x", 64<<10)
		for range 40 {
			os.Stdout.WriteString(chunk)
		}
	case "hang":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "X_FAKE_GROK=child", "X_FAKE_GROK_OUT=")
		_ = child.Start()
		time.Sleep(time.Minute)
	case "child":
		_ = os.WriteFile(os.Getenv("X_FAKE_GROK_PID"), []byte(strconv.Itoa(os.Getpid())), 0o600)
		time.Sleep(time.Minute)
	case "orphan": // answers and exits, leaving a child running (as an MCP server grok started could be)
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "X_FAKE_GROK=child", "X_FAKE_GROK_OUT=")
		_ = child.Start()
		for range 100 { // the child is up and has said who it is
			if raw, _ := os.ReadFile(os.Getenv("X_FAKE_GROK_PID")); len(raw) > 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		raw, _ := json.Marshal(map[string]any{"text": "done"})
		os.Stdout.Write(raw)
	}
}

// fake runs the test binary as grok in mode.
func fake(t *testing.T, mode string) (ExecGrok, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix only")
	}
	t.Setenv("X_FAKE_GROK", mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return ExecGrok{}, exe
}

func TestExecGrokSuccessArgsEnvAndCwd(t *testing.T) {
	g, exe := fake(t, "ok")
	out := filepath.Join(t.TempDir(), "seen.json")
	t.Setenv("X_FAKE_GROK_OUT", out)
	t.Setenv("X_FAKE_GROK_TEXT", `{"summary":"ok"}`)
	t.Setenv("PERSONA_HUB_TOKEN", "hub-token-not-for-grok")
	t.Setenv("JARVIS_HUB_TOKEN", "old-hub-token-not-for-grok")
	t.Setenv("persona_lower", "also-dropped")
	text, err := g.Run(context.Background(), exe, "find posts")
	if err != nil || text != `{"summary":"ok"}` {
		t.Fatalf("%q %v", text, err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var seen struct {
		Args    []string
		Env     []string
		WD, Cwd string
		Entries int
	}
	if err := json.Unmarshal(raw, &seen); err != nil {
		t.Fatal(err)
	}
	want := []string{"-p", "find posts", "--output-format", "json", "--disallowed-tools", strings.Join(grokLocalTools, ","),
		"--deny", "Bash", "--deny", "Read", "--deny", "Edit", "--deny", "Grep", "--deny", "WebFetch", "--deny", "MCPTool",
		"--deny", "AgentMessage", "--max-turns", "6", "--cwd"}
	if len(seen.Args) != len(want)+1 || !slices.Equal(seen.Args[:len(want)], want) {
		t.Fatalf("args %q", seen.Args)
	}
	if seen.Cwd == "" || seen.Entries != 0 {
		t.Fatalf("cwd %q with %d entries", seen.Cwd, seen.Entries)
	}
	if filepath.Base(seen.WD) != filepath.Base(seen.Cwd) { // macOS's /tmp is a link: compare the run's own directory
		t.Fatalf("ran in %s, --cwd %s", seen.WD, seen.Cwd)
	}
	if _, err := os.Stat(seen.Cwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the run's directory is still there: %v", err)
	}
	for _, kv := range seen.Env {
		k, _, _ := strings.Cut(kv, "=")
		if u := strings.ToUpper(k); strings.HasPrefix(u, "PERSONA_") || strings.HasPrefix(u, "JARVIS_") {
			t.Fatalf("grok got %s", k)
		}
	}
	if !slices.ContainsFunc(seen.Env, func(kv string) bool { return strings.HasPrefix(kv, "HOME=") }) && os.Getenv("HOME") != "" {
		t.Fatal("grok lost HOME (its sign-in is in ~/.grok)")
	}
}

func TestExecGrokErrors(t *testing.T) {
	for _, tc := range []struct{ mode, kind, has string }{
		{"auth", GrokAuth, "grok login"},
		{"rate", GrokRate, "429"},
		{"secret", GrokFailed, "~/.grok/auth.json"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			g, exe := fake(t, tc.mode)
			_, err := g.Run(context.Background(), exe, "p")
			var ge *GrokError
			if !errors.As(err, &ge) || ge.Kind != tc.kind || !strings.Contains(ge.Msg, tc.has) {
				t.Fatalf("%#v", err)
			}
			if strings.Contains(ge.Msg, "abcdefghijklmnopqrstuvwxyz0123456789") || len(ge.Msg) > 200 || strings.Contains(ge.Msg, "\n") {
				t.Fatalf("not scrubbed: %q", ge.Msg)
			}
			if home, _ := os.UserHomeDir(); home != "" && home != "/" && strings.Contains(ge.Msg, home) {
				t.Fatalf("home directory in %q", ge.Msg)
			}
		})
	}
	g := ExecGrok{}
	_, err := g.Run(context.Background(), filepath.Join(t.TempDir(), "no-grok"), "p")
	var ge *GrokError
	if !errors.As(err, &ge) || ge.Kind != GrokFailed {
		t.Fatalf("missing program: %#v", err)
	}
}

func TestExecGrokStdoutCap(t *testing.T) {
	g, exe := fake(t, "big")
	g.StdoutMost = 1 << 20
	_, err := g.Run(context.Background(), exe, "p")
	var ge *GrokError
	if !errors.As(err, &ge) || !strings.Contains(ge.Msg, "over") {
		t.Fatalf("%#v", err)
	}
}

// A run past its timeout is killed with everything it started.
func TestExecGrokTimeoutKillsTheGroup(t *testing.T) {
	g, exe := fake(t, "hang")
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("X_FAKE_GROK_PID", pidFile)
	g.Timeout = 2 * time.Second
	start := time.Now()
	_, err := g.Run(context.Background(), exe, "p")
	var ge *GrokError
	if !errors.As(err, &ge) || !strings.Contains(ge.Msg, "did not answer") {
		t.Fatalf("%#v", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("took %s", d)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the child never started: %v", err)
	}
	pid, _ := strconv.Atoi(string(raw))
	deadline := time.Now().Add(5 * time.Second)
	for !gone(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("grok's child %d outlived the run", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// gone says whether pid is no longer running (gone, or a zombie waiting for its reaper).
func gone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return runtime.GOOS == "linux"
	}
	_, rest, _ := strings.Cut(string(stat), ") ")
	return strings.HasPrefix(rest, "Z") || strings.HasPrefix(rest, "X")
}

// What grok started is killed when the run ends normally too, not only on a timeout.
func TestExecGrokEndKillsTheGroup(t *testing.T) {
	g, exe := fake(t, "orphan")
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("X_FAKE_GROK_PID", pidFile)
	if text, err := g.Run(context.Background(), exe, "p"); err != nil || text != "done" {
		t.Fatalf("%q %v", text, err)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the child never started: %v", err)
	}
	pid, _ := strconv.Atoi(string(raw))
	deadline := time.Now().Add(5 * time.Second)
	for !gone(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("grok's child %d outlived a run that ended", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// grok's arguments never carry --tools (it would turn X search off, or with x_search in it, every local tool on),
// and take away every local tool by name and by permission rule.
func TestGrokArgsKeepHostedSearchOnly(t *testing.T) {
	args := grokArgs("p", "/d", 3)
	if slices.Contains(args, "--tools") {
		t.Fatalf("--tools in %q", args)
	}
	i := slices.Index(args, "--disallowed-tools")
	if i < 0 {
		t.Fatalf("no --disallowed-tools in %q", args)
	}
	removed := strings.Split(args[i+1], ",")
	for _, name := range []string{"run_terminal_cmd", "bash", "read_file", "write", "search_replace", "apply_patch", "grep",
		"list_dir", "web_fetch", "task", "Agent", "use_tool", "search_tool", "image_gen"} {
		if !slices.Contains(removed, name) {
			t.Errorf("%s is not taken away", name)
		}
	}
	for _, name := range []string{"x_search", "web_search"} {
		if slices.Contains(removed, name) {
			t.Errorf("%s is taken away", name)
		}
	}
	var deny []string
	for j, a := range args {
		if a == "--deny" {
			deny = append(deny, args[j+1])
		}
	}
	for _, r := range []string{"Bash", "Read", "Edit", "Grep", "WebFetch", "MCPTool"} {
		if !slices.Contains(deny, r) {
			t.Errorf("no --deny %s in %q", r, args)
		}
	}
	if slices.Contains(args, "--yolo") || slices.Contains(args, "--disable-web-search") {
		t.Fatalf("%q", args)
	}
}

func TestExecGrokCancel(t *testing.T) {
	g, exe := fake(t, "hang")
	t.Setenv("X_FAKE_GROK_PID", filepath.Join(t.TempDir(), "child.pid"))
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := g.Run(ctx, exe, "p"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
}

func TestFindGrokOrder(t *testing.T) {
	home := t.TempDir()
	mk := func(p string) string {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	noPath := func(string) (string, error) { return "", exec.ErrNotFound }
	if got := FindGrok("", noPath, home); got != "" && !strings.HasPrefix(got, "/usr/local/bin/") && !strings.HasPrefix(got, "/opt/homebrew/") {
		t.Fatalf("found %s in an empty home", got)
	}
	local := mk(filepath.Join(home, ".local", "bin", "grok"))
	if got := FindGrok("", noPath, home); got != local {
		t.Fatalf("got %s, want %s", got, local)
	}
	installer := mk(filepath.Join(home, ".grok", "bin", "grok"))
	if got := FindGrok("", noPath, home); got != installer {
		t.Fatalf("got %s, want the installer's %s", got, installer)
	}
	inPath := mk(filepath.Join(t.TempDir(), "grok"))
	if got := FindGrok("", func(string) (string, error) { return inPath, nil }, home); got != inPath {
		t.Fatalf("PATH first: got %s", got)
	}
	configured := mk(filepath.Join(t.TempDir(), "my-grok"))
	if got := FindGrok(configured, func(string) (string, error) { return inPath, nil }, home); got != configured {
		t.Fatalf("configured first: got %s", got)
	}
	if got := FindGrok(filepath.Join(home, "missing"), func(string) (string, error) { return inPath, nil }, home); got != "" {
		t.Fatalf("a configured path that is not there: got %s", got)
	}
	notExec := filepath.Join(home, "plain")
	_ = os.WriteFile(notExec, nil, 0o644)
	if got := FindGrok(notExec, nil, home); got != "" {
		t.Fatalf("not executable: got %s", got)
	}
	if got := FindGrok("relative/grok", nil, home); got != "" {
		t.Fatalf("relative: got %s", got)
	}
}

func TestParseGrok(t *testing.T) {
	seen := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	body := `{"summary":"Two things happened.","topics":[{"title":"Databases","summary":"About databases","post_urls":["https://twitter.com/Example_Author/status/111?s=20","https://x.com/nobody/status/999","https://x.com/example_author/status/111"]}],
"posts":[
 {"url":"https://twitter.com/Example_Author/status/111?s=20&t=abc","author":"@example_author","name":"Example Author","text":"First post\nverbatim","at":"2026-11-06T08:00:00Z","why":"It started it"},
 {"url":"https://x.com/example_author/status/111","author":"example_author","text":"duplicate"},
 {"url":"https://x.com/other_author/status/222","author":"impostor","text":"Author fixed","at":"2026-11-06"},
 {"url":"https://example.com/other_author/status/333","author":"other_author","text":"not X"},
 {"url":"https://x.com/i/status/444","text":"no handle"},
 {"url":"https://x.com/other_author/status/abc","text":"no id"},
 {"url":"https://x.com/this_handle_is_far_too_long/status/555","text":"long handle"},
 {"url":"http://x.com/other_author/status/666","text":"not https"},
 {"url":42,"text":"not a string"},
 {"author":"no_url","text":"no url"}
]}`
	for name, text := range map[string]string{
		"bare":   body,
		"fenced": "Here is the digest:\n```json\n" + body + "\n```\nHope this helps {really}.",
		"lead":   "I found {a few} posts. " + body,
	} {
		t.Run(name, func(t *testing.T) {
			r, ok := ParseGrok(text, seen)
			if !ok {
				t.Fatal("not parsed")
			}
			if r.Summary != "Two things happened." || len(r.Posts) != 2 {
				t.Fatalf("%+v", r)
			}
			p := r.Posts[0]
			if p.ID != "111" || p.Author != "Example_Author" || p.Permalink() != "https://x.com/Example_Author/status/111" || p.Text != "First post\nverbatim" ||
				!p.At.Equal(time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)) || p.Seen != seen {
				t.Fatalf("first %+v", p)
			}
			if q := r.Posts[1]; q.ID != "222" || q.Author != "other_author" || !q.At.Equal(time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)) {
				t.Fatalf("author not taken from the url: %+v", q)
			}
			if len(r.Topics) != 1 || !slices.Equal(r.Topics[0].PostIDs, []string{"111"}) {
				t.Fatalf("topics %+v", r.Topics)
			}
		})
	}
	if _, ok := ParseGrok("no json here", seen); ok {
		t.Fatal("parsed nothing")
	}
	// Caps: posts, and a post's text.
	var posts []string
	for i := range 30 {
		posts = append(posts, fmt.Sprintf(`{"url":"https://x.com/a/status/%d","text":%q}`, i+1, strings.Repeat("y", 20000)))
	}
	r, ok := ParseGrok(`{"summary":"s","posts":[`+strings.Join(posts, ",")+`]}`, seen)
	if !ok || len(r.Posts) != grokPostsMost || len([]rune(r.Posts[0].Text)) != grokTextMost {
		t.Fatalf("caps: %d posts, text %d", len(r.Posts), len(r.Posts[0].Text))
	}
}

func TestClassifyGrok(t *testing.T) {
	for msg, want := range map[string]string{
		"HTTP 401 Unauthorized":                       GrokAuth,
		"Please run `grok login` to sign in":          GrokAuth,
		"Error: 429 Too Many Requests":                GrokRate,
		"You have reached your usage limit for today": GrokRate,
		"connection reset by peer":                    GrokFailed,
	} {
		if got := classifyGrok(msg); got != want {
			t.Errorf("%q: %s, want %s", msg, got, want)
		}
	}
}

func TestGrokEnv(t *testing.T) {
	got := grokEnv([]string{"HOME=/h", "PATH=/bin", "PERSONA_HUB_TOKEN=t", "JARVIS_X=y", "Persona_Mixed=z", "XAI_API_KEY=k"})
	if !slices.Equal(got, []string{"HOME=/h", "PATH=/bin", "XAI_API_KEY=k"}) {
		t.Fatalf("%v", got)
	}
}
