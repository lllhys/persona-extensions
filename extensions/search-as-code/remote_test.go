package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// fakeNodes runs each computer's program in process, through the same JSON the hub's nodes.call carries.
type fakeNodes struct {
	mu      sync.Mutex
	workers map[string]*nodeWorker
	down    map[string]bool
	calls   []string
}

func (f *fakeNodes) Call(ctx context.Context, machine, method string, params, out any) error {
	f.mu.Lock()
	f.calls = append(f.calls, machine+" "+method)
	w, down := f.workers[machine], f.down[machine]
	f.mu.Unlock()
	if w == nil || down {
		return errors.New("machine offline")
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ns, m, _ := strings.Cut(method, ".")
	if ns != "sac" {
		return ext.UnknownMethod(method)
	}
	res, err := w.rpc(ctx, m, raw)
	if err != nil {
		return err
	}
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (f *fakeNodes) setDown(machine string, down bool) {
	f.mu.Lock()
	f.down[machine] = down
	f.mu.Unlock()
}

type fakeLister struct{ machines []hubMachine }

func (l *fakeLister) Machines(context.Context) ([]hubMachine, error) { return l.machines, nil }

// writeSession writes one synthetic Claude Code transcript.
func writeSession(t *testing.T, root, id, cwd, user, assistant string, mod time.Time) {
	t.Helper()
	dir := filepath.Join(root, ".claude", "projects", strings.ReplaceAll(cwd, "/", "-"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-11-09T08:00:00Z","message":{"role":"user","content":%q}}`, id, cwd, user),
		fmt.Sprintf(`{"type":"assistant","sessionId":%q,"cwd":%q,"timestamp":"2026-11-09T08:01:00Z","message":{"role":"assistant","model":"claude-opus-5-5","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}},{"type":"text","text":%q}]}}`, id, cwd, assistant),
		fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":"2026-11-09T08:02:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"TOOL_NOISE_SECRET_LOG token=abc"}]}}`, id),
		fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":"2026-11-09T08:03:00Z","message":{"role":"user","content":[{"type":"tool_result","is_error":true,"content":"Error: TOOL_ERROR_SECRET_DUMP failed"}]}}`, id),
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

type testFleet struct {
	f      *fleet
	nodes  *fakeNodes
	hubDir string // the hub computer's sessions
}

// newTestFleet: the hub's computer, an online laptop (machine id m-1a2b, name "Laptop 2"), a desktop that goes
// offline, and a picked computer the hub does not know at all.
func newTestFleet(t *testing.T) testFleet {
	t.Helper()
	hubDir, laptopDir, desktopDir := t.TempDir(), t.TempDir(), t.TempDir()
	writeSession(t, hubDir, "sess-hub-1", "/work/hubproj", "hub quota question", "hub answer about quota", t0.Add(-3*time.Hour))
	writeSession(t, laptopDir, "sess-lap-1", "/work/persona-extensions", "laptop quota cooldown bug", "fixed quota cooldown in pkg/gateway/quota.go", t0.Add(-1*time.Hour))
	writeSession(t, laptopDir, "sess-lap-2", "/work/other3", "laptop unrelated", "something about quota too", t0.Add(-2*time.Hour))
	writeSession(t, desktopDir, "sess-desk-1", "/work/persona-extensions", "desktop quota planning", "desktop plan for quota windows", t0.Add(-30*time.Minute))
	nodes := &fakeNodes{
		workers: map[string]*nodeWorker{
			"m-1a2b": newNodeWorker(t.TempDir(), configSpec{SessionDirs: laptopDir}),
			"m-3c4d": newNodeWorker(t.TempDir(), configSpec{SessionDirs: desktopDir}),
		},
		down: map[string]bool{},
	}
	for _, w := range nodes.workers {
		w.now = func() time.Time { return t0 }
	}
	lister := &fakeLister{machines: []hubMachine{
		{ID: "m-1a2b", Name: "Laptop 2", Online: true},
		{ID: "m-3c4d", Name: "desktop", Online: true},
	}}
	f := newFleet(nodes, lister, t.TempDir(), []string{"m-1a2b", "m-3c4d", "m-gone"})
	f.now = func() time.Time { return t0 }
	return testFleet{f: f, nodes: nodes, hubDir: hubDir}
}

