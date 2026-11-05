package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// The hook: what Claude Code runs on PermissionRequest (settings.go writes the entry). It reads the request on stdin
// and answers allow only when every one of these holds; otherwise it prints nothing, so Claude Code (and any other
// hook) decides as if it were not there:
//
//   - the session's permission mode is bypassPermissions, and the tool is Bash;
//   - the node program is running (its heartbeat file is fresh): an extension that was switched off, stopped or
//     uninstalled without the chance to remove the entry allows nothing;
//   - no ask or deny rule for Bash that the hook can read could match the command (rules.go): those prompts are the
//     user's own and stay.
//
// It never fails the call: a hook that cannot read its input or write its log stays silent.

// heartbeatFresh is how old the node program's heartbeat may be (it touches it every heartbeatEvery).
const (
	heartbeatEvery = time.Minute
	heartbeatFresh = 5 * time.Minute
)

// hookInput is the part of Claude Code's PermissionRequest input the hook reads.
type hookInput struct {
	HookEventName  string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	Cwd            string `json:"cwd"`
	PermissionMode string `json:"permission_mode"`
	ToolName       string `json:"tool_name"`
	AgentID        string `json:"agent_id"`
	ToolInput      struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// hookArgs are the flags the entry passes: where its files are.
type hookArgs struct {
	ConfigDir string // the Claude config directory the entry is in (its settings hold the user's rules)
	Data      string // the node program's data directory: heartbeat and audit log
}

// allowOutput is Claude Code's answer for a PermissionRequest that lets the call run.
const allowOutput = `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`

// runHook answers one PermissionRequest: in is what Claude Code wrote, out where the answer goes.
func runHook(a hookArgs, in io.Reader, out io.Writer, now time.Time) {
	raw, err := io.ReadAll(io.LimitReader(in, 4<<20))
	if err != nil {
		return
	}
	var req hookInput
	if json.Unmarshal(raw, &req) != nil {
		return
	}
	if why := refuse(a, req, now); why != "" {
		return
	}
	appendAudit(filepath.Join(a.Data, auditFile), auditEntry{
		At: now.UTC().Format(time.RFC3339), Session: req.SessionID, Agent: req.AgentID, Cwd: req.Cwd,
		Command: redact(req.ToolInput.Command), Length: utf8.RuneCountInString(req.ToolInput.Command), ConfigDir: a.ConfigDir,
	})
	tellNode(raw)
	_, _ = io.WriteString(out, allowOutput+"\n")
}

// nodeSockets are where Persona's node listens for its hooks on this computer (node.sock in its home directory, the
// old name's first when both are the same directory).
func nodeSockets() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{filepath.Join(home, ".persona", "node.sock"), filepath.Join(home, ".jarvis", "node.sock")}
}

// nodeWait is the most tellNode waits for the node.
const nodeWait = 500 * time.Millisecond

// tellNode tells Persona's node that this call is answered (hook.decided, nwverse/persona#2648), with the input
// Claude Code gave this hook, which is what Persona's own hook got too: the node then shows no card on the phone for
// it, or withdraws the one it showed. A node that is not there, or too old to know hook.decided, changes nothing.
func tellNode(input []byte) {
	line, err := json.Marshal(map[string]any{"kind": "hook.decided", "input": json.RawMessage(input)})
	if err != nil {
		return
	}
	for _, path := range nodeSockets() {
		c, err := net.DialTimeout("unix", path, nodeWait)
		if err != nil {
			continue
		}
		_ = c.SetDeadline(time.Now().Add(nodeWait))
		_, err = c.Write(append(line, '\n'))
		if err == nil {
			_, _ = bufio.NewReader(c).ReadBytes('\n')
		}
		c.Close()
		return
	}
}

