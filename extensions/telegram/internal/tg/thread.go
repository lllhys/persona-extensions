package telegram

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
)

// The inbox's conversations as the app's conversation page (connector.Threads; docs/extensions/telegram.md#request-pacing): a page
// of messages a request (and one more for the messages they reply to that the page does not have), and each picture
// when the app shows it, a request a part. A group's headcount comes with its chat when Telegram said it there; else
// its first page asks once (Info) and that answer holds for membersFor. Everything goes through the connector's gate:
// nothing during a flood wait.

const (
	locsMost   = 4000 // pictures' locations kept for the pages read
	peersMost  = 4000
	membersFor = 12 * time.Hour
)

// headcount is a group's member count as Info said it, or 0 when asking failed; at is when it was asked.
type headcount struct {
	n  int
	at time.Time
}

// Media refs, opaque to the hub and the app: "a.<peer key>.<photo id>" (a person's or a chat's picture, which a new
// one gets a new id for), "m.<chat key>.<message id>.f|t" (a message's picture: the media itself, or its thumbnail).

func avatarRef(p peer, photo int64) string {
	if photo == 0 || p.ID == 0 {
		return ""
	}
	return fmt.Sprintf("a.%s.%d", p.key(), photo)
}

func mediaRef(chat string, id int, thumb bool) string {
	v := "f"
	if thumb {
		v = "t"
	}
	return fmt.Sprintf("m.%s.%d.%s", chat, id, v)
}

func (c *Conn) Thread(ctx context.Context, thread, before string, limit int) (connector.ThreadPage, error) {
	cl := c.client()
	if cl == nil {
		return connector.ThreadPage{}, errNotConnected
	}
	if left := c.gate.left(); left > 0 {
		return connector.ThreadPage{}, heldError{left}
	}
	from := 0
	if before != "" {
		n, err := strconv.Atoi(before)
		if err != nil || n <= 0 {
			return connector.ThreadPage{}, fmt.Errorf("before: %q is not a message id", before)
		}
		from = n
	}
	d, err := c.find(ctx, cl, thread)
	if err != nil {
		return connector.ThreadPage{}, c.writeError(err)
	}
	c.remember([]dialog{d}) // a reply from the page names it
	limit = min(max(limit, 1), connector.ThreadMost)
	msgs, peers, err := cl.Page(ctx, d, from, limit)
	if err != nil {
		return connector.ThreadPage{}, c.writeError(err)
	}
	c.keepPeers(peers)
	dcs := map[int64]int{d.Photo: d.PhotoDC}
	for _, m := range msgs {
		dcs[m.FromPhoto] = m.FromDC
	}
	c.keepPhotoDCs(dcs)
	page := connector.ThreadPage{Chat: c.threadChat(d), Messages: make([]connector.ThreadMessage, 0, len(msgs))}
	if len(msgs) == limit {
		page.Before = strconv.Itoa(msgs[0].ID)
	}
	quotes := c.quotes(ctx, cl, d, msgs)
	for _, m := range msgs {
		page.Messages = append(page.Messages, c.threadMessage(d, m, quotes))
	}
	if from == 0 && d.Kind == "group" {
		page.Chat.Members = c.members(ctx, cl, d) // last: a flood wait here costs the count, not the page
	}
	return page, nil
}

// members is how many are in group d: what its chat said, else Info's answer, asked at most once in membersFor (a
// failure too: an error is not asked again sooner). 0: not known.
func (c *Conn) members(ctx context.Context, cl client, d dialog) int {
	if d.Members > 0 {
		return d.Members
	}
	key := d.Peer.key()
	c.box.mu.Lock()
	h, ok := c.box.members[key]
	c.box.mu.Unlock()
	if ok && time.Since(h.at) < membersFor {
		return h.n
	}
	info, err := cl.Info(ctx, d)
	if errors.As(err, new(heldError)) || ctx.Err() != nil {
		return h.n // nothing was asked
	}
	h = headcount{n: info.Members, at: time.Now()}
	if err != nil {
		h.n = 0
	}
	c.box.mu.Lock()
	if c.box.members == nil || len(c.box.members) > peersMost {
		c.box.members = map[string]headcount{}
	}
	c.box.members[key] = h
	c.box.mu.Unlock()
	return h.n
}

