package exttest_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

var connectorManifest = []byte(`{
  "manifest_version": 1, "id": "examplechat", "name": "Example Chat", "version": "1.0.0",
  "engines": {"extension_protocol": 1},
  "runtime": {"command": "bin/examplechat"},
  "activation_events": ["on_startup"],
  "permissions": {"hub": ["kb.read"]},
  "contributes": {
    "connectors": [{"id": "examplechat", "name": "Example Chat", "capabilities": ["status", "inbox", "threads", "archive", "login", "events"], "kinds": ["mention"]}],
    "events": [{"id": "knowledge", "subscribe": ["kb.changed"]}]
  }
}`)

// exampleChat is a connector written with the SDK alone: its calls are the schema's types.
type exampleChat struct {
	hub     *ext.Hub
	init    *ext.Init
	mu      sync.Mutex
	heard   []string
	account string
}

func (c *exampleChat) program() ext.Program {
	conn := ext.Methods{
		"status": ext.Method(func(ctx context.Context, p wire.ConnectorParams) (wire.ConnectorStatus, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.account == "" {
				return wire.ConnectorStatus{State: wire.ConnectorStateNotSignedIn, Error: "Not signed in."}, nil
			}
			return wire.ConnectorStatus{State: wire.ConnectorStateOk, Account: c.account}, nil
		}),
		"inbox.collect": ext.Method(func(ctx context.Context, p wire.ConnectorParams) (wire.ConnectorCollectResult, error) {
			return wire.ConnectorCollectResult{Things: []wire.ConnectorInboxThing{{Thread: "chat_1", Kind: "mention",
				Title: "Example group", At: time.Date(2026, 11, 4, 8, 26, 0, 0, time.UTC), Last: "6", Tier: wire.ConnectorTierAction}}}, nil
		}),
		"thread.page": ext.Method(func(ctx context.Context, p wire.ConnectorThreadParams) (wire.ConnectorThreadPage, error) {
			return wire.ConnectorThreadPage{Chat: wire.ConnectorThreadChat{ID: p.Thread, Name: "Example group", Kind: "group"},
				Messages: []wire.ConnectorThreadMessage{{ID: "6", At: time.Date(2026, 11, 4, 8, 26, 0, 0, time.UTC),
					Sender: wire.ConnectorThreadPerson{Name: "Example A"}, Text: "Hello"}}}, nil
		}),
		"archive.changes": ext.Method(func(ctx context.Context, p wire.ConnectorChangesParams) (wire.ConnectorChangesResult, error) {
			return wire.ConnectorChangesResult{Next: "1"}, nil
		}),
		"login.step": ext.Method(func(ctx context.Context, p wire.ConnectorLoginParams) (wire.ConnectorPrompt, error) {
			if p.Step == "" {
				return wire.ConnectorPrompt{Step: "key", Fields: []wire.ConnectorField{{Name: "app_key", Label: "Application key", Secret: true}}}, nil
			}
			if err := c.hub.SaveSecret(ctx, p.Contribution, []byte(p.Values["app_key"])); err != nil {
				return wire.ConnectorPrompt{}, err
			}
			c.mu.Lock()
			c.account = "user_example_a"
			c.mu.Unlock()
			_ = c.hub.ConnectorChanged(p.Contribution, wire.ConnectorScopeInbox)
			return wire.ConnectorPrompt{Done: true, Account: "user_example_a"}, nil
		}),
		"logout": ext.Method(func(ctx context.Context, p wire.ConnectorParams) (ext.Empty, error) {
			return ext.Empty{}, c.hub.DeleteSecret(ctx, p.Contribution)
		}),
	}
	return ext.Program{ID: "examplechat", Version: "1.0.0", Manifest: connectorManifest,
		Init: func(ctx context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
			c.hub, c.init = hub, in
			return nil, nil
		},
		Namespaces: map[string]ext.Handler{
			ext.ConnectorNamespace: conn.For("examplechat"),
			"event": func(ctx context.Context, method string, params json.RawMessage) (any, error) {
				c.mu.Lock()
				c.heard = append(c.heard, method)
				c.mu.Unlock()
				return nil, nil
			},
		},
	}
}

