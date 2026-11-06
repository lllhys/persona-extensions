package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The site, read gently: one request at a time and at least requestGap apart, answers kept for a while, and no
// requests while its hourly quota is nearly spent. The API's paths and answers are all known here and in the types
// of feed.go and topic.go, so a change of API stays in these files.

// siteBase is the API (a variable so a test build can point it at a fake: -ldflags "-X main.siteBase=…").
var siteBase = "https://www.v2ex.com/api"

// webBase is where a topic is for people (the links in the page and the tools), whatever siteBase a test uses.
const webBase = "https://www.v2ex.com"

var userAgent = "persona-v2ex/" + about.Version

var (
	requestGap   = time.Second
	refreshFloor = 30 * time.Second // a refresh this soon after a read answers from the cache
	listFor      = 5 * time.Minute  // a list is read again after this
	topicFor     = 10 * time.Minute // a topic and its replies are read again after this
	pauseFor     = 10 * time.Minute // after a refusal that does not say how long
	pauseMost    = time.Hour        // the quota is hourly: never longer than this
)

// quotaFloor: no requests below this many left, until the quota resets.
const quotaFloor = 20

var (
	cacheEntries = 200
	cacheBytes   = 32 << 20 // a long topic's replies are megabytes
	answerMost   = int64(8 << 20)
)

var (
	errRateLimited = errors.New("V2EX 说请求太多，过一会儿再试")
	errChallenged  = errors.New("V2EX 没让 hub 这台机器读（防火墙要浏览器验证）")
	errSlow        = errors.New("V2EX 回得太慢，过一会儿再试")
	errBadAnswer   = errors.New("V2EX 的回答读不懂")
	errNotFound    = errors.New("V2EX 上没有这个") // a 404: errNoNode or errNoTopic by the time it is drawn
)

// site is the client with its pace and its cache.
type site struct {
	base string
	http *http.Client

	gate chan struct{} // holds the one request; waiting for it ends with the caller's ctx
	last time.Time     // the last request (the gate's)

	mu     sync.Mutex
	paused time.Time // no requests before this
	cache  map[string]cached
	size   int // bytes in the cache
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
	return &site{base: base, http: &http.Client{Timeout: 20 * time.Second}, gate: make(chan struct{}, 1), cache: map[string]cached{}}
}

func nodePath(name string) string { return "/topics/show.json?node_name=" + url.QueryEscape(name) }
func topicPath(id int64) string   { return fmt.Sprintf("/topics/show.json?id=%d", id) }
func repliesPath(id int64) string { return fmt.Sprintf("/replies/show.json?topic_id=%d", id) }
func topicURL(id int64) string    { return fmt.Sprintf("%s/t/%d", webBase, id) }

// get reads path, from the cache when it is younger than fresh (0 always reads). When the site fails, an older copy
// stands in and says why in stale, unless the thing is gone or the caller gave up.
func (s *site) get(ctx context.Context, path string, fresh time.Duration) (reading, error) {
	if rd, ok := s.young(path, fresh); ok {
		return rd, nil
	}
	rd, err := s.read(ctx, path, fresh)
	if err == nil {
		return rd, nil
	}
	if old, ok := s.lookup(path); ok && !errors.Is(err, errNotFound) && !errors.Is(err, context.Canceled) {
		return reading{body: old.body, at: old.at, stale: err}, nil
	}
	return reading{}, err
}

// young is the cached copy of path if it is younger than fresh.
func (s *site) young(path string, fresh time.Duration) (reading, bool) {
	c, ok := s.lookup(path)
	if !ok || fresh <= 0 || time.Since(c.at) >= fresh {
		return reading{}, false
	}
	return reading{body: c.body, at: c.at}, true
}

// read holds the gate for one request; a copy that came in while it waited (another page asked for the same thing)
// saves the request.
func (s *site) read(ctx context.Context, path string, fresh time.Duration) (reading, error) {
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return reading{}, requestError(ctx.Err())
	}
	if rd, ok := s.young(path, fresh); ok {
		return rd, nil
	}
	body, err := s.fetch(ctx, path)
	if err != nil {
		return reading{}, err
	}
	now := time.Now()
	s.store(path, body, now)
	return reading{body: body, at: now, live: true}, nil
}

