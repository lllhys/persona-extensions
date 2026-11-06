package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The site, read gently: one request at a time and at least requestGap apart (a burst gets a 429 from its
// firewall), answers kept for a while, and a pause after a 429.

// siteBase is the site (a variable so a test build can point it at a fake: -ldflags "-X main.siteBase=…").
var siteBase = "https://linux.do"

var userAgent = "persona-linuxdo/" + version + " (RSS reader)"

var (
	requestGap = 2 * time.Second
	listFor    = 5 * time.Minute  // a list is read again after this
	topicFor   = 10 * time.Minute // a topic is read again after this
	pauseFor   = 10 * time.Minute // after a 429
)

// cacheKeep bounds the cache: an answer past its per-kind TTL is pruned, then the oldest answers go.
const cacheKeep = 200

var (
	errRateLimited = errors.New("LINUX DO 说请求太多，过一会儿再试")
	errChallenged  = errors.New("LINUX DO 没让 hub 这台机器读（防火墙要浏览器验证）")
	errNoTopic     = errors.New("LINUX DO 上没有这个帖子（可能删了，或者要登录才能看）")
)

// site is the client with its pace and its cache.
type site struct {
	base string
	http *http.Client

	gate   sync.Mutex // one request at a time
	last   time.Time
	paused time.Time // no requests before this (after a 429)

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	body []byte
	at   time.Time
}

func newSite(base string) *site {
	return &site{base: base, http: &http.Client{Timeout: 20 * time.Second}, cache: map[string]cached{}}
}

// get reads path, from the cache when it is younger than fresh (fresh 0 reads it again). When the read fails
// and an older copy is cached, it returns that copy together with the error.
func (s *site) get(ctx context.Context, path string, fresh time.Duration) ([]byte, error) {
	s.mu.Lock()
	c, ok := s.cache[path]
	s.mu.Unlock()
	if ok && fresh > 0 && time.Since(c.at) < fresh {
		return c.body, nil
	}
	body, err := s.fetch(ctx, path)
	if err != nil {
		if ok && !errors.Is(err, errNoTopic) { // an older copy beats an error page
			return c.body, err
		}
		return nil, err
	}
	s.mu.Lock()
	s.cache[path] = cached{body, time.Now()}
	if len(s.cache) > cacheKeep {
		s.prune(time.Now())
	}
	s.mu.Unlock()
	return body, nil
}

// prune drops answers past their per-kind TTL (a topic topicFor, anything else listFor); when the cache is
// still over cacheKeep the oldest answers go, so sustained reads cannot grow it past the cap. Caller holds mu.
func (s *site) prune(now time.Time) {
	for k, v := range s.cache {
		ttl := listFor
		if strings.HasPrefix(k, "/t/") {
			ttl = topicFor
		}
		if now.Sub(v.at) > ttl {
			delete(s.cache, k)
		}
	}
	for len(s.cache) > cacheKeep {
		oldest := ""
		for k, v := range s.cache {
			if oldest == "" || v.at.Before(s.cache[oldest].at) {
				oldest = k
			}
		}
		delete(s.cache, oldest)
	}
}

func (s *site) fetch(ctx context.Context, path string) ([]byte, error) {
	s.gate.Lock()
	defer s.gate.Unlock()
	if time.Now().Before(s.paused) {
		return nil, errRateLimited
	}
	if wait := requestGap - time.Since(s.last); wait > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	res, err := s.http.Do(req)
	s.last = time.Now()
	if err != nil {
		return nil, fmt.Errorf("连不上 LINUX DO：%w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("连不上 LINUX DO：%w", err)
	}
	// The firewall's page answers 403 or 503; a 200 topic or feed may just
	// mention the phrase.
	challenge := res.StatusCode != http.StatusOK && bytes.Contains(body[:min(len(body), 2048)], []byte("Just a moment"))
	switch {
	case res.StatusCode == http.StatusTooManyRequests:
		pause := pauseFor
		if secs, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && secs > 0 {
			pause = max(time.Duration(secs)*time.Second, time.Minute)
		}
		s.paused = time.Now().Add(pause)
		return nil, errRateLimited
	case challenge:
		return nil, errChallenged
	case res.StatusCode == http.StatusNotFound, res.StatusCode == http.StatusForbidden:
		// Only a topic read means "this topic is gone"; the same status on a list feed is an ordinary fetch failure.
		if strings.HasPrefix(path, "/t/") {
			return nil, errNoTopic
		}
		return nil, fmt.Errorf("LINUX DO：HTTP %d", res.StatusCode)
	case res.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("LINUX DO：HTTP %d", res.StatusCode)
	}
	return body, nil
}
