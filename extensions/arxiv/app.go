package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

const connectorID = "arxiv"
const pageUI = "papers"

type app struct {
	mu      sync.RWMutex
	client  *client
	lastOK  time.Time
	lastErr error
}

func newApp() *app { return &app{client: newClient("https://export.arxiv.org")} }

func (a *app) init(_ context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
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
	out := map[string]any{"state": "ok", "account": "arXiv 公开研究论文"}
	if a.lastErr != nil {
		out["error"] = clipText(a.lastErr.Error(), 200)
		out["state"] = "error"
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