func TestRouteKeyStaysOneRememberedSegment(t *testing.T) {
	// The gate turns a segment with a digit or longer than 24 characters into "*" (larkPattern): a key never is one.
	gateWildcard := func(seg string) bool { return strings.ContainsAny(seg, "0123456789") || len(seg) > 24 }
	cases := map[string]string{
		"persona-extensions": "persona-extensions",
		"Laptop 2":           "laptop-zc",
		"web3":               "webzd",
		"zsh":                "zzsh",
		"m-1a2b":             "m-zbazcb",
	}
	for in, want := range cases {
		if got := routeKey(in); got != want {
			t.Errorf("routeKey(%q) = %q, want %q", in, got, want)
		}
	}
	seen := map[string]string{}
	for _, in := range []string{"web3", "webzd", "app-测试", "app-部署", "测试", "部署", "all", "hub", "a-very-long-project-name-that-goes-on", "a-very-long-project-name-that-goes-on-2", "x1", "xb"} {
		k := routeKey(in)
		if gateWildcard(k) || k == allKey || k == hubMachineKey || !projectSegment.MatchString(k) {
			t.Errorf("routeKey(%q) = %q is not one remembered segment", in, k)
		}
		if prev, ok := seen[k]; ok {
			t.Errorf("routeKey(%q) = routeKey(%q) = %q", in, prev, k)
		}
		seen[k] = in
	}
}

