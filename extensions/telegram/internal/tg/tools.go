package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/prompts"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/proto"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/tg/spec"
)

// The assistant's read tools (prompts/mcp/telegram): they only read, so they run without asking. They read
// Telegram live (the knowledge base has what was archived; these have what is there now) and mark nothing as read.

var readTools = []string{"telegram_chats", "telegram_messages", "telegram_search"}

// Tools are the assistant's read tools, then the tg CLI's (unlisted, cli.go) and the one that plans its command lines
// (cmdline.go).
func (c *Conn) Tools() []proto.Tool {
	out := make([]proto.Tool, 0, len(readTools))
	for _, n := range readTools {
		t := prompts.Tool(n)
		out = append(out, proto.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	t := prompts.Tool(spec.CLITool)
	return append(append(out, cliTools()...), proto.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
}

// Limits of an answer, so one call does not fill the model's context nor ask Telegram much: one page each, and at
// most three requests a call (the list, when the conversation is not in the last one, and the read).
const (
	lineCap      = 600 // one message
	chatsDef     = 30
	chatsMost    = 100
	messagesDef  = 30
	messagesMost = 50
	searchDef    = 20
	searchMost   = 30
)

func (c *Conn) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	if name == spec.CLITool {
		return cmdLine(args)
	}
	cl := c.client()
	if cl == nil {
		return "", errNotConnected
	}
	if left := c.gate.left(); left > 0 { // a flood wait is not over: no request at all
		return "", heldError{left}
	}
	if !slices.Contains(readTools, name) {
		out, err := c.cliCall(ctx, cl, name, args)
		if err != nil {
			return "", c.writeError(err)
		}
		return out, nil
	}
	var a struct {
		Limit      int    `json:"limit"`
		UnreadOnly bool   `json:"unread_only"`
		Chat       string `json:"chat"`
		Before     int    `json:"before"`
		Query      string `json:"query"`
	}
	if len(args) > 0 {
		// The tools' schemas say additionalProperties: false, so an argument the schema does not name is an
		// error, not a silently ignored one (as the unlisted CLI's parseArgs already does).
		dec := json.NewDecoder(bytes.NewReader(args))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&a); err != nil {
			return "", fmt.Errorf("arguments: %w", err)
		}
	}
	var out string
	var err error
	switch name {
	case "telegram_chats":
		out, err = c.toolChats(ctx, cl, bound(a.Limit, chatsDef, chatsMost), a.UnreadOnly)
	case "telegram_messages":
		out, err = c.toolMessages(ctx, cl, a.Chat, bound(a.Limit, messagesDef, messagesMost), a.Before)
	case "telegram_search":
		out, err = c.toolSearch(ctx, cl, a.Query, a.Chat, bound(a.Limit, searchDef, searchMost))
	default:
		return "", fmt.Errorf("unknown tool %s", name)
	}
	if err != nil {
		if why, ok := userError(err); ok {
			return "", fmt.Errorf("%s", why)
		}
		return "", scrubbed(err)
	}
	return out, nil
}

func bound(n, def, most int) int {
	if n <= 0 {
		return def
	}
	return min(n, most)
}

func (c *Conn) toolChats(ctx context.Context, cl client, limit int, unreadOnly bool) (string, error) {
	ds, err := cl.Dialogs(ctx, dialogsLimit)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	n := 0
	for _, d := range ds {
		if !d.kept() || unreadOnly && d.Unread == 0 {
			continue
		}
		if n == limit {
			break
		}
		n++
		kind := "私聊"
		if d.Kind == "group" {
			kind = "群"
		}
		fmt.Fprintf(&b, "%s · %s · %s", d.Peer.key(), d.Name, kind)
		if d.Unread > 0 {
			fmt.Fprintf(&b, " · 未读 %d", d.Unread)
			if d.Mentions > 0 {
				fmt.Fprintf(&b, "（@你 %d）", d.Mentions)
			}
		}
		if d.Muted {
			b.WriteString(" · 静音")
		}
		if d.Archived {
			b.WriteString(" · 已归档")
		}
		if !d.TopAt.IsZero() {
			from := d.TopFrom
			if d.TopOut {
				from = "我"
			}
			fmt.Fprintf(&b, "\n  最后 %s %s：%s", c.when(d.TopAt), from, clip(oneLine(d.TopText), 120))
		}
		b.WriteString("\n")
	}
	if n == 0 {
		return "没有符合的会话。", nil
	}
	return b.String(), nil
}

func (c *Conn) toolMessages(ctx context.Context, cl client, chat string, limit, before int) (string, error) {
	d, err := c.find(ctx, cl, chat)
	if err != nil {
		return "", err
	}
	msgs, err := cl.Older(ctx, d, before, time.Time{}, limit)
	if err != nil {
		return "", err
	}
	if len(msgs) == 0 {
		return d.Name + "：没有更早的消息了。", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s（%s）\n", d.Name, d.Peer.key())
	for _, m := range sourceMessages(msgs) {
		from := m.SenderName
		if m.Mine {
			from = "我"
		}
		fmt.Fprintf(&b, "%s %s：%s\n", c.when(m.At), from, clip(oneLine(m.Text), lineCap))
	}
	if len(msgs) == limit {
		fmt.Fprintf(&b, "（更早的：before=%d）\n", msgs[0].ID)
	}
	return b.String(), nil
}

func (c *Conn) toolSearch(ctx context.Context, cl client, query, chat string, limit int) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query is required")
	}
	var in *dialog
	if chat != "" {
		d, err := c.find(ctx, cl, chat)
		if err != nil {
			return "", err
		}
		in = &d
	}
	found, err := cl.Search(ctx, query, in, time.Time{}, time.Time{}, limit)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	n := 0
	for _, f := range found {
		if !f.Dialog.kept() {
			continue // a broadcast channel's post, a bot
		}
		n++
		from := f.Message.From
		if f.Message.Out {
			from = "我"
		}
		fmt.Fprintf(&b, "%s · %s · %s %s：%s\n", f.Dialog.Peer.key(), f.Dialog.Name, c.when(f.Message.At), from, clip(oneLine(f.Message.Text), lineCap))
	}
	if n == 0 {
		return "没有找到。", nil
	}
	return b.String(), nil
}

