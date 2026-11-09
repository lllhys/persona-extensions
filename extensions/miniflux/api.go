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
	errToken       = errors.New("Miniflux rejected the API key; replace it in extension settings")
	errUnavailable = errors.New("Miniflux could not be reached; try again later")
	errNotFound    = errors.New("Miniflux could not find that entry")
	errRateLimited = errors.New("Miniflux rate limit reached; try again later")
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
		return errors.New("Miniflux API key is not configured")
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
	req.Header.Set("X-Auth-Token", c.token)
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
		return errors.New("Miniflux returned an oversized response")
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated:
		if out == nil || len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("Miniflux returned invalid JSON")
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
		return errors.New("Miniflux rejected the operation; check the entry and filters")
	default:
		return fmt.Errorf("Miniflux returned HTTP %d", res.StatusCode)
	}
}

func (c *client) entries(ctx context.Context, view, query string, offset int) (entryPage, error) {
	if view != "unread" && view != "starred" && view != "search" {
		return entryPage{}, errors.New("view must be unread, starred or search")
	}
	if offset < 0 {
		return entryPage{}, errors.New("offset must not be negative")
	}
	if len([]rune(query)) > 200 || strings.ContainsRune(query, 0) {
		return entryPage{}, errors.New("invalid search text")
	}
	q := url.Values{"limit": {"50"}, "offset": {strconv.Itoa(offset)}, "direction": {"desc"}, "order": {"published_at"}}
	switch view {
	case "unread":
		q.Set("status", "unread")
	case "starred":
		q.Set("starred", "true")
	case "search":
		if strings.TrimSpace(query) == "" {
			return entryPage{}, errors.New("search text is required")
		}
		q.Set("search", query)
	}
	var out entryPage
	err := c.do(ctx, http.MethodGet, "/v1/entries", q, nil, &out)
	return out, err
}

func (c *client) entry(ctx context.Context, id int64) (entry, error) {
	var out entry
	if id <= 0 {
		return out, errors.New("entry ID must be positive")
	}
	err := c.do(ctx, http.MethodGet, "/v1/entries/"+strconv.FormatInt(id, 10), nil, nil, &out)
	return out, err
}

func (c *client) markRead(ctx context.Context, id int64) error {
	if id <= 0 {
		return errors.New("entry ID must be positive")
	}
	return c.do(ctx, http.MethodPut, "/v1/entries", nil, map[string]any{"entry_ids": []int64{id}, "status": "read"}, nil)
}

func (c *client) toggleBookmark(ctx context.Context, id int64) error {
	if id <= 0 {
		return errors.New("entry ID must be positive")
	}
	return c.do(ctx, http.MethodPut, "/v1/entries/"+strconv.FormatInt(id, 10)+"/bookmark", nil, nil, nil)
}

func safeArticleLink(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
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
