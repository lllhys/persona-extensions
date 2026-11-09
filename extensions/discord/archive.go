package main

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The knowledge base's archive (archive.changes), over REST. One conversation per channel, thread or forum post,
// discord-<channel id>. Each pass checks that Discord shows the bot what messages say, looks at what the bot can
// read (discovery and, now and then, each channel's archived threads), and reads the conversations whose last
// message moved past the cursor: history_days back the first time, what came after since then (never from before
// the window).
//
// The cursor, which the hub keeps, holds everything that decides what is read: a position only moves past messages
// that were read, a channel's archived threads count as looked at only once every one of them found was read, and the
// knowledge base drops a message id it already has, so reading something again is harmless. A call the hub does not
// keep (it could not store what it read) leaves the old cursor, and the next call reads the same again. The only
// thing kept in memory is which conversations to leave alone for a while; losing it repeats some requests.

const (
	backfillMost = 500 // messages read back the first time a conversation is seen
	pageSize     = 100 // what GET /channels/{id}/messages answers at most
	afterPages   = 5   // pages of new messages per conversation and call; more waits for the next call
	pruneAfter   = 7 * 24 * time.Hour
	retryFailed  = 15 * time.Minute // a conversation whose read failed is tried again after this, or on its next message
)

// cursor is the archive's position: per conversation the last message archived; per channel when its archived
// threads were last listed completely, and how far a listing going on has got.
type cursor struct {
	V    int                `json:"v"`
	Ch   map[string]string  `json:"ch,omitempty"`
	Seen map[string]int64   `json:"seen,omitempty"`
	List map[string]listPos `json:"list,omitempty"`
	Owe  map[string]owed    `json:"owe,omitempty"`
}

// owed is an archived thread a listing found and went past without reading it, because it failed or was held back
// for a while: it is read on its own, so one such thread never stops its channel's listing.
type owed struct {
	Last   string `json:"l"` // its last message
	Parent string `json:"p"`
	Name   string `json:"n"`
}

// listPos is a channel's archived-thread listing going on, a page per call: when it began (the channel's Seen once
// it is done), and how far back it has got (unix ms of an archive time; 0: from the newest), every thread archived
// since then read or needing nothing.
type listPos struct {
	Start int64 `json:"s"`
	Upto  int64 `json:"u,omitempty"`
}

// readCursor reads the hub's cursor; one that cannot be read starts over (ok false).
func readCursor(s string) (cur cursor, ok bool) {
	if s != "" && (json.Unmarshal([]byte(s), &cur) != nil || cur.V != 1) {
		cur, ok = cursor{}, false
	} else {
		ok = true
	}
	cur.V = 1
	if cur.Ch == nil {
		cur.Ch = map[string]string{}
	}
	if cur.Seen == nil {
		cur.Seen = map[string]int64{}
	}
	if cur.List == nil {
		cur.List = map[string]listPos{}
	}
	if cur.Owe == nil {
		cur.Owe = map[string]owed{}
	}
	return cur, ok
}

func (cur cursor) String() string {
	raw, _ := json.Marshal(cur)
	return string(raw)
}

// entry is a conversation's position (0: none).
func (cur cursor) entry(id snowflake) snowflake {
	e, _ := parseSnowflake(cur.Ch[id.String()])
	return e
}

func (cur cursor) set(id, last snowflake) { cur.Ch[id.String()] = last.String() }

// snowflakeAt is the smallest id of a moment: "after it" is "from then on".
func snowflakeAt(t time.Time) snowflake {
	ms := t.UnixMilli() - discordEpoch
	if ms <= 0 {
		return 1
	}
	return snowflake(uint64(ms) << 22)
}

// listing is one page of a channel's archived threads, as listed in this call.
type listing struct {
	parent  view
	pos     listPos
	threads []view // archived since the listing's start point, newest archived first
	end     bool   // the page reached that point, or was the last
}

// archivedAt is when an archived thread was archived.
func archivedAt(v view) time.Time {
	if v.ThreadMetadata == nil {
		return time.Time{}
	}
	return v.ThreadMetadata.ArchiveTimestamp
}

