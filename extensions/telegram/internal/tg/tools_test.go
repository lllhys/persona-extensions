package telegram

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/tg/spec"
)

func TestToolsAreDescribed(t *testing.T) {
	c := newConn(&memSecrets{}, nil)
	tools := append(c.Tools(), c.Actions()...)
	if len(tools) != 3+len(spec.CLITools)+len(spec.CLIActions)+1 { // and telegram_cli
		t.Fatalf("%d tools", len(tools))
	}
	for _, tl := range tools {
		if !strings.HasPrefix(tl.Name, "telegram_") || tl.Description == "" || !strings.HasPrefix(string(tl.InputSchema), `{"type":"object"`) {
			t.Errorf("%+v", tl)
		}
	}
}

// The tools read the account's conversations (people and groups only), one conversation by key or exact name, and
// search; times are in the user's zone.
func TestTools(t *testing.T) {
	c, f, ctx := archiveAccount(t)
	c.zone = time.FixedZone("UTC+8", 8*3600)
	at := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC) // 11:04 in the user's zone
	f.mu.Lock()
	f.dialogs[0].Unread, f.dialogs[0].Mentions = 3, 1
	f.history["c2"][0].At = at
	f.mu.Unlock()
	call := func(name, args string) (string, error) { return c.Call(ctx, name, json.RawMessage(args)) }

	out, err := call("telegram_chats", `{}`)
	if err != nil || !strings.Contains(out, "c2 · 示例群 · 群 · 未读 3（@你 1）") || !strings.Contains(out, "u1 · 示例用户 · 私聊") {
		t.Fatalf("chats %q %v", out, err)
	}
	for _, left := range []string{"机器人", "示例频道", "u4"} {
		if strings.Contains(out, left) {
			t.Fatalf("chats lists %s: %q", left, out)
		}
	}
	if out, _ = call("telegram_chats", `{"unread_only":true}`); strings.Contains(out, "u1") || !strings.Contains(out, "c2") {
		t.Fatalf("unread only %q", out)
	}

	out, err = call("telegram_messages", `{"chat":"示例群"}`)
	if err != nil || !strings.Contains(out, "2026-01-02 11:04 示例用户：群里的示例消息") || !strings.Contains(out, "另一个示例用户：回复「群里的示例消息」：[图片]") {
		t.Fatalf("messages by name %q %v", out, err)
	}
	out, err = call("telegram_messages", `{"chat":"u1","limit":1}`)
	if err != nil || !strings.Contains(out, "我：示例回复") || !strings.Contains(out, "before=3") || strings.Contains(out, "示例消息一") {
		t.Fatalf("one message %q %v", out, err)
	}
	if out, _ = call("telegram_messages", `{"chat":"u1","before":3,"limit":1}`); !strings.Contains(out, "示例消息一") {
		t.Fatalf("further back %q", out)
	}
	if _, err := call("telegram_messages", `{"chat":"没有这个会话"}`); err == nil || !strings.Contains(err.Error(), "telegram_chats") {
		t.Fatalf("unknown chat %v", err)
	}
	f.mu.Lock()
	f.dialogs = append(f.dialogs, dialog{Peer: peer{Kind: 'u', ID: 8}, Name: "示例用户", Kind: "dm", Top: 1})
	f.mu.Unlock()
	if _, err := call("telegram_messages", `{"chat":"示例用户"}`); err == nil || !strings.Contains(err.Error(), "2 个会话") {
		t.Fatalf("two of that name %v", err)
	}

	out, err = call("telegram_search", `{"query":"示例消息"}`)
	if err != nil || !strings.Contains(out, "c2 · 示例群") || !strings.Contains(out, "u1 · 示例用户") {
		t.Fatalf("search %q %v", out, err)
	}
	if out, _ = call("telegram_search", `{"query":"示例消息","chat":"c2"}`); strings.Contains(out, "u1") {
		t.Fatalf("search in one chat %q", out)
	}
	if _, err := call("telegram_search", `{"query":" "}`); err == nil {
		t.Fatal("an empty query")
	}
}

// Before the hub knows the user's zone the tools show UTC and say so, not the machine's zone.
func TestToolTimesWithoutTheUserZone(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 0, 0, time.FixedZone("UTC+8", 8*3600))
	if got := New(&memSecrets{}, nil).when(at); got != "2026-01-01 19:04 UTC" {
		t.Fatalf("no zone: %q", got)
	}
	if got := New(&memSecrets{}, time.FixedZone("UTC+8", 8*3600)).when(at); got != "2026-01-02 03:04" {
		t.Fatalf("the user's: %q", got)
	}
}
