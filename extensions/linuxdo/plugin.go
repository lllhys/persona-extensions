package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit"
)

// Config is the plugin's configuration (the manifest's form): which list the page opens with and the user's interests.
type Config struct {
	List              string   `json:"list,omitempty"`
	Categories        []string `json:"categories,omitempty"`
	ExcludeCategories []string `json:"exclude_categories,omitempty"`
	Keywords          []string `json:"keywords,omitempty"`
	ExcludeKeywords   []string `json:"exclude_keywords,omitempty"`
	HideNSFW          *bool    `json:"hide_nsfw,omitempty"`
	HidePinned        *bool    `json:"hide_pinned,omitempty"`
	TZ                string   `json:"tz,omitempty"`
}

func (c Config) list() string {
	if _, _, ok := listOf(c.List); ok {
		return c.List
	}
	return "hot"
}

func (c Config) filter() filter {
	return filter{Categories: c.Categories, ExcludeCategories: c.ExcludeCategories, Keywords: c.Keywords,
		ExcludeKeywords: c.ExcludeKeywords, HideNSFW: c.HideNSFW == nil || *c.HideNSFW, HidePinned: c.HidePinned == nil || *c.HidePinned}
}

// plugin is the running plugin: the site, the settings, and how the last read went (the connector's status).
type plugin struct {
	logf func(string, ...any)
	cfg  Config
	loc  *time.Location
	site *site

	mu        sync.Mutex
	lastOK    time.Time
	lastErr   error
	lastErrAt time.Time
}

func newPlugin(logf func(string, ...any)) *plugin {
	return &plugin{logf: logf, loc: time.Local, site: newSite(siteBase)}
}

// configure reads initialize's config.
func (p *plugin) configure(ip pluginkit.InitParams) error {
	var cfg Config
	if len(ip.Config) > 0 && string(ip.Config) != "null" {
		if err := json.Unmarshal(ip.Config, &cfg); err != nil {
			return fmt.Errorf("config: %w", err)
		}
	}
	if cfg.TZ != "" {
		l, err := time.LoadLocation(cfg.TZ)
		if err != nil {
			return fmt.Errorf("时区 %q 不认识", cfg.TZ)
		}
		p.loc = l
	}
	p.cfg = cfg
	return nil
}

// read reads the site and keeps how it went for the status. A cached copy standing in for a failed read is
// handed to the caller, but the failure still counts for the status.
func (p *plugin) read(ctx context.Context, path string, fresh time.Duration) ([]byte, error) {
	body, err := p.site.get(ctx, path, fresh)
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case err == nil:
		p.lastOK, p.lastErr = time.Now().UTC(), nil
	case errors.Is(err, errNoTopic), errors.Is(err, context.Canceled):
		// about one topic, or the caller gave up: not the connector's state
	default:
		p.lastErr, p.lastErrAt = err, time.Now()
		p.logf("linuxdo: %s", clip(oneLine(err.Error()), 200))
	}
	if body != nil {
		return body, nil
	}
	return nil, err
}

// connectorStatus is connector.status's answer.
type connectorStatus struct {
	State    string     `json:"state"`
	Account  string     `json:"account,omitempty"`
	Error    string     `json:"error,omitempty"`
	Guide    []string   `json:"guide,omitempty"`
	LastSync *time.Time `json:"last_sync,omitempty"`
}

// cStatus answers from memory: ok until a read fails; the last failure stands until a read succeeds again.
func (p *plugin) cStatus(context.Context, *pluginkit.Hub, json.RawMessage) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := connectorStatus{State: "ok", Account: "公开内容，不用账号"}
	if !p.lastOK.IsZero() {
		at := p.lastOK
		st.LastSync = &at
	}
	if p.lastErr != nil && p.lastErrAt.After(p.lastOK) {
		st.Error = clip(oneLine(p.lastErr.Error()), 200)
		switch {
		case errors.Is(p.lastErr, errRateLimited):
			st.State = "rate_limited"
		case errors.Is(p.lastErr, errChallenged):
			st.State = "error"
			st.Guide = []string{"hub 这台机器的出口被 LINUX DO 的防火墙拦了：换一台能直接访问 linux.do 的机器跑 hub，或者等一会儿再试"}
		default:
			st.State = "error"
		}
	}
	return st, nil
}

func (p *plugin) handlers() map[string]pluginkit.Handler {
	h := map[string]pluginkit.Handler{
		"connector.status":     p.cStatus,
		"connector.tools.list": p.cToolsList,
		"connector.tools.call": p.cToolsCall,
		"ui.render":            p.uiRender,
		"ui.action":            p.uiAction,
	}
	for name, fn := range h {
		want := "linuxdo"
		if strings.HasPrefix(name, "ui.") {
			want = "hot"
		}
		h[name] = func(ctx context.Context, hub *pluginkit.Hub, params json.RawMessage) (any, error) {
			if c := pluginkit.Contribution(params); c != "" && c != want {
				return nil, pluginkit.Errorf(pluginkit.CodeNoContribution, "no contribution %s here", c)
			}
			return fn(ctx, hub, params)
		}
	}
	return h
}