func TestFanOutMergesComputersAndFallsBackToSyncedIndex(t *testing.T) {
	tf := newTestFleet(t)
	ctx := context.Background()
	st := &State{SessionDirs: []string{tf.hubDir}}

	// The desktop is synced while online, then goes offline.
	targets := tf.f.targets(ctx)
	if keys := func() (k []string) {
		for _, t := range targets {
			k = append(k, t.Key)
		}
		return
	}(); !slices.Equal(keys, []string{"hub", "laptop-zc", "desktop", "m-gone"}) {
		t.Fatalf("keys %v", keys)
	}
	tf.f.syncAll(ctx)
	if c := tf.f.loadCache("m-3c4d"); c == nil || len(c.Sessions) != 1 {
		t.Fatalf("desktop index not synced: %+v", c)
	}
	tf.nodes.setDown("m-3c4d", true)

	prog, err := CompileProgram(CompileInput{Query: "quota group:machine", Scope: "conversation"})
	if err != nil {
		t.Fatal(err)
	}
	res := tf.f.search(ctx, st, prog, t0, tf.f.targets(ctx))
	byMachine := map[string]*SearchHit{}
	for _, h := range res.Hits {
		byMachine[h.SessionID] = h
	}
	if len(res.Hits) != 4 {
		t.Fatalf("want hits from hub, laptop (2) and cached desktop, got %d: %+v", len(res.Hits), res.Machines)
	}
	if h := byMachine["sess-lap-1"]; h.Machine != "m-1a2b" || h.MachineName != "Laptop 2" || h.Cached || h.ID != "m:m-1a2b/session:sess-lap-1" {
		t.Fatalf("laptop hit %+v", h)
	}
	if h := byMachine["sess-desk-1"]; h.Machine != "m-3c4d" || !h.Cached {
		t.Fatalf("offline desktop must come from its synced index: %+v", h)
	}
	if h := byMachine["sess-hub-1"]; h.Machine != "" || h.ID != "session:sess-hub-1" {
		t.Fatalf("hub hit %+v", h)
	}
	status := map[string]string{}
	for _, m := range res.Machines {
		status[m.Key] = m.Status
	}
	if status["hub"] != machineLocal || status["laptop-zc"] != machineOnline || status["desktop"] != machineCached || status["m-gone"] != machineUnavailable {
		t.Fatalf("statuses %v", status)
	}
	if res.StepTraces[0].Op != "fan_out" || res.StepTraces[len(res.StepTraces)-1].Op != "merge" {
		t.Fatalf("traces %+v", res.StepTraces)
	}
	if res.Groups[0].Field != "machine" || len(res.Groups) != 3 {
		t.Fatalf("group_by machine: %+v", res.Groups)
	}
	// Tool output never leaves a computer in the conversation scope, live or synced.
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "TOOL_NOISE_SECRET_LOG") {
		t.Fatal("tool output in a conversation-scope result")
	}
	for _, s := range tf.f.loadCache("m-3c4d").Sessions {
		for _, tr := range s.Turns {
			if tr.IsTool || strings.Contains(tr.Text, "TOOL_NOISE") {
				t.Fatalf("tool turn in the synced index: %+v", tr)
			}
		}
	}

	// machine: in the query, where(machine=) and scan_sessions(machine=) reach that computer only.
	prog, _ = CompileProgram(CompileInput{Query: "quota machine:laptop", Scope: "conversation"})
	tf.nodes.calls = nil
	res = tf.f.search(ctx, st, prog, t0, filterTargets(tf.f.targets(ctx), programMachine(prog)))
	if len(res.Hits) != 2 || res.Hits[0].Machine != "m-1a2b" || len(res.Machines) != 1 {
		t.Fatalf("machine:laptop %+v", res.Machines)
	}
	if !slices.Equal(tf.nodes.calls, []string{"m-1a2b sac.search"}) {
		t.Fatalf("calls %v", tf.nodes.calls)
	}
	prog, _ = CompileProgram(CompileInput{Code: `scan_sessions(query="quota") |> where(machine="hub") |> limit(n=5)`})
	res = tf.f.search(ctx, st, prog, t0, tf.f.targets(ctx))
	if len(res.Hits) != 1 || res.Hits[0].SessionID != "sess-hub-1" {
		t.Fatalf("where(machine=hub) %+v", res.Hits)
	}
}

func TestIndexPagesKeepEverySession(t *testing.T) {
	dir := t.TempDir()
	same := t0.Add(-time.Hour)
	for i := range 7 {
		writeSession(t, dir, fmt.Sprintf("sess-%02d", i), "/work/p", "q", "a", same)
	}
	w := newNodeWorker(t.TempDir(), configSpec{SessionDirs: dir})
	var got []string
	cursor := indexCursor{}
	for range 10 {
		page := w.index(indexParams{After: cursor, Limit: 3})
		for _, s := range page.Sessions {
			got = append(got, s.SessionID)
		}
		cursor = page.Cursor
		if !page.More {
			break
		}
	}
	if len(got) != 7 {
		t.Fatalf("sessions updated at the same moment lost across pages: %v", got)
	}
	if page := w.index(indexParams{After: cursor}); len(page.Sessions) != 0 {
		t.Fatalf("nothing changed, still %d", len(page.Sessions))
	}
}

func TestCompactSessionKeepsHeadAndTail(t *testing.T) {
	s := &SessionRecord{SessionID: "s", TextPath: "/x/text.jsonl", StateDir: "/x"}
	for i := range 200 {
		s.Turns = append(s.Turns, SessionTurn{Line: i, Role: "user", Text: strings.Repeat("长", 1000)})
		s.Turns = append(s.Turns, SessionTurn{Line: i, Role: "tool", IsTool: true, Text: "noise"})
	}
	c := compactSession(s)
	if len(c.Turns) != maxCachedTurns || c.Turns[0].Line != 0 || c.Turns[len(c.Turns)-1].Line != 199 {
		t.Fatalf("turns %d first %d last %d", len(c.Turns), c.Turns[0].Line, c.Turns[len(c.Turns)-1].Line)
	}
	if n := len([]rune(c.Turns[0].Text)); n > maxCachedTurnRunes+1 || c.TextPath != "" || c.StateDir != "" {
		t.Fatalf("not compact: %d runes, %q %q", n, c.TextPath, c.StateDir)
	}
}

