package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// assistantEnv is a computer with Persona's assistant directories in a home of its own, the node program running,
// and the assistant_writes setting as on says.
type assistantEnv struct {
	env
	home string
}

func newAssistantEnv(t *testing.T, on bool, dirs ...string) assistantEnv {
	t.Helper()
	e := assistantEnv{env: newEnv(t), home: t.TempDir()}
	if real, err := filepath.EvalSymlinks(e.home); err == nil {
		e.home = real
	}
	t.Setenv("HOME", e.home)
	for _, d := range []string{".jarvis/assistant/charters", ".persona/assistant", ".jarvis/health", "elsewhere"} {
		if err := os.MkdirAll(filepath.Join(e.home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	o := options{AssistantWrites: on, AssistantWriteDirs: dirs}
	b, _ := json.Marshal(o)
	writeJSON(t, filepath.Join(e.data, optionsFile), string(b))
	return e
}

// assistantCall is a Write or Edit from the assistant's main or supervisor conversation: Persona's node started it
// with PERSONA_ASSISTANT=1 in ~/.jarvis/assistant, so its transcript is in that directory's project.
func (e assistantEnv) assistantCall(tool, path string) map[string]any {
	return map[string]any{"hook_event_name": "PermissionRequest", "session_id": "5a1d0c2e-0000-4000-8000-000000000000",
		"cwd": filepath.Join(e.home, ".jarvis", "assistant"), "permission_mode": "default", "tool_name": tool,
		"transcript_path": filepath.Join(e.home, ".claude", "projects", "-home-u--jarvis-assistant", "5a1d0c2e.jsonl"),
		"tool_input":      map[string]any{"file_path": path, "content": "# charter\n"}}
}

var assistantProcess = func(k string) string {
	if k == "PERSONA_ASSISTANT" || k == "JARVIS_ASSISTANT" {
		return "1"
	}
	return ""
}

func (e assistantEnv) run(t *testing.T, req map[string]any, getenv func(string) string) string {
	t.Helper()
	b, _ := json.Marshal(req)
	var out bytes.Buffer
	runHook(hookArgs{ConfigDir: e.config, Data: e.data, getenv: getenv}, bytes.NewReader(b), &out, time.Now())
	return strings.TrimSpace(out.String())
}

func (e assistantEnv) charter() string {
	return filepath.Join(e.home, ".jarvis", "assistant", "charters", "weekly.md")
}

// 1. The setting is off (the default): the assistant's writes ask, as before.
func TestAssistantWritesOffAsks(t *testing.T) {
	e := newAssistantEnv(t, false)
	for _, tool := range []string{"Write", "Edit"} {
		if got := e.run(t, e.assistantCall(tool, e.charter()), assistantProcess); got != "" {
			t.Errorf("%s with the setting off: answered %q, should stay silent", tool, got)
		}
	}
	// No options file at all is the defaults too.
	if err := os.Remove(filepath.Join(e.data, optionsFile)); err != nil {
		t.Fatal(err)
	}
	if got := e.run(t, e.assistantCall("Write", e.charter()), assistantProcess); got != "" {
		t.Errorf("no options file: %q", got)
	}
	if n := len(readAudit(e.data, 10)); n != 0 {
		t.Errorf("audit has %d entries, want none", n)
	}
}

// 2. On, and the target is in a directory on the list: allowed, and logged.
func TestAssistantWritesOnAllowsListedDirectories(t *testing.T) {
	e := newAssistantEnv(t, true)
	for _, c := range []struct{ tool, path string }{
		{"Write", e.charter()},
		{"Edit", e.charter()},
		{"Write", filepath.Join(e.home, ".jarvis", "assistant", "charters", "new", "deep", "plan.md")}, // not there yet
		{"Edit", filepath.Join(e.home, ".persona", "assistant", "notes.md")},
	} {
		if got := e.run(t, e.assistantCall(c.tool, c.path), assistantProcess); got != allowOutput {
			t.Errorf("%s %s: %q, want allow", c.tool, c.path, got)
		}
	}
	// In every permission mode but plan, which writes nothing.
	for _, mode := range []string{"acceptEdits", "auto", "bypassPermissions", ""} {
		req := e.assistantCall("Write", e.charter())
		req["permission_mode"] = mode
		if got := e.run(t, req, assistantProcess); got != allowOutput {
			t.Errorf("mode %q: %q, want allow", mode, got)
		}
	}
	// A manager's own assistant directory (assistant-m-<hex>) is one too.
	req := e.assistantCall("Write", e.charter())
	req["transcript_path"] = filepath.Join(e.home, ".claude", "projects", "-home-u--persona-assistant-m-3f2a", "5a1d0c2e.jsonl")
	if got := e.run(t, req, assistantProcess); got != allowOutput {
		t.Errorf("a manager's assistant project: %q, want allow", got)
	}
	// The old name's variable alone counts (an older node mirrors both).
	jarvisOnly := func(k string) string {
		if k == "JARVIS_ASSISTANT" {
			return "1"
		}
		return ""
	}
	if got := e.run(t, e.assistantCall("Write", e.charter()), jarvisOnly); got != allowOutput {
		t.Errorf("JARVIS_ASSISTANT=1: %q, want allow", got)
	}
	recent := readAudit(e.data, 1)
	if len(recent) != 1 || recent[0].Tool != "Write" ||
		!strings.Contains(recent[0].Note, "assistant") {
		t.Errorf("audit: %+v", recent)
	}

	// The list is the setting's when it has one: the defaults are then not on it.
	e2 := newAssistantEnv(t, true, "~/elsewhere")
	if got := e2.run(t, e2.assistantCall("Write", filepath.Join(e2.home, "elsewhere", "a.md")), assistantProcess); got != allowOutput {
		t.Errorf("configured directory: %q, want allow", got)
	}
	if got := e2.run(t, e2.assistantCall("Write", e2.charter()), assistantProcess); got != "" {
		t.Errorf("a default directory not on a configured list: %q", got)
	}
}

// 3. On, but the target is not in a listed directory (or is, only by a link or ..), or the call is not a plain write:
// it asks.
func TestAssistantWritesOnAsksOutsideTheList(t *testing.T) {
	e := newAssistantEnv(t, true)
	assistant := filepath.Join(e.home, ".jarvis", "assistant")
	if err := os.Symlink(filepath.Join(e.home, "elsewhere"), filepath.Join(assistant, "out")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(e.home, "elsewhere", "victim.md")
	if err := os.WriteFile(outside, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(assistant, "charters", "link.md")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, tool, path string }{
		{"home", "Write", filepath.Join(e.home, ".bashrc")},
		{"another directory", "Edit", outside},
		{"/etc", "Write", "/etc/hosts"},
		{"the session's project", "Write", filepath.Join(e.project, "main.go")},
		{"the health space", "Write", filepath.Join(e.home, ".jarvis", "health", "log.md")},
		{"a name that starts the same", "Write", filepath.Join(e.home, ".jarvis", "assistant-bin", "x")},
		{"the listed directory itself", "Write", assistant},
		{"up and out with ..", "Write", filepath.Join(assistant, "charters", "..", "..", "..", ".bashrc")},
		{"through a directory link", "Write", filepath.Join(assistant, "out", "x.md")},
		{"through a file link", "Edit", filepath.Join(assistant, "charters", "link.md")},
		{"its Claude settings", "Write", filepath.Join(assistant, ".claude", "settings.json")},
		{"its skills", "Write", filepath.Join(assistant, ".claude", "skills", "x", "SKILL.md")},
		{"its git hooks", "Write", filepath.Join(assistant, ".git", "hooks", "pre-commit")},
		{"its MCP servers", "Write", filepath.Join(assistant, ".mcp.json")},
		{"a relative path", "Write", "charters/weekly.md"},
		{"no path", "Write", ""},
		// Only Write and Edit: deletion goes through Bash, which this setting does not touch.
		{"MultiEdit", "MultiEdit", e.charter()},
		{"NotebookEdit", "NotebookEdit", e.charter()},
		{"Bash rm", "Bash", e.charter()},
		{"WebFetch", "WebFetch", e.charter()},
		{"an MCP write", "mcp__lark__send", e.charter()},
	} {
		req := e.assistantCall(c.tool, c.path)
		if c.tool == "Bash" {
			req["tool_input"] = map[string]any{"command": "rm -rf " + c.path}
		}
		if got := e.run(t, req, assistantProcess); got != "" {
			t.Errorf("%s: answered %q, should stay silent", c.name, got)
		}
	}
	// Only a PermissionRequest.
	pre := e.assistantCall("Write", e.charter())
	pre["hook_event_name"] = "PreToolUse"
	if got := e.run(t, pre, assistantProcess); got != "" {
		t.Errorf("PreToolUse: %q", got)
	}
	// Plan mode writes nothing.
	req := e.assistantCall("Write", e.charter())
	req["permission_mode"] = "plan"
	if got := e.run(t, req, assistantProcess); got != "" {
		t.Errorf("plan mode: %q", got)
	}
	// The user's own rules about edits keep their prompt.
	writeJSON(t, filepath.Join(e.config, "settings.json"), `{"permissions":{"ask":["Edit(~/.jarvis/**)"]}}`)
	if got := e.run(t, e.assistantCall("Write", e.charter()), assistantProcess); got != "" {
		t.Errorf("an ask rule about Edit: %q", got)
	}
	if err := os.Remove(filepath.Join(e.config, "settings.json")); err != nil {
		t.Fatal(err)
	}
	// A node program that is not running allows nothing.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(e.data, heartbeatFile), old, old); err != nil {
		t.Fatal(err)
	}
	if got := e.run(t, e.assistantCall("Write", e.charter()), assistantProcess); got != "" {
		t.Errorf("stale heartbeat: %q", got)
	}
	if n := len(readAudit(e.data, 10)); n != 0 {
		t.Errorf("audit has %d entries, want none", n)
	}
}

// 4. On, but the call is not from an assistant conversation: an ordinary agent's write to the same file asks.
func TestAssistantWritesOnAsksForOtherAgents(t *testing.T) {
	e := newAssistantEnv(t, true)
	noEnv := func(string) string { return "" }
	other := func(req map[string]any) map[string]any {
		req["transcript_path"] = filepath.Join(e.home, ".claude", "projects", "-home-u-work-proj", "5a1d0c2e.jsonl")
		req["cwd"] = e.project
		return req
	}
	health := func(req map[string]any) map[string]any {
		req["transcript_path"] = filepath.Join(e.home, ".claude", "projects", "-home-u--jarvis-health", "5a1d0c2e.jsonl")
		return req
	}
	project := func(name string) func(map[string]any) map[string]any {
		return func(req map[string]any) map[string]any {
			req["transcript_path"] = filepath.Join(e.home, ".claude", "projects", name, "5a1d0c2e.jsonl")
			return req
		}
	}
	noTranscript := func(req map[string]any) map[string]any { delete(req, "transcript_path"); return req }
	for _, c := range []struct {
		name   string
		req    map[string]any
		getenv func(string) string
	}{
		{"an ordinary agent", other(e.assistantCall("Write", e.charter())), noEnv},
		{"an agent that cd'd into the assistant's directory", e.assistantCall("Write", e.charter()), noEnv},
		{"a process that inherited the variable, elsewhere", other(e.assistantCall("Write", e.charter())), assistantProcess},
		{"the health space", health(e.assistantCall("Write", e.charter())), assistantProcess},
		{"no transcript", noTranscript(e.assistantCall("Write", e.charter())), assistantProcess},
		{"a directory that only ends like the assistant's", project("-tmp-x-persona-assistant")(e.assistantCall("Write", e.charter())), assistantProcess},
		{"a directory under the assistant's", project("-home-u--persona-assistant-sub")(e.assistantCall("Write", e.charter())), assistantProcess},
		{"a guest conversation", e.assistantCall("Write", e.charter()), func(k string) string {
			if k == "PERSONA_GUEST" {
				return "meeting"
			}
			return assistantProcess(k)
		}},
		{"PERSONA_ASSISTANT=0", e.assistantCall("Write", e.charter()), func(k string) string {
			if k == "PERSONA_ASSISTANT" {
				return "0"
			}
			return ""
		}},
	} {
		if got := e.run(t, c.req, c.getenv); got != "" {
			t.Errorf("%s: answered %q, should stay silent", c.name, got)
		}
	}
	// And the setting changes nothing for an ordinary agent's Bash: still only in bypass mode.
	req := request(e.env, "default", "Bash", "ls")
	if got := e.run(t, req, noEnv); got != "" {
		t.Errorf("Bash outside bypass: %q", got)
	}
}

// The hook is run for Write and Edit only when the setting is on: off, the entry is the one from before.
func TestAssistantWritesMatcher(t *testing.T) {
	if got := matcherFor(false); got != hookMatcher {
		t.Errorf("off: %q", got)
	}
	if got := matcherFor(true); got != hookMatcher+"|Write|Edit" {
		t.Errorf("on: %q", got)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if _, err := installHookWith(p, matcherFor(true), hookCommand("/b", dir, "/d")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"matcher": "`+hookMatcher+`|Write|Edit"`) {
		t.Errorf("settings: %s", b)
	}
	if changed, err := installHook(p, hookCommand("/b", dir, "/d")); err != nil || !changed {
		t.Errorf("switching off rewrites the entry: changed %v, %v", changed, err)
	}
	b, _ = os.ReadFile(p)
	if strings.Contains(string(b), "|Write") {
		t.Errorf("off still matches Write: %s", b)
	}
}

// A Write through a link that leads nowhere yet must not be allowed just because the link itself is inside the listed
// directory; a listed directory that is a link to / is not a directory to allow writes in.
func TestAssistantWritableRejectsDanglingAndRootLinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "created-outside"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if got := assistantWritable(filepath.Join(root, "dangling"), []string{root}); got != "" {
		t.Errorf("a dangling link was allowed: %q", got)
	}
	if err := os.Symlink("/", filepath.Join(root, "root-link")); err != nil {
		t.Fatal(err)
	}
	if got := assistantWritable("/etc/hosts", []string{filepath.Join(root, "root-link")}); got != "" {
		t.Errorf("a link to / was allowed as a listed directory: %q", got)
	}
	if err := os.WriteFile(filepath.Join(root, "plain"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := assistantWritable(filepath.Join(root, "plain"), []string{root}); got != root {
		t.Errorf("a plain file was refused: %q", got)
	}
	if got := assistantWritable(filepath.Join(root, "new-file"), []string{root}); got != root {
		t.Errorf("a new file was refused: %q", got)
	}
}
