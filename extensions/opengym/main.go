// Command opengym is a Persona extension (docs/extensions/opengym.md) for a self-hosted openGym server
// (https://opengym.ch), the iPhone and Apple Watch workout log with an AI coach:
//
//   - ui (nav_page "opengym"): this week's sessions and volume, the next planned session, the last twelve weeks'
//     volume, the recent workouts with their sets, body weight, and the plan for the next days.
//   - ui (overview_card "opengym-card"): this week against last week, and the next session.
//   - connectors (id opengym): the read tools opengym_summary, opengym_workouts, opengym_plan, opengym_bodyweight and
//     opengym_exercises, and the writes opengym_log_workout, opengym_create_routine and opengym_schedule_day, each
//     confirmed on the phone every time.
//
// It reads the profile through openGym's scoped API tokens (GET /api/token/data, the document limited to the
// token's scopes), checks the cheap revision route in the background, and reads the whole document again only when
// the revision moved. Writes replace the top-level keys they change over the revision they read (PUT
// /api/token/data with baseRev).
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit"
	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var authorManifest []byte

var manifest = pluginkit.PackagedManifest(authorManifest)

// about is the manifest's id and version.
var about = func() (m struct{ ID, Version string }) {
	_ = json.Unmarshal(manifest, &m)
	return m
}()

// The contributions' ids (persona-extension.json).
const (
	pageUI      = "opengym"
	cardUI      = "opengym-card"
	connectorID = "opengym"
	secretToken = "api_token"
)

const (
	defaultRefresh = 15
	refreshFloor   = 5
	refreshCeil    = 1440
	quickSync      = time.Minute // a page or a tool reads again only when the last check is older
)

// config is the extension's settings (the manifest's form).
type config struct {
	ServerURL      string `json:"server_url,omitempty"`
	RefreshMinutes int    `json:"refresh_minutes,omitempty"`
	ExerciseNames  bool   `json:"exercise_names"`
}

// notifier is what the program tells the hub: that a page changed.
type notifier interface {
	Invalidate(contribution string) error
}

// app is the running extension.
type app struct {
	logf       func(string, ...any)
	client     *http.Client
	now        func() time.Time
	zone       func() *time.Location
	getenv     func(string) string
	catalogURL string

	mu       sync.Mutex
	base     *url.URL // nil until a usable address is set
	token    string
	cfgErr   error
	names    bool
	every    time.Duration
	dataDir  string
	hub      notifier
	cancel   context.CancelFunc
	snap     *snapshot // the last document read, nil before the first
	checked  time.Time // the server last answered
	lastErr  error     // and the last failure, when it is newer
	failedAt time.Time

	syncMu  sync.Mutex // one sync at a time
	writeMu sync.Mutex // one write at a time

	catalog catalogNames
}

func newApp(logf func(string, ...any)) *app {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &app{logf: logf, client: newClient(), now: time.Now, zone: func() *time.Location { return time.Local },
		getenv: os.Getenv, catalogURL: defaultCatalog, every: defaultRefresh * time.Minute, names: true,
		cfgErr: errNoServer}
}

func main() {
	log.SetFlags(0)
	ext.Main(program(newApp(log.Printf)))
}

func program(a *app) ext.Program {
	return ext.Program{
		ID: about.ID, Version: about.Version, Manifest: manifest,
		Init: a.init,
		UI: map[string]ext.Page{
			pageUI: {Render: a.renderPage, Action: a.pageAction},
			cardUI: {Render: a.renderCard},
		},
		Namespaces: map[string]ext.Handler{"connector": a.connector},
	}
}

func (a *app) init(ctx context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	cfg := config{ExerciseNames: true}
	if err := in.Config(&cfg); err != nil {
		return nil, err
	}
	a.configure(cfg, in.Secrets[secretToken])
	loc := time.Local
	if in.Hub.Timezone != "" {
		if l, err := time.LoadLocation(in.Hub.Timezone); err == nil {
			loc = l
		}
	}
	a.mu.Lock()
	a.dataDir = in.Info().DataDir
	a.zone = func() *time.Location { return loc }
	if hub != nil {
		a.hub = hub
	}
	if a.cancel != nil {
		a.cancel()
	}
	loop, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	every, cfgErr := a.every, a.cfgErr
	a.mu.Unlock()
	if cfgErr != nil {
		a.logf("opengym: %v", cfgErr)
		return nil, nil
	}
	a.logf("opengym: server %s, checking every %s", a.serverName(), every)
	go a.poll(loop, every)
	return nil, nil
}

var (
	errNoServer = errors.New("还没有填 openGym 服务器地址")
	errNoToken  = errors.New("还没有填 API 令牌")
)

// configure applies the settings and the token; an unusable address or a missing token is kept as the reason
// nothing is read.
func (a *app) configure(cfg config, token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.base, a.cfgErr = nil, nil
	base, err := parseServer(cfg.ServerURL)
	switch {
	case cfg.ServerURL == "":
		a.cfgErr = errNoServer
	case err != nil:
		a.cfgErr = err
	case token == "":
		a.cfgErr = errNoToken
	default:
		a.base = base
	}
	a.token = token
	a.names = cfg.ExerciseNames
	m := cfg.RefreshMinutes
	if m == 0 {
		m = defaultRefresh
	}
	a.every = time.Duration(min(max(m, refreshFloor), refreshCeil)) * time.Minute
	a.snap, a.lastErr, a.checked = nil, nil, time.Time{}
}

// poll checks the server soon after start and then every period, and the exercise names once a day, until ctx ends.
func (a *app) poll(ctx context.Context, every time.Duration) {
	t := time.NewTimer(3 * time.Second) // let the hub finish starting first
	defer t.Stop()
	var namesAt time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if time.Since(namesAt) > 24*time.Hour {
			namesAt = time.Now()
			if err := a.loadNames(ctx); err != nil {
				a.logf("opengym: exercise names: %v", err)
			}
		}
		if err := a.sync(ctx, true); err != nil {
			a.logf("opengym: %v", err)
		}
		t.Reset(every)
	}
}

func (a *app) invalidate() {
	a.mu.Lock()
	hub := a.hub
	a.mu.Unlock()
	if hub != nil {
		_ = hub.Invalidate(pageUI)
		_ = hub.Invalidate(cardUI)
	}
}

// health is how the server is doing, for the page and the status.
type health struct {
	Server  string
	Config  error
	Checked time.Time
	Err     error
	Every   time.Duration
}

func (a *app) health() health {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := health{Config: a.cfgErr, Checked: a.checked, Every: a.every}
	if a.base != nil {
		h.Server = a.base.Host + a.base.Path
	}
	if a.lastErr != nil && !a.failedAt.Before(a.checked) {
		h.Err = a.lastErr
	}
	return h
}

func (a *app) serverName() string { return a.health().Server }
