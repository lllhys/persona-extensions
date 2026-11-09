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
	ProjectID   int64  `json:"project_id"`
	Subject     string `json:"subject"`
	Description string `json:"description,omitempty"`
}

func (in createArgs) validate() error {
	if in.ProjectID <= 0 {
		return errors.New("project ID must be positive")
	}
	if strings.TrimSpace(in.Subject) == "" || len([]rune(in.Subject)) > 500 || len([]rune(in.Description)) > 30000 {
		return errors.New("subject must be nonempty and at most 500 characters; description is at most 30000 characters")
	}
	for _, r := range in.Subject {
		if r == 0 || unicode.IsControl(r) {
			return errors.New("subject contains unsupported control characters")
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
	if name != "redmine_create_issue" {
		return nil, fmt.Errorf("unknown action %q", name)
	}
	var in createArgs
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	summary := fmt.Sprintf("Create Redmine issue in project %d\nSubject: %s", in.ProjectID, in.Subject)
	if in.Description != "" {
		summary += "\nDescription: " + in.Description
	}
	return map[string]any{"action": map[string]any{"name": name, "visible": "owner"}, "summary": summary}, nil
}

func (a *app) doAction(ctx context.Context, requestID, name string, raw json.RawMessage) (map[string]any, error) {
	if name != "redmine_create_issue" {
		return nil, fmt.Errorf("unknown action %q", name)
	}
	var in createArgs
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	if _, err := a.client.project(ctx, in.ProjectID); err != nil {
		return nil, err
	}
	keyData, _ := json.Marshal(raw)
	text, err := a.receipts.commit(requestID, string(keyData), func() (string, error) {
		item, err := a.client.createIssue(ctx, in.ProjectID, in.Subject, in.Description)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Created Redmine issue #%d: %s", item.ID, item.Subject), nil
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
