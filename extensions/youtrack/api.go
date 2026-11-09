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
	errToken       = errors.New("YouTrack rejected the permanent token; replace it in extension settings")
	errUnavailable = errors.New("YouTrack could not be reached; try again later")
	errNotFound    = errors.New("YouTrack could not find that issue or project")
	errRateLimited = errors.New("YouTrack rate limit reached; try again later")
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
		return errors.New("YouTrack permanent token is not configured")
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
	req.Header.Set("Authorization", "Bearer "+c.token)
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
		return errors.New("YouTrack returned an oversized response")
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated:
		if out == nil || len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("YouTrack returned invalid JSON")
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
		return errors.New("YouTrack rejected the operation; check the fields and permissions")
	default:
		return fmt.Errorf("YouTrack returned HTTP %d", res.StatusCode)
	}
}

const issueFields = "id,idReadable,summary,description,resolved,updated,commentsCount,project(id,name,shortName),reporter(id,login,name)"

func validResourceID(value string) error {
	if len(value) == 0 || len(value) > 100 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n/?#") {
		return errors.New("issue or project ID must be a single path segment")
	}
	return nil
}

func (c *client) issues(ctx context.Context, skip int) ([]issue, error) {
	if skip < 0 {
		return nil, errors.New("skip must not be negative")
	}
	q := url.Values{"query": {"for: me #Unresolved"}, "$top": {"50"}, "$skip": {strconv.Itoa(skip)}, "fields": {issueFields}}
	var out []issue
	err := c.do(ctx, http.MethodGet, "/api/issues", q, nil, &out)
	return out, err
}

func (c *client) issue(ctx context.Context, id string) (issue, error) {
	var out issue
	if err := validResourceID(id); err != nil {
		return out, err
	}
	q := url.Values{"fields": {issueFields}}
	err := c.do(ctx, http.MethodGet, "/api/issues/"+url.PathEscape(id), q, nil, &out)
	return out, err
}

func (c *client) comments(ctx context.Context, id string) ([]comment, error) {
	if err := validResourceID(id); err != nil {
		return nil, err
	}
	q := url.Values{"$top": {"100"}, "$skip": {"0"}, "fields": {"id,text,created,deleted,author(id,login,name)"}}
	var out []comment
	err := c.do(ctx, http.MethodGet, "/api/issues/"+url.PathEscape(id)+"/comments", q, nil, &out)
	return out, err
}

func (c *client) projects(ctx context.Context, skip int) ([]project, error) {
	if skip < 0 {
		return nil, errors.New("skip must not be negative")
	}
	q := url.Values{"$top": {"100"}, "$skip": {strconv.Itoa(skip)}, "fields": {"id,name,shortName,archived"}}
	var out []project
	err := c.do(ctx, http.MethodGet, "/api/admin/projects", q, nil, &out)
	return out, err
}

func (c *client) project(ctx context.Context, id string) (project, error) {
	var out project
	if err := validResourceID(id); err != nil {
		return out, err
	}
	q := url.Values{"fields": {"id,name,shortName,archived"}}
	err := c.do(ctx, http.MethodGet, "/api/admin/projects/"+url.PathEscape(id), q, nil, &out)
	return out, err
}

func (c *client) createIssue(ctx context.Context, projectID, summary, description string) (issue, error) {
	if err := validResourceID(projectID); err != nil {
		return issue{}, err
	}
	var out issue
	q := url.Values{"fields": {issueFields}}
	err := c.do(ctx, http.MethodPost, "/api/admin/projects/"+url.PathEscape(projectID)+"/issues", q, map[string]string{"summary": summary, "description": description}, &out)
	return out, err
}

func clipText(text string, limit int) string {
	r := []rune(text)
	if len(r) > limit {
		return string(r[:max(0, limit-1)]) + "…"
	}
	return text
}
