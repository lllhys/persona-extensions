package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// A fake openGym server: the scoped-token routes of api/server.js (GET /api/token/me, GET /api/token/data,
// GET /api/token/data/rev, PUT /api/token/data), under a BASE_PATH, with synthetic data only.

const (
	fakeToken = "ogt_synthetic-token-for-tests-0000000000000000"
	basePath  = "/gym"
)

// zone and today pin the tests' clock: Friday 2026-11-13 19:30 in UTC+8.
var zone = time.FixedZone("UTC+8", 8*3600)

func today() time.Time { return time.Date(2026, 11, 13, 19, 30, 0, 0, zone) }

// ms is the epoch milliseconds of a local date and time in zone.
func ms(date, clock string) int64 {
	t, err := time.ParseInLocation("2006-01-02 15:04", date+" "+clock, zone)
	if err != nil {
		panic(err)
	}
	return t.UnixMilli()
}

type fakeGym struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	state    map[string]any // the whole profile document
	rev      int
	wid      string
	scopes   map[string]string
	requests []string // "METHOD path"
	puts     []map[string]any
	conflict int // answer this many PUTs with a 409 conflict after moving the revision
	refuse   bool
}

func newFakeGym(t *testing.T) *fakeGym {
	f := &fakeGym{t: t, state: sampleState(), rev: 41, wid: "w-device",
		scopes: map[string]string{"workouts": "apply", "plans": "apply", "bodyweight": "view", "measurements": "off", "notes": "off"}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGym) url() string { return f.srv.URL + basePath }

func (f *fakeGym) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// with changes the fake's behavior between requests.
func (f *fakeGym) with(change func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change()
}

func (f *fakeGym) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeGym) nPuts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.puts)
}

func (f *fakeGym) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

func (f *fakeGym) lastPut() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.puts) == 0 {
		return nil
	}
	return f.puts[len(f.puts)-1]
}

func (f *fakeGym) bump(mutate func(state map[string]any)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mutate(f.state)
	f.rev++
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// scoped is the document as the token sees it (tokens.js scopedState): settings always, categories by scope.
func (f *fakeGym) scoped() map[string]any {
	cat := map[string]string{"workouts": "workouts", "exWeights": "workouts", "customEx": "workouts",
		"routines": "plans", "week": "plans", "dayPlan": "plans", "queue": "plans", "bodyweight": "bodyweight"}
	out := map[string]any{}
	for k, v := range f.state {
		c, ok := cat[k]
		if k == "customEx" && (f.scopes["plans"] != "off" || f.scopes["workouts"] != "off") {
			out[k] = v
			continue
		}
		if !ok || f.scopes[c] != "off" {
			out[k] = v
		}
	}
	return out
}

func (f *fakeGym) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer "+fakeToken || f.refuse {
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		writeJSON(w, 401, map[string]any{"error": "token not accepted", "code": "token-invalid"})
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, basePath)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch r.Method + " " + path {
	case "GET /api/token/me":
		writeJSON(w, 200, map[string]any{"user": map[string]any{"id": "u-1", "name": "示例用户"},
			"token": map[string]any{"id": "t-1", "name": "Persona", "scopes": f.scopes, "exp": nil}})
	case "GET /api/token/data/rev":
		writeJSON(w, 200, map[string]any{"rev": f.rev, "wid": f.wid})
	case "GET /api/token/data":
		if len(f.state) == 0 { // no device synced: no state file
			writeJSON(w, 200, map[string]any{"state": nil, "rev": 0})
			return
		}
		writeJSON(w, 200, map[string]any{"state": f.scoped(), "rev": f.rev, "wid": f.wid})
	case "PUT /api/token/data":
		var body struct {
			State   map[string]any `json:"state"`
			BaseRev *int           `json:"baseRev"`
			BaseWid string         `json:"baseWid"`
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil || body.State == nil {
			writeJSON(w, 400, map[string]any{"error": "state required"})
			return
		}
		if body.BaseRev == nil {
			writeJSON(w, 400, map[string]any{"error": "baseRev required", "code": "base-rev"})
			return
		}
		if len(f.state) == 0 {
			writeJSON(w, 409, map[string]any{"error": "this profile has not synced from a device yet", "code": "not-synced"})
			return
		}
		if f.conflict > 0 {
			f.conflict--
			f.rev++
			writeJSON(w, 409, map[string]any{"error": "conflict", "rev": f.rev, "state": f.scoped(), "wid": f.wid})
			return
		}
		if *body.BaseRev != f.rev || (body.BaseWid != "" && body.BaseWid != f.wid) {
			writeJSON(w, 409, map[string]any{"error": "conflict", "rev": f.rev, "state": f.scoped(), "wid": f.wid})
			return
		}
		cat := map[string]string{"workouts": "workouts", "customEx": "workouts", "routines": "plans", "dayPlan": "plans", "week": "plans", "queue": "plans"}
		for k := range body.State {
			if c, ok := cat[k]; !ok || f.scopes[c] != "apply" {
				if k == "customEx" && f.scopes["plans"] == "apply" {
					continue
				}
				writeJSON(w, 403, map[string]any{"error": "the token may not change " + k, "code": "scope"})
				return
			}
		}
		f.puts = append(f.puts, body.State)
		for k, v := range body.State {
			f.state[k] = v
		}
		f.rev++
		f.wid = "w-token"
		writeJSON(w, 200, map[string]any{"ok": true, "ts": nil, "rev": f.rev, "wid": f.wid})
	default:
		http.NotFound(w, r)
	}
}

