package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

var syntheticSession = json.RawMessage(`{"region":"feishu","cookies":{"session":"synthetic-not-a-credential"},"user_id":"self-test"}`)

type memoryHub struct {
	mu      sync.Mutex
	data    []byte
	fail    bool
	targets []string
	events  []string
}

func (h *memoryHub) Call(_ context.Context, method string, p, out any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	params := p.(map[string]any)
	h.targets = append(h.targets, params["contribution"].(string))
	if h.fail {
		return errors.New("synthetic-cookie-should-not-leak")
	}
	switch method {
	case "connector.secrets.load":
		raw, _ := json.Marshal(map[string]any{"data": h.data})
		return json.Unmarshal(raw, out)
	case "connector.secrets.save":
		h.data = slices.Clone(params["data"].([]byte))
	case "connector.secrets.delete":
		h.data = nil
	default:
		return errors.New("unexpected hub method")
	}
	return nil
}
func (h *memoryHub) Notify(event string, p any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, event+":"+p.(map[string]string)["contribution"])
	return nil
}

type fakeBridge struct {
	mu     sync.Mutex
	calls  []string
	fail   string
	poll   string
	events chan bridgeEvent
	done   chan struct{}
	stop   sync.Once
}

func newFakeBridge() *fakeBridge {
	return &fakeBridge{events: make(chan bridgeEvent, 8), done: make(chan struct{}), poll: "pending"}
}
func (b *fakeBridge) Call(_ context.Context, m string, p, out any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, m)
	if b.fail == m {
		return failure("auth_expired")
	}
	result := any(map[string]any{"state": "ok"})
	switch m {
	case "session.send":
		result = map[string]string{"message_id": "message-test-sent"}
	case "login.begin":
		result = map[string]string{"state": "pending", "qr": "synthetic-qr-payload"}
	case "login.poll":
		result = map[string]any{"state": b.poll, "credentials": syntheticSession}
	}
	if out != nil {
		raw, _ := json.Marshal(result)
		return json.Unmarshal(raw, out)
	}
	return nil
}
func (b *fakeBridge) Events() <-chan bridgeEvent { return b.events }
func (b *fakeBridge) Done() <-chan struct{}      { return b.done }
func (b *fakeBridge) Close()                     { b.stop.Do(func() { close(b.done) }) }
func (b *fakeBridge) count(m string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(slices.DeleteFunc(slices.Clone(b.calls), func(x string) bool { return x != m }))
}

func fixture(t *testing.T) (*connector, *memoryHub, *fakeBridge) {
	t.Helper()
	c := newConnector()
	ctx, cancel := context.WithCancel(context.Background())
	c.ctx = ctx
	h := &memoryHub{}
	b := newFakeBridge()
	c.hub = h
	c.argv = []string{"synthetic-command"}
	c.chats = []string{"chat-test"}
	c.senders = []string{"sender-test"}
	c.b = b
	c.state = "ok"
	c.data.Session = slices.Clone(syntheticSession)
	c.factory = func(context.Context, []string) (bridge, error) { return newFakeBridge(), nil }
	t.Cleanup(func() { cancel(); c.op.Lock(); c.detach(); c.op.Unlock() })
	return c, h, b
}
func params(v map[string]any) json.RawMessage {
	v["contribution"] = id
	raw, _ := json.Marshal(v)
	return raw
}
func invoke(c *connector, m string, v map[string]any) (any, error) {
	return c.handle(context.Background(), m, params(v))
}
func sendParams(req, text string) map[string]any {
	return map[string]any{"name": sendAction, "request_id": req, "args": sendArgs{Chat: "chat-test", Text: text}}
}