func (c *Conn) threadChat(d dialog) connector.ThreadChat {
	kind := d.Kind
	if d.Broadcast {
		kind = "channel"
	}
	out := connector.ThreadChat{ID: d.Peer.key(), Name: d.Name, Kind: kind, URL: openURL(d), Avatar: avatarRef(d.Peer, d.Photo),
		Reply: "telegram_send"}
	c.keepPeers(map[string]peer{d.Peer.key(): d.Peer})
	return out
}

func (c *Conn) threadMessage(d dialog, m pageMessage, quotes map[int]connector.ThreadQuote) connector.ThreadMessage {
	out := connector.ThreadMessage{ID: strconv.Itoa(m.ID), At: m.At, Mine: m.Out, Event: m.Event, Text: m.Caption, Edited: m.Edited}
	if !m.Out {
		out.Sender = connector.ThreadPerson{ID: m.FromKey, Name: m.From, Avatar: avatarRef(m.FromPeer, m.FromPhoto)}
	} else {
		out.Sender = connector.ThreadPerson{Name: "我"}
	}
	if d.Peer.Kind == 'c' && d.Username != "" {
		out.URL = fmt.Sprintf("https://t.me/%s/%d", d.Username, m.ID)
	}
	if m.ReplyTo != 0 {
		q, ok := quotes[m.ReplyTo]
		if !ok {
			q = connector.ThreadQuote{ID: strconv.Itoa(m.ReplyTo)}
		}
		out.ReplyTo = &q
	}
	if md := m.Media; md != nil {
		tm := connector.ThreadMedia{Kind: md.Kind, Mime: md.Mime, Name: md.Name, Size: md.Size, Duration: md.Duration, Width: md.Width,
			Height: md.Height, Emoji: md.Emoji}
		key := d.Peer.key()
		if md.Full != nil {
			tm.Ref = mediaRef(key, m.ID, false)
			c.keepLoc(tm.Ref, md.Full)
		}
		if md.Thumb != nil {
			tm.Thumb = mediaRef(key, m.ID, true)
			c.keepLoc(tm.Thumb, md.Thumb)
		}
		out.Media = []connector.ThreadMedia{tm}
	}
	return out
}

// quotes are the messages the page's messages reply to: from the page itself, the rest in one request (a failed one
// leaves them as ids: the app finds them when the user goes back to them).
func (c *Conn) quotes(ctx context.Context, cl client, d dialog, msgs []pageMessage) map[int]connector.ThreadQuote {
	out := map[int]connector.ThreadQuote{}
	have := map[int]pageMessage{}
	for _, m := range msgs {
		have[m.ID] = m
	}
	var missing []int
	for _, m := range msgs {
		if m.ReplyTo == 0 {
			continue
		}
		if q, ok := have[m.ReplyTo]; ok {
			out[m.ReplyTo] = quoteOf(q.message, q.Caption)
		} else if !slices.Contains(missing, m.ReplyTo) {
			missing = append(missing, m.ReplyTo)
		}
	}
	if len(missing) == 0 || c.gate.left() > 0 {
		return out
	}
	found, err := cl.Messages(ctx, d, missing)
	if err != nil {
		return out
	}
	for _, m := range found {
		out[m.ID] = quoteOf(m, m.Text)
	}
	return out
}

func quoteOf(m message, text string) connector.ThreadQuote {
	from := m.From
	if m.Out {
		from = "我"
	}
	if text == "" {
		text = m.Text
	}
	return connector.ThreadQuote{ID: strconv.Itoa(m.ID), From: from, Text: clip(oneLine(text), 200)}
}

