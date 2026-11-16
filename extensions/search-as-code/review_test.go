package main

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

// Regressions from the review of #577.

// Projects whose names routeKey folds together (acme.web, acme_web, Acme Web, acme-web) get distinct keys, and an
// approved key reads exactly one of them.
func TestProjectKeysNameOneProject(t *testing.T) {
	names := []string{"acme.web", "acme_web", "Acme Web", "acme-web", "acme--web"}
	seen := map[string]string{}
	for _, n := range names {
		k := projectKey(n)
		if prev, ok := seen[k]; ok {
			t.Fatalf("projectKey(%q) = projectKey(%q) = %q", n, prev, k)
		}
		if !projectSegment.MatchString(k) {
			t.Fatalf("projectKey(%q) = %q is not one route segment", n, k)
		}
		seen[k] = n
	}
	if k := projectKey("billing-api"); k != "billing-api" {
		t.Fatalf("a name the key spells out stays as it is: %q", k)
	}

	root := t.TempDir()
	writeSession(t, root, "s-dot", "/work/acme.web", "deploy question", "dot answer", t0)
	writeSession(t, root, "s-under", "/work/acme_web", "deploy question", "underscore answer", t0)
	sessions, stats := loadSessions(&State{SessionDirs: []string{root}})
	for _, want := range []struct{ project, session string }{{"acme.web", "s-dot"}, {"acme_web", "s-under"}} {
		prog, err := CompileProgram(CompileInput{Query: "deploy", Scope: "conversation"})
		if err != nil {
			t.Fatal(err)
		}
		res := executeSessions(&State{}, sessions, stats, pinProgram(prog, "conversation", projectKey(want.project)), t0)
		if len(res.Hits) != 1 || res.Hits[0].SessionID != want.session {
			t.Fatalf("key of %s read %d hits: %+v", want.project, len(res.Hits), res.Hits)
		}
	}
	// A key two projects of a computer answer to (a hash collision, simulated) reads neither.
	if got := projectsForKey([]*SessionRecord{{Project: "x.y"}, {Project: "x_y"}}, projectKey("x.y")); len(got) != 1 || !got["x.y"] {
		t.Fatalf("distinct keys: %v", got)
	}
	collide := []*SessionRecord{{Project: "same"}, {Project: "same"}}
	if got := projectsForKey(collide, "same"); len(got) != 1 {
		t.Fatalf("one project in two sessions: %v", got)
	}
	collide[1].Project = "same" + "\u200b" // a different name; force its key to collide
	if got := projectsForKeyFn(collide, "same", func(string) string { return "same" }); got != nil {
		t.Fatalf("a key two projects answer to must read none: %v", got)
	}
}

// On a route pinned to one project, correlate (and every other operator) sees only that project's sessions.
func TestCorrelationStaysInTheApprovedProject(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "a", "/work/alpha", "loader question", "changed pkg/shared/config.go for the loader", t0)
	writeSession(t, root, "b", "/work/beta", "secret beta plan", "touched pkg/shared/config.go in beta", t0.Add(-time.Hour))
	sessions, stats := loadSessions(&State{SessionDirs: []string{root}})
	prog, err := CompileProgram(CompileInput{Code: `scan_sessions(query="loader", scope="all") |> correlate(by="file") |> limit(n=10)`})
	if err != nil {
		t.Fatal(err)
	}
	res := executeSessions(&State{}, sessions, stats, pinProgram(prog, "all", projectKey("alpha")), t0)
	raw, _ := json.Marshal(res.Hits)
	if len(res.Hits) != 1 || res.Hits[0].SessionID != "a" || strings.Contains(string(raw), "secret beta") || strings.Contains(string(raw), `"session:b"`) {
		t.Fatalf("beta leaked into an alpha-only route: %s", raw)
	}
	// Without a pin, correlation across projects is still there (beta does not match the query: only a reference).
	res = executeSessions(&State{}, sessions, stats, prog, t0)
	raw, _ = json.Marshal(res.Hits)
	if len(res.Hits) != 1 || !strings.Contains(string(raw), "secret beta") {
		t.Fatalf("unpinned correlation lost: %s", raw)
	}
}

