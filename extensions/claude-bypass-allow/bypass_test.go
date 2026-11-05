package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
}

func TestConformance(t *testing.T) {
	app := &hubApp{call: fakeNodes(nil)}
	exttest.Check(t, manifest, exttest.Serve(t, app.program("claude-bypass-allow", version(t))), json.RawMessage(`{"machines":["m1"]}`))
}

func version(t *testing.T) string {
	var m struct{ Version string }
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	return m.Version
}

// env is a computer: a config directory, the node program's data directory with a fresh heartbeat, a project.
type env struct{ config, data, project string }

func newEnv(t *testing.T) env {
	t.Helper()
	root := t.TempDir()
	e := env{config: filepath.Join(root, "claude"), data: filepath.Join(root, "data"), project: filepath.Join(root, "work", "proj")}
	for _, d := range []string{e.config, e.data, e.project} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(e.data, heartbeatFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e env) hook(t *testing.T, req map[string]any, now time.Time) string {
	t.Helper()
	b, _ := json.Marshal(req)
	var out bytes.Buffer
	runHook(hookArgs{ConfigDir: e.config, Data: e.data}, bytes.NewReader(b), &out, now)
	return out.String()
}

func request(e env, mode, tool, cmd string) map[string]any {
	return map[string]any{"hook_event_name": "PermissionRequest", "session_id": "93bed2c0-0000-4000-8000-000000000000",
		"cwd": e.project, "permission_mode": mode, "tool_name": tool,
		"tool_input": map[string]any{"command": cmd, "description": "remove the views"}, "permission_suggestions": []any{}}
}

func writeJSON(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestHookAllowsOnlyBashInBypass(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	cmd := "cd view && rm -f sub/*; ls"
	if got := e.hook(t, request(e, "bypassPermissions", "Bash", cmd), now); strings.TrimSpace(got) != allowOutput {
		t.Fatalf("bypass + Bash: %q", got)
	}
	for _, c := range []struct{ name, mode, tool string }{
		{"default mode", "default", "Bash"},
		{"accept edits", "acceptEdits", "Bash"},
		{"auto mode", "auto", "Bash"},
		{"plan mode", "plan", "Bash"},
		{"no mode", "", "Bash"},
		{"another tool", "bypassPermissions", "Write"},
		{"an MCP tool", "bypassPermissions", "mcp__x__run"},
	} {
		if got := e.hook(t, request(e, c.mode, c.tool, cmd), now); got != "" {
			t.Errorf("%s: answered %q, should stay silent", c.name, got)
		}
	}
	if got := e.hook(t, request(e, "bypassPermissions", "Bash", "  "), now); got != "" {
		t.Errorf("an empty command: %q", got)
	}
	var out bytes.Buffer
	runHook(hookArgs{ConfigDir: e.config, Data: e.data}, strings.NewReader("not json"), &out, now)
	if out.Len() != 0 {
		t.Errorf("bad input: %q", out.String())
	}
	if n := len(readAudit(e.data, 100)); n != 1 {
		t.Errorf("audit has %d entries, want the one allow", n)
	}
}

func TestHookNeedsTheNodeProgram(t *testing.T) {
	e := newEnv(t)
	req := request(e, "bypassPermissions", "Bash", "rm -rf \"$t\"")
	if got := e.hook(t, req, time.Now().Add(heartbeatFresh+time.Second)); got != "" {
		t.Errorf("stale heartbeat: %q", got)
	}
	if err := os.Remove(filepath.Join(e.data, heartbeatFile)); err != nil {
		t.Fatal(err)
	}
	if got := e.hook(t, req, time.Now()); got != "" {
		t.Errorf("no heartbeat: %q", got)
	}
}

func TestHookLeavesTheUsersRules(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	writeJSON(t, filepath.Join(e.config, "settings.json"), `{"permissions":{"ask":["Bash(git push:*)","Read(./secrets/**)"],"deny":["Bash(curl *)"]}}`)
	writeJSON(t, filepath.Join(filepath.Dir(e.project), ".claude", "settings.example.json"), `{"permissions":{"ask":["Bash(touch:*)"]}}`)
	for _, cmd := range []string{"git push origin HEAD", "cd x && git push", "touch y.tmp", "X=1 curl https://example.com"} {
		if got := e.hook(t, request(e, "bypassPermissions", "Bash", cmd), now); got != "" {
			t.Errorf("%q matches a rule of the user's, but the hook answered %q", cmd, got)
		}
	}
	if got := e.hook(t, request(e, "bypassPermissions", "Bash", "cd view && rm -f sub/*"), now); got == "" {
		t.Error("a command no rule is about: should be allowed")
	}

	all := newEnv(t)
	writeJSON(t, filepath.Join(all.project, ".claude", "settings.json"), `{"permissions":{"ask":["Bash"]}}`)
	if got := all.hook(t, request(all, "bypassPermissions", "Bash", "rm -f a/*"), now); got != "" {
		t.Errorf("ask: [Bash] covers every command: %q", got)
	}
}

func TestRuleLiteral(t *testing.T) {
	for _, c := range []struct {
		rule, lit string
		all       bool
	}{
		{"Bash", "", true},
		{"Bash()", "", true},
		{"Bash(*)", "", true},
		{"Bash(:*)", "", true},
		{"Bash(git push:*)", "git push", false},
		{"Bash(git push *)", "git push", false},
		{"Bash(npm run test)", "npm run test", false},
		{"Bash(rm -rf /tmp/*/cache)", "rm -rf /tmp/", false},
	} {
		lit, all := ruleLiteral(c.rule)
		if lit != c.lit || all != c.all {
			t.Errorf("%s: %q %v, want %q %v", c.rule, lit, all, c.lit, c.all)
		}
	}
	if isBashRule("BashOutput") || isBashRule("Read(./x)") || !isBashRule(" Bash(ls) ") {
		t.Error("isBashRule")
	}
}

// Claude Code 2.1.318 asks for each "asks" command here under ask: [Bash(touch:*), Bash(rm -rf:*)], measured in a
// bypass-mode session; the git push ones are the same shapes under the other rule.
func TestMatchingRule(t *testing.T) {
	rules := []string{"Bash(git push:*)", "Bash(rm -rf:*)", "Bash(touch:*)"}
	for _, c := range []struct{ cmd, rule string }{
		{"git push origin HEAD", "Bash(git push:*)"},
		{"git  push --force", "Bash(git push:*)"},
		{"git\tpush", "Bash(git push:*)"},
		{"'git' push", "Bash(git push:*)"},
		{"git 'push' origin main", "Bash(git push:*)"},
		{"git \\\n  push", "Bash(git push:*)"},
		{"g\\it push", "Bash(git push:*)"},
		{`"rm" -rf /x`, "Bash(rm -rf:*)"},
		{"rm  -rf /x", "Bash(rm -rf:*)"},
		{"cd x &&\n  rm \t-rf y", "Bash(rm -rf:*)"},
		{"touch\ta3.tmp", "Bash(touch:*)"},
		{"touch \\\n  a4.tmp", "Bash(touch:*)"},
		// Flags in another order or split: Claude Code does not ask for these either.
		{"rm -fr d", ""},
		{"rm -r -f d", ""},
		{"git pull", ""},
		{"cd view && rm -f sub/*", ""},
		{"echo 'git' && push", ""},
	} {
		if got := matchingRule(rules, c.cmd); got != c.rule {
			t.Errorf("matchingRule(%q) = %q, want %q", c.cmd, got, c.rule)
		}
	}
}

func TestRedact(t *testing.T) {
	for _, c := range []struct{ in, gone string }{
		{"GITHUB_TOKEN=ghp_TEST00000000daf86f70 gh pr list", "ghp_TEST00000000daf86f70"},
		{`curl -H "Authorization: Bearer sk-live-abcdefghijk" https://api.example.com`, "sk-live-abcdefghijk"},
		{"mysql --password=hunter22 -u me", "hunter22"},
		{"git clone https://me:s3cretpass@example.com/r.git", "s3cretpass"},
		{"echo 0123456789abcdef0123456789abcdef0123", "0123456789abcdef0123456789abcdef0123"},
		{"export API_KEY='a b c'; run", "a b c"},
		{"mysql -h db -u root -pS3cretpw app", "S3cretpw"},
		{"mysqldump -u root -p'S3cret pw' app > a.sql", "S3cret pw"},
		{"sshpass -p hunter22 ssh me@example.com", "hunter22"},
		{"sshpass -phunter22 ssh me@example.com", "hunter22"},
		{"curl -u me:s3cretpass https://example.com", "s3cretpass"},
		{"curl -sS --user 'me:s3cretpass' https://example.com", "s3cretpass"},
		{"curl -fsSL -ume:s3cretpass https://example.com", "s3cretpass"},
	} {
		got := redact(c.in)
		if strings.Contains(got, c.gone) {
			t.Errorf("redact(%q) = %q, still has %q", c.in, got, c.gone)
		}
	}
	for _, ordinary := range []string{"cd view && rm -f sub/*; ls", "mkdir -p build && cp -pr a b", "mysql -p app", "docker run -u 1000:1000 img", "curl -u me https://example.com"} {
		if got := redact(ordinary); got != ordinary {
			t.Errorf("an ordinary command changed: %q", got)
		}
	}
	long := redact(strings.Repeat("echo hi; ", 100))
	if n := len([]rune(long)); n != commandShown+1 || !strings.HasSuffix(long, "…") {
		t.Errorf("a long command is cut to %d runes and an ellipsis: %d %q", commandShown, n, long[len(long)-10:])
	}
	if got := redact("python3 - <<EOF\nprint(1)\nEOF"); strings.Contains(got, "\n") {
		t.Errorf("one line in the log: %q", got)
	}
}

func TestAuditEntry(t *testing.T) {
	e := newEnv(t)
	req := request(e, "bypassPermissions", "Bash", "TOKEN=abcdefgh12345678 rm -rf \"$t\"")
	req["agent_id"] = "a1b2"
	e.hook(t, req, time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC))
	got := readAudit(e.data, 10)
	if len(got) != 1 {
		t.Fatalf("%d entries", len(got))
	}
	a := got[0]
	if a.At != "2026-11-05T10:00:00Z" || a.Agent != "a1b2" || a.Cwd != e.project || a.ConfigDir != e.config || strings.Contains(a.Command, "abcdefgh12345678") || a.Length != 34 {
		t.Errorf("entry: %+v", a)
	}
	raw, _ := os.ReadFile(filepath.Join(e.data, auditFile))
	if strings.Contains(string(raw), "abcdefgh12345678") {
		t.Error("the secret reached the file")
	}
	if st, _ := os.Stat(filepath.Join(e.data, auditFile)); st.Mode().Perm() != 0o600 {
		t.Errorf("audit file mode %v", st.Mode().Perm())
	}
}

func TestAuditRotates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, auditFile)
	big := auditEntry{At: "2026-11-05T10:00:00Z", Session: "s", Command: strings.Repeat("x", 1000)}
	for range auditKeep/1000 + 10 {
		appendAudit(p, big)
	}
	appendAudit(p, auditEntry{At: "2026-11-05T11:00:00Z", Session: "s", Command: "newest"})
	if _, err := os.Stat(p + ".1"); err != nil {
		t.Fatal("no rotated file")
	}
	got := readAudit(dir, 3)
	if len(got) != 3 || got[0].Command != "newest" {
		t.Fatalf("newest first across both files: %+v", got[0])
	}
}

