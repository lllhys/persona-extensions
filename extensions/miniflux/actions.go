package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type entryArgs struct {
	EntryID int64 `json:"entry_id"`
}

func (in entryArgs) validate() error { return entryIDValid(in.EntryID) }

func describeAction(name string, raw json.RawMessage) (map[string]any, error) {
	var in entryArgs
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	var summary string
	switch name {
	case "miniflux_mark_read":
		summary = fmt.Sprintf("Mark Miniflux entry #%d read", in.EntryID)
	case "miniflux_toggle_bookmark":
		summary = fmt.Sprintf("Toggle the bookmark state of Miniflux entry #%d", in.EntryID)
	default:
		return nil, errors.New("unknown action " + name)
	}
	return map[string]any{"action": map[string]any{"name": name, "visible": "owner"}, "summary": summary}, nil
}

func (a *app) doAction(ctx context.Context, requestID, name string, raw json.RawMessage) (map[string]any, error) {
	var in entryArgs
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	var fn func() (string, error)
	switch name {
	case "miniflux_mark_read":
		fn = func() (string, error) {
			if err := a.client.markRead(ctx, in.EntryID); err != nil {
				return "", err
			}
			return fmt.Sprintf("Marked entry #%d read", in.EntryID), nil
		}
	case "miniflux_toggle_bookmark":
		fn = func() (string, error) {
			if err := a.client.toggleBookmark(ctx, in.EntryID); err != nil {
				return "", err
			}
			return fmt.Sprintf("Toggled bookmark for entry #%d", in.EntryID), nil
		}
	default:
		return nil, fmt.Errorf("unknown action %q", name)
	}
	keyData, _ := json.Marshal(raw)
	text, err := a.receipts.commit(requestID, string(keyData), fn)
	if err != nil {
		return nil, err
	}
	a.record(nil)
	if a.hub != nil {
		_ = a.hub.Invalidate(pageUI)
	}
	return map[string]any{"text": text}, nil
}
