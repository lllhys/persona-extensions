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
	errToken       = errors.New("Memos rejected the access token; replace it in extension settings")
	errUnavailable = errors.New("Memos could not be reached; try again later")
	errNotFound    = errors.New("Memos could not find that note or user")
	errRateLimited = errors.New("Memos rate limit reached; try again later")
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
		return errors.New("Memos access token is not configured")
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
		return errors.New("Memos returned an oversized response")
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated:
		if out == nil || len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("Memos returned invalid JSON")
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
		return errors.New("Memos rejected the operation; check the content and permissions")
	default:
		return fmt.Errorf("Memos returned HTTP %d", res.StatusCode)
	}
}

func validMemoID(value string) error {
	if len(value) == 0 || len(value) > 80 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n/?#") {
		return errors.New("memo ID must be a single path segment")
	}
	return nil
}

func validPageToken(value string) error {
	if len(value) > 2048 || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("invalid Memos page token")
	}
	return nil
}

func quoteCEL(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func (c *client) memos(ctx context.Context, creator, view, query, token string) (memoPage, error) {
	if creator == "" || !strings.HasPrefix(creator, "users/") || validUsername(strings.TrimPrefix(creator, "users/")) != nil {
		return memoPage{}, errors.New("invalid creator")
	}
	if view != "recent" && view != "pinned" && view != "search" {
		return memoPage{}, errors.New("view must be recent, pinned or search")
	}
	if err := validPageToken(token); err != nil {
		return memoPage{}, err
	}
	if len([]rune(query)) > 200 || strings.ContainsRune(query, 0) {
		return memoPage{}, errors.New("invalid note search")
	}
	filter := "creator == " + quoteCEL(creator)
	switch view {
	case "pinned":
		filter += " && pinned == true"
	case "search":
		if strings.TrimSpace(query) == "" {
			return memoPage{}, errors.New("search text is required")
		}
		filter += " && content.contains(" + quoteCEL(query) + ")"
	}
	q := url.Values{"pageSize": {"50"}, "state": {"NORMAL"}, "orderBy": {"pinned desc, create_time desc"}, "filter": {filter}}
	if token != "" {
		q.Set("pageToken", token)
	}
	var out memoPage
	err := c.do(ctx, http.MethodGet, "/api/memos", q, nil, &out)
	return out, err
}

func memoPath(id string) string { return "/api/memos/" + url.PathEscape(id) }

func (c *client) memo(ctx context.Context, id string) (memo, error) {
	var out memo
	if err := validMemoID(id); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodGet, memoPath(id), nil, nil, &out)
	return out, err
}

func (c *client) currentUser(ctx context.Context, username string) (user, error) {
	var out user
	if err := validUsername(username); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodGet, "/api/users/"+url.PathEscape(username), nil, nil, &out)
	return out, err
}

func (c *client) create(ctx context.Context, content string) (memo, error) {
	var out memo
	err := c.do(ctx, http.MethodPost, "/api/memos", nil, map[string]any{"content": content, "visibility": "PRIVATE", "state": "NORMAL"}, &out)
	return out, err
}

func (c *client) updateContent(ctx context.Context, id, content string) (memo, error) {
	var out memo
	err := c.do(ctx, http.MethodPatch, memoPath(id), url.Values{"updateMask": {"content"}}, map[string]any{"content": content}, &out)
	return out, err
}

func (c *client) updatePinned(ctx context.Context, id string, pinned bool) (memo, error) {
	var out memo
	err := c.do(ctx, http.MethodPatch, memoPath(id), url.Values{"updateMask": {"pinned"}}, map[string]any{"pinned": pinned}, &out)
	return out, err
}

func clipText(text string, limit int) string {
	r := []rune(text)
	if len(r) > limit {
		return string(r[:max(0, limit-1)]) + "…"
	}
	return text
}

func pageCount(value int) string { return strconv.Itoa(value) }
