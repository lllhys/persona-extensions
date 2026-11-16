package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The client for AMap's Web Service API (https://restapi.amap.com). Every request goes through get:
//
//   - answers are kept for a time that depends on what they are (cacheFor), and the same request asked again while one
//     is on its way waits for that one instead of asking twice;
//   - requests start at most three a second (gap), and one that would wait more than queueMost for its turn is refused;
//   - a per-second or per-minute refusal (ACCESS_TOO_FREQUENT and the QPS codes) is retried once after retryWait;
//   - when AMap says a group's daily or monthly allowance is used up, nothing of that group is asked again until it
//     comes back (midnight Beijing time, or the 1st of next month), and the tools say so at once;
//   - every request that reached AMap is counted per group and month, for the status (usage.json in the data dir);
//   - the key goes only into the query string to AMap: errors are written without the URL and with the key replaced.

// apiBase is AMap's Web Service API (a variable so tests can point it at a fake).
var apiBase = "https://restapi.amap.com"

// answerMost is how many bytes of an answer are read at most.
const answerMost = int64(2 << 20)

// beijing is the time AMap's allowances reset by (China has no daylight saving).
var beijing = time.FixedZone("CST", 8*3600)

// group is one of AMap's quota groups: the calls in one share an allowance.
type group string

const (
	groupSearch  group = "search"  // 基础搜索服务: keyword and around search (POI 2.0)
	groupLBS     group = "lbs"     // 基础 LBS 服务: geocoding, reverse geocoding, route planning
	groupWeather group = "weather" // 天气
)

var groups = []group{groupSearch, groupLBS, groupWeather}

// label is the group's name in words.
func (g group) label() string {
	switch g {
	case groupSearch:
		return "搜索"
	case groupLBS:
		return "地址和路线"
	}
	return "天气"
}

// tools is what still works while the group works, in words.
func (g group) tools() string {
	switch g {
	case groupSearch:
		return "地点搜索和周边搜索"
	case groupLBS:
		return "地址查询和路线规划"
	}
	return "天气"
}

var (
	errSlow      = errors.New("高德回得太慢，过一会儿再试")
	errBadAnswer = errors.New("高德的回答读不懂")
	errBusy      = &ext.Error{Code: wire.ConnectorErrorCodeRateLimited, Message: "同时要查的太多了，过几秒再试", Data: json.RawMessage(`{"retry_after":3}`)}
	errNoKey     = errors.New("还没填高德 Key：在 设置 › 扩展 › 高德地图 里填「Web服务」类型的 Key")
)

// client is the HTTP client with its cache, pacing, allowances and counts.
type client struct {
	base      string
	key       string
	http      *http.Client
	now       func() time.Time
	sleep     func(context.Context, time.Duration) error
	gap       time.Duration // between two request starts
	queueMost time.Duration // the longest a request waits for its turn
	retryWait time.Duration // before the one retry of a per-second refusal
	logf      func(string, ...any)

	slotMu sync.Mutex
	next   time.Time // when the next request may start

	mu      sync.Mutex
	cache   map[string]cached
	flights map[string]*flight
	usage   usage
	file    string // usage.json, empty when there is no data dir
	keyErr  *amapError
	lastOK  time.Time

	saveMu sync.Mutex
}

type cached struct {
	body []byte
	at   time.Time
}

type flight struct {
	done chan struct{}
	body []byte
	err  error
}

// cacheMost is how many answers are kept at most.
const cacheMost = 2000

// usage is what usage.json keeps: this month's counts and the groups AMap refused for their allowance.
type usage struct {
	Month   string              `json:"month"` // 2006-01 in Beijing time
	Key     string              `json:"key"`   // a short fingerprint of the key, so another key starts again
	Counts  map[group]int       `json:"counts"`
	Blocked map[group]time.Time `json:"blocked,omitempty"` // until when
	Codes   map[group]string    `json:"codes,omitempty"`   // what AMap said
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newClient(key, dataDir string, logf func(string, ...any)) *client {
	c := &client{base: apiBase, key: key,
		// The default transport, so the proxy a sandboxed extension is given still applies.
		http: &http.Client{Timeout: 8 * time.Second},
		now:  time.Now, sleep: sleepCtx, gap: time.Second / 3, queueMost: 3 * time.Second, retryWait: time.Second,
		logf: logf, cache: map[string]cached{}, flights: map[string]*flight{}}
	if dataDir != "" {
		c.file = filepath.Join(dataDir, "usage.json")
	}
	c.load()
	return c
}

// fingerprint is a short, one-way name for a key.
func fingerprint(key string) string {
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}

func (c *client) month() string { return c.now().In(beijing).Format("2006-01") }

// load reads usage.json; a missing or unreadable file, another month or another key is a fresh start.
func (c *client) load() {
	c.usage = usage{Month: c.month(), Key: fingerprint(c.key), Counts: map[group]int{}}
	if c.file == "" {
		return
	}
	b, err := os.ReadFile(c.file)
	if err != nil {
		return
	}
	var u usage
	if json.Unmarshal(b, &u) != nil || u.Key != c.usage.Key {
		return
	}
	if u.Month == c.usage.Month && u.Counts != nil {
		c.usage.Counts = u.Counts
	}
	// A block reaches into the next month only as far as its own end.
	for g, until := range u.Blocked {
		if until.After(c.now()) {
			if c.usage.Blocked == nil {
				c.usage.Blocked, c.usage.Codes = map[group]time.Time{}, map[group]string{}
			}
			c.usage.Blocked[g], c.usage.Codes[g] = until, u.Codes[g]
		}
	}
}

// save writes usage.json (0600, replaced whole); a failure is logged and the counts stay in memory.
func (c *client) save() {
	if c.file == "" {
		return
	}
	c.mu.Lock()
	b, err := json.Marshal(c.usage)
	c.mu.Unlock()
	if err != nil {
		return
	}
	c.saveMu.Lock()
	defer c.saveMu.Unlock()
	tmp := c.file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		c.logf("amap: usage not saved: %v", err)
		return
	}
	if err := os.Rename(tmp, c.file); err != nil {
		c.logf("amap: usage not saved: %v", err)
	}
}

