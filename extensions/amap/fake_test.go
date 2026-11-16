package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A fake AMap: it answers the recorded, scrubbed answers in testdata by path (public landmarks, fake phone numbers),
// records every request, and can be told to answer something else first.

// testKey is an obviously fake key of the real shape.
const testKey = "0123456789abcdef0123456789abcdef"

type fakeAMap struct {
	t   *testing.T
	srv *httptest.Server

	mu     sync.Mutex
	hits   []*url.URL
	queued map[string][]string // path → answers to give before the usual one
}

func newFake(t *testing.T) *fakeAMap {
	f := &fakeAMap{t: t, queued: map[string][]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const (
	noGeocodes = `{"status":"1","info":"OK","infocode":"10000","count":"0","geocodes":[]}`
	noPOIs     = `{"status":"1","info":"OK","infocode":"10000","count":"0","pois":[]}`
	// tiananmenPOI is a keyword search's answer for 天安门, at the point geocoding gives (geo-tiananmen.json).
	tiananmenPOI = `{"status":"1","info":"OK","infocode":"10000","count":"1","pois":[{"name":"天安门","location":"116.397755,39.903182",` +
		`"type":"风景名胜;风景名胜;国家级景点","pname":"北京市","cityname":"北京市","adname":"东城区","address":"东长安街","citycode":"010","adcode":"110101"}]}`
)

// refusal is AMap's answer with infocode code.
func refusal(code, info string) string {
	return `{"status":"0","info":"` + info + `","infocode":"` + code + `"}`
}

func (f *fakeAMap) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits = append(f.hits, r.URL)
	var body string
	if q := f.queued[r.URL.Path]; len(q) > 0 {
		body, f.queued[r.URL.Path] = q[0], q[1:]
	}
	f.mu.Unlock()
	if body == "" {
		body = f.usual(r)
	}
	if body == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	_, _ = w.Write([]byte(body))
}

// usual is the recorded answer for a request.
func (f *fakeAMap) usual(r *http.Request) string {
	q := r.URL.Query()
	switch r.URL.Path {
	case "/v3/geocode/geo":
		switch q.Get("address") {
		case "北京市天安门", "天安门", "北京市东城区天安门广场", "北京市东城区天安门广场1号":
			return fixture(f.t, "geo-tiananmen")
		case "北京南站", "杭州":
			coarse := fixture(f.t, "geo-coarse")
			if q.Get("address") == "杭州" {
				coarse = strings.NewReplacer(`"110000"`, `"330100"`, `"010"`, `"0571"`, "北京市", "杭州市").Replace(coarse)
			}
			return coarse
		}
		return noGeocodes
	case "/v3/geocode/regeo":
		return fixture(f.t, "regeo-square")
	case "/v5/place/text":
		switch q.Get("keywords") {
		case "北京南站":
			return fixture(f.t, "text-station")
		case "天安门":
			return tiananmenPOI
		}
		return noPOIs
	case "/v5/place/around":
		return fixture(f.t, "around-coffee")
	case "/v5/direction/driving":
		return fixture(f.t, "driving")
	case "/v5/direction/walking":
		return fixture(f.t, "walking")
	case "/v5/direction/bicycling", "/v5/direction/electrobike":
		return fixture(f.t, "cycling")
	case "/v5/direction/transit/integrated":
		return fixture(f.t, "transit")
	case "/v3/weather/weatherInfo":
		if q.Get("extensions") == "all" {
			return fixture(f.t, "weather-all")
		}
		return fixture(f.t, "weather-base")
	}
	return ""
}

func (f *fakeAMap) queue(path string, bodies ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queued[path] = append(f.queued[path], bodies...)
}

// count is how many requests reached path ("" for all).
func (f *fakeAMap) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, u := range f.hits {
		if path == "" || u.Path == path {
			n++
		}
	}
	return n
}

// last is the query of the latest request to path.
func (f *fakeAMap) last(path string) url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.hits) - 1; i >= 0; i-- {
		if f.hits[i].Path == path {
			return f.hits[i].Query()
		}
	}
	f.t.Fatalf("no request to %s", path)
	return nil
}

// clock is a settable time; sleeps advance it and are recorded.
type clock struct {
	mu     sync.Mutex
	t      time.Time
	sleeps []time.Duration
}

func newClock() *clock { return &clock{t: time.Date(2026, 11, 15, 10, 0, 0, 0, beijing)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.sleeps = append(c.sleeps, d)
	if d > 0 {
		c.t = c.t.Add(d)
	}
	c.mu.Unlock()
	return ctx.Err()
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// tuneFor points a client at the fake with the clock (no real waiting).
func tuneFor(f *fakeAMap, c *clock) func(*client) {
	return func(cl *client) {
		cl.base = f.srv.URL
		cl.now, cl.sleep = c.now, c.sleep
	}
}

// testApp is an app with testKey, its data in a temporary directory, talking to f.
func testApp(t *testing.T, f *fakeAMap) (*app, *clock) {
	t.Helper()
	c := newClock()
	a := &app{logf: t.Logf, tune: tuneFor(f, c)}
	a.client = newClient(testKey, t.TempDir(), t.Logf)
	a.tune(a.client)
	a.client.load() // by the test's clock
	return a, c
}

func call(t *testing.T, a *app, name, raw string) (string, error) {
	t.Helper()
	return a.callTool(context.Background(), name, []byte(raw))
}

func mustCall(t *testing.T, a *app, name, raw string) string {
	t.Helper()
	text, err := call(t, a, name, raw)
	if err != nil {
		t.Fatalf("%s %s: %v", name, raw, err)
	}
	return text
}

func wantIn(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(text, p) {
			t.Errorf("missing %q in:\n%s", p, text)
		}
	}
}
