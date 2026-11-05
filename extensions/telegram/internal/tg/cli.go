package telegram

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/prompts"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/proto"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/tg/spec"
)

// What the tg CLI calls (prompts/mcp/telegram, unlisted: not in the models' tool lists; the telegram skill tells the
// assistant how to use it, assistantskills/builtin/telegram). The reads answer JSON for a program to read, one page a
// call and the same limits as the assistant's tools; the writes are actions, so the hub's gate (connector_gate, the
// phone's card) lets each through first. Every request goes through the connector's gate (gate.go) like any other.

// Limits of the CLI's reads and writes.
const (
	textMost    = 4096 // a message's text out (Telegram's own limit for one)
	membersDef  = 50
	membersMost = 100
	forwardMost = 20 // messages one forward takes
)

// cliArgs are the arguments of the CLI's reads and writes (unknown fields are refused, as the schemas say).
type cliArgs struct {
	Chat       string `json:"chat"`
	Limit      int    `json:"limit"`
	UnreadOnly bool   `json:"unread_only"`
	Before     int    `json:"before"`
	Since      string `json:"since"`
	Until      string `json:"until"`
	Query      string `json:"query"`
	ID         int    `json:"id"`
	Text       string `json:"text"`
	ReplyTo    int    `json:"reply_to"`
	To         string `json:"to"`
	IDs        []int  `json:"ids"`
	Emoji      string `json:"emoji"`
	UpTo       int    `json:"up_to"`
}

// argNames are cliArgs' fields as the arguments name them.
var argNames = func() map[string]bool {
	t := reflect.TypeFor[cliArgs]()
	out := make(map[string]bool, t.NumField())
	for i := range t.NumField() {
		out[t.Field(i).Tag.Get("json")] = true
	}
	return out
}()

func parseArgs(args json.RawMessage) (cliArgs, error) {
	var a cliArgs
	if len(args) == 0 {
		return a, nil
	}
	// encoding/json fills a field from a key in any case ("Chat", "CHAT"), but connector_gate's rules look a
	// field up by its exact name, so a key that is not one of cliArgs' names as written is refused.
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(args, &keys); err != nil {
		return a, fmt.Errorf("arguments: %w", err)
	}
	for k := range keys {
		if !argNames[k] {
			return a, fmt.Errorf("arguments: unknown field %q", k)
		}
	}
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, fmt.Errorf("arguments: %w", err)
	}
	return a, nil
}