func TestAConnectorWrittenWithTheSDK(t *testing.T) {
	c := &exampleChat{}
	h := exttest.CheckConnectors(t, connectorManifest, exttest.Serve(t, c.program()))

	var p wire.ConnectorPrompt
	if err := h.Call("connector.login.step", wire.ConnectorLoginParams{Contribution: "examplechat", Step: "key",
		Values: map[string]string{"app_key": "fixture-key"}}, &p); err != nil || !p.Done {
		t.Fatalf("sign in: %+v %v", p, err)
	}
	if got := string(h.Secret("examplechat")); got != "fixture-key" {
		t.Fatalf("the secret kept: %q", got)
	}
	var st wire.ConnectorStatus
	if err := h.Call("connector.status", wire.ConnectorParams{Contribution: "examplechat"}, &st); err != nil || st.State != wire.ConnectorStateOk {
		t.Fatalf("status after sign-in: %+v %v", st, err)
	}
	if err := h.Call("connector.logout", wire.ConnectorParams{Contribution: "examplechat"}, nil); err != nil || h.Secret("examplechat") != nil {
		t.Fatalf("logout: %v, secret %q", err, h.Secret("examplechat"))
	}
	waitFor(t, func() bool { return len(h.Events()) > 0 })
	if e := h.Events()[0]; e.Name != "connector.changed" || !strings.Contains(string(e.Params), `"inbox"`) {
		t.Fatalf("event %s %s", e.Name, e.Params)
	}
	if err := h.Hear("event.kb.changed", wire.KBChangedEvent{Kind: "chats", ID: "examplechat-chat_1", What: "messages"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { c.mu.Lock(); defer c.mu.Unlock(); return len(c.heard) > 0 })
	if c.heard[0] != "kb.changed" {
		t.Fatalf("heard %v", c.heard)
	}
}

func TestTheKnowledgeBaseFromTheSDK(t *testing.T) {
	c := &exampleChat{}
	h := exttest.Start(t, connectorManifest, exttest.Serve(t, c.program()))
	h.Answer("kb.list", func(raw json.RawMessage) (any, error) {
		var p wire.KBListParams
		_ = json.Unmarshal(raw, &p)
		return wire.KBListResult{Items: []wire.KBEntry{{Kind: p.Kind, ID: "example", Name: "Example", Relevance: 50,
			Updated: time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)}}, Tags: []string{}, Total: 1}, nil
	})
	got, err := c.hub.KBList(context.Background(), wire.KBListParams{Kind: wire.KBKindProjects, Limit: 10})
	if err != nil || len(got.Items) != 1 || got.Items[0].ID != "example" {
		t.Fatalf("kb.list: %+v %v", got, err)
	}
	// As the real hub: without an answer there is no knowledge base, without kb.write no update, and a method
	// it does not have is unknown.
	ctx := context.Background()
	var e *ext.Error
	if _, err := c.hub.KBOwner(ctx); !errors.As(err, &e) || e.Code != wire.KBErrorCodeKbUnavailable {
		t.Errorf("kb.owner without an answer: %v", err)
	}
	if _, err := c.hub.KBUpdate(ctx, wire.KBUpdateParams{Kind: wire.KBKindProjects, ID: "example"}); !errors.As(err, &e) ||
		e.Code != wire.ErrorCodePermissionDenied || !strings.Contains(string(e.Data), "kb.write") {
		t.Errorf("kb.update without kb.write: %v", err)
	}
	if err := c.hub.Call(ctx, "kb.lst", map[string]any{}, nil); !errors.As(err, &e) || e.Code != wire.ErrorCodeUnknownMethod {
		t.Errorf("a misspelled kb method: %v", err)
	}
	// The extension's only connector takes its calls whatever they name, as the hub routes them.
	if _, err := c.hub.LoadSecret(ctx, ""); err != nil {
		t.Errorf("the only connector's secret, no contribution named: %v", err)
	}
	if err := c.hub.Call(ctx, "kb.update", map[string]any{"kind": "projects", "id": "x"}, nil); !errors.As(err, &e) ||
		e.Code != wire.ErrorCodePermissionDenied {
		t.Errorf("kb.update by the program, not granted: %v", err)
	}
	if _, err := c.hub.PutPosts(ctx, wire.ConnectorPostsPutParams{Contribution: "examplechat", Posts: []wire.ConnectorPostPut{}}); err == nil ||
		!strings.Contains(err.Error(), "no knowledge base") {
		t.Errorf("posts.put with no knowledge base: %v", err)
	}
	// An answer the test gives replaces the host's own, secrets too.
	h.Answer("connector.secrets.load", func(json.RawMessage) (any, error) { return nil, &ext.Error{Message: "the store is down"} })
	if _, err := c.hub.LoadSecret(ctx, "examplechat"); err == nil || !strings.Contains(err.Error(), "store is down") {
		t.Errorf("secrets.load answered by the test: %v", err)
	}
	// What the real hub starts it with: its contributions active, its permissions granted, the hub's capabilities.
	if in := c.init; in == nil || !slices.Contains(in.Active["connectors"], "examplechat") || !slices.Contains(in.Active["events"], "knowledge") ||
		!slices.Contains(in.Granted.Hub, "kb.read") || !slices.Contains(in.Hub.Capabilities, wire.HubCapabilityInboxGroup) {
		t.Errorf("initialize: %+v", c.init)
	}
}

