package x

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Grok source: no X account and no X keys. When the official xAI CLI `grok` (Grok Build) is installed on the hub
// machine and signed in with the user's own subscription (`grok login`), the extension runs it headless to search X
// and organize what it found. What grok answers is parsed as untrusted data (grokparse.go).
//
// X search is a hosted tool: xAI runs it server side, it is not one of grok's local tools. grok offers it only while
// its --tools allowlist is empty (or names it), so the run takes away grok's local tools another way (grokArgs), and
// runs in a fresh empty directory with none of the hub's or the extension's variables and never an X key.

// GrokRunner runs grok once with prompt and answers the text of its reply (the "text" of --output-format json).
// Its errors are *GrokError. Tests use a fake; the program uses ExecGrok.
type GrokRunner interface {
	Run(ctx context.Context, path, prompt string) (string, error)
}

// Kinds of a grok failure.
const (
	GrokAuth   = "auth"   // not signed in, or the sign-in expired (grok login)
	GrokRate   = "rate"   // rate limited or out of the subscription's quota
	GrokFailed = "failed" // anything else
)

// GrokError is a failed grok run: its kind and one short scrubbed line (no paths of the home directory, no tokens).
type GrokError struct {
	Kind string
	Msg  string
}

func (e *GrokError) Error() string { return "grok: " + e.Msg }

// ExecGrok runs the grok program.
type ExecGrok struct {
	// Timeout bounds a run (grokRunTimeout when 0); MaxTurns grok's agentic turns (grokMaxTurns when 0).
	Timeout  time.Duration
	MaxTurns int
	// StdoutMost bounds what is read of grok's output (grokStdoutMost when 0): more fails the run.
	StdoutMost int
}

// Bounds of a run.
const (
	grokRunTimeout = 4 * time.Minute
	grokMaxTurns   = 6
	grokStdoutMost = 1 << 20
	grokStderrMost = 64 << 10
	// grokWaitDelay is how long the run waits for grok's output after killing it.
	grokWaitDelay = 5 * time.Second
)

// grokLocalTools are grok's local tools, by their short names in every toolset grok has (Grok Build's own, the
// concise and hashline ones, Codex's, opencode's): --disallowed-tools removes each one it finds; a name this grok
// does not have only logs a warning. grokDeny are permission rules on top, for a tool this list misses (a newer grok,
// a tool pack): no shell, no reading, editing or searching files, no fetching, no MCP tool (grok also loads the MCP
// servers of ~/.grok/config.toml, ~/.claude.json and ~/.cursor/mcp.json, and no flag keeps them from loading), no
// messages to other agents. Headless grok also cancels any tool call that would ask for permission.
var (
	grokLocalTools = []string{
		"run_terminal_cmd", "run_terminal_command", "bash", "get_task_output", "get_terminal_command_output",
		"get_command_or_subagent_output", "kill_task", "kill_terminal_command", "kill_command_or_subagent", "wait_tasks",
		"monitor", "read_file", "read", "hashline_read", "write", "edit", "search_replace", "hashline_edit", "apply_patch",
		"grep", "grep_files", "hashline_grep", "glob", "list_dir", "lsp", "web_fetch", "todo_write", "todowrite",
		"workflow", "task", "spawn_subagent", "Agent", "send_subagent_message", "scheduler_create", "scheduler_delete",
		"scheduler_list", "image_gen", "image_edit", "image_to_video", "reference_to_video", "video_gen",
		"ask_user_question", "enter_plan_mode", "exit_plan_mode", "update_goal", "send_feedback", "skill",
		"memory_get", "memory_search", "search_tool", "use_tool",
	}
	grokDeny = []string{"Bash", "Read", "Edit", "Grep", "WebFetch", "MCPTool", "AgentMessage"}
)

// grokArgs are grok's arguments: headless, JSON out, every local tool taken away, X search and web search (hosted
// tools) left. Never --tools: a non-empty allowlist that does not name x_search turns X search off, and naming
// x_search (not a local tool) makes grok drop the whole allowlist and keep every local tool.
func grokArgs(prompt, dir string, maxTurns int) []string {
	args := []string{"-p", prompt, "--output-format", "json", "--disallowed-tools", strings.Join(grokLocalTools, ",")}
	for _, r := range grokDeny {
		args = append(args, "--deny", r)
	}
	return append(args, "--max-turns", strconv.Itoa(maxTurns), "--cwd", dir)
}