func newAPIApp(t *testing.T) (*app, testFleet, http.Handler) {
	t.Helper()
	tf := newTestFleet(t)
	a := newApp(t.TempDir())
	a.now = func() time.Time { return t0 }
	a.fleet = tf.f
	a.hubURL, a.token = "http://127.0.0.1:1", "test-token"
	_ = a.db().update(func(st *State, _ time.Time) error {
		st.SessionDirs = []string{tf.hubDir}
		return nil
	})
	return a, tf, a.handler()
}

func get(t *testing.T, h http.Handler, method, path string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestAPIRoutesNameWhatTheyRead(t *testing.T) {
	_, tf, h := newAPIApp(t)

	code, out := get(t, h, "GET", "/machines")
	if code != 200 || len(out["machines"].([]any)) != 4 {
		t.Fatalf("/machines %d %v", code, out)
	}
	code, out = get(t, h, "GET", "/projects/laptop-zc")
	if code != 200 || !strings.Contains(fmt.Sprint(out), "persona-extensions") || !strings.Contains(fmt.Sprint(out), "otherzd") {
		t.Fatalf("/projects %d %v", code, out)
	}

	// One computer, one project, one scope: nothing else is read, whatever the code says.
	code, out = get(t, h, "GET", "/search/laptop-zc/persona-extensions/conversation?"+url.Values{
		"code": {`scan_sessions(query="quota", scope="all", machine="desktop") |> limit(n=20)`},
	}.Encode())
	if code != 200 {
		t.Fatalf("search %d %v", code, out)
	}
	// The code's machine filter is dropped: the route's computer is searched, and only it.
	hits := out["hits"].([]any)
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "TOOL_NOISE") || strings.Contains(string(raw), "sess-desk") || len(out["machines"].([]any)) != 1 {
		t.Fatalf("machine=desktop in code must not leave the laptop route: %s", raw)
	}
	for _, h := range hits {
		if h.(map[string]any)["machine"] != "m-1a2b" {
			t.Fatalf("a hit from outside the laptop route: %s", raw)
		}
	}
	code, out = get(t, h, "GET", "/search/laptop-zc/persona-extensions/conversation?q="+url.QueryEscape("quota scope:all project:other3"))
	raw, _ = json.Marshal(out)
	if code != 200 || strings.Contains(string(raw), "sess-lap-2") || strings.Contains(string(raw), "TOOL_NOISE") {
		t.Fatalf("inline tokens widened the route: %s", raw)
	}
	// An inline scope:errors would bring tool error output into a conversation route.
	code, out = get(t, h, "GET", "/search/laptop-zc/persona-extensions/conversation?q="+url.QueryEscape("TOOL_ERROR_SECRET_DUMP scope:errors"))
	raw, _ = json.Marshal(out)
	if code != 200 || strings.Contains(string(raw), "TOOL_ERROR_SECRET_DUMP failed") || len(out["hits"].([]any)) != 0 {
		t.Fatalf("inline scope:errors widened the route: %s", raw)
	}
	code, out = get(t, h, "GET", "/search/laptop-zc/persona-extensions/errors?q=TOOL_ERROR_SECRET_DUMP")
	if code != 200 || len(out["hits"].([]any)) != 1 {
		t.Fatalf("the errors route itself finds it: %v", out)
	}
	code, out = get(t, h, "GET", "/search/laptop-zc/persona-extensions/conversation?q=quota")
	hits = out["hits"].([]any)
	if code != 200 || len(hits) != 1 || hits[0].(map[string]any)["session_id"] != "sess-lap-1" || hits[0].(map[string]any)["machine_name"] != "Laptop 2" {
		t.Fatalf("route search %d %v", code, out)
	}
	if !slices.Equal(tf.nodes.calls[len(tf.nodes.calls)-1:], []string{"m-1a2b sac.search"}) {
		t.Fatalf("calls %v", tf.nodes.calls)
	}

	for path, want := range map[string]int{
		"/search/nobody/all/conversation":    404,
		"/search/all/Bad1/conversation":      400,
		"/search/all/all/everything":         400,
		"/search/all/all/conversation?q=xyz": 200,
		"/search/hub/all/errors":             200,
	} {
		if code, out := get(t, h, "GET", path); code != want {
			t.Errorf("%s: %d %v, want %d", path, code, out, want)
		}
	}
}

