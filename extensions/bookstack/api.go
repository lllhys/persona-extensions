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
	errToken       = errors.New("BookStack rejected the API token; replace it in extension settings")
	errUnavailable = errors.New("BookStack could not be reached; try again later")
	errNotFound    = errors.New("BookStack could not find that book or page")
	errRateLimited = errors.New("BookStack rate limit reached; try again later")
)

type client struct {
	base, tokenID, tokenSecret string
	http                       *http.Client
}

func newClient(base, tokenID, tokenSecret string) *client {
	return &client{base: strings.TrimRight(base, "/"), tokenID: strings.TrimSpace(tokenID), tokenSecret: strings.TrimSpace(tokenSecret), http: &http.Client{
		Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	if c.tokenID == "" || c.tokenSecret == "" {
		return errors.New("BookStack Token ID and Secret are not configured")
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
	req.Header.Set("Authorization", "Token "+c.tokenID+":"+c.tokenSecret)
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
		return errors.New("BookStack returned an oversized response")
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
			return errors.New("BookStack returned invalid JSON")
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
		return errors.New("BookStack rejected the operation; check the fields and permissions")
	default:
		return fmt.Errorf("BookStack returned HTTP %d", res.StatusCode)
	}
}

func validID(id int64) error {
	if id <= 0 {
		return errors.New("book or page ID must be positive")
	}
	return nil
}

func validOffset(offset int) error {
	if offset < 0 {
		return errors.New("offset must not be negative")
	}
	return nil
}

func (c *client) books(ctx context.Context, offset int) (listPage[book], error) {
	if err := validOffset(offset); err != nil {
		return listPage[book]{}, err
	}
	q := url.Values{"count": {"50"}, "offset": {strconv.Itoa(offset)}, "sort": {"+name"}}
	var out listPage[book]
	err := c.do(ctx, http.MethodGet, "/api/books", q, nil, &out)
	return out, err
}

func (c *client) book(ctx context.Context, id int64) (book, error) {
	if err := validID(id); err != nil {
		return book{}, err
	}
	var out book
	err := c.do(ctx, http.MethodGet, "/api/books/"+strconv.FormatInt(id, 10), nil, nil, &out)
	return out, err
}

func (c *client) pages(ctx context.Context, bookID int64, offset int) (listPage[page], error) {
	if err := validID(bookID); err != nil {
		return listPage[page]{}, err
	}
	if err := validOffset(offset); err != nil {
		return listPage[page]{}, err
	}
	q := url.Values{"count": {"50"}, "offset": {strconv.Itoa(offset)}, "sort": {"+name"}, "filter[book_id]": {strconv.FormatInt(bookID, 10)}}
	var out listPage[page]
	err := c.do(ctx, http.MethodGet, "/api/pages", q, nil, &out)
	return out, err
}

func (c *client) search(ctx context.Context, query string, pageNumber int) (listPage[searchResult], error) {
	if strings.TrimSpace(query) == "" || len([]rune(query)) > 200 || strings.ContainsRune(query, 0) {
		return listPage[searchResult]{}, errors.New("search text must be nonempty and at most 200 characters")
	}
	if pageNumber < 1 {
		return listPage[searchResult]{}, errors.New("search page must be positive")
	}
	q := url.Values{"query": {query}, "page": {strconv.Itoa(pageNumber)}, "count": {"50"}}
	var out listPage[searchResult]
	err := c.do(ctx, http.MethodGet, "/api/search", q, nil, &out)
	return out, err
}

func (c *client) page(ctx context.Context, id int64) (page, error) {
	if err := validID(id); err != nil {
		return page{}, err
	}
	var out page
	err := c.do(ctx, http.MethodGet, "/api/pages/"+strconv.FormatInt(id, 10), nil, nil, &out)
	return out, err
}

func (c *client) markdown(ctx context.Context, id int64) (string, error) {
	if err := validID(id); err != nil {
		return "", err
	}
	var out page
	if err := c.do(ctx, http.MethodGet, "/api/pages/"+strconv.FormatInt(id, 10), nil, nil, &out); err != nil {
		return "", err
	}
	if out.Markdown != "" {
		return out.Markdown, nil
	}
	return "", errors.New("this BookStack page has no Markdown source; edit it in BookStack rather than converting HTML here")
}

func (c *client) pageText(ctx context.Context, id int64) (string, error) {
	var out string
	if err := validID(id); err != nil {
		return "", err
	}
	err := c.do(ctx, http.MethodGet, "/api/pages/"+strconv.FormatInt(id, 10)+"/export/plaintext", nil, nil, &out)
	return out, err
}

func (c *client) createPage(ctx context.Context, bookID int64, name, markdown string) (page, error) {
	if err := validID(bookID); err != nil {
		return page{}, err
	}
	var out page
	err := c.do(ctx, http.MethodPost, "/api/pages", nil, map[string]any{"book_id": bookID, "name": name, "markdown": markdown}, &out)
	return out, err
}

func (c *client) updatePage(ctx context.Context, id int64, name, markdown string) (page, error) {
	if err := validID(id); err != nil {
		return page{}, err
	}
	body := map[string]any{"markdown": markdown}
	if name != "" {
		body["name"] = name
	}
	var out page
	err := c.do(ctx, http.MethodPut, "/api/pages/"+strconv.FormatInt(id, 10), nil, body, &out)
	return out, err
}

func safePageLink(raw string) string {
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
