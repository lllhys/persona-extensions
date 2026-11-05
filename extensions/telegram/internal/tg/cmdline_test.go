package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// telegram_cli plans a command line and shows an answer from the words alone: signed out, it still answers; a field it
// does not know is refused.
func TestCommandLineTool(t *testing.T) {
	c, ctx := New(&memSecrets{}, nil), context.Background()
	out, err := c.Call(ctx, "telegram_cli", json.RawMessage(`{"argv":["send","u7","-"],"stdin":"第一行\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Name string `json:"name"`
		Call struct {
			Tool string         `json:"tool"`
			Args map[string]any `json:"args"`
		} `json:"call"`
		Render string `json:"render"`
	}
	if err := json.Unmarshal([]byte(out), &plan); err != nil || plan.Name != "tg" || plan.Call.Tool != "telegram_send" ||
		plan.Call.Args["text"] != "第一行" || plan.Render != "send" {
		t.Fatalf("plan: %s (%v)", out, err)
	}
	if out, _ = c.Call(ctx, "telegram_cli", json.RawMessage(`{"argv":["nope"]}`)); !strings.Contains(out, `"exit":2`) {
		t.Fatalf("a wrong command line: %s", out)
	}
	out, err = c.Call(ctx, "telegram_cli", json.RawMessage(`{"render":"send","result":"{\"sent\":{\"id\":992,\"at\":\"2026-11-03T09:41:00+08:00\",\"mine\":true,\"text\":\"好的\"},\"chat\":\"u7\"}"}`))
	if err != nil || out != `{"stdout":"已发送到 u7（消息 992）\n"}` {
		t.Fatalf("render: %s (%v)", out, err)
	}
	if _, err := c.Call(ctx, "telegram_cli", json.RawMessage(`{"argv":[],"format":"text"}`)); err == nil {
		t.Fatal("an unknown field was taken")
	}
	if _, err := c.Call(ctx, "telegram_list_chats", nil); err == nil {
		t.Fatal("a read ran signed out")
	}
}