// A picked computer's key is never taken by the hub computer's alias, and same-named computers whose ids also
// fold to one key still get distinct keys, each resolving to its own computer.
func TestMachineKeysAreUniqueAndAliasesNeverWin(t *testing.T) {
	tf := newTestFleet(t)
	ctx := context.Background()
	tf.f.host = "studio-host"
	tf.f.lister = &fakeLister{machines: []hubMachine{
		{ID: "studio", Name: "Studio", Online: true, Kind: "local", Hostname: "studio-host"},
		{ID: "remote", Name: "Studio", Online: true, Kind: "link"},
		{ID: "box.1", Name: "Box", Online: true, Kind: "link"},
		{ID: "box_1", Name: "Box", Online: true, Kind: "link"},
	}}
	tf.f.knownAt = time.Time{}
	tf.f.setSelected([]string{"studio", "remote", "box.1", "box_1"})
	targets := tf.f.targets(ctx)

	keys := map[string]string{}
	for _, tg := range targets {
		if prev, ok := keys[tg.Key]; ok {
			t.Fatalf("key %q for both %q and %q", tg.Key, prev, tg.ID)
		}
		keys[tg.Key] = tg.ID
	}
	got, err := resolveTargets(targets, "studio")
	if err != nil || len(got) != 1 || got[0].Local || got[0].ID != "remote" {
		t.Fatalf(`"studio" is the picked computer named Studio, not the hub's alias: %+v %v`, got, err)
	}
	for _, tg := range targets {
		got, err := resolveTargets(targets, tg.Key)
		if err != nil || len(got) != 1 || got[0].ID != tg.ID {
			t.Fatalf("key %q resolved to %+v, want %q (%v)", tg.Key, got, tg.ID, err)
		}
		if tg.ID != "" && !projectSegment.MatchString(tg.Key) {
			t.Fatalf("key %q is not one remembered segment", tg.Key)
		}
	}
}

