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
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	WorkflowStateID int64  `json:"workflow_state_id"`
	StoryType       string `json:"story_type,omitempty"`
	Deadline        string `json:"deadline,omitempty"`
}

type updateArgs struct {
	StoryID         int64 `json:"story_id"`
	WorkflowStateID int64 `json:"workflow_state_id"`
}

func validText(value string, limit int, multiline bool) error {
	if len([]rune(value)) > limit {
		return errors.New("story text exceeds the extension limit")
	}
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) && (!multiline || r != '\n' && r != '\t') {
			return errors.New("story text contains unsupported control characters")
		}
	}
	return nil
}

func (in createArgs) validate() error {
	if strings.TrimSpace(in.Name) == "" || validText(in.Name, 512, false) != nil || validText(in.Description, 10000, true) != nil {
		return errors.New("story title must be nonempty, one line and at most 512 characters; description is at most 10000 characters")
	}
	if in.WorkflowStateID <= 0 {
		return errors.New("workflow_state_id must be positive")
	}
	if in.StoryType != "" && in.StoryType != "feature" && in.StoryType != "bug" && in.StoryType != "chore" {
		return errors.New("story type must be feature, bug or chore")
	}
	return validDeadline(in.Deadline)
}

func (in updateArgs) validate() error {
	if in.StoryID <= 0 || in.WorkflowStateID <= 0 {
		return errors.New("story and workflow state IDs must be positive")
	}
	return nil
}

func describeAction(name string, raw json.RawMessage) (map[string]any, error) {
	var summary string
	switch name {
	case "shortcut_create_story":
		var in createArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		summary = "Create Shortcut story owned by you\nTitle: " + in.Name + fmt.Sprintf("\nWorkflow state ID: %d", in.WorkflowStateID)
		if in.Description != "" {
			summary += "\nDescription: " + in.Description
		}
	case "shortcut_update_story_state":
		var in updateArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		summary = fmt.Sprintf("Move Shortcut story #%d to workflow state %d", in.StoryID, in.WorkflowStateID)
	default:
		return nil, fmt.Errorf("unknown action %q", name)
	}
	return map[string]any{"action": map[string]any{"name": name, "visible": "owner"}, "summary": summary}, nil
}

func (a *app) doAction(ctx context.Context, requestID, name string, raw json.RawMessage) (map[string]any, error) {
	var fn func() (string, error)
	switch name {
	case "shortcut_create_story":
		var in createArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		me, err := a.client.member(ctx)
		if err != nil {
			return nil, err
		}
		if me.ID == "" {
			return nil, errors.New("Shortcut did not return the current member ID")
		}
		workflows, err := a.client.workflows(ctx)
		if err != nil {
			return nil, err
		}
		if !validState(workflows, 0, in.WorkflowStateID) {
			return nil, errors.New("workflow_state_id is not in the current workspace workflows; no story was created")
		}
		fn = func() (string, error) {
			item, err := a.client.createStory(ctx, in.Name, in.Description, in.StoryType, in.Deadline, in.WorkflowStateID, me.ID)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Created Shortcut story #%d: %s", item.ID, item.Name), nil
		}
	case "shortcut_update_story_state":
		var in updateArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		item, err := a.client.story(ctx, in.StoryID)
		if err != nil {
			return nil, err
		}
		workflows, err := a.client.workflows(ctx)
		if err != nil {
			return nil, err
		}
		if !validState(workflows, item.WorkflowID, in.WorkflowStateID) {
			return nil, errors.New("state must belong to the story's current workflow; no story was changed")
		}
		fn = func() (string, error) {
			if _, err := a.client.updateStoryState(ctx, in.StoryID, in.WorkflowStateID); err != nil {
				return "", err
			}
			return fmt.Sprintf("Moved Shortcut story #%d to state %d", in.StoryID, in.WorkflowStateID), nil
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

func validState(workflows []workflow, workflowID, stateID int64) bool {
	for _, workflow := range workflows {
		if workflowID != 0 && workflow.ID != workflowID {
			continue
		}
		for _, state := range workflow.States {
			if state.ID == stateID {
				return true
			}
		}
	}
	return false
}