// sampleState is a synthetic profile: three routines on a weekly plan, eleven weeks of workouts, body weight and a
// custom exercise. Exercise ids are openGym catalogue ids; their names come from fakeCatalogue.
func sampleState() map[string]any {
	set := func(w, r float64) map[string]any { return map[string]any{"w": w, "r": r, "done": true} }
	warm := func(w, r float64) map[string]any {
		return map[string]any{"w": w, "r": r, "done": true, "phase": "warmup"}
	}
	workout := func(id, date, name, routine string, minutes int, entries ...map[string]any) map[string]any {
		start := ms(date, "18:30")
		return map[string]any{"id": id, "d": date, "start": start, "end": start + int64(minutes)*60000, "name": name,
			"routineIds": []any{routine}, "routineId": routine, "bw": nil, "prs": []any{}, "entries": entries}
	}
	entry := func(id string, sets ...map[string]any) map[string]any {
		return map[string]any{"id": id, "topW": nil, "target": nil, "sets": sets}
	}
	var workouts []any
	// Ten earlier weeks, two sessions each, getting a little heavier.
	for week := 10; week >= 1; week-- {
		monday := time.Date(2026, 11, 9, 0, 0, 0, 0, zone).AddDate(0, 0, -7*week)
		w := 60 + float64(10-week)*2.5
		workouts = append(workouts,
			workout("w"+monday.Format("0102")+"a", monday.Format("2006-01-02"), "推", "r_push", 55,
				entry("0025", set(w, 8), set(w, 8), set(w, 7))),
			workout("w"+monday.Format("0102")+"b", monday.AddDate(0, 0, 3).Format("2006-01-02"), "腿", "r_legs", 62,
				entry("0043", set(w+20, 5), set(w+20, 5), set(w+20, 5))))
	}
	// This week: Monday push with a PR and a warm-up, Wednesday pull, nothing yet on Friday.
	mon := workout("w1109", "2026-11-09", "推", "r_push", 58,
		entry("0025", warm(40, 10), set(85, 5), set(85, 5), set(80, 6)),
		entry("0405", set(20, 12), set(20, 12)))
	mon["prs"] = []any{"0025"}
	mon["vol"] = 2165.0 // the app's stored volume wins over the computed one
	wed := workout("w1111", "2026-11-11", "拉", "r_pull", 47,
		entry("0027", set(70, 8), set(70, 8), set(70, 8)),
		entry("c_sled", map[string]any{"w": 100, "sec": 30, "done": true}))
	workouts = append(workouts, mon, wed)

	bodyweight := []any{}
	for i, w := range []float64{78.6, 78.4, 78.1, 77.9, 77.8, 77.5} {
		d := time.Date(2026, 10, 3, 0, 0, 0, 0, zone).AddDate(0, 0, 8*i).Format("2006-01-02")
		bodyweight = append(bodyweight, map[string]any{"d": d, "w": w, "t": ms(d, "07:00")})
	}
	return map[string]any{
		"unit": "kg", "lang": "zh", "_rev": 41,
		"workouts": workouts,
		"routines": []any{
			map[string]any{"id": "r_push", "name": "推", "emoji": "figureStrength", "ex": []any{
				map[string]any{"id": "0025", "sets": 3, "reps": 5, "weight": 85, "mode": "reps", "restSec": 150},
				map[string]any{"id": "0405", "sets": 3, "reps": 12, "weight": 20, "mode": "reps"}}},
			map[string]any{"id": "r_pull", "name": "拉", "emoji": "figureStrength", "ex": []any{
				map[string]any{"id": "0027", "sets": 3, "reps": 8, "weight": 72.5, "mode": "reps"}}},
			map[string]any{"id": "r_legs", "name": "腿", "emoji": "figureStrength", "ex": []any{
				map[string]any{"id": "0043", "sets": 5, "reps": 5, "weight": 105, "mode": "reps"},
				map[string]any{"id": "c_sled", "sets": 3, "sec": 30, "weight": 100, "mode": "time"}}},
		},
		"week":       map[string]any{"1": "r_push", "3": []any{"r_pull"}, "5": "r_legs", "6": []any{"r_push", "r_pull"}},
		"dayPlan":    map[string]any{"2026-11-14": "rest", "2026-11-17": "r_legs"},
		"customEx":   []any{map[string]any{"id": "c_sled", "n": "雪橇推", "bp": "upper legs", "custom": true}},
		"exWeights":  map[string]any{"0025": map[string]any{"w": 85, "d": "2026-11-09"}},
		"bodyweight": bodyweight,
		"coach":      map[string]any{"messages": []any{"never returned to a token"}},
	}
}

