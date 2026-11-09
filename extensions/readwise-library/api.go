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
	errToken       = errors.New("Readwise rejected the API token; replace it in extension settings")
	errUnavailable = errors.New("Readwise Library could not be reached; try again later")
	errNotFound    = errors.New("Readwise Library could not find that book or highlight")
	errRateLimited = errors.New("Readwise rate limit reached; try again later")
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
		return errors.New("Readwise API token is not configured")
	}
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+"/api/v2"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+c.token)
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
		return errors.New("Readwise returned an oversized response")
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated:
		if out == nil || len(data) == 0 {
			return nil
		}
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("Readwise returned invalid JSON")
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
		return errors.New("Readwise rejected the operation; check the fields and permissions")
	default:
		return fmt.Errorf("Readwise returned HTTP %d", res.StatusCode)
	}
}

func validID(id int64) error {
	if id <= 0 {
		return errors.New("book or highlight ID must be positive")
	}
	return nil
}

func validPage(page int) error {
	if page < 1 {
		return errors.New("page must be positive")
	}
	return nil
}

func (c *client) books(ctx context.Context, category string, pageNumber int) (page[book], error) {
	if category != "" && category != "books" && category != "articles" && category != "tweets" && category != "podcasts" && category != "supplementals" {
		return page[book]{}, errors.New("unsupported book category")
	}
	if err := validPage(pageNumber); err != nil {
		return page[book]{}, err
	}
	q := url.Values{"page_size": {"50"}, "page": {strconv.Itoa(pageNumber)}}
	if category != "" {
		q.Set("category", category)
	}
	var out page[book]
	err := c.do(ctx, http.MethodGet, "/books/", q, nil, &out)
	return out, err
}

func (c *client) book(ctx context.Context, id int64) (book, error) {
	if err := validID(id); err != nil {
		return book{}, err
	}
	var out book
	err := c.do(ctx, http.MethodGet, "/books/"+strconv.FormatInt(id, 10)+"/", nil, nil, &out)
	return out, err
}

func (c *client) highlights(ctx context.Context, bookID int64, pageNumber int) (page[highlight], error) {
	if err := validID(bookID); err != nil {
		return page[highlight]{}, err
	}
	if err := validPage(pageNumber); err != nil {
		return page[highlight]{}, err
	}
	q := url.Values{"page_size": {"50"}, "page": {strconv.Itoa(pageNumber)}, "book_id": {strconv.FormatInt(bookID, 10)}}
	var out page[highlight]
	err := c.do(ctx, http.MethodGet, "/highlights/", q, nil, &out)
	return out, err
}

func (c *client) highlight(ctx context.Context, id int64) (highlight, error) {
	if err := validID(id); err != nil {
		return highlight{}, err
	}
	var out highlight
	err := c.do(ctx, http.MethodGet, "/highlights/"+strconv.FormatInt(id, 10)+"/", nil, nil, &out)
	return out, err
}

func (c *client) createHighlight(ctx context.Context, text, title, author, note, sourceURL string) error {
	body := map[string]any{"text": text, "title": title, "author": author, "source_type": "persona", "category": "articles"}
	if note != "" {
		body["note"] = note
	}
	if sourceURL != "" {
		body["source_url"] = sourceURL
	}
	return c.do(ctx, http.MethodPost, "/highlights/", nil, map[string]any{"highlights": []map[string]any{body}}, nil)
}

func (c *client) updateNote(ctx context.Context, id int64, note string) (highlight, error) {
	if err := validID(id); err != nil {
		return highlight{}, err
	}
	var out highlight
	err := c.do(ctx, http.MethodPatch, "/highlights/"+strconv.FormatInt(id, 10)+"/", nil, map[string]string{"note": note}, &out)
	return out, err
}

func (c *client) addBookTag(ctx context.Context, bookID int64, name string) (tag, error) {
	if err := validID(bookID); err != nil {
		return tag{}, err
	}
	var out tag
	err := c.do(ctx, http.MethodPost, "/books/"+strconv.FormatInt(bookID, 10)+"/tags/", nil, map[string]string{"name": name}, &out)
	return out, err
}

func nextPage(next string, current int) int {
	if next == "" {
		return 0
	}
	u, err := url.Parse(next)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(u.Query().Get("page"))
	if err != nil || n <= current {
		return 0
	}
	return n
}

func safeLibraryLink(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() != "readwise.io" || u.User != nil {
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