// A transcript whose only text before the answer is a tool's output does not take its title from that output,
// in the scan or in the synced index.
func TestTitleNeverComesFromToolOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	lines := []string{
		`{"type":"user","sessionId":"s","cwd":"/work/p","timestamp":"2026-11-09T08:00:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"TOP_SECRET_TOKEN=abc"}]}}`,
		`{"type":"assistant","sessionId":"s","cwd":"/work/p","timestamp":"2026-11-09T08:01:00Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := parseSessionFile(path, t0)
	if rec == nil || strings.Contains(rec.Title, "TOP_SECRET") || rec.Title != "done" {
		t.Fatalf("scan title %+v", rec)
	}
	// A record titled from a tool's output (as an older scan made it) is retitled when it is indexed.
	rec.Title = "TOP_SECRET_TOKEN=abc"
	c := compactSession(rec)
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), "TOP_SECRET") || c.Title != "done" {
		t.Fatalf("synced index keeps the tool output: %s", raw)
	}
	// A title from the conversation stays.
	rec.Title = "done"
	if c := compactSession(rec); c.Title != "done" {
		t.Fatalf("title %q", c.Title)
	}
}

// A restore names a computer that must still be picked: from the page action and from the fleet itself.
func TestRestoreRefusesAComputerNoLongerPicked(t *testing.T) {
	a, tf, _ := newAPIApp(t)
	ctx := context.Background()
	tf.f.setSelected([]string{"m-3c4d"}) // the laptop m-1a2b is no longer picked

	res, err := a.runRemoteRestore(ctx, &ext.UIRequest{}, page{}, "m-1a2b", "entry-1")
	if err != nil || res == nil || !strings.Contains(res.Toast, "不在要检索的电脑里") {
		t.Fatalf("page restore: %+v %v", res, err)
	}
	a.restores.Wait()
	if _, err := tf.f.restore(ctx, "m-1a2b", "entry-1"); err == nil {
		t.Fatal("fleet restore of a computer no longer picked")
	}
	for _, c := range tf.nodes.calls {
		if strings.HasPrefix(c, "m-1a2b ") {
			t.Fatalf("the removed computer was called: %v", tf.nodes.calls)
		}
	}
	var running bool
	_ = a.db().read(func(st *State) { running = st.restoreRunning("entry-1") })
	if running {
		t.Fatal("a restore was recorded as started")
	}
}

// A machine filter in the code cannot take a route to another computer: the route's computers are the ones
// searched.
func TestRouteMachineFilterCannotLeaveTheRoute(t *testing.T) {
	_, tf, h := newAPIApp(t)
	code, out := get(t, h, "GET", "/search/hub/all/conversation?"+url.Values{
		"code": {`scan_sessions(query="quota", machine="desktop") |> where(machine="desktop") |> limit(n=10)`},
	}.Encode())
	if code != 200 {
		t.Fatalf("search %d %v", code, out)
	}
	for _, c := range tf.nodes.calls {
		if strings.HasSuffix(c, " sac.search") {
			t.Fatalf("a hub-only route called a computer: %v", tf.nodes.calls)
		}
	}
	raw, _ := json.Marshal(out)
	if hits := out["hits"].([]any); len(hits) == 0 || strings.Contains(string(raw), "sess-desk") {
		t.Fatalf("want the hub's sessions only: %s", raw)
	}
	// scan_files (the hub's repository directories) is not part of a route.
	prog, err := CompileProgram(CompileInput{Code: `scan_sessions(query="x") |> scan_files(pattern="x")`})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range pinProgram(prog, "conversation", "").Steps {
		if s.Op == "scan_files" || s.Args["machine"] != "" {
			t.Fatalf("pinned program keeps %+v", s)
		}
	}
}

// The start sync runs once; after it, each interval syncs once.
func TestSyncLoopSyncsOncePerInterval(t *testing.T) {
	tf := newTestFleet(t)
	first, every, stop := make(chan time.Time), make(chan time.Time), make(chan struct{})
	done := make(chan struct{})
	go func() {
		tf.f.runSyncLoop(stop, first, every)
		close(done)
	}()
	first <- t0
	every <- t0
	every <- t0
	select {
	case first <- t0:
		t.Fatal("the start sync ran again")
	case <-time.After(200 * time.Millisecond):
	}
	tf.nodes.mu.Lock()
	n := 0
	for _, c := range tf.nodes.calls {
		if strings.HasSuffix(c, " sac.index") {
			n++
		}
	}
	tf.nodes.mu.Unlock()
	close(stop)
	<-done
	// Three syncs (start, two ticks) of two online computers, one page each.
	if n != 6 {
		t.Fatalf("sac.index calls %d, want 6: %v", n, tf.nodes.calls)
	}
}

// Every computer runs the program with the merged result's limit, and a program without one gets the cap, so a
// cross-computer search keeps no more than a single computer's.
func TestEveryComputerRunsTheMergedLimit(t *testing.T) {
	tf := newTestFleet(t)
	ctx := context.Background()
	st := &State{SessionDirs: []string{tf.hubDir}}
	prog, err := CompileProgram(CompileInput{Code: `scan_sessions(query="quota") |> limit(n=1)`})
	if err != nil {
		t.Fatal(err)
	}
	res := tf.f.search(ctx, st, prog, t0, tf.f.targets(ctx))
	if len(res.Hits) != 1 {
		t.Fatalf("merged %d hits", len(res.Hits))
	}
	for _, m := range res.Machines {
		if m.Hits > 1 {
			t.Fatalf("%s answered %d hits over the limit", m.Name, m.Hits)
		}
	}
	noLimit := SearchProgram{Steps: []StepSpec{{Op: "scan_sessions", Args: map[string]string{"query": "quota"}}}}
	if got := withLimit(noLimit, programLimit(noLimit)); got.Steps[len(got.Steps)-1].Op != "limit" || got.Steps[len(got.Steps)-1].Args["n"] != "100" {
		t.Fatalf("no limit step: %+v", got.Steps)
	}
	big := SearchProgram{Steps: []StepSpec{{Op: "limit", Args: map[string]string{"n": "5000"}}}}
	if n := programLimit(big); n != maxLimitCap {
		t.Fatalf("limit %d", n)
	}
}
