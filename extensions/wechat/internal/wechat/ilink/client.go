// Package ilink is a client for iLink, Tencent's API for AI bots in personal WeChat: QR authorization, long polling
// for messages, sending text, the typing status and downloading media. The wire behavior follows Tencent's open
// source client openclaw-weixin (MIT, see THIRD_PARTY_NOTICES.md). The package keeps no state of its own: the caller
// stores the token and the polling cursor.
package ilink

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// AuthOrigin is where QR authorization starts; a confirmed authorization names the base URL for everything else.
const AuthOrigin = "https://ilinkai.weixin.qq.com"

// ClientVersion is the version this client tells iLink (its wire behavior, not Persona's build).
const ClientVersion = "2.4.6"

const (
	apiTimeout      = 15 * time.Second
	longPollTimeout = 35 * time.Second
	maxResponse     = 4 << 20
	maxPollMessages = 100
	maxItems        = 20
	// MaxText is the longest text one message may carry, in code points.
	MaxText = 3500
)

// Options configure a Client.
type Options struct {
	// BaseURL is the origin from the authorization (Credentials.BaseURL); AuthOrigin before there is one.
	BaseURL string
	Token   string
	// Agent is the product token iLink sees ("Persona/1"); anything that is not product/version is dropped.
	Agent string
	HTTP  *http.Client
}

// Client calls iLink. It is safe for concurrent use.
type Client struct {
	base  *url.URL
	token string
	agent string
	http  *http.Client
}

// New checks the base URL: only credential-free HTTPS origins under weixin.qq.com are accepted.
func New(o Options) (*Client, error) {
	if o.BaseURL == "" {
		o.BaseURL = AuthOrigin
	}
	base, err := ValidateBaseURL(o.BaseURL)
	if err != nil {
		return nil, err
	}
	h := o.HTTP
	if h == nil {
		h = &http.Client{}
	}
	// Never follow a redirect: the token must not leave Tencent's origin.
	hc := *h
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: base, token: strings.TrimSpace(o.Token), agent: sanitizeAgent(o.Agent), http: &hc}, nil
}

// ValidateBaseURL accepts https://<weixin.qq.com or a subdomain>[:443]/ and nothing else.
func ValidateBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") ||
		u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || !tencentHost(u.Hostname()) {
		return nil, &Error{Code: CodeProtocol, Msg: "iLink base URL must be a Tencent HTTPS origin"}
	}
	u.Path = "/"
	return u, nil
}

func tencentHost(h string) bool {
	h = strings.ToLower(h)
	return h == "weixin.qq.com" || strings.HasSuffix(h, ".weixin.qq.com")
}

// sanitizeAgent keeps the product/version tokens of a User-Agent-like string, at most 256 bytes.
func sanitizeAgent(raw string) string {
	var out []string
	n := 0
	for _, tok := range strings.Fields(raw) {
		name, ver, ok := strings.Cut(tok, "/")
		if !ok || !agentPart(name, false) || !agentPart(ver, true) {
			continue
		}
		next := n + len(tok)
		if len(out) > 0 {
			next++
		}
		if next > 256 {
			break
		}
		out = append(out, tok)
		n = next
	}
	if len(out) == 0 {
		return "Persona/unknown"
	}
	return strings.Join(out, " ")
}

func agentPart(s string, plus bool) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-' || plus && r == '+'
		if !ok {
			return false
		}
	}
	return true
}

func versionNumber(v string) int {
	parts := strings.SplitN(v, ".", 3)
	n := 0
	for i := range 3 {
		x := 0
		if i < len(parts) {
			x, _ = strconv.Atoi(parts[i])
		}
		n = n<<8 | x&0xff
	}
	return n
}

func (c *Client) baseInfo() map[string]string {
	return map[string]string{"channel_version": ClientVersion, "bot_agent": c.agent}
}

func randomUIN() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return base64.StdEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(binary.BigEndian.Uint32(b[:])), 10)))
}

