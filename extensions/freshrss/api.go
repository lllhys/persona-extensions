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
	"sync"
	"time"
)

var (
	errToken       = errors.New("FreshRSS rejected the API username or password; check the dedicated API credentials")
	errUnavailable = errors.New("FreshRSS could not be reached; try again later")
	errNotFound    = errors.New("FreshRSS could not find that article or feed")
	errRateLimited = errors.New("FreshRSS rate limit reached; try again later")
)

type client struct {
	base, username, password string
	http                     *http.Client
	mu                       sync.Mutex
	auth                     string
}

func newClient(base, username, password string) *client {
	return &client{base: strings.TrimRight(base, "/"), username: strings.TrimSpace(username), password: strings.TrimSpace(password), http: &http.Client{
		Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *client) login(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.auth != "" {
		return c.auth, nil
	}
	if c.username == "" || c.password == "" {
		return "", errors.New("FreshRSS API username and dedicated API password are required")
	}
	form := url.Values{"Email": {c.username}, "Passwd": {c.password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/accounts/ClientLogin", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/plain")
	res, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errUnavailable, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8192))
	if err != nil {
		return "", errUnavailable
	}
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return "", errToken
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("FreshRSS login returned HTTP %d", res.StatusCode)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Auth=") {
			auth := strings.TrimSpace(strings.TrimPrefix(line, "Auth="))
			parts := strings.Split(auth, "/")
			if len(parts) == 2 && parts[0] == c.username && len(parts[1]) >= 20 && len(parts[1]) <= 256 {
				c.auth = auth
				return c.auth, nil
			}
		}
	}
	return "", errors.New("FreshRSS returned an invalid login response")
}

func (c *client) do(ctx context.Context, method, path string, query, form url.Values, out any) error {
	data, err := c.doBytes(ctx, method, path, query, form)
	if err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("FreshRSS returned invalid JSON")
		}
	} else if strings.TrimSpace(string(data)) != "OK" && !json.Valid(data) {
		return errors.New("FreshRSS returned an unsuccessful operation")
	}
	return nil
}

// doBytes performs an authenticated request and returns a successful response body without assuming its format.
func (c *client) doBytes(ctx context.Context, method, path string, query, form url.Values) ([]byte, error) {
	auth, err := c.login(ctx)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "GoogleLogin auth="+auth)
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
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
		return nil, errors.New("FreshRSS returned an oversized response")
	}
	switch res.StatusCode {
	case http.StatusOK:
		return data, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, errToken
	case http.StatusNotFound:
		return nil, errNotFound
	case http.StatusTooManyRequests:
		return nil, errRateLimited
	default:
		return nil, fmt.Errorf("FreshRSS returned HTTP %d", res.StatusCode)
	}
}

func (c *client) articles(ctx context.Context, view, continuation string) (articlePage, error) {
	if continuation != "" {
		if _, err := strconv.ParseUint(continuation, 10, 64); err != nil || continuation == "0" {
			return articlePage{}, errors.New("invalid article continuation")
		}
	}
	path := "/reader/api/0/stream/contents/reading-list"
	if view == "starred" {
		path = "/reader/api/0/stream/contents/user/-/state/com.google/starred"
	} else if view != "unread" {
		return articlePage{}, errors.New("invalid reading view")
	}
	q := url.Values{"output": {"json"}, "n": {"30"}, "r": {"n"}}
	if view == "unread" {
		q.Set("xt", "user/-/state/com.google/read")
	}
	if continuation != "" {
		q.Set("c", continuation)
	}
	var out articlePage
	err := c.do(ctx, http.MethodGet, path, q, nil, &out)
	return out, err
}

func (c *client) subscriptions(ctx context.Context) ([]subscription, error) {
	var out struct {
		Subscriptions []subscription `json:"subscriptions"`
	}
	err := c.do(ctx, http.MethodGet, "/reader/api/0/subscription/list", url.Values{"output": {"json"}}, nil, &out)
	return out.Subscriptions, err
}

func (c *client) actionToken(ctx context.Context) (string, error) {
	data, err := c.doBytes(ctx, http.MethodGet, "/reader/api/0/token", nil, nil)
	if err != nil {
		return "", err
	}
	// FreshRSS returns the token as plain text followed by one newline, not as a JSON string.
	token := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if len(token) < 20 || len(token) > 128 || strings.ContainsAny(token, "\r\n") {
		return "", errors.New("FreshRSS returned an invalid action token")
	}
	return token, nil
}

func (c *client) markRead(ctx context.Context, id string) error {
	if !validItemID(id) {
		return errors.New("invalid FreshRSS article ID")
	}
	token, err := c.actionToken(ctx)
	if err != nil {
		return err
	}
	form := url.Values{"T": {token}, "i": {id}, "a": {"user/-/state/com.google/read"}}
	return c.do(ctx, http.MethodPost, "/reader/api/0/edit-tag", nil, form, nil)
}

func clipText(text string, limit int) string {
	r := []rune(text)
	if len(r) > limit {
		return string(r[:max(0, limit-1)]) + "…"
	}
	return text
}
