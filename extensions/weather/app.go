package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/ext"
)

// citiesMost is how many cities the page and the tools read together (the first is the default).
const citiesMost = 8

// toolBudget is how long one tool call may read; a slow API gets an answer of what was read rather than none.
var toolBudget = 25 * time.Second

// renderBudget is how long a page may read: an app waits for its page through the hub.
var renderBudget = 20 * time.Second

// app is the running extension: the client, the settings, and how the last read went (the connector's status).
type app struct {
	logf      func(string, ...any)
	cfg       Config
	cities    []City
	badCities []City
	client    *client

	mu        sync.Mutex
	lastOK    time.Time
	lastErr   error
	lastErrAt time.Time
}

func newApp() *app {
	return &app{logf: log.Printf, client: newClient()}
}

func (a *app) init(_ context.Context, in *ext.Init, _ *ext.Hub) (map[string]any, error) {
	var cfg Config
	if err := in.Config(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	a.cfg = cfg
	a.cities, a.badCities = cleanCities(cfg.Cities)
	return nil, nil
}

// freshness is how old an answer may be: a refresh asks for a new one, but not within refreshFloor of the last.
func freshness(refresh bool) time.Duration {
	if refresh {
		return refreshFloor
	}
	return cacheFor
}

// read reads one endpoint and keeps how it went for the status.
func (a *app) read(ctx context.Context, base, path string, fresh time.Duration) (reading, error) {
	rd, err := a.client.get(ctx, base, path, fresh)
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case err == nil:
		a.lastOK = rd.at.UTC()
	case errors.Is(err, errNoCity), errors.Is(err, context.Canceled):
		// about one city, or the caller gave up: not the connector's state
	default:
		a.lastErr, a.lastErrAt = err, time.Now()
		a.logf("weather: %s", clip(oneLine(err.Error()), 200))
	}
	return rd, err
}

// connectorStatus is connector.status's answer.
type connectorStatus struct {
	State    string     `json:"state"`
	Account  string     `json:"account,omitempty"`
	Error    string     `json:"error,omitempty"`
	LastSync *time.Time `json:"last_sync,omitempty"`
}

// status answers from memory: ok until a read fails; the last failure stands until a read succeeds again.
func (a *app) status() connectorStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := connectorStatus{State: "ok", Account: "公开数据，不用账号"}
	if !a.lastOK.IsZero() {
		at := a.lastOK
		st.LastSync = &at
	}
	if a.lastErr != nil && a.lastErrAt.After(a.lastOK) {
		st.State, st.Error = "error", clip(oneLine(a.lastErr.Error()), 200)
	}
	return st
}

// connector answers the connector namespace.
func (a *app) connector(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var p struct {
		Contribution string          `json:"contribution"`
		Name         string          `json:"name"`
		Args         json.RawMessage `json:"args"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &ext.Error{Code: "invalid", Message: err.Error()}
		}
	}
	if p.Contribution != "" && p.Contribution != connectorID {
		return nil, ext.NoContribution(p.Contribution)
	}
	switch method {
	case "status":
		return a.status(), nil
	case "tools.list":
		return map[string]any{"tools": tools}, nil
	case "tools.call":
		ctx, cancel := context.WithTimeout(ctx, toolBudget)
		defer cancel()
		text, err := a.callTool(ctx, p.Name, p.Args)
		if err != nil {
			return nil, err
		}
		return map[string]any{"text": text}, nil
	}
	return nil, ext.UnknownMethod("connector." + method)
}

// locFor is the time zone to write "读于" in: the city's own, else the hub's local.
func locFor(c City) *time.Location {
	if c.Timezone != "" {
		if l, err := time.LoadLocation(c.Timezone); err == nil {
			return l
		}
	}
	return time.Local
}

// windDir names a wind direction in degrees (0 is north) as a compass point.
func windDir(deg float64) string {
	names := [...]string{"北风", "东北风", "东风", "东南风", "南风", "西南风", "西风", "西北风"}
	idx := int((deg+22.5)/45) % 8
	return names[idx]
}

func nonEmpty(xs []string) []string {
	var out []string
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func oneLine(s string) string { return strings.TrimSpace(strings.Join(strings.Fields(s), " ")) }

// clip cuts s to at most n characters, marking the cut.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n])) + "…"
}
