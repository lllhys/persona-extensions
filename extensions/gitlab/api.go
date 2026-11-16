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
	errToken       = errors.New("GitLab rejected the personal access token or its scope; check it in extension settings")
	errUnavailable = errors.New("GitLab could not be reached; try again later")
	errNotFound    = errors.New("GitLab could not find that merge request or project")
	errRateLimited = errors.New("GitLab rate limit reached; try again later")
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

func (c *client) do(ctx context.Context, method, path string, query url.Values, in, out any) (http.Header, error) {
	if c.token == "" {
		return nil, errors.New("GitLab personal access token is not configured")
	}
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if query != nil {
		req.URL.RawQuery = query.Encode()
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errUnavailable, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20+1))
	if err != nil {
		return nil, errUnavailable
	}
	if len(data) > 4<<20 {
		return nil, errors.New("GitLab returned an oversized response")
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated:
		if out == nil || len(data) == 0 {
			return res.Header, nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return nil, errors.New("GitLab returned invalid JSON")
		}
		return res.Header, nil
	case http.StatusNoContent:
		return res.Header, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, errToken
	case http.StatusNotFound:
		return nil, errNotFound
	case http.StatusTooManyRequests:
		return nil, errRateLimited
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return nil, errors.New("GitLab rejected the operation; check permissions and the merge request's current state")
	default:
		return nil, fmt.Errorf("GitLab returned HTTP %d", res.StatusCode)
	}
}

func validProjectID(project string) error {
	if len(project) == 0 || len(project) > 255 || strings.TrimSpace(project) != project || strings.ContainsAny(project, "\x00\r\n?#") {
		return errors.New("project must be a numeric ID or full namespace path")
	}
	return nil
}

func projectPath(project string) string { return url.PathEscape(project) }

func (c *client) mergeRequests(ctx context.Context, scope, search string, page int) (mrPage, error) {
	if page < 1 {
		return mrPage{}, errors.New("pagination page must be positive")
	}
	if scope != "assigned_to_me" && scope != "reviews_for_me" {
		return mrPage{}, errors.New("unsupported merge request scope")
	}
	q := url.Values{"scope": {scope}, "state": {"opened"}, "per_page": {"50"}, "page": {strconv.Itoa(page)}}
	if search != "" {
		q.Set("search", search)
		q.Set("in", "title")
	}
	var out []mergeRequest
	header, err := c.do(ctx, http.MethodGet, "/merge_requests", q, nil, &out)
	return mrPage{Items: out, Next: header.Get("X-Next-Page")}, err
}

func mrResource(project string, iid int64) string {
	return "/projects/" + projectPath(project) + "/merge_requests/" + strconv.FormatInt(iid, 10)
}

func (c *client) mergeRequest(ctx context.Context, project string, iid int64) (mergeRequest, error) {
	var out mergeRequest
	_, err := c.do(ctx, http.MethodGet, mrResource(project, iid), nil, nil, &out)
	return out, err
}

// notes returns every note up to maxNotePages pages, and the next page number when that bound leaves more.
func (c *client) notes(ctx context.Context, project string, iid int64) ([]note, string, error) {
	const maxNotePages = 5
	q := url.Values{"per_page": {"100"}, "sort": {"asc"}, "order_by": {"created_at"}}
	var out []note
	for page := 1; page <= maxNotePages; page++ {
		var batch []note
		header, err := c.do(ctx, http.MethodGet, mrResource(project, iid)+"/notes", q, nil, &batch)
		if err != nil {
			return nil, "", err
		}
		out = append(out, batch...)
		next := strings.TrimSpace(header.Get("X-Next-Page"))
		if next == "" {
			return out, "", nil
		}
		n, err := strconv.Atoi(next)
		if err != nil || n <= page {
			return nil, "", fmt.Errorf("GitLab returned an invalid next notes page %q", next)
		}
		if page == maxNotePages {
			return out, next, nil
		}
		q.Set("page", next)
	}
	return out, "", nil
}

func (c *client) approve(ctx context.Context, project string, iid int64) error {
	_, err := c.do(ctx, http.MethodPost, mrResource(project, iid)+"/approve", nil, nil, nil)
	return err
}

func (c *client) unapprove(ctx context.Context, project string, iid int64) error {
	_, err := c.do(ctx, http.MethodPost, mrResource(project, iid)+"/unapprove", nil, nil, nil)
	return err
}

func (c *client) comment(ctx context.Context, project string, iid int64, body string) error {
	_, err := c.do(ctx, http.MethodPost, mrResource(project, iid)+"/notes", nil, map[string]string{"body": body}, nil)
	return err
}

func clipText(text string, limit int) string {
	r := []rune(text)
	if len(r) > limit {
		return string(r[:max(0, limit-1)]) + "…"
	}
	return text
}