// rollover starts a new month's counts; c.mu is held.
func (c *client) rollover() {
	if m := c.month(); c.usage.Month != m {
		c.usage.Month, c.usage.Counts = m, map[group]int{}
	}
}

// count notes one request of g that reached AMap.
func (c *client) count(g group) {
	c.mu.Lock()
	c.rollover()
	c.usage.Counts[g]++
	c.mu.Unlock()
	c.save()
}

// blockedUntil is when g may be asked again, if AMap refused it for its allowance.
func (c *client) blockedUntil(g group) (time.Time, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	until, ok := c.usage.Blocked[g]
	if !ok {
		return time.Time{}, "", false
	}
	if !until.After(c.now()) {
		delete(c.usage.Blocked, g)
		delete(c.usage.Codes, g)
		return time.Time{}, "", false
	}
	return until, c.usage.Codes[g], true
}

func (c *client) block(g group, until time.Time, code string) {
	c.mu.Lock()
	if c.usage.Blocked == nil {
		c.usage.Blocked, c.usage.Codes = map[group]time.Time{}, map[group]string{}
	}
	c.usage.Blocked[g], c.usage.Codes[g] = until, code
	c.mu.Unlock()
	c.save()
}

// nextMidnight is the next 00:00 in Beijing; nextMonth the 1st of next month there.
func nextMidnight(t time.Time) time.Time {
	b := t.In(beijing)
	return time.Date(b.Year(), b.Month(), b.Day()+1, 0, 0, 0, 0, beijing)
}

func nextMonth(t time.Time) time.Time {
	b := t.In(beijing)
	return time.Date(b.Year(), b.Month()+1, 1, 0, 0, 0, 0, beijing)
}

// quotaError says a group's allowance is used up, until when, and what still works.
func (c *client) quotaError(g group, until time.Time, code string) error {
	var when string
	if until.Day() == 1 && until.Sub(c.now()) > 24*time.Hour {
		when = "下个月 1 日恢复，或在高德控制台加额度"
	} else {
		when = "北京时间明天 0 点恢复"
	}
	var others []string
	for _, o := range groups {
		if _, _, blocked := c.blockedUntil(o); o != g && !blocked {
			others = append(others, o.tools())
		}
	}
	msg := fmt.Sprintf("高德的「%s」额度用完了（高德说 %s），%s。", g.label(), code, when)
	if len(others) > 0 {
		msg += strings.Join(others, "、") + "还能用。"
	}
	return &ext.Error{Code: wire.ConnectorErrorCodeRateLimited, Message: msg,
		Data: json.RawMessage(fmt.Sprintf(`{"retry_after":%d}`, int(until.Sub(c.now()).Seconds())+1))}
}

// wait takes the next start slot, at most queueMost away.
func (c *client) wait(ctx context.Context) error {
	c.slotMu.Lock()
	now := c.now()
	slot := c.next
	if slot.Before(now) {
		slot = now
	}
	if slot.Sub(now) > c.queueMost {
		c.slotMu.Unlock()
		return errBusy
	}
	c.next = slot.Add(c.gap)
	c.slotMu.Unlock()
	return c.sleep(ctx, slot.Sub(now))
}

// cacheKey is a request without its key.
func cacheKey(path string, q url.Values) string { return path + "?" + q.Encode() }

func (c *client) fromCache(k string, ttl time.Duration) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[k]
	if !ok || c.now().Sub(e.at) >= ttl {
		return nil, false
	}
	return e.body, true
}

// keep caches an answer; c.mu is held. Past cacheMost the oldest goes.
func (c *client) keep(k string, body []byte) {
	if _, ok := c.cache[k]; !ok && len(c.cache) >= cacheMost {
		var oldest string
		var at time.Time
		for key, e := range c.cache {
			if oldest == "" || e.at.Before(at) {
				oldest, at = key, e.at
			}
		}
		delete(c.cache, oldest)
	}
	c.cache[k] = cached{body: body, at: c.now()}
}

