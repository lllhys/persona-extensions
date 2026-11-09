package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

const connectorID = "readwiselibrary"
const pageUI = "highlights"

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
	if strings.TrimSpace(in.Secrets["access_token"]) == "" {
		return nil, nil
	}
	store, err := openWriteStore(in.Info().DataDir)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client, a.hub, a.receipts = newClient("https://readwise.io/api/v2", in.Secrets["access_token"]), hub, store
	return nil, nil
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
	out := map[string]any{"state": state, "account": "Readwise Library 划线知识库"}
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
