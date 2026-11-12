package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func scanFor(t *testing.T, probe string) (*State, *Machine) {
	t.Helper()
	m := &Machine{ID: "local", Host: "localhost", Name: "local", IsLocal: true, Enabled: true, Reachable: true}
	st := &State{Machines: []*Machine{m}, Scans: map[string]*Scan{"local": ParseProbeOutput(m, probe, t0)}}
	return st, m
}

func sessionItems(sc *Scan) []ScanItem {
	var out []ScanItem
	for _, it := range sc.Items {
		if it.Category == CatColdAgentSession {
			out = append(out, it)
		}
	}
	return out
}

func TestProbeWithoutSessionCLIOffersNoArchive(t *testing.T) {
	// The ARMBOX fixture has no disklessly CLI: cold sessions are reported but never archivable.
	st, _ := scanFor(t, sampleProbeARMBOX)
	sc := st.Scans["local"]
	if got := sessionItems(sc); len(got) != 0 {
		t.Fatalf("session items without CLI: %+v", got)
	}
	if sc.Sessions.CLIReady || sc.Sessions.Note == "" || sc.Sessions.ColdCount != 31 {
		t.Fatalf("unexpected summary: %+v", sc.Sessions)
	}
}

func TestProbeUsesCLIPlanAndBackendReachability(t *testing.T) {
	st, _ := scanFor(t, sampleProbeLocal)
	items := sessionItems(st.Scans["local"])
	if len(items) != 2 {
		t.Fatalf("want claude and codex items, got %+v", items)
	}
	for _, it := range items {
		switch it.ID {
		case "sessions-cold-claude":
			// The file only find considered old (not in the CLI plan) is not counted.
			if it.SessionCnt != 2 || it.Bytes != 1500*1024 {
				t.Fatalf("claude item: %+v", it)
			}
		case "sessions-cold-codex":
			if it.SessionCnt != 1 || it.Bytes != 600*1024 {
				t.Fatalf("codex item: %+v", it)
			}
		}
	}
	c := st.Scans["local"].Sessions.Cloud
	if c == nil || !c.Backend.Reachable || c.Backend.Type != "rclone" || c.Counts["archived"] != 12 {
		t.Fatalf("cloud status: %+v", c)
	}

	down := strings.Replace(sampleProbeLocal, `"reachable":true,"latencyMs":42,"error":""`, `"reachable":false,"latencyMs":10000,"error":"timeout"`, 1)
	st, _ = scanFor(t, down)
	if got := sessionItems(st.Scans["local"]); len(got) != 0 {
		t.Fatalf("session items with unreachable backend: %+v", got)
	}
	if !strings.Contains(st.Scans["local"].Sessions.Note, "timeout") {
		t.Fatalf("note: %q", st.Scans["local"].Sessions.Note)
	}
}

