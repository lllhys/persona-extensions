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
	errToken       = errors.New("Gitea rejected the personal access token; replace it in extension settings")
	errUnavailable = errors.New("Gitea could not be reached; try again later")
	errNotFound    = errors.New("Gitea could not find that pull request or repository")
	errRateLimited = errors.New("Gitea rate limit reached; try again later")
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
		return errors.New("Gitea personal access token is not configured")
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
	req.Header.Set("Authorization", "token "+c.token)
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
		return errors.New("Gitea returned an oversized response")
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated:
		if out == nil || len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("Gitea returned invalid JSON")
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
		return errors.New("Gitea rejected the operation; check the fields and permissions")
	default:
		return fmt.Errorf("Gitea returned HTTP %d", res.StatusCode)
	}
}

func validRepo(owner, repo string) error {
	for _, value := range []string{owner, repo} {
		if len(value) == 0 || len(value) > 128 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n/?#") {
			return errors.New("owner and repository must be single path segments")
		}
	}
	return nil
}

func repoPath(owner, repo string) string {
	return "/api/v1/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
}

func (c *client) pullRequests(ctx context.Context, owner, repo string, page int) ([]pullRequest, error) {
	if err := validRepo(owner, repo); err != nil {
		return nil, err
	}
	if page < 1 {
		return nil, errors.New("page must be positive")
	}
	q := url.Values{"state": {"open"}, "page": {strconv.Itoa(page)}, "limit": {"50"}}
	var out []pullRequest
	err := c.do(ctx, http.MethodGet, repoPath(owner, repo)+"/pulls", q, nil, &out)
	return out, err
}

func (c *client) pullRequest(ctx context.Context, owner, repo string, id int64) (pullRequest, error) {
	var out pullRequest
	if id <= 0 {
		return out, errors.New("pull request number must be positive")
	}
	if err := validRepo(owner, repo); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodGet, repoPath(owner, repo)+"/pulls/"+strconv.FormatInt(id, 10), nil, nil, &out)
	return out, err
}

func (c *client) comments(ctx context.Context, owner, repo string, id int64) ([]comment, error) {
	if id <= 0 {
		return nil, errors.New("pull request number must be positive")
	}
	if err := validRepo(owner, repo); err != nil {
		return nil, err
	}
	var out []comment
	err := c.do(ctx, http.MethodGet, repoPath(owner, repo)+"/issues/"+strconv.FormatInt(id, 10)+"/comments", nil, nil, &out)
	return out, err
}

func (c *client) comment(ctx context.Context, owner, repo string, id int64, body string) (comment, error) {
	if id <= 0 {
		return comment{}, errors.New("pull request number must be positive")
	}
	if err := validRepo(owner, repo); err != nil {
		return comment{}, err
	}
	var out comment
	err := c.do(ctx, http.MethodPost, repoPath(owner, repo)+"/issues/"+strconv.FormatInt(id, 10)+"/comments", nil, map[string]string{"body": body}, &out)
	return out, err
}

func (c *client) approve(ctx context.Context, owner, repo string, id int64) error {
	if id <= 0 {
		return errors.New("pull request number must be positive")
	}
	if err := validRepo(owner, repo); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, repoPath(owner, repo)+"/pulls/"+strconv.FormatInt(id, 10)+"/reviews", nil, map[string]string{"state": "APPROVED"}, nil)
}

func safePRLink(raw string) string {
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