// fakeCatalogue serves openGym's catalogue names (catalogue/i18n/zh.json's shape) with an ETag.
func fakeCatalogue(t *testing.T) (*httptest.Server, *int) {
	hits := 0
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		if r.Header.Get("If-None-Match") == `"cat-1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"cat-1"`)
		writeJSON(w, 200, map[string]any{
			"0025": map[string]any{"name": "杠铃卧推", "instructions": []any{"…"}},
			"0027": map[string]any{"name": "杠铃划船"},
			"0043": map[string]any{"name": "杠铃深蹲"},
			"0405": map[string]any{"name": "哑铃侧平举"},
			"0294": map[string]any{"name": "哑铃弯举"},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestServeFake serves the fake server with the synthetic profile on OPENGYM_FAKE_LISTEN (such as 127.0.0.1:18791)
// until the test is stopped, for running the extension on a test hub (the screenshots in the pull request). It is
// skipped otherwise. The token is fakeToken; the server's address is http://<listen>/gym.
func TestServeFake(t *testing.T) {
	addr := os.Getenv("OPENGYM_FAKE_LISTEN")
	if addr == "" {
		t.Skip("OPENGYM_FAKE_LISTEN is not set")
	}
	f := &fakeGym{t: t, state: sampleState(), rev: 41, wid: "w-device",
		scopes: map[string]string{"workouts": "apply", "plans": "apply", "bodyweight": "view", "measurements": "off", "notes": "off"}}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fake openGym on http://%s%s", addr, basePath)
	_ = http.Serve(ln, http.HandlerFunc(f.serve))
}