// call sends one request to base (c.base when nil) and decodes the JSON object it answers.
func (c *Client) call(ctx context.Context, method, path string, body any, authed bool, timeout time.Duration, base *url.URL) (map[string]any, error) {
	if base == nil {
		base = c.base
	}
	ref, err := url.Parse(path)
	if err != nil {
		return nil, &Error{Code: CodeProtocol, Msg: "bad iLink path"}
	}
	u := base.ResolveReference(ref)
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, method, u.String(), rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("iLink-App-Id", "bot")
	req.Header.Set("iLink-App-ClientVersion", strconv.Itoa(versionNumber(ClientVersion)))
	req.Header.Set("X-WECHAT-UIN", randomUIN())
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("AuthorizationType", "ilink_bot_token")
	}
	if authed && c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return nil, &Error{Code: CodeAborted, Msg: "iLink call cancelled", Retry: true}
		case errors.Is(cctx.Err(), context.DeadlineExceeded):
			return nil, &Error{Code: CodeTimeout, Msg: "iLink call timed out", Retry: true}
		}
		return nil, &Error{Code: CodeNetwork, Msg: "iLink call failed", Retry: true, err: err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		if errors.Is(cctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return nil, &Error{Code: CodeTimeout, Msg: "iLink call timed out", Retry: true}
		}
		return nil, &Error{Code: CodeNetwork, Msg: "iLink call failed", Retry: true, err: err}
	}
	if len(data) > maxResponse {
		return nil, &Error{Code: CodeBadResponse, Msg: "iLink answer too large", Retry: true}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &Error{Code: CodeHTTP, Msg: fmt.Sprintf("iLink answered HTTP %d", resp.StatusCode), Retry: resp.StatusCode >= 500 || resp.StatusCode == 429}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil || out == nil {
		return nil, &Error{Code: CodeBadResponse, Msg: "iLink answered invalid JSON", Retry: true}
	}
	return out, nil
}

// ret checks the {"ret": n} most answers carry; absent counts as 0.
func ret(out map[string]any, what string) error {
	v, ok := out["ret"]
	if !ok {
		return nil
	}
	n, isNum := v.(float64)
	if !isNum {
		return &Error{Code: CodeBadResponse, Msg: "iLink answered an invalid status for " + what, Retry: true}
	}
	if n != 0 {
		return &Error{Code: CodeProtocol, Msg: fmt.Sprintf("iLink rejected %s (%d)", what, int(n)), Retry: true}
	}
	return nil
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// NotifyStart and NotifyStop tell iLink the bot went on or off line.
func (c *Client) NotifyStart(ctx context.Context) error {
	return c.lifecycle(ctx, "ilink/bot/msg/notifystart", "start")
}
func (c *Client) NotifyStop(ctx context.Context) error {
	return c.lifecycle(ctx, "ilink/bot/msg/notifystop", "stop")
}

func (c *Client) lifecycle(ctx context.Context, path, what string) error {
	out, err := c.call(ctx, http.MethodPost, path, map[string]any{"base_info": c.baseInfo()}, true, 5*time.Second, nil)
	if err != nil {
		return err
	}
	return ret(out, what)
}

// Poll is one answer of GetUpdates.
type Poll struct {
	Cursor   string
	Messages []Message
	// Timeout is how long iLink suggests the next long poll may take (0: not given).
	Timeout time.Duration
}

// GetUpdates long-polls for new messages after cursor ("" the first time). A long poll that ends without anything
// is not an error: it answers the same cursor and no messages. errcode -14 (another client took the token) is
// CodeReplaced.
func (c *Client) GetUpdates(ctx context.Context, cursor string) (Poll, error) {
	out, err := c.call(ctx, http.MethodPost, "ilink/bot/getupdates",
		map[string]any{"get_updates_buf": cursor, "base_info": c.baseInfo()}, true, longPollTimeout, nil)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Code == CodeTimeout {
			return Poll{Cursor: cursor}, nil
		}
		return Poll{}, err
	}
	if code, ok := out["errcode"].(float64); ok && code == -14 {
		return Poll{}, &Error{Code: CodeReplaced, Msg: "the iLink token is no longer active"}
	}
	if err := ret(out, "the poll"); err != nil {
		return Poll{}, err
	}
	p := Poll{Cursor: cursor}
	if s, ok := out["get_updates_buf"].(string); ok {
		p.Cursor = s
	}
	if ms, ok := out["longpolling_timeout_ms"].(float64); ok && ms > 0 {
		p.Timeout = time.Duration(ms) * time.Millisecond
	}
	raw, err := json.Marshal(out["msgs"])
	if err != nil {
		return Poll{}, err
	}
	var msgs []wireMessage
	if out["msgs"] != nil {
		if err := json.Unmarshal(raw, &msgs); err != nil {
			return Poll{}, &Error{Code: CodeBadResponse, Msg: "iLink answered an invalid message list", Retry: true}
		}
	}
	if len(msgs) > maxPollMessages {
		return Poll{}, &Error{Code: CodeBadResponse, Msg: "iLink answered too many messages", Retry: true}
	}
	for _, m := range msgs {
		if len(m.Items) > maxItems {
			return Poll{}, &Error{Code: CodeBadResponse, Msg: "iLink answered too many items in a message", Retry: true}
		}
		if d, ok := decode(m); ok {
			p.Messages = append(p.Messages, d)
		}
	}
	return p, nil
}

