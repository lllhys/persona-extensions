package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
)

// The inbox (connector.Inbox): what in the account needs the user. Collect answers from memory — the conversations
// as last listed (an archive round, Telegram's updates, connecting) with a few messages of context for each thing —
// so the hub's every collection costs Telegram nothing.
//
// - A private chat whose last message is the other person's and unread: direct message. Read somewhere: done. I wrote last:
//   replied.
// - A group with unread messages that @ me or reply to me (Telegram counts both): mentions you. Read: done.
// - Muted and archived conversations stay out (the knowledge base still has them); so do bots and channels.

const (
	inboxDays      = 8 * 24 * time.Hour // conversations quieter than this say nothing
	contextLines   = 10
	contextsPerRun = 10   // context reads per refresh: the rest the next one
	inboxMost      = 4000 // groups' marks and moves' contexts kept in memory (like the page caches, thread.go)
)

type inboxState struct {
	mu       sync.Mutex
	dialogs  []dialog
	contexts map[string]threadContext // peer key -> the context read for a move
	marks    map[string]markState     // group key -> when its mentions last went up
	seen     map[string]dialog        // conversations the tg CLI's reads listed, for its writes to name (cli.go)
	peers    map[string]peer          // people and chats the conversation pages named, for their pictures (thread.go)
	locs     map[string]*fileLoc      // where the pages' pictures are, by media ref (thread.go)
	members  map[string]headcount     // group key -> how many are in it, as asked (thread.go)
	photoDCs map[int64]int            // picture id -> the data center it is on, from the pages read (thread.go)
	refresh  sync.Mutex               // one refresh at a time
}

type threadContext struct {
	last string
	text string
}

type markState struct {
	mentions int
	top      int // the newest message when they went up: the item's move
}

// Collect gives a thing for each conversation that needs the user or did (so an item read elsewhere closes). Each
// needs the user (action), as the built-in connector said; with the user's own triage rules the hub's model sorts it
// by them instead, and it is action when no model does (the fallback, extra.rule_tier: Telegram never waits for one).
func (c *Conn) Collect(context.Context) ([]connector.InboxThing, error) {
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	now := time.Now()
	var out []connector.InboxThing
	for _, d := range c.box.dialogs {
		if !d.kept() || d.Muted || d.Archived || now.Sub(d.TopAt) > inboxDays {
			continue
		}
		key := d.Peer.key()
		t := connector.InboxThing{Thread: key, Title: d.Name, Sender: d.TopFrom, Preview: d.TopText, At: d.TopAt,
			OpenURL: openURL(d), Tier: connector.TierAction, Context: c.box.contexts[key].text,
			Tags:  []connector.Tag{{Key: connector.TagChat, Value: key, Name: d.Name}},
			Extra: &connector.ThingExtra{RuleTier: string(connector.TierAction)}}
		if c.rules != "" {
			t.Tier, t.Rules = "", c.rules
		}
		switch d.Kind {
		case "dm":
			t.Kind, t.Label, t.Last = "dm", "私聊", strconv.Itoa(d.Top)
			switch {
			case d.TopOut:
				t.Done = connector.DoneReplied
			case d.Unread == 0:
				t.Read = true
			}
		default:
			mark := c.box.marks[key]
			t.Kind, t.Label, t.Last = "mention", "@你", strconv.Itoa(mark.top)
			t.Read = d.Mentions == 0
			if mark.top == 0 {
				continue // no mention seen yet
			}
		}
		out = append(out, t)
	}
	return out, nil
}

// Reply: the connector only reads. The app shows "Reply in Telegram" (OpenURL) instead.
func (c *Conn) Reply(context.Context, connector.InboxReply) error {
	return errors.New("Telegram 是只读的：在 Telegram 里回复")
}

// openURL opens the conversation in the Telegram app: by username, a user by id, a supergroup by its link.
func openURL(d dialog) string {
	switch {
	case d.Username != "":
		return "tg://resolve?domain=" + d.Username
	case d.Peer.Kind == 'u':
		return fmt.Sprintf("tg://user?id=%d", d.Peer.ID)
	case d.Peer.Kind == 'c':
		return fmt.Sprintf("https://t.me/c/%d/%d", d.Peer.ID, d.Top)
	}
	return ""
}

