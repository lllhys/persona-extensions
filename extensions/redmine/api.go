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
	errToken       = errors.New("Redmine rejected the API key; replace it in extension settings")
	errUnavailable = errors.New("Redmine could not be reached; try again later")
	errNotFound    = errors.New("Redmine could not find that issue or project")
	errRateLimited = errors.New("Redmine rate limit reached; try again later")
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
		return errors.New("Redmine API key is not configured")
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
	req.Header.Set("X-Redmine-API-Key", c.token)
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
		return errors.New("Redmine returned an oversized response")
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated:
		if out == nil || len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("Redmine returned invalid JSON")
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
		return errors.New("Redmine rejected the operation; check the fields and permissions")
	default:
		return fmt.Errorf("Redmine returned HTTP %d", res.StatusCode)
	}
}

func validOffset(offset int) error {
	if offset < 0 {
		return errors.New("offset must not be negative")
	}
	return nil
}

func (c *client) issues(ctx context.Context, offset int) (issuePage, error) {
	if err := validOffset(offset); err != nil {
		return issuePage{}, err
	}
	q := url.Values{"assigned_to_id": {"me"}, "status_id": {"open"}, "limit": {"50"}, "offset": {strconv.Itoa(offset)}, "sort": {"updated_on:desc"}}
	var out issuePage
	err := c.do(ctx, http.MethodGet, "/issues.json", q, nil, &out)
	return out, err
}

func (c *client) issue(ctx context.Context, id int64) (issue, error) {
	if id <= 0 {
		return issue{}, errors.New("issue ID must be positive")
	}
	var out struct {
		Issue issue `json:"issue"`
	}
	q := url.Values{"include": {"journals"}}
	err := c.do(ctx, http.MethodGet, "/issues/"+strconv.FormatInt(id, 10)+".json", q, nil, &out)
	return out.Issue, err
}

func (c *client) projects(ctx context.Context, offset int) (projectPage, error) {
	if err := validOffset(offset); err != nil {
		return projectPage{}, err
	}
	q := url.Values{"limit": {"100"}, "offset": {strconv.Itoa(offset)}}
	var out projectPage
	err := c.do(ctx, http.MethodGet, "/projects.json", q, nil, &out)
	return out, err
}

func (c *client) project(ctx context.Context, id int64) (namedResource, error) {
	if id <= 0 {
		return namedResource{}, errors.New("project ID must be positive")
	}
	var out struct {
		Project namedResource `json:"project"`
	}
	err := c.do(ctx, http.MethodGet, "/projects/"+strconv.FormatInt(id, 10)+".json", nil, nil, &out)
	return out.Project, err
}

func (c *client) createIssue(ctx context.Context, projectID int64, subject, description string) (issue, error) {
	if projectID <= 0 {
		return issue{}, errors.New("project ID must be positive")
	}
	var out struct {
		Issue issue `json:"issue"`
	}
	body := map[string]any{"issue": map[string]any{"project_id": projectID, "subject": subject, "description": description}}
	err := c.do(ctx, http.MethodPost, "/issues.json", nil, body, &out)
	return out.Issue, err
}

func clipText(text string, limit int) string {
	r := []rune(text)
	if len(r) > limit {
		return string(r[:max(0, limit-1)]) + "…"
	}
	return text
}
