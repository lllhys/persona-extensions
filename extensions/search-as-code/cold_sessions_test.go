package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// writeColdIndex writes a synthetic disklessly text index <textDir>/<entryID>.jsonl.
func writeColdIndex(t *testing.T, textDir, entryID string, header map[string]any, lines ...map[string]any) {
	t.Helper()
	if err := os.MkdirAll(textDir, 0o700); err != nil {
		t.Fatal(err)
	}
	h := map[string]any{"type": "disklessly-archive", "version": 1, "entryId": entryID, "deviceId": "dev-test",
		"tool": "claude", "mtime": "2026-11-01T08:00:00Z", "archivedAt": "2026-11-05T08:00:00Z", "bytes": 1234,
		"segment": "sessions/segments/seg-1.tar"}
	for k, v := range header {
		h[k] = v
	}
	var b strings.Builder
	for _, obj := range append([]map[string]any{h}, lines...) {
		raw, err := json.Marshal(obj)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(textDir, entryID+".jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func msg(role, text string) map[string]any {
	return map[string]any{"type": role, "timestamp": "2026-11-01T07:00:00Z", "text": text}
}

// seedColdFixtures writes live fixtures plus a text index with: one archived session, one restored session
// (original path exists again, outside the session dirs), one whose original path is a live transcript, and one
// sharing a live session id.
func seedColdFixtures(t *testing.T) (st *State, stateDir, missingPath string) {
	t.Helper()
	sessionDir, repoDir := seedFixtures(t)
	root := t.TempDir()
	stateDir = filepath.Join(root, "state")
	textDir := filepath.Join(stateDir, "sessions", "text")
	missingPath = filepath.Join(root, "gone", ".claude", "projects", "-workspace-orbit", "sess-cold-01.jsonl")

	writeColdIndex(t, textDir, "entry-cold-01", map[string]any{
		"sessionId": "sess-cold-01", "path": missingPath, "cwd": "/workspace/orbit", "title": "Orbit planner",
	},
		msg("user", "How should the orbit planner cache ZEPHYRQUILL lookups in pkg/orbit/cache.go, please?"),
		map[string]any{"type": "omitted", "bytes": 4096},
		msg("assistant", "Cache ZEPHYRQUILL results per epoch and invalidate them on reload."),
	)

	restored := filepath.Join(root, "restored", "sess-back-02.jsonl")
	if err := os.MkdirAll(filepath.Dir(restored), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(restored, []byte(`{"type":"user","sessionId":"sess-back-02","message":{"role":"user","content":"RESTOREDMARK is back"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeColdIndex(t, textDir, "entry-back-02", map[string]any{"sessionId": "sess-back-02", "path": restored},
		msg("user", "ZEPHYRQUILL appears in a restored session too"))

	livePath := filepath.Join(sessionDir, ".claude", "projects", "-workspace-persona-hub", "sess-claude-01.jsonl")
	writeColdIndex(t, textDir, "entry-live-03", map[string]any{"sessionId": "sess-claude-01", "path": livePath},
		msg("assistant", "globalCooldown ZEPHYRQUILL"))

	writeColdIndex(t, textDir, "entry-dup-04", map[string]any{
		"sessionId": "sess-codex-02", "tool": "codex", "path": filepath.Join(root, "gone", "codex.jsonl"),
	}, msg("user", "globalCooldown ZEPHYRQUILL duplicate"))

	return &State{DefaultScope: "conversation", SessionDirs: []string{sessionDir}, RepoDirs: []string{repoDir},
		ColdTextDirs: []string{textDir}}, stateDir, missingPath
}

func TestArchivedSessionsAreSearchable(t *testing.T) {
	st, stateDir, missingPath := seedColdFixtures(t)

	sessions, stats := loadSessions(st)
	if stats.ArchivedSessions != 1 || stats.TotalSessions != 4 || len(sessions) != 4 {
		t.Fatalf("want 2 live + 1 restored + 1 archived, got stats %+v", stats)
	}

	prog, err := CompileProgram(CompileInput{Query: "zephyrquill", Scope: "conversation"})
	if err != nil {
		t.Fatal(err)
	}
	res := ExecuteProgram(st, prog, t0)
	if len(res.Hits) != 1 {
		t.Fatalf("want exactly the archived hit, got %+v", res.Hits)
	}
	h := res.Hits[0]
	if h.Location != locationArchived || h.EntryID != "entry-cold-01" || h.OriginalPath != missingPath ||
		h.SessionID != "sess-cold-01" || h.Project != "orbit" || h.Harness != "claude-code" {
		t.Fatalf("archived hit fields: %+v", h)
	}
	if res.ScannedArchived != 1 || !strings.Contains(res.StepTraces[0].Note, locationArchived) {
		t.Fatalf("trace must count archived sessions: %d %q", res.ScannedArchived, res.StepTraces[0].Note)
	}
	if len(h.Snippets) == 0 || h.Snippets[0].Role != "User" {
		t.Fatalf("snippets: %+v", h.Snippets)
	}

	rec := findArchivedSession(st, "entry-cold-01")
	if rec == nil || rec.StateDir != stateDir || !slices.Contains(rec.Files, "pkg/orbit/cache.go") {
		t.Fatalf("archived record: %+v", rec)
	}
	if findArchivedSession(st, "entry-back-02") != nil {
		t.Fatal("a restored session must not be listed as archived")
	}

	// A restored transcript outside the session dirs is scanned as local, not as archived.
	prog3, _ := CompileProgram(CompileInput{Query: "restoredmark", Scope: "conversation"})
	if res3 := ExecuteProgram(st, prog3, t0); len(res3.Hits) != 1 || res3.Hits[0].Location != locationLocal ||
		res3.Hits[0].SessionID != "sess-back-02" {
		t.Fatalf("restored session: %+v", res3.Hits)
	}

	// A live session found by a word the text index also holds is reported once, as local.
	prog2, _ := CompileProgram(CompileInput{Query: "globalCooldown", Scope: "conversation"})
	res2 := ExecuteProgram(st, prog2, t0)
	if len(res2.Hits) != 1 || res2.Hits[0].Location != locationLocal || res2.Hits[0].EntryID != "" {
		t.Fatalf("live session must not be double counted: %+v", res2.Hits)
	}
}

// fakeDisklessly installs a fake CLI through DISKLESSLY_BIN that records its arguments.
func fakeDisklessly(t *testing.T, mode string) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	script := filepath.Join(dir, "disklessly")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$FAKE_ARGS\"\n" +
		"case \"$FAKE_MODE\" in\n" +
		"ok) printf '{\"ok\":true,\"path\":\"/restored/path.jsonl\",\"id\":\"entry-cold-01\",\"sessionId\":\"sess-cold-01\"}\\n' ;;\n" +
		"down) printf '{\"ok\":false,\"error\":{\"code\":\"unreachable\",\"message\":\"backend unreachable\",\"name\":\"BackendUnreachable\"}}\\n'; exit 4 ;;\n" +
		"*) printf '{\"ok\":false,\"error\":{\"code\":\"notFound\",\"message\":\"no such entry\",\"name\":\"NotFound\"}}\\n'; exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DISKLESSLY_BIN", script)
	t.Setenv("FAKE_ARGS", argsFile)
	t.Setenv("FAKE_MODE", mode)
	t.Setenv("DISKLESSLY_STATE_DIR", "")
	t.Setenv("HOME", t.TempDir())
	return argsFile
}

func TestRestoreArchivedRunsDisklessly(t *testing.T) {
	st, stateDir, _ := seedColdFixtures(t)
	ctx := context.Background()
	lockDir := t.TempDir()

	argsFile := fakeDisklessly(t, "ok")
	out, err := restoreArchived(ctx, st, "entry-cold-01", lockDir)
	if err != nil {
		t.Fatal(err)
	}
	if out.Path != "/restored/path.jsonl" || out.Resume != "claude --resume sess-cold-01" {
		t.Fatalf("restore result: %+v", out)
	}
	args, _ := os.ReadFile(argsFile)
	if want := "--json --state-dir " + stateDir + " sessions restore entry-cold-01"; strings.TrimSpace(string(args)) != want {
		t.Fatalf("args %q, want %q", args, want)
	}

	// Not in this computer's text index (restored already): the CLI is not run.
	_ = os.Remove(argsFile)
	if _, err := restoreArchived(ctx, st, "entry-back-02", lockDir); err == nil {
		t.Fatal("restoring a restored session must fail")
	}
	if _, err := restoreArchived(ctx, st, "--help", lockDir); err == nil {
		t.Fatal("an entry id that looks like a flag must be refused")
	}
	if _, err := os.Stat(argsFile); err == nil {
		t.Fatal("the CLI must not run for unknown entries")
	}

	t.Setenv("FAKE_MODE", "down")
	if _, err := restoreArchived(ctx, st, "entry-cold-01", lockDir); err == nil || err.Error() != "云端不可达，稍后再试" {
		t.Fatalf("exit 4: %v", err)
	}
	t.Setenv("FAKE_MODE", "missing")
	if _, err := restoreArchived(ctx, st, "entry-cold-01", lockDir); err == nil || !strings.Contains(err.Error(), "no such entry") {
		t.Fatalf("error JSON: %v", err)
	}
}

func TestArchivedHitsInUIAndMCP(t *testing.T) {
	st, _, missingPath := seedColdFixtures(t)
	a := newApp(t.TempDir())
	a.now = func() time.Time { return t0 }
	_ = a.db().update(func(s *State, _ time.Time) error {
		s.SessionDirs, s.RepoDirs, s.ColdTextDirs = st.SessionDirs, st.RepoDirs, st.ColdTextDirs
		return nil
	})
	ctx := context.Background()

	v, err := a.render(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(page{Query: "zephyrquill"})}})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(v); err != nil {
		t.Fatalf("CheckView: %v", err)
	}
	var row *wire.Component
	for _, sec := range v.Sections {
		for i := range sec.Items {
			if sec.Items[i].Key == "hit-row-0" {
				row = &sec.Items[i]
			}
		}
	}
	if row == nil || row.Badge == nil || row.Badge.Text != locationArchived || len(row.Actions) != 1 ||
		row.Actions[0].Title != "恢复" || !strings.Contains(row.Actions[0].Action.Confirm, missingPath) ||
		!strings.Contains(row.Actions[0].Action.Confirm, "claude --resume sess-cold-01") {
		t.Fatalf("archived row: %+v", row)
	}
	for _, p := range []page{{Mode: modeHit, HitID: "session:sess-cold-01", Query: "zephyrquill"}, {Tab: "sources"}} {
		dv, err := a.render(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(p)}})
		if err != nil {
			t.Fatal(err)
		}
		if err := wire.CheckView(dv); err != nil {
			t.Fatalf("CheckView %+v: %v", p, err)
		}
	}

	fakeDisklessly(t, "ok")
	restore := func() *wire.ActionResult {
		t.Helper()
		r, err := a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{
			State: ext.EncodeState(page{Query: "zephyrquill"}), Action: "restore-session",
			Values: map[string]string{"entry": "entry-cold-01"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	// The page action starts the restore in the background and returns at once; the outcome is stored.
	notice := func() RestoreNotice {
		t.Helper()
		a.restores.Wait()
		var n RestoreNotice
		_ = a.db().read(func(s *State) { n = s.Restores[0] })
		return n
	}
	if r := restore(); !strings.Contains(r.Toast, "正在从云端恢复") || r.View == nil {
		t.Fatalf("restore toast: %q", r.Toast)
	}
	if n := notice(); !n.OK || n.Running || n.Resume != "claude --resume sess-cold-01" || n.Path != "/restored/path.jsonl" {
		t.Fatalf("restore notice: %+v", n)
	}
	t.Setenv("FAKE_MODE", "down")
	restore()
	if n := notice(); n.OK || n.Message != "云端不可达，稍后再试" {
		t.Fatalf("exit 4 notice: %+v", n)
	}
	v2, err := a.render(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(page{Query: "zephyrquill"})}})
	if err != nil || wire.CheckView(v2) != nil || v2.Sections[0].Title != "会话恢复" {
		t.Fatalf("restore section: %v %+v", err, v2.Sections[0])
	}
	// A restore in flight is not started again, and its row offers no 恢复.
	_ = a.db().update(func(s *State, now time.Time) error {
		s.setRestore(RestoreNotice{EntryID: "entry-cold-01", Running: true, StartedAt: now})
		return nil
	})
	if r := restore(); r.Toast != errRestoreBusy.Error() {
		t.Fatalf("second restore: %q", r.Toast)
	}
	v3, _ := a.render(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(page{Query: "zephyrquill"})}})
	for _, sec := range v3.Sections {
		for _, it := range sec.Items {
			if it.Key == "hit-row-0" && (len(it.Actions) != 0 || it.Badge.Text != "恢复中") {
				t.Fatalf("row while restoring: %+v", it)
			}
		}
	}
	if _, err := a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(page{}), Action: "clear-restores"}}); err != nil {
		t.Fatal(err)
	}
	_ = a.db().update(func(s *State, _ time.Time) error { s.Restores = nil; return nil })

	// MCP: archived hits carry location and entry id; the restore tool is a write tool outside the allow list.
	m := &mcpServer{store: a.db()}
	out, err := m.call("sac_session_search", map[string]any{"query": "zephyrquill"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), `"location":"已上云"`) || !strings.Contains(string(raw), `"entry_id":"entry-cold-01"`) {
		t.Fatalf("MCP hit: %s", raw)
	}
	t.Setenv("FAKE_MODE", "ok")
	if res, err := m.call("sac_session_restore", map[string]any{"entry_id": "entry-cold-01"}); err != nil ||
		res.(*RestoreResult).Resume != "claude --resume sess-cold-01" {
		t.Fatalf("sac_session_restore: %+v %v", res, err)
	}
	for _, tl := range tools {
		if tl.name == "sac_session_restore" && tl.readOnly {
			t.Fatal("sac_session_restore must not be read-only (and so not in allow)")
		}
	}
	if strings.Contains(string(manifest), `"sac_session_restore"`) {
		t.Fatal("sac_session_restore must not be in the manifest allow list")
	}
}

func TestUnconfirmedIndexIsNotOfferedForRestore(t *testing.T) {
	root := t.TempDir()
	textDir := filepath.Join(root, "state", "sessions", "text")
	writeColdIndex(t, textDir, "entry-pending-01", map[string]any{
		"sessionId": "sess-pending-01", "path": filepath.Join(root, "gone", "pending.jsonl"), "segment": "", "archivedAt": "",
	}, msg("user", "QUASARVELLUM only in a pending index"))
	st := &State{DefaultScope: "conversation", SessionDirs: []string{filepath.Join(root, "none")}, ColdTextDirs: []string{textDir}}

	prog, _ := CompileProgram(CompileInput{Query: "quasarvellum", Scope: "conversation"})
	res := ExecuteProgram(st, prog, t0)
	if len(res.Hits) != 1 || res.Hits[0].Location != locationUnconfirmed || res.ScannedUnconfirmed != 1 ||
		!strings.Contains(res.StepTraces[0].Note, locationUnconfirmed) {
		t.Fatalf("pending index: %+v %q", res.Hits, res.StepTraces[0].Note)
	}

	a := newApp(t.TempDir())
	a.now = func() time.Time { return t0 }
	_ = a.db().update(func(s *State, _ time.Time) error { *s = *st; return nil })
	ctx := context.Background()
	for _, p := range []page{{Query: "quasarvellum"}, {Mode: modeHit, HitID: "session:sess-pending-01", Query: "quasarvellum"}} {
		v, err := a.render(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(p)}})
		if err != nil {
			t.Fatal(err)
		}
		if err := wire.CheckView(v); err != nil {
			t.Fatalf("CheckView %+v: %v", p, err)
		}
		for _, sec := range v.Sections {
			for _, it := range sec.Items {
				if len(it.Actions) > 0 || it.Key == "restore-session" {
					t.Fatalf("a pending index must not offer 恢复: %+v", it)
				}
				if it.Key == "hit-row-0" && it.Badge.Text != locationUnconfirmed {
					t.Fatalf("pending badge: %+v", it.Badge)
				}
			}
		}
	}
}

func TestColdWindowAndLargeIndexes(t *testing.T) {
	root := t.TempDir()
	textDir := filepath.Join(root, "state", "sessions", "text")
	old := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	// The oldest archived index: beyond the window for search, still restorable by id.
	writeColdIndex(t, textDir, "entry-old-01", map[string]any{"sessionId": "sess-old-01", "path": filepath.Join(root, "gone", "old.jsonl")},
		msg("user", "NEBULAFORGE in an old session"))
	_ = os.Chtimes(filepath.Join(textDir, "entry-old-01.jsonl"), old, old)
	// A newer archived index larger than any per-file cap a reader might use: parsed up to the cap, not dropped.
	big := []map[string]any{msg("user", "LARGEINDEXMARK at the start")}
	for range 400 {
		big = append(big, msg("assistant", strings.Repeat("filler text line ", 20)))
	}
	writeColdIndex(t, textDir, "entry-big-02", map[string]any{"sessionId": "sess-big-02", "path": filepath.Join(root, "gone", "big.jsonl")}, big...)
	defer func(n int64) { maxColdReadBytes = n }(maxColdReadBytes)
	maxColdReadBytes = 64 << 10
	if fi, _ := os.Stat(filepath.Join(textDir, "entry-big-02.jsonl")); fi.Size() <= maxColdReadBytes {
		t.Fatalf("fixture too small: %d", fi.Size())
	}
	_ = os.Chtimes(filepath.Join(textDir, "entry-big-02.jsonl"), old.Add(time.Hour), old.Add(time.Hour))
	// Restored indexes, newest of all, do not use up the window.
	for i := range 3 {
		orig := filepath.Join(root, "back", fmt.Sprintf("back-%d.jsonl", i))
		_ = os.MkdirAll(filepath.Dir(orig), 0o755)
		_ = os.WriteFile(orig, []byte(`{"type":"user","sessionId":"sess-back","message":{"role":"user","content":"x"}}`+"\n"), 0o600)
		writeColdIndex(t, textDir, fmt.Sprintf("entry-back-%d", i), map[string]any{"sessionId": fmt.Sprintf("sess-back-%d", i), "path": orig}, msg("user", "x"))
	}
	st := &State{SessionDirs: []string{filepath.Join(root, "none")}, ColdTextDirs: []string{textDir}}

	defer func(n int) { maxScannedColdFiles = n }(maxScannedColdFiles)
	maxScannedColdFiles = 1
	archived, restored, _, omitted := loadColdSessions(st)
	if len(archived) != 1 || archived[0].EntryID != "entry-big-02" || len(restored) != 3 || omitted != 1 {
		t.Fatalf("window: archived %d restored %d omitted %d", len(archived), len(restored), omitted)
	}
	if !strings.Contains(archived[0].Turns[0].Text, "LARGEINDEXMARK") || len(archived[0].Turns) >= 401 {
		t.Fatal("large index must be searchable")
	}
	if rec := findArchivedSession(st, "entry-old-01"); rec == nil || rec.SessionID != "sess-old-01" {
		t.Fatalf("an index beyond the window must still be restorable: %+v", rec)
	}
	_, stats := loadSessions(st)
	if stats.ArchivedOmitted != 1 || !strings.Contains(coldNote(stats.ArchivedSessions, 0, stats.ArchivedOmitted), "超出上限") {
		t.Fatalf("omitted stat: %+v", stats)
	}
}

func TestResumeCommandPerTool(t *testing.T) {
	for harness, want := range map[string]string{"claude-code": "claude --resume s1", "codex": "codex resume s1", "gemini": "", "pi": ""} {
		if got := resumeCommand(harness, "s1"); got != want {
			t.Errorf("%s: %q, want %q", harness, got, want)
		}
	}
	if q := restoreConfirm("/p.jsonl", "gemini", "s1"); strings.Contains(q, "--resume") {
		t.Errorf("confirm for gemini: %q", q)
	}
}

func TestRestoreTimeoutKillsWrapperAndLockRefusesTwice(t *testing.T) {
	st, _, _ := seedColdFixtures(t)
	fakeDisklessly(t, "ok")
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "disklessly")
	// A wrapper that does not exec: its child keeps the output pipe open.
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nsleep 30\necho '{}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DISKLESSLY_BIN", wrapper)
	defer func(d, g time.Duration) { restoreTimeout, restoreKillGrace = d, g }(restoreTimeout, restoreKillGrace)
	restoreTimeout, restoreKillGrace = 300*time.Millisecond, 300*time.Millisecond

	lockDir := t.TempDir()
	start := time.Now()
	_, err := restoreArchived(context.Background(), st, "entry-cold-01", lockDir)
	if err == nil || !strings.Contains(err.Error(), "恢复超时") || time.Since(start) > 5*time.Second {
		t.Fatalf("timeout: %v after %s", err, time.Since(start))
	}

	unlock, err := lockRestore(lockDir, "entry-cold-01")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restoreArchived(context.Background(), st, "entry-cold-01", lockDir); err != errRestoreBusy {
		t.Fatalf("second restore while locked: %v", err)
	}
	unlock()
}

func TestFindDisklesslyAppBundle(t *testing.T) {
	t.Setenv("DISKLESSLY_BIN", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	bin := filepath.Join(t.TempDir(), "disklessly")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func(p string) { disklesslyAppCLI = p }(disklesslyAppCLI)
	disklesslyAppCLI = bin
	if got, err := findDisklessly(); err != nil || got != bin {
		t.Fatalf("app bundle CLI: %q %v", got, err)
	}
}

// TestColdTextDirCandidatesPasswdHome: the CLI's default state dir on Linux follows the passwd home, so its
// text index is found even when $HOME points elsewhere; duplicates are dropped.
func TestColdTextDirCandidatesPasswdHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	envHome, pwHome := t.TempDir(), t.TempDir()
	got := coldTextDirCandidates("", []string{envHome, pwHome, envHome})
	for _, home := range []string{envHome, pwHome} {
		for _, want := range []string{
			filepath.Join(home, ".local", "share", "disklessly", "sessions", "text"),
			filepath.Join(home, "Library", "Application Support", "Disklessly", "sessions", "text"),
		} {
			if !slices.Contains(got, want) {
				t.Fatalf("candidates miss %s: %v", want, got)
			}
		}
	}
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c] {
			t.Fatalf("duplicate candidate %s", c)
		}
		seen[c] = true
	}
	pwText := filepath.Join(pwHome, ".local", "share", "disklessly", "sessions", "text")
	if err := os.MkdirAll(pwText, 0o755); err != nil {
		t.Fatal(err)
	}
	if dirs := existingDirs(got); !slices.Equal(dirs, []string{pwText}) {
		t.Fatalf("existing dirs: %v", dirs)
	}

	// homeDirs adds the passwd home to $HOME, and --state-dir is left out only when both homes agree.
	old := passwdHome
	t.Cleanup(func() { passwdHome = old })
	t.Setenv("HOME", envHome)
	t.Setenv("DISKLESSLY_STATE_DIR", "")
	passwdHome = func() string { return pwHome }
	if h := homeDirs(); !slices.Equal(h, []string{envHome, pwHome}) {
		t.Fatalf("homeDirs: %v", h)
	}
	if isDisklesslyDefaultStateDir(disklesslyStateDirFor(envHome)) {
		t.Fatal("$HOME state dir treated as default while the passwd home differs")
	}
	passwdHome = func() string { return envHome }
	if !isDisklesslyDefaultStateDir(disklesslyStateDirFor(envHome)) {
		t.Fatal("default state dir not recognized when homes agree")
	}
}