// noteDialogs keeps the conversations as just listed and the moves of their mentions.
func (c *Conn) noteDialogs(ds []dialog) {
	c.box.mu.Lock()
	defer c.box.mu.Unlock()
	if c.box.marks == nil {
		c.box.marks, c.box.contexts = map[string]markState{}, map[string]threadContext{}
	}
	// A mark outlives the conversation that earned it (a group left, a one-off supergroup), so marks would add
	// entries forever in a process that lists conversations by the minute. Past inboxMost drop the marks the
	// listing in hand no longer contains: Collect only walks the conversations listed now, so a dropped group
	// says nothing anyway, while the groups still listed keep their move and a mention read this round closes.
	// An empty listing is never pruned: callers only get here after a successful Dialogs, and thousands of
	// marks plus zero conversations means the answer is abnormal, not that every group left.
	if len(ds) > 0 && len(c.box.marks) > inboxMost {
		listed := make(map[string]struct{}, len(ds))
		for _, d := range ds {
			listed[d.Peer.key()] = struct{}{}
		}
		for key := range c.box.marks {
			if _, ok := listed[key]; !ok {
				delete(c.box.marks, key)
			}
		}
	}
	for _, d := range ds {
		if d.Kind != "group" {
			continue
		}
		key := d.Peer.key()
		m := c.box.marks[key]
		if d.Mentions > m.mentions || d.Mentions > 0 && m.top == 0 {
			m.top = d.Top
		}
		m.mentions = d.Mentions
		c.box.marks[key] = m
	}
	c.box.dialogs = ds
}

// refreshInbox lists the conversations and reads the context of the things that moved, then the hub is told.
func (c *Conn) refreshInbox(ctx context.Context) {
	cl := c.client()
	if cl == nil || c.gate.left() > 0 { // during a flood wait the inbox stays as last read
		return
	}
	c.box.refresh.Lock()
	defer c.box.refresh.Unlock()
	ds, err := cl.Dialogs(ctx, inboxDialogs)
	if err != nil {
		return
	}
	c.noteDialogs(ds)
	c.readContexts(ctx, cl)
}

// readContexts reads a few messages for each thing whose move has no context yet.
func (c *Conn) readContexts(ctx context.Context, cl client) {
	// Every new move adds a context entry (the key is the move's message id), so contexts would grow for the
	// process's lifetime. It is only a read cache: past inboxMost reset it, and the moves in hand are re-read
	// below (at most contextsPerRun this round, the rest the next one).
	c.box.mu.Lock()
	if len(c.box.contexts) > inboxMost {
		c.box.contexts = map[string]threadContext{}
	}
	c.box.mu.Unlock()
	things, _ := c.Collect(ctx)
	read := 0
	for _, t := range things {
		if t.Read || t.Done != "" || read == contextsPerRun {
			continue
		}
		c.box.mu.Lock()
		have := c.box.contexts[t.Thread].last == t.Last
		var d dialog
		for _, x := range c.box.dialogs {
			if x.Peer.key() == t.Thread {
				d = x
			}
		}
		c.box.mu.Unlock()
		if have {
			continue
		}
		read++
		var msgs []message
		var err error
		if t.Kind == "mention" {
			msgs, err = cl.UnreadMentions(ctx, d, contextLines)
		} else {
			msgs, err = cl.Older(ctx, d, 0, time.Time{}, contextLines)
		}
		if err != nil { // a flood wait among them: the rest next time
			return
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s（%s）\n", d.Name, map[string]string{"dm": "Telegram 私聊", "mention": "Telegram 群"}[t.Kind])
		for _, m := range sourceMessages(msgs) {
			from := m.SenderName
			if m.Mine {
				from = "我"
			}
			fmt.Fprintf(&b, "%s %s：%s\n", c.when(m.At), from, clip(oneLine(m.Text), lineCap))
		}
		c.box.mu.Lock()
		c.box.contexts[t.Thread] = threadContext{last: t.Last, text: b.String()}
		c.box.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
	}
}
