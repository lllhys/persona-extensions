package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/github/internal/connector"
)

// Where the token comes from (docs/connectors.md「二 · 7」): one computer's gh login, which the user picks in the app
// (hub.json's github.auth.from_machine is only the default), or the device flow (login.go), which needs no computer.
// Choosing one leaves the other: there is no separate signing out.

// Sources of the token.
const (
	SourceMachine = "machine"
	SourceDevice  = "device"
)

var errNoDeviceLogin = errors.New("not signed in with the device flow")

// Source is where the token comes from now.
func (c *Connector) Source() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.source
}

// MachineID is the computer whose gh login is read ("" with the device flow).
func (c *Connector) MachineID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.source != SourceMachine {
		return ""
	}
	return c.machineID
}

// UseMachine reads the token of machine id (its display name for messages) through fetch from now on; the device
// flow's token is forgotten.
func (c *Connector) UseMachine(ctx context.Context, id, name string, fetch TokenSource) {
	_ = c.forgetDevice(ctx)
	c.mu.Lock()
	c.source, c.machineID, c.machine, c.fetch, c.token = SourceMachine, id, name, fetch, ""
	c.scopes, c.scopesKnown = nil, false
	c.status = statusWaiting("正在读取 " + name + " 上的 gh 登录…")
	c.mu.Unlock()
	c.kickCheck()
}

// UseDevice signs in with the device flow from now on (no computer's login is read).
func (c *Connector) UseDevice() {
	c.mu.Lock()
	changed := c.source != SourceDevice
	c.source, c.token = SourceDevice, ""
	if changed {
		c.scopes, c.scopesKnown = nil, false
		c.status = statusWaiting(notSignedIn)
	}
	c.mu.Unlock()
	c.kickCheck()
}

func (c *Connector) kickCheck() {
	select {
	case c.poke <- struct{}{}:
	default:
	}
}

// RequiredScope is a scope the connector needs and what does not work without it.
type RequiredScope struct {
	Scope   string   `json:"scope"`
	Affects []string `json:"affects"`
}

// MissingScopes are the scopes a token lacks (granted: X-OAuth-Scopes) and what goes wrong for it. repo covers
// everything the connector does — reading private repositories' PRs, issues, comments, checks and CI logs, searching,
// commenting and reviewing — and notifications; without it, notifications alone keeps the inbox.
func MissingScopes(granted []string) []RequiredScope {
	has := func(s string) bool { return slices.Contains(granted, s) }
	if has("repo") {
		return nil
	}
	write := "评论和 review 发不了"
	if has("public_repo") {
		write = "评论和 review 只能发到公开仓库"
	}
	out := []RequiredScope{{Scope: "repo", Affects: []string{"私有仓库的 PR、issue、评论、代码搜索读不到", "私有仓库的 CI 失败和日志读不到", write}}}
	if !has("notifications") {
		out = append(out, RequiredScope{Scope: "notifications", Affects: []string{"收件箱收不到 GitHub 的通知"}})
	}
	return out
}

// Scopes are what the token in use may do; known is false until GitHub said (or for tokens it says nothing for, such
// as fine-grained ones).
func (c *Connector) Scopes() (scopes []string, known bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.scopes), c.scopesKnown
}

// scopesOf reads X-OAuth-Scopes ("repo, read:org"); known is false when GitHub did not send it.
func scopesOf(h http.Header) ([]string, bool) {
	if h == nil || len(h.Values("X-OAuth-Scopes")) == 0 {
		return nil, false
	}
	var out []string
	for _, s := range strings.Split(h.Get("X-OAuth-Scopes"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out, true
}

// MachineLogin is how a computer's gh login looks to GitHub.
type MachineLogin struct {
	// State: signed_in, not_signed_in (gh has no login, or GitHub refused it), no_gh, too_old (its jarvisd),
	// offline, error.
	State   string          `json:"state"`
	Account string          `json:"account,omitempty"`
	Scopes  []string        `json:"scopes,omitempty"`
	Missing []RequiredScope `json:"missing,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// Probe asks a computer for its gh login (fetch) and GitHub who it is and what it may do; the token goes nowhere.
func (c *Connector) Probe(ctx context.Context, fetch TokenSource) MachineLogin {
	tok, err := fetch(ctx, c.cfg.host())
	switch {
	case errors.Is(err, ErrOffline), errors.Is(err, ErrNoMachine):
		return MachineLogin{State: "offline"}
	case errors.Is(err, ErrNoGh):
		return MachineLogin{State: "no_gh"}
	case errors.Is(err, ErrNodeTooOld):
		return MachineLogin{State: "too_old"}
	case errors.Is(err, ErrNotSignedIn):
		return MachineLogin{State: "not_signed_in"}
	case err != nil:
		return MachineLogin{State: "error", Error: Scrub(err.Error())}
	}
	resp, err := c.send(ctx, http.MethodGet, "/user", nil, nil, tok)
	if err != nil {
		return MachineLogin{State: "error", Error: Scrub(err.Error())}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return MachineLogin{State: "not_signed_in"}
	}
	var u struct {
		Login string `json:"login"`
	}
	if resp.StatusCode >= 300 || decodeJSON(resp, &u) != nil {
		return MachineLogin{State: "error", Error: resp.Status}
	}
	m := MachineLogin{State: "signed_in", Account: u.Login}
	if scopes, known := scopesOf(resp.Header); known {
		m.Scopes, m.Missing = scopes, MissingScopes(scopes)
	}
	return m
}

func statusWaiting(why string) connector.Status {
	return connector.Status{State: connector.StateNotSignedIn, Error: why}
}

func decodeJSON(resp *http.Response, out any) error {
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}
