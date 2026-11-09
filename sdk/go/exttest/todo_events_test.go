package exttest_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

var todoManifest = []byte(`{
  "manifest_version": 1, "id": "example-todo-thread", "name": "Example To-do Thread", "version": "1.0.0",
  "engines": {"extension_protocol": 1},
  "runtime": {"command": "bin/example-todo-thread"},
  "activation_events": ["on_startup"],
  "contributes": {"events": [{"id": "todos", "subscribe": ["todo.changed", "todo.decision"]}]},
  "permissions": {"hub": ["todos.read"]}
}`)

// An extension that follows the to-dos (event.todo.changed, event.todo.decision; permission todos.read) hears them as
// the wire types, and the harness holds what it is told to the schema (todos.schema.json).
func TestTodoEventsFromTheSDK(t *testing.T) {
	var mu sync.Mutex
	var changed []wire.TodoChangedEvent
	var decisions []wire.TodoDecisionEvent
	prog := ext.Program{ID: "example-todo-thread", Version: "1.0.0", Manifest: todoManifest,
		Namespaces: map[string]ext.Handler{
			"event": func(ctx context.Context, method string, params json.RawMessage) (any, error) {
				mu.Lock()
				defer mu.Unlock()
				switch method {
				case "todo.changed":
					var e wire.TodoChangedEvent
					if err := json.Unmarshal(params, &e); err != nil {
						return nil, err
					}
					changed = append(changed, e)
				case "todo.decision":
					var e wire.TodoDecisionEvent
					if err := json.Unmarshal(params, &e); err != nil {
						return nil, err
					}
					decisions = append(decisions, e)
				}
				return nil, nil
			},
		},
	}
	h := exttest.Start(t, todoManifest, exttest.Serve(t, prog))

	thread := &wire.TodoEventThread{Channel: "feishu", ChatID: "oc_example_1", ThreadID: "omt_example_1", Title: "Release 1.3"}
	if err := h.Hear("event.todo.changed", wire.TodoChangedEvent{ID: "todo_example_1", Title: "Ship the release", What: "updated", Done: true, Thread: thread}); err != nil {
		t.Fatal(err)
	}
	if err := h.Hear("event.todo.decision", wire.TodoDecisionEvent{ID: "todo_example_1", Title: "Ship the release", Thread: thread,
		Decision: wire.TodoEventDecision{ID: "dec_example_1", Question: "Which branch", Choice: "release-1.3", Source: "thread"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(changed) == 1 && len(decisions) == 1 })
	mu.Lock()
	defer mu.Unlock()
	if c := changed[0]; c.ID != "todo_example_1" || c.What != "updated" || !c.Done || c.Title != "Ship the release" || c.Thread == nil || c.Thread.ThreadID != "omt_example_1" {
		t.Fatalf("todo.changed: %+v", c)
	}
	if d := decisions[0]; d.ID != "todo_example_1" || d.Decision.ID != "dec_example_1" || d.Decision.Choice != "release-1.3" || d.Decision.Source != "thread" || d.Thread == nil || d.Thread.Channel != "feishu" {
		t.Fatalf("todo.decision: %+v", d)
	}
}
