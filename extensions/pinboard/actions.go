package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

type addArgs struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	Notes string `json:"notes,omitempty"`
	Tags  string `json:"tags,omitempty"`
}

func (in addArgs) validate() error {
	u, err := url.Parse(strings.TrimSpace(in.URL))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return errors.New("bookmark URL must be a valid HTTPS URL without embedded credentials")
	}
	if strings.TrimSpace(in.Title) == "" || len([]rune(in.Title)) > 255 {
		return errors.New("bookmark title must be nonempty and at most 255 characters")
	}
	if len([]rune(in.Notes)) > 65536 || len(in.Tags) > 1000 {
		return errors.New("bookmark notes or tags exceed the documented limits")
	}
	for _, r := range in.Title {
		if r == 0 || unicode.IsControl(r) {
			return errors.New("bookmark title contains unsupported control characters")
		}
	}
	for _, r := range in.Notes + in.Tags {
		if r == 0 || unicode.IsControl(r) && r != '\n' && r != '\t' {
			return errors.New("bookmark notes or tags contain unsupported control characters")
		}
	}
	if len(strings.Fields(in.Tags)) > 100 || strings.Contains(in.Tags, ",") {
		return errors.New("bookmark tags must be space-separated and at most 100 tags")
	}
	return nil
}

func describeAction(name string, raw json.RawMessage) (map[string]any, error) {
	if name != "pinboard_add" {
		return nil, fmt.Errorf("unknown action %q", name)
	}
	var in addArgs
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	summary := "Add a private read-later Pinboard bookmark\nTitle: " + in.Title + "\nURL: " + in.URL
	if in.Notes != "" {
		summary += "\nNotes: " + in.Notes
	}
	if in.Tags != "" {
		summary += "\nTags: " + in.Tags
	}
	return map[string]any{"action": map[string]any{"name": name, "visible": "owner"}, "summary": summary}, nil
}

func (a *app) doAction(ctx context.Context, requestID, name string, raw json.RawMessage) (map[string]any, error) {
	if name != "pinboard_add" {
		return nil, fmt.Errorf("unknown action %q", name)
	}
	var in addArgs
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	keyData, _ := json.Marshal(raw)
	text, err := a.receipts.commit(requestID, string(keyData), func() (string, error) {
		if err := a.client.add(ctx, in.URL, in.Title, in.Notes, in.Tags); err != nil {
			return "", err
		}
		return "Added private read-later Pinboard bookmark: " + in.Title, nil
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
