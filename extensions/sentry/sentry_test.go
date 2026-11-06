package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, newApp(t.Logf).program()))
}

func TestParseDSN(t *testing.T) {
	d, err := ParseDSN("https://abc123@o1.ingest.sentry.io/42")
	if err != nil || d.PublicKey != "abc123" || d.Host != "o1.ingest.sentry.io" || d.Project != "42" ||
		d.EnvelopeURL() != "https://o1.ingest.sentry.io/api/42/envelope/" || strings.Contains(d.String(), "abc123") {
		t.Fatalf("%+v %v", d, err)
	}
	d, err = ParseDSN("https://key@errors.example.com/sentry/7")
	if err != nil || d.EnvelopeURL() != "https://errors.example.com/sentry/api/7/envelope/" {
		t.Fatalf("a path before the project: %+v %v", d, err)
	}
	for _, bad := range []string{"", "http://k@o1.ingest.sentry.io/1", "https://o1.ingest.sentry.io/1", "https://k@o1.ingest.sentry.io/x"} {
		if _, err := ParseDSN(bad); err == nil {
			t.Errorf("taken: %q", bad)
		}
	}
}

func sample() wire.ErrorReport {
	return wire.ErrorReport{ID: "r1", Source: "android", Kind: "crash", Level: "fatal", Time: "2026-11-06T09:00:00Z",
		Release: "1.4.0", Build: "512", OS: "android", OSVersion: "16", DeviceModel: "Pixel 9", Message: "IllegalStateException: boom",
		Exceptions: []wire.ErrorException{
			{Type: "java.lang.RuntimeException", Value: "wrapped", Frames: []wire.ErrorFrame{{Function: "outer", Module: "persona.App", InApp: true}}},
			{Type: "java.lang.IllegalStateException", Value: "boom", Frames: []wire.ErrorFrame{{Function: "load", Module: "persona.Inbox", File: "Inbox.kt", Line: 88, InApp: true}}},
		},
		Breadcrumbs:    []wire.ErrorBreadcrumb{{Time: "2026-11-06T08:59:59Z", Category: "navigation", Message: "inbox"}},
		Tags:           map[string]string{"screen": "inbox", "source": "spoofed"},
		PreviousLaunch: true, Fingerprint: "fp1", Count: 3, ReceivedAt: "2026-11-06T09:01:00Z"}
}

