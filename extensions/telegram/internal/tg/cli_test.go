package telegram

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
)

// The models see the assistant's three read tools only; the CLI's reads and writes are there to be called by name.
func TestCLIToolsAreUnlisted(t *testing.T) {
	c, _, ctx := archiveAccount(t)
	r := connector.NewRegistry(nil)
	if err := r.Add(c); err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, tl := range r.Tools(ctx) {
		listed = append(listed, tl.Name)
	}
	if !slices.Equal(listed, []string{"telegram_chats", "telegram_messages", "telegram_search"}) {
		t.Fatalf("the models see %v", listed)
	}
	for _, n := range []string{"telegram_list_chats", "telegram_send", "telegram_mark_read", "telegram_cli"} {
		if !r.Offers(n) {
			t.Errorf("%s cannot be called by name", n)
		}
	}
}

// The CLI's reads answer JSON: conversations with their ids, a page of messages and where the next one starts, a
// time range, one message, a profile, members, search; a name it does not know or an unknown argument is refused.
func TestCLIReads(t *testing.T) {
	c, f, ctx := archiveAccount(t)
	c.zone = time.FixedZone("UTC+8", 8*3600)
	f.mu.Lock()
	f.dialogs[0].Unread, f.dialogs[0].Mentions, f.dialogs[0].Username = 3, 1, "example_group"
	f.infos = map[string]chatInfo{"c2": {About: "示例群的介绍", Members: 2}}
	f.members = map[string][]member{"c2": {{Key: "u1", Name: "示例用户"}, {Key: "u7", Name: "另一个示例用户", Username: "example7"}}}
	f.mu.Unlock()
	call := func(name, args string, out any) {
		t.Helper()
		raw, err := c.Call(ctx, name, json.RawMessage(args))
		if err != nil {
			t.Fatalf("%s %s: %v", name, args, err)
		}
		reflect.ValueOf(out).Elem().SetZero() // what an answer leaves out is not the last one's
		if err := json.Unmarshal([]byte(raw), out); err != nil {
			t.Fatalf("%s: not JSON: %s", name, raw)
		}
	}

	var chats struct {
		Chats []chatOut `json:"chats"`
		More  bool      `json:"more"`
	}
	call("telegram_list_chats", `{}`, &chats)
	if len(chats.Chats) != 2 || chats.Chats[0].ID != "c2" || chats.Chats[0].Unread != 3 || chats.Chats[0].Mentions != 1 ||
		chats.Chats[0].Username != "example_group" || chats.Chats[1].ID != "u1" || chats.Chats[1].Kind != "dm" || chats.Chats[0].Last == nil {
		t.Fatalf("chats %+v (a bot, Saved Messages or a channel got in?)", chats)
	}
	call("telegram_list_chats", `{"unread_only":true,"limit":1}`, &chats)
	if len(chats.Chats) != 1 || chats.Chats[0].ID != "c2" || chats.More {
		t.Fatalf("unread only %+v", chats)
	}

	var page struct {
		Chat       chatOut      `json:"chat"`
		Messages   []messageOut `json:"messages"`
		NextBefore int          `json:"next_before"`
	}
	call("telegram_read_messages", `{"chat":"u1","limit":2}`, &page)
	if page.Chat.Name != "示例用户" || len(page.Messages) != 2 || page.Messages[0].Text != "示例消息一" || !page.Messages[1].Mine ||
		page.Messages[1].From != "" || page.NextBefore != 2 || !strings.HasSuffix(page.Messages[0].At, "+08:00") {
		t.Fatalf("a page %+v", page)
	}
	call("telegram_read_messages", `{"chat":"u1","before":2}`, &page)
	if len(page.Messages) != 1 || page.Messages[0].ID != 1 || page.NextBefore != 0 {
		t.Fatalf("the next page %+v", page)
	}
	since := time.Now().Add(-3 * time.Hour).Format(time.RFC3339)
	call("telegram_read_messages", `{"chat":"u1","limit":3,"since":"`+since+`"}`, &page)
	if len(page.Messages) != 2 || page.NextBefore != 0 {
		t.Fatalf("since: %+v (nothing older; no next page past since)", page)
	}
	until := time.Now().Add(-90 * time.Minute).Format(time.RFC3339)
	call("telegram_read_messages", `{"chat":"u1","until":"`+until+`"}`, &page)
	if len(page.Messages) != 2 || page.Messages[1].ID != 2 {
		t.Fatalf("until: %+v", page)
	}

	var one struct {
		Chat    chatOut    `json:"chat"`
		Message messageOut `json:"message"`
	}
	call("telegram_get_message", `{"chat":"c2","id":12}`, &one)
	if one.Message.ReplyTo != 11 || one.Message.FromID != "u7" || one.Message.Type != "image" || one.Chat.ID != "c2" {
		t.Fatalf("one message %+v", one)
	}
	var profile struct {
		Chat chatOut `json:"chat"`
	}
	call("telegram_get_chat", `{"chat":"示例群"}`, &profile)
	if profile.Chat.About != "示例群的介绍" || profile.Chat.Members != 2 || profile.Chat.Unread != 3 {
		t.Fatalf("profile %+v", profile)
	}
	var members struct {
		Members []memberOut `json:"members"`
		Count   int         `json:"count"`
	}
	call("telegram_list_members", `{"chat":"c2","limit":1}`, &members)
	if len(members.Members) != 1 || members.Count != 2 || members.Members[0].ID != "u1" {
		t.Fatalf("members %+v", members)
	}
	var found struct {
		Results []foundOut `json:"results"`
	}
	call("telegram_search_messages", `{"query":"示例消息"}`, &found)
	if len(found.Results) != 3 || found.Results[0].Chat.ID != "c2" {
		t.Fatalf("search %+v", found)
	}
	call("telegram_search_messages", `{"query":"示例消息","since":"`+since+`"}`, &found)
	if len(found.Results) != 2 {
		t.Fatalf("search since %+v", found)
	}

	for _, bad := range []struct{ name, args, says string }{
		{"telegram_read_messages", `{"chat":"没有这个会话"}`, "没有叫"},
		{"telegram_read_messages", `{"chat":"u1","since":"yesterday"}`, "RFC 3339"},
		{"telegram_read_messages", `{"chat":"u1","until":"2026-13-01"}`, "nor a date"},
		{"telegram_list_chats", `{"limitt":3}`, "unknown field"},
		{"telegram_get_message", `{"chat":"u1","id":99}`, "没有消息 99"},
		{"telegram_search_messages", `{}`, "query is required"},
	} {
		if _, err := c.Call(ctx, bad.name, json.RawMessage(bad.args)); err == nil || !strings.Contains(err.Error(), bad.says) {
			t.Errorf("%s %s: %v, want %q", bad.name, bad.args, err, bad.says)
		}
	}
}

