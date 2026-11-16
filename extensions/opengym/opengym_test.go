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
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

// testApp is an app on the fake server, its clock pinned to today() and the catalogue served by cat (nil: names
// off). It has synced once.
func testApp(t *testing.T, f *fakeGym, catURL string) *app {
	t.Helper()
	a := newApp(t.Logf)
	a.now = today
	a.zone = func() *time.Location { return zone }
	a.dataDir = t.TempDir()
	if catURL != "" {
		a.catalogURL = catURL
	}
	a.configure(config{ServerURL: f.url(), RefreshMinutes: 15, ExerciseNames: catURL != ""}, fakeToken)
	if catURL != "" {
		if err := a.loadNames(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.sync(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestParseServer(t *testing.T) {
	for _, ok := range []string{"https://gym.example.com", "https://gym.example.com/gym/", "http://127.0.0.1:8080",
		"http://localhost:8080", "http://[::1]:8080", "http://192.168.1.20:8080", "http://10.0.0.5", "http://nas.local:8080",
		"http://gym.lan", "http://gym:8080"} {
		if _, err := parseServer(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "gym.example.com", "ftp://gym.example.com", "http://gym.example.com",
		"http://203.0.113.9", "https://user:pw@gym.example.com", "https://gym.example.com/?x=1", "https://gym.example.com/#a"} {
		if _, err := parseServer(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	u, _ := parseServer("https://gym.example.com/gym/")
	if got := endpoint(u, "/api/token/data"); got != "https://gym.example.com/gym/api/token/data" {
		t.Fatalf("endpoint %s", got)
	}
}

func TestDialRewritesLoopbackOnlyBehindAProxy(t *testing.T) {
	u, _ := parseServer("http://127.0.0.1:8080/gym")
	none := func(string) string { return "" }
	proxy := func(k string) string {
		if k == "HTTPS_PROXY" {
			return "http://proxy.example:3128"
		}
		return ""
	}
	if got := dialURL(u, none).Host; got != "127.0.0.1:8080" {
		t.Fatalf("without a proxy %s", got)
	}
	if got := dialURL(u, proxy).Host; got != "host.persona.internal:8080" {
		t.Fatalf("behind a proxy %s", got)
	}
	lan, _ := parseServer("http://192.168.1.20:8080")
	if got := dialURL(lan, proxy).Host; got != "192.168.1.20:8080" {
		t.Fatalf("lan behind a proxy %s", got)
	}
}

func TestSyncReadsTheDocumentOnlyWhenTheRevisionMoves(t *testing.T) {
	f := newFakeGym(t)
	a := testApp(t, f, "")
	// "…/data" is a prefix of "…/data/rev" too
	data := func() int {
		return f.count("GET "+basePath+"/api/token/data") - f.count("GET "+basePath+"/api/token/data/rev")
	}
	if data() != 1 || f.count("GET "+basePath+"/api/token/me") != 1 {
		t.Fatalf("first sync: %v", f.log())
	}
	if err := a.sync(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if data() != 1 {
		t.Fatalf("read the document again at the same revision: %v", f.log())
	}
	f.bump(func(s map[string]any) {
		s["bodyweight"] = append(s["bodyweight"].([]any), map[string]any{"d": "2026-11-13", "w": 77.2, "t": ms("2026-11-13", "07:00")})
	})
	if err := a.sync(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if data() != 2 {
		t.Fatalf("did not read the moved revision: %v", f.log())
	}
	s := a.snapshot()
	if s.Rev != 42 || s.Me.User.Name != "示例用户" || len(s.State.Bodyweight) != 7 {
		t.Fatalf("snapshot rev %d me %+v bw %d", s.Rev, s.Me, len(s.State.Bodyweight))
	}
	// a quick sync within the minute asks nothing
	before := f.n()
	if err := a.sync(context.Background(), false); err != nil || f.n() != before {
		t.Fatalf("unforced sync asked the server (%v)", err)
	}
}

func TestRefusedTokenIsNeverQuoted(t *testing.T) {
	f := newFakeGym(t)
	a := testApp(t, f, "")
	f.with(func() { f.refuse = true })
	err := a.sync(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), fakeToken) {
		t.Fatalf("refusal %v", err)
	}
	st := a.status()
	b, _ := json.Marshal(st)
	if st["state"] != "error" || strings.Contains(string(b), fakeToken) || !strings.Contains(string(b), "令牌") {
		t.Fatalf("status %s", b)
	}
	// what was read before stays for the page
	if len(a.snapshot().State.Workouts) == 0 {
		t.Fatal("lost the cached document")
	}
}

func TestStatusNotConfigured(t *testing.T) {
	a := newApp(t.Logf)
	a.configure(config{}, "")
	if st := a.status(); st["state"] != "not_configured" {
		t.Fatalf("no address: %+v", st)
	}
	a.configure(config{ServerURL: "https://gym.example.com"}, "")
	if st := a.status(); st["state"] != "not_configured" || !strings.Contains(fmt.Sprint(st["guide"]), "API 令牌") {
		t.Fatalf("no token: %+v", st)
	}
	a.configure(config{ServerURL: "http://gym.example.com"}, fakeToken)
	if st := a.status(); st["state"] != "not_configured" || !strings.Contains(fmt.Sprint(st["error"]), "https") {
		t.Fatalf("public http: %+v", st)
	}
}

func TestDocumentFigures(t *testing.T) {
	f := newFakeGym(t)
	a := testApp(t, f, "")
	s := a.snapshot().State
	if s.Unit != "kg" || len(s.Workouts) != 22 || len(s.Routines) != 3 {
		t.Fatalf("parsed %q %d %d", s.Unit, len(s.Workouts), len(s.Routines))
	}
	mon := s.workout("w1109")
	if mon == nil || mon.volume() != 2165 || !mon.hasPR() {
		t.Fatalf("stored volume and PR: %+v", mon)
	}
	wed := s.workout("w1111")
	// 3 × 8 × 70, and a timed set adds no volume
	if wed.volume() != 1680 || wed.minutes() != 47 || wed.doneSets() != 4 {
		t.Fatalf("computed volume %v minutes %v sets %d", wed.volume(), wed.minutes(), wed.doneSets())
	}
	weeks := weeklyTotals(s, today(), zone, 12)
	if len(weeks) != 12 || weeks[11].Start != "2026-11-09" || weeks[11].Sessions != 2 || weeks[11].Volume != 2165+1680 {
		t.Fatalf("this week %+v", weeks[len(weeks)-1])
	}
	if weeks[0].Sessions != 0 || weeks[1].Sessions != 2 {
		t.Fatalf("oldest weeks %+v %+v", weeks[0], weeks[1])
	}
	if bw, ok := s.latestWeight(); !ok || bw.W != 77.5 {
		t.Fatalf("latest weight %+v", bw)
	}
}

func TestPlanRules(t *testing.T) {
	f := newFakeGym(t)
	a := testApp(t, f, "")
	s := a.snapshot().State
	days := s.planDays(today(), zone, 7)
	want := []struct{ date, by, ids string }{
		{"2026-11-13", "weekday", "r_legs"},
		{"2026-11-14", "rest_override", ""},
		{"2026-11-15", "rest", ""},
		{"2026-11-16", "weekday", "r_push"},
		{"2026-11-17", "override", "r_legs"},
		{"2026-11-18", "weekday", "r_pull"},
		{"2026-11-19", "rest", ""},
	}
	for i, w := range want {
		d := days[i]
		if d.Date != w.date || d.By != w.by || strings.Join(d.IDs, ",") != w.ids {
			t.Errorf("day %d: %+v, want %+v", i, d, w)
		}
	}
	n := s.next(today(), zone)
	if n == nil || n.Date != "2026-11-13" || n.Routine.ID != "r_legs" || n.By != "weekday" {
		t.Fatalf("next %+v", n)
	}
	// Once today's session is in, the next one is Monday's.
	f.bump(func(st map[string]any) {
		st["workouts"] = append(st["workouts"].([]any), map[string]any{"id": "w1113", "d": "2026-11-13", "start": ms("2026-11-13", "18:00"),
			"end": ms("2026-11-13", "19:00"), "name": "腿", "routineIds": []any{"r_legs"}, "entries": []any{}})
	})
	_ = a.sync(context.Background(), true)
	if n := a.snapshot().State.next(today(), zone); n == nil || n.Date != "2026-11-16" || n.Routine.ID != "r_push" {
		t.Fatalf("next after today's %+v", n)
	}

	// A coach week: sessions in order on whatever day, a pin, done by clock.
	f.bump(func(st map[string]any) {
		st["queue"] = map[string]any{"ids": []any{"r_push", "r_pull", "r_legs", "r_gone"}, "since": ms("2026-11-10", "00:00"),
			"startsOn": "2026-11-10", "label": "力量周"}
		st["dayPlan"] = map[string]any{"2026-11-16": "r_legs"}
	})
	_ = a.sync(context.Background(), true)
	s = a.snapshot().State
	cw := s.coachWeek(today(), zone)
	if cw == nil || cw.Label != "力量周" || len(cw.Items) != 3 {
		t.Fatalf("coach week %+v", cw)
	}
	// r_push was done before since (Monday 11-09 18:30 < 11-10): still to do; r_pull done Wednesday; r_legs done
	// today (w1113) — so push is next, and the Monday pin on legs is fulfilled.
	states := []string{cw.Items[0].State, cw.Items[1].State, cw.Items[2].State}
	if !slices.Equal(states, []string{"next", "done", "done"}) {
		t.Fatalf("coach states %v", states)
	}
	if n := s.next(today(), zone); n == nil || n.Routine.ID != "r_push" || n.By != "coach" {
		t.Fatalf("coach next %+v", n)
	}
	// weekday plans stay hidden for queue routines while the week is live
	for _, d := range s.planDays(today(), zone, 7) {
		if d.Date == "2026-11-18" && d.By != "rest" {
			t.Fatalf("weekday pull shown inside a live coach week: %+v", d)
		}
	}
}

func TestExerciseNames(t *testing.T) {
	f := newFakeGym(t)
	cat, hits := fakeCatalogue(t)
	a := testApp(t, f, cat.URL+"/zh.json")
	if got := a.exName("0025"); got != "杠铃卧推" {
		t.Fatalf("catalogue name %q", got)
	}
	if got := a.exName("c_sled"); got != "雪橇推" {
		t.Fatalf("custom name %q", got)
	}
	if got := a.exName("9999"); got != "动作 9999" {
		t.Fatalf("unknown %q", got)
	}
	// kept on disk with its ETag, asked again with If-None-Match only after a week
	if _, err := os.Stat(filepath.Join(a.dataDir, namesFile)); err != nil {
		t.Fatal(err)
	}
	b := newApp(t.Logf)
	b.dataDir, b.catalogURL, b.now = a.dataDir, a.catalogURL, func() time.Time { return today().Add(time.Hour) }
	b.configure(config{ServerURL: f.url(), ExerciseNames: true}, fakeToken)
	if err := b.loadNames(context.Background()); err != nil || *hits != 1 || b.exName("0043") != "杠铃深蹲" {
		t.Fatalf("reload from disk: %v hits %d", err, *hits)
	}
	b.now = func() time.Time { return today().AddDate(0, 0, 8) }
	if err := b.loadNames(context.Background()); err != nil || *hits != 2 || b.exName("0043") != "杠铃深蹲" {
		t.Fatalf("weekly recheck: %v hits %d", err, *hits)
	}
	// names off: ids and custom names only
	c := newApp(t.Logf)
	c.dataDir = t.TempDir()
	c.configure(config{ServerURL: f.url(), ExerciseNames: false}, fakeToken)
	_ = c.loadNames(context.Background())
	if *hits != 2 || c.exName("0025") != "动作 0025" {
		t.Fatalf("names off: hits %d name %q", *hits, c.exName("0025"))
	}
	// resolving what the assistant says
	if id, err := a.resolveExercise("杠铃卧推"); err != nil || id != "0025" {
		t.Fatalf("resolve name %q %v", id, err)
	}
	if id, err := a.resolveExercise("0405"); err != nil || id != "0405" {
		t.Fatalf("resolve id %q %v", id, err)
	}
	if _, err := a.resolveExercise("卧推"); err == nil || !strings.Contains(err.Error(), "杠铃卧推") {
		t.Fatalf("ambiguous should list candidates: %v", err)
	}
}

func call(t *testing.T, a *app, method, name string, args any, requestID string) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"contribution": connectorID, "name": name, "args": args, "request_id": requestID})
	res, err := a.connector(context.Background(), method, raw)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(res)
	return string(b), nil
}

func TestReadTools(t *testing.T) {
	f := newFakeGym(t)
	cat, _ := fakeCatalogue(t)
	a := testApp(t, f, cat.URL+"/zh.json")
	cases := []struct {
		name string
		args map[string]any
		want []string
		not  []string
	}{
		{toolSummary, nil, []string{"本周 2 次", "3,845 kg", "下一次：2026-11-13（今天）腿", "体重 77.5 kg", "示例用户"}, nil},
		{toolWorkouts, map[string]any{"limit": 3}, []string{"w1111", "2026-11-11", "拉", "47 分钟", "1,680 kg", "w1109", "PR", "w1102b"}, []string{"w1102a"}},
		{toolWorkouts, map[string]any{"from": "2026-09-01", "to": "2026-09-07"}, []string{"w0831b", "2026-09-03"}, []string{"w0831a", "w1109"}},
		{toolWorkouts, map[string]any{"workout_id": "w1109"}, []string{"杠铃卧推", "热身 40 kg × 10", "85 kg × 5", "哑铃侧平举", "PR"}, nil},
		{toolPlan, nil, []string{"2026-11-13（周五）腿", "2026-11-14（周六）休息（当天改成休息）", "2026-11-17（周二）腿（当天单独安排）", "r_push", "杠铃卧推 3 组 × 5 次 @ 85 kg", "雪橇推 3 组 × 30 秒 @ 100 kg"}, nil},
		{toolBodyweight, nil, []string{"77.5 kg", "2026-11-12", "78.6 kg"}, nil},
		{toolExercises, map[string]any{"query": "哑铃"}, []string{"0405 哑铃侧平举", "0294 哑铃弯举"}, []string{"杠铃"}},
		{toolExercises, nil, []string{"0025 杠铃卧推", "c_sled 雪橇推（自建）"}, nil},
	}
	for _, c := range cases {
		out, err := call(t, a, "tools.call", c.name, c.args, "")
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var res struct{ Text string }
		_ = json.Unmarshal([]byte(out), &res)
		for _, w := range c.want {
			if !strings.Contains(res.Text, w) {
				t.Errorf("%s %v: no %q in\n%s", c.name, c.args, w, res.Text)
			}
		}
		for _, n := range c.not {
			if strings.Contains(res.Text, n) {
				t.Errorf("%s %v: %q should not be in\n%s", c.name, c.args, n, res.Text)
			}
		}
		if strings.Contains(res.Text, "never returned") || strings.Contains(res.Text, fakeToken) {
			t.Fatalf("%s leaked: %s", c.name, res.Text)
		}
	}
	if _, err := call(t, a, "tools.call", toolWorkouts, map[string]any{"workout_id": "nope"}, ""); err == nil {
		t.Fatal("an unknown workout answered")
	}
	if _, err := call(t, a, "tools.call", "opengym_drop_tables", nil, ""); err == nil {
		t.Fatal("an unknown tool answered")
	}
	if _, err := call(t, a, "status", "", nil, ""); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"contribution": "other"})
	if _, err := a.connector(context.Background(), "status", raw); err == nil {
		t.Fatal("another connector answered")
	}
}

func TestLogWorkoutIsDescribedWithoutTheNetworkAndWrittenOnce(t *testing.T) {
	f := newFakeGym(t)
	cat, _ := fakeCatalogue(t)
	a := testApp(t, f, cat.URL+"/zh.json")
	args := map[string]any{"date": "2026-11-13", "name": "腿", "start_time": "18:10", "duration_minutes": 50, "note": "状态不错",
		"exercises": []any{
			map[string]any{"exercise": "杠铃深蹲", "sets": []any{map[string]any{"reps": 10, "weight": 60, "warmup": true},
				map[string]any{"reps": 5, "weight": 110}, map[string]any{"reps": 5, "weight": 110}}},
			map[string]any{"exercise": "c_sled", "sets": []any{map[string]any{"seconds": 30, "weight": 120}}},
			map[string]any{"exercise": "农夫行走", "create_custom": true, "body_part": "upper legs", "sets": []any{map[string]any{"seconds": 40, "weight": 32}}},
		}}
	before := f.n()
	out, err := call(t, a, "actions.describe", actLogWorkout, args, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.n() != before {
		t.Fatal("describe reached the server")
	}
	for _, w := range []string{"2026-11-13 18:10", "腿", "杠铃深蹲 2 组", "热身 1 组", "农夫行走（新建自定义动作）", "1,100 kg"} {
		if !strings.Contains(out, w) {
			t.Errorf("summary has no %q: %s", w, out)
		}
	}
	out, err = call(t, a, "actions.do", actLogWorkout, args, "req-1")
	if err != nil || !strings.Contains(out, "记好了") {
		t.Fatalf("do %s %v", out, err)
	}
	put := f.lastPut()
	ws := put["workouts"].([]any)
	if len(ws) != 23 {
		t.Fatalf("%d workouts written", len(ws))
	}
	last := ws[len(ws)-1].(map[string]any) // chronological: today's is last
	if last["d"] != "2026-11-13" || last["name"] != "腿" || last["vol"].(float64) != 1100 || last["note"] != "状态不错" {
		t.Fatalf("written workout %v", last)
	}
	if last["start"].(float64) != float64(ms("2026-11-13", "18:10")) || last["end"].(float64) != float64(ms("2026-11-13", "19:00")) {
		t.Fatalf("times %v %v", last["start"], last["end"])
	}
	entries := last["entries"].([]any)
	sq := entries[0].(map[string]any)
	sets := sq["sets"].([]any)
	if sq["id"] != "0043" || sq["topW"].(float64) != 110 || sets[0].(map[string]any)["phase"] != "warmup" || sets[1].(map[string]any)["r"].(float64) != 5 {
		t.Fatalf("entry %v", sq)
	}
	custom := put["customEx"].([]any)
	newEx := custom[len(custom)-1].(map[string]any)
	if newEx["n"] != "农夫行走" || newEx["custom"] != true || entries[2].(map[string]any)["id"] != newEx["id"] {
		t.Fatalf("custom exercise %v", custom)
	}
	// an existing workout keeps the fields this extension does not know
	first := ws[0].(map[string]any)
	if first["routineId"] != "r_push" {
		t.Fatalf("lost fields of an existing workout: %v", first)
	}
	if _, ok := put["routines"]; ok {
		t.Fatal("wrote keys it did not change")
	}
	// the same request again writes nothing
	puts := f.nPuts()
	if out, err := call(t, a, "actions.do", actLogWorkout, args, "req-1"); err != nil || f.nPuts() != puts || !strings.Contains(out, "记好了") {
		t.Fatalf("repeat %s %v", out, err)
	}
	// another request id with the same workout is refused as a duplicate
	if out, err := call(t, a, "actions.do", actLogWorkout, args, "req-2"); err != nil || f.nPuts() != puts || !strings.Contains(out, "已经有") {
		t.Fatalf("duplicate %s %v", out, err)
	}
	// the page and tools see it
	if s := a.snapshot().State; s.workout(last["id"].(string)) == nil {
		t.Fatal("not in the snapshot after writing")
	}
}

func TestWritesRetryAConflictOnce(t *testing.T) {
	f := newFakeGym(t)
	a := testApp(t, f, "")
	f.with(func() { f.conflict = 1 })
	args := map[string]any{"date": "2026-11-18", "routine_id": "r_legs"}
	if out, err := call(t, a, "actions.do", actScheduleDay, args, "req-c"); err != nil || !strings.Contains(out, "2026-11-18") {
		t.Fatalf("after one conflict %s %v", out, err)
	}
	if got := f.lastPut()["dayPlan"].(map[string]any); got["2026-11-18"] != "r_legs" || got["2026-11-14"] != "rest" {
		t.Fatalf("dayPlan %v", got)
	}
	f.with(func() { f.conflict = 2 })
	if _, err := call(t, a, "actions.do", actScheduleDay, map[string]any{"date": "2026-11-19", "rest": true}, "req-d"); err == nil || !strings.Contains(err.Error(), "冲突") {
		t.Fatalf("two conflicts: %v", err)
	}
	// clearing a day's plan removes its key
	if _, err := call(t, a, "actions.do", actScheduleDay, map[string]any{"date": "2026-11-14", "clear": true}, "req-e"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.lastPut()["dayPlan"].(map[string]any)["2026-11-14"]; ok {
		t.Fatal("cleared day still planned")
	}
	for _, bad := range []map[string]any{{"date": "2026-11-18"}, {"date": "18.11.2026", "rest": true}, {"date": "2026-11-18", "routine_id": "r_nope"}} {
		if _, err := call(t, a, "actions.describe", actScheduleDay, bad, ""); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestCreateRoutine(t *testing.T) {
	f := newFakeGym(t)
	cat, _ := fakeCatalogue(t)
	a := testApp(t, f, cat.URL+"/zh.json")
	args := map[string]any{"name": "上肢 A", "date": "2026-11-15", "exercises": []any{
		map[string]any{"exercise": "0025", "sets": 4, "reps": 6, "weight": 82.5, "rest_seconds": 180},
		map[string]any{"exercise": "哑铃弯举", "sets": 3, "reps": 12, "weight": 12},
		map[string]any{"exercise": "c_sled", "sets": 3, "seconds": 30, "weight": 100},
	}}
	out, err := call(t, a, "actions.describe", actCreateRoutine, args, "")
	if err != nil || !strings.Contains(out, "上肢 A") || !strings.Contains(out, "杠铃卧推 4 组 × 6 次 @ 82.5 kg") || !strings.Contains(out, "2026-11-15") {
		t.Fatalf("describe %s %v", out, err)
	}
	if _, err := call(t, a, "actions.do", actCreateRoutine, args, "req-r"); err != nil {
		t.Fatal(err)
	}
	put := f.lastPut()
	rs := put["routines"].([]any)
	r := rs[len(rs)-1].(map[string]any)
	ex := r["ex"].([]any)
	if len(rs) != 4 || r["name"] != "上肢 A" || ex[0].(map[string]any)["restSec"].(float64) != 180 || ex[2].(map[string]any)["mode"] != "time" || ex[2].(map[string]any)["sec"].(float64) != 30 {
		t.Fatalf("routine %v", r)
	}
	if put["dayPlan"].(map[string]any)["2026-11-15"] != r["id"] {
		t.Fatalf("not planned on the date: %v", put["dayPlan"])
	}
	for _, bad := range []map[string]any{{"name": "x"}, {"name": "", "exercises": args["exercises"]},
		{"name": "x", "exercises": []any{map[string]any{"exercise": "0025", "sets": 99, "reps": 5}}},
		{"name": "x", "exercises": []any{map[string]any{"exercise": "不存在的动作", "sets": 3, "reps": 5}}}} {
		if _, err := call(t, a, "actions.describe", actCreateRoutine, bad, ""); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestWritesNeedTheApplyScope(t *testing.T) {
	f := newFakeGym(t)
	f.with(func() { f.scopes["plans"] = "view" })
	a := testApp(t, f, "")
	_, err := call(t, a, "actions.describe", actScheduleDay, map[string]any{"date": "2026-11-18", "rest": true}, "")
	if err == nil || !strings.Contains(err.Error(), "应用") {
		t.Fatalf("view scope: %v", err)
	}
	f.with(func() { f.scopes["workouts"] = "off" })
	_ = a.sync(context.Background(), true)
	out, _ := call(t, a, "tools.call", toolWorkouts, nil, "")
	if !strings.Contains(out, "没有") || !strings.Contains(out, "训练") {
		t.Fatalf("workouts off: %s", out)
	}
}

func TestDoRefusesAMissingRequestID(t *testing.T) {
	f := newFakeGym(t)
	a := testApp(t, f, "")
	if _, err := call(t, a, "actions.do", actScheduleDay, map[string]any{"date": "2026-11-18", "rest": true}, ""); err == nil {
		t.Fatal("ran without a request id")
	}
}

func uiRequest(contribution string, version int, action, state string, values map[string]string) *ext.UIRequest {
	placement, size := wire.PlacementNavPage, wire.SizeFull
	if contribution == cardUI {
		placement, size = wire.PlacementOverviewCard, wire.SizeCompact
	}
	return &ext.UIRequest{UIParams: wire.UIParams{Contribution: contribution, Placement: placement, Size: size,
		UIVersion: version, TZ: "Asia/Shanghai", Action: action, State: state, Values: values}}
}

func checkView(t *testing.T, v *wire.View, compact bool) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := exttest.CheckView(raw, compact); err != nil {
		t.Fatalf("view: %v\n%s", err, raw)
	}
	return string(raw)
}

func TestPage(t *testing.T) {
	f := newFakeGym(t)
	cat, _ := fakeCatalogue(t)
	a := testApp(t, f, cat.URL+"/zh.json")
	ctx := context.Background()
	for v := 1; v <= 6; v++ {
		view, err := a.renderPage(ctx, uiRequest(pageUI, v, "", "", nil))
		if err != nil {
			t.Fatal(err)
		}
		raw := checkView(t, view, false)
		for _, w := range []string{"下一次训练", "腿", "杠铃深蹲", "最近训练", "拉"} {
			if !strings.Contains(raw, w) {
				t.Errorf("v%d page has no %q", v, w)
			}
		}
		if v >= 2 && (!strings.Contains(raw, "每周训练量") || !strings.Contains(raw, `"chart"`) || !strings.Contains(raw, "stat_grid")) {
			t.Errorf("v%d page has no chart or stats: %s", v, raw)
		}
		if v < 2 && (strings.Contains(raw, `"chart"`) || strings.Contains(raw, "stat_grid")) {
			t.Errorf("v1 page has v2 components")
		}
	}
	// a workout's detail, then back
	res, err := a.pageAction(ctx, uiRequest(pageUI, 4, "workout:w1109", "", nil))
	if err != nil {
		t.Fatal(err)
	}
	raw := checkView(t, res.View, false)
	if !strings.Contains(raw, "杠铃卧推") || !strings.Contains(raw, "85 kg × 5") || !strings.Contains(raw, "热身") {
		t.Fatalf("detail %s", raw)
	}
	res, _ = a.pageAction(ctx, uiRequest(pageUI, 4, "back", res.View.State, nil))
	if raw := checkView(t, res.View, false); !strings.Contains(raw, "每周训练量") {
		t.Fatalf("back %s", raw)
	}
	// the plan tab
	res, _ = a.pageAction(ctx, uiRequest(pageUI, 4, "tab", "", map[string]string{"tab": tabPlan}))
	if raw := checkView(t, res.View, false); !strings.Contains(raw, "未来 7 天") || !strings.Contains(raw, "推") {
		t.Fatalf("plan tab %s", raw)
	}
	// refresh
	res, _ = a.pageAction(ctx, uiRequest(pageUI, 4, "refresh", "", nil))
	if !strings.Contains(res.Toast, "最新") {
		t.Fatalf("refresh toast %q", res.Toast)
	}
	// a stale state draws the overview
	view, _ := a.renderPage(ctx, uiRequest(pageUI, 4, "", `{"w":"gone"}`, nil))
	checkView(t, view, false)

	// the card
	for v := 1; v <= 6; v++ {
		view, err := a.renderCard(ctx, uiRequest(cardUI, v, "", "", nil))
		if err != nil {
			t.Fatal(err)
		}
		raw := checkView(t, view, true)
		if !strings.Contains(raw, "本周") || !strings.Contains(raw, "腿") {
			t.Errorf("v%d card %s", v, raw)
		}
	}
}

func TestPageNotConfiguredAndRefused(t *testing.T) {
	a := newApp(t.Logf)
	a.configure(config{}, "")
	ctx := context.Background()
	for _, ui := range []string{pageUI, cardUI} {
		r := uiRequest(ui, 4, "", "", nil)
		var v *wire.View
		if ui == pageUI {
			v, _ = a.renderPage(ctx, r)
		} else {
			v, _ = a.renderCard(ctx, r)
		}
		if raw := checkView(t, v, ui == cardUI); !strings.Contains(raw, "服务器地址") {
			t.Fatalf("%s not configured: %s", ui, raw)
		}
	}
	f := newFakeGym(t)
	f.with(func() { f.refuse = true })
	b := newApp(t.Logf)
	b.configure(config{ServerURL: f.url()}, fakeToken)
	v, _ := b.renderPage(ctx, uiRequest(pageUI, 4, "", "", nil))
	if raw := checkView(t, v, false); !strings.Contains(raw, "令牌") {
		t.Fatalf("refused page %s", raw)
	}
}

func TestEmptyProfile(t *testing.T) {
	f := newFakeGym(t)
	f.state = map[string]any{}
	f.rev = 0
	a := newApp(t.Logf)
	a.now, a.zone = today, func() *time.Location { return zone }
	a.configure(config{ServerURL: f.url()}, fakeToken)
	_ = a.sync(context.Background(), true)
	v, _ := a.renderPage(context.Background(), uiRequest(pageUI, 4, "", "", nil))
	if raw := checkView(t, v, false); !strings.Contains(raw, "还没有") {
		t.Fatalf("empty %s", raw)
	}
	if _, err := call(t, a, "actions.do", actScheduleDay, map[string]any{"date": "2026-11-18", "rest": true}, "r"); err == nil {
		t.Fatal("wrote to a profile no device synced")
	}
}

func TestManifestMatchesTheCode(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID, Version string
		Runtime     struct{ Command map[string]string }
		Contributes struct {
			Connectors []struct {
				ID            string
				Capabilities  []string
				ConfirmAlways []string `json:"confirm_always"`
			}
			UI     []struct{ ID, Placement, Opens string }
			Skills []struct{ ID, Dir string }
		}
		Configuration struct {
			Properties map[string]struct{ Default any }
		}
		Secrets     []struct{ Name string }
		Permissions struct{ Network []string }
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "opengym" || m.ID != about.ID || m.Version != about.Version {
		t.Fatalf("id %q version %q", m.ID, m.Version)
	}
	c := m.Contributes.Connectors
	if len(c) != 1 || c[0].ID != connectorID || !slices.Equal(c[0].Capabilities, []string{"status", "tools", "actions"}) {
		t.Fatalf("connectors %+v", c)
	}
	var writes []string
	for _, w := range actions {
		writes = append(writes, w.Name)
	}
	if !slices.Equal(c[0].ConfirmAlways, writes) {
		t.Fatalf("every write must be confirmed: confirm_always %v, actions %v", c[0].ConfirmAlways, writes)
	}
	u := m.Contributes.UI
	if len(u) != 2 || u[0].ID != pageUI || u[0].Placement != wire.PlacementNavPage || u[1].ID != cardUI ||
		u[1].Placement != wire.PlacementOverviewCard || u[1].Opens != pageUI {
		t.Fatalf("ui %+v", u)
	}
	if s := m.Contributes.Skills; len(s) != 1 {
		t.Fatalf("skills %+v", s)
	} else if _, err := os.Stat(filepath.Join(s[0].Dir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if m.Configuration.Properties["refresh_minutes"].Default != float64(defaultRefresh) || m.Configuration.Properties["exercise_names"].Default != true {
		t.Fatalf("defaults %+v", m.Configuration.Properties)
	}
	if len(m.Secrets) != 1 || m.Secrets[0].Name != secretToken {
		t.Fatalf("secrets %+v", m.Secrets)
	}
	if !slices.Equal(m.Permissions.Network, []string{"raw.githubusercontent.com", sandboxHost}) {
		t.Fatalf("network %v", m.Permissions.Network)
	}
	for _, p := range []string{"linux-amd64", "linux-arm64", "darwin-arm64", "darwin-amd64"} {
		if m.Runtime.Command[p] != "bin/opengym-"+p {
			t.Errorf("runtime.command[%s] = %q", p, m.Runtime.Command[p])
		}
	}
	for _, list := range [][]tool{tools, actions} {
		for _, tl := range list {
			if !strings.HasPrefix(tl.Name, connectorID+"_") {
				t.Errorf("tool %s does not start with the connector id", tl.Name)
			}
		}
	}
	skill, _ := os.ReadFile("skills/opengym/SKILL.md")
	for _, list := range [][]tool{tools, actions} {
		for _, tl := range list {
			if !strings.Contains(string(skill), tl.Name) {
				t.Errorf("the skill does not mention %s", tl.Name)
			}
		}
	}
}

func TestConformance(t *testing.T) {
	f := newFakeGym(t)
	cfg, _ := json.Marshal(config{ServerURL: f.url(), RefreshMinutes: 60})
	exttest.Check(t, manifest, exttest.Serve(t, program(newApp(t.Logf))), cfg)
	h := exttest.CheckConnectors(t, manifest, exttest.Serve(t, program(newApp(t.Logf))), cfg)
	var st wire.ConnectorStatus
	if err := h.Call("connector.status", wire.ConnectorParams{Contribution: connectorID}, &st); err != nil || st.State != wire.ConnectorStateNotConfigured {
		t.Fatalf("status without a token %+v %v", st, err)
	}
}
