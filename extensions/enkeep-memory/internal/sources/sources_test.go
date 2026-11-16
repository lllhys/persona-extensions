package sources

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona/sdk/go/wire"
)

// kb is a knowledge base with only what a test puts in it (recordings).
type kb struct {
	entries []wire.KBEntry
	meta    map[string]map[string]any
}

func (k *kb) List(context.Context, string) ([]wire.KBEntry, error) { return nil, nil }
func (k *kb) Get(_ context.Context, kind, id string) (wire.KBDetail, error) {
	m, ok := k.meta[id]
	if !ok {
		return wire.KBDetail{}, kbsrc.ErrNotFound
	}
	raw, _ := json.Marshal(m)
	return wire.KBDetail{Kind: kind, ID: id, Meta: raw, Summary: "a recording summary"}, nil
}
func (k *kb) Day(context.Context, string, string) (wire.KBDayResult, error) {
	return wire.KBDayResult{}, kbsrc.ErrNotFound
}
func (k *kb) Recent(context.Context, time.Time, time.Time) ([]wire.KBEntry, error) {
	return append([]wire.KBEntry(nil), k.entries...), nil
}
func (k *kb) Update(context.Context, wire.KBUpdateParams) (map[string]json.RawMessage, error) {
	return nil, nil
}
func (k *kb) Owner(context.Context) (wire.KBOwner, error) { return wire.KBOwner{}, nil }

var day3 = time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)

func at(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, time.UTC) }

// hub is a hub with placeholder sessions, cards, to-dos, "about me" and memory, recording the items queries.
type hub struct {
	hubapi.None
	queries     []hubapi.ItemsQuery
	items       []hubapi.Item
	sessions    []hubapi.Session
	memoryFiles []hubapi.MemoryFile
}

func (h *hub) Sessions(context.Context, time.Time) ([]hubapi.Session, error) {
	if h.sessions != nil {
		return h.sessions, nil
	}
	return []hubapi.Session{
		{Machine: "m1", ID: "s1", Title: "project-x", Started: at(3, 8), Updated: at(4, 12)},
		{Machine: "m1", ID: "s-health", Title: "health", Started: at(3, 8), Updated: at(4, 12), Assistant: true, Health: true},
		{Machine: "m1", ID: "s-main", Title: "main", Started: at(3, 8), Updated: at(4, 12), Assistant: true},
	}, nil
}

func (h *hub) Items(_ context.Context, machine, session string, q hubapi.ItemsQuery) ([]hubapi.Item, error) {
	h.queries = append(h.queries, q)
	var out []hubapi.Item
	for _, it := range h.items {
		if q.Through > 0 {
			if it.Seq >= q.From && it.Seq <= q.Through {
				out = append(out, it)
			}
		} else if it.Seq > q.After {
			out = append(out, it)
		}
	}
	if q.Fold && q.Through == 0 {
		out = hubapi.FoldWork(out, false)
	}
	return out, nil
}

func (h *hub) Interactions(_ context.Context, q hubapi.InteractionsQuery) ([]hubapi.Interaction, error) {
	all := []hubapi.Interaction{
		{ID: "c1", Machine: "m1", Session: "s1", At: at(3, 10), Question: "which port?", Options: []string{"8080", "9090"}, Answers: []string{"9090"}, AnsweredBy: "user"},
		{ID: "c2", Machine: "m1", Session: "s-main", At: at(3, 11), Question: "send it?", Answers: []string{"yes"}, AnsweredBy: "user"},
		{ID: "c3", Machine: "m1", Session: "s1", At: at(3, 12), Question: "a private one", Private: true},
	}
	var out []hubapi.Interaction
	for _, c := range all {
		if !c.At.Before(q.Since) && (q.Before.IsZero() || c.At.Before(q.Before)) {
			out = append(out, c)
		}
	}
	return out, &hubapi.WaitingHub{Change: "H3"}
}

func (h *hub) Todos(context.Context, time.Time) ([]hubapi.Todo, error) {
	return []hubapi.Todo{{ID: "t1", Title: "ship project-x", Created: at(3, 9), Updated: at(3, 18), Closed: at(3, 18), Done: true, Outcome: "done", Reason: "merged"}}, nil
}

func (h *hub) TodoDecisions(context.Context, string) ([]hubapi.Decision, error) {
	return []hubapi.Decision{hubapi.NewDecision(at(3, 12), "card", "which branch?", "main", "small", false)}, nil
}

