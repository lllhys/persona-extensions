package convsrc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

var hubManifest = []byte(`{
  "manifest_version": 1, "id": "convsrc-test", "name": "convsrc test", "version": "1.0.0",
  "engines": {"extension_protocol": 1},
  "runtime": {"command": "bin/convsrc-test"},
  "activation_events": ["on_startup"],
  "permissions": {"hub": ["kb.read", "assistant.read"]}
}`)

// hubFor starts a program on the SDK's fake host and returns its hub, with answers for the assistant calls.
func hubFor(t *testing.T, answers map[string]func(json.RawMessage) (any, error)) *ext.Hub {
	t.Helper()
	var mu sync.Mutex
	var hub *ext.Hub
	prog := ext.Program{ID: "convsrc-test", Version: "1.0.0", Manifest: hubManifest,
		Init: func(_ context.Context, _ *ext.Init, h *ext.Hub) (map[string]any, error) {
			mu.Lock()
			hub = h
			mu.Unlock()
			return nil, nil
		}}
	h := exttest.Start(t, hubManifest, exttest.Serve(t, prog))
	for m, f := range answers {
		h.Answer(m, f)
	}
	mu.Lock()
	defer mu.Unlock()
	return hub
}

// On the hub, the conversations come from assistant.conversations (only those the user opened) and their words from
// assistant.messages, paged by seq until next_seq is gone, an empty page included; they are shaped as a
// transcript's are, and a day reads only that local day.
func TestHubAssistantRead(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 7, h, m, 0, 0, time.UTC) }
	var asked []wire.AssistantMessagesParams
	hub := hubFor(t, map[string]func(json.RawMessage) (any, error){
		"assistant.conversations": func(raw json.RawMessage) (any, error) {
			var p wire.AssistantConversationsParams
			_ = json.Unmarshal(raw, &p)
			if p.Since != "2026-11-07T08:00:00Z" {
				t.Errorf("since %q", p.Since)
			}
			return wire.AssistantConversationsResult{Items: []wire.AssistantConversation{
				{ID: "c-main", Title: "部署的事", Main: true, Origin: "user", Updated: at(20, 0)},
				{ID: "c-task", Title: "巡检任务", Origin: "task", Updated: at(19, 0)},
			}, Total: 2}, nil
		},
		"assistant.messages": func(raw json.RawMessage) (any, error) {
			var p wire.AssistantMessagesParams
			_ = json.Unmarshal(raw, &p)
			asked = append(asked, p)
			two, three := 2, 3
			switch {
			case p.AfterSeq == nil:
				return wire.AssistantMessagesResult{Messages: []wire.AssistantMessage{
					{Seq: 1, At: at(17, 0), Role: "user", Text: "以后部署前先跑一遍冒烟测试"},
					{Seq: 2, At: at(17, 1), Role: "assistant", Text: "好的。"},
				}, NextSeq: &two}, nil
			case *p.AfterSeq == 2:
				return wire.AssistantMessagesResult{Messages: []wire.AssistantMessage{}, NextSeq: &three}, nil // tool work only
			default:
				return wire.AssistantMessagesResult{Messages: []wire.AssistantMessage{
					{Seq: 4, At: at(17, 5), Role: "assistant", Text: "测试都过了。"},
					{Seq: 5, At: at(17, 6), Role: "user", Text: strings.Repeat("贴", 2100)},
				}}, nil
			}
		},
	})
	loc, _ := time.LoadLocation("America/Los_Angeles")
	src := With(emptySource{}, Hub{H: hub}, loc)
	ctx := context.Background()
	es, err := src.Recent(ctx, time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC), time.Time{})
	if err != nil || len(es) != 1 || es[0].ID != "persona-c-main" || es[0].Name != "Persona 对话 · 部署的事" {
		t.Fatalf("recent: %+v %v", es, err)
	}
	r, err := src.Day(ctx, "persona-c-main", "2026-11-07")
	if err != nil || len(r.Messages) != 3 {
		t.Fatalf("day: %+v %v", r, err)
	}
	var ls []norm.Line
	for _, m := range r.Messages {
		ls = append(ls, norm.Parse(m.N, m.Text))
	}
	if ls[0].Speaker != "我" || ls[0].Time != "09:00" || ls[1].Speaker != "助手" || ls[1].Body != "好的。\n测试都过了。" ||
		ls[2].Speaker != "我" || !strings.Contains(ls[2].Body, "疑似粘贴材料") {
		t.Errorf("lines: %+v", ls)
	}
	if len(asked) != 3 || asked[0].Since != "2026-11-07T08:00:00Z" || asked[0].Until != "2026-11-08T08:00:00Z" || asked[0].ID != "c-main" {
		t.Errorf("asked: %+v", asked)
	}
}

// Without the grant (or on a hub without assistant.read), the conversations are left out and the rest goes on.
func TestHubAssistantReadDenied(t *testing.T) {
	hub := hubFor(t, map[string]func(json.RawMessage) (any, error){
		"assistant.conversations": func(json.RawMessage) (any, error) {
			return nil, &ext.Error{Code: wire.ErrorCodePermissionDenied, Message: "assistant.read not granted"}
		},
	})
	var logged []string
	src := With(emptySource{}, Hub{H: hub}, time.UTC)
	src.Logf = func(f string, a ...any) { logged = append(logged, f) }
	es, err := src.Recent(context.Background(), time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC), time.Time{})
	if err != nil || len(es) != 0 || len(logged) != 1 {
		t.Fatalf("recent: %+v %v %v", es, err, logged)
	}
	if _, err := (Hub{H: hub}).Conversations(context.Background(), time.Now(), time.Time{}); !errors.Is(err, ErrNoAccess) {
		t.Errorf("not ErrNoAccess: %v", err)
	}
}

// emptySource is a knowledge base with nothing in it.
type emptySource struct{ kbsrc.Source }

func (emptySource) Recent(context.Context, time.Time, time.Time) ([]wire.KBEntry, error) {
	return nil, nil
}
func (emptySource) Day(context.Context, string, string) (wire.KBDayResult, error) {
	return wire.KBDayResult{}, kbsrc.ErrNotFound
}
func (emptySource) Get(context.Context, string, string) (wire.KBDetail, error) {
	return wire.KBDetail{}, kbsrc.ErrNotFound
}
