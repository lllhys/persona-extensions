package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func TestMain(m *testing.M) {
	refreshFloor = 0 // a refresh always reads, so tests can flip the fake's answer
	os.Exit(m.Run())
}

// set changes a package variable for one test.
func set[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// answer is what the fake site says for a path (status 0 is 200).
type answer struct {
	status int
	header map[string]string
	body   string
}

// fakeSite is a small Open-Meteo API: answers by path, counting the requests. The three endpoints differ by path, so
// the query is not part of the key.
type fakeSite struct {
	srv *httptest.Server

	mu      sync.Mutex
	answers map[string]answer
	all     *answer       // answer everything with this when set
	delay   time.Duration // before answering; a request that ends stops the wait
	hits    map[string]int
}

func newFakeSite(t *testing.T) *fakeSite {
	f := &fakeSite{answers: map[string]answer{}, hits: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		f.mu.Lock()
		f.hits[key]++
		a, ok := f.answers[key]
		if f.all != nil {
			a, ok = *f.all, true
		}
		delay := f.delay
		f.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if !ok {
			a = answer{status: http.StatusNotFound, body: `{"error":true}`}
		}
		for k, v := range a.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(cmp.Or(a.status, http.StatusOK))
		_, _ = w.Write([]byte(a.body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSite) put(path string, a answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[path] = a
}

func (f *fakeSite) serve(path, body string) { f.put(path, answer{body: body}) }

// answerAll answers every path with a (nil: each with its own again).
func (f *fakeSite) answerAll(a *answer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.all = a
}

func (f *fakeSite) setDelay(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delay = d
}

func (f *fakeSite) hit(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

// readFixture reads a real Open-Meteo answer captured under testdata/.
func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sample fills the site with the real answers: a forecast and air quality for the default city, and one geocoding hit.
func sample(t *testing.T, f *fakeSite) {
	f.serve("/v1/forecast", readFixture(t, "forecast.json"))
	f.serve("/v1/air-quality", readFixture(t, "air.json"))
	f.serve("/v1/search", readFixture(t, "geo.json"))
}

// testApp is the extension reading f, initialized with config (JSON; "" for none).
func testApp(t *testing.T, f *fakeSite, config string) *app {
	t.Helper()
	a := newApp()
	a.logf = t.Logf
	a.client = newClient()
	a.client.forecast, a.client.air, a.client.geo = f.srv.URL, f.srv.URL, f.srv.URL
	in := &ext.Init{}
	if config != "" {
		in.InitializeParams.Config = json.RawMessage(config)
	}
	if _, err := a.init(context.Background(), in, nil); err != nil {
		t.Fatal(err)
	}
	return a
}

func req(version int, st *pageState) *ext.UIRequest {
	r := &ext.UIRequest{}
	r.Contribution, r.Placement, r.Size, r.UIVersion, r.TZ = navUI, wire.PlacementNavPage, wire.SizeFull, version, "Asia/Shanghai"
	if st != nil {
		r.State = ext.EncodeState(*st)
	}
	return r
}

// checked holds v to what the apps draw: the SDK's check and exttest's.
func checked(t *testing.T, v *wire.View) *wire.View {
	t.Helper()
	if err := wire.CheckView(v); err != nil {
		t.Fatalf("CheckView: %v\n%s", err, texts(v))
	}
	raw, _ := json.Marshal(v)
	if err := exttest.CheckView(raw, false); err != nil {
		t.Fatalf("exttest.CheckView: %v\n%s", err, texts(v))
	}
	return v
}

func draw(t *testing.T, a *app, version int, st *pageState) *wire.View {
	t.Helper()
	v, err := a.render(context.Background(), req(version, st))
	if err != nil {
		t.Fatal(err)
	}
	return checked(t, v)
}

// tap is a tap on the action id of the page v (values: the page's).
func tap(t *testing.T, a *app, version int, v *wire.View, id string, values map[string]string) *wire.View {
	t.Helper()
	r := req(version, nil)
	r.State, r.Action, r.Values = v.State, id, values
	res, err := a.action(context.Background(), r)
	if err != nil || res == nil || res.View == nil {
		t.Fatalf("action %s: %+v %v", id, res, err)
	}
	return checked(t, res.View)
}

// texts is a view in lines for the tests to search: [type] its words, <choices>, (badge), {stat}, {series}, →action.
func texts(v *wire.View) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", v.Title)
	for _, s := range v.Sections {
		if s.Title != "" {
			fmt.Fprintf(&b, "## %s\n", s.Title)
		}
		for _, c := range s.Items {
			fmt.Fprintf(&b, "[%s]", c.Type)
			for _, part := range []string{c.Title, c.Subtitle, c.Text, c.Value, c.Label, c.Detail, c.URL} {
				if part != "" {
					b.WriteString(" " + part)
				}
			}
			for _, ch := range c.Choices {
				fmt.Fprintf(&b, " <%s:%s>", ch.Value, ch.Title)
			}
			if c.Badge != nil {
				fmt.Fprintf(&b, " (%s)", c.Badge.Text)
			}
			for _, st := range c.Stats {
				fmt.Fprintf(&b, " {%s=%s", st.Label, st.Value)
				if st.Detail != "" {
					fmt.Fprintf(&b, "|%s", st.Detail)
				}
				b.WriteString("}")
			}
			for _, se := range c.Series {
				fmt.Fprintf(&b, " {series:%s[%d]}", se.Name, len(se.Points))
			}
			if c.Action != nil {
				fmt.Fprintf(&b, " →%s", c.Action.ID)
			}
			b.WriteString("\n")
		}
		if s.Footer != "" {
			fmt.Fprintf(&b, "_%s_\n", s.Footer)
		}
	}
	return b.String()
}

func findRow(v *wire.View, title string) *wire.Component {
	for i := range v.Sections {
		for j := range v.Sections[i].Items {
			if c := &v.Sections[i].Items[j]; c.Type == wire.ComponentRow && c.Title == title {
				return c
			}
		}
	}
	return nil
}

// says fails unless the page has each of words.
func says(t *testing.T, v *wire.View, words ...string) {
	t.Helper()
	s := texts(v)
	for _, w := range words {
		if !strings.Contains(s, w) {
			t.Errorf("no %q in:\n%s", w, s)
		}
	}
}

// saysNot fails if the page has any of words.
func saysNot(t *testing.T, v *wire.View, words ...string) {
	t.Helper()
	s := texts(v)
	for _, w := range words {
		if strings.Contains(s, w) {
			t.Errorf("%q in:\n%s", w, s)
		}
	}
}

// stateOf reads the page state out of a view.
func stateOf(t *testing.T, raw string) pageState {
	t.Helper()
	r := &ext.UIRequest{}
	r.State = raw
	var st pageState
	if err := r.DecodeState(&st); err != nil {
		t.Fatal(err)
	}
	return st
}
