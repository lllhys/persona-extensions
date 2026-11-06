// Command sentry is a Persona extension (docs/extensions/sentry.md) that forwards the error and crash reports of the
// hub and the user's devices to a Sentry project. The hub gives it reports already scrubbed, deduplicated and rate
// limited (contribution error_reports, error_reports.deliver); it turns each into a Sentry event and posts it to the
// project's envelope endpoint, with its own rate limit, sampling and Sentry's rate limits honored. Without a DSN the
// hub does not start it, and no report is collected anywhere.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"log"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

//go:embed persona-extension.json
var manifest []byte

const (
	id        = "sentry"
	queueMost = 200 // reports waiting to be sent; more are dropped
	rateMost  = 60  // events a minute at most
)

var version = func() string {
	var m struct{ Version string }
	_ = json.Unmarshal(manifest, &m)
	return m.Version
}()

// Config is the extension's configuration (the manifest's form).
type Config struct {
	Environment     string   `json:"environment,omitempty"`
	SampleRate      *float64 `json:"sample_rate,omitempty"`
	IncludeLogLines *bool    `json:"include_log_lines,omitempty"`
}

// app is the running extension.
type app struct {
	logf   func(string, ...any)
	client *http.Client
	now    func() time.Time
	rand   func() float64

	mu      sync.Mutex
	dsn     DSN
	opts    Options
	sample  float64
	logs    bool
	queue   chan wire.ErrorReport
	paused  time.Time // Sentry said to wait until then
	minute  time.Time
	inMin   int
	sent    int
	dropped int
	started bool
}

func newApp(logf func(string, ...any)) *app {
	return &app{logf: logf, client: &http.Client{Timeout: 20 * time.Second}, now: time.Now, rand: rand.Float64,
		queue: make(chan wire.ErrorReport, queueMost)}
}

func (a *app) program() ext.Program {
	return ext.Program{ID: id, Version: version, Manifest: manifest, Init: a.init,
		Namespaces: map[string]ext.Handler{"error_reports": a.errorReports}}
}

func (a *app) init(ctx context.Context, in *ext.Init, _ *ext.Hub) (map[string]any, error) {
	var cfg Config
	if err := in.Config(&cfg); err != nil {
		return nil, err
	}
	raw := in.Secrets["dsn"]
	if raw == "" { // the hub starts it only with its DSN; without one (a test of the protocol) it sends nothing
		a.logf("sentry: no DSN, nothing is sent")
		return nil, nil
	}
	dsn, err := ParseDSN(raw)
	if err != nil {
		return nil, err
	}
	a.configure(dsn, cfg)
	a.mu.Lock()
	start := !a.started
	a.started = true
	a.mu.Unlock()
	if start {
		go a.run(ctx)
	}
	a.logf("sentry: forwarding to %s", dsn)
	return nil, nil
}

func (a *app) configure(dsn DSN, cfg Config) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dsn = dsn
	a.opts = Options{Environment: cfg.Environment, Client: "persona-sentry/" + version}
	if a.opts.Environment == "" {
		a.opts.Environment = "production"
	}
	a.sample = 1
	if cfg.SampleRate != nil {
		a.sample = min(max(*cfg.SampleRate, 0), 1)
	}
	a.logs = cfg.IncludeLogLines == nil || *cfg.IncludeLogLines
}

// errorReports is the error_reports namespace: deliver (a notification) queues the reports it takes.
func (a *app) errorReports(_ context.Context, method string, params json.RawMessage) (any, error) {
	if method != "deliver" {
		return nil, ext.UnknownMethod(method)
	}
	var d wire.ErrorReportsDeliver
	if err := json.Unmarshal(params, &d); err != nil {
		return nil, err
	}
	for _, r := range d.Reports {
		if !a.takes(r) {
			continue
		}
		select {
		case a.queue <- r:
		default:
			a.mu.Lock()
			a.dropped++
			a.mu.Unlock()
		}
	}
	return nil, nil
}

// takes says whether the settings send r: log lines only when asked, then the sample rate.
func (a *app) takes(r wire.ErrorReport) bool {
	a.mu.Lock()
	logs, sample, ready := a.logs, a.sample, a.dsn.Host != ""
	a.mu.Unlock()
	if !ready {
		return false
	}
	if r.Kind == wire.ErrorKindLog && !logs {
		return false
	}
	return sample >= 1 || a.rand() < sample
}

// run sends the queue until ctx ends.
func (a *app) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-a.queue:
			a.send(ctx, r)
		}
	}
}

// send posts one report, after waiting out Sentry's limit; over this extension's own rate it is dropped.
func (a *app) send(ctx context.Context, r wire.ErrorReport) {
	a.mu.Lock()
	now := a.now()
	if now.Sub(a.minute) >= time.Minute {
		a.minute, a.inMin = now, 0
	}
	if a.inMin >= rateMost || now.Before(a.paused) {
		a.dropped++
		a.mu.Unlock()
		return
	}
	a.inMin++
	dsn, opts := a.dsn, a.opts
	a.mu.Unlock()

	ev := Event(r, opts)
	env, err := Envelope(dsn, ev)
	if err != nil {
		return
	}
	err = Send(ctx, a.client, dsn, opts.Client, env, a.now())
	a.mu.Lock()
	defer a.mu.Unlock()
	var limited errLimited
	switch {
	case errors.As(err, &limited):
		a.paused = limited.until
		a.dropped++
		// neutral words: the manager reports its extensions' log lines that read like errors
		a.logf("sentry: Sentry asks to wait until %s; reports until then are skipped", limited.until.Format(time.RFC3339))
	case err != nil:
		a.dropped++
		a.logf("sentry: event %s not sent (%v)", ev["event_id"], err)
	default:
		a.sent++
		a.logf("sentry: sent event %s (%s %s)", ev["event_id"], r.Source, r.Kind)
	}
}

func main() {
	log.SetFlags(0)
	a := newApp(log.Printf)
	ext.Main(a.program())
}
