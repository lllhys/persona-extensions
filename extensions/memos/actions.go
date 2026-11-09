package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type createArgs struct {
	Content string `json:"content"`
}

type updateArgs struct {
	MemoID  string `json:"memo_id"`
	Content string `json:"content"`
}

type pinArgs struct {
	MemoID string `json:"memo_id"`
	Pinned bool   `json:"pinned"`
}

func describeAction(name string, raw json.RawMessage) (map[string]any, error) {
	var summary string
	switch name {
	case "memos_create":
		var in createArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validContent(in.Content); err != nil {
			return nil, err
		}
		summary = "Create a private Memos note\n\n" + in.Content
	case "memos_update":
		var in updateArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validMemoID(in.MemoID); err != nil {
			return nil, err
		}
		if err := validContent(in.Content); err != nil {
			return nil, err
		}
		summary = "Replace the content of Memos note " + in.MemoID + "\n\n" + in.Content
	case "memos_toggle_pin":
		var in pinArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validMemoID(in.MemoID); err != nil {
			return nil, err
		}
		summary = fmt.Sprintf("Set pinned=%v for Memos note %s", in.Pinned, in.MemoID)
	default:
		return nil, fmt.Errorf("unknown action %q", name)
	}
	return map[string]any{"action": map[string]any{"name": name, "visible": "owner"}, "summary": summary}, nil
}

func (a *app) doAction(ctx context.Context, requestID, name string, raw json.RawMessage) (map[string]any, error) {
	var fn func() (string, error)
	switch name {
	case "memos_create":
		var in createArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validContent(in.Content); err != nil {
			return nil, err
		}
		fn = func() (string, error) {
			item, err := a.client.create(ctx, in.Content)
			if err != nil {
				return "", err
			}
			return "Created private Memos note " + item.Name, nil
		}
	case "memos_update":
		var in updateArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validMemoID(in.MemoID); err != nil {
			return nil, err
		}
		if err := validContent(in.Content); err != nil {
			return nil, err
		}
		if err := a.checkOwner(ctx, in.MemoID); err != nil {
			return nil, err
		}
		fn = func() (string, error) {
			if _, err := a.client.updateContent(ctx, in.MemoID, in.Content); err != nil {
				return "", err
			}
			return "Updated Memos note " + in.MemoID, nil
		}
	case "memos_toggle_pin":
		var in pinArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validMemoID(in.MemoID); err != nil {
			return nil, err
		}
		if err := a.checkOwner(ctx, in.MemoID); err != nil {
			return nil, err
		}
		fn = func() (string, error) {
			if _, err := a.client.updatePinned(ctx, in.MemoID, in.Pinned); err != nil {
				return "", err
			}
			return fmt.Sprintf("Set pinned=%v for Memos note %s", in.Pinned, in.MemoID), nil
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

func (a *app) checkOwner(ctx context.Context, id string) error {
	item, err := a.client.memo(ctx, id)
	if err != nil {
		return err
	}
	if item.Creator != a.creator {
		return errors.New("this memo does not belong to the configured Memos user; no change was made")
	}
	return nil
}