func TestRestoreGoesToTheComputerThatHoldsTheSession(t *testing.T) {
	a, tf, h := newAPIApp(t)
	// No disklessly index on the laptop: the laptop's program answers, not the hub's.
	code, out := get(t, h, "POST", "/restore/laptop-zc/entry-01")
	if code != 409 || !strings.Contains(fmt.Sprint(out), "entry-01") {
		t.Fatalf("restore %d %v", code, out)
	}
	if last := tf.nodes.calls[len(tf.nodes.calls)-1]; last != "m-1a2b sac.restore" {
		t.Fatalf("restore went to %q", last)
	}
	if code, _ := get(t, h, "POST", "/restore/all/entry-01"); code != 404 {
		t.Fatalf("restore on all computers: %d", code)
	}
	// The page's restore of a remote hit goes there too.
	res, err := a.action(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{
		State: ext.EncodeState(page{}), Action: "restore-session", Values: map[string]string{"entry": "entry-01", "machine": "m-1a2b"},
	}})
	if err != nil || res.View == nil {
		t.Fatalf("page restore: %v", err)
	}
	a.restores.Wait()
	var n []RestoreNotice
	_ = a.db().read(func(st *State) { n = st.Restores })
	if len(n) != 1 || n[0].Machine != "m-1a2b" || n[0].OK || n[0].Running {
		t.Fatalf("notice %+v", n)
	}
}

func TestGateAsksBeforeReadingHistory(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("assistant", "gate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var gate struct {
		Command, Script, BaseEnv string
		Secret                   []string
		Env                      []string
		Rules                    []struct{ Method, Path, Action string }
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if err := dec.Decode(&gate); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join("skills", gate.Script)); err != nil {
		t.Fatalf("gate script: %v", err)
	}
	// The first rule that matches decides (the gate's order); no rule allows reading history.
	decide := func(method, path string) string {
		for _, r := range gate.Rules {
			if (r.Method == "*" || r.Method == method) && gatePathMatch(r.Path, path) {
				return r.Action
			}
		}
		return "confirm"
	}
	for call, want := range map[string]string{
		"GET /machines":           "allow",
		"GET /projects/laptop-zc": "allow",
		"GET /recipes":            "allow",
		"GET /search/laptop-zc/persona-extensions/all": "confirm",
		"GET /search/hub/all/conversation":             "confirm",
		"POST /restore/laptop-zc/entry-01":             "confirm",
		"POST /search/hub/all/conversation":            "deny",
		"GET /search/hub/all":                          "deny",
		"DELETE /recipes":                              "deny",
		"GET /search/hub/all/conversation/extra":       "deny",
	} {
		m, p, _ := strings.Cut(call, " ")
		if got := decide(m, p); got != want {
			t.Errorf("%s: %s, want %s", call, got, want)
		}
	}

	// The credentials name exactly the gate's variables.
	a, _, _ := newAPIApp(t)
	out, err := a.assistant(context.Background(), "credentials", nil)
	if err != nil {
		t.Fatal(err)
	}
	env := out.(map[string]any)["env"].(map[string]string)
	if env["SAC_BASE"] != "http://127.0.0.1:1/api/v1/extensions/search-as-code" || env["SAC_TOKEN"] != "test-token" || len(env) != len(gate.Env) {
		t.Fatalf("credentials %v", env)
	}
}

