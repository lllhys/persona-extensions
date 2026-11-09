package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	errToken       = errors.New("Shortcut rejected the API token; replace it in extension settings")
	errUnavailable = errors.New("Shortcut could not be reached; try again later")
	errNotFound    = errors.New("Shortcut could not find that story or workflow")
	errRateLimited = errors.New("Shortcut rate limit reached; try again later")
)

type client struct {
	base, token string
	http        *http.Client
}

func newClient(base, token string) *client {
	return &client{base: strings.TrimRight(base, "/"), token: strings.TrimSpace(token), http: &http.Client{
		Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	if c.token == "" {
		return errors.New("Shortcut API token is not configured")
	}
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Shortcut-Token", c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if query != nil {
		req.URL.RawQuery = query.Encode()
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", errUnavailable, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20+1))
	if err != nil {
		return errUnavailable
	}
	if len(data) > 4<<20 {
		return errors.New("Shortcut returned an oversized response")
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated:
		if out == nil || len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("Shortcut returned invalid JSON")
		}
		return nil
	case http.StatusNoContent:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return errToken
	case http.StatusNotFound:
		return errNotFound
	case http.StatusTooManyRequests:
		return errRateLimited
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return errors.New("Shortcut rejected the operation; check the fields and permissions")
	default:
		return fmt.Errorf("Shortcut returned HTTP %d", res.StatusCode)
	}
}

func validMention(value string) error {
	if len(value) == 0 || len(value) > 80 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n\"'") {
		return errors.New("current member mention name is invalid")
	}
	return nil
}

func validNext(value string) error {
	if len(value) > 2048 || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("invalid Shortcut next-page token")
	}
	return nil
}

func (c *client) member(ctx context.Context) (member, error) {
	var out member
	err := c.do(ctx, http.MethodGet, "/member", nil, nil, &out)
	return out, err
}

func (c *client) stories(ctx context.Context, mention, next string) (storyPage, error) {
	if err := validMention(mention); err != nil {
		return storyPage{}, err
	}
	if err := validNext(next); err != nil {
		return storyPage{}, err
	}
	q := url.Values{"query": {"owner:" + mention + " is:story !is:done"}, "page_size": {"50"}, "detail": {"slim"}, "entity_types": {"story"}}
	if next != "" {
		q.Set("next", next)
	}
	var out storyPage
	err := c.do(ctx, http.MethodGet, "/search/stories", q, nil, &out)
	return out, err
}

func storyPath(id int64) string { return "/stories/" + strconv.FormatInt(id, 10) }

func (c *client) story(ctx context.Context, id int64) (story, error) {
	var out story
	if id <= 0 {
		return out, errors.New("story ID must be positive")
	}
	err := c.do(ctx, http.MethodGet, storyPath(id), nil, nil, &out)
	return out, err
}

func (c *client) workflows(ctx context.Context) ([]workflow, error) {
	var out []workflow
	err := c.do(ctx, http.MethodGet, "/workflows", nil, nil, &out)
	return out, err
}

func (c *client) createStory(ctx context.Context, name, description, storyType, deadline string, stateID int64, ownerID string) (story, error) {
	if stateID <= 0 || ownerID == "" || len(ownerID) > 80 || strings.ContainsAny(ownerID, "\x00\r\n") {
		return story{}, errors.New("invalid workflow state or owner ID")
	}
	body := map[string]any{"name": name, "workflow_state_id": stateID, "owner_ids": []string{ownerID}}
	if description != "" {
		body["description"] = description
	}
	if storyType != "" {
		body["story_type"] = storyType
	}
	if deadline != "" {
		body["deadline"] = deadline
	}
	var out story
	err := c.do(ctx, http.MethodPost, "/stories", nil, body, &out)
	return out, err
}

func (c *client) updateStoryState(ctx context.Context, id, stateID int64) (story, error) {
	var out story
	if id <= 0 || stateID <= 0 {
		return out, errors.New("story and workflow state IDs must be positive")
	}
	err := c.do(ctx, http.MethodPut, storyPath(id), nil, map[string]any{"workflow_state_id": stateID}, &out)
	return out, err
}

func safeStoryLink(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() != "app.shortcut.com" || u.User != nil {
		return ""
	}
	return raw
}

func clipText(text string, limit int) string {
	r := []rune(text)
	if len(r) > limit {
		return string(r[:max(0, limit-1)]) + "…"
	}
	return text
}
