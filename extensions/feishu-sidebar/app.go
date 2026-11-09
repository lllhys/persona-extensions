package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nwverse/persona/sdk/go/ext"
)

// Config is the extension's configuration (the manifest's form): the hub address the apps and Feishu reach, and the
// default same-site path the page opens with.
type Config struct {
	HubURL string `json:"hub_url,omitempty"`
	Path   string `json:"path,omitempty"`
}

// app is the running extension: the settings, and the hub address they resolve to.
type app struct {
	cfg    Config
	hubURL string // the hub's public address ("" until the user sets it)
}

func newApp() *app { return &app{} }

// init reads the settings. hub_url comes only from the configuration; there is no hub-provided fallback.
func (a *app) init(_ context.Context, in *ext.Init, _ *ext.Hub) (map[string]any, error) {
	var cfg Config
	if err := in.Config(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	a.cfg = cfg
	a.hubURL = strings.TrimSpace(cfg.HubURL)
	return nil, nil
}

// defaultPath is the path the page opens with: the setting, or the site root.
func (a *app) defaultPath() string {
	if p := strings.TrimSpace(a.cfg.Path); p != "" {
		return p
	}
	return "/"
}

// status answers connector.status from memory: the address is set or not, and whether it can be one.
func (a *app) status() map[string]any {
	if a.hubURL == "" {
		return map[string]any{"state": "not_configured", "error": "还没有 hub 的对外地址",
			"guide": []string{"在这个扩展的设置里填「hub 对外地址」（飞书内置浏览器能访问到的地址）"}}
	}
	if _, err := normalizeBase(a.hubURL); err != nil {
		return map[string]any{"state": "error", "error": err.Error(),
			"guide": []string{"在扩展设置里把「hub 对外地址」改成 http(s) 地址"}}
	}
	return map[string]any{"state": "ok", "account": "不用账号，不需要飞书应用"}
}

// connector answers the connector namespace: status and the assistant's tool.
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
		text, err := a.callTool(ctx, p.Name, p.Args)
		if err != nil {
			return nil, err
		}
		return map[string]any{"text": text}, nil
	}
	return nil, ext.UnknownMethod("connector." + method)
}
