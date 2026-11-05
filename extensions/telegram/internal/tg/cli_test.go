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
		{"telegram_send", `{"chat":"u1","text":"` + strings.Repeat("😀", textMost) + `"}`, "at most 4096"},
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

// The send limit is Telegram's, counted in UTF-16 code units: an astral rune (an emoji) is two. Exactly the limit
// passes the pre-flight check and, once allowed, sends; one unit over is refused before the card, whatever the runes.
func TestCLISendLengthUTF16(t *testing.T) {
	c, f, ctx := archiveAccount(t)
	r := connector.NewRegistry(nil)
	if err := r.Add(c); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(ctx, "telegram_list_chats", nil); err != nil {
		t.Fatal(err)
	}
	var card string
	yes := func(_ context.Context, summary string, _ json.RawMessage) bool { card = summary; return true }

	before := len(f.written())
	atLimit := json.RawMessage(`{"chat":"u1","text":"` + strings.Repeat("😀", textMost/2) + `"}`) // 2048 runes, 4096 units
	if res := r.Call(ctx, "telegram_send", atLimit, yes); res.IsError || !strings.Contains(card, strings.Repeat("😀", 4)) ||
		len(f.written()) != before+1 {
		t.Fatalf("exactly 4096 UTF-16 units was refused: %+v, card %q", res, card)
	}

	for i, text := range []string{
		strings.Repeat("😀", textMost),         // 4096 runes, 8192 units
		strings.Repeat("😀", textMost/2) + "x", // 2049 runes, 4097 units
	} {
		card = ""
		sent := len(f.written())
		args := json.RawMessage(`{"chat":"u1","text":"` + text + `"}`)
		if res := r.Call(ctx, "telegram_send", args, yes); !res.IsError ||
			!strings.Contains(res.Content[0].Text, "at most 4096") || card != "" || len(f.written()) != sent {
			t.Fatalf("case %d: over the UTF-16 limit reached the card or Telegram: %+v, card %q", i, res, card)
		}
	}
}