// changes is archive.changes: the conversations with something new since the cursor, at most limit of them, read in
// about c.budget and c.bytesMost (at least one a call, so every call makes progress); more says there is more to read
// now.
func (c *conn) changes(ctx context.Context, p wire.ConnectorChangesParams) (wire.ConnectorChangesResult, error) {
	s := c.session()
	if s == nil {
		return wire.ConnectorChangesResult{}, c.notSignedIn()
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, c.budget+30*time.Second)
	defer cancel()
	cur, ok := readCursor(p.Cursor)
	if !ok {
		c.logf("discord: the archive's cursor is not readable: starting over (the knowledge base drops what it has)")
	}
	// Without the Message Content intent Discord sends every message empty: read nothing, move nothing; the health
	// check, asked to look now, says so in the status.
	app, err := s.api.application(ctx)
	if err != nil {
		return wire.ConnectorChangesResult{}, c.callError(s, err)
	}
	if !app.contentIntent() {
		s.checkNow()
		return wire.ConnectorChangesResult{Next: p.Cursor}, nil
	}
	d, err := s.discover(ctx)
	if err != nil {
		return wire.ConnectorChangesResult{}, c.callError(s, err)
	}
	now := time.Now()
	cutoff := now.AddDate(0, 0, -c.cfg.historyDays())
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}
	listed, found := c.listArchived(ctx, s, d, cur, cutoff, start)
	owedViews(cur, d, found)

	var res wire.ConnectorChangesResult
	var stop error
	refused := map[snowflake]bool{}
	size := 0
	for i, v := range c.archiveOrder(s, d, found, cur) {
		if i > 0 && (len(res.Batches) >= limit || size > c.bytesMost || time.Since(start) > c.budget) {
			res.More = true
			break
		}
		entry := cur.entry(v.ID)
		var msgs []message
		var last snowflake
		var more bool
		switch {
		case entry == 0:
			msgs, last, err = c.readBack(ctx, s, v.ID, cutoff) // first seen: the window, at most backfillMost
		case entry.Time().Before(cutoff):
			msgs, last, more, err = c.readAfter(ctx, s, v.ID, snowflakeAt(cutoff)) // a gap longer than the window
		default:
			msgs, last, more, err = c.readAfter(ctx, s, v.ID, entry)
		}
		_, isFound := found[v.ID]
		switch {
		case last != 0:
			cur.set(v.ID, last)
			if b := c.batch(ctx, s, d, v, msgs); len(b.Messages) > 0 {
				res.Batches = append(res.Batches, b)
				size += batchSize(b)
			}
		case err == nil && isFound:
			// An archived thread from a listing that needs Read Message History: showing nothing means its messages
			// were deleted (or are all older than the window).
			cur.set(v.ID, v.LastMessageID)
		case err == nil:
			// It says it has messages but shows none: no Read Message History, its newest messages deleted, or none
			// within the window. Its position stays, so it is read once allowed; tried again on its next message, or
			// after a while.
			s.hold(v, c.holdFor, false)
		}
		res.More = res.More || more
		if err == nil {
			continue
		}
		e, _ := asAPIError(err)
		switch {
		case statusOf(err) == 403 || statusOf(err) == 404:
			s.hold(v, c.holdFor, true) // refused, or gone: refusals count toward an IP ban
			refused[v.ID] = true
		case local(err):
			wait := retryFailed
			if e != nil && e.Status == 429 {
				wait = e.RetryAfter
			}
			s.hold(v, wait, false) // about this conversation only: the others go on
			c.logf("discord: reading conversation %s: %s", v.ID, clip(oneLine(err.Error()), 200))
		default:
			stop = err // the network, the token or a global rate limit: every other request would fail too
			c.refused(s, err)
		}
		if stop != nil {
			break
		}
	}
	if stop != nil && len(res.Batches) == 0 {
		return wire.ConnectorChangesResult{}, c.callError(s, stop)
	}
	// A listing moves back past the threads that were read or need nothing, and past those held back (failed, refused,
	// or showing nothing for now), which it leaves owed; it stops at the first one this call did not get to, and the
	// next call lists from there. It is done once it reaches its start point.
	keep := map[snowflake]bool{}
	for _, l := range listed {
		key, pos, done, walked := l.parent.ID.String(), l.pos, l.end, true
		for _, t := range l.threads {
			keep[t.ID] = true
			if _, active := d.views[t.ID]; !active && t.LastMessageID > cur.entry(t.ID) {
				if !s.heldNow(t) {
					done, walked = false, false
					break
				}
				cur.Owe[t.ID.String()] = owed{Last: t.LastMessageID.String(), Parent: l.parent.ID.String(), Name: t.Name}
			}
			pos.Upto = archivedAt(t).UnixMilli()
		}
		switch {
		case done:
			cur.Seen[key] = pos.Start
			delete(cur.List, key)
		case walked && pos.Upto == l.pos.Upto && len(l.threads) > 0:
			// A whole page archived at the very moment it went on from: step past it, or it would be listed forever.
			pos.Upto--
			cur.List[key] = pos
		default:
			cur.List[key] = pos
		}
	}
	for id := range found {
		keep[id] = true
	}
	settleOwed(cur, cutoff)
	prune(cur, d, keep, cutoff)
	res.Next = cur.String()
	if stop == nil {
		c.mu.Lock()
		c.lastSync = now
		c.mu.Unlock()
	}
	return res, nil
}

