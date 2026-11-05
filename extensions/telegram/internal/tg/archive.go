package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/kb"
)

// The knowledge base's reading (connector.Archive): one cursor for the account, which holds, for each conversation,
// the newest message it archived. Every round lists the conversations and reads the ones with something newer; a
// conversation read the first time goes back firstDays, at most firstMax messages (the user's choice, 2026-11-03). One
// read since the cursor also takes at most firstMax, the oldest: the rest comes in the next call.
var (
	firstDays    = 30 * 24 * time.Hour
	firstMax     = 500
	dialogsLimit = 200             // conversations listed per round, per folder: newest first, so what moved is at the top
	newsSettle   = 3 * time.Second // Telegram's updates come in bursts: one kick for each
	// What Telegram's news sets off (the archive's kick, the inbox's refresh) goes in rounds, at most one every
	// newsEvery: an account in busy groups is told of something new every few seconds, and the inbox lists the
	// conversations, which Telegram limits tightly (messages.getDialogs). The gap doubles after a flood wait, up to
	// newsEveryMost, and halves back after each round without one (#1024).
	newsEvery     = time.Minute
	newsEveryMost = 16 * time.Minute
	inboxDialogs  = 100 // conversations the inbox lists per folder: plenty for the last inboxDays
)

type archiveCursor struct {
	Seen map[string]int `json:"seen"` // peer key -> the newest message archived
}

// client is the connected client, or nil.
func (c *Conn) client() client {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cl
}

var errNotConnected = errors.New("not connected to Telegram")

// Changes gives the conversations with messages newer than the cursor, at most limit of them. The gate paces every
// request (gate.go). A Telegram error part way ends the round: what was read (and the cursor up to it) goes to the
// hub, the rest waits for the next round; during a flood wait it asks nothing.
func (c *Conn) Changes(ctx context.Context, cursor string, limit int) ([]connector.ChatBatch, string, bool, error) {
	cl := c.client()
	if cl == nil {
		return nil, cursor, false, errNotConnected
	}
	if left := c.gate.left(); left > 0 {
		return nil, cursor, false, heldError{left}
	}
	var cur archiveCursor
	if cursor != "" {
		if err := json.Unmarshal([]byte(cursor), &cur); err != nil {
			return nil, cursor, false, fmt.Errorf("the archive's cursor does not read: %w", err)
		}
	}
	if cur.Seen == nil {
		cur.Seen = map[string]int{}
	}
	ds, err := cl.Dialogs(ctx, dialogsLimit)
	if err != nil {
		return nil, cursor, false, scrubbed(err)
	}
	c.noteDialogs(ds) // the inbox reads the same list
	var batches []connector.ChatBatch
	more := false
	for _, d := range ds {
		key := d.Peer.key()
		if !d.kept() || d.Top <= cur.Seen[key] {
			continue
		}
		if len(batches) >= limit {
			more = true
			break
		}
		seen := cur.Seen[key]
		var since time.Time
		if seen == 0 {
			since = time.Now().Add(-firstDays)
		}
		msgs, err := cl.History(ctx, d, seen, since, firstMax)
		if err != nil {
			log.Printf("archive: %s: %s", key, explain(err))
			if len(batches) == 0 {
				return nil, cursor, false, scrubbed(err)
			}
			break // keep what was read; the rest next round
		}
		top := seen
		for _, m := range msgs {
			top = max(top, m.ID)
		}
		if seen > 0 && len(msgs) >= firstMax {
			// More came since the last round than one read takes: the cursor goes only as far as what was read, and
			// the next call goes on from there (#78).
			cur.Seen[key] = top
			more = true
		} else {
			cur.Seen[key] = max(top, d.Top)
		}
		if len(msgs) > 0 {
			batches = append(batches, connector.ChatBatch{Chat: kb.SourceChat{ID: key, Name: d.Name, Kind: d.Kind, UpdatedAt: d.TopAt},
				Messages: sourceMessages(msgs)})
		}
		if ctx.Err() != nil {
			return batches, encodeCursor(cur), true, nil
		}
	}
	c.mu.Lock()
	c.lastSync = time.Now()
	c.mu.Unlock()
	return batches, encodeCursor(cur), more, nil
}

func encodeCursor(cur archiveCursor) string {
	b, _ := json.Marshal(cur)
	return string(b)
}

// sourceMessages turns messages into the knowledge base's, a reply's words naming what it answers when that is in
// the same batch.
func sourceMessages(msgs []message) []kb.SourceMessage {
	text := map[int]string{}
	for _, m := range msgs {
		text[m.ID] = m.Text
	}
	out := make([]kb.SourceMessage, 0, len(msgs))
	for _, m := range msgs {
		t := m.Text
		if m.ReplyTo != 0 {
			if quoted, ok := text[m.ReplyTo]; ok && quoted != "" {
				t = "回复「" + clip(quoted, 30) + "」：" + t
			} else {
				t = "回复一条消息：" + t
			}
		}
		sm := kb.SourceMessage{ID: strconv.Itoa(m.ID), SenderID: m.FromKey, SenderName: m.From, Mine: m.Out, Type: m.Type, Text: t, At: m.At}
		if m.FromKey != "" {
			sm.SenderHandles = []kb.Handle{{Kind: kb.HandleTelegram, Value: m.FromKey}}
		}
		out = append(out, sm)
	}
	return out
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// updated is Telegram telling of something new: Watch kicks the hub once the burst settles.
func (c *Conn) updated() {
	select {
	case c.news <- struct{}{}:
	default:
	}
}

// Watch kicks the hub's archive and inbox when Telegram tells of something new (connector.Events): in rounds, at most
// one a gap (newsEvery…), whatever came meanwhile going into the next round.
func (c *Conn) Watch(ctx context.Context, changed func(connector.Scope)) error {
	gap := newsEvery
	var last time.Time // when the last round started
	_, _, seen := c.gate.flood()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-c.news:
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(max(newsSettle, time.Until(last.Add(gap)))):
		}
		c.drainNews() // what came meanwhile is in this round
		last = time.Now()
		changed(connector.ScopeChats) // the hub archives when it is due
		c.refreshInbox(ctx)           // the inbox answers from memory: bring it up to date first
		changed(connector.ScopeInbox)
		_, _, until := c.gate.flood()
		gap = newsGap(gap, until != seen) // any flood wait since the last round: Telegram's limit is the account's
		seen = until
	}
}

func (c *Conn) drainNews() {
	select {
	case <-c.news:
	default:
	}
}

// newsGap is the next gap between the rounds: doubled after a flood wait, halved back toward newsEvery after a round
// without one.
func newsGap(gap time.Duration, flooded bool) time.Duration {
	if flooded {
		return min(gap*2, newsEveryMost)
	}
	return max(gap/2, newsEvery)
}