const userSettings = `{
    "model": "opus",
    "hooks": {
        "PermissionRequest": [
            {"matcher": "*", "hooks": [{"type": "command", "command": "other-tool hook"}]}
        ],
        "Stop": [{"hooks": [{"type": "command", "command": "notify && done"}]}]
    },
    "env": {"A": "1"}
}
`

func TestInstallKeepsEverythingElse(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	writeJSON(t, p, userSettings)
	cmd := hookCommand("/opt/x/bin/claude-bypass-allow", dir, "/data/it's")
	if changed, err := installHook(p, cmd); err != nil || !changed {
		t.Fatalf("install: %v %v", changed, err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if strings.Index(s, `"model"`) > strings.Index(s, `"hooks"`) || strings.Index(s, `"hooks"`) > strings.Index(s, `"env"`) {
		t.Errorf("order of the keys changed:\n%s", s)
	}
	if !strings.Contains(s, "other-tool hook") || !strings.Contains(s, "notify && done") || !strings.Contains(s, "\n    \"model\"") {
		t.Errorf("other hooks or the indent changed:\n%s", s)
	}
	if !hasHook(p) {
		t.Fatal("no entry after install")
	}
	if changed, err := installHook(p, cmd); err != nil || changed {
		t.Errorf("a second install should not write: %v %v", changed, err)
	}
	// An upgrade: the program moved, the entry follows.
	if changed, _ := installHook(p, hookCommand("/opt/y/bin/claude-bypass-allow", dir, "/data")); !changed {
		t.Error("a new command should replace the old entry")
	}
	b, _ = os.ReadFile(p)
	if strings.Count(string(b), tagFlag) != 1 || strings.Contains(string(b), "/opt/x/") {
		t.Errorf("one entry, the new one:\n%s", b)
	}
	if _, err := os.Stat(p + ".claude-bypass-allow.bak"); err != nil {
		t.Error("no backup")
	}
	if changed, err := uninstallHook(p); err != nil || !changed {
		t.Fatalf("uninstall: %v %v", changed, err)
	}
	var before, after any
	b, _ = os.ReadFile(p)
	_ = json.Unmarshal([]byte(userSettings), &before)
	_ = json.Unmarshal(b, &after)
	if !jsonEqual(before, after) {
		t.Errorf("after uninstall the settings differ from the user's:\n%s", b)
	}
	if changed, _ := uninstallHook(p); changed {
		t.Error("a second uninstall should not write")
	}
}

func TestInstallIntoNothing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	if changed, _ := uninstallHook(p); changed {
		t.Error("uninstall created a file")
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("uninstall created a file")
	}
	if _, err := installHook(p, hookCommand("/b", dir, "/d")); err != nil {
		t.Fatal(err)
	}
	if _, err := uninstallHook(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if strings.TrimSpace(string(b)) != "{}" {
		t.Errorf("hooks and PermissionRequest should go when empty: %s", b)
	}
	writeJSON(t, p, "[1, 2]")
	if _, err := installHook(p, "x "+tagFlag); err == nil {
		t.Error("a file that is not an object should be left alone")
	}
}

func TestUninstallKeepsTheUsersHookInOurGroup(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.json")
	cmd := hookCommand("/b", dir, "/d")
	writeJSON(t, p, `{"hooks":{"PermissionRequest":[{"matcher":"Bash","hooks":[{"type":"command","command":"`+strings.ReplaceAll(cmd, `'`, `'`)+`"},{"type":"command","command":"mine"}]}]}}`)
	if _, err := uninstallHook(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"mine"`) || strings.Contains(string(b), tagFlag) {
		t.Errorf("%s", b)
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func TestNodeInstallsAndCleansUp(t *testing.T) {
	e := newEnv(t)
	t.Setenv("HOME", filepath.Dir(e.config))
	writeJSON(t, filepath.Join(e.config, "settings.json"), userSettings)
	n := &node{bin: "/opt/bin/claude-bypass-allow"}
	in := &ext.Init{}
	in.Extension = &wire.ExtensionInfo{ID: "claude-bypass-allow", DataDir: e.data}
	in.InitializeParams.Config = json.RawMessage(`{"machines":["m1"],"config_dirs":["~/claude","~/missing","relative/dir"]}`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := n.init(ctx, in, nil); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(e.config, "settings.json")
	if !hasHook(p) {
		t.Fatal("no entry after init")
	}
	if !alive(filepath.Join(e.data, heartbeatFile), time.Now()) {
		t.Fatal("no heartbeat")
	}
	h := n.program("claude-bypass-allow", "0.1.0").Namespaces["bypass"]
	raw, err := h(ctx, "status", nil)
	if err != nil {
		t.Fatal(err)
	}
	st := raw.(status)
	if len(st.Dirs) != 3 || !st.Dirs[0].Installed || st.Dirs[1].Error == "" || st.Dirs[2].Error == "" {
		t.Errorf("dirs: %+v", st.Dirs)
	}
	if _, err := h(ctx, "nope", nil); err == nil {
		t.Error("an unknown method")
	}
	n.cleanup()
	if hasHook(p) {
		t.Error("entry left after cleanup")
	}
	if _, err := os.Stat(filepath.Join(e.data, heartbeatFile)); err == nil {
		t.Error("heartbeat left after cleanup")
	}
}

func fakeNodes(by map[string]any) nodeCaller {
	return func(_ context.Context, machine string, out any) error {
		v, ok := by[machine]
		if !ok {
			return errors.New("machine " + machine + " is offline")
		}
		b, _ := json.Marshal(v)
		return json.Unmarshal(b, out)
	}
}

func TestPage(t *testing.T) {
	app := &hubApp{machines: []string{"m1", "m2"}, call: fakeNodes(map[string]any{
		"m1": status{Version: "0.1.0", Dirs: []dirState{{Dir: "~/.claude", Settings: "/home/me/.claude/settings.json", Installed: true}},
			Recent: []auditEntry{{At: "2026-11-05T10:00:00Z", Session: "93bed2c0-0000", Agent: "a1", Cwd: "/w/proj", Command: "cd view && rm -f sub/*"}}},
	})}
	for _, locale := range []string{"zh-Hans", "en", "ja"} {
		r := &ext.UIRequest{}
		r.Contribution, r.Size, r.UIVersion, r.Locale, r.TZ = logUI, wire.SizeFull, 3, locale, "Asia/Shanghai"
		v, err := app.render(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(v)
		if err := exttest.CheckView(raw, false); err != nil {
			t.Fatalf("%s: %v", locale, err)
		}
		if len(v.Sections) != 3 || v.Sections[0].Title != "m1" || v.Sections[1].Items[0].Badge == nil {
			t.Fatalf("%s: %s", locale, raw)
		}
		if !strings.Contains(string(raw), "18:00:00") || !strings.Contains(string(raw), "cd view") {
			t.Errorf("%s: the entry, in the user's time zone: %s", locale, raw)
		}
	}
	empty := &hubApp{call: fakeNodes(nil)}
	r := &ext.UIRequest{}
	r.Contribution, r.Size, r.UIVersion = logUI, wire.SizeFull, 3
	v, _ := empty.render(context.Background(), r)
	if len(v.Sections) != 1 || v.Sections[0].Items[0].Type != wire.ComponentEmpty {
		t.Errorf("no machine: %+v", v)
	}
}

// An allow tells Persona's node (hook.decided) with the input Claude Code gave the hook; a refusal tells it nothing.
func TestHookTellsTheNode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".persona"), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(home, ".persona", "node.sock"))
	if err != nil {
		t.Skip("no unix socket here:", err)
	}
	defer ln.Close()
	got := make(chan []byte, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadBytes('\n')
			_, _ = c.Write([]byte("{\"ok\":true}\n"))
			c.Close()
			got <- line
		}
	}()
	e := newEnv(t)
	e.hook(t, request(e, "default", "Bash", "rm -f a/*"), time.Now())
	req := request(e, "bypassPermissions", "Bash", "cd view && rm -f sub/*")
	if out := e.hook(t, req, time.Now()); out == "" {
		t.Fatal("not allowed")
	}
	select {
	case line := <-got:
		var m struct {
			Kind  string          `json:"kind"`
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(line, &m); err != nil || m.Kind != "hook.decided" {
			t.Fatalf("%s", line)
		}
		want, _ := json.Marshal(req)
		if !bytes.Equal(m.Input, want) {
			t.Fatalf("the input as Claude Code gave it:\n%s\n%s", m.Input, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the node heard nothing")
	}
	select {
	case line := <-got:
		t.Fatalf("a refusal told the node: %s", line)
	default:
	}
}

func TestHookWithoutANode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	e := newEnv(t)
	start := time.Now()
	if out := e.hook(t, request(e, "bypassPermissions", "Bash", "rm -f a/*"), time.Now()); out == "" || time.Since(start) > nodeWait {
		t.Fatalf("no node: %q after %v", out, time.Since(start))
	}
}
