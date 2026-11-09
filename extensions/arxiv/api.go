package main

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var errUnavailable = errors.New("arXiv could not be reached; try again later")

type client struct {
	base string
	http *http.Client
}

func newClient(base string) *client {
	return &client{base: strings.TrimRight(base, "/"), http: &http.Client{
		Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *client) query(ctx context.Context, query string, recent bool, start int) (feed, error) {
	if len([]rune(query)) > 200 || strings.ContainsRune(query, 0) {
		return feed{}, errors.New("search text must be at most 200 characters")
	}
	if start < 0 {
		return feed{}, errors.New("start must not be negative")
	}
	q := url.Values{"start": {strconv.Itoa(start)}, "max_results": {"20"}}
	if recent {
		q.Set("search_query", "all:"+query)
		q.Set("sortBy", "submittedDate")
		q.Set("sortOrder", "descending")
	} else {
		if strings.TrimSpace(query) == "" {
			return feed{}, errors.New("search text is required")
		}
		q.Set("search_query", "all:"+query)
		q.Set("sortBy", "relevance")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/query", nil)
	if err != nil {
		return feed{}, err
	}
	req.Header.Set("Accept", "application/atom+xml")
	req.URL.RawQuery = q.Encode()
	res, err := c.http.Do(req)
	if err != nil {
		return feed{}, fmt.Errorf("%w: %v", errUnavailable, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20+1))
	if err != nil {
		return feed{}, errUnavailable
	}
	if len(data) > 4<<20 {
		return feed{}, errors.New("arXiv returned an oversized response")
	}
	if res.StatusCode != http.StatusOK {
		return feed{}, fmt.Errorf("arXiv returned HTTP %d", res.StatusCode)
	}
	var out feed
	if err := xml.Unmarshal(data, &out); err != nil {
		return feed{}, errors.New("arXiv returned invalid Atom XML")
	}
	if len(out.Entries) > 100 {
		return feed{}, errors.New("arXiv returned too many entries")
	}
	return out, nil
}

func (c *client) paper(ctx context.Context, id string) (paper, error) {
	if len(id) == 0 || len(id) > 100 || strings.ContainsAny(id, "\x00\r\n/?#") {
		return paper{}, errors.New("invalid arXiv ID")
	}
	q := url.Values{"id_list": {id}, "max_results": {"1"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/query", nil)
	if err != nil {
		return paper{}, err
	}
	req.URL.RawQuery = q.Encode()
	res, err := c.http.Do(req)
	if err != nil {
		return paper{}, fmt.Errorf("%w: %v", errUnavailable, err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 2<<20+1))
	if err != nil {
		return paper{}, errUnavailable
	}
	if len(data) > 2<<20 {
		return paper{}, errors.New("arXiv returned an oversized response")
	}
	if res.StatusCode != http.StatusOK {
		return paper{}, fmt.Errorf("arXiv returned HTTP %d", res.StatusCode)
	}
	var out feed
	if err := xml.Unmarshal(data, &out); err != nil {
		return paper{}, errors.New("arXiv returned invalid Atom XML")
	}
	if len(out.Entries) != 1 {
		return paper{}, errors.New("arXiv could not find that paper")
	}
	return out.Entries[0], nil
}

func paperURL(item paper) string {
	u, err := url.Parse(item.ID)
	if err != nil || u.Scheme != "https" || u.Hostname() != "arxiv.org" || u.User != nil {
		return ""
	}
	return item.ID
}

func clipText(text string, limit int) string {
	r := []rune(text)
	if len(r) > limit {
		return string(r[:max(0, limit-1)]) + "…"
	}
	return text
}