func (h *hub) AboutMe(context.Context) (string, error) {
	return "I work on project-x.\nI prefer short answers.", nil
}

func (h *hub) Memories(context.Context, bool) ([]hubapi.MemoryFile, error) {
	if h.memoryFiles != nil {
		return h.memoryFiles, nil
	}
	return []hubapi.MemoryFile{
		{Name: "port.md", Description: "port of machine-a", Type: "reference", Created: at(3, 7), Modified: at(3, 7), Content: "machine-a uses 9090"},
		{Name: "later.md", Description: "later", Created: at(5, 7), Modified: at(5, 7), Content: "not yet"},
	}, nil
}

func sessionItems() []hubapi.Item {
	return []hubapi.Item{
		{Seq: 1, At: at(3, 9), Kind: hubapi.KindUser, Text: "build project-x"},
		{Seq: 2, At: at(3, 9), Kind: hubapi.KindToolUse, Tool: "Bash", Text: `{"command":"make"}`, Call: "a"},
		{Seq: 3, At: at(3, 9), Kind: hubapi.KindToolResult, Tool: "Bash", Text: "error", Err: true, Call: "a"},
		{Seq: 4, At: at(3, 9), Kind: hubapi.KindToolUse, Tool: "Edit", Text: `{"file_path":"/r/project-x/a.go"}`, Call: "b"},
		{Seq: 5, At: at(3, 9), Kind: hubapi.KindToolResult, Tool: "Edit", Text: "ok", Call: "b"},
		{Seq: 6, At: at(3, 10), Kind: hubapi.KindAgent, Text: "fixed"},
		{Seq: 7, At: at(4, 9), Kind: hubapi.KindUser, Text: "now deploy"},
		{Seq: 8, At: at(4, 9), Kind: hubapi.KindToolUse, Tool: "Bash", Text: `{"command":"deploy"}`, Call: "c"},
		{Seq: 9, At: at(4, 9), Kind: hubapi.KindToolResult, Tool: "Bash", Text: "ok", Call: "c"},
	}
}

func dayLines(t *testing.T, s *Source, chat, day string) []string {
	t.Helper()
	r, err := s.Day(context.Background(), chat, day)
	if errors.Is(err, kbsrc.ErrNotFound) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range r.Messages {
		out = append(out, m.Text)
	}
	return out
}

func refs(es []wire.KBEntry) map[string]bool {
	out := map[string]bool{}
	for _, e := range es {
		out[e.Kind+"/"+e.ID] = true
	}
	return out
}

// Every source missing: the day runs, nothing fails, the card says why.
func TestMissingSourcesAreEmpty(t *testing.T) {
	s := With(&kb{}, hubapi.None{}, time.UTC)
	st := &store.State{}
	s.Begin(st, "2026-11-03")
	es, err := s.Recent(context.Background(), day3, time.Time{})
	if err != nil || len(es) != 0 {
		t.Fatalf("recent: %v %v", es, err)
	}
	if _, err := s.Day(context.Background(), "asks/2026-11-03", "2026-11-03"); !errors.Is(err, kbsrc.ErrNotFound) {
		t.Errorf("a missing source's day: %v", err)
	}
	s.End(st, "2026-11-03", 0)
	want := map[string]string{"S3/agents": "no_scope", "S2/asks": "no_scope", "S5/todos": "no_scope", "S7/about": "no_scope", "S6/memory": "waiting_hub"}
	for k, missing := range want {
		got := st.Sources[k]
		if got == nil || got.Available || got.Missing != missing {
			t.Errorf("%s: %+v, want missing %s", k, got, missing)
		}
	}
	if st.Sources["S6/memory"].Waiting != "H2" {
		t.Errorf("memory waits for H2: %+v", st.Sources["S6/memory"])
	}
	if len(st.ActiveDays) != 0 {
		t.Errorf("no data, no active day: %v", st.ActiveDays)
	}
	// Offline without copies: not connected.
	s = With(&kb{}, &hubapi.Offline{}, time.UTC)
	st = &store.State{}
	s.Begin(st, "2026-11-03")
	if _, err := s.Recent(context.Background(), day3, time.Time{}); err != nil {
		t.Fatal(err)
	}
	s.End(st, "2026-11-03", 0)
	for _, k := range []string{"S3/agents", "S2/asks", "S5/todos", "S7/about", "S6/memory"} {
		if st.Sources[k] == nil || st.Sources[k].Missing != "not_connected" {
			t.Errorf("%s offline without a copy: %+v", k, st.Sources[k])
		}
	}
}