func (s *site) fetch(ctx context.Context, path string) ([]byte, error) {
	if s.isPaused() {
		return nil, errRateLimited
	}
	if wait := requestGap - time.Since(s.last); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, requestError(ctx.Err())
		case <-timer.C:
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	res, err := s.http.Do(req)
	s.last = time.Now()
	if err != nil {
		return nil, requestError(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, answerMost))
	if err != nil {
		return nil, requestError(err)
	}
	s.noteQuota(res.Header)
	return s.judge(res, body)
}

// requestError says a failed request in words: too slow (the page's budget or the client's timeout), the caller gone,
// or no way there.
func requestError(err error) error {
	var neterr net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &neterr) && neterr.Timeout():
		return errSlow
	}
	return fmt.Errorf("连不上 V2EX：%w", err)
}

// judge turns an answer into its body or what is wrong with it. Every path this reads answers a JSON array, so
// anything else (a firewall's page, a cut answer, a refusal in a 200) never replaces a good copy in the cache.
func (s *site) judge(res *http.Response, body []byte) ([]byte, error) {
	var refusal struct {
		Message   string `json:"message"`
		RateLimit struct {
			Used    int   `json:"used"`
			Quota   int   `json:"quota"`
			ResetAt int64 `json:"reset_at"`
		} `json:"rate_limit"`
	}
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")) {
		_ = json.Unmarshal(body, &refusal)
	}
	spent := refusal.RateLimit.Quota > 0 && refusal.RateLimit.Used >= refusal.RateLimit.Quota
	switch {
	case res.StatusCode == http.StatusTooManyRequests, spent, strings.Contains(strings.ToLower(refusal.Message), "rate limit"):
		s.pause(time.Now().Add(limitPause(res.Header, refusal.RateLimit.ResetAt)))
		return nil, errRateLimited
	case bytes.Contains(body[:min(len(body), 2048)], []byte("Just a moment")), res.StatusCode == http.StatusForbidden:
		return nil, errChallenged
	case res.StatusCode == http.StatusNotFound:
		return nil, errNotFound
	case res.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("V2EX：HTTP %d", res.StatusCode)
	case !bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")), !json.Valid(body):
		return nil, errBadAnswer
	}
	return body, nil
}

// limitPause is how long a refusal holds requests: what it says, else until the quota resets, else pauseFor.
func limitPause(h http.Header, resetAt int64) time.Duration {
	if secs, err := strconv.Atoi(h.Get("Retry-After")); err == nil && secs > 0 {
		return max(time.Duration(min(secs, int(pauseMost.Seconds())))*time.Second, time.Minute)
	}
	if d := time.Until(time.Unix(resetAt, 0)); resetAt > 0 && d > 0 {
		return min(d, pauseMost)
	}
	return pauseFor
}

// noteQuota pauses before the site has to refuse. Only an answer from the site itself counts: the CDN replays the
// numbers of whoever asked first.
func (s *site) noteQuota(h http.Header) {
	switch h.Get("Cf-Cache-Status") {
	case "", "MISS", "BYPASS", "DYNAMIC", "EXPIRED":
	default:
		return
	}
	left, err := strconv.Atoi(h.Get("X-Rate-Limit-Remaining"))
	if err != nil || left >= quotaFloor {
		return
	}
	reset, err := strconv.ParseInt(h.Get("X-Rate-Limit-Reset"), 10, 64)
	if err != nil {
		return
	}
	if d := time.Until(time.Unix(reset, 0)); d > 0 {
		s.pause(time.Now().Add(min(d, pauseMost)))
	}
}

func (s *site) pause(until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if until.After(s.paused) {
		s.paused = until
	}
}

func (s *site) isPaused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().Before(s.paused)
}

func (s *site) lookup(path string) (cached, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cache[path]
	return c, ok
}

// store keeps an answer, and drops the oldest while there are too many or they are too big.
func (s *site) store(path string, body []byte, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.size += len(body) - len(s.cache[path].body)
	s.cache[path] = cached{body, at}
	for len(s.cache) > cacheEntries || s.size > cacheBytes {
		oldest := ""
		for k, c := range s.cache {
			if k != path && (oldest == "" || c.at.Before(s.cache[oldest].at)) {
				oldest = k
			}
		}
		if oldest == "" {
			return
		}
		s.size -= len(s.cache[oldest].body)
		delete(s.cache, oldest)
	}
}
