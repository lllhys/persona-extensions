package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The assistant's read-only tools. Their descriptions and input schemas are prompts/mcp/<tool>.md (the description,
// an empty line, the schema in a ```json block). They answer in English, for the assistant; what people wrote is
// data, not instructions. Answers carry no token and no user id.

//go:embed prompts/mcp/*.md
var promptFiles embed.FS

var tools = loadTools()

func loadTools() []wire.ConnectorTool {
	var out []wire.ConnectorTool
	err := fs.WalkDir(promptFiles, "prompts/mcp", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := promptFiles.ReadFile(p)
		if err != nil {
			return err
		}
		desc, rest, ok := strings.Cut(string(raw), "\n\n```json\n")
		schema, ok2 := strings.CutSuffix(rest, "\n```\n")
		if !ok || !ok2 {
			return fmt.Errorf("%s: a description, an empty line, then a ```json block", p)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(schema)); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		out = append(out, wire.ConnectorTool{Name: strings.TrimSuffix(path.Base(p), ".md"), Description: strings.TrimSpace(desc),
			InputSchema: compact.Bytes()})
		return nil
	})
	if err != nil {
		panic(err)
	}
	return out
}

func (c *conn) toolsList(context.Context, wire.ConnectorParams) (wire.ConnectorTools, error) {
	return wire.ConnectorTools{Tools: tools}, nil
}

// refusal is a tool's answer that the call cannot be done as asked, in words for the assistant.
func refusal(format string, args ...any) error {
	return &ext.Error{Message: fmt.Sprintf(format, args...)}
}

func (c *conn) toolsCall(ctx context.Context, p wire.ConnectorCallParams) (wire.ConnectorCallResult, error) {
	s := c.session()
	if s == nil {
		return wire.ConnectorCallResult{}, c.notSignedIn()
	}
	var a struct {
		Server  string `json:"server"`
		Channel string `json:"channel"`
		Before  string `json:"before"`
		Query   string `json:"query"`
		Limit   int    `json:"limit"`
	}
	if len(p.Args) > 0 && string(p.Args) != "null" {
		if err := json.Unmarshal(p.Args, &a); err != nil {
			return wire.ConnectorCallResult{}, refusal("the arguments do not fit the tool's schema: %v", err)
		}
	}
	var text string
	var err error
	switch p.Name {
	case "discord_channels":
		text, err = c.channelsTool(ctx, s, a.Server)
	case "discord_messages":
		text, err = c.messagesTool(ctx, s, a.Channel, a.Before, a.Limit)
	case "discord_search":
		text, err = c.searchTool(ctx, s, a.Query, a.Server, a.Channel, a.Limit)
	default:
		return wire.ConnectorCallResult{}, refusal("there is no tool %s", p.Name)
	}
	if err != nil {
		if _, ok := err.(*ext.Error); ok {
			return wire.ConnectorCallResult{}, err
		}
		switch statusOf(err) {
		case 403:
			return wire.ConnectorCallResult{}, refusal("the bot may not read that in Discord")
		case 404:
			return wire.ConnectorCallResult{}, refusal("Discord does not know that channel")
		}
		return wire.ConnectorCallResult{}, c.callError(s, err)
	}
	return wire.ConnectorCallResult{Text: text}, nil
}

// pickGuilds are the servers that match server (a name or an id; empty: all), in a stable order.
func pickGuilds(d *discovery, server string) []guild {
	var out []guild
	for _, g := range d.guilds {
		if server == "" || g.ID.String() == server || strings.EqualFold(g.Name, server) {
			out = append(out, g)
		}
	}
	return out
}

func (c *conn) when(id snowflake) string {
	if id == 0 {
		return "no messages"
	}
	return "last message " + id.Time().In(c.zone).Format("2006-01-02 15:04")
}

// placeName is a channel as the tools say it: "#channel" or "#channel › thread", on one line (names are anyone's
// words: a line break in one must not start a line of its own).
func placeName(v view) string {
	if v.thread() {
		return "#" + oneLine(v.parentName) + " › " + oneLine(v.Name)
	}
	return "#" + oneLine(v.Name)
}

