package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The openGym server's scoped-token routes (openGym api/server.js, docs/API_TOKENS.md):
//
//	GET /api/token/me        {user:{id,name}, token:{id,name,scopes,exp}}
//	GET /api/token/data      {state, rev, wid?}: the document limited to the token's scopes
//	GET /api/token/data/rev  {rev, wid?}
//	PUT /api/token/data      {state:{keys to replace}, baseRev, baseWid?} -> {ok, rev, wid} | 409 {rev, state, wid}

const (
	sandboxHost    = "host.persona.internal"
	requestTimeout = 20 * time.Second
	bodyMost       = 16 << 20 // the server takes at most 5 MB a document
)

var errAddress = errors.New("openGym 服务器地址要是 https 网址；局域网和本机（192.168.x.x、10.x.x.x、*.local、127.0.0.1、localhost）可以用 http")

// parseServer reads the server setting: an http(s) address with an optional BASE_PATH, no user info, query or
// fragment. Plain http is only for addresses that stay on the local network: the token goes in every request.
func parseServer(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errAddress
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(raw, "#") || strings.HasSuffix(raw, "?") {
		return nil, errors.New("openGym 服务器地址里不要带用户名、密码、?参数 或 #")
	}
	if u.Scheme == "http" && !localHost(u.Hostname()) {
		return nil, errAddress
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

// localHost reports whether h stays on this machine or the local network: loopback, private and link-local
// addresses, a name without a dot, or a name under .local, .lan, .home.arpa or .internal.
func localHost(h string) bool {
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
	}
	if h == "localhost" || !strings.Contains(h, ".") {
		return true
	}
	for _, s := range []string{".localhost", ".local", ".lan", ".home.arpa", ".internal"} {
		if strings.HasSuffix(h, s) {
			return true
		}
	}
	return false
}

func endpoint(base *url.URL, path string) string {
	u := *base
	u.Path = base.Path + path
	return u.String()
}

// proxied reports whether requests go through a proxy (the sandbox's egress proxy sets one).
func proxied(getenv func(string) string) bool {
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		if getenv(k) != "" {
			return true
		}
	}
	return false
}

// dialURL is the address to request for u: behind a proxy, an http loopback host becomes host.persona.internal with
// the same port, which the sandbox's proxy connects to the hub machine's loopback. Other hosts stay as they are.
func dialURL(u *url.URL, getenv func(string) string) *url.URL {
	h := strings.Trim(u.Hostname(), "[]")
	ip := net.ParseIP(h)
	loop := h == "localhost" || strings.HasSuffix(h, ".localhost") || (ip != nil && ip.IsLoopback())
	if u.Scheme != "http" || !loop || !proxied(getenv) {
		return u
	}
	cp := *u
	if port := u.Port(); port != "" {
		cp.Host = net.JoinHostPort(sandboxHost, port)
	} else {
		cp.Host = sandboxHost
	}
	return &cp
}

func newClient() *http.Client {
	return &http.Client{
		Timeout:   requestTimeout,
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: requestTimeout, MaxIdleConnsPerHost: 2},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 0 && via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("refused a redirect from https to http")
			}
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				return errors.New("refused a redirect to another host") // the token stays with the server
			}
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

// apiError is an answer of the server other than 2xx.
type apiError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter string
	Body       json.RawMessage // a 409 conflict's {rev, state, wid}
}

func (e *apiError) Error() string {
	switch {
	case e.Status == http.StatusUnauthorized:
		return "openGym 不接受这个 API 令牌（401）：令牌错了、过期了或被撤销了，在 openGym 的「设置 › 集成 › API 令牌」里新建一个，再填到扩展的设置里"
	case e.Status == http.StatusForbidden && e.Code == "scope":
		return "API 令牌没有改这部分的权限（403）：在 openGym 里给令牌的这一类开「应用」"
	case e.Status == http.StatusNotFound:
		return "openGym 服务器上没有令牌接口（404）：地址对吗（有 BASE_PATH 要带上）？服务器关了 API_TOKENS 也会这样"
	case e.Status == http.StatusTooManyRequests:
		return "openGym 说请求太多了（429），" + firstNonEmpty(e.RetryAfter, "一会儿") + " 秒后再试"
	case e.Status == http.StatusConflict && e.Code == "not-synced":
		return "这个 openGym 账号还没有从手机同步过（409），先在 App 里同步一次"
	}
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("openGym 回答 %d：%s", e.Status, clip(oneLine(msg), 200))
}

