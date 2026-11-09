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

type createArgs struct {
	Text      string `json:"text"`
	Title     string `json:"title"`
	Author    string `json:"author,omitempty"`
	Note      string `json:"note,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
}

type noteArgs struct {
	HighlightID int64  `json:"highlight_id"`
	Note        string `json:"note"`
}

type tagArgs struct {
	BookID int64  `json:"book_id"`
	Name   string `json:"name"`
}

func validText(value string, limit int, multiline bool) error {
	if len([]rune(value)) > limit {
		return errors.New("text exceeds the documented Readwise limit")
	}
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) && (!multiline || r != '\n' && r != '\t') {
			return errors.New("text contains unsupported control characters")
		}
	}
	return nil
}

func (in createArgs) validate() error {
	if strings.TrimSpace(in.Text) == "" || validText(in.Text, 8191, true) != nil {
		return errors.New("highlight text must be nonempty and at most 8191 characters")
	}
	if strings.TrimSpace(in.Title) == "" || validText(in.Title, 511, false) != nil || validText(in.Author, 1024, false) != nil || validText(in.Note, 8191, true) != nil {
		return errors.New("highlight title, author or note is invalid")
	}
	if in.SourceURL != "" {
		u, err := url.Parse(in.SourceURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || len(in.SourceURL) > 2047 {
			return errors.New("source URL must be a valid HTTPS URL without credentials")
		}
	}
	return nil
}

func describeAction(name string, raw json.RawMessage) (map[string]any, error) {
	var summary string
	switch name {
	case "readwise_library_create_highlight":
		var in createArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		summary = "Create a Readwise Library highlight\nSource: " + in.Title + "\nText: " + in.Text
		if in.Note != "" {
			summary += "\nNote: " + in.Note
		}
	case "readwise_library_update_note":
		var in noteArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validID(in.HighlightID); err != nil {
			return nil, err
		}
		if err := validText(in.Note, 8191, true); err != nil {
			return nil, err
		}
		summary = fmt.Sprintf("Replace the note on Readwise highlight #%d\n\n%s", in.HighlightID, in.Note)
	case "readwise_library_add_book_tag":
		var in tagArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validID(in.BookID); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Name) == "" || validText(in.Name, 127, false) != nil {
			return nil, errors.New("tag name must be nonempty, one line and at most 127 characters")
		}
		summary = fmt.Sprintf("Add tag %q to Readwise book #%d", in.Name, in.BookID)
	default:
		return nil, fmt.Errorf("unknown action %q", name)
	}
	return map[string]any{"action": map[string]any{"name": name, "visible": "owner"}, "summary": summary}, nil
}

func (a *app) doAction(ctx context.Context, requestID, name string, raw json.RawMessage) (map[string]any, error) {
	var fn func() (string, error)
	switch name {
	case "readwise_library_create_highlight":
		var in createArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		fn = func() (string, error) {
			if err := a.client.createHighlight(ctx, in.Text, in.Title, in.Author, in.Note, in.SourceURL); err != nil {
				return "", err
			}
			return "Created Readwise Library highlight: " + in.Title, nil
		}
	case "readwise_library_update_note":
		var in noteArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validID(in.HighlightID); err != nil {
			return nil, err
		}
		if err := validText(in.Note, 8191, true); err != nil {
			return nil, err
		}
		fn = func() (string, error) {
			if _, err := a.client.updateNote(ctx, in.HighlightID, in.Note); err != nil {
				return "", err
			}
			return fmt.Sprintf("Updated note on Readwise highlight #%d", in.HighlightID), nil
		}
	case "readwise_library_add_book_tag":
		var in tagArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validID(in.BookID); err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Name) == "" || validText(in.Name, 127, false) != nil {
			return nil, errors.New("tag name must be nonempty, one line and at most 127 characters")
		}
		if _, err := a.client.book(ctx, in.BookID); err != nil {
			return nil, err
		}
		fn = func() (string, error) {
			item, err := a.client.addBookTag(ctx, in.BookID, in.Name)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Added tag %q to Readwise book #%d", item.Name, in.BookID), nil
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
