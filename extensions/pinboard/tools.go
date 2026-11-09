package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

var tagSchema = map[string]any{"type": "string", "maxLength": 300}

var readTools = []tool{
	{Name: "pinboard_recent", Description: "List the current user's most recent Pinboard bookmarks, optionally filtered by up to three space-separated tags. Returns title, URL, notes, tags and read-later state.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"tags": tagSchema}}},
	{Name: "pinboard_all", Description: "Page through the current user's Pinboard archive with start/results and optional up-to-three-tag filtering. Use the returned next start when present.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"tags": tagSchema, "start": map[string]any{"type": "integer", "minimum": 0}}}},
	{Name: "pinboard_tags", Description: "List the current user's Pinboard tags with usage counts, useful for choosing a documented tag filter or new bookmark tags.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
}

var actionTools = []tool{
	{Name: "pinboard_add", Description: "Add a private read-later Pinboard bookmark after explicit confirmation. replace=no prevents overwriting an existing URL; shared=no and toread=yes are explicit.",
		InputSchema: map[string]any{"type": "object", "required": []string{"url", "title"}, "properties": map[string]any{
			"url": map[string]any{"type": "string", "format": "uri"}, "title": map[string]any{"type": "string", "minLength": 1, "maxLength": 255},
			"notes": map[string]any{"type": "string", "maxLength": 65536}, "tags": map[string]any{"type": "string", "maxLength": 1000},
		}}},
}

func decode(raw json.RawMessage, out any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	if dec.More() {
		return errors.New("invalid arguments: trailing JSON value")
	}
	return nil
}

func (a *app) callTool(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	var in struct {
		Tags  string `json:"tags"`
		Start int    `json:"start"`
	}
	if err := decode(raw, &in); err != nil {
		return "", err
	}
	switch name {
	case "pinboard_recent":
		items, err := a.client.recent(ctx, in.Tags)
		a.record(err)
		if err != nil {
			return "", err
		}
		return clipText(formatBookmarks(items), 90000), nil
	case "pinboard_all":
		items, err := a.client.all(ctx, in.Tags, in.Start)
		a.record(err)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		b.WriteString(formatBookmarks(items))
		if len(items) == 50 {
			fmt.Fprintf(&b, "\nNext start: %d\n", in.Start+len(items))
		}
		return clipText(b.String(), 90000), nil
	case "pinboard_tags":
		items, err := a.client.tags(ctx)
		a.record(err)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, item := range items {
			fmt.Fprintf(&b, "- %s: %d\n", clipText(item.Tag, 255), item.Count)
		}
		if len(items) == 0 {
			b.WriteString("No tags.\n")
		}
		return clipText(b.String(), 90000), nil
	}
	return "", fmt.Errorf("no tool %s", name)
}

func formatBookmarks(items []bookmark) string {
	if len(items) == 0 {
		return "No bookmarks in this view.\n"
	}
	var b strings.Builder
	for _, item := range items {
		fmt.Fprintf(&b, "- %s\n  URL: %s\n  Tags: %s | read later: %s | saved: %s\n", clipText(item.Description, 500), safeBookmarkLink(item.Href), item.Tags, item.ToRead, item.Time)
		if item.Extended != "" {
			b.WriteString("  Notes: " + clipText(item.Extended, 1000) + "\n")
		}
	}
	return b.String()
}