// Daily reading asks only the folded page, and the next day only what came after the last seq read.
func TestAgentsReadFoldedIncrementally(t *testing.T) {
	h := &hub{items: sessionItems()}
	s := With(&kb{}, h, time.UTC)
	st := &store.State{}
	ctx := context.Background()

	s.Begin(st, "2026-11-03")
	es, _ := s.Recent(ctx, day3, time.Time{})
	got := refs(es)
	if !got["agents/m1/s1"] || got["agents/m1/s-health"] || got["agents/m1/s-main"] {
		t.Fatalf("listed: %v", got)
	}
	lines := dayLines(t, s, "agents/m1/s1", "2026-11-03")
	text := strings.Join(lines, "\n")
	for _, want := range []string{"我: build project-x", "工作: 2 次工具调用，报错数未知", "Agent: fixed", "卡片: 问「which port?」", "我: 选了：9090", "改了 project-x/a.go", "[seq 2-5]"} {
		if !strings.Contains(text, want) {
			t.Errorf("day 3 lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "deploy") || strings.Contains(text, "error") || strings.Contains(text, "private") {
		t.Errorf("day 3 has another day's items, a tool result or a private card:\n%s", text)
	}
	s.End(st, "2026-11-03", 1)
	if st.Cursors["S3/m1/s1"] != "6|2026-11-03" {
		t.Errorf("cursor: %v", st.Cursors)
	}

	s.Begin(st, "2026-11-04")
	_, _ = s.Recent(ctx, at(4, 0), time.Time{})
	lines = dayLines(t, s, "agents/m1/s1", "2026-11-04")
	if len(lines) != 2 || !strings.Contains(lines[0], "now deploy") {
		t.Errorf("day 4: %v", lines)
	}
	s.End(st, "2026-11-04", 1)
	if len(h.queries) != 2 {
		t.Fatalf("one folded page per pass: %+v", h.queries)
	}
	for i, q := range h.queries {
		if !q.Fold || q.Through != 0 || q.From != 0 {
			t.Errorf("query %d is not a folded page: %+v", i, q)
		}
	}
	if h.queries[0].After != 0 || h.queries[1].After != 6 {
		t.Errorf("after_seq: %d then %d, want 0 then 6", h.queries[0].After, h.queries[1].After)
	}
	if st.Sources["S3/agents"].Units != 2 || st.Sources["S3/agents"].Bytes == 0 {
		t.Errorf("S3 volumes: %+v", st.Sources["S3/agents"])
	}
	if a := st.Sources["S3/agents"]; !a.Available || a.Waiting != "H3" {
		t.Errorf("folds without error counts: S3 waits for H3: %+v", a)
	}
	if strings.Join(st.ActiveDays, ",") != "2026-11-03,2026-11-04" {
		t.Errorf("active days: %v", st.ActiveDays)
	}
	// A rerun of a day already read reads its folded page from the start again, still folded.
	s.Begin(st, "2026-11-03")
	_, _ = s.Recent(ctx, day3, time.Time{})
	if lines := dayLines(t, s, "agents/m1/s1", "2026-11-03"); len(lines) == 0 {
		t.Error("a rerun day reads nothing")
	}
	if q := h.queries[len(h.queries)-1]; !q.Fold || q.After != 0 {
		t.Errorf("rerun query: %+v", q)
	}
}

func TestAgentsTrailingFoldCursorIncludesThrough(t *testing.T) {
	h := &hub{items: []hubapi.Item{
		{Seq: 1, At: at(3, 8), Kind: hubapi.KindUser, Text: "start"},
		{Seq: 2, At: at(3, 9), Kind: hubapi.KindToolUse, Tool: "Bash", Text: "first", Call: "a"},
		{Seq: 3, At: at(3, 10), Kind: hubapi.KindToolResult, Tool: "Bash", Text: "ok", Call: "a"},
	}}
	s := With(&kb{}, h, time.UTC)
	st := &store.State{}
	ctx := context.Background()
	s.Begin(st, "2026-11-03")
	if _, err := s.Recent(ctx, day3, time.Time{}); err != nil {
		t.Fatal(err)
	}
	_ = dayLines(t, s, "agents/m1/s1", "2026-11-03")
	s.End(st, "2026-11-03", 1)
	if got := st.Cursors["S3/m1/s1"]; got != "3|2026-11-03" {
		t.Errorf("a trailing fold left the cursor before its through sequence: %q", got)
	}

	h.items = append(h.items,
		hubapi.Item{Seq: 4, At: at(4, 8), Kind: hubapi.KindToolUse, Tool: "Bash", Text: "second", Call: "b"},
		hubapi.Item{Seq: 5, At: at(4, 9), Kind: hubapi.KindToolResult, Tool: "Bash", Text: "ok", Call: "b"},
		hubapi.Item{Seq: 6, At: at(4, 10), Kind: hubapi.KindAgent, Text: "done"},
	)
	s.Begin(st, "2026-11-04")
	if _, err := s.Recent(ctx, at(4, 0), time.Time{}); err != nil {
		t.Fatal(err)
	}
	text := strings.Join(dayLines(t, s, "agents/m1/s1", "2026-11-04"), "\n")
	if len(h.queries) != 2 || h.queries[1].After != 3 {
		t.Errorf("next query starts after the trailing fold: %+v", h.queries)
	}
	if !strings.Contains(text, "工作: 1 次工具调用") || !strings.Contains(text, "[seq 4-5]") || !strings.Contains(text, "Agent: done") {
		t.Errorf("the next day's tool work was folded into the previous day and lost:\n%s", text)
	}
	s.End(st, "2026-11-04", 1)
	if got := st.Cursors["S3/m1/s1"]; got != "6|2026-11-04" {
		t.Errorf("cursor after the next day: %q", got)
	}
}

func TestByPersonaFirstMessageSpeaker(t *testing.T) {
	h := &hub{
		sessions: []hubapi.Session{{Machine: "m1", ID: "s-persona", Started: at(3, 8), Updated: at(4, 9), ByPersona: true}},
		items: []hubapi.Item{
			{Seq: 1, At: at(3, 9), Kind: hubapi.KindUser, Text: "delegated task"},
			{Seq: 2, At: at(3, 10), Kind: hubapi.KindUser, Text: "my own follow-up"},
			{Seq: 3, At: at(4, 9), Kind: hubapi.KindUser, Text: "next day's user message"},
		},
	}
	s := With(&kb{}, h, time.UTC)
	st := &store.State{}
	ctx := context.Background()
	s.Begin(st, "2026-11-03")
	if _, err := s.Recent(ctx, day3, time.Time{}); err != nil {
		t.Fatal(err)
	}
	lines := dayLines(t, s, "agents/m1/s-persona", "2026-11-03")
	if len(lines) != 2 {
		t.Fatalf("day 3 lines: %v", lines)
	}
	if !strings.Contains(lines[0], SpeakerPersona+": delegated task") {
		t.Errorf("the first message is not Persona's: %v", lines[0])
	}
	if !strings.Contains(lines[1], SpeakerUser+": my own follow-up") {
		t.Errorf("the user follow-up changed speaker: %v", lines[1])
	}
	s.End(st, "2026-11-03", 1)

	s.Begin(st, "2026-11-04")
	if _, err := s.Recent(ctx, at(4, 0), time.Time{}); err != nil {
		t.Fatal(err)
	}
	lines = dayLines(t, s, "agents/m1/s-persona", "2026-11-04")
	if len(lines) != 1 || !strings.Contains(lines[0], SpeakerUser+": next day's user message") {
		t.Errorf("an incremental page's first user message changed speaker: %v", lines)
	}
}

// A segment of seqs gives back its tool calls and results, unfolded, paired, and counted under S4.
func TestSegment(t *testing.T) {
	h := &hub{items: sessionItems()}
	s := With(&kb{}, h, time.UTC)
	items, err := s.Segment(context.Background(), "agents/m1/s1", 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 || items[0].Seq != 2 || items[3].Seq != 5 {
		t.Fatalf("segment: %+v", items)
	}
	q := h.queries[len(h.queries)-1]
	if q.Fold || q.From != 2 || q.Through != 5 {
		t.Errorf("segment query: %+v", q)
	}
	calls := Calls(items)
	if len(calls) != 2 || calls[0].Tool != "Bash" || !calls[0].Err || calls[0].Result != "error" || calls[1].Result != "ok" {
		t.Errorf("calls: %+v", calls)
	}
	if st := s.Status()["S4/segments"]; st.Units != 1 || st.Bytes == 0 {
		t.Errorf("S4 ledger: %+v", st)
	}
}

// Cards: a private card is never read; the assistant's card goes to asks/<day>; a range without H3 is noted.
func TestAsksAndPrivacy(t *testing.T) {
	h := &hub{items: sessionItems()}
	s := With(&kb{}, h, time.UTC)
	st := &store.State{}
	s.Begin(st, "2026-11-03")
	es, _ := s.Recent(context.Background(), day3, time.Time{})
	if !refs(es)["asks/2026-11-03"] {
		t.Fatalf("no asks unit: %v", refs(es))
	}
	lines := dayLines(t, s, "asks/2026-11-03", "2026-11-03")
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "send it?") || strings.Contains(text, "which port?") || strings.Contains(text, "private") {
		t.Errorf("asks day:\n%s", text)
	}
	s.End(st, "2026-11-03", 1)
	if a := st.Sources["S2/asks"]; a == nil || !a.Available || a.Waiting != "H3" {
		t.Errorf("S2: %+v", a)
	}
}

// To-dos, "about me" and memory: read on their day, the catalogs once until they change.
func TestTodosAboutMemory(t *testing.T) {
	h := &hub{}
	s := With(&kb{}, h, time.UTC)
	st := &store.State{}
	ctx := context.Background()
	s.Begin(st, "2026-11-03")
	es, _ := s.Recent(ctx, day3, time.Time{})
	got := refs(es)
	if !got["todos/t1"] || !got["about/me"] || !got["memory/port.md"] || got["memory/later.md"] {
		t.Fatalf("listed: %v", got)
	}
	todo := strings.Join(dayLines(t, s, "todos/t1", "2026-11-03"), "\n")
	for _, want := range []string{"待办: 新建「ship project-x」", "决定（card）: which branch? → main（small）", "结局: 完成「ship project-x」：merged"} {
		if !strings.Contains(todo, want) {
			t.Errorf("todo lacks %q:\n%s", want, todo)
		}
	}
	about := dayLines(t, s, "about/me", "2026-11-03")
	if len(about) != 2 || !strings.HasPrefix(about[0], "00:00 我: ") {
		t.Errorf("about: %v", about)
	}
	mem := dayLines(t, s, "memory/port.md", "2026-11-03")
	if len(mem) != 2 || !strings.Contains(mem[0], "07:00 助手记忆: port of machine-a（reference）") {
		t.Errorf("memory: %v", mem)
	}
	d, _ := s.Get(ctx, KindAbout, "me")
	m := kbsrc.DecodeMeta(d)
	if m.Code != entry.SrcAbout || m.Origin != string(entry.SelfReport) {
		t.Errorf("about meta: %+v", m)
	}
	d, _ = s.Get(ctx, KindMemory, "port.md")
	if m := kbsrc.DecodeMeta(d); m.Code != entry.SrcMemory || m.Origin != string(entry.Handwritten) {
		t.Errorf("memory meta: %+v", m)
	}
	d, _ = s.Get(ctx, KindAgents, "m1/s1")
	if m := kbsrc.DecodeMeta(d); m.Code != entry.SrcAgent || m.Origin != string(entry.Behavior) {
		t.Errorf("agents meta: %+v", m)
	}
	s.End(st, "2026-11-03", 3)
	s.Begin(st, "2026-11-04")
	es, _ = s.Recent(ctx, at(4, 0), time.Time{})
	got = refs(es)
	if got["about/me"] || got["memory/port.md"] {
		t.Errorf("unchanged catalogs listed again: %v", got)
	}
}

func TestTruncatedMemoryIsSkippedUntilComplete(t *testing.T) {
	h := &hub{memoryFiles: []hubapi.MemoryFile{
		{Name: "cut.md", Created: at(3, 7), Modified: at(3, 7), Content: "approved prefix", Truncated: true},
		{Name: "omitted.md", Description: "metadata only", Created: at(3, 7), Modified: at(3, 7), Truncated: true},
		{Name: "whole.md", Created: at(3, 7), Modified: at(3, 7), Content: "complete body"},
	}}
	s := With(&kb{}, h, time.UTC)
	st := &store.State{}
	ctx := context.Background()
	s.Begin(st, "2026-11-03")
	es, err := s.Recent(ctx, day3, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	got := refs(es)
	if !got["memory/whole.md"] || got["memory/cut.md"] || got["memory/omitted.md"] {
		t.Errorf("mixed complete and truncated memories: %v", got)
	}
	for _, name := range []string{"cut.md", "omitted.md"} {
		if d, err := s.Day(ctx, "memory/"+name, "2026-11-03"); !errors.Is(err, kbsrc.ErrNotFound) {
			t.Errorf("truncated %s returned content: %+v, %v", name, d, err)
		}
	}
	if text := strings.Join(dayLines(t, s, "memory/whole.md", "2026-11-03"), "\n"); !strings.Contains(text, "complete body") {
		t.Errorf("complete memory: %s", text)
	}
	s.End(st, "2026-11-03", 1)
	for _, name := range []string{"cut.md", "omitted.md"} {
		if st.Cursors["S6/"+name] != "" {
			t.Errorf("truncated %s committed a cursor: %v", name, st.Cursors)
		}
	}
	if st.Cursors["S6/whole.md"] == "" {
		t.Errorf("complete memory has no cursor: %v", st.Cursors)
	}

	h.memoryFiles[0].Content = "approved prefix\ncomplete suffix"
	h.memoryFiles[0].Modified = at(4, 7)
	h.memoryFiles[0].Truncated = false
	s.Begin(st, "2026-11-04")
	es, err = s.Recent(ctx, at(4, 0), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	got = refs(es)
	if !got["memory/cut.md"] || got["memory/omitted.md"] || got["memory/whole.md"] {
		t.Errorf("later complete memory: %v", got)
	}
	if text := strings.Join(dayLines(t, s, "memory/cut.md", "2026-11-04"), "\n"); !strings.Contains(text, "complete suffix") {
		t.Errorf("later complete memory lacks its suffix: %s", text)
	}
	s.End(st, "2026-11-04", 1)
	if st.Cursors["S6/cut.md"] == "" || st.Cursors["S6/omitted.md"] != "" {
		t.Errorf("cursors after the complete response: %v", st.Cursors)
	}
}

func TestDirectRecordingGetChecksPrivacy(t *testing.T) {
	k := &kb{meta: map[string]map[string]any{
		"r-private": {"id": "r-private", "private": true},
		"r-public":  {"id": "r-public"},
	}}
	s := With(k, hubapi.None{}, time.UTC)
	d, err := s.Get(context.Background(), "recordings", "r-private")
	if !errors.Is(err, kbsrc.ErrNotFound) {
		t.Fatalf("private recording returned without a preceding Recent: detail=%+v err=%v", d, err)
	}
	d, err = s.Get(context.Background(), "recordings", "r-public")
	if err != nil || d.Summary != "a recording summary" {
		t.Fatalf("public recording: detail=%+v err=%v", d, err)
	}
}

// Recordings: K11 off leaves them all out; a private one is never read.
func TestRecordingsSwitchAndPrivate(t *testing.T) {
	k := &kb{entries: []wire.KBEntry{{Kind: "recordings", ID: "r1", Updated: at(3, 9)}, {Kind: "recordings", ID: "r2", Updated: at(3, 9)}},
		meta: map[string]map[string]any{"r1": {"id": "r1"}, "r2": {"id": "r2", "private": true}}}
	s := With(k, hubapi.None{}, time.UTC)
	st := &store.State{}
	s.Begin(st, "2026-11-03")
	es, _ := s.Recent(context.Background(), day3, time.Time{})
	got := refs(es)
	if !got["recordings/r1"] || got["recordings/r2"] {
		t.Errorf("private recording: %v", got)
	}
	if _, err := s.Get(context.Background(), "recordings", "r2"); !errors.Is(err, kbsrc.ErrNotFound) {
		t.Errorf("a private recording's text was given: %v", err)
	}
	if _, err := s.Get(context.Background(), "recordings", "r1"); err != nil {
		t.Fatal(err)
	}
	s.End(st, "2026-11-03", 1)
	if r := st.Sources["S11/recordings"]; r == nil || r.Units != 1 {
		t.Errorf("S11 ledger: %+v", r)
	}
	st.Switches = map[string]entry.SwitchSetting{entry.K11Recordings: {Value: "off"}}
	s.Begin(st, "2026-11-04")
	es, _ = s.Recent(context.Background(), day3, time.Time{})
	if len(es) != 0 {
		t.Errorf("K11 off: %v", refs(es))
	}
	s.End(st, "2026-11-04", 0)
	if r := st.Sources["S11/recordings"]; r.Missing != "off" {
		t.Errorf("K11 off on the card: %+v", r)
	}
}