// batchSize is about how many bytes a batch takes on the wire.
func batchSize(b wire.ConnectorChatBatch) int {
	n := 200
	for _, m := range b.Messages {
		n += len(m.Text) + len(m.SenderName) + 120
	}
	return n
}

// callError is a failed call's error for the hub: its words (never a token or a body), a rate limit with its wait,
// and a refused token noted.
func (c *conn) callError(s *session, err error) error {
	c.refused(s, err)
	e := &ext.Error{Message: err.Error()}
	if ae, ok := asAPIError(err); ok && ae.Status == 429 {
		e.Code = wire.ConnectorErrorCodeRateLimited
		e.Data, _ = json.Marshal(map[string]int{"retry_after": seconds(ae.RetryAfter)})
	}
	return e
}

// archiveOrder is what to read: those whose last message moved past the cursor and are not held back for now. Those
// read before (furthest behind first) and those never read (the most recently active first) take turns, so neither a
// busy server nor a newly joined one makes the other wait.
func (c *conn) archiveOrder(s *session, d *discovery, found map[snowflake]view, cur cursor) []view {
	var seen, fresh []view
	add := func(v view) {
		entry := cur.entry(v.ID)
		switch {
		case v.LastMessageID == 0 || v.LastMessageID <= entry || s.heldNow(v):
		case entry == 0:
			fresh = append(fresh, v)
		default:
			seen = append(seen, v)
		}
	}
	for _, v := range d.views {
		add(v)
	}
	for _, v := range found {
		add(v)
	}
	slices.SortFunc(seen, func(a, b view) int {
		if x := cmpID(cur.entry(a.ID), cur.entry(b.ID)); x != 0 {
			return x
		}
		return cmpID(b.LastMessageID, a.LastMessageID)
	})
	slices.SortFunc(fresh, func(a, b view) int { return cmpID(b.LastMessageID, a.LastMessageID) })
	out := make([]view, 0, len(seen)+len(fresh))
	for i := 0; i < len(seen) || i < len(fresh); i++ {
		if i < len(seen) {
			out = append(out, seen[i])
		}
		if i < len(fresh) {
			out = append(out, fresh[i])
		}
	}
	return out
}

