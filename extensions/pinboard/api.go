package main

import (
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
	errToken       = errors.New("Pinboard rejected the API token; replace it in extension settings")
	errUnavailable = errors.New("Pinboard could not be reached; try again later")
	errRateLimited = errors.New("Pinboard rate limit reached; try again later")
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

func (c *client) do(ctx context.Context, path string, query url.Values, out any) error {
	if c.token == "" {
		return errors.New("Pinboard API token is not configured")
	}
	if query == nil {
		query = url.Values{}
	}
	query.Set("auth_token", c.token)
	query.Set("format", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.URL.RawQuery = query.Encode()
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
		return errors.New("Pinboard returned an oversized response")
	}
	switch res.StatusCode {
	case http.StatusOK:
		if out == nil || len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("Pinboard returned invalid JSON")
		}
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return errToken
	case http.StatusTooManyRequests:
		return errRateLimited
	default:
		return fmt.Errorf("Pinboard returned HTTP %d", res.StatusCode)
	}
}

func validTags(value string) error {
	if value == "" {
		return nil
	}
	parts := strings.Fields(value)
	if len(parts) > 3 {
		return errors.New("Pinboard supports at most three tag filters")
	}
	for _, part := range parts {
		if len([]rune(part)) > 255 || strings.Contains(part, ",") {
			return errors.New("invalid Pinboard tag filter")
		}
	}
	return nil
}

func (c *client) recent(ctx context.Context, tags string) (bookmarkList, error) {
	if err := validTags(tags); err != nil {
		return nil, err
	}
	q := url.Values{"count": {"50"}}
	if tags != "" {
		q.Set("tag", tags)
	}
	var out bookmarkList
	err := c.do(ctx, "/posts/recent", q, &out)
	return out, err
}

func (c *client) all(ctx context.Context, tags string, start int) (bookmarkList, error) {
	if err := validTags(tags); err != nil {
		return nil, err
	}
	if start < 0 {
		return nil, errors.New("start must not be negative")
	}
	q := url.Values{"start": {strconv.Itoa(start)}, "results": {"50"}}
	if tags != "" {
		q.Set("tag", tags)
	}
	var out bookmarkList
	err := c.do(ctx, "/posts/all", q, &out)
	return out, err
}

func (c *client) tags(ctx context.Context) (tagList, error) {
	var out tagList
	err := c.do(ctx, "/tags/get", nil, &out)
	return out, err
}

func (c *client) add(ctx context.Context, rawURL, title, notes, tags string) error {
	q := url.Values{"url": {rawURL}, "description": {title}, "replace": {"no"}, "shared": {"no"}, "toread": {"yes"}}
	if notes != "" {
		q.Set("extended", notes)
	}
	if tags != "" {
		q.Set("tags", tags)
	}
	var result struct {
		Result struct {
			Code string `json:"code"`
		} `json:"result"`
		Code string `json:"code"`
	}
	if err := c.do(ctx, "/posts/add", q, &result); err != nil {
		return err
	}
	code := result.Code
	if code == "" {
		code = result.Result.Code
	}
	if code != "done" {
		return errors.New("Pinboard did not add the bookmark; it may already exist or the input was rejected")
	}
	return nil
}

func safeBookmarkLink(raw string) string {
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