func (c *Conn) keepPeers(ps map[string]peer) {
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	if c.box.peers == nil || len(c.box.peers) > peersMost {
		c.box.peers = map[string]peer{}
	}
	for k, p := range ps {
		if p.Hash != 0 || p.Kind == 'g' {
			c.box.peers[k] = p
		}
	}
}

// keepPhotoDCs keeps where the pictures the page names are (photo id -> data center), for fetching them there.
func (c *Conn) keepPhotoDCs(dcs map[int64]int) {
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	if c.box.photoDCs == nil || len(c.box.photoDCs) > peersMost {
		c.box.photoDCs = map[int64]int{}
	}
	for id, dc := range dcs {
		if id != 0 && dc != 0 {
			c.box.photoDCs[id] = dc
		}
	}
}

func (c *Conn) keepLoc(ref string, f *fileLoc) {
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	if c.box.locs == nil || len(c.box.locs) > locsMost {
		c.box.locs = map[string]*fileLoc{}
	}
	c.box.locs[ref] = f
}

var errNoMedia = errors.New("不认识这个媒体")

func (c *Conn) Media(ctx context.Context, ref string) (connector.Media, error) {
	cl := c.client()
	if cl == nil {
		return connector.Media{}, errNotConnected
	}
	if left := c.gate.left(); left > 0 {
		return connector.Media{}, heldError{left}
	}
	ctx, trace := withTrace(ctx)
	start := time.Now()
	m, err := c.media(ctx, cl, ref)
	kind := "message picture"
	if strings.HasPrefix(ref, "a.") {
		kind = "avatar"
	}
	if line := trace.line(kind, time.Since(start), err); line != "" {
		traceLogf("%s", line)
	}
	return m, err
}

func (c *Conn) media(ctx context.Context, cl client, ref string) (connector.Media, error) {
	f, err := c.locate(ctx, cl, ref)
	if err != nil {
		return connector.Media{}, err
	}
	data, err := cl.Fetch(ctx, f, connector.MediaMost)
	if err != nil {
		return connector.Media{}, c.writeError(err)
	}
	if len(data) == 0 {
		return connector.Media{}, errMediaGone
	}
	return connector.Media{Mime: f.mime, Data: data}, nil
}

// locate is where ref's picture is: kept from the page that listed it, else read again (a request).
func (c *Conn) locate(ctx context.Context, cl client, ref string) (*fileLoc, error) {
	c.box.mu.Lock()
	f := c.box.locs[ref]
	c.box.mu.Unlock()
	if f != nil {
		return f, nil
	}
	parts := strings.Split(ref, ".")
	switch {
	case len(parts) == 3 && parts[0] == "a":
		photo, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return nil, errNoMedia
		}
		c.box.mu.Lock()
		p, ok := c.box.peers[parts[1]]
		dc := c.box.photoDCs[photo]
		c.box.mu.Unlock()
		if !ok {
			if d, err := pick(c.listed(), parts[1]); err == nil {
				p, ok = d.Peer, true
				if d.Photo == photo {
					dc = d.PhotoDC
				}
			}
		}
		if !ok {
			return nil, errNoMedia // a person no page read since the start: their initial instead
		}
		return avatarLoc(p, photo, dc), nil
	case len(parts) == 4 && parts[0] == "m" && (parts[3] == "f" || parts[3] == "t"):
		id, err := strconv.Atoi(parts[2])
		if err != nil {
			return nil, errNoMedia
		}
		d, err := c.find(ctx, cl, parts[1])
		if err != nil {
			return nil, c.writeError(err)
		}
		f, err := cl.Locate(ctx, d, id, parts[3] == "t")
		if err != nil {
			return nil, c.writeError(err)
		}
		c.keepLoc(ref, f)
		return f, nil
	}
	return nil, errNoMedia
}

// listed are the conversations last listed (the inbox's, the CLI's reads').
func (c *Conn) listed() []dialog {
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	out := slices.Clone(c.box.dialogs)
	for _, d := range c.box.seen {
		out = append(out, d)
	}
	return out
}
