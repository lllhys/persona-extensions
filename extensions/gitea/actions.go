package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

type reviewArgs struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Number int64  `json:"number"`
	Body   string `json:"body,omitempty"`
}

func (in reviewArgs) validate() error {
	if err := validRepo(in.Owner, in.Repo); err != nil {
		return err
	}
	if in.Number <= 0 {
		return errors.New("pull request number must be positive")
	}
	return nil
}

func validComment(body string) error {
	if strings.TrimSpace(body) == "" || len([]rune(body)) > 10000 {
		return errors.New("comment must be nonempty and at most 10000 characters")
	}
	for _, r := range body {
		if r == 0 || unicode.IsControl(r) && r != '\n' && r != '\t' {
			return errors.New("comment contains unsupported control characters")
		}
	}
	return nil
}

func describeAction(name string, raw json.RawMessage) (map[string]any, error) {
	var in reviewArgs
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	var summary string
	switch name {
	case "gitea_approve":
		summary = fmt.Sprintf("Submit an APPROVED review for PR #%d in %s/%s", in.Number, in.Owner, in.Repo)
	case "gitea_comment":
		if err := validComment(in.Body); err != nil {
			return nil, err
		}
		summary = fmt.Sprintf("Comment on PR #%d in %s/%s\n\n%s", in.Number, in.Owner, in.Repo, in.Body)
	default:
		return nil, fmt.Errorf("unknown action %q", name)
	}
	return map[string]any{"action": map[string]any{"name": name, "visible": "owner"}, "summary": summary}, nil
}

func (a *app) doAction(ctx context.Context, requestID, name string, raw json.RawMessage) (map[string]any, error) {
	var in reviewArgs
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	var fn func() (string, error)
	switch name {
	case "gitea_approve":
		fn = func() (string, error) {
			if err := a.client.approve(ctx, in.Owner, in.Repo, in.Number); err != nil {
				return "", err
			}
			return fmt.Sprintf("Approved PR #%d", in.Number), nil
		}
	case "gitea_comment":
		if err := validComment(in.Body); err != nil {
			return nil, err
		}
		fn = func() (string, error) {
			if _, err := a.client.comment(ctx, in.Owner, in.Repo, in.Number, in.Body); err != nil {
				return "", err
			}
			return fmt.Sprintf("Commented on PR #%d", in.Number), nil
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
