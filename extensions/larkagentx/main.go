package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/pluginkit"
	"github.com/nwverse/persona/sdk/go/ext"
)

const id = "larkagentx"
const version = "0.1.1"
const sendAction = "larkagentx_send_text"

//go:embed persona-extension.json
var manifest json.RawMessage

//go:embed bridge.py
var companion []byte

type config struct {
	Command string `json:"bridge_command"`
	Chats   string `json:"allowed_chats"`
	Senders string `json:"allowed_senders"`
	Login   string `json:"login_method"`
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func parseList(text string) ([]string, error) {
	items := strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == '\n' })
	if len(items) == 0 || len(items) > 256 {
		return nil, failure("not_configured")
	}
	for i := range items {
		items[i] = strings.TrimSpace(items[i])
		if !identifier.MatchString(items[i]) {
			return nil, failure("invalid_params")
		}
	}
	return items, nil
}

func (c config) parse() (argv, chats, senders []string, err error) {
	if c.Login != "" && c.Login != "qr" && c.Login != "import" {
		return nil, nil, nil, failure("invalid_params")
	}
	if len(c.Command) > 8192 || json.Unmarshal([]byte(c.Command), &argv) != nil || len(argv) == 0 || len(argv) > 32 || argv[0] == "" {
		return nil, nil, nil, failure("not_configured")
	}
	for _, a := range argv {
		if strings.ContainsRune(a, '\x00') {
			return nil, nil, nil, failure("invalid_params")
		}
	}
	chats, err = parseList(c.Chats)
	if err == nil {
		senders, err = parseList(c.Senders)
	}
	return
}

func failure(code string) error {
	messages := map[string]string{
		"invalid_params":     "Invalid or unauthorized parameters.",
		"not_configured":     "Configure the companion command and both exact allowlists first.",
		"auth_expired":       "Sign-in is missing or expired; sign in again.",
		"unavailable":        "The companion is unavailable; check its installation and SSH connection.",
		"unknown_result":     "Send outcome is unknown. Check Feishu before making a new request; this request will not be retried.",
		"secret_unavailable": "Persona could not persist the encrypted connector state; no new send is allowed.",
		"journal_full":       "The send journal is full; no new send is allowed.",
		"unknown_method":     "Unsupported companion method.",
	}
	if _, ok := messages[code]; !ok {
		code = "unavailable"
	}
	return &ext.Error{Code: code, Message: messages[code]}
}

func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

type message struct {
	ID     string    `json:"id"`
	Chat   string    `json:"chat"`
	Sender string    `json:"sender"`
	Text   string    `json:"text"`
	Anchor string    `json:"anchor,omitempty"`
	At     time.Time `json:"at,omitempty"`
}

type thing = pluginkit.InboxThing[any]

type receipt struct {
	Digest string `json:"digest"`
	Result string `json:"result,omitempty"`
}

type stored struct {
	Session json.RawMessage    `json:"session,omitempty"`
	Seen    []string           `json:"seen,omitempty"`
	Inbox   []thing            `json:"inbox,omitempty"`
	Writes  map[string]receipt `json:"writes,omitempty"`
}

type hub interface {
	Call(context.Context, string, any, any) error
	Notify(string, any) error
}

type connector struct {
	mu                   sync.Mutex
	op                   sync.Mutex
	ctx                  context.Context
	hub                  hub
	argv, chats, senders []string
	factory              func(context.Context, []string) (bridge, error)
	b                    bridge
	state                string
	rules                string
	loginMethod          string
	qr                   string
	data                 stored
	wake                 chan struct{}
}

func newConnector() *connector {
	return &connector{state: "not_signed_in", factory: startBridge, wake: make(chan struct{}, 1)}
}

func (c *connector) program() ext.Program {
	return ext.Program{ID: id, Version: version, Manifest: manifest, Init: c.initialize, Namespaces: map[string]ext.Handler{"connector": c.handle}}
}

func (c *connector) initialize(ctx context.Context, in *ext.Init, h *ext.Hub) (map[string]any, error) {
	var cfg config
	if in.Config(&cfg) != nil {
		return nil, failure("invalid_params")
	}
	c.ctx, c.hub = ctx, h
	c.rules = pluginkit.TriageRules(in.InitializeParams.Config)
	c.loginMethod = cfg.Login
	var err error
	c.argv, c.chats, c.senders, err = cfg.parse()
	if err != nil {
		c.state = "not_configured"
	}
	go c.run()
	return nil, nil
}

