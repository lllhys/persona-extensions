package model

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fake writes a stand-in for the command line: it logs its arguments, its stdin and its working directory's
// listing under log, counts its runs, and prints the file named by mode.
func fake(t *testing.T, outputs map[string]string) (cmd, log string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	dir := t.TempDir()
	log = filepath.Join(dir, "log")
	if err := os.MkdirAll(log, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range outputs {
		if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
log="` + log + `"
n=$(ls "$log" | grep -c '^run' )
n=$((n+1))
mkdir "$log/run$n"
for a in "$@"; do printf '%s\n' "$a" >> "$log/run$n/args"; done
cat > "$log/run$n/stdin"
pwd > "$log/run$n/pwd"
ls -A > "$log/run$n/ls"
mode=$(cat "` + dir + `/mode")
if [ -f "` + dir + `/mode$n" ]; then mode=$(cat "` + dir + `/mode$n"); fi
if [ "$mode" = fail ]; then echo "boom" >&2; exit 3; fi
cat "` + dir + `/$mode.json"
`
	cmd = filepath.Join(dir, "claude")
	if err := os.WriteFile(cmd, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return cmd, log
}

func setMode(t *testing.T, cmd, mode string, run ...int) {
	t.Helper()
	name := "mode"
	if len(run) > 0 {
		name += string(rune('0' + run[0]))
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(cmd), name), []byte(mode), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runs(t *testing.T, log string) int {
	t.Helper()
	es, _ := os.ReadDir(log)
	return len(es)
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var req = Request{Step: "daily", Tier: Small, System: "you are a curator", User: `{"lines":["a"]}`,
	Schema: json.RawMessage(`{ "type": "object",
  "properties": {"ok": {"type": "boolean"}} }`)}

// TestCLIStructuredOutput: the command gets the flags of a tool-less one-turn call, the user text on stdin and an
// empty working directory; the answer is structured_output re-marshalled, the usage summed.
func TestCLIStructuredOutput(t *testing.T) {
	cmd, log := fake(t, map[string]string{
		"structured": `{"type":"result","is_error":false,"result":"ignored prose","structured_output":{"ok": true, "n": [1, 2]},
			"usage":{"input_tokens":10,"cache_read_input_tokens":5,"cache_creation_input_tokens":2,"output_tokens":7}}`,
	})
	setMode(t, cmd, "structured")
	c := CLI{Command: cmd, Models: map[Tier]string{Small: "model-s", Medium: "model-m"}, Effort: "low"}
	resp, err := c.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"n":[1,2],"ok":true}` || resp.InTokens != 17 || resp.OutTokens != 7 || resp.Model != "model-s" {
		t.Errorf("response: %+v", resp)
	}
	args := strings.Split(strings.TrimSuffix(read(t, filepath.Join(log, "run1", "args")), "\n"), "\n")
	want := []string{"-p", "--model", "model-s", "--effort", "low", "--system-prompt", "you are a curator", "--tools", "",
		"--strict-mcp-config", "--no-session-persistence", "--output-format", "json", "--json-schema",
		`{"type":"object","properties":{"ok":{"type":"boolean"}}}`}
	if strings.Join(args, "|") != strings.Join(want, "|") {
		t.Errorf("args:\n%q\nwant\n%q", args, want)
	}
	if got := read(t, filepath.Join(log, "run1", "stdin")); got != req.User {
		t.Errorf("stdin %q", got)
	}
	if ls := read(t, filepath.Join(log, "run1", "ls")); ls != "" {
		t.Errorf("working directory not empty: %q", ls)
	}
	if wd := strings.TrimSpace(read(t, filepath.Join(log, "run1", "pwd"))); !strings.Contains(wd, "enkeep-memory-model-") {
		t.Errorf("working directory %q", wd)
	} else if _, err := os.Stat(wd); !os.IsNotExist(err) {
		t.Errorf("working directory left behind: %v", err)
	}
	// The medium tier takes its own model.
	m := req
	m.Tier = Medium
	if resp, _ := c.Complete(context.Background(), m); resp.Model != "model-m" {
		t.Errorf("medium model %q", resp.Model)
	}
}

// TestCLIResultAndErrors: without structured_output the answer is result; is_error and a failing command are
// errors after one retry, and a retry that succeeds answers.
func TestCLIResultAndErrors(t *testing.T) {
	cmd, log := fake(t, map[string]string{
		"plain": `{"is_error":false,"result":"{\"ok\":false}","usage":{"input_tokens":3,"output_tokens":1}}`,
		"error": `{"is_error":true,"subtype":"error_max_turns","result":"no"}`,
	})
	c := CLI{Command: cmd}
	ctx := context.Background()
	setMode(t, cmd, "plain")
	if resp, err := c.Complete(ctx, req); err != nil || resp.Text != `{"ok":false}` || resp.InTokens != 3 {
		t.Fatalf("plain: %+v %v", resp, err)
	}
	setMode(t, cmd, "error")
	if _, err := c.Complete(ctx, req); err == nil || !strings.Contains(err.Error(), "error_max_turns") {
		t.Errorf("is_error: %v", err)
	}
	if n := runs(t, log); n != 3 {
		t.Errorf("runs %d, want 3 (one, then an error retried once)", n)
	}
	setMode(t, cmd, "fail")
	if _, err := c.Complete(ctx, req); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("failing command: %v", err)
	}
	setMode(t, cmd, "plain", 7) // the 6th run fails, the 7th (its retry) answers
	if resp, err := c.Complete(ctx, req); err != nil || resp.Text != `{"ok":false}` {
		t.Errorf("retry: %+v %v", resp, err)
	}
	if n := runs(t, log); n != 7 {
		t.Errorf("runs %d, want 7", n)
	}
}

// TestCLICache: with a cache directory, the same request on the same model is answered from disk without running
// the command (and costs no tokens); a changed prompt or model runs it again.
func TestCLICache(t *testing.T) {
	cmd, log := fake(t, map[string]string{
		"structured": `{"is_error":false,"structured_output":{"ok":true},"usage":{"input_tokens":10,"output_tokens":7}}`,
	})
	setMode(t, cmd, "structured")
	c := CLI{Command: cmd, Models: map[Tier]string{Small: "a"}, CacheDir: filepath.Join(t.TempDir(), "cache")}
	ctx := context.Background()
	first, err := c.Complete(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.Complete(ctx, req)
	if err != nil || again.Text != first.Text || again.InTokens != 0 || again.OutTokens != 0 {
		t.Errorf("cached: %+v %v", again, err)
	}
	if n := runs(t, log); n != 1 {
		t.Errorf("runs %d, want 1", n)
	}
	changed := req
	changed.System += " v2"
	_, _ = c.Complete(ctx, changed)
	other := c
	other.Models = map[Tier]string{Small: "b"}
	_, _ = other.Complete(ctx, req)
	if n := runs(t, log); n != 3 {
		t.Errorf("runs %d, want 3", n)
	}
}
