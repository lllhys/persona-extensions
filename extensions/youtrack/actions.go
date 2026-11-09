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
	ProjectID   string `json:"project_id"`
	Summary     string `json:"summary"`
	Description string `json:"description,omitempty"`
}

func (in createArgs) validate() error {
	if err := validResourceID(in.ProjectID); err != nil {
		return err
	}
	if strings.TrimSpace(in.Summary) == "" || len([]rune(in.Summary)) > 500 || len([]rune(in.Description)) > 30000 {
		return errors.New("summary must be nonempty and at most 500 characters; description is at most 30000 characters")
	}
	for _, r := range in.Summary {
		if r == 0 || unicode.IsControl(r) {
			return errors.New("summary contains unsupported control characters")
		}
	}
	for _, r := range in.Description {
		if r == 0 || unicode.IsControl(r) && r != '\n' && r != '\t' {
			return errors.New("description contains unsupported control characters")
		}
	}
	return nil
}

func describeAction(name string, raw json.RawMessage) (map[string]any, error) {
	if name != "youtrack_create_issue" {
		return nil, fmt.Errorf("unknown action %q", name)
	}
	var in createArgs
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	summary := "Create YouTrack issue in project " + in.ProjectID + "\nSummary: " + in.Summary
	if in.Description != "" {
		summary += "\nDescription: " + in.Description
	}
	return map[string]any{"action": map[string]any{"name": name, "visible": "owner"}, "summary": summary}, nil
}

func (a *app) doAction(ctx context.Context, requestID, name string, raw json.RawMessage) (map[string]any, error) {
	if name != "youtrack_create_issue" {
		return nil, fmt.Errorf("unknown action %q", name)
	}
	var in createArgs
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	project, err := a.client.project(ctx, in.ProjectID)
	if err != nil {
		return nil, err
	}
	if project.Archived {
		return nil, errors.New("cannot create an issue in an archived YouTrack project")
	}
	keyData, _ := json.Marshal(raw)
	text, err := a.receipts.commit(requestID, string(keyData), func() (string, error) {
		item, err := a.client.createIssue(ctx, in.ProjectID, in.Summary, in.Description)
		if err != nil {
			return "", err
		}
		return "Created YouTrack issue " + item.IDReadable + ": " + item.Summary, nil
	})
	if err != nil {
		return nil, err
	}
	a.record(nil)
	if a.hub != nil {
		_ = a.hub.Invalidate(pageUI)
	}
	return map[string]any{"text": text}, nil
}