// Run runs grok in a fresh empty directory, removed afterwards; cancelling ctx (or the timeout) kills grok's whole
// process group, and so does the end of the run (what grok started and left running goes with it).
func (e ExecGrok) Run(ctx context.Context, path, prompt string) (string, error) {
	timeout, turns, most := e.Timeout, e.MaxTurns, e.StdoutMost
	if timeout <= 0 {
		timeout = grokRunTimeout
	}
	if turns <= 0 {
		turns = grokMaxTurns
	}
	if most <= 0 {
		most = grokStdoutMost
	}
	dir, err := os.MkdirTemp("", "persona-x-grok-")
	if err != nil {
		return "", &GrokError{Kind: GrokFailed, Msg: "no temporary directory: " + scrubLine(err.Error())}
	}
	defer os.RemoveAll(dir)
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(rctx, path, grokArgs(prompt, dir, turns)...)
	cmd.Dir = dir
	cmd.Env = grokEnv(os.Environ())
	stdout, stderr := &capped{most: most}, &capped{most: grokStderrMost}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	ownGroup(cmd)
	cmd.WaitDelay = grokWaitDelay
	err = cmd.Run()
	reapGroup(cmd)
	if rctx.Err() != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", &GrokError{Kind: GrokFailed, Msg: fmt.Sprintf("grok did not answer within %s", timeout)}
	}
	if stdout.over {
		return "", &GrokError{Kind: GrokFailed, Msg: fmt.Sprintf("grok's answer is over %d bytes", most)}
	}
	out := bytes.TrimSpace(stdout.buf.Bytes())
	var res struct {
		Type    string  `json:"type"`
		Message string  `json:"message"`
		Text    *string `json:"text"`
	}
	jerr := json.Unmarshal(out, &res)
	if err != nil || res.Type == "error" {
		if cmd.Process == nil {
			// grok never started (missing, not executable, no interpreter): its path is not grok speaking, so the
			// reason is told without it and is never a sign-in or a rate limit.
			why := err
			var nf *exec.Error
			var pe *fs.PathError
			switch {
			case errors.As(err, &nf):
				why = nf.Err
			case errors.As(err, &pe):
				why = pe.Err
			}
			return "", &GrokError{Kind: GrokFailed, Msg: "cannot run grok: " + scrubLine(why.Error())}
		}
		msg := res.Message
		if jerr != nil || msg == "" {
			msg = lastLine(stderr.buf.String())
		}
		if msg == "" && err != nil {
			msg = err.Error()
		}
		home, _ := os.UserHomeDir()
		return "", &GrokError{Kind: classifyGrok(withoutPaths(msg, dir, path, home)), Msg: scrubLine(msg)}
	}
	if jerr != nil || res.Text == nil {
		return "", &GrokError{Kind: GrokFailed, Msg: "grok's answer is not the JSON of --output-format json"}
	}
	return *res.Text, nil
}

// grokEnv is env without the hub's and the extension's variables (PERSONA_*, JARVIS_*: tokens, sockets, data
// directories). HOME stays: grok's sign-in is in ~/.grok.
func grokEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		u := strings.ToUpper(k)
		if strings.HasPrefix(u, "PERSONA_") || strings.HasPrefix(u, "JARVIS_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// capped keeps the first most bytes written to it and notes that more came.
type capped struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	most int
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.most - c.buf.Len(); room < len(p) {
		c.over = true
		c.buf.Write(p[:max(room, 0)])
	} else {
		c.buf.Write(p)
	}
	return len(p), nil
}

// classifyGrok sorts a failure by what grok said.
func classifyGrok(msg string) string {
	m := strings.ToLower(msg)
	for _, w := range []string{"401", "unauthenticated", "unauthorized", "not logged in", "not signed in", "grok login", "log in", "login",
		"sign in", "sign-in", "re-authenticate", "reauthenticate", "token expired", "session expired", "credentials"} {
		if strings.Contains(m, w) {
			return GrokAuth
		}
	}
	for _, w := range []string{"429", "rate limit", "rate-limit", "ratelimit", "too many requests", "quota", "usage limit", "limit reached",
		"resource_exhausted", "resource exhausted"} {
		if strings.Contains(m, w) {
			return GrokRate
		}
	}
	return GrokFailed
}

// withoutPaths is msg with this run's paths (grok's directory, grok itself, the home directory) taken out, most
// specific first, for classifyGrok: their names are not grok's words, and a user called login or a temporary
// directory ending in 401 is neither a sign-in nor a rate limit.
func withoutPaths(msg string, paths ...string) string {
	for _, p := range paths {
		if len(p) > 1 {
			msg = strings.ReplaceAll(msg, p, " ")
		}
	}
	return msg
}

// lastLine is s's last non-empty line.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// tokenish are runs that may be a token or a key: long, without spaces.
var tokenish = regexp.MustCompile(`[A-Za-z0-9_\-.+/=]{24,}`)

// scrubLine is s as one short line people may read: the home directory as ~, anything token-like left out.
func scrubLine(s string) string {
	s = oneLine(s)
	if home, err := os.UserHomeDir(); err == nil && len(home) > 1 {
		s = strings.ReplaceAll(s, home, "~")
	}
	s = tokenish.ReplaceAllString(s, "…")
	return clip(s, 160)
}

// grokPlaces are where grok is looked for after PATH: the installer's and the usual bin directories.
func grokPlaces(home string) []string {
	var out []string
	if home != "" {
		out = append(out, filepath.Join(home, ".grok", "bin", "grok"), filepath.Join(home, ".local", "bin", "grok"))
	}
	return append(out, "/usr/local/bin/grok", "/opt/homebrew/bin/grok")
}

// FindGrok is grok's path: configured when set (and runnable), else grok in PATH, else the first runnable of
// grokPlaces; "" when there is none.
func FindGrok(configured string, lookPath func(string) (string, error), home string) string {
	if configured = strings.TrimSpace(configured); configured != "" {
		if runnable(configured) {
			return configured
		}
		return ""
	}
	if lookPath != nil {
		if p, err := lookPath("grok"); err == nil && p != "" {
			if abs, err := filepath.Abs(p); err == nil {
				return abs
			}
			return p
		}
	}
	for _, p := range grokPlaces(home) {
		if runnable(p) {
			return p
		}
	}
	return ""
}

// runnable says whether p is an executable file.
func runnable(p string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

// LookupGrok finds grok for the settings' grok_path. Tests replace it: no test ever finds a real grok.
var LookupGrok = func(configured string) string {
	home, _ := os.UserHomeDir()
	return FindGrok(configured, exec.LookPath, home)
}