func TestEvent(t *testing.T) {
	ev := Event(sample(), Options{Environment: "staging", Client: "persona-sentry/0.1.0"})
	raw, _ := json.Marshal(ev)
	var got struct {
		EventID     string            `json:"event_id"`
		Platform    string            `json:"platform"`
		Level       string            `json:"level"`
		Release     string            `json:"release"`
		Dist        string            `json:"dist"`
		Environment string            `json:"environment"`
		Fingerprint []string          `json:"fingerprint"`
		Tags        map[string]string `json:"tags"`
		Exception   struct {
			Values []struct {
				Type       string `json:"type"`
				Stacktrace struct {
					Frames []map[string]any `json:"frames"`
				} `json:"stacktrace"`
			} `json:"values"`
		} `json:"exception"`
		Breadcrumbs struct {
			Values []map[string]any `json:"values"`
		} `json:"breadcrumbs"`
		Contexts map[string]map[string]any `json:"contexts"`
		Extra    map[string]any            `json:"extra"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.EventID) != 32 || got.EventID != EventID(sample()) || got.Platform != "java" || got.Level != "fatal" ||
		got.Release != "persona-android@1.4.0+512" || got.Dist != "512" || got.Environment != "staging" || got.Fingerprint[0] != "fp1" {
		t.Fatalf("%s", raw)
	}
	if got.Tags["source"] != "android" || got.Tags["kind"] != "crash" || got.Tags["screen"] != "inbox" || got.Tags["previous_launch"] != "true" {
		t.Fatalf("tags: %v", got.Tags)
	}
	if len(got.Exception.Values) != 2 || got.Exception.Values[0].Type != "java.lang.IllegalStateException" ||
		got.Exception.Values[0].Stacktrace.Frames[0]["lineno"] != float64(88) {
		t.Fatalf("the innermost cause first: %s", raw)
	}
	if got.Breadcrumbs.Values[0]["timestamp"] == nil || got.Contexts["device"]["model"] != "Pixel 9" || got.Extra["count"] != float64(3) {
		t.Fatalf("%s", raw)
	}
	if strings.Contains(string(raw), "server_name") || strings.Contains(string(raw), `"user"`) {
		t.Fatalf("no host or user: %s", raw)
	}
}

func TestLimitedUntil(t *testing.T) {
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	resp := &http.Response{StatusCode: 200, Header: http.Header{"X-Sentry-Rate-Limits": {"60:transaction:key, 120:error;default:organization"}}}
	if u, ok := limitedUntil(resp, now); !ok || !u.Equal(now.Add(120*time.Second)) {
		t.Fatalf("%v %v", u, ok)
	}
	resp = &http.Response{StatusCode: 200, Header: http.Header{"X-Sentry-Rate-Limits": {"60:transaction:key"}}}
	if _, ok := limitedUntil(resp, now); ok {
		t.Fatal("transactions only")
	}
	resp = &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"30"}}}
	if u, ok := limitedUntil(resp, now); !ok || !u.Equal(now.Add(30*time.Second)) {
		t.Fatalf("%v %v", u, ok)
	}
}

// fakeSentry is a Sentry project's envelope endpoint: it keeps what it was sent.
type fakeSentry struct {
	mu        sync.Mutex
	envelopes [][]byte
	auth      []string
	status    int
}

func (f *fakeSentry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path != "/api/42/envelope/" || r.Header.Get("Content-Type") != "application/x-sentry-envelope" {
		w.WriteHeader(400)
		return
	}
	f.envelopes = append(f.envelopes, body)
	f.auth = append(f.auth, r.Header.Get("X-Sentry-Auth"))
	if f.status != 0 {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(f.status)
		return
	}
	_, _ = w.Write([]byte(`{"id":"x"}`))
}

func (f *fakeSentry) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.envelopes) }

func testApp(t *testing.T, srv *httptest.Server, cfg Config) *app {
	a := newApp(t.Logf)
	a.client = srv.Client()
	dsn, err := ParseDSN(strings.Replace(srv.URL, "https://", "https://pubkey@", 1) + "/42")
	if err != nil {
		t.Fatal(err)
	}
	a.configure(dsn, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go a.run(ctx)
	return a
}

func deliver(t *testing.T, a *app, reports ...wire.ErrorReport) {
	raw, _ := json.Marshal(wire.ErrorReportsDeliver{Contribution: "sentry", Reports: reports})
	if _, err := a.errorReports(context.Background(), "deliver", raw); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 200 {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestForwards(t *testing.T) {
	f := &fakeSentry{}
	srv := httptest.NewTLSServer(f)
	defer srv.Close()
	off := false
	a := testApp(t, srv, Config{Environment: "staging", IncludeLogLines: &off})
	logLine := wire.ErrorReport{ID: "l1", Source: "hub", Kind: "log", Level: "error", Time: "2026-11-06T09:00:00Z", Message: "x failed"}
	deliver(t, a, sample(), logLine)
	waitFor(t, func() bool { return f.count() == 1 })
	time.Sleep(50 * time.Millisecond)
	if f.count() != 1 {
		t.Fatal("log lines are off")
	}
	f.mu.Lock()
	env, auth := f.envelopes[0], f.auth[0]
	f.mu.Unlock()
	if !strings.Contains(auth, "sentry_key=pubkey") || !strings.Contains(auth, "sentry_version=7") {
		t.Fatalf("auth: %s", auth)
	}
	sc := bufio.NewScanner(bytes.NewReader(env))
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) != 3 || !strings.Contains(lines[0], EventID(sample())) || strings.Contains(lines[0], "pubkey") ||
		!strings.Contains(lines[1], `"type":"event"`) || !strings.Contains(lines[2], `"environment":"staging"`) {
		t.Fatalf("envelope: %q", lines)
	}
}

func TestSentryRateLimit(t *testing.T) {
	f := &fakeSentry{status: 429}
	srv := httptest.NewTLSServer(f)
	defer srv.Close()
	a := testApp(t, srv, Config{})
	r1, r2 := sample(), sample()
	r2.ID = "r2"
	deliver(t, a, r1)
	waitFor(t, func() bool { return f.count() == 1 })
	deliver(t, a, r2)
	waitFor(t, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.dropped == 2 })
	if f.count() != 1 {
		t.Fatal("waits out Retry-After without sending")
	}
}

func TestSampleRate(t *testing.T) {
	f := &fakeSentry{}
	srv := httptest.NewTLSServer(f)
	defer srv.Close()
	zero := 0.0
	a := testApp(t, srv, Config{SampleRate: &zero})
	deliver(t, a, sample())
	time.Sleep(50 * time.Millisecond)
	if f.count() != 0 {
		t.Fatal("sample rate 0 sends nothing")
	}
}
