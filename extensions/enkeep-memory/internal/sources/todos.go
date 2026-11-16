package sources

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona/sdk/go/wire"
)

// todoEntries lists the to-dos touched since the day began (S5); todoDay says which of them had something that day.
func (s *Source) todoEntries(ctx context.Context, since, end time.Time) []wire.KBEntry {
	ts, err := s.Hub.Todos(ctx, since)
	s.note(entry.SrcTodo, KindTodos, err)
	if err != nil {
		s.logf("to-dos not read: %v", err)
		return nil
	}
	raw, _ := json.Marshal(ts)
	s.addBytes(entry.SrcTodo, KindTodos, len(raw))
	var out []wire.KBEntry
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range ts {
		if !t.Created.IsZero() && !t.Created.Before(end) {
			continue
		}
		s.todos[t.ID] = t
		out = append(out, wire.KBEntry{Kind: KindTodos, ID: t.ID, Name: "待办 · " + clip(t.Title, 30), Updated: t.Updated, Source: "persona"})
	}
	return out
}

// todoDay reads a to-do's day: made that day, the decisions written that day (with where they came from, and
// whether a later one replaced them), and how it ended (done, cancelled, with the reason) or that it was reopened.
func (s *Source) todoDay(ctx context.Context, id, day string) ([]string, error) {
	start, end, err := s.bounds(day)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	t, ok := s.todos[id]
	ds, read := s.decided[id]
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	if !read {
		ds, err = s.Hub.TodoDecisions(ctx, id)
		if err != nil {
			s.note(entry.SrcTodo, KindTodos, err)
			ds = nil
		}
		raw, _ := json.Marshal(ds)
		s.addBytes(entry.SrcTodo, KindTodos, len(raw))
		s.mu.Lock()
		s.decided[id] = ds
		s.mu.Unlock()
	}
	in := func(at time.Time) bool { return !at.IsZero() && !at.Before(start) && at.Before(end) }
	var out []string
	if in(t.Created) {
		l := hhmm(t.Created, s.Loc) + " " + SpeakerTodo + ": 新建「" + oneLine(clip(t.Title, 200)) + "」"
		if t.Project != "" {
			l += "（项目 " + t.Project + "）"
		}
		out = append(out, l)
	}
	for _, d := range ds {
		if !in(d.At) {
			continue
		}
		l := hhmm(d.At, s.Loc) + " " + SpeakerDecide
		if d.By != "" {
			l += "（" + d.By + "）"
		}
		l += ": " + oneLine(clip(d.Text, 600))
		if d.Replaced {
			l += "（后来被改了）"
		}
		out = append(out, l)
	}
	switch {
	case in(t.Closed) && t.Outcome == "done":
		l := hhmm(t.Closed, s.Loc) + " " + SpeakerEnd + ": 完成「" + oneLine(clip(t.Title, 100)) + "」"
		if t.Reason != "" {
			l += "：" + oneLine(clip(t.Reason, 300))
		}
		out = append(out, l)
	case in(t.Closed) && t.Outcome == "cancelled":
		l := hhmm(t.Closed, s.Loc) + " " + SpeakerEnd + ": 取消「" + oneLine(clip(t.Title, 100)) + "」"
		if t.Reason != "" {
			l += "：" + oneLine(clip(t.Reason, 300))
		}
		out = append(out, l)
	case t.Reopened > 0 && in(t.Updated) && t.Closed.IsZero():
		out = append(out, hhmm(t.Updated, s.Loc)+" "+SpeakerEnd+": 重开「"+oneLine(clip(t.Title, 100))+"」")
	}
	if len(out) > 0 {
		s.count(entry.SrcTodo, KindTodos, KindTodos+"/"+id, day, len(out), 0)
	}
	return out, nil
}
