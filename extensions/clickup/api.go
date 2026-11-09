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
	errToken       = errors.New("ClickUp rejected the personal API key; replace it in extension settings")
	errUnavailable = errors.New("ClickUp could not be reached; try again later")
	errNotFound    = errors.New("ClickUp could not find that task, list or workspace")
	errRateLimited = errors.New("ClickUp rate limit reached; try again later")
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
		return errors.New("ClickUp personal API key is not configured")
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
	req.Header.Set("Authorization", c.token)
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
		return errors.New("ClickUp returned an oversized response")
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated:
		if out == nil || len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("ClickUp returned invalid JSON")
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
		return errors.New("ClickUp rejected the operation; check the fields and permissions")
	default:
		return fmt.Errorf("ClickUp returned HTTP %d", res.StatusCode)
	}
}

func validID(value string) error {
	if len(value) == 0 || len(value) > 100 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n/?#") {
		return errors.New("task, list or workspace ID must be a single path segment")
	}
	return nil
}

func (c *client) user(ctx context.Context) (user, error) {
	var out struct {
		User user `json:"user"`
	}
	err := c.do(ctx, http.MethodGet, "/user", nil, nil, &out)
	return out.User, err
}

func (c *client) teams(ctx context.Context) ([]team, error) {
	var out teamPage
	err := c.do(ctx, http.MethodGet, "/team", nil, nil, &out)
	return out.Teams, err
}

func (c *client) spaces(ctx context.Context, teamID string) ([]struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}, error) {
	if err := validID(teamID); err != nil {
		return nil, err
	}
	var out struct {
		Spaces []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"spaces"`
	}
	err := c.do(ctx, http.MethodGet, "/team/"+url.PathEscape(teamID)+"/space", nil, nil, &out)
	return out.Spaces, err
}

func (c *client) lists(ctx context.Context, spaceID string) ([]taskList, error) {
	if err := validID(spaceID); err != nil {
		return nil, err
	}
	var out listPage
	err := c.do(ctx, http.MethodGet, "/space/"+url.PathEscape(spaceID)+"/list", nil, nil, &out)
	return out.Lists, err
}

func (c *client) tasks(ctx context.Context, teamID string, userID int64, page int) (taskPage, error) {
	if err := validID(teamID); err != nil {
		return taskPage{}, err
	}
	if userID <= 0 || page < 0 {
		return taskPage{}, errors.New("invalid user or page")
	}
	q := url.Values{"assignees[]": {strconv.FormatInt(userID, 10)}, "page": {strconv.Itoa(page)}, "order_by": {"updated"}, "reverse": {"true"}}
	var out taskPage
	err := c.do(ctx, http.MethodGet, "/team/"+url.PathEscape(teamID)+"/task", q, nil, &out)
	return out, err
}

func (c *client) task(ctx context.Context, id string) (task, error) {
	if err := validID(id); err != nil {
		return task{}, err
	}
	var out task
	err := c.do(ctx, http.MethodGet, "/task/"+url.PathEscape(id), nil, nil, &out)
	return out, err
}

func (c *client) list(ctx context.Context, id string) (taskList, error) {
	if err := validID(id); err != nil {
		return taskList{}, err
	}
	var out taskList
	err := c.do(ctx, http.MethodGet, "/list/"+url.PathEscape(id), nil, nil, &out)
	return out, err
}

func (c *client) createTask(ctx context.Context, listID string, userID int64, name, description string) (task, error) {
	if err := validID(listID); err != nil {
		return task{}, err
	}
	var out task
	err := c.do(ctx, http.MethodPost, "/list/"+url.PathEscape(listID)+"/task", nil, map[string]any{"name": name, "description": description, "assignees": []int64{userID}}, &out)
	return out, err
}

func (c *client) updateStatus(ctx context.Context, taskID, status string) (task, error) {
	if err := validID(taskID); err != nil {
		return task{}, err
	}
	var out task
	err := c.do(ctx, http.MethodPut, "/task/"+url.PathEscape(taskID), nil, map[string]string{"status": status}, &out)
	return out, err
}

func safeTaskLink(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() != "app.clickup.com" || u.User != nil {
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
