package sources

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
)

// Segment reads one stretch of a session unfolded, from seq through seq: its tool calls and results, thoughts and
// text, for a dig a friction signal pointed at (3.1, P5). It is the only way tool calls and results are read. The
// bytes read are counted under S4. A health space session is never read (the hub client drops it).
func (s *Source) Segment(ctx context.Context, ref string, from, through int) ([]hubapi.Item, error) {
	machine, session, ok := strings.Cut(strings.TrimPrefix(ref, KindAgents+"/"), "/")
	if !ok || through < from {
		return nil, nil
	}
	items, n, err := Segment(ctx, s.Hub, machine, session, from, through)
	if err != nil {
		s.note(entry.SrcTools, "segments", err)
		return nil, err
	}
	s.mu.Lock()
	st := s.statusOf(entry.SrcTools, "segments")
	st.Available, st.Missing = true, ""
	st.Units++
	st.Lines += len(items)
	st.Bytes += n
	s.mu.Unlock()
	return items, nil
}

// Segment reads [from, through] of a session unfolded through any hub, and says how many bytes that was.
func Segment(ctx context.Context, h hubapi.Hub, machine, session string, from, through int) ([]hubapi.Item, int, error) {
	items, err := h.Items(ctx, machine, session, hubapi.ItemsQuery{From: from, Through: through})
	if err != nil {
		return nil, 0, err
	}
	var out []hubapi.Item
	for _, it := range items {
		if it.Seq >= from && it.Seq <= through {
			out = append(out, it)
		}
	}
	raw, _ := json.Marshal(out)
	return out, len(raw), nil
}

// ToolCall is one tool call and their results of a segment, paired: what P5 reads to tell a pitfall from a normal try.
type ToolCall struct {
	Seq    int
	Tool   string
	Input  string
	Result string
	Err    bool
	done   bool
}

// Calls pairs a segment's tool calls with their results (by tool_use id; a result without one goes to the last call
// still open), in order.
func Calls(items []hubapi.Item) []ToolCall {
	var out []ToolCall
	byCall := map[string]int{}
	last := -1
	for _, it := range items {
		switch it.Kind {
		case hubapi.KindToolUse:
			out = append(out, ToolCall{Seq: it.Seq, Tool: it.Tool, Input: it.Text})
			last = len(out) - 1
			if it.Call != "" {
				byCall[it.Call] = last
			}
		case hubapi.KindToolResult:
			i, ok := byCall[it.Call]
			if !ok || it.Call == "" {
				i = last
			}
			if i >= 0 && out[i].Result == "" && !out[i].done {
				out[i].Result, out[i].Err, out[i].done = it.Text, it.Err, true
			}
		}
	}
	return out
}
