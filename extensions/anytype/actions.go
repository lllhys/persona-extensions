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
	SpaceID string `json:"space_id"`
	TypeKey string `json:"type_key"`
	Name    string `json:"name"`
	Body    string `json:"body"`
}

func (in createArgs) validate() error {
	if err := validObjectID(in.SpaceID); err != nil {
		return err
	}
	if in.TypeKey != "page" && in.TypeKey != "note" {
		return errors.New("type_key must be page or note")
	}
	if strings.TrimSpace(in.Name) == "" || len([]rune(in.Name)) > 255 {
		return errors.New("object name must be nonempty and at most 255 characters")
	}
	for _, r := range in.Name {
		if r == 0 || unicode.IsControl(r) {
			return errors.New("object name contains unsupported control characters")
		}
	}
	if strings.TrimSpace(in.Body) == "" || len([]rune(in.Body)) > 30000 {
		return errors.New("object body must be nonempty and at most 30000 characters")
	}
	for _, r := range in.Body {
		if r == 0 || unicode.IsControl(r) && r != '\n' && r != '\t' {
			return errors.New("object body contains unsupported control characters")
		}
	}
	return nil
}

func describeAction(name string, raw json.RawMessage) (map[string]any, error) {
	if name != "anytype_create_object" {
		return nil, fmt.Errorf("unknown action %q", name)
	}
	var in createArgs
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	return map[string]any{"action": map[string]any{"name": name, "visible": "owner"}, "summary": "Create Anytype object in space " + in.SpaceID + "\nTitle: " + in.Name + "\n\n" + in.Body}, nil
}

func (a *app) doAction(ctx context.Context, requestID, name string, raw json.RawMessage) (map[string]any, error) {
	if name != "anytype_create_object" {
		return nil, fmt.Errorf("unknown action %q", name)
	}
	var in createArgs
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	found := false
	for offset := 0; offset < 1000; {
		page, err := a.client.types(ctx, in.SpaceID, offset)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Data {
			if item.TypeKey == in.TypeKey && (item.Layout == "basic" || item.Layout == "note") {
				found = true
				break
			}
		}
		if found || !page.Pagination.HasMore || len(page.Data) == 0 {
			break
		}
		offset += len(page.Data)
	}
	if !found {
		return nil, errors.New("the selected space has no compatible page/note type; no object was created")
	}
	keyData, _ := json.Marshal(raw)
	text, err := a.receipts.commit(requestID, string(keyData), func() (string, error) {
		item, err := a.client.createObject(ctx, in.SpaceID, in.Name, in.Body, in.TypeKey)
		if err != nil {
			return "", err
		}
		return "Created Anytype object " + item.ID + ": " + item.Name, nil
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
