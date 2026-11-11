package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
)

// The OAuth client the Codex CLI signs in with (device code; no client secret).
const (
	clientID   = "app_EMoamEEZ73f0CkXaXp7hrann"
	userAgent  = "codex_cli_rs/0.1" // chatgpt.com sits behind Cloudflare, which refuses requests without one
	originator = "codex_cli_rs"
)

// tokens are a ChatGPT sign-in. Kept in memory and in the hub's connector secrets, never in a file, a log or an
// answer.
type tokens struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id,omitempty"`
}

type secret struct {
	Version int    `json:"version"`
	Tokens  tokens `json:"tokens"`
}

func decodeSecret(data []byte) (*tokens, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var s secret
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Tokens.AccessToken == "" || s.Tokens.RefreshToken == "" {
		return nil, errors.New("no tokens")
	}
	t := s.Tokens
	return &t, nil
}

// claims are a JWT's claims, unverified: only for the expiry and who is signed in.
func claims(jwt string) map[string]any {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var c map[string]any
	if json.Unmarshal(raw, &c) != nil {
		return nil
	}
	return c
}

func authClaims(jwt string) map[string]any {
	m, _ := claims(jwt)["https://api.openai.com/auth"].(map[string]any)
	return m
}

func (t *tokens) expiry() time.Time {
	if exp, ok := claims(t.AccessToken)["exp"].(float64); ok {
		return time.Unix(int64(exp), 0)
	}
	return time.Time{}
}

// account is who is signed in, for people: the mail address and the plan.
func (t *tokens) account() string {
	email, _ := claims(t.IDToken)["email"].(string)
	plan, _ := authClaims(t.IDToken)["chatgpt_plan_type"].(string)
	switch {
	case email != "" && plan != "":
		return email + " (" + plan + ")"
	case email != "":
		return email
	}
	return "ChatGPT"
}

func (t *tokens) accountID() string {
	if t.AccountID != "" {
		return t.AccountID
	}
	for _, jwt := range []string{t.IDToken, t.AccessToken} {
		if id, _ := authClaims(jwt)["chatgpt_account_id"].(string); id != "" {
			return id
		}
	}
	return ""
}

// fromGrant is the tokens of an oauth/token answer, the old ones kept where it gives none.
func fromGrant(old tokens, g tokenGrant) tokens {
	t := old
	if g.IDToken != "" {
		t.IDToken = g.IDToken
	}
	if g.AccessToken != "" {
		t.AccessToken = g.AccessToken
	}
	if g.RefreshToken != "" {
		t.RefreshToken = g.RefreshToken
	}
	t.AccountID = ""
	t.AccountID = t.accountID()
	return t
}

type tokenGrant struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

var (
	errNotSignedIn = errors.New("not signed in to ChatGPT: the user signs in from the extension's details (Settings › Extensions › ChatGPT Apps)")
	errExpired     = errors.New("the ChatGPT sign-in has expired: the user signs in again from the extension's details")
)

// httpError is an answer other than 2xx: the status and the start of the body (an error message, never logged).
type httpError struct {
	Status int
	Body   string
}

func (e *httpError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body) }

func (a *App) postAuth(ctx context.Context, path, contentType string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.ep.Auth+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := a.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return &httpError{Status: resp.StatusCode, Body: short(string(raw), 300)}
	}
	return json.Unmarshal(raw, out)
}

func (a *App) tokenRequest(ctx context.Context, form url.Values) (tokenGrant, error) {
	var g tokenGrant
	err := a.postAuth(ctx, "/oauth/token", "application/x-www-form-urlencoded", []byte(form.Encode()), &g)
	if err == nil && (g.AccessToken == "" || g.RefreshToken == "" && form.Get("grant_type") != "refresh_token") {
		err = errors.New("the sign-in answered no tokens")
	}
	return g, err
}

// save keeps t as the sign-in, in memory and in the hub's secrets. A refresh token works once, so a new one is
// saved before anything uses it.
func (a *App) save(ctx context.Context, t tokens) error {
	data, err := json.Marshal(secret{Version: 1, Tokens: t})
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.tok, a.expired = &t, false
	hub := a.hub
	a.mu.Unlock()
	if hub == nil {
		return nil
	}
	if err := hub.SaveSecret(ctx, connectorID, data); err != nil {
		log.Printf("could not store the sign-in (kept in memory until the extension restarts): %v", err)
		return err
	}
	return nil
}

func (a *App) current() *tokens {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tok == nil {
		return nil
	}
	t := *a.tok
	return &t
}