func (c *connector) secret(ctx context.Context, method string, out any, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p := map[string]any{"contribution": id}
	if data != nil {
		p["data"] = data
	}
	if c.hub.Call(ctx, "connector.secrets."+method, p, out) != nil {
		return failure("secret_unavailable")
	}
	return nil
}

func (c *connector) save(ctx context.Context) error {
	c.mu.Lock()
	raw, err := json.Marshal(c.data)
	c.mu.Unlock()
	if err != nil {
		return failure("secret_unavailable")
	}
	return c.secret(ctx, "save", nil, raw)
}

func (c *connector) changed() {
	_ = c.hub.Notify("connector.changed", map[string]string{"contribution": id, "scope": "inbox"})
}
func (c *connector) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *connector) run() {
	defer func() { c.op.Lock(); c.detach(); c.op.Unlock() }()
	c.op.Lock()
	var res struct {
		Data []byte `json:"data"`
	}
	err := c.secret(c.ctx, "load", &res, nil)
	c.mu.Lock()
	if err != nil {
		c.state = "error"
	} else if len(res.Data) > 0 {
		if json.Unmarshal(res.Data, &c.data) != nil {
			c.data = stored{}
			c.state = "error"
		} else if len(c.argv) > 0 && len(c.chats) > 0 && len(c.senders) > 0 && len(c.data.Session) > 0 {
			c.state = "error"
		}
	}
	c.mu.Unlock()
	c.op.Unlock()
	wait := 2 * time.Second
	for c.ctx.Err() == nil {
		c.op.Lock()
		c.mu.Lock()
		can := c.b == nil && len(c.data.Session) > 0 && c.state != "not_signed_in" && c.state != "not_configured"
		c.mu.Unlock()
		if can {
			if err := c.open(c.ctx); err != nil {
				c.setError(err)
			} else {
				wait = 2 * time.Second
			}
		}
		c.mu.Lock()
		b := c.b
		state := c.state
		c.mu.Unlock()
		if b != nil && state == "ok" {
			var result struct {
				State string `json:"state"`
			}
			if err := b.Call(c.ctx, "session.check", nil, &result); err != nil {
				c.setError(err)
				c.detach()
			} else if result.State != "ok" {
				c.setError(failure("auth_expired"))
				c.detach()
			}
		}
		c.op.Unlock()
		pause := wait
		if b != nil {
			pause = time.Minute
		} else {
			wait = min(wait*2, time.Minute)
		}
		select {
		case <-c.ctx.Done():
			return
		case <-c.wake:
		case <-time.After(pause):
		}
	}
}

func (c *connector) setError(err error) {
	state := "error"
	if e, ok := err.(*ext.Error); ok && e.Code == "auth_expired" {
		state = "not_signed_in"
	}
	c.mu.Lock()
	c.state = state
	c.mu.Unlock()
	c.changed()
}

func (c *connector) detach() {
	c.mu.Lock()
	b := c.b
	c.b = nil
	c.qr = ""
	c.mu.Unlock()
	if b != nil {
		b.Close()
	}
}

func (c *connector) create() (bridge, error) {
	b, err := c.factory(c.ctx, c.argv)
	if err != nil {
		return nil, failure("unavailable")
	}
	c.mu.Lock()
	c.b = b
	c.mu.Unlock()
	go c.watch(b)
	return b, nil
}

func (c *connector) sessionParams(session json.RawMessage) map[string]any {
	return map[string]any{"credentials": session, "region": "feishu", "allowed_chats": c.chats, "allowed_senders": c.senders}
}

func (c *connector) open(ctx context.Context) error {
	c.detach()
	b, err := c.create()
	if err != nil {
		return err
	}
	c.mu.Lock()
	session := slices.Clone(c.data.Session)
	c.mu.Unlock()
	var result struct {
		State string `json:"state"`
	}
	if err = b.Call(ctx, "session.open", c.sessionParams(session), &result); err != nil {
		c.detach()
		return err
	}
	if result.State != "ok" {
		c.detach()
		return failure("auth_expired")
	}
	c.mu.Lock()
	c.state = "ok"
	c.mu.Unlock()
	c.changed()
	return nil
}

