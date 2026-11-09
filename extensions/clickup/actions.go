package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

type createArgs struct {
	ListID      string `json:"list_id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type statusArgs struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

func validText(value string, limit int, multiline bool) error {
	if len([]rune(value)) > limit {
		return errors.New("task text exceeds the extension limit")
	}
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) && (!multiline || r != '\n' && r != '\t') {
			return errors.New("task text contains unsupported control characters")
		}
	}
	return nil
}

func (in createArgs) validate() error {
	if err := validID(in.ListID); err != nil {
		return err
	}
	if strings.TrimSpace(in.Name) == "" || validText(in.Name, 500, false) != nil || validText(in.Description, 30000, true) != nil {
		return errors.New("task name must be nonempty, one line and at most 500 characters; description is at most 30000 characters")
	}
	return nil
}

func (in statusArgs) validate() error {
	if err := validID(in.TaskID); err != nil {
		return err
	}
	if strings.TrimSpace(in.Status) == "" || validText(in.Status, 100, false) != nil {
		return errors.New("status must be nonempty, one line and at most 100 characters")
	}
	return nil
}

func describeAction(name string, raw json.RawMessage) (map[string]any, error) {
	var summary string
	switch name {
	case "clickup_create_task":
		var in createArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		summary = "Create ClickUp task in list " + in.ListID + "\nTitle: " + in.Name
		if in.Description != "" {
			summary += "\nDescription: " + in.Description
		}
	case "clickup_update_status":
		var in statusArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		summary = "Update ClickUp task " + in.TaskID + " to status " + in.Status
	default:
		return nil, fmt.Errorf("unknown action %q", name)
	}
	return map[string]any{"action": map[string]any{"name": name, "visible": "owner"}, "summary": summary}, nil
}

func (a *app) doAction(ctx context.Context, requestID, name string, raw json.RawMessage) (map[string]any, error) {
	var fn func() (string, error)
	switch name {
	case "clickup_create_task":
		var in createArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		me, err := a.client.user(ctx)
		if err != nil {
			return nil, err
		}
		list, err := a.client.list(ctx, in.ListID)
		if err != nil {
			return nil, err
		}
		if list.Archived {
			return nil, errors.New("cannot create a task in an archived ClickUp list")
		}
		fn = func() (string, error) {
			item, err := a.client.createTask(ctx, in.ListID, me.ID, in.Name, in.Description)
			if err != nil {
				return "", err
			}
			return "Created ClickUp task " + item.ID + ": " + item.Name, nil
		}
	case "clickup_update_status":
		var in statusArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		fn = func() (string, error) {
			if _, err := a.client.updateStatus(ctx, in.TaskID, in.Status); err != nil {
				return "", err
			}
			return "Updated ClickUp task " + in.TaskID + " to status " + in.Status, nil
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