func TestManifestAndConformance(t *testing.T) {
	if err := wire.Validate("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile("persona-plugin.json")
	if err != nil || string(old) != string(manifest) {
		t.Fatal("legacy manifest differs", err)
	}
	if _, err := pluginkit.CheckManifest(manifest); err != nil {
		t.Fatal(err)
	}
	if err := pluginkit.CheckStandardSettings(manifest); err != nil {
		t.Fatal(err)
	}
	var m wire.Manifest
	_ = json.Unmarshal(manifest, &m)
	if m.ID != id || len(m.Contributes.Connectors) != 1 || m.Contributes.Connectors[0].ID == "lark" || len(m.Contributes.Channel) != 0 || len(m.Contributes.API) != 0 || len(m.Contributes.AgentSources) != 0 || !slices.Contains(m.Contributes.Connectors[0].ConfirmAlways, sendAction) {
		t.Fatal("scope or confirmation violation")
	}
	init := exttest.Check(t, manifest, exttest.Serve(t, newConnector().program()))
	if init.ID != id {
		t.Fatal(init.ID)
	}
}

func TestConfigurationFailsClosed(t *testing.T) {
	for _, cfg := range []config{{}, {Command: `["ssh","companion.example.com"]`, Chats: "chat-test", Senders: "*"}, {Command: `["ssh"]`, Chats: "", Senders: "sender-test"}, {Command: `"ssh companion"`, Chats: "chat-test", Senders: "sender-test"}} {
		if _, _, _, err := cfg.parse(); err == nil {
			t.Fatal("accepted unsafe configuration")
		}
	}
	argv, chats, senders, err := (config{Command: `["ssh","-T","companion.example.com","python3","-I","/opt/adapter/bridge.py"]`, Chats: "chat-test,chat-other", Senders: "sender-test\nsender-other"}).parse()
	if err != nil || len(argv) != 6 || len(chats) != 2 || len(senders) != 2 {
		t.Fatal(argv, chats, senders, err)
	}
}

func TestReceiveAuthorizationAndReconnectDedup(t *testing.T) {
	c, h, _ := fixture(t)
	m := message{ID: "message-test", Chat: "chat-test", Sender: "sender-test", Text: "Synthetic message", At: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	for _, bad := range []message{{ID: m.ID, Chat: "chat-denied", Sender: m.Sender, Text: m.Text}, {ID: m.ID, Chat: m.Chat, Sender: "sender-denied", Text: m.Text}, {ID: m.ID, Chat: m.Chat, Sender: m.Sender, Text: ""}} {
		c.receive(bad)
	}
	c.receive(m)
	c.receive(m)
	if len(c.data.Inbox) != 1 || len(c.data.Seen) != 1 {
		t.Fatal(c.data)
	}
	c.rules = "Synthetic triage rule"
	out, err := invoke(c, "inbox.collect", map[string]any{})
	if err != nil || pluginkit.CheckThings(out, c.rules, false) != nil {
		t.Fatal(out, err)
	}
	if c.data.Inbox[0].CanReply {
		t.Fatal("inbox.reply must not bypass per-send gate")
	}
	c2, _, _ := fixture(t)
	if json.Unmarshal(h.data, &c2.data) != nil {
		t.Fatal("invalid secret state")
	}
	c2.receive(m)
	if len(c2.data.Seen) != 1 || len(c2.data.Inbox) != 1 {
		t.Fatal("restart duplicated message")
	}
	for _, target := range h.targets {
		if target != "larkagentx" {
			t.Fatal("touched another connector")
		}
	}
}

func TestConfirmedWriteJournal(t *testing.T) {
	c, h, b := fixture(t)
	p := sendParams("request-test", "Synthetic outbound text")
	card, err := invoke(c, "actions.describe", p)
	if err != nil || !strings.Contains(card.(map[string]any)["summary"].(string), "Synthetic outbound text") || b.count("session.send") != 0 {
		t.Fatal(card, err)
	}
	if _, err = invoke(c, "actions.do", sendParams("", "text")); err == nil {
		t.Fatal("missing confirmation request accepted")
	}
	if _, err = invoke(c, "tools.call", p); err == nil {
		t.Fatal("read tool bypassed confirmation")
	}
	if _, err = invoke(c, "inbox.reply", p); err == nil {
		t.Fatal("reply bypassed confirmation")
	}
	first, err := invoke(c, "actions.do", p)
	if err != nil {
		t.Fatal(err)
	}
	second, err := invoke(c, "actions.do", p)
	if err != nil || b.count("session.send") != 1 {
		t.Fatal(second, err, b.calls)
	}
	if first.(map[string]string)["text"] != second.(map[string]string)["text"] {
		t.Fatal("receipt changed")
	}
	if _, err = invoke(c, "actions.do", sendParams("request-test", "Different text")); err == nil {
		t.Fatal("same ID changed confirmed text")
	}
	c2, _, b2 := fixture(t)
	_ = json.Unmarshal(h.data, &c2.data)
	if _, err = invoke(c2, "actions.do", p); err != nil || b2.count("session.send") != 0 {
		t.Fatal("restart repeated write", err)
	}
}

func TestWriteReservationFailuresAndUnknownOutcome(t *testing.T) {
	c, h, b := fixture(t)
	h.fail = true
	if _, err := invoke(c, "actions.do", sendParams("reserve-test", "Synthetic text")); err == nil || strings.Contains(err.Error(), "synthetic-cookie") {
		t.Fatal("secret failure not sanitized")
	}
	if b.count("session.send") != 0 {
		t.Fatal("sent before durable reservation")
	}
	c, h, b = fixture(t)
	b.fail = "session.send"
	p := sendParams("unknown-test", "Synthetic text")
	if _, err := invoke(c, "actions.do", p); err == nil {
		t.Fatal("accepted unknown outcome")
	}
	c2, _, b2 := fixture(t)
	_ = json.Unmarshal(h.data, &c2.data)
	if _, err := invoke(c2, "actions.do", p); err == nil || b2.count("session.send") != 0 {
		t.Fatal("unknown result resent")
	}
}

func TestLoginExpiryLogoutAndLateEvents(t *testing.T) {
	c, h, _ := fixture(t)
	b := newFakeBridge()
	c.factory = func(context.Context, []string) (bridge, error) { return b, nil }
	prompt, err := invoke(c, "login.step", map[string]any{})
	if err != nil || prompt.(map[string]any)["kind"] != "qr" {
		t.Fatal(prompt, err)
	}
	b.poll = "ready"
	c.factory = func(context.Context, []string) (bridge, error) { return newFakeBridge(), nil }
	if _, err = invoke(c, "login.step", map[string]any{"step": "qr"}); err != nil || len(h.data) == 0 || c.state != "ok" {
		t.Fatal(err)
	}
	old := c.b
	if _, err = invoke(c, "logout", map[string]any{}); err != nil || len(h.data) != 0 || len(c.data.Session) != 0 || c.state != "not_signed_in" {
		t.Fatal(err)
	}
	select {
	case <-old.Done():
	default:
		t.Fatal("logout did not stop companion")
	}
	if c.b != nil {
		t.Fatal("late companion still attached")
	}
	if err := validateSession(json.RawMessage(`{"region":"lark","cookies":{"session":"synthetic"}}`)); err == nil {
		t.Fatal("unverified Lark accepted")
	}
	if err := validateSession(json.RawMessage(`{"region":"feishu","cookies":{"session":"synthetic"},"endpoint":"https://invalid.example.com"}`)); err == nil {
		t.Fatal("arbitrary endpoint accepted")
	}
	for _, code := range []string{"auth_expired", "unavailable", "raw-cookie=synthetic"} {
		if strings.Contains(failure(code).Error(), "raw-cookie") {
			t.Fatal("error leak")
		}
	}
}

func TestCoexistenceDoesNotHandlelarkintegration(t *testing.T) {
	c, h, _ := fixture(t)
	for _, target := range []string{"larkintegration", "lark", "feishu"} {
		raw, _ := json.Marshal(map[string]string{"contribution": target})
		_, err := c.handle(context.Background(), "logout", raw)
		if e, ok := err.(*ext.Error); !ok || e.Code != wire.ErrorCodeNoContribution {
			t.Fatal("accepted larkintegration contribution", target, err)
		}
	}
	if len(h.targets) != 0 {
		t.Fatal("changed larkintegration secrets")
	}
	var m wire.Manifest
	_ = json.Unmarshal(manifest, &m)
	if m.Contributes.Connectors[0].ID != "larkagentx" || m.ID == "larkintegration" {
		t.Fatal("identity collision")
	}
}

func TestConcurrentSameRequest(t *testing.T) {
	c, _, b := fixture(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := invoke(c, "actions.do", sendParams("parallel-test", "Synthetic text")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if b.count("session.send") != 1 {
		t.Fatal(b.calls)
	}
}
