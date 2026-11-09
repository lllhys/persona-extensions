package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	userMention    = regexp.MustCompile(`<@!?(\d+)>`)
	roleMention    = regexp.MustCompile(`<@&(\d+)>`)
	channelMention = regexp.MustCompile(`<#(\d+)>`)
	customEmoji    = regexp.MustCompile(`<a?:(\w+):\d+>`)
	timestampTag   = regexp.MustCompile(`<t:(-?\d+)(?::[tTdDfFR])?>`)
	commandMention = regexp.MustCompile(`</([\w -]+):\d+>`)
)

// Message types (https://discord.com/developers/docs/resources/message#message-object-message-types).
const (
	msgDefault        = 0
	msgThreadCreated  = 18
	msgReply          = 19
	msgSlashCommand   = 20
	msgThreadStarter  = 21
	msgContextCommand = 23
)

// render is a message as the knowledge base and the tools show it, and its type (TEXT, IMAGE, FILE, CARD or
// SYSTEM); "" leaves it out (system messages nobody needs, an empty message). Mentions read as names, custom emoji
// as :name:, Discord's timestamps as times in the user's zone; a reply starts with what it answers, a forward with
// what it forwards. tx are the words of its notes (the archive's are the user's language, the tools' English); d
// may be nil (channel names unknown).
func (c *conn) render(ctx context.Context, tx *texts, s *session, d *discovery, guildID snowflake, m message) (string, string) {
	switch {
	case m.Type == msgDefault, m.Type == msgReply, m.Type == msgSlashCommand, m.Type == msgContextCommand, m.Type == msgThreadStarter:
	case m.Type == msgThreadCreated:
		return fmt.Sprintf(tx.threadStarted, clip(oneLine(m.Content), 100)), "SYSTEM"
	default:
		return "", ""
	}
	var parts []string
	if r := m.ReferencedMessage; r != nil && m.Type == msgReply {
		said, _ := c.body(ctx, tx, s, d, guildID, *r)
		parts = append(parts, fmt.Sprintf(tx.replyTo, r.Author.name(), clip(oneLine(said), 80)))
	}
	text, kind := c.body(ctx, tx, s, d, guildID, m)
	for _, snap := range m.MessageSnapshots {
		if fwd, _ := c.body(ctx, tx, s, d, guildID, snap.Message); fwd != "" {
			text = strings.TrimSpace(text + "\n" + tx.forwarded + fwd)
		}
	}
	if text == "" {
		return "", ""
	}
	return strings.Join(append(parts, text), "\n"), kind
}

// body is what one message says: its text, then its attachments, stickers, poll and embeds as short notes.
func (c *conn) body(ctx context.Context, tx *texts, s *session, d *discovery, guildID snowflake, m message) (string, string) {
	var lines []string
	if t := strings.TrimSpace(c.resolve(ctx, s, d, guildID, m)); t != "" {
		lines = append(lines, t)
	}
	images := 0
	for _, a := range m.Attachments {
		lines = append(lines, fmt.Sprintf(tx.attachment, a.Filename))
		if strings.HasPrefix(a.ContentType, "image/") {
			images++
		}
	}
	for _, st := range m.StickerItems {
		lines = append(lines, fmt.Sprintf(tx.sticker, st.Name))
	}
	if m.Poll != nil {
		lines = append(lines, fmt.Sprintf(tx.poll, m.Poll.Question.Text))
	}
	embeds := 0
	for _, e := range m.Embeds {
		if e.URL != "" && strings.Contains(m.Content, e.URL) {
			continue // a link's preview: the link is in the text already
		}
		title := strings.TrimSpace(e.Title)
		var note []string
		switch {
		case title != "" && e.URL != "":
			note = append(note, fmt.Sprintf(tx.embed, title+" "+e.URL))
		case title != "":
			note = append(note, fmt.Sprintf(tx.embed, title))
		case e.URL != "":
			note = append(note, fmt.Sprintf(tx.embed, e.URL))
		}
		if desc := strings.TrimSpace(e.Description); desc != "" {
			note = append(note, clip(desc, 500))
		}
		for i, f := range e.Fields {
			if i == 10 {
				break
			}
			if line := strings.TrimSpace(clip(oneLine(f.Name), 100) + ": " + clip(oneLine(f.Value), 200)); line != ":" {
				note = append(note, line)
			}
		}
		if len(note) > 0 {
			lines = append(lines, note...)
			embeds++
		}
	}
	// Components V2: a message (usually a bot's) whose text is in its components' text displays.
	if shown := componentText(m.Components); shown != "" {
		lines = append(lines, clip(shown, 2000))
		embeds++
	}
	kind := "TEXT"
	if strings.TrimSpace(m.Content) == "" {
		switch {
		case len(m.Attachments) > 0 && images == len(m.Attachments):
			kind = "IMAGE"
		case len(m.Attachments) > 0:
			kind = "FILE"
		case embeds > 0:
			kind = "CARD"
		}
	}
	return strings.Join(lines, "\n"), kind
}

// componentText is the text of the text displays (type 10) anywhere in a message's components, a line each.
func componentText(components []json.RawMessage) string {
	var lines []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if t, _ := x["type"].(float64); t == 10 {
				if s, _ := x["content"].(string); strings.TrimSpace(s) != "" {
					lines = append(lines, strings.TrimSpace(s))
				}
			}
			for _, child := range x {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	for _, raw := range components {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			walk(v)
		}
	}
	return strings.Join(lines, "\n")
}

// resolve is a message's text with Discord's markup for mentions, emoji and times made readable.
func (c *conn) resolve(ctx context.Context, s *session, d *discovery, guildID snowflake, m message) string {
	t := m.Content
	if t == "" {
		return ""
	}
	t = userMention.ReplaceAllStringFunc(t, func(tag string) string {
		id, _ := parseSnowflake(userMention.FindStringSubmatch(tag)[1])
		for _, u := range m.Mentions {
			if u.ID == id {
				return "@" + u.name()
			}
		}
		return "@?"
	})
	t = roleMention.ReplaceAllStringFunc(t, func(tag string) string {
		id, _ := parseSnowflake(roleMention.FindStringSubmatch(tag)[1])
		if s != nil && guildID != 0 {
			if name := s.roleName(ctx, guildID, id); name != "" {
				return "@" + name
			}
		}
		return "@?"
	})
	t = channelMention.ReplaceAllStringFunc(t, func(tag string) string {
		id, _ := parseSnowflake(channelMention.FindStringSubmatch(tag)[1])
		if d != nil {
			if name := d.names[id]; name != "" {
				return "#" + name
			}
		}
		for _, mc := range m.MentionChannels {
			if mc.ID == id {
				return "#" + mc.Name
			}
		}
		return "#?"
	})
	t = customEmoji.ReplaceAllString(t, ":$1:")
	t = commandMention.ReplaceAllString(t, "/$1")
	t = timestampTag.ReplaceAllStringFunc(t, func(tag string) string {
		sec, err := strconv.ParseInt(timestampTag.FindStringSubmatch(tag)[1], 10, 64)
		if err != nil {
			return tag
		}
		return time.Unix(sec, 0).In(c.zone).Format("2006-01-02 15:04")
	})
	return t
}

// oneLine joins s's lines with spaces.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// clip is s cut to at most n runes, with … when cut.
func clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