// cliTools are the CLI's reads.
func cliTools() []proto.Tool {
	out := make([]proto.Tool, 0, len(spec.CLITools))
	for _, n := range spec.CLITools {
		t := prompts.Tool(n)
		out = append(out, proto.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	return out
}

// Actions are the CLI's writes.
func (c *Conn) Actions() []proto.Tool {
	out := make([]proto.Tool, 0, len(spec.CLIActions))
	for _, n := range spec.CLIActions {
		t := prompts.Tool(n)
		out = append(out, proto.Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	return out
}

// The JSON the CLI reads (snake_case, times in RFC 3339 in the user's zone, UTC while the hub does not know it).
type (
	chatOut struct {
		ID       string      `json:"id"`
		Name     string      `json:"name"`
		Kind     string      `json:"kind"` // dm | group
		Username string      `json:"username,omitempty"`
		Unread   int         `json:"unread,omitempty"`
		Mentions int         `json:"mentions,omitempty"`
		Muted    bool        `json:"muted,omitempty"`
		Archived bool        `json:"archived,omitempty"`
		Last     *messageOut `json:"last,omitempty"`
		About    string      `json:"about,omitempty"`
		Members  int         `json:"members,omitempty"`
	}
	messageOut struct {
		ID        int    `json:"id,omitempty"`
		At        string `json:"at"`
		From      string `json:"from,omitempty"`
		FromID    string `json:"from_id,omitempty"`
		Mine      bool   `json:"mine,omitempty"`
		Type      string `json:"type,omitempty"`
		Text      string `json:"text"`
		ReplyTo   int    `json:"reply_to,omitempty"`
		Mentioned bool   `json:"mentioned,omitempty"`
	}
	memberOut struct {
		ID       string `json:"id"`
		Name     string `json:"name,omitempty"`
		Username string `json:"username,omitempty"`
		Bot      bool   `json:"bot,omitempty"`
	}
	foundOut struct {
		Chat    chatOut    `json:"chat"`
		Message messageOut `json:"message"`
	}
)

func (c *Conn) stamp(t time.Time) string {
	if c.zone == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return t.In(c.zone).Format(time.RFC3339)
}

// ref is a conversation in a few words: what a message's or a member's answer says it is in.
func ref(d dialog) chatOut {
	return chatOut{ID: d.Peer.key(), Name: d.Name, Kind: d.Kind, Username: d.Username}
}

func (c *Conn) chatOf(d dialog) chatOut {
	out := ref(d)
	out.Unread, out.Mentions, out.Muted, out.Archived = d.Unread, d.Mentions, d.Muted, d.Archived
	if !d.TopAt.IsZero() {
		from := d.TopFrom
		if d.TopOut {
			from = ""
		}
		out.Last = &messageOut{ID: d.Top, At: c.stamp(d.TopAt), From: from, Mine: d.TopOut, Text: clip(d.TopText, textMost)}
	}
	return out
}

func (c *Conn) messageOf(m message) messageOut {
	out := messageOut{ID: m.ID, At: c.stamp(m.At), From: m.From, FromID: m.FromKey, Mine: m.Out, Type: strings.ToLower(m.Type),
		Text: clip(m.Text, textMost), ReplyTo: m.ReplyTo, Mentioned: m.Mentioned}
	if m.Out {
		out.From, out.FromID = "", ""
	}
	return out
}

func jsonOf(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// parseWhen parses a CLI time: RFC 3339 (the CLI turns Unix seconds and "2h ago" into it) or a date, midnight in the
// user's zone (UTC while the hub does not know it); "" is no bound.
func (c *Conn) parseWhen(s, what string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	zone := c.zone
	if zone == nil {
		zone = time.UTC
	}
	if t, err := time.ParseInLocation(time.DateOnly, s, zone); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%s: %q is neither an RFC 3339 time nor a date (2006-01-02)", what, s)
}

// cliCall runs one of the CLI's reads.
func (c *Conn) cliCall(ctx context.Context, cl client, name string, args json.RawMessage) (string, error) {
	a, err := parseArgs(args)
	if err != nil {
		return "", err
	}
	since, err := c.parseWhen(a.Since, "since")
	if err != nil {
		return "", err
	}
	until, err := c.parseWhen(a.Until, "until")
	if err != nil {
		return "", err
	}
	switch name {
	case "telegram_list_chats":
		ds, err := cl.Dialogs(ctx, dialogsLimit)
		if err != nil {
			return "", err
		}
		c.remember(ds)
		limit := bound(a.Limit, chatsDef, chatsMost)
		out := struct {
			Chats []chatOut `json:"chats"`
			More  bool      `json:"more,omitempty"`
		}{Chats: []chatOut{}}
		for _, d := range ds {
			if !d.kept() || a.UnreadOnly && d.Unread == 0 {
				continue
			}
			if len(out.Chats) == limit {
				out.More = true
				break
			}
			out.Chats = append(out.Chats, c.chatOf(d))
		}
		return jsonOf(out)
	case "telegram_read_messages":
		d, err := c.find(ctx, cl, a.Chat)
		if err != nil {
			return "", err
		}
		limit := bound(a.Limit, messagesDef, messagesMost)
		msgs, err := cl.Older(ctx, d, a.Before, until, limit)
		if err != nil {
			return "", err
		}
		full := len(msgs) == limit
		msgs = slices.DeleteFunc(msgs, func(m message) bool { return !since.IsZero() && m.At.Before(since) })
		out := struct {
			Chat       chatOut      `json:"chat"`
			Messages   []messageOut `json:"messages"`
			NextBefore int          `json:"next_before,omitempty"` // the next page's before; none at the start or at since
		}{Chat: ref(d), Messages: []messageOut{}}
		for _, m := range msgs {
			out.Messages = append(out.Messages, c.messageOf(m))
		}
		if full && len(msgs) == limit {
			out.NextBefore = msgs[0].ID
		}
		return jsonOf(out)
	case "telegram_search_messages":
		if strings.TrimSpace(a.Query) == "" {
			return "", errors.New("query is required")
		}
		var in *dialog
		if a.Chat != "" {
			d, err := c.find(ctx, cl, a.Chat)
			if err != nil {
				return "", err
			}
			in = &d
		}
		found, err := cl.Search(ctx, a.Query, in, since, until, bound(a.Limit, searchDef, searchMost))
		if err != nil {
			return "", err
		}
		out := struct {
			Results []foundOut `json:"results"`
		}{Results: []foundOut{}}
		var seen []dialog
		for _, f := range found {
			if !f.Dialog.kept() {
				continue // a broadcast channel's post, a bot
			}
			seen = append(seen, f.Dialog)
			out.Results = append(out.Results, foundOut{Chat: ref(f.Dialog), Message: c.messageOf(f.Message)})
		}
		c.remember(seen)
		return jsonOf(out)
	case "telegram_get_message":
		if a.ID <= 0 {
			return "", errors.New("id is required")
		}
		d, err := c.find(ctx, cl, a.Chat)
		if err != nil {
			return "", err
		}
		msgs, err := cl.Messages(ctx, d, []int{a.ID})
		if err != nil {
			return "", err
		}
		if len(msgs) == 0 {
			return "", fmt.Errorf("%s 里没有消息 %d", d.Name, a.ID)
		}
		return jsonOf(struct {
			Chat    chatOut    `json:"chat"`
			Message messageOut `json:"message"`
		}{ref(d), c.messageOf(msgs[0])})
	case "telegram_get_chat":
		d, err := c.find(ctx, cl, a.Chat)
		if err != nil {
			return "", err
		}
		info, err := cl.Info(ctx, d)
		if err != nil {
			return "", err
		}
		out := c.chatOf(d)
		out.About, out.Members = clip(info.About, textMost), info.Members
		return jsonOf(struct {
			Chat chatOut `json:"chat"`
		}{out})
	case "telegram_list_members":
		d, err := c.find(ctx, cl, a.Chat)
		if err != nil {
			return "", err
		}
		ms, count, err := cl.Members(ctx, d, bound(a.Limit, membersDef, membersMost))
		if err != nil {
			return "", err
		}
		out := struct {
			Chat    chatOut     `json:"chat"`
			Members []memberOut `json:"members"`
			Count   int         `json:"count"`
		}{Chat: ref(d), Members: []memberOut{}, Count: count}
		for _, m := range ms {
			out.Members = append(out.Members, memberOut{ID: m.Key, Name: m.Name, Username: m.Username, Bot: m.Bot})
		}
		return jsonOf(out)
	}
	return "", fmt.Errorf("unknown tool %s", name)
}

// remember keeps conversations the CLI's reads listed, for its writes to name (target).
func (c *Conn) remember(ds []dialog) {
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	if c.box.seen == nil {
		c.box.seen = map[string]dialog{}
	}
	for _, d := range ds {
		if d.kept() {
			c.box.seen[d.Peer.key()] = d
		}
	}
}

var keyForm = regexp.MustCompile(`^[ugc][0-9]+$`)

// target is the conversation a write names: its id from the CLI's reads (u…, g…, c…) or its @username, found in
// what was last listed (the inbox's list, the CLI's reads). It asks Telegram nothing, so the card and the write go
// to the same conversation; a name is refused (two chats may share one). It takes the value only as written, with
// no spaces around it and the @username in the case Telegram keeps it, so connector_gate's rules, which match
// the written value, see the conversation the write goes to.
func (c *Conn) target(chat string) (dialog, error) {
	if strings.TrimSpace(chat) == "" {
		return dialog{}, errors.New("chat is required")
	}
	if chat != strings.TrimSpace(chat) {
		return dialog{}, fmt.Errorf("会话 %q 前后不能有空白", chat)
	}
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	user, byUser := strings.CutPrefix(chat, "@")
	if !byUser && !keyForm.MatchString(chat) {
		return dialog{}, fmt.Errorf("写操作要用会话的 id（tg chats 或 tg search 给的 u…、g…、c…）或 @用户名，不认名字：%q", chat)
	}
	for _, d := range slices.Concat(c.box.dialogs, slices.Collect(maps.Values(c.box.seen))) {
		if !d.kept() {
			continue
		}
		if byUser && d.Username != "" && d.Username == user || !byUser && d.Peer.key() == chat {
			return d, nil
		}
	}
	return dialog{}, fmt.Errorf("不认识会话 %q：先用 tg chats 或 tg search 找到它", chat)
}

// write is one of the CLI's writes as both the card and the write see it: the same arguments make the same one.
type write struct {
	name     string
	to, from dialog
	a        cliArgs
}

// plan checks a write's arguments and finds its conversations; Describe and Do both start from it.
func (c *Conn) plan(name string, args json.RawMessage) (write, error) {
	a, err := parseArgs(args)
	if err != nil {
		return write{}, err
	}
	w := write{name: name, a: a}
	switch name {
	case "telegram_send":
		if strings.TrimSpace(a.Text) == "" {
			return w, errors.New("text is required")
		}
		if n := utf8.RuneCountInString(a.Text); n > textMost {
			return w, fmt.Errorf("text has %d characters; Telegram takes at most %d in one message", n, textMost)
		}
		if a.ReplyTo < 0 {
			return w, errors.New("reply_to is a message id")
		}
	case "telegram_forward":
		if len(a.IDs) == 0 || len(a.IDs) > forwardMost {
			return w, fmt.Errorf("ids: 1 to %d message ids", forwardMost)
		}
		if w.from, err = c.target(a.Chat); err != nil {
			return w, err
		}
		if w.to, err = c.target(a.To); err != nil {
			return w, fmt.Errorf("to: %w", err)
		}
		return w, nil
	case "telegram_react":
		if a.ID <= 0 {
			return w, errors.New("id is required")
		}
		if a.Emoji == "" || utf8.RuneCountInString(a.Emoji) > 8 {
			return w, errors.New("emoji is one emoji (👍)")
		}
	case "telegram_mark_read":
		if a.UpTo < 0 {
			return w, errors.New("up_to is a message id")
		}
	default:
		return w, fmt.Errorf("unknown action %s", name)
	}
	w.to, err = c.target(a.Chat)
	return w, err
}

// called is how a conversation shows on the card: its name, what it is and its id.
func called(d dialog) string {
	kind := "私聊"
	if d.Kind == "group" {
		kind = "群"
	}
	if d.Username != "" {
		return fmt.Sprintf("%s（%s · @%s · %s）", d.Name, kind, d.Username, d.Peer.key())
	}
	return fmt.Sprintf("%s（%s · %s）", d.Name, kind, d.Peer.key())
}

// audience is who sees what is written to d.
func audience(d dialog) string {
	if d.Kind == "group" {
		return d.Name + "的所有成员"
	}
	return d.Name
}

// Describe is the card's words: which conversation, what exactly, who sees it. It asks Telegram nothing.
func (c *Conn) Describe(_ context.Context, name string, args json.RawMessage) (connector.Action, string, error) {
	w, err := c.plan(name, args)
	if err != nil {
		return connector.Action{}, "", err
	}
	act := connector.Action{Name: name, Visible: audience(w.to)}
	switch name {
	case "telegram_send":
		if w.a.ReplyTo != 0 {
			return act, fmt.Sprintf("用你的 Telegram 回复 %s里的消息 %d：\n\n%s", called(w.to), w.a.ReplyTo, w.a.Text), nil
		}
		return act, fmt.Sprintf("用你的 Telegram 发给 %s：\n\n%s", called(w.to), w.a.Text), nil
	case "telegram_forward":
		ids := make([]string, len(w.a.IDs))
		for i, id := range w.a.IDs {
			ids[i] = fmt.Sprint(id)
		}
		return act, fmt.Sprintf("把 %s里的 %d 条消息（%s）转发到 %s", called(w.from), len(ids), strings.Join(ids, "、"), called(w.to)), nil
	case "telegram_react":
		return act, fmt.Sprintf("在 %s的消息 %d 上点 %s", called(w.to), w.a.ID, w.a.Emoji), nil
	}
	upTo := "全部"
	if w.a.UpTo != 0 {
		upTo = fmt.Sprintf("到消息 %d", w.a.UpTo)
	}
	act.Visible = audience(w.to) + "（能看到你已读）"
	return act, fmt.Sprintf("把 %s标为已读（%s）", called(w.to), upTo), nil
}

// Do runs a write the gate let through, with the arguments the card was made from.
func (c *Conn) Do(ctx context.Context, name string, args json.RawMessage) (string, error) {
	w, err := c.plan(name, args)
	if err != nil {
		return "", err
	}
	cl := c.client()
	if cl == nil {
		return "", errNotConnected
	}
	if left := c.gate.left(); left > 0 {
		return "", heldError{left}
	}
	var out any
	switch name {
	case "telegram_send":
		id, err := cl.Send(ctx, w.to, w.a.Text, w.a.ReplyTo, randomID())
		if err != nil {
			return "", c.writeError(err)
		}
		out = struct {
			Sent messageOut `json:"sent"`
			Chat string     `json:"chat"`
		}{messageOut{ID: id, At: c.stamp(time.Now()), Mine: true, Text: w.a.Text, ReplyTo: w.a.ReplyTo}, w.to.Peer.key()}
	case "telegram_forward":
		random := make([]int64, len(w.a.IDs))
		for i := range random {
			random[i] = randomID()
		}
		if err := cl.Forward(ctx, w.from, w.a.IDs, w.to, random); err != nil {
			return "", c.writeError(err)
		}
		out = struct {
			Forwarded int    `json:"forwarded"`
			To        string `json:"to"`
		}{len(w.a.IDs), w.to.Peer.key()}
	case "telegram_react":
		if err := cl.React(ctx, w.to, w.a.ID, w.a.Emoji); err != nil {
			return "", c.writeError(err)
		}
		out = struct {
			Reacted string `json:"reacted"`
			Chat    string `json:"chat"`
			ID      int    `json:"id"`
		}{w.a.Emoji, w.to.Peer.key(), w.a.ID}
	case "telegram_mark_read":
		upTo := w.a.UpTo
		if err := cl.MarkRead(ctx, w.to, upTo); err != nil {
			return "", c.writeError(err)
		}
		out = struct {
			Read string `json:"read"`
			UpTo int    `json:"up_to,omitempty"`
		}{w.to.Peer.key(), upTo}
	}
	return jsonOf(out)
}

// writeError is a write's failure as the tools say theirs.
func (c *Conn) writeError(err error) error {
	if why, ok := userError(err); ok {
		return errors.New(why)
	}
	return scrubbed(err)
}

// randomID is a send's random_id: Telegram drops a second message sent with the same one.
func randomID() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(binary.LittleEndian.Uint64(b[:]) &^ (1 << 63))
}