func TestSessionArchiveRunsOnceAndAttributesReport(t *testing.T) {
	st, m := scanFor(t, sampleProbeLocal)
	var applyRuns int
	var script string
	runner := func(ctx context.Context, mm *Machine, s string, d time.Duration) (string, error) {
		if strings.Contains(s, "sessions --apply") {
			applyRuns++
			script = s
		}
		return fakeRunner(ctx, mm, s, d)
	}
	_, rec, err := ExecuteClean(context.Background(), runner, st, m, []string{CatColdAgentSession}, nil, false, false, []string{"/home/dev/x y.jsonl", "bad\nline", "0199aaaa-bbbb"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if applyRuns != 1 {
		t.Fatalf("sessions --apply ran %d times", applyRuns)
	}
	if !strings.Contains(script, `'/home/dev/x y.jsonl'`) || !strings.Contains(script, "'0199aaaa-bbbb'") || strings.Contains(script, "bad") {
		t.Fatalf("exclusions not passed safely:\n%s", script)
	}
	if strings.Contains(script, "rm -f \"$f\"") || strings.Contains(script, "tar ") {
		t.Fatalf("session script still has a local delete fallback:\n%s", script)
	}
	if rec.FreedBytes != 1228800 {
		t.Fatalf("freed = %d", rec.FreedBytes)
	}
	for _, r := range rec.Items {
		switch r.ID {
		case "sessions-cold-claude":
			if r.FreedBytes != 1228800 || !strings.Contains(r.Note, "被引用") {
				t.Fatalf("claude result: %+v", r)
			}
		case "sessions-cold-codex":
			if r.FreedBytes != 0 || r.Error != "" {
				t.Fatalf("codex result: %+v", r)
			}
		}
	}
}

func TestSessionArchiveRefusalIsAnError(t *testing.T) {
	for name, out := range map[string]string{
		"backend": "@@SESSIONS_RC 4\n@@SESSIONS_JSON {\"ok\":false,\"error\":{\"code\":4,\"message\":\"backend unreachable: repo.json missing\",\"name\":\"EX_BACKEND\"}}\n@@RC 0\n",
		"missing": "@@SESSIONS_RC 127\n@@RC 0\n",
		"failure": "@@SESSIONS_RC 1\n@@SESSIONS_JSON {\"ok\":false,\"error\":{\"code\":1,\"message\":\"boom\",\"name\":\"EX_FAILURE\"}}\n@@RC 0\n",
		"silent":  "@@RC 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			st, m := scanFor(t, sampleProbeLocal)
			runner := func(ctx context.Context, mm *Machine, s string, d time.Duration) (string, error) {
				if strings.Contains(s, "sessions --apply") {
					return out, nil
				}
				return fakeRunner(ctx, mm, s, d)
			}
			_, rec, err := ExecuteClean(context.Background(), runner, st, m, []string{CatColdAgentSession}, nil, false, false, nil, t0)
			if err == nil || rec != nil || len(st.History) != 0 {
				t.Fatalf("err=%v rec=%+v history=%d", err, rec, len(st.History))
			}
			if name == "backend" && !strings.Contains(err.Error(), "备份后端不可达") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// TestSessionArchiveScriptKeepsFilesWhenCLIRefuses runs the real clean script under /bin/sh with
// a stub disklessly CLI and a temporary HOME, so no real session directory is touched.
func TestSessionArchiveScriptKeepsFilesWhenCLIRefuses(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	home := t.TempDir()
	bin := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "old.jsonl")
	if err := os.WriteFile(old, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".disklessly"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".disklessly", "session-excludes"), []byte("from-file-id\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A pane herdr would resume (synthetic id) reaches the exclusion list through the reference probe.
	if err := os.MkdirAll(filepath.Join(home, ".config", "herdr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "herdr", "session.json"), []byte(`{"agent_resume":["--resume","eeeeeeee-1111-4222-8333-444444444444"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	logf := filepath.Join(home, "cli.log")
	stub := "#!/bin/sh\nprintf 'ARGS %s\\n' \"$*\" >> \"$DKL_LOG\"\nwhile [ $# -gt 0 ]; do if [ \"$1\" = --exclude-from ]; then cat \"$2\" >> \"$DKL_LOG\"; fi; shift; done\n" +
		"echo '{\"ok\":false,\"error\":{\"code\":4,\"message\":\"backend unreachable: no repo\",\"name\":\"EX_BACKEND\"}}'\nexit 4\n"
	if err := os.WriteFile(filepath.Join(bin, "disklessly"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	item := ScanItem{ID: "sessions-cold-claude", Category: CatColdAgentSession, Path: filepath.Join(home, ".claude", "projects")}
	cmd := exec.Command("sh", "-c", BuildItemCleanScript(item, []string{"it's-referenced"}))
	cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "TMPDIR=" + t.TempDir(), "DKL_LOG=" + logf}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	o := parseSessionRun(string(out))
	if o.refsFound < 1 {
		t.Fatalf("reference count missing:\n%s", out)
	}
	if e := o.err(); e == nil || !strings.Contains(e.Error(), "backend unreachable: no repo") {
		t.Fatalf("outcome err = %v\n%s", e, out)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("cold transcript was removed after the CLI refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".disklessly", "session-archives")); !os.IsNotExist(err) {
		t.Fatalf("local tar fallback ran: %v", err)
	}
	log, _ := os.ReadFile(logf)
	for _, want := range []string{"ARGS --json sessions --apply --days 7 --exclude-from", "it's-referenced\n", "from-file-id\n", "eeeeeeee-1111-4222-8333-444444444444\n"} {
		if !strings.Contains(string(log), want) {
			t.Fatalf("cli log missing %q:\n%s", want, log)
		}
	}

	// Without any CLI the script reports it and still touches nothing.
	cmd = exec.Command("sh", "-c", BuildItemCleanScript(item, nil))
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	out, _ = cmd.CombinedOutput()
	if o := parseSessionRun(string(out)); o.exitCode != cliExitMissing {
		t.Fatalf("missing CLI outcome: %+v\n%s", o, out)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("cold transcript removed without CLI: %v", err)
	}
}

// Cold sessions leave the machine, so a generic safe cleanup never includes them, and one
// session item selects them all because a single CLI run covers every agent.
func TestColdSessionsNeedExplicitSelection(t *testing.T) {
	st, m := scanFor(t, sampleProbeLocal)
	sc := st.Scans["local"]
	for name, plan := range map[string]func() (*CleanPlan, error){
		"default":        func() (*CleanPlan, error) { return BuildCleanPlan(m, sc, nil, nil, false) },
		"all":            func() (*CleanPlan, error) { return BuildCleanPlan(m, sc, []string{"all"}, nil, true) },
		"other category": func() (*CleanPlan, error) { return BuildCleanPlan(m, sc, []string{CatGoCache}, nil, false) },
	} {
		p, err := plan()
		if err != nil {
			t.Fatal(err)
		}
		if len(sessionItems(&Scan{Items: p.Items})) != 0 {
			t.Fatalf("%s plan includes cold sessions", name)
		}
	}
	var safe uint64
	for _, it := range sc.Items {
		if it.Safety == SafetySafe && !it.InUse && it.Category != CatColdAgentSession {
			safe += it.Bytes
		}
	}
	if sc.SafeBytes != safe {
		t.Fatalf("safe bytes %d include cold sessions (want %d)", sc.SafeBytes, safe)
	}
	p, err := BuildCleanPlan(m, sc, nil, []string{"sessions-cold-codex"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionItems(&Scan{Items: p.Items}); len(got) != 2 {
		t.Fatalf("one session item should select all: %+v", got)
	}

	// Archives of agents without a scan item are recorded too.
	out := strings.Replace(sampleSessionApply, `"bytes":1228800}]`,
		`"bytes":1228800},{"id":"bbbbbbbbbbbb","path":"/home/dev/.pi/agent/sessions/p/s.jsonl","sessionId":"s","bytes":2048}]`, 1)
	runner := func(ctx context.Context, mm *Machine, s string, d time.Duration) (string, error) {
		if strings.Contains(s, "sessions --apply") {
			return out, nil
		}
		return fakeRunner(ctx, mm, s, d)
	}
	_, rec, err := ExecuteClean(context.Background(), runner, st, m, nil, []string{"sessions-cold-codex"}, false, false, nil, t0)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range rec.Items {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "sessions-cold-claude,sessions-cold-codex,sessions-cold-other" && strings.Join(ids, ",") != "sessions-cold-codex,sessions-cold-claude,sessions-cold-other" {
		t.Fatalf("record items: %v", ids)
	}
	if last := rec.Items[len(rec.Items)-1]; last.FreedBytes != 2048 || !strings.Contains(last.Note, "1 个其他 Agent") {
		t.Fatalf("other agents: %+v", last)
	}
}

func TestColdSessionDaysPassedToCLI(t *testing.T) {
	if cliDaysFlag != "--days "+strconv.Itoa(coldSessionDays) {
		t.Fatalf("cliDaysFlag %q does not match coldSessionDays %d", cliDaysFlag, coldSessionDays)
	}
	if !strings.Contains(ProbeScript, `"$DKL" --json sessions --days 7 `) {
		t.Fatal("probe dry run does not pass --days 7")
	}
	item := ScanItem{ID: "sessions-cold-claude", Category: CatColdAgentSession, Path: "/h/.claude/projects"}
	if s := BuildItemCleanScript(item, nil); !strings.Contains(s, "sessions --apply --days 7 --exclude-from") {
		t.Fatal("archive run does not pass --days 7")
	}
}

func TestResolveCLIScriptLocations(t *testing.T) {
	resolve := func(home string, env ...string) string {
		t.Helper()
		cmd := exec.Command("/bin/sh", "-c", resolveCLIScript+"\nprintf '%s' \"$DKL\"")
		cmd.Env = append([]string{"HOME=" + home, "PATH=/nonexistent"}, env...)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	home := t.TempDir()
	if got := resolve(home); got != "" && !strings.HasPrefix(got, "/usr/local/bin/") && !strings.HasPrefix(got, "/opt/homebrew/") && !strings.HasPrefix(got, "/Applications/") {
		t.Fatalf("empty home resolved %q", got)
	}
	local := filepath.Join(home, ".local", "bin", "disklessly")
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolve(home); got != local {
		t.Fatalf("~/.local/bin: got %q", got)
	}
	explicit := filepath.Join(t.TempDir(), "dkl")
	if err := os.WriteFile(explicit, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolve(home, "DISKLESSLY_BIN="+explicit); got != explicit {
		t.Fatalf("DISKLESSLY_BIN: got %q", got)
	}
	for _, p := range []string{"/usr/local/bin/disklessly", "/opt/homebrew/bin/disklessly"} {
		if !strings.Contains(resolveCLIScript, p) {
			t.Fatalf("lookup misses %s", p)
		}
	}
}

// TestSessionPathsThroughSymlink: a symlinked ~/.claude/projects is matched against the CLI's
// realpath'ed transcript paths for counts, sizes and archived totals.
func TestSessionPathsThroughSymlink(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(t.TempDir(), "claude-projects")
	if err := os.MkdirAll(filepath.Join(real, "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(home, ".claude", "projects")); err != nil {
		t.Fatal(err)
	}
	cold := filepath.Join(real, "p", "s.jsonl")
	if err := os.WriteFile(cold, make([]byte, 8192), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(cold, past, past); err != nil {
		t.Fatal(err)
	}
	realResolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	coldResolved := filepath.Join(realResolved, "p", "s.jsonl")
	bin := t.TempDir()
	stub := "#!/bin/sh\ncase \"$*\" in\n" +
		"*status*) echo '{\"ok\":true,\"backend\":{\"reachable\":true}}' ;;\n" +
		"*) echo '{\"ok\":true,\"cold\":[\"" + coldResolved + "\"]}' ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "disklessly"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	// Run only the session sections of the probe.
	script := "export LC_ALL=C\n" + ProbeScript[strings.Index(ProbeScript, `echo "@@SECTION:SESSIONS"`):]
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe: %v\n%s", err, out)
	}
	m := &Machine{ID: "local", Host: "localhost", Name: "local", IsLocal: true, Enabled: true, Reachable: true}
	sc := ParseProbeOutput(m, string(out), time.Now())
	items := sessionItems(sc)
	if len(items) != 1 || items[0].SessionCnt != 1 || items[0].Bytes == 0 {
		t.Fatalf("cold items through symlink: %+v\n%s", items, out)
	}
	if items[0].Path != filepath.Join(home, ".claude", "projects") || items[0].RealPath != realResolved {
		t.Fatalf("item paths: %q %q", items[0].Path, items[0].RealPath)
	}
	rep := &sessionReport{Archived: []ArchivedSession{{Path: coldResolved, Bytes: 8192}}}
	if n, b := rep.archivedUnder(items[0].Path, items[0].RealPath); n != 1 || b != 8192 {
		t.Fatalf("archivedUnder through symlink: %d %d", n, b)
	}
	if n, _ := rep.archivedOutside([]string{items[0].Path, items[0].RealPath}); n != 0 {
		t.Fatalf("archivedOutside counted the symlinked tool: %d", n)
	}
}