// A connector that sends in the user's name on its own declares it (sends_as_user) and tells the hub of each send; as
// the real hub, the fake refuses a connector that does not declare it, and holds the params to the schema.
func TestRecordSentFromTheSDK(t *testing.T) {
	manifest := []byte(`{
  "manifest_version": 1, "id": "examplechat", "name": "Example Chat", "version": "1.0.0",
  "engines": {"extension_protocol": 1},
  "runtime": {"command": "bin/examplechat"},
  "activation_events": ["on_startup"],
  "contributes": {"connectors": [
    {"id": "examplechat", "name": "Example Chat", "capabilities": ["status"], "sends_as_user": true},
    {"id": "examplequiet", "name": "Example Quiet", "capabilities": ["status"]}
  ]}
}`)
	c := &exampleChat{}
	prog := c.program()
	prog.Manifest = manifest
	h := exttest.Start(t, manifest, exttest.Serve(t, prog))
	ctx := context.Background()
	err := c.hub.RecordSent(ctx, wire.ConnectorSentRecordParams{Contribution: "examplechat", Target: map[string]json.RawMessage{"chat": json.RawMessage(`"chat_1"`)},
		Text: "Thanks, got it.", Summary: "Auto-reply", SessionID: "conv_1", Setting: "auto_reply"})
	if got := h.Sent(); err != nil || len(got) != 1 || got[0].Contribution != "examplechat" || got[0].Text != "Thanks, got it." ||
		got[0].Setting != "auto_reply" || string(got[0].Target["chat"]) != `"chat_1"` {
		t.Fatalf("sent.record: %v, recorded %+v", err, h.Sent())
	}
	// As the hub keeps it: 20 keys of the target at most, in key order.
	many := map[string]json.RawMessage{}
	for i := range 25 {
		many[fmt.Sprintf("k%02d", i)] = json.RawMessage(`"v"`)
	}
	if err := c.hub.RecordSent(ctx, wire.ConnectorSentRecordParams{Contribution: "examplechat", Target: many, Text: ""}); err != nil {
		t.Fatalf("a long target and an empty text: %v", err)
	}
	if got := h.Sent(); len(got) != 2 || len(got[1].Target) != 20 || got[1].Target["k19"] == nil || got[1].Target["k20"] != nil {
		t.Errorf("the target as the hub keeps it: %+v", got)
	}
	var e *ext.Error
	err = c.hub.RecordSent(ctx, wire.ConnectorSentRecordParams{Contribution: "examplequiet", Target: map[string]json.RawMessage{}, Text: "x"})
	if !errors.As(err, &e) || e.Code != wire.ErrorCodePermissionDenied || len(h.Sent()) != 2 {
		t.Errorf("a connector that does not declare sends_as_user: %v, recorded %+v", err, h.Sent())
	}
	err = c.hub.RecordSent(ctx, wire.ConnectorSentRecordParams{Contribution: "another", Target: map[string]json.RawMessage{}, Text: "x"})
	if !errors.As(err, &e) || e.Code != wire.ErrorCodePermissionDenied || len(h.Sent()) != 2 {
		t.Errorf("a connector the extension does not have: %v, recorded %+v", err, h.Sent())
	}
}

