package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// The site: the public Firebase API, which has no published rate limit but answers one item per request, so a list
// is its ids and then its items, read a few at a time. Answers are kept for a while, and an older copy stands in when
// a read fails. The API's paths and answers are all known here and in item.go.

// siteBase is the API (a variable so a test can point it at a fake).
var siteBase = "https://hacker-news.firebaseio.com/v0"

// webBase is where a story is for people (the links in the page and the tools).
const webBase = "https://news.ycombinator.com"

var userAgent = "persona-hackernews/" + about.Version

var (
	listFor      = 5 * time.Minute  // a list's ids are read again after this
	itemFor      = 5 * time.Minute  // an item (its score and comment count change) is read again after this
	refreshFloor = 30 * time.Second // a refresh this soon after a read answers from the cache
	parallel     = 8                // requests at a time
	cacheEntries = 2000
	answerMost   = int64(2 << 20)
)

var (
	errSlow      = errors.New("Hacker News answered too slowly; try again in a while")
	errBadAnswer = errors.New("Hacker News sent an answer that could not be read")
	errNotFound  = errors.New("Hacker News has no such item")
)

// site is the client with its cache.
type site struct {
	base string
	http *http.Client
	gate chan struct{} // at most parallel requests at once

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	body []byte
	at   time.Time
}

// reading is what get answers.
type reading struct {
	body  []byte
	at    time.Time // when it was read from the site
	live  bool      // this call read it, not the cache
	stale error     // why an older copy stands in for a failed read
}

func newSite(base string) *site {
	// The default transport, so the proxy a sandboxed extension is given still applies.
	return &site{base: base, http: &http.Client{Timeout: 15 * time.Second}, gate: make(chan struct{}, parallel), cache: map[string]cached{}}
}

func listPath(endpoint string) string { return "/" + endpoint + ".json" }
func itemPath(id int64) string        { return fmt.Sprintf("/item/%d.json", id) }
func itemURL(id int64) string         { return fmt.Sprintf("%s/item?id=%d", webBase, id) }

// get reads path, from the cache when it is younger than fresh (0 always reads). When the site fails, an older copy
// stands in and says why in stale, unless the thing is gone or the caller gave up.
func (s *site) get(ctx context.Context, path string, fresh time.Duration) (reading, error) {
	old, had := s.lookup(path)
	if had && fresh > 0 && time.Since(old.at) < fresh {
		return reading{body: old.body, at: old.at}, nil
	}
	body, err := s.fetch(ctx, path)
	if err == nil {
		now := time.Now()
		s.store(path, body, now)
		return reading{body: body, at: now, live: true}, nil
	}
	if had && !errors.Is(err, errNotFound) && !errors.Is(err, context.Canceled) {
		return reading{body: old.body, at: old.at, stale: err}, nil
	}
	return reading{}, err
}

func (s *site) fetch(ctx context.Context, path string) ([]byte, error) {
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return nil, requestError(ctx.Err())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	res, err := s.http.Do(req)
	if err != nil {
		return nil, requestError(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, answerMost+1))
	if err != nil {
		return nil, requestError(err)
	}
	switch {
	case res.StatusCode == http.StatusNotFound:
		return nil, errNotFound
	case res.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("Hacker News answered HTTP %d", res.StatusCode)
	case int64(len(body)) > answerMost || !json.Valid(body):
		return nil, errBadAnswer
	case string(body) == "null": // the API's answer for an id it does not have
		return nil, errNotFound
	}
	return body, nil
}

// requestError says a failed request in words: too slow, the caller gone, or no way there.
func requestError(err error) error {
	var neterr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &neterr) && neterr.Timeout():
		return errSlow
	}
	return fmt.Errorf("cannot reach Hacker News: %w", err)
}

func (s *site) lookup(path string) (cached, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cache[path]
	return c, ok
}

// store keeps an answer; past cacheEntries the oldest go first.
func (s *site) store(path string, body []byte, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[path] = cached{body: body, at: at}
	for len(s.cache) > cacheEntries {
		var oldest string
		var when time.Time
		for p, c := range s.cache {
			if oldest == "" || c.at.Before(when) {
				oldest, when = p, c.at
			}
		}
		delete(s.cache, oldest)
	}
}
