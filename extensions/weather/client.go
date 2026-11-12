package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// The client for Open-Meteo: one request at a time, answers kept for 10 minutes (a refresh reads again), and a
// timeout of 8 seconds. The endpoints and their answers are all known here and in forecast.go, air.go and geo.go, so
// a change of API stays in these files.

// The three Open-Meteo endpoints (variables so a test build can point them at a fake).
var (
	forecastBase = "https://api.open-meteo.com"
	airBase      = "https://air-quality-api.open-meteo.com"
	geoBase      = "https://geocoding-api.open-meteo.com"
)

// userAgent names the extension to the API.
var userAgent = "persona-weather/" + about.Version

// answerMost is how many bytes of an answer are read at most.
const answerMost = int64(2 << 20)

// cacheFor is how long an answer is reused before it is read again (the hub's 10-minute cache).
const cacheFor = 10 * time.Minute

// refreshFloor is how soon after a read a refresh reads again.
var refreshFloor = 30 * time.Second

var (
	errSlow      = errors.New("Open-Meteo 回得太慢，过一会儿再试")
	errBadAnswer = errors.New("Open-Meteo 的回答读不懂")
	errNoCity    = errors.New("Open-Meteo 没搜到这个城市")
)

// client is the HTTP client with its cache.
type client struct {
	forecast, air, geo string // the base URLs
	http               *http.Client

	gate chan struct{} // holds the one request; waiting for it ends with the caller's ctx

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	body []byte
	at   time.Time
}

func newClient() *client {
	// The default transport, so the proxy a sandboxed extension is given still applies.
	return &client{forecast: forecastBase, air: airBase, geo: geoBase,
		http: &http.Client{Timeout: 8 * time.Second}, gate: make(chan struct{}, 1), cache: map[string]cached{}}
}

// reading is what get answers.
type reading struct {
	body []byte
	at   time.Time // when it was read
}

// get reads base+path, from the cache when it is younger than fresh (0 always reads).
func (c *client) get(ctx context.Context, base, path string, fresh time.Duration) (reading, error) {
	full := base + path
	if rd, ok := c.young(full, fresh); ok {
		return rd, nil
	}
	rd, err := c.read(ctx, full, fresh)
	if err != nil {
		return reading{}, err
	}
	return rd, nil
}

// young is the cached copy of full if it is younger than fresh.
func (c *client) young(full string, fresh time.Duration) (reading, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cc, ok := c.cache[full]
	if !ok || fresh <= 0 || time.Since(cc.at) >= fresh {
		return reading{}, false
	}
	return reading{body: cc.body, at: cc.at}, true
}

// read holds the gate for one request; a copy that came in while it waited saves the request.
func (c *client) read(ctx context.Context, full string, fresh time.Duration) (reading, error) {
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return reading{}, requestError(ctx.Err())
	}
	if rd, ok := c.young(full, fresh); ok {
		return rd, nil
	}
	body, err := c.fetch(ctx, full)
	if err != nil {
		return reading{}, err
	}
	now := time.Now()
	c.store(full, body, now)
	return reading{body: body, at: now}, nil
}

// fetch does one GET and turns the answer into its body or what is wrong with it.
func (c *client) fetch(ctx context.Context, full string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, requestError(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, answerMost))
	if err != nil {
		return nil, requestError(err)
	}
	switch {
	case res.StatusCode == http.StatusNotFound:
		return nil, errNoCity
	case res.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("Open-Meteo：HTTP %d", res.StatusCode)
	case !validJSONObject(body):
		return nil, errBadAnswer
	case isErrorBody(body):
		return nil, errBadAnswer
	}
	return body, nil
}

// isErrorBody says whether a 200 body is Open-Meteo's error object, so it never parses as a page of zeros.
func isErrorBody(body []byte) bool {
	var p struct {
		Error bool `json:"error"`
	}
	return json.Unmarshal(body, &p) == nil && p.Error
}

// validJSONObject says whether body is a JSON object, so a firewall's page never replaces a good copy in the cache.
func validJSONObject(body []byte) bool {
	body = trimSpaceBytes(body)
	return len(body) > 0 && body[0] == '{'
}

func trimSpaceBytes(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\n' || b[i] == '\r' || b[i] == '\t') {
		i++
	}
	for i < j && (b[j-1] == ' ' || b[j-1] == '\n' || b[j-1] == '\r' || b[j-1] == '\t') {
		j--
	}
	return b[i:j]
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
	return fmt.Errorf("连不上 Open-Meteo：%w", err)
}

func (c *client) store(full string, body []byte, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[full] = cached{body, at}
}

// geoURL builds the geocoding search URL. The name goes in after QueryEscape, so it can carry any characters.
func geoURL(name string) string {
	return "/v1/search?name=" + url.QueryEscape(name) + "&count=5&language=zh"
}

// forecastURL builds the forecast URL for a city: today and the next six days, the next hours, and the current values.
func forecastURL(c City) string {
	return "/v1/forecast?latitude=" + coord(c.Latitude) + "&longitude=" + coord(c.Longitude) +
		"&current=temperature_2m,apparent_temperature,relative_humidity_2m,weather_code,wind_speed_10m,wind_direction_10m" +
		"&hourly=temperature_2m,weather_code,precipitation_probability" +
		"&daily=weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max,sunrise,sunset" +
		"&timezone=auto&forecast_days=7"
}

// airURL builds the air-quality URL for a city. Current conditions remain the default; outlook asks only for bounded hourly fields.
func airURL(c City) string {
	return "/v1/air-quality?latitude=" + coord(c.Latitude) + "&longitude=" + coord(c.Longitude) +
		"&current=pm2_5,pm10,european_aqi&timezone=auto"
}

func airOutlookURL(c City) string {
	return "/v1/air-quality?latitude=" + coord(c.Latitude) + "&longitude=" + coord(c.Longitude) +
		"&current=pm2_5,pm10,european_aqi" +
		"&hourly=pm2_5,pm10,european_aqi,alder_pollen,birch_pollen,grass_pollen,mugwort_pollen,olive_pollen,ragweed_pollen" +
		"&timezone=auto&forecast_days=5"
}

// coord writes a coordinate with enough digits and no exponent (the API rejects a scientific notation).
func coord(v float64) string {
	return strconv.FormatFloat(v, 'f', 4, 64)
}