func TestStartWithAnotherGrant(t *testing.T) {
	c := &exampleChat{}
	h := exttest.StartWith(t, connectorManifest, exttest.Serve(t, c.program()), func(p *wire.InitializeParams) {
		p.Granted = wire.Permissions{Hub: []string{"kb.read", "kb.write"}}
	})
	h.Answer("kb.update", func(json.RawMessage) (any, error) { return wire.KBUpdateResult{ID: "example"}, nil })
	tags := []string{}
	got, err := c.hub.KBUpdate(context.Background(), wire.KBUpdateParams{Kind: wire.KBKindProjects, ID: "example", Tags: tags})
	if err != nil || got.ID != "example" {
		t.Fatalf("kb.update granted: %+v %v", got, err)
	}
	b, _ := json.Marshal(wire.KBUpdateParams{Kind: wire.KBKindProjects, ID: "example", Tags: tags})
	if !strings.Contains(string(b), `"tags":[]`) {
		t.Errorf("an empty tag list clears the tags, so it is sent: %s", b)
	}
	b, _ = json.Marshal(wire.ConnectorPostPut{Post: wire.ConnectorPost{ID: "p"}, Comments: []wire.ConnectorPostComment{}})
	if !strings.Contains(string(b), `"comments":[]`) {
		t.Errorf("an empty comment list says the post has none, so it is sent: %s", b)
	}
}

// A message that does not hold to the schema fails the test that sent or got it.
func TestHostHoldsMessagesToTheSchema(t *testing.T) {
	ft := &failures{TB: t}
	c := &exampleChat{}
	prog := c.program()
	prog.Namespaces[ext.ConnectorNamespace] = ext.Methods{
		"status": ext.Method(func(ctx context.Context, p wire.ConnectorParams) (map[string]any, error) {
			return map[string]any{"account": "no state"}, nil
		}),
	}.For("examplechat")
	h := exttest.Start(ft, connectorManifest, exttest.Serve(t, prog))
	_ = h.Call("connector.status", wire.ConnectorParams{Contribution: "examplechat"}, nil)
	ft.runCleanups()
	if len(ft.errs) == 0 || !strings.Contains(strings.Join(ft.errs, "\n"), "no state") {
		t.Fatalf("a status without its state passed: %v", ft.errs)
	}
}

// failures is a testing.TB that keeps errors instead of failing, and runs its cleanups when told.
type failures struct {
	testing.TB
	errs     []string
	cleanups []func()
}

func (f *failures) Errorf(format string, args ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}
func (f *failures) Cleanup(fn func()) { f.cleanups = append(f.cleanups, fn) }
func (f *failures) runCleanups() {
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 200 {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

// A program with two connectors names the one it means: another is no_contribution, as on the hub.
func TestTwoConnectorsNameTheirs(t *testing.T) {
	two := []byte(strings.Replace(string(connectorManifest), `"connectors": [`,
		`"connectors": [{"id": "examplemail", "name": "Example Mail", "capabilities": ["status"]}, `, 1))
	c := &exampleChat{}
	prog := c.program()
	prog.Manifest = two
	exttest.Start(t, two, exttest.Serve(t, prog))
	var e *ext.Error
	if _, err := c.hub.LoadSecret(context.Background(), "another"); !errors.As(err, &e) || e.Code != wire.ErrorCodeNoContribution {
		t.Errorf("another connector's secret: %v", err)
	}
	if _, err := c.hub.LoadSecret(context.Background(), "examplemail"); err != nil {
		t.Errorf("its own second connector's secret: %v", err)
	}
}