// refuse says why the hook leaves req to Claude Code ("" lets it allow).
func refuse(a hookArgs, req hookInput, now time.Time) string {
	switch {
	case req.HookEventName != "" && req.HookEventName != "PermissionRequest":
		return "not a PermissionRequest"
	case req.PermissionMode != "bypassPermissions":
		return "not bypassPermissions"
	case req.ToolName != "Bash":
		return "not Bash"
	case strings.TrimSpace(req.ToolInput.Command) == "":
		return "no command"
	case a.Data == "" || !alive(filepath.Join(a.Data, heartbeatFile), now):
		return "the node program is not running"
	}
	if r := matchingRule(userRules(a.ConfigDir, req.Cwd), req.ToolInput.Command); r != "" {
		return "a rule of the user's: " + r
	}
	return ""
}

// alive says whether the heartbeat at path is fresh.
func alive(path string, now time.Time) bool {
	st, err := os.Stat(path)
	return err == nil && now.Sub(st.ModTime()) < heartbeatFresh
}

// The audit log: one JSON line per auto-allow, in the node program's data directory; the newest auditKeep bytes in
// two files (audit.jsonl, and the one before it).
const (
	auditFile     = "audit.jsonl"
	heartbeatFile = "alive"
	auditKeep     = 512 << 10
	commandShown  = 240 // runes of a command kept in the log
)

type auditEntry struct {
	At        string `json:"at"`
	Session   string `json:"session_id"`
	Agent     string `json:"agent_id,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	Command   string `json:"command"` // redacted and cut
	Length    int    `json:"length"`  // of the whole command, in characters
	ConfigDir string `json:"config_dir,omitempty"`
}

func appendAudit(path string, e auditEntry) {
	b, err := marshalPlain(e) // && and > as they are, for whoever reads the file
	if err != nil {
		return
	}
	if st, err := os.Stat(path); err == nil && st.Size() > auditKeep {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// readAudit is the newest n entries, newest first.
func readAudit(dir string, n int) []auditEntry {
	var all []auditEntry
	for _, name := range []string{auditFile + ".1", auditFile} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			var e auditEntry
			if json.Unmarshal([]byte(line), &e) == nil && e.At != "" {
				all = append(all, e)
			}
		}
	}
	out := make([]auditEntry, 0, min(n, len(all)))
	for i := len(all) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, all[i])
	}
	return out
}

// What looks like a secret in a command, masked before it is logged.
var secretPatterns = []struct {
	re   *regexp.Regexp
	with string
}{
	// NAME=value where the name says what it is: API_KEY=…, GITHUB_TOKEN=…, PASSWORD='…'
	{regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|PASSPHRASE|API_?KEY|ACCESS_?KEY|PRIVATE_?KEY|AUTH|CREDENTIALS?|COOKIE|SESSION)[A-Z0-9_]*)=("[^"]*"|'[^']*'|\S+)`), "$1=***"},
	// Authorization: Bearer …, token …, Basic …
	{regexp.MustCompile(`(?i)\b(bearer|token|basic)(\s+)[A-Za-z0-9._~+/=-]{8,}`), "$1$2***"},
	// --password=…, --token …, -p…: flags that carry one
	{regexp.MustCompile(`(?i)(--?(?:password|passwd|token|secret|api-?key|auth)[= ])("[^"]*"|'[^']*'|\S+)`), "$1***"},
	// user:password@ in a URL
	{regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@`), "://***@"},
	// long opaque strings: keys, tokens, hashes
	{regexp.MustCompile(`[A-Za-z0-9_\-+/=]{32,}`), "***"},
}

// redact masks what looks like a secret and cuts the command to commandShown characters.
func redact(cmd string) string {
	for _, p := range secretPatterns {
		cmd = p.re.ReplaceAllString(cmd, p.with)
	}
	cmd = strings.Join(strings.Fields(cmd), " ")
	if utf8.RuneCountInString(cmd) > commandShown {
		cmd = string([]rune(cmd)[:commandShown]) + "…"
	}
	return cmd
}
