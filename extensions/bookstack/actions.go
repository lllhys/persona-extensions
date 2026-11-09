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
	BookID   int64  `json:"book_id"`
	Name     string `json:"name"`
	Markdown string `json:"markdown"`
}

type updateArgs struct {
	PageID   int64  `json:"page_id"`
	Name     string `json:"name,omitempty"`
	Markdown string `json:"markdown"`
}

func validTitle(value string) error {
	if strings.TrimSpace(value) == "" || len([]rune(value)) > 255 {
		return errors.New("page title must be nonempty and at most 255 characters")
	}
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) {
			return errors.New("page title contains unsupported control characters")
		}
	}
	return nil
}

func validMarkdown(value string) error {
	if strings.TrimSpace(value) == "" || len([]rune(value)) > 60000 {
		return errors.New("Markdown must be nonempty and at most 60000 characters")
	}
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) && r != '\n' && r != '\t' {
			return errors.New("Markdown contains unsupported control characters")
		}
	}
	return nil
}

func (in createArgs) validate() error {
	if err := validID(in.BookID); err != nil {
		return err
	}
	if err := validTitle(in.Name); err != nil {
		return err
	}
	return validMarkdown(in.Markdown)
}

func (in updateArgs) validate() error {
	if err := validID(in.PageID); err != nil {
		return err
	}
	if in.Name != "" {
		if err := validTitle(in.Name); err != nil {
			return err
		}
	}
	return validMarkdown(in.Markdown)
}

func describeAction(name string, raw json.RawMessage) (map[string]any, error) {
	var summary string
	switch name {
	case "bookstack_create_page":
		var in createArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		summary = fmt.Sprintf("Create BookStack page in book %d\nTitle: %s\n\n%s", in.BookID, in.Name, in.Markdown)
	case "bookstack_update_page":
		var in updateArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		summary = fmt.Sprintf("Replace Markdown source of BookStack page %d\n\n%s", in.PageID, in.Markdown)
	default:
		return nil, fmt.Errorf("unknown action %q", name)
	}
	return map[string]any{"action": map[string]any{"name": name, "visible": "owner"}, "summary": summary}, nil
}

func (a *app) doAction(ctx context.Context, requestID, name string, raw json.RawMessage) (map[string]any, error) {
	var fn func() (string, error)
	switch name {
	case "bookstack_create_page":
		var in createArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		if _, err := a.client.book(ctx, in.BookID); err != nil {
			return nil, err
		}
		fn = func() (string, error) {
			item, err := a.client.createPage(ctx, in.BookID, in.Name, in.Markdown)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Created BookStack page #%d: %s", item.ID, item.Name), nil
		}
	case "bookstack_update_page":
		var in updateArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		item, err := a.client.page(ctx, in.PageID)
		if err != nil {
			return nil, err
		}
		if item.Markdown == "" {
			return nil, errors.New("this page has no Markdown source; refusing to convert its HTML implicitly")
		}
		fn = func() (string, error) {
			if _, err := a.client.updatePage(ctx, in.PageID, in.Name, in.Markdown); err != nil {
				return "", err
			}
			return fmt.Sprintf("Updated BookStack page #%d", in.PageID), nil
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