// readAfter reads the messages after after, oldest first, a page at a time (afterPages at most): what it read, the
// newest id read (0: none), and whether there is more. What was read before an error is answered with the error.
func (c *conn) readAfter(ctx context.Context, s *session, ch, after snowflake) ([]message, snowflake, bool, error) {
	var out []message
	var last snowflake
	anchor := after
	for range afterPages {
		p, err := s.api.messagesAfter(ctx, ch, anchor, pageSize)
		if err != nil {
			return out, last, false, err
		}
		slices.SortFunc(p.msgs, func(a, b message) int { return cmpID(a.ID, b.ID) })
		for _, m := range p.msgs {
			if m.ID > anchor {
				out = append(out, m)
				last = m.ID
			}
		}
		if p.newest > anchor {
			anchor = p.newest // past a message that could not be read, too
		}
		if p.n < pageSize {
			if anchor > after {
				last = anchor
			}
			return out, last, false, nil
		}
	}
	if anchor > after {
		last = anchor
	}
	return out, last, true, nil
}

// readBack reads a conversation the first time: back from its newest message to cutoff, backfillMost at most,
// oldest first; and the newest id there is (0: none shown). All or nothing: an error reads it again next time.
func (c *conn) readBack(ctx context.Context, s *session, ch snowflake, cutoff time.Time) ([]message, snowflake, error) {
	var kept []message
	var newest, before snowflake
	for len(kept) < backfillMost {
		p, err := s.api.messagesBefore(ctx, ch, before, pageSize)
		if err != nil {
			return nil, 0, err
		}
		if p.n == 0 {
			break
		}
		newest = max(newest, p.newest)
		slices.SortFunc(p.msgs, func(a, b message) int { return cmpID(b.ID, a.ID) })
		done := p.n < pageSize
		for _, m := range p.msgs {
			if m.Timestamp.Before(cutoff) || len(kept) == backfillMost {
				done = true
				break
			}
			kept = append(kept, m)
		}
		if done || p.oldest == 0 {
			break
		}
		before = p.oldest // past a message that could not be read, too
	}
	slices.Reverse(kept)
	return kept, newest, nil
}

// listArchived lists a page of archived threads of each channel that is due: one whose listing is going on, or whose
// last complete listing is older than c.scanEvery (or never happened: a server the bot joined later too); those
// listed longest ago first, at least one a call and then within half the budget. A listing goes back to its start
// point: the window the first time, an hour before the last complete listing after that. It answers the pages and the
// threads found with something to read. A channel that cannot be listed now is listed again next call; the others
// go on.
func (c *conn) listArchived(ctx context.Context, s *session, d *discovery, cur cursor, cutoff, start time.Time) ([]listing, map[snowflake]view) {
	now := time.Now()
	var due []listing
	for _, p := range d.parents {
		key := p.ID.String()
		pos, going := cur.List[key]
		if seen := cur.Seen[key]; !going && seen != 0 && now.Sub(time.Unix(seen, 0)) < c.scanEvery || s.refusedNow(p) {
			continue
		}
		if !going {
			pos = listPos{Start: now.Unix()}
		}
		due = append(due, listing{parent: p, pos: pos})
	}
	slices.SortStableFunc(due, func(a, b listing) int {
		x, y := cur.Seen[a.parent.ID.String()], cur.Seen[b.parent.ID.String()]
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	})
	var out []listing
	found := map[snowflake]view{}
	for i, l := range due {
		if i > 0 && time.Since(start) > c.budget/2 {
			break
		}
		p := l.parent
		since := cutoff
		if seen := cur.Seen[p.ID.String()]; seen != 0 {
			if t := time.Unix(seen, 0).Add(-time.Hour); t.After(cutoff) {
				since = t
			}
		}
		var before time.Time
		if l.pos.Upto != 0 {
			before = time.UnixMilli(l.pos.Upto + 1) // the last one again: one archived at the same moment is not skipped
		}
		threads, more, err := s.api.archivedThreads(ctx, p.ID, before)
		if err != nil {
			switch {
			case statusOf(err) == 403 || statusOf(err) == 404:
				s.hold(p, c.holdFor, true)
			case local(err):
				c.logf("discord: listing the archived threads of %s: %s", p.ID, clip(oneLine(err.Error()), 200))
			default:
				return out, found // the network, the token or a global limit: the reads will say it
			}
			continue
		}
		l.end = !more
		for _, t := range threads {
			t.GuildID = p.GuildID
			v := view{channel: t, guildName: p.guildName, parentName: p.Name}
			if archivedAt(v).Before(since) {
				l.end = true
				break
			}
			l.threads = append(l.threads, v)
			if _, active := d.views[t.ID]; !active && t.LastMessageID > cur.entry(t.ID) {
				found[t.ID] = v
			}
		}
		out = append(out, l)
	}
	return out, found
}