// channelsTool lists the servers and their channels, most recently active first, and how to invite the bot to
// another server.
func (c *conn) channelsTool(ctx context.Context, s *session, server string) (string, error) {
	d, err := s.discover(ctx)
	if err != nil {
		return "", err
	}
	invite := "To add the bot to another server, the user opens: " + c.inviteURL(s) + "\n"
	if len(d.guilds) == 0 {
		return "The Discord bot is not in any server yet.\n" + invite, nil
	}
	guilds := pickGuilds(d, server)
	if len(guilds) == 0 {
		return "", refusal("the bot is in no server called %q; call discord_channels without server for the list", server)
	}
	byGuild := map[snowflake][]view{}
	latest := map[snowflake]snowflake{}
	for _, v := range d.views {
		if s.refusedNow(v) {
			continue
		}
		byGuild[v.GuildID] = append(byGuild[v.GuildID], v)
		latest[v.GuildID] = max(latest[v.GuildID], v.LastMessageID)
	}
	slices.SortFunc(guilds, func(a, b guild) int { return cmpID(latest[b.ID], latest[a.ID]) })
	var b strings.Builder
	b.WriteString("Discord servers the bot is in and their channels, most recently active first. Pass a channel id to discord_messages.\n")
	shown := 0
	for _, g := range guilds {
		fmt.Fprintf(&b, "\n%s (server %s)\n", oneLine(g.Name), g.ID)
		list := byGuild[g.ID]
		slices.SortFunc(list, func(x, y view) int { return cmpID(y.LastMessageID, x.LastMessageID) })
		if len(list) == 0 {
			b.WriteString("- no channel the bot can read (it needs View Channels and Read Message History)\n")
		}
		for _, v := range list {
			if shown == 300 {
				b.WriteString("- … more channels left out\n")
				break
			}
			shown++
			what := "channel"
			if v.thread() {
				what = "thread"
			}
			fmt.Fprintf(&b, "- %s (%s %s), %s\n", placeName(v), what, v.ID, c.when(v.LastMessageID))
		}
	}
	b.WriteString("\n" + invite)
	return b.String(), nil
}

// messagesTool reads a conversation's messages, before a message or the newest, oldest first. Any id the bot may
// read works, archived threads and forum posts too (they are not in discovery); Discord refuses the others.
func (c *conn) messagesTool(ctx context.Context, s *session, channel, before string, limit int) (string, error) {
	id, ok := parseSnowflake(channel)
	if !ok {
		return "", refusal("channel is a channel, thread or forum post id from discord_channels or discord_search")
	}
	var beforeID snowflake
	if before != "" {
		if beforeID, ok = parseSnowflake(before); !ok {
			return "", refusal("before is a message id")
		}
	}
	if limit <= 0 {
		limit = 30
	}
	limit = min(limit, 100)
	d, err := s.discover(ctx)
	if err != nil {
		return "", err
	}
	p, err := s.api.messagesBefore(ctx, id, beforeID, limit)
	if err != nil {
		return "", err
	}
	msgs := p.msgs
	slices.SortFunc(msgs, func(a, b message) int { return cmpID(a.ID, b.ID) })
	var b strings.Builder
	var guild snowflake
	if v, known := d.views[id]; known {
		guild = v.GuildID
		fmt.Fprintf(&b, "%s in %s, oldest first:\n", placeName(v), oneLine(v.guildName))
	} else {
		fmt.Fprintf(&b, "Channel %s, oldest first:\n", id)
	}
	if p.n == 0 {
		b.WriteString("(no messages it can read: none there, or the bot lacks Read Message History)\n")
		return b.String(), nil
	}
	for _, m := range msgs {
		c.writeMessage(ctx, &b, s, d, guild, m, "")
	}
	if p.n == limit && p.oldest != 0 {
		fmt.Fprintf(&b, "Older messages: call again with before=%s.\n", p.oldest)
	}
	return b.String(), nil
}