// get asks AMap path with q (without the key) and answers the body of a successful answer, from the cache when an
// answer younger than ttl is there (ttl 0 always asks).
func (c *client) get(ctx context.Context, g group, path string, q url.Values, ttl time.Duration) ([]byte, error) {
	if c.key == "" {
		return nil, errNoKey
	}
	if until, code, blocked := c.blockedUntil(g); blocked {
		return nil, c.quotaError(g, until, code)
	}
	k := cacheKey(path, q)
	if ttl > 0 {
		if body, ok := c.fromCache(k, ttl); ok {
			return body, nil
		}
	}
	c.mu.Lock()
	if f, ok := c.flights[k]; ok {
		c.mu.Unlock()
		select {
		case <-f.done:
			return f.body, f.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f := &flight{done: make(chan struct{})}
	c.flights[k] = f
	c.mu.Unlock()

	f.body, f.err = c.fetch(ctx, g, path, q)
	c.mu.Lock()
	delete(c.flights, k)
	if f.err == nil {
		c.lastOK = c.now()
		if ttl > 0 {
			c.keep(k, f.body)
		}
	}
	c.mu.Unlock()
	close(f.done)
	return f.body, f.err
}

// fetch asks AMap, retrying a per-second refusal once.
func (c *client) fetch(ctx context.Context, g group, path string, q url.Values) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		body, err := c.ask(ctx, path, q, c.key)
		if err != nil {
			return nil, err
		}
		c.count(g)
		e := checkAnswer(body)
		if e == nil {
			return body, nil
		}
		switch e.kind() {
		case kindBusy:
			if attempt == 0 {
				if err := c.sleep(ctx, c.retryWait); err != nil {
					return nil, err
				}
				continue
			}
			return nil, &ext.Error{Code: wire.ConnectorErrorCodeRateLimited, Message: e.msg, Data: json.RawMessage(`{"retry_after":5}`)}
		case kindDaily:
			until := nextMidnight(c.now())
			c.block(g, until, e.info)
			c.logf("amap: %s allowance used up (%s), paused until %s", g, e.code, until.Format(time.RFC3339))
			return nil, c.quotaError(g, until, e.info)
		case kindPlan:
			until := nextMonth(c.now())
			c.block(g, until, e.info)
			c.logf("amap: %s plan used up (%s), paused until %s", g, e.code, until.Format(time.RFC3339))
			return nil, c.quotaError(g, until, e.info)
		case kindKey:
			c.mu.Lock()
			c.keyErr = e
			c.mu.Unlock()
			c.logf("amap: the key was refused (%s)", e.code)
		}
		return nil, e
	}
}

// ask sends one request with key and answers the body; the error never holds the URL or the key.
func (c *client) ask(ctx context.Context, path string, q url.Values, key string) ([]byte, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	full := url.Values{}
	for k, v := range q {
		full[k] = v
	}
	full.Set("key", key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path+"?"+full.Encode(), nil)
	if err != nil {
		return nil, errors.New("请求写不出来")
	}
	req.Header.Set("User-Agent", "persona-amap/"+about.Version)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.netError(ctx, err, key)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, answerMost+1))
	if err != nil {
		return nil, c.netError(ctx, err, key)
	}
	if int64(len(body)) > answerMost {
		return nil, errBadAnswer
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("高德回了 HTTP %d，稍后再试", resp.StatusCode)
	}
	return body, nil
}

// netError words a failed request without its URL (which holds the key).
func (c *client) netError(ctx context.Context, err error, key string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return errSlow
		}
		err = ue.Err
	}
	msg := err.Error()
	if key != "" {
		msg = strings.ReplaceAll(msg, key, "***")
	}
	c.logf("amap: request failed: %s", clip(msg, 200))
	return fmt.Errorf("连不上高德（%s），稍后再试", clip(msg, 120))
}

// try asks one cheap request with another key, for a settings check: nothing is cached, counted or blocked.
func (c *client) try(ctx context.Context, key string) error {
	body, err := c.ask(ctx, "/v3/geocode/geo", url.Values{"address": {checkAddress}}, key)
	if err != nil {
		return err
	}
	if e := checkAnswer(body); e != nil && e.kind() != kindDaily && e.kind() != kindPlan && e.kind() != kindBusy {
		return e
	}
	// A used-up allowance or a busy moment is still a key AMap knows.
	return nil
}

// checkAddress is the address a settings check and the start-up check look up: a public landmark.
const checkAddress = "北京市天安门"

// status is how the client is doing, from memory.
func (c *client) state() (keyErr *amapError, lastOK time.Time, u usage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rollover()
	u = c.usage
	u.Counts = make(map[group]int, len(c.usage.Counts))
	for g, n := range c.usage.Counts {
		u.Counts[g] = n
	}
	u.Blocked = map[group]time.Time{}
	for g, t := range c.usage.Blocked {
		if t.After(c.now()) {
			u.Blocked[g] = t
		}
	}
	return c.keyErr, c.lastOK, u
}