func (c *connector) watch(b bridge) {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-b.Done():
			c.op.Lock()
			c.mu.Lock()
			current := c.b == b
			c.mu.Unlock()
			if current {
				c.detach()
				c.mu.Lock()
				if c.state == "ok" {
					c.state = "error"
				}
				c.mu.Unlock()
				c.changed()
				c.signal()
			}
			c.op.Unlock()
			return
		case ev := <-b.Events():
			c.op.Lock()
			c.mu.Lock()
			current := c.b == b && c.state == "ok"
			c.mu.Unlock()
			if current {
				if ev.Event == "message" {
					c.receive(ev.Message)
				}
				if ev.Event == "status" && ev.State != "ok" {
					err := failure("unavailable")
					if ev.State == "not_signed_in" {
						err = failure("auth_expired")
					}
					c.setError(err)
					c.detach()
					c.signal()
				}
			}
			c.op.Unlock()
		}
	}
}

func (c *connector) receive(m message) {
	if !identifier.MatchString(m.ID) || !identifier.MatchString(m.Chat) || !identifier.MatchString(m.Sender) || (m.Anchor != "" && !identifier.MatchString(m.Anchor)) || !slices.Contains(c.chats, m.Chat) || !slices.Contains(c.senders, m.Sender) || strings.TrimSpace(m.Text) == "" || utf8.RuneCountInString(m.Text) > 4096 {
		return
	}
	key := digest(m.Chat + "\x00" + m.ID)
	c.mu.Lock()
	if slices.Contains(c.data.Seen, key) {
		c.mu.Unlock()
		return
	}
	before := c.data
	c.data.Seen = append(slices.Clone(c.data.Seen), key)
	if len(c.data.Seen) > 4096 {
		c.data.Seen = c.data.Seen[len(c.data.Seen)-4096:]
	}
	thread := m.Chat
	if m.Anchor != "" {
		thread += ":" + m.Anchor
	}
	if m.At.IsZero() {
		m.At = time.Now().UTC()
	}
	t := thing{Thread: thread, Kind: "message", Title: "Feishu · " + m.Chat, Sender: m.Sender, Preview: m.Text, Context: m.Text, At: m.At, Last: m.ID, CanReply: false}
	items := slices.Clone(c.data.Inbox)
	items = slices.DeleteFunc(items, func(x thing) bool { return x.Thread == thread })
	items = append(items, t)
	if len(items) > 128 {
		items = items[len(items)-128:]
	}
	c.data.Inbox = items
	c.mu.Unlock()
	if c.save(c.ctx) != nil {
		c.mu.Lock()
		c.data = before
		c.state = "error"
		c.mu.Unlock()
		c.detach()
	}
	c.changed()
}

func validateSession(raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > 65536 {
		return failure("invalid_params")
	}
	var v struct {
		Region  string            `json:"region"`
		Cookies map[string]string `json:"cookies"`
		UserID  string            `json:"user_id"`
	}
	if json.Unmarshal(raw, &v) != nil || (v.Region != "" && v.Region != "feishu") || len(v.Cookies) == 0 || len(v.Cookies) > 128 {
		return failure("invalid_params")
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil {
		return failure("invalid_params")
	}
	for k := range keys {
		if !slices.Contains([]string{"region", "cookies", "saved_at", "device_id", "app_key", "user_id", "csrf_token", "web_app_id"}, k) {
			return failure("invalid_params")
		}
	}
	return nil
}

func (c *connector) adopt(ctx context.Context, session json.RawMessage) error {
	if err := validateSession(session); err != nil {
		return err
	}
	c.mu.Lock()
	old := c.data
	c.data = stored{Session: slices.Clone(session), Writes: old.Writes}
	c.mu.Unlock()
	if err := c.open(ctx); err != nil {
		c.mu.Lock()
		c.data = old
		c.mu.Unlock()
		c.setError(err)
		return err
	}
	if err := c.save(ctx); err != nil {
		c.detach()
		c.mu.Lock()
		c.data = old
		c.mu.Unlock()
		c.setError(err)
		return err
	}
	c.signal()
	return nil
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "companion-script" {
		_, _ = os.Stdout.Write(companion)
		return
	}
	ext.Main(newConnector().program())
}
