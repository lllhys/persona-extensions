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
	errToken       = errors.New("GoodLinks rejected the API token; replace it in extension settings")
	errUnavailable = errors.New("GoodLinks local API could not be reached; check GoodLinks is running and the API is enabled")
	errNotFound    = errors.New("GoodLinks could not find that link or highlight")
	errRateLimited = errors.New("GoodLinks rate limit reached; try again later")
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
		return errors.New("GoodLinks API token is not configured")
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
		return errors.New("GoodLinks returned an oversized response")
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated:
		if out == nil || len(data) == 0 {
			return nil
		}
		if text, ok := out.(*string); ok {
			*text = string(data)
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("GoodLinks returned invalid JSON")
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
		return errors.New("GoodLinks rejected the operation; check the link and fields")
	default:
		return fmt.Errorf("GoodLinks returned HTTP %d", res.StatusCode)
	}
}

func validLinkID(value string) error {
	if len(value) == 0 || len(value) > 256 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n/?#") {
		return errors.New("link ID must be a single path segment")
	}
	return nil
}

func (c *client) links(ctx context.Context, view, query string, offset int) (linkPage, error) {
	if view != "unread" && view != "starred" && view != "search" {
		return linkPage{}, errors.New("view must be unread, starred or search")
	}
	if offset < 0 || len([]rune(query)) > 200 || strings.ContainsRune(query, 0) {
		return linkPage{}, errors.New("invalid search or offset")
	}
	var path string
	q := url.Values{"limit": {"50"}, "offset": {strconv.Itoa(offset)}}
	if view == "search" {
		if strings.TrimSpace(query) == "" {
			return linkPage{}, errors.New("search text is required")
		}
		path = "/api/v1/links"
		q.Set("search", query)
	} else {
		path = "/api/v1/lists/" + view
	}
	var out linkPage
	err := c.do(ctx, http.MethodGet, path, q, nil, &out)
	return out, err
}

func (c *client) link(ctx context.Context, id string) (link, error) {
	var out link
	if err := validLinkID(id); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodGet, "/api/v1/links/"+url.PathEscape(id), nil, nil, &out)
	return out, err
}

func (c *client) content(ctx context.Context, id string) (string, error) {
	if err := validLinkID(id); err != nil {
		return "", err
	}
	var out string
	q := url.Values{"format": {"plaintext"}, "autoDownload": {"false"}}
	err := c.do(ctx, http.MethodGet, "/api/v1/links/"+url.PathEscape(id)+"/content", q, nil, &out)
	return out, err
}

func (c *client) highlights(ctx context.Context, linkID string, offset int) (highlightPage, error) {
	if err := validLinkID(linkID); err != nil {
		return highlightPage{}, err
	}
	if offset < 0 {
		return highlightPage{}, errors.New("offset must not be negative")
	}
	q := url.Values{"linkID": {linkID}, "limit": {"50"}, "offset": {strconv.Itoa(offset)}, "sort": {"newest"}}
	var out highlightPage
	err := c.do(ctx, http.MethodGet, "/api/v1/highlights", q, nil, &out)
	return out, err
}

func (c *client) update(ctx context.Context, id string, body map[string]any) (link, error) {
	if err := validLinkID(id); err != nil {
		return link{}, err
	}
	var out link
	err := c.do(ctx, http.MethodPatch, "/api/v1/links/"+url.PathEscape(id), nil, body, &out)
	return out, err
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
