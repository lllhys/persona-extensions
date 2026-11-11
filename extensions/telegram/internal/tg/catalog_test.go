package telegram

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The catalog has every tool of tools.list under the source the hub derived before (telegram, the names without
// "telegram_"); messages and search answer JSON a subscription can follow message by message, an id unique across
// chats on each, while the assistant's tools keep their text.
func TestCatalog(t *testing.T) {
	c, f, ctx := archiveAccount(t)
	c.zone = time.FixedZone("UTC+8", 8*3600)
	f.mu.Lock()
	f.history["c2"][0].At = time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	f.mu.Unlock()

	sources, err := c.Catalog(ctx)
	if err != nil || len(sources) != 1 || sources[0].ID != "telegram" || len(sources[0].Tools) != len(c.Tools()) {
		t.Fatalf("catalog %+v %v", sources, err)
	}
	if err := wire.CheckConnectorCatalog(wire.ConnectorCatalog{Sources: sources}); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range sources[0].Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"chats", "messages", "search", "read_messages", "cli"} {
		if !names[want] {
			t.Errorf("no %s in %v", want, names)
		}
	}

	call := func(tool, args string) (string, error) {
		return c.CatalogCall(ctx, connector.CatalogQuery{Source: "telegram", Tool: tool, Args: json.RawMessage(args)})
	}
	type item struct {
		ID, Thread, Title, Sender, Time, Body string
		Message                               int
		Mine                                  bool
	}
	out, err := call("messages", `{"chat":"示例群"}`)
	var page struct {
		Chat     struct{ ID, Name string }
		More     bool
		Messages []item
	}
	if err != nil || json.Unmarshal([]byte(out), &page) != nil || page.Chat.ID != "c2" || len(page.Messages) == 0 {
		t.Fatalf("messages %s %v", out, err)
	}
	first := page.Messages[0]
	if first.ID != "c2:"+itoa(first.Message) || first.Thread != "c2" || first.Title != "示例群" || first.Sender != "示例用户" ||
		first.Time != "2026-01-02T11:04:00+08:00" || first.Body != "群里的示例消息" {
		t.Fatalf("a message %+v", first)
	}
	if out, err = call("messages", `{"chat":"u1","limit":1}`); err != nil || !strings.Contains(out, `"more":true`) || !strings.Contains(out, `"mine":true`) {
		t.Fatalf("one message %s %v", out, err)
	}
	var found struct {
		Query    string
		Messages []item
	}
	out, err = call("search", `{"query":"示例消息"}`)
	if err != nil || json.Unmarshal([]byte(out), &found) != nil || len(found.Messages) < 2 {
		t.Fatalf("search %s %v", out, err)
	}
	ids := map[string]bool{}
	for _, m := range found.Messages {
		if ids[m.ID] || !strings.HasPrefix(m.ID, m.Thread+":") || m.Title == "" || m.Time == "" {
			t.Errorf("a found message %+v", m)
		}
		ids[m.ID] = true
	}
	if _, err := call("search", `{"query":" "}`); err == nil {
		t.Error("an empty query")
	}
	// The other tools answer as they do for the assistant.
	if out, err = call("chats", `{}`); err != nil || !strings.Contains(out, "c2 · 示例群") {
		t.Fatalf("chats %s %v", out, err)
	}
	if _, err := call("send", `{}`); err == nil {
		t.Error("an action ran from the catalog")
	}
	if text, err := c.Call(ctx, "telegram_messages", json.RawMessage(`{"chat":"示例群"}`)); err != nil || !strings.Contains(text, "2026-01-02 11:04 示例用户：群里的示例消息") {
		t.Fatalf("the assistant's text %q %v", text, err)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