// A write names its conversation exactly as connector_gate sees it: rules that deny a chat by its id and its
// @username (the two names a write takes) are not stepped around by spacing or by the @username's case.
func TestCLIWriteNamesTheChatTheGateSees(t *testing.T) {
	c, f, ctx := archiveAccount(t)
	f.mu.Lock()
	f.dialogs[0].Username = "example_group"
	f.mu.Unlock()
	r := connector.NewRegistry([]connector.Rule{
		{Connector: "telegram", Action: "telegram_react", Match: map[string]any{"chat": "c2"}, Decision: connector.Deny},
		{Connector: "telegram", Action: "telegram_react", Match: map[string]any{"chat": "@example_group"}, Decision: connector.Deny},
		{Connector: "telegram", Action: "telegram_forward", Match: map[string]any{"to": "c2"}, Decision: connector.Deny},
		{Connector: "telegram", Action: "telegram_forward", Match: map[string]any{"to": "@example_group"}, Decision: connector.Deny},
		{Connector: "telegram", Action: "telegram_react", Decision: connector.Allow},
		{Connector: "telegram", Action: "telegram_forward", Decision: connector.Allow},
	})
	if err := r.Add(c); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(ctx, "telegram_list_chats", nil); err != nil {
		t.Fatal(err)
	}
	yes := func(context.Context, string, json.RawMessage) bool { return true }
	for _, chat := range []string{"c2", "@example_group", " c2", "c2\n", "@Example_Group", " @example_group"} {
		args, _ := json.Marshal(map[string]any{"chat": chat, "id": 12, "emoji": "👍"})
		if res := r.Call(ctx, "telegram_react", args, yes); !res.IsError {
			t.Errorf("react in %q went through a rule that denies c2: %+v", chat, res)
		}
		args, _ = json.Marshal(map[string]any{"chat": "u1", "ids": []int{11}, "to": chat})
		if res := r.Call(ctx, "telegram_forward", args, yes); !res.IsError {
			t.Errorf("forward to %q went through a rule that denies c2: %+v", chat, res)
		}
	}
	// The gate looks a field up by its exact name; encoding/json would fill it from "Chat" or "TO" too.
	for _, args := range []string{
		`{"Chat":"c2","id":12,"emoji":"👍"}`,
		`{"chat":"u1","CHAT":"c2","id":12,"emoji":"👍"}`,
		`{"chat":"u1","ids":[11],"To":"c2"}`,
	} {
		name := "telegram_react"
		if strings.Contains(args, "ids") {
			name = "telegram_forward"
		}
		if res := r.Call(ctx, name, json.RawMessage(args), yes); !res.IsError {
			t.Errorf("%s %s went through a rule that denies c2: %+v", name, args, res)
		}
	}
	if got := f.written(); len(got) != 0 {
		t.Fatalf("a denied write reached Telegram: %q", got)
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

// A watcher that runs for a long time searches again and again, each search listing peers the process had not seen:
// the seen map is capped like the sibling caches (thread.go) instead of growing for the process's lifetime. The
// conversations a read just listed still resolve as a write's target, by id and by @username; an evicted one is
// asked to be found again. Re-listing a conversation updates it in place, so the recent de-dup is intact.
func TestCLISeenMapIsCapped(t *testing.T) {
	c := &Conn{}
	const batch = searchMost // a search page is the read that keeps introducing fresh peers
	id := int64(1)
	rememberPage := func() dialog {
		ds := make([]dialog, batch)
		for i := range ds {
			ds[i] = dialog{Peer: peer{Kind: 'u', ID: id}, Name: "示例用户", Kind: "dm"}
			id++
		}
		c.remember(ds)
		return ds[batch-1]
	}
	var fresh dialog
	for range locsMost/batch + 2 { // more than locsMost distinct peers across the run
		fresh = rememberPage()
	}
	if got := len(c.box.seen); got > locsMost+batch { // past the cap the map resets, leaving at most the page in hand
		t.Fatalf("the seen map grew to %d entries, want it capped at about %d", got, locsMost)
	}
	if _, err := c.target(fresh.Peer.key()); err != nil {
		t.Fatalf("a conversation a read just listed no longer resolves: %v", err)
	}
	fresh.Username = "fresh_one"
	c.remember([]dialog{fresh})
	if _, err := c.target("@fresh_one"); err != nil {
		t.Fatalf("the freshly listed @username no longer resolves: %v", err)
	}
	if _, err := c.target("u1"); err == nil { // evicted many pages ago
		t.Fatal("an evicted conversation still resolved")
	}
	filled := len(c.box.seen)
	c.remember([]dialog{{Peer: fresh.Peer, Name: "示例用户改名", Username: "fresh_one", Kind: "dm"}})
	if len(c.box.seen) != filled {
		t.Fatalf("re-listing a known conversation grew the map from %d to %d", filled, len(c.box.seen))
	}
	if d, err := c.target(fresh.Peer.key()); err != nil || d.Name != "示例用户改名" {
		t.Fatalf("re-listing did not update the conversation in place: %+v %v", d, err)
	}
	// Bots, Saved Messages and broadcast channels are never a write's target, even straight from a read.
	c.remember([]dialog{
		{Peer: peer{Kind: 'u', ID: 901}, Name: "机器人", Kind: "dm", Bot: true},
		{Peer: peer{Kind: 'u', ID: 902}, Name: "我", Kind: "dm", Self: true},
		{Peer: peer{Kind: 'c', ID: 903}, Name: "频道", Kind: "group", Broadcast: true},
	})
	for _, chat := range []string{"u901", "u902", "c903"} {
		if _, err := c.target(chat); err == nil {
			t.Errorf("%s was kept though its kind is never written to", chat)
		}
	}
}
