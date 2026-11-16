package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Persona assistant writes (the assistant_writes setting, off by default): Persona's main and supervisor
// conversations do not run in bypassPermissions, so each Write or Edit of their own notes (charters, plans) puts a
// card in front of the user. With the setting on, the hook allows those two tools, in any mode but plan, when all of
// these hold; otherwise it stays silent, as for every other call:
//
//   - the call comes from a Persona assistant conversation (isAssistantSession);
//   - the file, with its links and ..s resolved, is inside one of the listed directories (assistant_write_dirs,
//     ~/.jarvis/assistant and ~/.persona/assistant by default) and not in their .claude or .git or their .mcp.json:
//     those would let a conversation change its own permissions, hooks or servers;
//   - the node program is running, and no ask or deny rule of the user's could be about the call (as for Bash).
//
// Deletion goes through Bash, and network writes through Bash or MCP tools; neither is touched by this setting.

// assistantWriteTools are the tools the setting answers for. MultiEdit and NotebookEdit stay with Claude Code.
var assistantWriteTools = []string{"Write", "Edit"}

// defaultAssistantWriteDirs are where Persona keeps the assistant's directory, under the current and the old name.
var defaultAssistantWriteDirs = []string{"~/.jarvis/assistant", "~/.persona/assistant"}

// matcherFor is the entry's matcher: Write and Edit join it only while the setting is on, so with it off the entry
// is the one from before and the hook is not even run for them.
func matcherFor(assistantWrites bool) string {
	if !assistantWrites {
		return hookMatcher
	}
	return hookMatcher + "|" + strings.Join(assistantWriteTools, "|")
}

func isAssistantWriteTool(name string) bool {
	for _, t := range assistantWriteTools {
		if t == name {
			return true
		}
	}
	return false
}

// isAssistantSession says whether the call comes from a Persona assistant conversation. Both must hold:
//
//   - Persona's node started the Claude Code process as an assistant: it sets PERSONA_ASSISTANT=1 (and the old name,
//     JARVIS_ASSISTANT=1) in that process's environment only, and Claude Code passes its environment to the hook;
//   - the conversation's transcript is in the project of the assistant's directory (…/projects/-…-persona-assistant…
//     or -jarvis-assistant…). Claude Code names the project after the directory the process started in, which a
//     later cd does not change; Persona's node reads the same thing (IsAssistantTranscript). The health space's
//     conversations (-persona-health) are not assistant ones here: Persona confirms their writes every time.
//
// So an agent that cd's into the assistant's directory, or a process started from an assistant's shell (which
// inherits the variable) in some other directory, is not one.
func isAssistantSession(req hookInput, getenv func(string) string) bool {
	if getenv("PERSONA_ASSISTANT") != "1" && getenv("JARVIS_ASSISTANT") != "1" {
		return false
	}
	if getenv("PERSONA_GUEST") != "" || getenv("JARVIS_GUEST") != "" { // a guest conversation is not the user's assistant
		return false
	}
	if req.TranscriptPath == "" {
		return false
	}
	return assistantProject.MatchString(filepath.Base(filepath.Dir(req.TranscriptPath)))
}

// assistantProject is the project Claude Code names after an assistant directory: …/.persona/assistant becomes
// …--persona-assistant (the / and the . both turn into -), and a manager's own …/.persona/assistant-m-<hex>. A
// directory that only ends the same way (/tmp/x-persona-assistant) has one dash, and is not one.
var assistantProject = regexp.MustCompile(`--(persona|jarvis)-assistant(-m-[0-9a-f]+)?$`)

// refuseAssistantWrite says why the hook leaves a Write or Edit to Claude Code ("" lets it allow), and the note for
// the audit log when it allows.
func refuseAssistantWrite(a hookArgs, req hookInput, o options, now time.Time) (why, note string) {
	switch {
	case req.HookEventName != "" && req.HookEventName != "PermissionRequest":
		return "not a PermissionRequest", ""
	case !o.AssistantWrites:
		return "assistant writes are off", ""
	case req.PermissionMode == "plan":
		return "plan mode", ""
	case !isAssistantSession(req, a.env()):
		return "not a Persona assistant conversation", ""
	case a.Data == "" || !alive(filepath.Join(a.Data, heartbeatFile), now):
		return "the node program is not running", ""
	}
	dir := assistantWritable(req.ToolInput.FilePath, o.assistantDirs())
	if dir == "" {
		return "not in a listed assistant directory: " + req.ToolInput.FilePath, ""
	}
	if r := matchingRule(userRules(a.ConfigDir, req.Cwd), req.ToolName, ""); r != "" {
		return "a rule of the user's: " + r, ""
	}
	return "", "Persona assistant conversation, in " + dir
}

// assistantWritable is the listed directory p is in ("" when it is in none): p must be absolute, and once its links
// and ..s are resolved it must be under one of dirs (resolved too), not the directory itself, and not in its .claude
// or .git or its .mcp.json.
func assistantWritable(p string, dirs []string) string {
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	clean, ok := physical(p)
	if !ok || danglingLink(clean) {
		return "" // a Write through a link that leads nowhere yet would land outside the listed directory
	}
	real := resolve(clean, true) // a Write to a link writes where it leads
	for _, d := range dirs {
		abs, err := expand(d)
		if err != nil || abs == "/" {
			continue
		}
		root := resolve(abs, true)
		if root == "/" || real == root || !within(real, root) {
			continue
		}
		rel := strings.TrimPrefix(real, strings.TrimSuffix(root, "/")+"/")
		if protectedAssistantPath(rel) {
			return ""
		}
		return d
	}
	return ""
}

// danglingLink says whether p or one of its parents is a symlink that does not resolve. Such a link inside a listed
// directory can point outside it even when its target does not exist yet.
func danglingLink(p string) bool {
	for d := p; d != "/" && d != "."; d = filepath.Dir(d) {
		info, err := os.Lstat(d)
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if _, err := filepath.EvalSymlinks(d); err != nil {
				return true
			}
		}
	}
	return false
}

// protectedAssistantPath says whether rel (inside a listed directory) is one a conversation must not write without
// asking: its Claude settings, skills and hooks (.claude), its repository's hooks and config (.git), its MCP servers
// (.mcp.json). Letter case does not count: macOS's file systems ignore it.
func protectedAssistantPath(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, part := range parts {
		if strings.EqualFold(part, ".claude") || strings.EqualFold(part, ".git") {
			return true
		}
	}
	return strings.EqualFold(parts[len(parts)-1], ".mcp.json")
}

// env is how the hook reads its environment: os.Getenv, or a fake in tests.
func (a hookArgs) env() func(string) string {
	if a.getenv != nil {
		return a.getenv
	}
	return os.Getenv
}

// assistantDirs is the assistant_write_dirs setting, the defaults when it lists none.
func (o options) assistantDirs() []string {
	if len(o.AssistantWriteDirs) == 0 {
		return defaultAssistantWriteDirs
	}
	return o.AssistantWriteDirs
}
