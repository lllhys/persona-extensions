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
	errToken       = errors.New("Anytype rejected the API key; replace it in extension settings")
	errUnavailable = errors.New("Anytype local API could not be reached; check the configured instance")
	errNotFound    = errors.New("Anytype could not find that space, object or type")
	errRateLimited = errors.New("Anytype rate limit reached; try again later")
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
		return errors.New("Anytype API key is not configured")
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
	req.Header.Set("Anytype-Version", "2025-11-08")
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
		return errors.New("Anytype returned an oversized response")
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated:
		if out == nil || len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("Anytype returned invalid JSON")
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
		return errors.New("Anytype rejected the operation; check the fields and object type")
	default:
		return fmt.Errorf("Anytype returned HTTP %d", res.StatusCode)
	}
}

func validObjectID(value string) error {
	if len(value) == 0 || len(value) > 256 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n/?#") {
		return errors.New("space, object or type ID must be a single path segment")
	}
	return nil
}

func validOffset(offset int) error {
	if offset < 0 {
		return errors.New("offset must not be negative")
	}
	return nil
}

func (c *client) spaces(ctx context.Context, offset int) (page[space], error) {
	if err := validOffset(offset); err != nil {
		return page[space]{}, err
	}
	q := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {"50"}}
	var out page[space]
	err := c.do(ctx, http.MethodGet, "/v1/spaces", q, nil, &out)
	return out, err
}

func (c *client) objects(ctx context.Context, spaceID string, offset int) (page[knowledgeObject], error) {
	if err := validObjectID(spaceID); err != nil {
		return page[knowledgeObject]{}, err
	}
	if err := validOffset(offset); err != nil {
		return page[knowledgeObject]{}, err
	}
	q := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {"50"}}
	var out page[knowledgeObject]
	err := c.do(ctx, http.MethodGet, "/v1/spaces/"+url.PathEscape(spaceID)+"/objects", q, nil, &out)
	return out, err
}

func (c *client) search(ctx context.Context, query string, offset int) (page[knowledgeObject], error) {
	if strings.TrimSpace(query) == "" || len([]rune(query)) > 200 || strings.ContainsRune(query, 0) {
		return page[knowledgeObject]{}, errors.New("search text must be nonempty and at most 200 characters")
	}
	if err := validOffset(offset); err != nil {
		return page[knowledgeObject]{}, err
	}
	q := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {"50"}}
	var out page[knowledgeObject]
	err := c.do(ctx, http.MethodPost, "/v1/search", q, map[string]any{"query": query}, &out)
	return out, err
}

func (c *client) object(ctx context.Context, spaceID, objectID string) (knowledgeObject, error) {
	var out knowledgeObject
	if err := validObjectID(spaceID); err != nil {
		return out, err
	}
	if err := validObjectID(objectID); err != nil {
		return out, err
	}
	err := c.do(ctx, http.MethodGet, "/v1/spaces/"+url.PathEscape(spaceID)+"/objects/"+url.PathEscape(objectID), nil, nil, &out)
	return out, err
}

func (c *client) types(ctx context.Context, spaceID string, offset int) (page[objectType], error) {
	if err := validObjectID(spaceID); err != nil {
		return page[objectType]{}, err
	}
	if err := validOffset(offset); err != nil {
		return page[objectType]{}, err
	}
	q := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {"50"}}
	var out page[objectType]
	err := c.do(ctx, http.MethodGet, "/v1/spaces/"+url.PathEscape(spaceID)+"/types", q, nil, &out)
	return out, err
}

func (c *client) createObject(ctx context.Context, spaceID, name, markdown, typeKey string) (knowledgeObject, error) {
	if err := validObjectID(spaceID); err != nil {
		return knowledgeObject{}, err
	}
	body := map[string]any{"name": name, "body": markdown, "type_key": typeKey}
	var out knowledgeObject
	err := c.do(ctx, http.MethodPost, "/v1/spaces/"+url.PathEscape(spaceID)+"/objects", nil, body, &out)
	return out, err
}

func objectText(item knowledgeObject) string {
	if item.Markdown != "" {
		return item.Markdown
	}
	if item.Body != "" {
		return item.Body
	}
	var lines []string
	for _, block := range item.Blocks {
		if block.Text != nil && block.Text.Text != "" {
			lines = append(lines, block.Text.Text)
		}
	}
	return strings.Join(lines, "\n\n")
}

func clipText(text string, limit int) string {
	r := []rune(text)
	if len(r) > limit {
		return string(r[:max(0, limit-1)]) + "…"
	}
	return text
}
