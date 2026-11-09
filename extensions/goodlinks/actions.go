package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type readArgs struct {
	LinkID string `json:"link_id"`
}

type starArgs struct {
	LinkID  string `json:"link_id"`
	Starred bool   `json:"starred"`
}

func describeAction(name string, raw json.RawMessage) (map[string]any, error) {
	var summary string
	switch name {
	case "goodlinks_mark_read":
		var in readArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validLinkID(in.LinkID); err != nil {
			return nil, err
		}
		summary = "Mark GoodLinks link " + in.LinkID + " read"
	case "goodlinks_toggle_star":
		var in starArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validLinkID(in.LinkID); err != nil {
			return nil, err
		}
		summary = fmt.Sprintf("Set starred=%v for GoodLinks link %s", in.Starred, in.LinkID)
	default:
		return nil, fmt.Errorf("unknown action %q", name)
	}
	return map[string]any{"action": map[string]any{"name": name, "visible": "owner"}, "summary": summary}, nil
}

func (a *app) doAction(ctx context.Context, requestID, name string, raw json.RawMessage) (map[string]any, error) {
	var fn func() (string, error)
	switch name {
	case "goodlinks_mark_read":
		var in readArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validLinkID(in.LinkID); err != nil {
			return nil, err
		}
		fn = func() (string, error) {
			if _, err := a.client.update(ctx, in.LinkID, map[string]any{"read": true}); err != nil {
				return "", err
			}
			return "Marked GoodLinks link read: " + in.LinkID, nil
		}
	case "goodlinks_toggle_star":
		var in starArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validLinkID(in.LinkID); err != nil {
			return nil, err
		}
		fn = func() (string, error) {
			if _, err := a.client.update(ctx, in.LinkID, map[string]any{"starred": in.Starred}); err != nil {
				return "", err
			}
			return fmt.Sprintf("Set starred=%v for GoodLinks link %s", in.Starred, in.LinkID), nil
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

func requirePositiveOffset(offset int) error {
	if offset < 0 {
		return errors.New("offset must not be negative")
	}
	return nil
}