// gatePathMatch is the gate's path pattern: * one segment, ** the rest.
func gatePathMatch(pattern, path string) bool {
	ps, xs := strings.Split(strings.Trim(pattern, "/"), "/"), strings.Split(strings.Trim(path, "/"), "/")
	for i, p := range ps {
		if p == "**" {
			return true
		}
		if i >= len(xs) || (p != "*" && p != xs[i]) {
			return false
		}
	}
	return len(ps) == len(xs)
}

func TestApprovalCardSpellsOutTheTarget(t *testing.T) {
	a, tf, _ := newAPIApp(t)
	tf.f.syncAll(context.Background())
	tf.nodes.setDown("m-3c4d", true)
	tf.f.knownAt = time.Time{}
	tf.f.lister.(*fakeLister).machines[1].Online = false

	card := "以你的身份在代码化搜索上读取 Agent 会话历史（其他电脑或其他项目）\nGET /search/desktop/persona-extensions/conversation?q=quota%20cooldown&since=7d"
	out, err := a.assistant(context.Background(), "summary", json.RawMessage(mustJSON(map[string]string{"summary": card})))
	if err != nil {
		t.Fatal(err)
	}
	got := out.(map[string]string)["summary"]
	for _, want := range []string{card, "电脑：desktop（离线，查已同步的索引）", "项目：项目 persona-extensions", "范围：纯对话", "查询：quota cooldown", "时间：7d"} {
		if !strings.Contains(got, want) {
			t.Errorf("card lacks %q:\n%s", want, got)
		}
	}
	out, _ = a.assistant(context.Background(), "summary", json.RawMessage(mustJSON(map[string]string{"summary": "x\nGET /search/all/all/errors"})))
	if got := out.(map[string]string)["summary"]; !strings.Contains(got, "本机（hub）、Laptop 2、desktop（离线，查已同步的索引）、m-gone（离线，查已同步的索引）") || !strings.Contains(got, "全部项目") {
		t.Errorf("all computers card:\n%s", got)
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestPageShowsComputers(t *testing.T) {
	a, tf, _ := newAPIApp(t)
	ctx := context.Background()
	tf.f.syncAll(ctx)
	res, err := a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(page{}), Action: "search", Values: map[string]string{"q": "quota"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(res.View); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(res.View)
	if !strings.Contains(string(raw), "Laptop 2") || !strings.Contains(string(raw), "检索电脑") {
		t.Fatalf("results without computers: %s", raw)
	}
	for _, p := range []page{{Tab: "sources"}, {Tab: "code", Query: "quota"}, {Mode: modeHit, HitID: "m:m-3c4d/session:sess-desk-1", Query: "quota"}} {
		v, err := a.render(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(p)}})
		if err != nil {
			t.Fatal(err)
		}
		if err := wire.CheckView(v); err != nil {
			t.Fatalf("%+v: %v", p, err)
		}
		raw, _ := json.Marshal(v)
		switch {
		case p.Tab == "sources" && !strings.Contains(string(raw), "machine:laptop-zc"):
			t.Fatalf("sources: %s", raw)
		case p.Tab == "code" && !strings.Contains(string(raw), "fan_out"):
			t.Fatalf("code: %s", raw)
		case p.Mode == modeHit && !strings.Contains(string(raw), "desktop plan for quota windows"):
			t.Fatalf("remote hit detail from the synced index: %s", raw)
		}
	}
	if _, err := a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(page{Tab: "sources"}), Action: "sync-machines"}}); err != nil {
		t.Fatal(err)
	}
}