// SendText sends text (at most MaxText code points; use Chunk) to peer as the bot. contextToken comes from the
// peer's latest message; clientID makes the send idempotent on iLink's side.
func (c *Client) SendText(ctx context.Context, peer, contextToken, clientID, text string) error {
	if strings.TrimSpace(peer) == "" || strings.TrimSpace(contextToken) == "" || strings.TrimSpace(clientID) == "" ||
		text == "" || len([]rune(text)) > MaxText {
		return &Error{Code: CodeProtocol, Msg: "incomplete iLink text message"}
	}
	msg := map[string]any{
		"from_user_id":  "",
		"to_user_id":    peer,
		"client_id":     clientID,
		"message_type":  typeBot,
		"message_state": stateFinish,
		"item_list":     []any{map[string]any{"type": itemText, "text_item": map[string]string{"text": text}}},
		"context_token": contextToken,
	}
	out, err := c.call(ctx, http.MethodPost, "ilink/bot/sendmessage", map[string]any{"msg": msg, "base_info": c.baseInfo()}, true, apiTimeout, nil)
	if err != nil {
		return err
	}
	return ret(out, "the message")
}

// TypingTicket gets the ticket SetTyping needs for peer.
func (c *Client) TypingTicket(ctx context.Context, peer, contextToken string) (string, error) {
	if strings.TrimSpace(peer) == "" || strings.TrimSpace(contextToken) == "" {
		return "", &Error{Code: CodeProtocol, Msg: "incomplete iLink typing request"}
	}
	out, err := c.call(ctx, http.MethodPost, "ilink/bot/getconfig",
		map[string]any{"ilink_user_id": peer, "context_token": contextToken, "base_info": c.baseInfo()}, true, apiTimeout, nil)
	if err != nil {
		return "", err
	}
	if err := ret(out, "the typing configuration"); err != nil {
		return "", err
	}
	t := str(out, "typing_ticket")
	if t == "" {
		return "", &Error{Code: CodeBadResponse, Msg: "iLink answered no typing ticket", Retry: true}
	}
	return t, nil
}

// SetTyping shows (on) or clears the typing status in peer's chat.
func (c *Client) SetTyping(ctx context.Context, peer, ticket string, on bool) error {
	if strings.TrimSpace(peer) == "" || strings.TrimSpace(ticket) == "" {
		return &Error{Code: CodeProtocol, Msg: "incomplete iLink typing request"}
	}
	status := typingCancel
	if on {
		status = typingOn
	}
	out, err := c.call(ctx, http.MethodPost, "ilink/bot/sendtyping",
		map[string]any{"ilink_user_id": peer, "typing_ticket": ticket, "status": status, "base_info": c.baseInfo()}, true, apiTimeout, nil)
	if err != nil {
		return err
	}
	return ret(out, "the typing status")
}
