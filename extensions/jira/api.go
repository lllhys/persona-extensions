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
	"strings"
	"time"
)

var (
	errToken       = errors.New("Jira rejected the personal access token; replace it in extension settings")
	errUnavailable = errors.New("Jira could not be reached; try again later")
	errNotFound    = errors.New("Jira could not find that issue")
	errRateLimited = errors.New("Jira rate limit reached; try again later")
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
		return errors.New("Jira personal access token is not configured")
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
		return errors.New("Jira returned an oversized response")
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated:
		if out == nil || len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("Jira returned invalid JSON")
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
		return errors.New("Jira rejected the issue fields")
	default:
		return fmt.Errorf("Jira returned HTTP %d", res.StatusCode)
	}
}

// search reads a page of the user's unresolved assigned issues: the first when token is "", else the page a previous
// answer's nextPageToken names. /search/jql ignores startAt, so the token is the only way past the first page.
func (c *client) search(ctx context.Context, token string) (searchPage, error) {
	if len(token) > maxPageToken {
		return searchPage{}, errors.New("invalid page token")
	}
	q := url.Values{
		"jql":        {"assignee = currentUser() AND resolution = Unresolved ORDER BY updated DESC"},
		"maxResults": {"50"},
		"fields":     {"summary,status,assignee,project,priority,issuetype,updated"},
	}
	if token != "" {
		q.Set("nextPageToken", token)
	}
	var out searchPage
	err := c.do(ctx, http.MethodGet, "/search/jql", q, nil, &out)
	return out, err
}

func (c *client) issue(ctx context.Context, key string) (issue, error) {
	var out issue
	err := c.do(ctx, http.MethodGet, "/issue/"+url.PathEscape(key), url.Values{"fields": {"summary,description,status,assignee,project,priority,issuetype,updated,comment"}}, nil, &out)
	return out, err
}

func (c *client) projects(ctx context.Context) ([]map[string]any, error) {
	var out []map[string]any
	err := c.do(ctx, http.MethodGet, "/project", nil, nil, &out)
	return out, err
}

func (c *client) issueTypes(ctx context.Context, projectID string) ([]issueType, error) {
	var out []issueType
	err := c.do(ctx, http.MethodGet, "/issuetype", url.Values{"projectId": {projectID}}, nil, &out)
	return out, err
}

func (c *client) create(ctx context.Context, in map[string]any) (issue, error) {
	var out issue
	err := c.do(ctx, http.MethodPost, "/issue", nil, in, &out)
	return out, err
}

func (c *client) comment(ctx context.Context, key, body string) error {
	return c.do(ctx, http.MethodPost, "/issue/"+url.PathEscape(key)+"/comment", nil, map[string]string{"body": body}, nil)
}

func clipText(text string, limit int) string {
	r := []rune(text)
	if len(r) > limit {
		return string(r[:max(0, limit-1)]) + "…"
	}
	return text
}