// Without other computers picked, a search is the single-computer one it always was.
func TestNoComputersPickedIsUnchanged(t *testing.T) {
	sessionDir, _ := seedFixtures(t)
	a := newApp(t.TempDir())
	a.fleet = newFleet(&fakeNodes{}, &fakeLister{}, t.TempDir(), nil)
	st := &State{SessionDirs: []string{sessionDir}}
	prog, _ := CompileProgram(CompileInput{Query: "quota"})
	got := a.execute(context.Background(), st, prog, t0)
	want := ExecuteProgram(st, prog, t0)
	if len(got.Machines) != 0 || len(got.Hits) != len(want.Hits) || len(got.StepTraces) != len(want.StepTraces) {
		t.Fatalf("got %d hits %d traces %v", len(got.Hits), len(got.StepTraces), got.Machines)
	}
}

// The hub's own computer among the picked ones (the hub lists it as kind local, with the hub's host name) is
// searched once, directly, and answers to its id and name as well as to hub.
func TestHubComputerPickedIsSearchedOnce(t *testing.T) {
	tf := newTestFleet(t)
	ctx := context.Background()
	tf.f.host = "studio-host"
	tf.f.lister = &fakeLister{machines: []hubMachine{
		{ID: "studio", Name: "Studio", Online: true, Kind: "local", Hostname: "studio-host"},
		{ID: "m-1a2b", Name: "Laptop 2", Online: true, Kind: "link", Hostname: "laptop-host"},
		// another manager's local computer: not the hub's
		{ID: "far", Name: "Far", Online: true, Kind: "local", Hostname: "far-host"},
	}}
	tf.f.knownAt = time.Time{}
	tf.f.setSelected([]string{"studio", "m-1a2b", "far"})
	tf.nodes.workers["studio"] = newNodeWorker(t.TempDir(), configSpec{SessionDirs: tf.hubDir})
	st := &State{SessionDirs: []string{tf.hubDir}}

	targets := tf.f.targets(ctx)
	var keys []string
	for _, t := range targets {
		keys = append(keys, t.Key)
	}
	if !slices.Equal(keys, []string{"hub", "laptop-zc", "far"}) || targets[0].AliasID != "studio" || targets[0].Name != "Studio（hub）" {
		t.Fatalf("targets %+v", targets)
	}
	prog, err := CompileProgram(CompileInput{Query: "quota", Scope: "conversation"})
	if err != nil {
		t.Fatal(err)
	}
	res := tf.f.search(ctx, st, prog, t0, targets)
	if len(res.Machines) != 3 || res.StepTraces[0].Note != "hub 本机直接检索，1 台经 nodes.call 实时检索，1 台不可用（离线且没有同步过索引）" {
		t.Fatalf("machines %+v, fan_out %q", res.Machines, res.StepTraces[0].Note)
	}
	for _, c := range tf.nodes.calls {
		if strings.HasPrefix(c, "studio ") {
			t.Fatalf("the hub's computer was also called over nodes: %v", tf.nodes.calls)
		}
	}

	// Its key, id and name all reach it; a machine filter naming it keeps its sessions.
	for _, key := range []string{"hub", "studio"} {
		if got, err := resolveTargets(targets, key); err != nil || len(got) != 1 || !got[0].Local {
			t.Fatalf("route key %q: %+v %v", key, got, err)
		}
	}
	prog, err = CompileProgram(CompileInput{Query: "quota machine:Studio", Scope: "conversation"})
	if err != nil {
		t.Fatal(err)
	}
	res = tf.f.search(ctx, st, prog, t0, filterTargets(targets, programMachine(prog)))
	if len(res.Machines) != 1 || len(res.Hits) != 1 || res.Hits[0].SessionID != "sess-hub-1" ||
		res.Hits[0].Machine != "" || machineLabel(res.Hits[0].Machine, res.Hits[0].MachineName) != "Studio（hub）" {
		t.Fatalf("machine:Studio: machines %+v hits %d", res.Machines, len(res.Hits))
	}
}