// access is a usable sign-in: refreshed first when the access token ends within two minutes.
func (a *App) access(ctx context.Context) (tokens, error) {
	a.waitLoaded(ctx)
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	t := a.current()
	if t == nil {
		a.mu.Lock()
		expired := a.expired
		a.mu.Unlock()
		if expired {
			return tokens{}, errExpired
		}
		return tokens{}, errNotSignedIn
	}
	if exp := t.expiry(); !exp.IsZero() && exp.After(a.now().Add(2*time.Minute)) {
		return *t, nil
	}
	return a.refreshLocked(ctx, *t)
}

// refreshAfter401 refreshes after the backend refused used's access token, unless another call has already.
func (a *App) refreshAfter401(ctx context.Context, used string) (tokens, error) {
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	t := a.current()
	if t == nil {
		return tokens{}, errNotSignedIn
	}
	if t.AccessToken != used {
		return *t, nil
	}
	return a.refreshLocked(ctx, *t)
}

func (a *App) refreshLocked(ctx context.Context, t tokens) (tokens, error) {
	g, err := a.tokenRequest(ctx, url.Values{
		"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {t.RefreshToken},
		"scope": {"openid profile email"},
	})
	var he *httpError
	if errors.As(err, &he) && (he.Status == http.StatusBadRequest || he.Status == http.StatusUnauthorized) {
		// Used elsewhere, revoked or too old: this sign-in is over.
		log.Printf("refresh refused (%d): signed out", he.Status)
		a.mu.Lock()
		a.tok, a.expired = nil, true
		hub := a.hub
		a.mu.Unlock()
		if hub != nil {
			_ = hub.DeleteSecret(ctx, connectorID)
		}
		a.changed()
		return tokens{}, errExpired
	}
	if err != nil {
		return tokens{}, fmt.Errorf("refreshing the ChatGPT sign-in: %w", err)
	}
	n := fromGrant(t, g)
	_ = a.save(ctx, n)
	log.Printf("sign-in refreshed")
	return n, nil
}

// ---- auth.* (manifest auth): a device code

// deviceLogin is a sign-in in progress: the code the user enters and what polling found.
type deviceLogin struct {
	deviceAuthID, userCode string
	interval               time.Duration
	expires                time.Time
	done                   bool
	err                    string
	cancel                 context.CancelFunc
}

func (a *App) verificationURL() string { return a.ep.Auth + "/codex/device" }

// Status: from memory only.
func (a *App) Status(ctx context.Context) (wire.AuthStatus, error) {
	a.waitLoaded(ctx)
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case a.tok != nil:
		st := wire.AuthStatus{State: wire.AuthStateSignedIn, Account: a.tok.account()}
		return st, nil
	case a.login != nil && !a.login.done && a.login.err == "":
		return wire.AuthStatus{State: wire.AuthStatePending}, nil
	case a.expired:
		return wire.AuthStatus{State: wire.AuthStateExpired, Message: a.text.authExpired}, nil
	}
	return wire.AuthStatus{State: wire.AuthStateSignedOut}, nil
}

// Step: "" asks for a new code and starts polling it in the background (the user may leave the card); the same step
// asked again answers what polling found so far, with no request of its own.
func (a *App) Step(ctx context.Context, step string, _ map[string]string) (wire.AuthPrompt, error) {
	if step == "" {
		return a.startLogin(ctx)
	}
	a.mu.Lock()
	l, t, text := a.login, a.tok, a.text
	var done bool
	var failed string
	if l != nil {
		done, failed = l.done, l.err
	}
	a.mu.Unlock()
	switch {
	case l == nil:
		return wire.AuthPrompt{Error: text.authCodeExpired}, nil
	case done && t != nil:
		return wire.AuthPrompt{Done: true, Account: t.account()}, nil
	case failed != "":
		return wire.AuthPrompt{Error: failed}, nil
	}
	return a.devicePrompt(l, text), nil
}

func (a *App) devicePrompt(l *deviceLogin, text texts) wire.AuthPrompt {
	return wire.AuthPrompt{Step: "device", Kind: wire.ConnectorPromptKindDeviceCode, URL: a.verificationURL(), UserCode: l.userCode,
		Interval: int(l.interval / time.Second), Hint: text.authHint, ExpiresAt: l.expires}
}