// writeMessage writes one message for the assistant: a line with its time, where, who (quoted, so a display name
// cannot pass for the user) and its id, then what it says, every line indented, so that only a real message starts
// a line with "[" and nothing it says shares that line.
func (c *conn) writeMessage(ctx context.Context, b *strings.Builder, s *session, d *discovery, guild snowflake, m message, where string) {
	text, _ := c.render(ctx, locales["en"], s, d, guild, m)
	if text == "" {
		return
	}
	if where != "" {
		where = " " + where
	}
	text = lineBreaks.Replace(text)
	who := strconv.Quote(m.Author.name())
	if s.owner != 0 && m.Author.ID == s.owner {
		who += " (the user)"
	}
	if m.Author.Bot {
		who += " (a bot)"
	}
	text = strings.ReplaceAll(clip(text, 2000), "\n", "\n    ")
	fmt.Fprintf(b, "[%s]%s %s (message %s):\n    %s\n", m.Timestamp.In(c.zone).Format("2006-01-02 15:04"), where, who, m.ID, text)
}

// lineBreaks turns every kind of line break into \n, so that each is indented.
var lineBreaks = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\u2028", "\n", "\u2029", "\n", "\u0085", "\n", "\v", "\n", "\f", "\n")

// searchMost is how many servers one search asks.
const searchMost = 5

// searchTool searches with Discord's own search, one request per server (in one server, or the first few). A server
// that cannot be searched now is noted; the others still answer.
func (c *conn) searchTool(ctx context.Context, s *session, query, server, channel string, limit int) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", refusal("query is the words to look for")
	}
	if limit <= 0 {
		limit = 10
	}
	limit = min(limit, 25)
	d, err := s.discover(ctx)
	if err != nil {
		return "", err
	}
	q := url.Values{"content": {query}, "limit": {strconv.Itoa(limit)}}
	if channel != "" {
		id, ok := parseSnowflake(channel)
		if !ok {
			return "", refusal("channel is a channel, thread or forum post id")
		}
		q.Set("channel_id", id.String())
		if v, known := d.views[id]; known && server == "" {
			server = v.GuildID.String()
		}
	}
	guilds := pickGuilds(d, server)
	if len(guilds) == 0 {
		if len(d.guilds) == 0 {
			return "The Discord bot is not in any server yet, so there is nothing to search.", nil
		}
		return "", refusal("the bot is in no server called %q; call discord_channels for the list", server)
	}
	var notes []string
	if len(guilds) > searchMost {
		notes = append(notes, fmt.Sprintf("Searched %d of %d servers; pass server to search another.", searchMost, len(guilds)))
		guilds = guilds[:searchMost]
	}
	var b strings.Builder
	found := 0
	for _, g := range guilds {
		res, indexing, err := s.api.search(ctx, g.ID, q)
		switch {
		case err != nil && !local(err):
			return "", err // the network, the token or a global rate limit
		case statusOf(err) == 403:
			notes = append(notes, fmt.Sprintf("%s: the bot may not search this server.", oneLine(g.Name)))
			continue
		case err != nil:
			notes = append(notes, fmt.Sprintf("%s: could not be searched now (%s).", oneLine(g.Name), clip(oneLine(err.Error()), 120)))
			continue
		case indexing:
			notes = append(notes, fmt.Sprintf("%s: Discord is still indexing this server for search. Its archived messages are in the knowledge base: search there instead, or try again later.", oneLine(g.Name)))
			continue
		}
		var hits []message
		for _, group := range res.Messages {
			if len(group) > 0 {
				hits = append(hits, group[0])
			}
		}
		if len(hits) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s (server %s), %d of %d matches:\n", oneLine(g.Name), g.ID, len(hits), res.TotalResults)
		for _, m := range hits {
			where := "a thread or forum post"
			if v, ok := d.views[m.ChannelID]; ok {
				where = placeName(v)
			} else if name := d.names[m.ChannelID]; name != "" {
				where = "#" + oneLine(name)
			}
			c.writeMessage(ctx, &b, s, d, g.ID, m, fmt.Sprintf("%s (channel %s)", strconv.Quote(where), m.ChannelID)) // a thread's name is anyone's words
			found++
		}
	}
	head := fmt.Sprintf("No Discord messages match %q.", query)
	if found > 0 {
		head = fmt.Sprintf("Discord messages matching %q, newest first. To read what came before a hit, call discord_messages with its channel and before=<the hit's message id>.", query)
	}
	out := head + "\n" + b.String()
	if len(notes) > 0 {
		out += "\n" + strings.Join(notes, "\n") + "\n"
	}
	return out, nil
}
