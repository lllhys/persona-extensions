package exttest_test

import (
	"context"
	"encoding/json"
	"fmt"
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
			c.hub = hub
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
	// What the hub does not answer here is {}: the program sees an empty owner, and the test no schema failure
	// for its own params.
	if _, err := c.hub.KBOwner(context.Background()); err != nil {
		t.Fatal(err)
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
