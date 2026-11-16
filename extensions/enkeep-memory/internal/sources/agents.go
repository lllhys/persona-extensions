package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona/sdk/go/wire"
)

// agentEntries lists the agent sessions with activity on the day (S3): not the assistant's own (its words are S1,
// through convsrc), never a health space one, not one started after the day.
func (s *Source) agentEntries(ctx context.Context, since, end time.Time) []wire.KBEntry {
	ss, err := s.Hub.Sessions(ctx, since)
	s.note(entry.SrcAgent, KindAgents, err)
	if err != nil {
		s.logf("agent sessions not read: %v", err)
		return nil
	}
	var out []wire.KBEntry
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, se := range ss {
		if se.Health || se.Assistant || (!se.Started.IsZero() && !se.Started.Before(end)) {
			continue
		}
		id := se.Machine + "/" + se.ID
		s.sessions[id] = se
		out = append(out, wire.KBEntry{Kind: KindAgents, ID: id, Name: sessionName(se, s.Machine), Updated: se.Updated, Source: "persona"})
	}
	return out
}

// cursorKey is the State.Cursors key of an agent session.
func cursorKey(id string) string { return entry.SrcAgent + "/" + id }

// folded is a session's folded page from the last seq read on: read once per pass (fold_work=1, after_seq), kept
// for the days after. A day before the one the cursor was read through (a rerun) reads the page from the start.
func (s *Source) folded(ctx context.Context, machine, session, day string) (*foldPage, error) {
	id := machine + "/" + session
	s.mu.Lock()
	page := s.folds[id]
	seq, through := cursorOf(s.cursors[cursorKey(id)])
	s.mu.Unlock()
	after := seq
	if through != "" && day <= through {
		after = 0 // read before: the items of that day are behind the cursor
	}
	if page != nil && page.base <= after {
		return page, nil
	}
	items, err := s.Hub.Items(ctx, machine, session, hubapi.ItemsQuery{Fold: true, After: after})
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(items)
	s.addBytes(entry.SrcAgent, KindAgents, len(raw))
	for _, it := range items {
		if it.Kind == hubapi.KindFold && it.Fold != nil && !it.Fold.Known {
			s.partial(entry.SrcAgent, KindAgents, "H3", "折叠的工作里还没有报错数和工具名（等 hub 的 H3），不顺信号只能靠文字和卡片")
			break
		}
	}
	page = &foldPage{base: after, items: items}
	s.mu.Lock()
	s.folds[id] = page
	s.mu.Unlock()
	return page, nil
}

// agentDay reads an agent session's day: the user's words ("我"), the agent's (head and tail), what Persona sent it,
// the session's cards with their answers, one line per stretch of work, and how the session ended.
func (s *Source) agentDay(ctx context.Context, id, day string) ([]string, error) {
	machine, session, ok := strings.Cut(id, "/")
	if !ok {
		return nil, nil
	}
	start, end, err := s.bounds(day)
	if err != nil {
		return nil, err
	}
	page, err := s.folded(ctx, machine, session, day)
	if err != nil {
		s.note(entry.SrcAgent, KindAgents, err)
		return nil, nil // a session that cannot be read now is left out, not the day
	}
	type line struct {
		at   time.Time
		text string
	}
	s.mu.Lock()
	se := s.sessions[id]
	s.mu.Unlock()
	var ls []line
	last := -1
	for i, it := range page.items {
		if it.At.Before(start) || !it.At.Before(end) {
			continue
		}
		if it.Seq > last {
			last = it.Seq
		}
		if it.Fold != nil && it.Fold.To > last {
			last = it.Fold.To // Fold.Seq is only the range's start: the next page must resume after Fold.To.
		}
		if t := s.itemLine(it, se.ByPersona && page.base == 0 && i == 0); t != "" {
			ls = append(ls, line{it.At, t})
		}
	}
	for _, c := range s.dayCards(ctx, day) {
		if c.Session == session && (c.Machine == "" || c.Machine == machine) {
			for _, l := range cardLines(c, s.Loc) {
				ls = append(ls, line{c.At, l})
			}
		}
	}
	if se.Ended != "" && !se.Updated.Before(start) && se.Updated.Before(end) {
		ls = append(ls, line{se.Updated, hhmm(se.Updated, s.Loc) + " " + SpeakerEnd + ": " + endText(se.Ended)})
	}
	sort.SliceStable(ls, func(i, j int) bool { return ls[i].at.Before(ls[j].at) })
	var out []string
	for _, l := range ls {
		out = append(out, l.text)
	}
	if last >= 0 {
		s.mu.Lock()
		s.pending[cursorKey(id)] = fmt.Sprintf("%d|%s", last, day)
		s.mu.Unlock()
	}
	if len(out) > 0 { // the bytes were counted when the folded page was read
		s.count(entry.SrcAgent, KindAgents, KindAgents+"/"+id, day, len(out), 0)
	}
	return out, nil
}

