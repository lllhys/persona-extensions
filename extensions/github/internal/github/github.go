// Package github is the GitHub connector: the user's PRs, issues, reviews and checks, read with the token of a
// machine's gh login (docs/plan-connectors.md「四、GitHub 连接器」).
//
// The token lives only in this process's memory: it is never stored, logged, returned to a client, put in an error
// or handed to the assistant.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona-extensions/extensions/github/internal/connector"
)

// Config is the connector's configuration (the hub had it from hub.json's "github"; the extension makes it from its
// settings: Settings.Config).
type Config struct {
	Auth AuthConfig `json:"auth"`
	// Inbox: the defaults of what comes into the inbox until the user chooses in the app (rules.go).
	Inbox InboxOptions `json:"inbox,omitempty"`
	// Egress gives the proxy setting: an http:// proxy for everything sent to GitHub (the device flow on its web host,
	// the REST API, CI logs), for networks that cannot reach github.com directly; empty is direct.
	connector.Egress
}

// AuthConfig: whose gh login the hub reads, and for which host. Host is github.com when empty; a GitHub Enterprise
// host goes here and only here (not built yet: only github.com is tried).
type AuthConfig struct {
	FromMachine string `json:"from_machine,omitempty"`
	Host        string `json:"host,omitempty"`
}

// Configured says whether the hub should run the connector at all.
func (c *Config) Configured() bool { return c != nil && c.Auth.FromMachine != "" }

func (c *Config) host() string {
	if c.Auth.Host == "" {
		return "github.com"
	}
	return strings.ToLower(c.Auth.Host)
}

// Why a token could not be had (TokenSource's errors, errors.Is).
var (
	ErrNoMachine   = errors.New("the machine is not one of the hub's")
	ErrOffline     = errors.New("the machine is offline")
	ErrNodeTooOld  = errors.New("the machine's jarvisd is too old")
	ErrNoGh        = errors.New("gh is not installed")
	ErrNotSignedIn = errors.New("gh is not signed in")
	// ErrHubTooOld: the hub cannot hand an extension a computer's gh login (no connector.credentials.gh_token).
	ErrHubTooOld = errors.New("the hub cannot read a computer's gh login for an extension")
	// ErrNotGranted: the hub did not grant the extension gh_login (installed without it).
	ErrNotGranted = errors.New("the extension may not read a computer's gh login")
	// errBadProxy: the extension's proxy setting cannot be used; every request fails with it rather than going direct.
	errBadProxy = errors.New("扩展设置里的代理")
)

// refuse is the transport of a connector whose proxy is malformed.
type refuse struct{ err error }

func (r refuse) RoundTrip(*http.Request) (*http.Response, error) { return nil, r.err }

// httpClient is the connector's client: through the proxy setting when set, else as the environment says.
func httpClient(cfg Config) *http.Client {
	const timeout = 30 * time.Second
	cl, err := cfg.HTTPClient(timeout)
	if err != nil {
		return &http.Client{Timeout: timeout, Transport: refuse{fmt.Errorf("%w: %v", errBadProxy, err)}}
	}
	return cl
}

// TokenSource asks the machine for its gh login's token for host.
type TokenSource func(ctx context.Context, host string) (string, error)

// Connector is GitHub. Run keeps its status (and token) fresh.
type Connector struct {
	cfg     Config
	machine string // the machine's name, for messages
	fetch   TokenSource
	api     string
	client  *http.Client

	mu     sync.Mutex
	token  string
	status connector.Status
	poke   chan struct{}
	// rateLow: under a tenth of the hour's requests are left (X-RateLimit-Remaining); only notifications are polled
	// until rateReset.
	rateLow   bool
	rateReset time.Time

	inbox inboxState   // inbox.go
	opts  InboxOptions // rules.go
	etags etagCache    // thread.go: answers kept for If-None-Match, failed checks' logs, avatars

	// Where the token comes from (source.go): a computer's gh login (machineID, through fetch) or the device flow.
	source    string
	machineID string
	// scopes: what the token in use may do (X-OAuth-Scopes), known once GitHub answered with them.
	scopes      []string
	scopesKnown bool

	// The device flow's sign-in (login.go): GitHub's web host, the hub's store, what it keeps, a code waiting.
	web       string
	secrets   connector.Secrets
	dev       deviceLogin
	devLoaded bool
	pending   *devicePending

	lists listCache // pagedata.go: the page's lists

	// Running as an extension (extension.go): the user's triage rules, and how a computer's gh login is asked for.
	rules    string
	hubFetch func(ctx context.Context, machine, host string) (string, error)
}

// noteRate records how many requests GitHub still allows this hour.
func (c *Connector) noteRate(h http.Header) {
	left, err1 := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	limit, err2 := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	if err1 != nil || err2 != nil || limit <= 0 {
		return
	}
	reset, _ := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	c.mu.Lock()
	c.rateLow, c.rateReset = left*10 < limit, time.Unix(reset, 0)
	c.mu.Unlock()
}

// ratesLow says whether to save requests (only notifications are polled).
func (c *Connector) ratesLow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rateLow && time.Now().Before(c.rateReset)
}

