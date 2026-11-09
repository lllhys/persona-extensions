package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

const connectorID = "goodlinks"
const pageUI = "reading"

type app struct {
	mu       sync.RWMutex
	client   *client
	hub      hubNotifier
	receipts *writeStore
	lastOK   time.Time
	lastErr  error
}

type hubNotifier interface{ Invalidate(string) error }

func newApp() *app { return &app{client: newClient("", ""), receipts: newWriteStore("")} }

func (a *app) init(_ context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	var cfg struct {
		InstanceURL string `json:"instance_url"`
	}
	if err := in.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg.InstanceURL == "" {
		cfg.InstanceURL = "http://127.0.0.1:9428"
	}
	if strings.TrimSpace(in.Secrets["api_token"]) == "" {
		return nil, nil
	}
	base, err := instanceURL(cfg.InstanceURL)
	if err != nil {
		return nil, err
	}
	store, err := openWriteStore(in.Info().DataDir)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client, a.hub, a.receipts = newClient(base, in.Secrets["api_token"]), hub, store
	return nil, nil
}

func instanceURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("GoodLinks API URL must be a clean loopback address")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("GoodLinks API must use HTTP or HTTPS")
	}
	if u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1" {
		return "", errors.New("GoodLinks API is local-only; configure a loopback address")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func (a *app) record(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil {
		a.lastOK, a.lastErr = time.Now(), nil
	} else {
		a.lastErr = err
	}
}

func (a *app) status() map[string]any {
	a.mu.RLock()
	defer a.mu.RUnlock()
	state := "ok"
	if a.client.token == "" {
		state = "not_configured"
	}
	out := map[string]any{"state": state, "account": "GoodLinks 本地阅读库"}
	if a.lastErr != nil {
		out["error"] = clipText(a.lastErr.Error(), 200)
		switch {
		case errors.Is(a.lastErr, errToken):
			out["state"] = "not_signed_in"
		case errors.Is(a.lastErr, errRateLimited):
			out["state"] = "rate_limited"
		case state == "ok":
			out["state"] = "error"
		}
	}
	if !a.lastOK.IsZero() {
		out["last_sync"] = a.lastOK
	}
	return out
}

func (a *app) connector(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var in struct {
		Contribution string          `json:"contribution"`
		Name         string          `json:"name"`
		Args         json.RawMessage `json:"args"`
		RequestID    string          `json:"request_id"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, &ext.Error{Code: "invalid", Message: "invalid connector parameters"}
		}
	}
	if in.Contribution != "" && in.Contribution != connectorID {
		return nil, ext.NoContribution(in.Contribution)
	}
	switch method {
	case "status":
		return a.status(), nil
	case "tools.list":
		return map[string]any{"tools": readTools}, nil
	case "tools.call":
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		text, err := a.callTool(ctx, in.Name, in.Args)
		if err != nil {
			return nil, err
		}
		return map[string]any{"text": text}, nil
	case "actions.list":
		return map[string]any{"tools": actionTools}, nil
	case "actions.describe":
		return describeAction(in.Name, in.Args)
	case "actions.do":
		if len(in.RequestID) == 0 || len(in.RequestID) > 128 || strings.TrimSpace(in.RequestID) == "" {
			return nil, errors.New("actions.do needs a nonempty request_id of at most 128 characters")
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return a.doAction(ctx, in.RequestID, in.Name, in.Args)
	}
	return nil, ext.UnknownMethod("connector." + method)
}

func main() {
	log.SetFlags(0)
	ext.Main(program(newApp()))
}

var about = func() (out struct{ ID, Version string }) {
	if err := json.Unmarshal(manifest, &out); err != nil {
		panic(err)
	}
	return out
}()

func program(a *app) ext.Program {
	return ext.Program{ID: about.ID, Version: about.Version, Manifest: manifest, Init: a.init,
		UI:         map[string]ext.Page{pageUI: {Render: a.render, Action: a.action}},
		Namespaces: map[string]ext.Handler{"connector": a.connector}}
}