// itemLine is one item as a day's line, or "" for what is not read daily (slash commands, notifications).
// byPersona is set only for the first item of a Persona-started session read from the beginning.
func (s *Source) itemLine(it hubapi.Item, byPersona bool) string {
	at := hhmm(it.At, s.Loc)
	switch it.Kind {
	case hubapi.KindUser:
		speaker := SpeakerUser
		if byPersona {
			speaker = SpeakerPersona
		}
		return at + " " + speaker + ": " + oneLine(it.Text)
	case hubapi.KindAgent:
		return at + " " + SpeakerAgent + ": " + oneLine(headTail(it.Text, agentHead, agentTail))
	case hubapi.KindFold:
		if it.Fold == nil || it.Fold.Calls == 0 {
			return "" // thoughts only: nothing was done
		}
		return at + " " + SpeakerWork + ": " + foldText(it.Fold)
	case hubapi.KindNotice:
		switch {
		case it.Notice == "persona" || strings.HasPrefix(it.Notice, "injected:") || strings.HasPrefix(it.Notice, "source:"):
			return at + " " + SpeakerPersona + ": " + oneLine(clip(it.Text, noticeCut))
		case it.Notice == "paste":
			return at + " " + SpeakerPaste + ": " + oneLine(clip(it.Text, noticeCut))
		case it.Notice == "peer" || strings.HasPrefix(it.Notice, "from:"):
			return at + " " + SpeakerOther + ": " + oneLine(clip(it.Text, noticeCut))
		}
	}
	return ""
}

// foldText is a stretch of work in one line: calls, tool names and errors (when the hub gives them, H3; otherwise
// said to be unknown, never "0"), the files changed, and the seq range a dig can open.
func foldText(f *hubapi.Fold) string {
	if f == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d 次工具调用", f.Calls)
	if f.Known {
		if len(f.Tools) > 0 {
			fmt.Fprintf(&b, "（%s）", strings.Join(f.Tools, "、"))
		}
		fmt.Fprintf(&b, "，报错 %d 次", f.Errors)
	} else {
		b.WriteString("，报错数未知")
	}
	if len(f.Files) > 0 {
		files := f.Files
		more := ""
		if len(files) > 6 {
			more = fmt.Sprintf(" 等 %d 个", len(files))
			files = files[:6]
		}
		var short []string
		for _, p := range files {
			short = append(short, shortPath(p))
		}
		fmt.Fprintf(&b, "，改了 %s%s", strings.Join(short, "、"), more)
	}
	fmt.Fprintf(&b, " [seq %d-%d]", f.From, f.To)
	return b.String()
}

// shortPath keeps a path's last two parts: enough to say what was changed, without the home directory.
func shortPath(p string) string {
	parts := strings.Split(strings.TrimRight(p, "/"), "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "/")
}

func endText(e string) string {
	switch e {
	case "interrupted":
		return "会话被打断"
	case "timeout":
		return "会话超时"
	case "normal":
		return "会话正常结束"
	}
	return e
}