// New is the connector for cfg, reading the token of machine (its display name) through fetch.
func New(cfg Config, machine string, fetch TokenSource) *Connector {
	api := "https://api.github.com"
	if h := cfg.host(); h != "github.com" {
		api = "https://" + h + "/api/v3"
	}
	waiting, source := "正在读取 "+machine+" 上的 gh 登录…", SourceMachine
	if fetch == nil {
		waiting, source = notSignedIn, SourceDevice
	}
	return &Connector{cfg: cfg, machine: machine, machineID: cfg.Auth.FromMachine, fetch: fetch, source: source, api: api,
		web: "https://" + cfg.host(), client: httpClient(cfg), opts: cfg.Inbox,
		status: connector.Status{State: connector.StateNotSignedIn, Error: waiting}, poke: make(chan struct{}, 1)}
}

func (c *Connector) ID() string   { return "github" }
func (c *Connector) Name() string { return "GitHub" }

// Manifest: everything; a PR's or issue's thread opens as GitHub's own page (threads).
func (c *Connector) Manifest() connector.Manifest {
	return connector.Manifest{
		Capabilities: []connector.Capability{connector.CapStatus, connector.CapTools, connector.CapActions, connector.CapEvents,
			connector.CapInbox, connector.CapSettle, connector.CapArchive, connector.CapLogin, connector.CapOptions, connector.CapThreads,
			connector.CapMachines, connector.CapFallback},
		Hosts:            []string{c.cfg.host()},
		Kinds:            []string{"review_requested", "mention", "team_mention", "assign", "author", "ci_failed"},
		KindDescriptions: KindDescriptions,
	}
}

func (c *Connector) Status(context.Context) connector.Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.status
	switch {
	case c.source == SourceDevice && c.dev.Token != "":
		st.Via = "设备码登录"
	case c.source == SourceMachine && c.fetch != nil:
		st.Via = "来自 " + c.machine + " 的 gh 登录"
	}
	if st.State == connector.StateOK && c.rateLow && time.Now().Before(c.rateReset) {
		st.State = connector.StateRateLimited
		// minutes, not a clock time: the hub's clock is not in the user's time zone
		st.Error = fmt.Sprintf("这个小时的 GitHub 请求额度快用完了，约 %d 分钟内只看通知", int(time.Until(c.rateReset).Minutes())+1)
	}
	return st
}

// signedIn: the last check found the token good (rate limits aside).
func (c *Connector) signedIn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status.State == connector.StateOK
}

// MachineOnline: the machine the token comes from just connected, so a token that was missing may be there now.
func (c *Connector) MachineOnline(id string) {
	c.mu.Lock()
	mine := c.source == SourceMachine && id == c.machineID
	c.mu.Unlock()
	if !mine {
		return
	}
	select {
	case c.poke <- struct{}{}:
	default:
	}
}

// How often Run checks again: when all is well, and when it is not.
var (
	checkOK    = 30 * time.Minute
	checkRetry = time.Minute
)

// Run checks the sign-in now and then until ctx ends: again soon while it fails, and when the machine connects.
func (c *Connector) Run(ctx context.Context) {
	for {
		c.check(ctx)
		wait := checkOK
		if !c.signedIn() {
			wait = checkRetry
		}
		select {
		case <-ctx.Done():
			return
		case <-c.poke:
		case <-time.After(wait):
		}
	}
}

// check asks GitHub who the token is (fetching one first if there is none) and records the answer.
func (c *Connector) check(ctx context.Context) {
	var user struct {
		Login string `json:"login"`
	}
	h, err := c.Get(ctx, "/user", &user)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.scopes, c.scopesKnown = scopesOf(h)
		c.status = connector.Status{State: connector.StateOK, Account: user.Login, LastSync: time.Now().UTC(), LastEvent: c.status.LastEvent}
		return
	}
	c.status.State, c.status.Error = c.explain(err)
}

// notSignedIn is the status of a GitHub with neither a device sign-in nor a computer's gh login.
const notSignedIn = "还没登录 GitHub：在下面选一台电脑上的 gh 登录，或者用设备码登录"

