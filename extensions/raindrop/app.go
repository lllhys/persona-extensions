package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

type app struct {
	mu        sync.RWMutex
	api       *apiClient
	defaultID int64
	hub       hubNotifier

	lastOK  time.Time
	lastErr error
	cols    []collection
	colsAt  time.Time

	receipts *saveStore
}

type hubNotifier interface{ Invalidate(string) error }

func newApp() *app {
	return &app{api: newAPIClient(defaultAPIBase, ""), receipts: newSaveStore("")}
}

func (a *app) init(_ context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	var cfg struct {
		DefaultCollection string `json:"default_collection"`
	}
	if err := in.Config(&cfg); err != nil {
		return nil, fmt.Errorf("configuration: %w", err)
	}
	id, err := strconv.ParseInt(cfg.DefaultCollection, 10, 64)
	if cfg.DefaultCollection != "" && (err != nil || id < -1) {
		return nil, errors.New("default_collection must be -1, 0 or a positive collection ID")
	}
	store, err := openSaveStore(in.Info().DataDir)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.api = newAPIClient(defaultAPIBase, strings.TrimSpace(in.Secrets["access_token"]))
	a.defaultID, a.hub, a.receipts = id, hub, store
	a.cols, a.colsAt = nil, time.Time{}
	return nil, nil
}

func (a *app) read[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	result, err := fn()
	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil {
		a.lastOK, a.lastErr = time.Now(), nil
	} else if !errors.Is(err, context.Canceled) {
		a.lastErr = err
	}
	return result, err
}

func (a *app) status() map[string]any {
	a.mu.RLock()
	defer a.mu.RUnlock()
	state := "ok"
	if a.api == nil || strings.TrimSpace(a.api.token) == "" {
		state = "not_configured"
	}
	result := map[string]any{"state": state, "account": "Raindrop 个人书签"}
	if a.lastErr != nil {
		result["error"] = clipText(a.lastErr.Error(), 200)
		if errors.Is(a.lastErr, errBadToken) {
			result["state"] = "not_signed_in"
		} else if errors.Is(a.lastErr, errRateLimited) {
			result["state"] = "rate_limited"
		} else if state == "ok" {
			result["state"] = "error"
		}
	}
	if !a.lastOK.IsZero() {
		result["last_sync"] = a.lastOK
	}
	return result
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
		return map[string]any{"tools": tools}, nil
	case "tools.call":
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		text, err := a.callTool(ctx, in.Name, in.Args)
		if err != nil {
			return nil, err
		}
		return map[string]any{"text": text}, nil
	case "actions.list":
		return map[string]any{"tools": []tool{saveTool}}, nil
	case "actions.describe":
		if in.Name != saveTool.Name {
			return nil, fmt.Errorf("unknown action %q", in.Name)
		}
		return a.describeSave(in.Args)
	case "actions.do":
		if in.Name != saveTool.Name {
			return nil, fmt.Errorf("unknown action %q", in.Name)
		}
		if len(in.RequestID) == 0 || len(in.RequestID) > 128 || strings.TrimSpace(in.RequestID) == "" {
			return nil, errors.New("actions.do needs a nonempty request_id of at most 128 characters")
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		item, err := a.saveOnce(ctx, in.RequestID, in.Args)
		if err != nil {
			return nil, err
		}
		return map[string]any{"text": "Saved to Raindrop:\n" + formatBookmark(item), "message_ref": strconv.FormatInt(item.ID, 10)}, nil
	}
	return nil, ext.UnknownMethod("connector." + method)
}

func clipText(s string, limit int) string {
	r := []rune(s)
	if len(r) > limit {
		return string(r[:max(0, limit-1)]) + "…"
	}
	return s
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func pageLink(id int64) string {
	return "https://app.raindrop.io/my/0/item/" + url.PathEscape(strconv.FormatInt(id, 10)) + "/preview"
}