func (a *App) startLogin(ctx context.Context) (wire.AuthPrompt, error) {
	var code struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		Interval     any    `json:"interval"`
		ExpiresAt    string `json:"expires_at"`
	}
	body, _ := json.Marshal(map[string]string{"client_id": clientID})
	if err := a.postAuth(ctx, "/api/accounts/deviceauth/usercode", "application/json", body, &code); err != nil {
		log.Printf("device code refused: %v", statusOnly(err))
		a.mu.Lock()
		text := a.text
		a.mu.Unlock()
		return wire.AuthPrompt{Error: fmt.Sprintf(text.authFailed, statusOnly(err))}, nil
	}
	l := &deviceLogin{deviceAuthID: code.DeviceAuthID, userCode: code.UserCode, interval: 5 * time.Second,
		expires: a.now().Add(15 * time.Minute)}
	switch v := code.Interval.(type) {
	case float64:
		l.interval = time.Duration(v) * time.Second
	case string:
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			l.interval = time.Duration(n) * time.Second
		}
	}
	if l.interval < time.Second {
		l.interval = time.Second
	}
	if at, err := time.Parse(time.RFC3339, code.ExpiresAt); err == nil {
		l.expires = at
	}
	a.mu.Lock()
	if a.login != nil && a.login.cancel != nil {
		a.login.cancel()
	}
	root := a.ctx
	if root == nil {
		root = context.Background()
	}
	pctx, cancel := context.WithDeadline(root, l.expires)
	l.cancel = cancel
	a.login = l
	text := a.text
	a.mu.Unlock()
	go a.poll(pctx, l)
	return a.devicePrompt(l, text), nil
}

// poll asks whether the user entered the code, every interval until it expires; on success it signs in.
func (a *App) poll(ctx context.Context, l *deviceLogin) {
	defer l.cancel()
	fail := func(msg string) {
		a.mu.Lock()
		l.err = msg
		a.mu.Unlock()
		a.changed()
	}
	for {
		select {
		case <-ctx.Done():
			a.mu.Lock()
			current := a.login == l && !l.done
			text := a.text
			a.mu.Unlock()
			if current && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				fail(text.authCodeExpired)
			}
			return
		case <-time.After(l.interval):
		}
		var got struct {
			AuthorizationCode string `json:"authorization_code"`
			CodeVerifier      string `json:"code_verifier"`
		}
		body, _ := json.Marshal(map[string]string{"device_auth_id": l.deviceAuthID, "user_code": l.userCode})
		err := a.postAuth(ctx, "/api/accounts/deviceauth/token", "application/json", body, &got)
		var he *httpError
		if errors.As(err, &he) && (he.Status == http.StatusForbidden || he.Status == http.StatusNotFound || strings.Contains(he.Body, "pending")) {
			continue // not entered yet
		}
		if err != nil {
			if ctx.Err() != nil {
				continue // the select above says why
			}
			if he == nil {
				continue // the network: try again on the next round
			}
			log.Printf("device code polling refused: %v", statusOnly(err))
			a.mu.Lock()
			text := a.text
			a.mu.Unlock()
			fail(fmt.Sprintf(text.authFailed, statusOnly(err)))
			return
		}
		g, err := a.tokenRequest(ctx, url.Values{
			"grant_type": {"authorization_code"}, "code": {got.AuthorizationCode},
			"redirect_uri": {a.ep.Auth + "/deviceauth/callback"}, "client_id": {clientID}, "code_verifier": {got.CodeVerifier},
		})
		if err != nil {
			log.Printf("code exchange refused: %v", statusOnly(err))
			a.mu.Lock()
			text := a.text
			a.mu.Unlock()
			fail(fmt.Sprintf(text.authFailed, statusOnly(err)))
			return
		}
		a.refreshMu.Lock()
		_ = a.save(context.WithoutCancel(ctx), fromGrant(tokens{}, g))
		a.refreshMu.Unlock()
		a.mu.Lock()
		l.done = true
		a.mu.Unlock()
		log.Printf("signed in")
		a.clearCaches()
		a.changed()
		return
	}
}

// Logout forgets the sign-in: memory, the hub's secrets, a sign-in in progress and what was read with it.
func (a *App) Logout(ctx context.Context) (wire.AuthStatus, error) {
	a.waitLoaded(ctx)
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	a.mu.Lock()
	if a.login != nil && a.login.cancel != nil {
		a.login.cancel()
	}
	a.login, a.tok, a.expired = nil, nil, false
	hub := a.hub
	a.mu.Unlock()
	a.clearCaches()
	if hub != nil {
		if err := hub.DeleteSecret(ctx, connectorID); err != nil {
			log.Printf("could not delete the stored sign-in: %v", err)
		}
	}
	return wire.AuthStatus{State: wire.AuthStateSignedOut}, nil
}

func (a *App) clearCaches() {
	a.mu.Lock()
	a.tools, a.toolsAt = nil, time.Time{}
	a.plugins = map[string]pluginDetail{}
	clear(a.listings)
	a.inst, a.instAt = nil, time.Time{}
	a.mu.Unlock()
}

// statusOnly is an error for a log or a person: an HTTP answer as its status, without its body.
func statusOnly(err error) string {
	var he *httpError
	if errors.As(err, &he) {
		return fmt.Sprintf("HTTP %d", he.Status)
	}
	return err.Error()
}

func short(s string, n int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
