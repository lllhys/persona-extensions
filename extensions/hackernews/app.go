package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

// toolBudget is how long one tool call may read: the hub does not wait for long either.
var toolBudget = 25 * time.Second

// Config is the extension's configuration (the manifest's form): which list the page opens with and the user's interests.
type Config struct {
	List            string   `json:"list,omitempty"`
	Keywords        []string `json:"keywords,omitempty"`
	ExcludeKeywords []string `json:"exclude_keywords,omitempty"`
	MinScore        int      `json:"min_score,omitempty"`
}

func (c Config) filter() filter {
	return filter{Keywords: c.Keywords, ExcludeKeywords: c.ExcludeKeywords, MinScore: c.MinScore}
}

// app is the running extension: the site, the settings, and how the last read went (the connector's status).
type app struct {
	logf func(string, ...any)
	cfg  Config
	site *site

	mu        sync.Mutex
	lastOK    time.Time
	lastErr   error
	lastErrAt time.Time
}

func newApp() *app { return &app{logf: log.Printf, cfg: Config{List: "top"}, site: newSite(siteBase)} }

func (a *app) init(_ context.Context, in *ext.Init, _ *ext.Hub) (map[string]any, error) {
	var cfg Config
	if err := in.Config(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if _, ok := listEndpoint(cfg.List); !ok {
		cfg.List = "top"
	}
	cfg.MinScore = max(cfg.MinScore, 0)
	a.cfg = cfg
	return nil, nil
}

// read reads the site and keeps how it went for the status. An older copy standing in is a failed read.
func (a *app) read(ctx context.Context, path string, fresh time.Duration) (reading, error) {
	rd, err := a.site.get(ctx, path, fresh)
	failure := err
	if failure == nil {
		failure = rd.stale
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case failure == nil:
		if rd.live {
			a.lastOK = rd.at.UTC()
		}
	case errors.Is(failure, errNotFound), errors.Is(failure, context.Canceled):
		// about one item, or the caller gave up: not the connector's state
	default:
		a.lastErr, a.lastErrAt = failure, time.Now()
		a.logf("hackernews: %s", clip(oneLine(failure.Error()), 200))
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
	st := connectorStatus{State: "ok", Account: "公开内容，不用账号"}
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