// owedViews adds the owed threads to found, as views, when their channel is in this discovery (and they are not
// active again: then discovery has them).
func owedViews(cur cursor, d *discovery, found map[snowflake]view) {
	parents := map[snowflake]view{}
	for _, p := range d.parents {
		parents[p.ID] = p
	}
	for key, o := range cur.Owe {
		id, ok := parseSnowflake(key)
		pid, _ := parseSnowflake(o.Parent)
		last, _ := parseSnowflake(o.Last)
		p, there := parents[pid]
		if _, active := d.views[id]; !ok || !there || active {
			continue
		}
		if _, listed := found[id]; listed {
			continue
		}
		found[id] = view{channel: channel{ID: id, Type: chPublicThread, GuildID: p.GuildID, Name: o.Name, ParentID: pid, LastMessageID: last,
			ThreadMetadata: &threadMeta{Archived: true}}, guildName: p.guildName, parentName: p.Name}
	}
}

// settleOwed forgets the owed threads that have been read, or whose last message left the window.
func settleOwed(cur cursor, cutoff time.Time) {
	for key, o := range cur.Owe {
		id, ok := parseSnowflake(key)
		last, ok2 := parseSnowflake(o.Last)
		if !ok || !ok2 || cur.entry(id) >= last || last.Time().Before(cutoff) {
			delete(cur.Owe, key)
		}
	}
}

// prune drops what the cursor keeps about conversations and channels that are neither in this discovery nor in this
// call's listings, once well older than the window: it does not grow forever with old forum posts. One that comes back is
// read like a new one (its window only), and the knowledge base drops what it already has.
func prune(cur cursor, d *discovery, keep map[snowflake]bool, cutoff time.Time) {
	old := cutoff.Add(-pruneAfter)
	for key, from := range cur.Ch {
		id, ok := parseSnowflake(key)
		last, ok2 := parseSnowflake(from)
		if !ok || !ok2 {
			delete(cur.Ch, key)
			continue
		}
		if _, there := d.views[id]; !there && !keep[id] && last.Time().Before(old) {
			delete(cur.Ch, key)
		}
	}
	parents := map[snowflake]bool{}
	for _, p := range d.parents {
		parents[p.ID] = true
	}
	for key, at := range cur.Seen {
		id, ok := parseSnowflake(key)
		if !ok || !parents[id] && time.Unix(at, 0).Before(old) {
			delete(cur.Seen, key)
		}
	}
	for key, l := range cur.List {
		id, ok := parseSnowflake(key)
		if !ok || !parents[id] && time.Unix(l.Start, 0).Before(old) {
			delete(cur.List, key)
		}
	}
}

// batch is one conversation's messages as the archive takes them. Senders are named, never identified: no sender
// id and no handles, so the knowledge base keeps no people from Discord (Discord's developer policy forbids
// profiling its users).
func (c *conn) batch(ctx context.Context, s *session, d *discovery, v view, msgs []message) wire.ConnectorChatBatch {
	chat := &wire.ConnectorArchiveChat{ID: v.ID.String(), Name: v.chatName(c.tx.nameSep), Kind: "group"}
	var out []wire.ConnectorArchiveMessage
	for _, m := range msgs {
		text, kind := c.render(ctx, c.tx, s, d, v.GuildID, m)
		if text == "" {
			continue
		}
		out = append(out, wire.ConnectorArchiveMessage{ID: m.ID.String(), SenderName: m.Author.name(),
			Mine: s.owner != 0 && m.Author.ID == s.owner, Type: kind, Text: text, At: m.Timestamp.UTC()})
	}
	if len(out) > 0 {
		chat.UpdatedAt = out[len(out)-1].At
	}
	return wire.ConnectorChatBatch{Chat: chat, Messages: out}
}