// find is the conversation a tool names: its key (u…, g…, c…), or its exact name. It looks in the list as last read
// (the archive's, the inbox's) first, and asks Telegram for it only when the conversation is not there.
func (c *Conn) find(ctx context.Context, cl client, chat string) (dialog, error) {
	chat = strings.TrimSpace(chat)
	if chat == "" {
		return dialog{}, fmt.Errorf("chat is required")
	}
	c.box.mu.Lock()
	last := c.box.dialogs
	c.box.mu.Unlock()
	if d, err := pick(last, chat); err == nil {
		return d, nil
	}
	ds, err := cl.Dialogs(ctx, dialogsLimit)
	if err != nil {
		return dialog{}, err
	}
	return pick(ds, chat)
}

// pick is the conversation in ds a tool names.
func pick(ds []dialog, chat string) (dialog, error) {
	var named []dialog
	for _, d := range ds {
		if !d.kept() {
			continue
		}
		if d.Peer.key() == chat {
			return d, nil
		}
		if d.Name == chat {
			named = append(named, d)
		}
	}
	switch len(named) {
	case 1:
		return named[0], nil
	case 0:
		return dialog{}, fmt.Errorf("没有叫 %q 的会话（用 telegram_chats 看会话的 key）", chat)
	}
	return dialog{}, fmt.Errorf("有 %d 个会话叫 %q：用 telegram_chats 里的 key", len(named), chat)
}

// when is a time as the tools show it: in the user's zone, or in UTC saying so while the hub does not know the user's.
func (c *Conn) when(t time.Time) string {
	if c.zone == nil {
		return t.UTC().Format("2006-01-02 15:04 UTC")
	}
	return t.In(c.zone).Format("2006-01-02 15:04")
}

// clock is a time of day as the status says it, in the user's zone (UTC, saying so, while the hub does not know it).
func (c *Conn) clock(t time.Time) string {
	if c.zone == nil {
		return t.UTC().Format("15:04:05 UTC")
	}
	return t.In(c.zone).Format("15:04:05")
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
