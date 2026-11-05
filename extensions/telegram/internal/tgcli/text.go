package tgcli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// The answers as --format text prints them: a line a conversation, a message, a member. Times are shown as the hub
// gave them (in the user's zone).

type (
	chatJSON struct {
		ID       string       `json:"id"`
		Name     string       `json:"name"`
		Kind     string       `json:"kind"`
		Username string       `json:"username"`
		Unread   int          `json:"unread"`
		Mentions int          `json:"mentions"`
		Muted    bool         `json:"muted"`
		Archived bool         `json:"archived"`
		Last     *messageJSON `json:"last"`
		About    string       `json:"about"`
		Members  int          `json:"members"`
	}
	messageJSON struct {
		ID      int    `json:"id"`
		At      string `json:"at"`
		From    string `json:"from"`
		Mine    bool   `json:"mine"`
		Text    string `json:"text"`
		ReplyTo int    `json:"reply_to"`
	}
)

// shown is an answer's time as a person reads it.
func shown(at string) string {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return at
	}
	return t.Format("2006-01-02 15:04")
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func (c chatJSON) line() string {
	kind := "私聊"
	if c.Kind == "group" {
		kind = "群"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s  %s", c.ID, c.Name, kind)
	if c.Username != "" {
		fmt.Fprintf(&b, "  @%s", c.Username)
	}
	if c.Unread > 0 {
		fmt.Fprintf(&b, "  未读 %d", c.Unread)
		if c.Mentions > 0 {
			fmt.Fprintf(&b, "（@你 %d）", c.Mentions)
		}
	}
	if c.Muted {
		b.WriteString("  静音")
	}
	if c.Archived {
		b.WriteString("  已归档")
	}
	return b.String()
}

func (m messageJSON) line() string {
	from := m.From
	if m.Mine {
		from = "我"
	}
	reply := ""
	if m.ReplyTo != 0 {
		reply = fmt.Sprintf("（回复 %d）", m.ReplyTo)
	}
	return fmt.Sprintf("%s [%d] %s%s：%s", shown(m.At), m.ID, from, reply, oneLine(m.Text))
}

func textChats(w io.Writer, raw []byte) error {
	var a struct {
		Chats []chatJSON `json:"chats"`
		More  bool       `json:"more"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return err
	}
	if len(a.Chats) == 0 {
		fmt.Fprintln(w, "没有符合的会话。")
	}
	for _, c := range a.Chats {
		fmt.Fprintln(w, c.line())
		if c.Last != nil {
			fmt.Fprintln(w, "    "+c.Last.line())
		}
	}
	if a.More {
		fmt.Fprintln(w, "（还有更多：--limit 加大）")
	}
	return nil
}

func textMessages(w io.Writer, raw []byte) error {
	var a struct {
		Chat       chatJSON      `json:"chat"`
		Messages   []messageJSON `json:"messages"`
		NextBefore int           `json:"next_before"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s（%s）\n", a.Chat.Name, a.Chat.ID)
	if len(a.Messages) == 0 {
		fmt.Fprintln(w, "没有消息。")
	}
	for _, m := range a.Messages {
		fmt.Fprintln(w, m.line())
	}
	if a.NextBefore != 0 {
		fmt.Fprintf(w, "（更早的：--before %d）\n", a.NextBefore)
	}
	return nil
}

func textSearch(w io.Writer, raw []byte) error {
	var a struct {
		Results []struct {
			Chat    chatJSON    `json:"chat"`
			Message messageJSON `json:"message"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return err
	}
	if len(a.Results) == 0 {
		fmt.Fprintln(w, "没有找到。")
	}
	for _, r := range a.Results {
		fmt.Fprintf(w, "%s  %s  %s\n", r.Chat.ID, r.Chat.Name, r.Message.line())
	}
	return nil
}

func textMessage(w io.Writer, raw []byte) error {
	var a struct {
		Chat    chatJSON    `json:"chat"`
		Message messageJSON `json:"message"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s（%s）\n%s\n", a.Chat.Name, a.Chat.ID, a.Message.line())
	return nil
}

func textChat(w io.Writer, raw []byte) error {
	var a struct {
		Chat chatJSON `json:"chat"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return err
	}
	fmt.Fprintln(w, a.Chat.line())
	if a.Chat.Members > 0 {
		fmt.Fprintf(w, "成员 %d 人\n", a.Chat.Members)
	}
	if a.Chat.About != "" {
		fmt.Fprintln(w, "简介："+oneLine(a.Chat.About))
	}
	if a.Chat.Last != nil {
		fmt.Fprintln(w, "最后："+a.Chat.Last.line())
	}
	return nil
}

func textMembers(w io.Writer, raw []byte) error {
	var a struct {
		Chat    chatJSON `json:"chat"`
		Members []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Username string `json:"username"`
			Bot      bool   `json:"bot"`
		} `json:"members"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return err
	}
	fmt.Fprintf(w, "%s（%s）：%d 人，列出 %d 人\n", a.Chat.Name, a.Chat.ID, a.Count, len(a.Members))
	for _, m := range a.Members {
		line := m.ID + "  " + m.Name
		if m.Username != "" {
			line += "  @" + m.Username
		}
		if m.Bot {
			line += "  机器人"
		}
		fmt.Fprintln(w, line)
	}
	return nil
}

// textWrite says what a write did.
func textWrite(w io.Writer, raw []byte) error {
	var a struct {
		Sent *struct {
			ID int `json:"id"`
		} `json:"sent"`
		Chat      string `json:"chat"`
		Forwarded int    `json:"forwarded"`
		To        string `json:"to"`
		Reacted   string `json:"reacted"`
		ID        int    `json:"id"`
		Read      string `json:"read"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return err
	}
	switch {
	case a.Sent != nil:
		fmt.Fprintf(w, "已发送到 %s（消息 %d）\n", a.Chat, a.Sent.ID)
	case a.Forwarded > 0:
		fmt.Fprintf(w, "已转发 %d 条到 %s\n", a.Forwarded, a.To)
	case a.Reacted != "":
		fmt.Fprintf(w, "已在 %s 的消息 %d 上点 %s\n", a.Chat, a.ID, a.Reacted)
	case a.Read != "":
		fmt.Fprintf(w, "已把 %s 标为已读\n", a.Read)
	default:
		fmt.Fprintln(w, string(raw))
	}
	return nil
}
