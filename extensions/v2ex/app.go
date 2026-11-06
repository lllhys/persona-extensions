package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

// nodesMost is how many nodes the page and the tools read together (the manifest's maxItems): each is a request.
const nodesMost = 5

// toolBudget is how long one tool call may read: the hub does not wait for long either, so a slow site gets an answer
// of what was read rather than none.
var toolBudget = 25 * time.Second

// nodeNameRE is what a node's name looks like in its link: it goes into a request only after this.
var nodeNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Config is the extension's configuration (the manifest's form): which list the page opens with and the user's interests.
type Config struct {
	List            string   `json:"list,omitempty"`
	Nodes           []string `json:"nodes,omitempty"`
	ExcludeNodes    []string `json:"exclude_nodes,omitempty"`
	Keywords        []string `json:"keywords,omitempty"`
	ExcludeKeywords []string `json:"exclude_keywords,omitempty"`
	TZ              string   `json:"tz,omitempty"`
}

func (c Config) filter() filter {
	return filter{ExcludeNodes: c.ExcludeNodes, Keywords: c.Keywords, ExcludeKeywords: c.ExcludeKeywords}
}

// cleanNodes splits the nodes setting into the names to read (at most nodesMost, once each) and those that cannot be names.
func cleanNodes(names []string) (good, bad []string) {
	for _, name := range nonEmpty(names) {
		switch {
		case !nodeNameRE.MatchString(name):
			bad = append(bad, name)
		case len(good) < nodesMost && !slices.Contains(good, name):
			good = append(good, name)
		}
	}
	return good, bad
}

// app is the running extension: the site, the settings, and how the last read went (the connector's status).
type app struct {
	logf     func(string, ...any)
	cfg      Config
	nodes    []string
	badNodes []string
	loc      *time.Location
	site     *site

	mu        sync.Mutex
	lastOK    time.Time
	lastErr   error
	lastErrAt time.Time
}

func newApp() *app {
	return &app{logf: log.Printf, loc: time.Local, site: newSite(siteBase)}
}

func (a *app) init(_ context.Context, in *ext.Init, _ *ext.Hub) (map[string]any, error) {
	var cfg Config
	if err := in.Config(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if cfg.TZ != "" {
		loc, err := time.LoadLocation(cfg.TZ)
		if err != nil {
			return nil, fmt.Errorf("时区 %q 不认识", cfg.TZ)
		}
		a.loc = loc
	}
	a.cfg = cfg
	a.nodes, a.badNodes = cleanNodes(cfg.Nodes)
	return nil, nil
}

// read reads the site and keeps how it went for the status. An older copy standing in is a failed read, whatever
// the caller gets to show.
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
		// about one node or topic, or the caller gave up: not the connector's state
	default:
		a.lastErr, a.lastErrAt = failure, time.Now()
		a.logf("v2ex: %s", clip(oneLine(failure.Error()), 200))
	}
	return rd, err
}

// connectorStatus is connector.status's answer.
type connectorStatus struct {
	State    string     `json:"state"`
	Account  string     `json:"account,omitempty"`
	Error    string     `json:"error,omitempty"`
	Guide    []string   `json:"guide,omitempty"`
	LastSync *time.Time `json:"last_sync,omitempty"`
}

// status answers from memory: ok until a read fails; the last failure stands until a read succeeds again, and a
// pause is rate_limited for as long as it lasts, even with old copies being shown.
func (a *app) status() connectorStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := connectorStatus{State: "ok", Account: "公开内容，不用账号"}
	if !a.lastOK.IsZero() {
		at := a.lastOK
		st.LastSync = &at
	}
	switch failed := a.lastErr != nil && a.lastErrAt.After(a.lastOK); {
	case a.site.isPaused():
		st.State, st.Error = "rate_limited", errRateLimited.Error()
	case failed && !errors.Is(a.lastErr, errRateLimited):
		st.State, st.Error = "error", clip(oneLine(a.lastErr.Error()), 200)
		if errors.Is(a.lastErr, errChallenged) {
			st.Guide = []string{"hub 这台机器的出口被 V2EX 的防火墙拦了：换一台能直接访问 www.v2ex.com 的机器跑 hub，或者等一会儿再试"}
		}
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

func nonEmpty(xs []string) []string {
	var out []string
	for _, x := range xs {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}