// do sends one request with the token and decodes a 2xx answer into out. Errors never contain the token.
func (a *app) do(ctx context.Context, method, path string, body, out any) error {
	a.mu.Lock()
	base, token := a.base, a.token
	a.mu.Unlock()
	if base == nil {
		return a.health().Config
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	target := dialURL(base, a.getenv)
	req, err := http.NewRequestWithContext(ctx, method, endpoint(target, path), rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "persona-opengym/"+about.Version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.client.Do(req)
	if err != nil {
		return a.redact(fmt.Errorf("连不上 openGym 服务器 %s：%w", base.Host, unwrapURL(err)), token)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, bodyMost+1))
	if err != nil {
		return a.redact(fmt.Errorf("读 openGym 的回答失败：%w", err), token)
	}
	if len(raw) > bodyMost {
		return errors.New("openGym 的回答超过 16 MB")
	}
	if res.StatusCode/100 != 2 {
		e := &apiError{Status: res.StatusCode, RetryAfter: res.Header.Get("Retry-After")}
		var msg struct{ Error, Code string }
		if json.Unmarshal(raw, &msg) == nil {
			e.Message, e.Code = msg.Error, msg.Code
			if res.StatusCode == http.StatusConflict {
				e.Body = raw
			}
		} else {
			e.Message = string(raw)
		}
		e.Message = strings.ReplaceAll(e.Message, token, "…")
		return e
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("openGym 的回答不是预期的 JSON（地址指向的是 openGym 吗？）：%w", err)
	}
	return nil
}

func unwrapURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func (a *app) redact(err error, token string) error {
	if token == "" || !strings.Contains(err.Error(), token) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), token, "…"))
}

// me is GET /api/token/me.
type me struct {
	User struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"user"`
	Token struct {
		ID     string            `json:"id"`
		Name   string            `json:"name"`
		Scopes map[string]string `json:"scopes"`
		Exp    *int64            `json:"exp"`
	} `json:"token"`
}

// can reports whether the token's scope for category reaches level (view or apply).
func (m me) can(category, level string) bool {
	s := m.Token.Scopes[category]
	return s == "apply" || (level == "view" && s == "view")
}

// snapshot is one read of the document.
type snapshot struct {
	Rev   int
	Wid   string
	Me    me
	State gymState
	Raw   map[string]json.RawMessage // the document as read, for writes that keep what they do not know
	Empty bool                       // no device synced the profile yet
	At    time.Time
}

type dataAnswer struct {
	State json.RawMessage `json:"state"`
	Rev   int             `json:"rev"`
	Wid   string          `json:"wid"`
}

func (a *app) snapshot() *snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snap
}

// sync reads the server: who the token is, then the revision, then the document when the revision moved (or on the
// first read). Unforced, it does nothing within a minute of the last answer.
func (a *app) sync(ctx context.Context, force bool) error {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	h := a.health()
	if h.Config != nil {
		return h.Config
	}
	if !force && !h.Checked.IsZero() && a.now().Sub(h.Checked) < quickSync && h.Err == nil {
		return nil
	}
	err := a.read(ctx)
	a.mu.Lock()
	if err != nil {
		a.lastErr, a.failedAt = err, a.now()
	} else {
		a.lastErr, a.checked = nil, a.now()
	}
	a.mu.Unlock()
	return err
}

func (a *app) read(ctx context.Context) error {
	var who me
	if err := a.do(ctx, http.MethodGet, "/api/token/me", nil, &who); err != nil {
		return err
	}
	old := a.snapshot()
	if old != nil {
		var rev struct {
			Rev int    `json:"rev"`
			Wid string `json:"wid"`
		}
		if err := a.do(ctx, http.MethodGet, "/api/token/data/rev", nil, &rev); err != nil {
			return err
		}
		if rev.Rev == old.Rev && rev.Wid == old.Wid && maps.Equal(who.Token.Scopes, old.Me.Token.Scopes) {
			cp := *old
			cp.Me, cp.At = who, a.now()
			a.keep(&cp, false)
			return nil
		}
	}
	var d dataAnswer
	if err := a.do(ctx, http.MethodGet, "/api/token/data", nil, &d); err != nil {
		return err
	}
	s, err := a.parse(d)
	if err != nil {
		return err
	}
	s.Me = who
	a.keep(s, true)
	return nil
}

// parse turns a data answer (or a conflict's body) into a snapshot.
func (a *app) parse(d dataAnswer) (*snapshot, error) {
	s := &snapshot{Rev: d.Rev, Wid: d.Wid, At: a.now(), Raw: map[string]json.RawMessage{}}
	if len(d.State) == 0 || string(d.State) == "null" {
		s.Empty = true
		return s, nil
	}
	if err := json.Unmarshal(d.State, &s.Raw); err != nil {
		return nil, fmt.Errorf("openGym 的数据不是一个 JSON 对象：%w", err)
	}
	s.State = parseState(s.Raw)
	return s, nil
}

func (a *app) keep(s *snapshot, changed bool) {
	a.mu.Lock()
	a.snap = s
	a.mu.Unlock()
	if changed {
		a.invalidate()
	}
}
