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

var readTools = []tool{
	{Name: "arxiv_search", Description: "Search arXiv papers by topic or show recent submissions with the official Atom API. Returns title, authors, abstract, category, arXiv ID and next offset.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{
			"query": map[string]any{"type": "string", "maxLength": 200}, "recent": map[string]any{"type": "boolean"}, "start": map[string]any{"type": "integer", "minimum": 0},
		}}},
	{Name: "arxiv_paper", Description: "Read one arXiv paper by exact ID using id_list. Returns title, authors, abstract, category and original arXiv link; no PDF or source attachment is downloaded.",
		InputSchema: map[string]any{"type": "object", "required": []string{"paper_id"}, "properties": map[string]any{"paper_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 100}}}},
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
		Query   string `json:"query"`
		Recent  bool   `json:"recent"`
		Start   int    `json:"start"`
		PaperID string `json:"paper_id"`
	}
	if err := decode(raw, &in); err != nil {
		return "", err
	}
	switch name {
	case "arxiv_search":
		if in.Query == "" {
			in.Recent = true
		}
		feed, err := a.client.query(ctx, in.Query, in.Recent, in.Start)
		a.record(err)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, item := range feed.Entries {
			b.WriteString(formatPaper(item, false))
		}
		if len(feed.Entries) == 0 {
			b.WriteString("No papers in this view.\n")
		}
		if len(feed.Entries) == 20 {
			fmt.Fprintf(&b, "Next start: %d\n", in.Start+len(feed.Entries))
		}
		return clipText(b.String(), 90000), nil
	case "arxiv_paper":
		item, err := a.client.paper(ctx, in.PaperID)
		a.record(err)
		if err != nil {
			return "", err
		}
		return clipText(formatPaper(item, true), 90000), nil
	}
	return "", fmt.Errorf("no tool %s", name)
}

func formatPaper(item paper, detail bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s\n  ID: %s | published: %s | updated: %s | category: %s\n", clipText(strings.Join(strings.Fields(item.Title), " "), 1000), item.ID, item.Published, item.Updated, item.Category.Term)
	var authors []string
	for _, author := range item.Authors {
		authors = append(authors, author.Name)
	}
	if len(authors) > 0 {
		b.WriteString("  Authors: " + clipText(strings.Join(authors, ", "), 2000) + "\n")
	}
	if detail {
		if item.Summary != "" {
			b.WriteString("\nAbstract:\n" + clipText(strings.Join(strings.Fields(item.Summary), " "), 20000) + "\n")
		}
		if link := paperURL(item); link != "" {
			b.WriteString("\n" + link + "\n")
		}
	}
	return b.String()
}