// The writes go through the hub's gate: the card names the conversation (only one the reads listed, by id or
// @username, never by a name two chats could share) and has the full text; declined, nothing reaches Telegram;
// allowed, exactly that does, with a random_id.
func TestCLIWrites(t *testing.T) {
	c, f, ctx := archiveAccount(t)
	f.mu.Lock()
	f.dialogs[0].Username = "example_group"
	f.mu.Unlock()
	r := connector.NewRegistry([]connector.Rule{{Connector: "telegram", Action: "telegram_react", Decision: connector.Allow}})
	if err := r.Add(c); err != nil {
		t.Fatal(err)
	}
	var card string
	no := func(_ context.Context, summary string, _ json.RawMessage) bool { card = summary; return false }
	yes := func(_ context.Context, summary string, _ json.RawMessage) bool { card = summary; return true }
	send := json.RawMessage(`{"chat":"u1","text":"示例回复：好的"}`)

	// Not listed yet: the inbox has not read the list, nor has the CLI.
	if res := r.Call(ctx, "telegram_send", send, yes); !res.IsError || !strings.Contains(res.Content[0].Text, "先用 tg chats") || card != "" {
		t.Fatalf("an unknown conversation: %+v, card %q", res, card)
	}
	if _, err := c.Call(ctx, "telegram_list_chats", nil); err != nil {
		t.Fatal(err)
	}
	if res := r.Call(ctx, "telegram_send", json.RawMessage(`{"chat":"示例用户","text":"示例"}`), yes); !res.IsError || !strings.Contains(res.Content[0].Text, "不认名字") {
		t.Fatalf("a write by name: %+v", res)
	}
	before := len(f.requests())
	if res := r.Call(ctx, "telegram_send", send, no); !res.IsError || len(f.written()) != 0 || len(f.requests()) != before {
		t.Fatalf("declined, yet it sent: %+v %v", res, f.written())
	}
	if !strings.Contains(card, "发给 示例用户（私聊 · u1）") || !strings.Contains(card, "示例回复：好的") || !strings.Contains(card, "谁会看到：示例用户") {
		t.Fatalf("the card: %q", card)
	}
	res := r.Call(ctx, "telegram_send", send, yes)
	if res.IsError || !strings.Contains(res.Content[0].Text, `"chat":"u1"`) {
		t.Fatalf("send: %+v", res)
	}
	if res := r.Call(ctx, "telegram_send", json.RawMessage(`{"chat":"@example_group","text":"示例","reply_to":11}`), yes); res.IsError ||
		!strings.Contains(card, "回复 示例群（群 · @example_group · c2）里的消息 11") || !strings.Contains(card, "谁会看到：示例群的所有成员") {
		t.Fatalf("reply: %+v, card %q", res, card)
	}
	if res := r.Call(ctx, "telegram_forward", json.RawMessage(`{"chat":"c2","ids":[11,12],"to":"u1"}`), yes); res.IsError ||
		!strings.Contains(card, "把 示例群（群 · @example_group · c2）里的 2 条消息（11、12）转发到 示例用户（私聊 · u1）") {
		t.Fatalf("forward: %+v, card %q", res, card)
	}
	card = ""
	if res := r.Call(ctx, "telegram_react", json.RawMessage(`{"chat":"c2","id":12,"emoji":"👍"}`), no); res.IsError || card != "" {
		t.Fatalf("a reaction the rules allow was asked about or refused: %+v, card %q", res, card)
	}
	if res := r.Call(ctx, "telegram_mark_read", json.RawMessage(`{"chat":"u1"}`), yes); res.IsError || !strings.Contains(card, "把 示例用户（私聊 · u1）标为已读（全部）") {
		t.Fatalf("mark read: %+v, card %q", res, card)
	}
	want := []string{"send u1 reply 0: 示例回复：好的", "send c2 reply 11: 示例", "forward c2 [11 12] to u1", "react c2 12 👍", "read u1 up to 0"}
	if got := f.written(); !slices.Equal(got, want) {
		t.Fatalf("Telegram got\n%q\nwant\n%q", got, want)
	}

	for _, bad := range []struct{ name, args, says string }{
		{"telegram_send", `{"chat":"u1"}`, "text is required"},
		{"telegram_send", `{"chat":"u1","text":"` + strings.Repeat("字", textMost+1) + `"}`, "at most 4096"},
		{"telegram_forward", `{"chat":"c2","ids":[],"to":"u1"}`, "ids"},
		{"telegram_forward", `{"chat":"c2","ids":[11],"to":"u9"}`, "to: 不认识会话"},
		{"telegram_react", `{"chat":"c2","id":12}`, "emoji"},
		{"telegram_send", `{"chat":"u1","text":"示例","silent":true}`, "unknown field"},
	} {
		card = ""
		if res := r.Call(ctx, bad.name, json.RawMessage(bad.args), yes); !res.IsError || !strings.Contains(res.Content[0].Text, bad.says) || card != "" {
			t.Errorf("%s %s: %+v, card %q, want %q", bad.name, bad.args, res, card, bad.says)
		}
	}
	if got := f.written(); len(got) != len(want) {
		t.Fatalf("a refused write reached Telegram: %q", got)
	}
}

// During a flood wait a write the user allowed sends nothing either, and says how long to wait.
func TestCLIWriteDuringAFloodWait(t *testing.T) {
	c, f, ctx := archiveAccount(t)
	if _, err := c.Call(ctx, "telegram_list_chats", nil); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.fail = func(method string) error {
		if method == "Dialogs" {
			return tgerr.New(420, "FLOOD_WAIT_30")
		}
		return nil
	}
	f.mu.Unlock()
	if _, err := c.Call(ctx, "telegram_list_chats", nil); err == nil {
		t.Fatal("no flood wait")
	}
	sent := len(f.requests())
	out, err := c.Do(ctx, "telegram_send", json.RawMessage(`{"chat":"u1","text":"示例"}`))
	if err == nil || !strings.Contains(err.Error(), "Telegram 限流中") || len(f.requests()) != sent || len(f.written()) != 0 {
		t.Fatalf("sent during a flood wait: %q %v, %d request(s)", out, err, len(f.requests())-sent)
	}
	if _, err := c.Call(ctx, "telegram_read_messages", json.RawMessage(`{"chat":"u1"}`)); err == nil || !strings.Contains(err.Error(), "Telegram 限流中") {
		t.Fatalf("a read during a flood wait: %v", err)
	}
}