// explain turns an error into a state and one sentence for people.
func (c *Connector) explain(err error) (string, string) {
	m := c.machine
	switch {
	case errors.Is(err, errBadProxy):
		return connector.StateNotConfigured, "扩展设置里的「代理」不对（要 http://[用户名:密码@]主机:端口）"
	case errors.Is(err, errUnauthorized) && c.source == SourceDevice:
		return connector.StateNotSignedIn, "设备码登录失效了（在 GitHub 上撤销了授权？）：重新登录"
	case errors.Is(err, errNoDeviceLogin):
		return connector.StateNotSignedIn, notSignedIn
	case errors.Is(err, ErrNoMachine):
		return connector.StateNotConfigured, "选的电脑不在这台 hub 上了：在 设置 › 连接器 › GitHub 重新选一台，或者用设备码登录"
	case errors.Is(err, ErrNotGranted):
		return connector.StateNotSignedIn, "扩展没有获准读电脑上的 gh 登录：在 设置 › 扩展 › GitHub 重新同意它的权限，或者改用设备码登录"
	case errors.Is(err, ErrHubTooOld):
		return connector.StateNotSignedIn, "这台 hub 太旧，扩展读不了电脑上的 gh 登录：更新 hub，或者改用设备码登录"
	case errors.Is(err, ErrOffline):
		return connector.StateNotSignedIn, "GitHub 未授权（" + m + " 离线）：它连上后会自动再读"
	case errors.Is(err, ErrNodeTooOld):
		return connector.StateError, m + " 上的 personad 太旧，读不了 gh 登录：更新它"
	case errors.Is(err, ErrNoGh):
		return connector.StateNotSignedIn, m + " 上没有 gh：在那台机器上装好 gh 并运行 gh auth login"
	case errors.Is(err, ErrNotSignedIn):
		return connector.StateNotSignedIn, m + " 上的 gh 没有登录 " + c.cfg.host() + "，或者登录存在钥匙串里、经 ssh 读不到：" +
			"在那台机器上运行 gh auth login --insecure-storage"
	case errors.Is(err, errUnauthorized):
		return connector.StateNotSignedIn, m + " 上的 gh 登录失效了：在那台机器上运行 gh auth login"
	}
	return connector.StateError, "连不上 GitHub：" + Scrub(err.Error())
}

var errUnauthorized = errors.New("GitHub says the token is not valid (401)")

// currentToken is the token in memory, fetching one only when there is none or (refresh) GitHub refused it: a machine
// that went offline leaves the one in memory in use.
func (c *Connector) currentToken(ctx context.Context, refresh bool) (string, error) {
	c.mu.Lock()
	tok := c.token
	c.mu.Unlock()
	if tok != "" && !refresh {
		return tok, nil
	}
	c.mu.Lock()
	source, fetch := c.source, c.fetch
	c.mu.Unlock()
	if source == SourceDevice {
		d := c.deviceState(ctx)
		if d.Token == "" {
			return "", errNoDeviceLogin
		}
		c.mu.Lock()
		c.token = d.Token
		c.mu.Unlock()
		return d.Token, nil
	}
	if fetch == nil {
		return "", ErrNoMachine
	}
	fresh, err := fetch(ctx, c.cfg.host())
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.token = fresh
	c.mu.Unlock()
	return fresh, nil
}

// Get reads path of the REST API into out (JSON; a *[]byte takes the body as it is; nil drops it), refetching the
// token once if GitHub refuses it.
func (c *Connector) Get(ctx context.Context, path string, out any) (http.Header, error) {
	_, h, err := c.request(ctx, http.MethodGet, path, nil, nil, out)
	return h, err
}

// raw reads path as text (a job's log).
func (c *Connector) raw(ctx context.Context, path string) (string, error) {
	var b []byte
	_, _, err := c.request(ctx, http.MethodGet, path, nil, nil, &b)
	return string(b), err
}

// request sends one API request with the extra headers hdr and body (JSON), decoding the answer into out (as Get);
// it returns the status (304 is not an error: out is left as it is).
func (c *Connector) request(ctx context.Context, method, path string, hdr http.Header, body []byte, out any) (int, http.Header, error) {
	tok, err := c.currentToken(ctx, false)
	if err != nil {
		return 0, nil, err
	}
	for attempt := 0; ; attempt++ {
		resp, err := c.send(ctx, method, path, hdr, body, tok)
		if errors.Is(err, errBadProxy) {
			return 0, nil, err // names the setting, not the token: nothing to scrub, and explain must see it
		}
		if err != nil {
			return 0, nil, errors.New(Scrub(err.Error()))
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			// The machine may have signed in again since: ask it once more.
			if tok, err = c.currentToken(ctx, true); err != nil {
				return 0, nil, err
			}
			continue
		}
		defer resp.Body.Close()
		c.noteRate(resp.Header)
		limit := int64(8 << 20)
		if _, text := out.(*[]byte); text {
			limit = 32 << 20 // a job's log, of which the end is kept
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
		code := resp.StatusCode
		switch {
		case code == http.StatusUnauthorized:
			return code, resp.Header, errUnauthorized
		case code == http.StatusNotModified:
			return code, resp.Header, nil
		case code >= 300:
			var e struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(raw, &e)
			return code, resp.Header, fmt.Errorf("GitHub %s %s: %d %s", method, path, code, Scrub(e.Message))
		}
		if text, ok := out.(*[]byte); ok {
			*text = raw
		} else if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				return code, resp.Header, fmt.Errorf("GitHub %s %s: %v", method, path, err)
			}
		}
		return code, resp.Header, nil
	}
}

func (c *Connector) send(ctx context.Context, method, path string, hdr http.Header, body []byte, tok string) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.api+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	return c.client.Do(req)
}

var tokenLike = regexp.MustCompile(`\b(gh[opsur]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{16,}|[0-9a-f]{40})\b`)

// Scrub blanks anything that looks like a GitHub token in s (an error, a message): tokens never leave the hub.
func Scrub(s string) string { return tokenLike.ReplaceAllString(s, "[token]") }
