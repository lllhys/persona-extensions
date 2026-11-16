package signals

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// All data here is placeholder (user-a, project-x, host-a…); the model is a scripted fake.

const day = "2026-01-05"

var base = time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)

func at(min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }

type query struct {
	Session string
	Q       hubapi.ItemsQuery
}

type fakeHub struct {
	hubapi.None
	sessions []hubapi.Session
	full     map[string][]hubapi.Item // session → every item, unfolded
	cards    []hubapi.Interaction
	todos    []hubapi.Todo
	memories []hubapi.MemoryFile
	queries  []query
	mu       sync.Mutex
}

func (h *fakeHub) Sessions(context.Context, time.Time) ([]hubapi.Session, error) {
	return h.sessions, nil
}
func (h *fakeHub) Interactions(context.Context, hubapi.InteractionsQuery) ([]hubapi.Interaction, error) {
	return h.cards, nil
}
func (h *fakeHub) Todos(context.Context, time.Time) ([]hubapi.Todo, error) { return h.todos, nil }
func (h *fakeHub) Memories(context.Context, bool) ([]hubapi.MemoryFile, error) {
	return h.memories, nil
}

// Items folds like the hub: every run of tool calls and results is one fold with its counts (H3 known).
func (h *fakeHub) Items(_ context.Context, _, session string, q hubapi.ItemsQuery) ([]hubapi.Item, error) {
	h.mu.Lock()
	h.queries = append(h.queries, query{Session: session, Q: q})
	h.mu.Unlock()
	all := h.full[session]
	if q.Fold {
		var out []hubapi.Item
		for i := 0; i < len(all); i++ {
			it := all[i]
			if it.Kind != kToolUse && it.Kind != kToolResult {
				out = append(out, it)
				continue
			}
			f := &hubapi.Fold{From: it.Seq, Known: true}
			tools := map[string]bool{}
			for ; i < len(all) && (all[i].Kind == kToolUse || all[i].Kind == kToolResult); i++ {
				f.To = all[i].Seq
				if all[i].Kind == kToolUse {
					f.Calls++
					if !tools[all[i].Tool] {
						tools[all[i].Tool] = true
						f.Tools = append(f.Tools, all[i].Tool)
					}
				}
				if all[i].Err {
					f.Errors++
				}
			}
			i--
			out = append(out, hubapi.Item{Seq: f.From, At: it.At, Kind: kFold, Fold: f})
		}
		return out, nil
	}
	var out []hubapi.Item
	for _, it := range all {
		if it.Seq >= q.From && it.Seq <= q.Through {
			out = append(out, it)
		}
	}
	return out, nil
}

// fakeModel answers each step with a scripted function of its input.
type fakeModel struct {
	correct func(text string) map[string]any // a message's text → its verdict (nil: not a correction)
	dig     bool
	group   bool
	calls   map[string]int
	mu      sync.Mutex
}

func (m *fakeModel) Complete(_ context.Context, r model.Request) (model.Response, error) {
	m.mu.Lock()
	if m.calls == nil {
		m.calls = map[string]int{}
	}
	m.calls[r.Step]++
	m.mu.Unlock()
	var in map[string]any
	_ = json.Unmarshal([]byte(r.User), &in)
	switch r.Step {
	case "signals_correct":
		var vs []map[string]any
		for _, x := range in["messages"].([]any) {
			msg := x.(map[string]any)
			v := map[string]any{"i": msg["i"], "correction": false, "target": "", "issue_key": "", "issue_text": "", "sensitive": ""}
			if m.correct != nil {
				if got := m.correct(msg["text"].(string)); got != nil {
					for k, val := range got {
						v[k] = val
					}
					v["correction"] = true
				}
			}
			vs = append(vs, v)
		}
		b, _ := json.Marshal(map[string]any{"verdicts": vs})
		return model.Response{Text: string(b)}, nil
	case "signals_issues":
		// group: every open issue in one group (the scripted case of two keys for one kind of problem).
		groups := []map[string]any{}
		if m.group {
			var keys []string
			for _, x := range in["issues"].([]any) {
				keys = append(keys, x.(map[string]any)["key"].(string))
			}
			groups = append(groups, map[string]any{"key": keys[0], "members": keys, "text": "占位：合并后的要求"})
		}
		b, _ := json.Marshal(map[string]any{"groups": groups})
		return model.Response{Text: string(b)}, nil
	case "signals_dig":
		var ds []map[string]any
		for _, x := range in["hits"].([]any) {
			h := x.(map[string]any)
			ds = append(ds, map[string]any{"h": h["h"], "dig": m.dig, "reason": "placeholder", "from_seq": 0, "through_seq": 0})
		}
		b, _ := json.Marshal(map[string]any{"decisions": ds})
		return model.Response{Text: string(b)}, nil
	case "signals_mine":
		// One ② entry anchored to the first failing call's result.
		var es []map[string]any
		for _, x := range in["calls"].([]any) {
			c := x.(map[string]any)
			if c["error"] == true {
				es = append(es, map[string]any{"facet": "tools", "kind": "pitfall", "object": "host-a", "type": "machine",
					"text": fmt.Sprintf("连 host-a 时用占位入口 entry-%v，直连会失败", c["seq"]), "situation": "", "choice": "", "why": "",
					"pitfall": "直连失败", "fix": "改用占位入口", "seq": c["result_seq"], "quote": "connection refused",
					"impact": map[string]any{"moment": "use_object", "action": "改用占位入口", "object": "host-a"}})
				break
			}
		}
		b, _ := json.Marshal(map[string]any{"entries": es})
		return model.Response{Text: string(b)}, nil
	}
	return model.Response{}, fmt.Errorf("unexpected step %s", r.Step)
}

type mineModel struct {
	*fakeModel
	respond   func() (model.Response, error)
	mineCalls int
}

func (m *mineModel) Complete(ctx context.Context, r model.Request) (model.Response, error) {
	if r.Step != "signals_mine" {
		return m.fakeModel.Complete(ctx, r)
	}
	m.mineCalls++
	return m.respond()
}

func newState() *store.State {
	return &store.State{Entries: map[string]*entry.Entry{}, Samples: map[string][]string{}}
}

// observe stands in for the curator's Observe (P3 owns the real one): keep by text, count sightings.
func observe(st *store.State) func(e *entry.Entry, run string) string {
	return func(e *entry.Entry, _ string) string {
		id := fmt.Sprintf("%c-%010x", e.Facet[0], len(e.Text)*7919+int(e.Text[0]))
		for _, x := range st.Entries {
			if x.Text == e.Text && x.Facet == e.Facet {
				x.Evidence = append(x.Evidence, e.Evidence...)
				x.Count++
				return x.ID
			}
		}
		e.ID = id
		if e.Status == "" {
			e.Status = entry.Candidate
		}
		e.Count = max(e.Count, 1)
		st.Entries[id] = e
		return id
	}
}

func opts(t *testing.T, h hubapi.Hub, m model.Model, st *store.State) Options {
	return Options{Hub: h, Model: m, Lang: "zh-Hans", Loc: time.UTC, Dir: t.TempDir(), Now: func() time.Time { return at(600) },
		Observe: observe(st), Run: "test"}
}

// session builds a session's items from (kind, text) pairs, one minute apart, starting at seq 1.
func items(start int, rows ...[2]string) []hubapi.Item {
	var out []hubapi.Item
	for i, r := range rows {
		it := hubapi.Item{Seq: i + 1, At: at(start + i), Kind: r[0], Text: r[1]}
		if r[0] == kToolUse {
			it.Tool, it.Text = "bash", r[1]
		}
		if r[0] == kToolResult && strings.HasPrefix(r[1], "ERR ") {
			it.Err, it.Text = true, strings.TrimPrefix(r[1], "ERR ")
		}
		out = append(out, it)
	}
	return out
}

func sess(id string, assistant bool) hubapi.Session {
	return hubapi.Session{Machine: "m1", ID: id, Title: "placeholder " + id, Assistant: assistant, Started: at(-60), Updated: at(300)}
}

// The ledger with no entries and no hand-written memory: every correction is a new issue; the same kind twice in two
// sessions becomes a stable ① entry (K8 on), and the corrections point at it.
func TestLedgerEmptyCatalogue(t *testing.T) {
	h := &fakeHub{full: map[string][]hubapi.Item{
		"s1": items(0, [2]string{kAgent, "要我确认一下 project-x 的目标吗？"}, [2]string{kUser, "不是说过了吗，别再问了"}),
		"s2": items(30, [2]string{kAgent, "需要我再确认 project-y 的范围吗？"}, [2]string{kUser, "说过了，不用再问"}, [2]string{kUser, "好的继续"}),
	}, sessions: []hubapi.Session{sess("s1", true), sess("s2", false)}}
	st := newState()
	m := &fakeModel{correct: func(text string) map[string]any {
		if strings.Contains(text, "说过") {
			return map[string]any{"issue_key": "Asks Already Answered", "issue_text": "用户已经回答过的事不要再问一遍"}
		}
		return nil
	}}
	o := opts(t, h, m, st)
	res, err := Run(context.Background(), st, day, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.UserMsgs != 3 || res.Screened != 2 || res.Corrections != 2 {
		t.Fatalf("msgs %d screened %d corrections %d", res.UserMsgs, res.Screened, res.Corrections)
	}
	if len(res.NewEntries) != 1 {
		t.Fatalf("new entries %v", res.NewEntries)
	}
	e := st.Entries[res.NewEntries[0]]
	if e.Facet != entry.Interaction || e.Status != entry.Stable || len(e.Evidence) != 2 || e.Text != "用户已经回答过的事不要再问一遍" {
		t.Fatalf("entry %+v", e)
	}
	for _, c := range st.Corrections {
		if c.Issue != "asks-already-answered" || c.Entry != e.ID || c.Recurrence {
			t.Fatalf("correction %+v", c)
		}
	}
	// Run again: nothing new (idempotent), the issue not filed twice.
	res, _ = Run(context.Background(), st, day, o)
	if res.Corrections != 0 || len(res.NewEntries) != 0 || len(st.Corrections) != 2 {
		t.Fatalf("rerun: %+v, %d corrections", res, len(st.Corrections))
	}
	// K8 off: the issue's entry starts as a candidate.
	st2 := newState()
	st2.Switches = map[string]entry.SwitchSetting{entry.K8AutoEffective: {Value: "off"}}
	o2 := opts(t, h, m, st2)
	res, _ = Run(context.Background(), st2, day, o2)
	if len(res.NewEntries) != 1 || st2.Entries[res.NewEntries[0]].Status != entry.Candidate {
		t.Fatalf("K8 off: %+v", res.NewEntries)
	}
}

// The judge gave one kind of problem two keys: the grouping merges them, and the merged issue (two sessions) is filed.
// The same long session on two days counts as two occasions.
func TestLedgerGroupsIssues(t *testing.T) {
	h := &fakeHub{full: map[string][]hubapi.Item{
		"s1": items(0, [2]string{kUser, "说过了 A"}),
		"s2": items(30, [2]string{kUser, "说过了 B"}),
	}, sessions: []hubapi.Session{sess("s1", true), sess("s2", false)}}
	st := newState()
	m := &fakeModel{group: true, correct: func(text string) map[string]any {
		return map[string]any{"issue_key": "key-" + text[len(text)-1:], "issue_text": "占位要求 " + text[len(text)-1:]}
	}}
	res, err := Run(context.Background(), st, day, opts(t, h, m, st))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.NewEntries) != 1 || st.Entries[res.NewEntries[0]].Text != "占位：合并后的要求" {
		t.Fatalf("grouped: %+v", res.NewEntries)
	}
	if st.Corrections[0].Issue != st.Corrections[1].Issue || st.Corrections[1].Entry != res.NewEntries[0] {
		t.Fatalf("corrections %+v", st.Corrections)
	}
	// One session, two days.
	h2 := &fakeHub{full: map[string][]hubapi.Item{
		"s1": {{Seq: 1, At: at(0), Kind: kUser, Text: "说过了"}, {Seq: 2, At: at(-24 * 60), Kind: kUser, Text: "又说过了"}},
	}, sessions: []hubapi.Session{func() hubapi.Session { s := sess("s1", true); s.Started = at(-48 * 60); return s }()}}
	st2 := newState()
	m2 := &fakeModel{correct: func(string) map[string]any { return map[string]any{"issue_key": "k", "issue_text": "占位要求二"} }}
	o2 := opts(t, h2, m2, st2)
	_, _ = Run(context.Background(), st2, "2026-01-04", o2)
	res, _ = Run(context.Background(), st2, day, o2)
	if len(res.NewEntries) != 1 {
		t.Fatalf("one session over two days: %+v, %d corrections", res, len(st2.Corrections))
	}
}

// The next day reads a session's folded page on from where the last day ended (after_seq), with some context.
func TestFoldedPageIncremental(t *testing.T) {
	var rows []hubapi.Item
	for i := 1; i <= 200; i++ {
		rows = append(rows, hubapi.Item{Seq: i, At: at(-24*60 + i*5), Kind: kAgent, Text: "x"})
	}
	s := sess("s1", false)
	s.Started = at(-24 * 60)
	h := &fakeHub{full: map[string][]hubapi.Item{"s1": rows}, sessions: []hubapi.Session{s}}
	st := newState()
	o := opts(t, h, nil, st)
	_, _ = Run(context.Background(), st, "2026-01-04", o)
	_, _ = Run(context.Background(), st, day, o)
	_, _ = Run(context.Background(), st, "2026-01-04", o) // a day again: read whole
	if len(h.queries) != 3 || h.queries[0].Q.After != 0 || h.queries[1].Q.After == 0 || h.queries[2].Q.After != 0 {
		t.Fatalf("queries %+v", h.queries)
	}
	if h.queries[1].Q.After != 167-contextSeqs { // seq i is at 10:00 the day before plus 5i minutes: 167 is the last before midnight
		t.Fatalf("after %d", h.queries[1].Q.After)
	}
}

// One correction of the same issue in one session only is not an entry yet.
func TestLedgerOneSessionIsNotEnough(t *testing.T) {
	h := &fakeHub{full: map[string][]hubapi.Item{
		"s1": items(0, [2]string{kUser, "说过了"}, [2]string{kAgent, "抱歉"}, [2]string{kUser, "又说过了"}),
	}, sessions: []hubapi.Session{sess("s1", true)}}
	st := newState()
	m := &fakeModel{correct: func(string) map[string]any { return map[string]any{"issue_key": "k", "issue_text": "占位要求一"} }}
	res, _ := Run(context.Background(), st, day, opts(t, h, m, st))
	if res.Corrections != 2 || len(res.NewEntries) != 0 {
		t.Fatalf("%+v", res)
	}
}

// With entries and hand-written memory: corrections file against them; a recurrence counts only more than ten
// minutes after the entry took effect (the same turn is not one), never before it took effect.
func TestLedgerRecurrence(t *testing.T) {
	st := newState()
	st.Entries["i-0000000001"] = &entry.Entry{ID: "i-0000000001", Facet: entry.Interaction, Text: "占位要求：先给结论", Status: entry.Stable,
		Effective: at(0).Format(time.RFC3339), Count: 1}
	st.Entries["i-0000000002"] = &entry.Entry{ID: "i-0000000002", Facet: entry.Interaction, Text: "占位要求：不在出口里", Status: entry.Candidate, Count: 1}
	h := &fakeHub{full: map[string][]hubapi.Item{
		"s1": items(-5, [2]string{kUser, "不对 A 结论呢"}, [2]string{kAgent, "x"}, [2]string{kAgent, "y"}, [2]string{kAgent, "z"},
			[2]string{kAgent, "w"}, [2]string{kAgent, "v"}, [2]string{kAgent, "u"}, [2]string{kAgent, "t"}, [2]string{kAgent, "s"},
			[2]string{kAgent, "r"}, [2]string{kUser, "不对 A 又没先给结论"}),
		"s2": items(30, [2]string{kUser, "不对 B 未生效"}, [2]string{kUser, "不对 M 记忆"}),
	}, sessions: []hubapi.Session{sess("s1", true), sess("s2", false)},
		memories: []hubapi.MemoryFile{{Name: "no-reconfirm.md", Description: "占位：说过的不再问", Created: at(-120)}}}
	// s1: messages at 9:55 (before Effective) and 10:05 (5 minutes after: the same turn); s2 at 10:30.
	m := &fakeModel{correct: func(text string) map[string]any {
		switch {
		case strings.Contains(text, "A"):
			return map[string]any{"target": "i-0000000001"}
		case strings.Contains(text, "B"):
			return map[string]any{"target": "i-0000000002"}
		case strings.Contains(text, "M"):
			return map[string]any{"target": "memory:no-reconfirm.md"}
		}
		return nil
	}}
	res, err := Run(context.Background(), st, day, opts(t, h, m, st))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range st.Corrections {
		got[c.Quote] = c.Recurrence
		if c.Entry == "" {
			t.Fatalf("not filed: %+v", c)
		}
	}
	want := map[string]bool{"不对 A 结论呢": false, "不对 A 又没先给结论": false, "不对 B 未生效": false, "不对 M 记忆": true}
	for q, w := range want {
		if got[q] != w {
			t.Errorf("%s: recurrence %v, want %v", q, got[q], w)
		}
	}
	if res.Recurrences != 1 {
		t.Fatalf("recurrences %d", res.Recurrences)
	}
	// Eleven minutes after: a recurrence.
	h.full["s3"] = items(11, [2]string{kUser, "不对 A 还是没先给结论"})
	h.sessions = append(h.sessions, sess("s3", false))
	_, _ = Run(context.Background(), st, day, opts(t, h, m, st))
	last := st.Corrections[len(st.Corrections)-1]
	if last.Quote != "不对 A 还是没先给结论" || !last.Recurrence {
		t.Fatalf("eleven minutes after: %+v", last)
	}
}

// screenOf runs the screen on one session's items.
func screenOf(st *store.State, rows []hubapi.Item, foldIt bool) []hit {
	h := &fakeHub{full: map[string][]hubapi.Item{"s1": rows}}
	page, _ := h.Items(context.Background(), "m1", "s1", hubapi.ItemsQuery{Fold: foldIt})
	s := &session{Session: sess("s1", false), Ref: "agents/m1/s1", Source: entry.SrcAgent, Items: page, start: base.Add(-time.Hour), end: base.Add(23 * time.Hour)}
	g := &gathered{Day: day, Sessions: []*session{s}}
	return screen(g, st, nil, nil)
}

func signalsOf(hs []hit) []string {
	var out []string
	for _, h := range hs {
		out = append(out, h.Signal)
	}
	return out
}

// Every kind of friction signal, one case each, found without a model.
func TestScreenEverySignal(t *testing.T) {
	st := newState()
	st.Entries["t-1"] = &entry.Entry{ID: "t-1", Facet: entry.Tools, Object: "host-b", Status: entry.Stable}
	cases := map[string][]hubapi.Item{
		entry.SigAskInfra: items(0, [2]string{kAgent, "请问部署到哪台机器？端口是多少？"}),
		entry.SigStuck:    items(0, [2]string{kAgent, "我卡住了，绕不过这个限制"}),
		entry.SigBlocked:  items(0, [2]string{kNotice, "tool call denied by policy"}),
		entry.SigPraise:   items(0, [2]string{kUser, "这样就对了"}),
		entry.SigExplicit: items(0, [2]string{kUser, "以后都用 make check"}),
		entry.SigRetry: items(0, [2]string{kToolUse, "deploy"}, [2]string{kToolResult, "ERR failed"},
			[2]string{kToolUse, "deploy --retry"}, [2]string{kToolResult, "ok"}),
	}
	for want, rows := range cases {
		got := signalsOf(screenOf(st, rows, true))
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s: got %v", want, got)
		}
	}
	// repeat: errors in a stretch touching a known ② object.
	rows := items(0, [2]string{kToolUse, "ssh host-b"}, [2]string{kToolResult, "ERR refused"})
	rows[0].Tool = "ssh-host-b"
	if got := signalsOf(screenOf(st, rows, true)); len(got) != 1 || got[0] != entry.SigRepeat {
		t.Errorf("repeat: got %v", got)
	}
	// A fold without error counts (no H3) is "not known", never a signal and never "no errors".
	h := &fakeHub{full: map[string][]hubapi.Item{"s1": items(0, [2]string{kToolUse, "x"}, [2]string{kToolResult, "ERR y"}, [2]string{kToolUse, "z"})}}
	page, _ := h.Items(context.Background(), "m1", "s1", hubapi.ItemsQuery{Fold: true})
	page[0].Fold.Known, page[0].Fold.Errors = false, 0
	s := &session{Session: sess("s1", false), Ref: "agents/m1/s1", Items: page, start: base.Add(-time.Hour), end: base.Add(23 * time.Hour)}
	g := &gathered{Day: day, Sessions: []*session{s}}
	if hs := screen(g, st, nil, nil); len(hs) != 0 || g.foldsUnknown() != 1 {
		t.Errorf("unknown fold: %v, %d unknown", signalsOf(hs), g.foldsUnknown())
	}
	// correction: from the ledger.
	s = &session{Session: sess("s1", false), Ref: "agents/m1/s1", Items: items(0, [2]string{kUser, "x"}), start: base, end: base.Add(time.Hour)}
	g = &gathered{Day: day, Sessions: []*session{s}}
	if got := signalsOf(screen(g, st, nil, []entry.Correction{{ID: "c-1", Ref: "agents/m1/s1", Seq: 1}})); len(got) != 1 || got[0] != entry.SigCorrection {
		t.Errorf("correction: got %v", got)
	}
	// rework: a reopened to-do, an interrupted session; blocked and ask_infra from cards.
	s.Ended, s.Updated = "interrupted", at(10)
	g = &gathered{Day: day, Sessions: []*session{s}, Todos: []hubapi.Todo{{ID: "todo-1", Reopened: 1}},
		Cards: []hubapi.Interaction{{ID: "k1", Kind: "permission", At: base}, {ID: "k2", Kind: "ask_user", Question: "用哪个账号登录？", At: base}}}
	got := strings.Join(signalsOf(screen(g, st, nil, nil)), ",")
	for _, want := range []string{entry.SigRework + "", entry.SigBlocked, entry.SigAskInfra} {
		if !strings.Contains(got, want) {
			t.Errorf("cards and to-dos: %s missing in %s", want, got)
		}
	}
	if strings.Count(got, entry.SigRework) != 2 {
		t.Errorf("rework twice (to-do, session): %s", got)
	}
	// The user's own words for the screen.
	s = &session{Session: sess("s1", false), Ref: "agents/m1/s1", Items: items(0, [2]string{kUser, "word-z"}), start: base, end: base.Add(time.Hour)}
	g = &gathered{Day: day, Sessions: []*session{s}}
	if got := signalsOf(screen(g, st, map[string][]string{entry.SigExplicit: {"word-z"}}, nil)); len(got) != 1 {
		t.Errorf("user word: %v", got)
	}
}

// A dig reads at most one folded stretch on each side of the signal.
func TestReach(t *testing.T) {
	rows := items(0, [2]string{kUser, "a"}, [2]string{kToolUse, "b"}, [2]string{kToolResult, "c"}, [2]string{kAgent, "d"},
		[2]string{kUser, "e"}, [2]string{kToolUse, "f"}, [2]string{kToolResult, "g"}, [2]string{kAgent, "h"},
		[2]string{kToolUse, "i"}, [2]string{kToolResult, "j"})
	h := &fakeHub{full: map[string][]hubapi.Item{"s1": rows}}
	page, _ := h.Items(context.Background(), "m1", "s1", hubapi.ItemsQuery{Fold: true})
	s := &session{Items: page}
	// folds: 2–3, 6–7, 9–10. A text hit at 5 reaches 2..7 at most; a hit inside 6–7 only that fold.
	if lo, hi, _ := reach(s, 5); lo != 2 || hi != 7 {
		t.Errorf("reach(5) = %d..%d", lo, hi)
	}
	if lo, hi, _ := reach(s, 6); lo != 6 || hi != 7 {
		t.Errorf("reach(6) = %d..%d", lo, hi)
	}
	if lo, hi := clamp(s, 5, 1, 10); lo != 2 || hi != 7 {
		t.Errorf("clamp = %d..%d", lo, hi)
	}
}

func TestDigMineFailureIsNotRecordedAsDig(t *testing.T) {
	tests := []struct {
		name         string
		respond      func() (model.Response, error)
		wantDecision string
		wantReason   []string
	}{
		{name: "model error", respond: func() (model.Response, error) {
			return model.Response{}, fmt.Errorf("sentinel mine failure")
		}, wantDecision: DecSkip, wantReason: []string{"mine failed", "sentinel mine failure"}},
		{name: "malformed answer", respond: func() (model.Response, error) {
			return model.Response{Text: "{not-json"}, nil
		}, wantDecision: DecSkip, wantReason: []string{"mine answer"}},
		{name: "valid empty", respond: func() (model.Response, error) {
			return model.Response{Text: `{"entries":[]}`}, nil
		}, wantDecision: DecDig, wantReason: []string{"placeholder"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeHub{full: map[string][]hubapi.Item{
				"s1": items(0, [2]string{kToolUse, "deploy"}, [2]string{kToolResult, "ERR connection refused"},
					[2]string{kToolUse, "deploy --retry"}, [2]string{kToolResult, "ok"}),
			}, sessions: []hubapi.Session{sess("s1", false)}}
			m := &mineModel{fakeModel: &fakeModel{dig: true}, respond: tc.respond}
			st := newState()
			o := opts(t, h, m, st)
			res, err := Run(context.Background(), st, day, o)
			if err != nil {
				t.Fatal(err)
			}
			if res.Hits[entry.SigRetry] != 1 || m.mineCalls != 1 {
				t.Fatalf("the case did not reach mining: hits=%v mine calls=%d", res.Hits, m.mineCalls)
			}
			if len(st.Digs) != 1 {
				t.Fatalf("digs: %+v", st.Digs)
			}
			d := st.Digs[0]
			if d.Bytes == 0 || d.FromSeq != 1 || d.ThruSeq != 4 || res.Digs != 1 || res.SegmentBytes != d.Bytes {
				t.Fatalf("the unfolded read was not accounted for: dig=%+v result=%+v", d, res)
			}
			if len(d.Entries) != 0 || len(res.Mined) != 0 {
				t.Fatalf("unexpected mined entries: dig=%+v result=%+v", d, res)
			}
			if d.Decision != tc.wantDecision {
				t.Errorf("decision=%q, want %q; dig=%+v", d.Decision, tc.wantDecision, d)
			}
			for _, want := range tc.wantReason {
				if !strings.Contains(d.Reason, want) {
					t.Errorf("reason=%q lacks %q", d.Reason, want)
				}
			}
			if tc.name == "model error" {
				m.respond = func() (model.Response, error) { return model.Response{Text: `{"entries":[]}`}, nil }
				if _, err := Run(context.Background(), st, day, o); err != nil {
					t.Fatal(err)
				}
				if m.mineCalls != 1 || len(st.Digs) != 1 {
					t.Errorf("terminal dig was retried or duplicated: calls=%d digs=%+v", m.mineCalls, st.Digs)
				}
			}
		})
	}
}

func TestCallsPairConcurrentResultsByCallID(t *testing.T) {
	got := calls([]hubapi.Item{
		{Seq: 1, Kind: kToolUse, Tool: "tool-a", Text: "input A", Call: "call-a"},
		{Seq: 2, Kind: kToolUse, Tool: "tool-b", Text: "input B", Call: "call-b"},
		{Seq: 3, Kind: kToolResult, Tool: "tool-a", Text: "A failed", Call: "call-a", Err: true},
		{Seq: 4, Kind: kToolResult, Tool: "tool-b", Text: "B ok", Call: "call-b"},
	})
	if len(got) != 2 {
		t.Fatalf("got %d calls: %+v", len(got), got)
	}
	if got[0].Seq != 1 || got[0].ResultSeq != 3 || got[0].Result != "A failed" || !got[0].Error {
		t.Fatalf("call-a: %+v", got[0])
	}
	if got[1].Seq != 2 || got[1].ResultSeq != 4 || got[1].Result != "B ok" || got[1].Error {
		t.Fatalf("call-b: %+v", got[1])
	}
}

func TestCallsWithoutCallIDsPairSequentially(t *testing.T) {
	got := calls([]hubapi.Item{
		{Seq: 1, Kind: kToolUse, Tool: "tool-a", Text: "input A"},
		{Seq: 2, Kind: kToolResult, Tool: "tool-a", Text: "A ok"},
	})
	if len(got) != 1 || got[0].ResultSeq != 2 || got[0].Result != "A ok" || got[0].Error {
		t.Fatalf("sequential call without a Call ID: %+v", got)
	}
}

// The daily run: only folded pages and the triggered stretches are requested; every dig records its signal, decision
// and bytes; every entry it made points back to it.
func TestDigsPointBackAndReadOnlyWhatIsTriggered(t *testing.T) {
	h := &fakeHub{full: map[string][]hubapi.Item{
		"s1": items(0,
			[2]string{kUser, "部署 project-x"},
			[2]string{kToolUse, "ssh host-a"}, [2]string{kToolResult, "ERR connection refused"},
			[2]string{kToolUse, "ssh entry-host"}, [2]string{kToolResult, "ok"},
			[2]string{kAgent, "完成"},
			[2]string{kUser, "以后都这么连"},
			[2]string{kToolUse, "ls"}, [2]string{kToolResult, "ok"},
			[2]string{kToolUse, "cat big"}, [2]string{kToolResult, strings.Repeat("payload ", 500)},
		),
	}, sessions: []hubapi.Session{sess("s1", false)}}
	st := newState()
	m := &fakeModel{dig: true}
	res, err := Run(context.Background(), st, day, opts(t, h, m, st))
	if err != nil {
		t.Fatal(err)
	}
	if res.Hits[entry.SigRetry] != 1 || res.Hits[entry.SigExplicit] != 1 {
		t.Fatalf("hits %v", res.Hits)
	}
	if len(st.Digs) != 2 {
		t.Fatalf("digs %+v", st.Digs)
	}
	ranges := map[[2]int]bool{}
	page, _ := h.Items(context.Background(), "m1", "s1", hubapi.ItemsQuery{Fold: true})
	h.queries = h.queries[:len(h.queries)-1]
	for _, d := range st.Digs {
		if d.Decision != DecDig || d.Bytes == 0 || d.Signal == "" || d.Hit == "" {
			t.Fatalf("dig %+v", d)
		}
		lo, hi, _ := reach(&session{Items: page}, d.HitSeq)
		if d.FromSeq < lo || d.ThruSeq > hi {
			t.Fatalf("dig %s read %d..%d beyond one stretch each side (%d..%d)", d.Signal, d.FromSeq, d.ThruSeq, lo, hi)
		}
		ranges[[2]int{d.FromSeq, d.ThruSeq}] = true
	}
	for _, q := range h.queries {
		if !q.Q.Fold && !ranges[[2]int{q.Q.From, q.Q.Through}] {
			t.Fatalf("an unfolded read no dig asked for: %+v", q)
		}
	}
	if len(res.Mined) == 0 {
		t.Fatal("nothing mined")
	}
	for _, id := range res.Mined {
		e := st.Entries[id]
		d := DigOf(st, e)
		if d == nil || !containsStr(d.Entries, id) {
			t.Fatalf("entry %s does not point back to its dig: %+v", id, e)
		}
		if e.Facet != entry.Tools || e.Evidence[0].Source != entry.SrcTools || e.Evidence[0].Seq < d.FromSeq || e.Evidence[0].Seq > d.ThruSeq {
			t.Fatalf("entry %+v", e)
		}
		if e.Status != entry.Stable {
			t.Fatalf("a pitfall shown by the tool results is effective at once with K8: %s", e.Status)
		}
	}
	// No model: hits are recorded as skipped, nothing is read unfolded.
	h.queries = nil
	st2 := newState()
	o := opts(t, h, nil, st2)
	if _, err := Run(context.Background(), st2, day, o); err != nil {
		t.Fatal(err)
	}
	for _, q := range h.queries {
		if !q.Q.Fold {
			t.Fatalf("unfolded read without a dig: %+v", q)
		}
	}
	for _, d := range st2.Digs {
		if d.Decision != DecSkip || d.Bytes != 0 {
			t.Fatalf("no model: %+v", d)
		}
	}
	// Workers > 1: the same digs, entries and bytes.
	st3 := newState()
	o3 := opts(t, h, m, st3)
	o3.Workers = 3
	res3, err := Run(context.Background(), st3, day, o3)
	if err != nil {
		t.Fatal(err)
	}
	if len(st3.Digs) != len(st.Digs) || res3.SegmentBytes != res.SegmentBytes || len(res3.Mined) != len(res.Mined) {
		t.Fatalf("workers: %d digs %d bytes, want %d %d", len(st3.Digs), res3.SegmentBytes, len(st.Digs), res.SegmentBytes)
	}
	vol, _ := VolumeOf(st3, o3.Dir, []string{day})
	if vol.Segments != 2 || vol.SegmentBytes != res.SegmentBytes || vol.BySignal[entry.SigRetry] != 1 {
		t.Fatalf("volume %+v", vol)
	}
	// The sources card's S4 row says what was dug, not "nothing yet".
	if s := st3.Sources[entry.SrcTools+"/segments"]; s == nil || !s.Available || s.Units != 2 || s.Bytes != res.SegmentBytes {
		t.Fatalf("S4 status %+v", s)
	}
}

func containsStr(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// An entry that was there before a dig saw it again carries no flag: the dig's list, or its evidence at the hit,
// points back.
func TestDigOfWithoutFlag(t *testing.T) {
	st := newState()
	st.Digs = []entry.Dig{{ID: "d-1", Signal: entry.SigCorrection, Ref: "agents/m1/s1", Decision: DecDig, HitSeq: 9, FromSeq: 10, ThruSeq: 20, Entries: []string{"i-1"}},
		{ID: "d-2", Signal: entry.SigExplicit, Ref: "agents/m1/s2", Decision: DecDig, HitSeq: 40, FromSeq: 30, ThruSeq: 39}}
	if d := DigOf(st, &entry.Entry{ID: "i-1"}); d == nil || d.ID != "d-1" {
		t.Fatalf("by list: %+v", d)
	}
	if d := DigOf(st, &entry.Entry{ID: "w-1", Evidence: []entry.Evidence{{Ref: "agents/m1/s2", Seq: 40}}}); d == nil || d.ID != "d-2" {
		t.Fatalf("by the hit: %+v", d)
	}
}

// A mined entry whose quote is not in what the model was shown, or that is a bare fact, is dropped.
func TestMinedChecks(t *testing.T) {
	st := newState()
	o := Options{Lang: "zh-Hans", Loc: time.UTC}
	s := &session{Session: sess("s1", false), Ref: "agents/m1/s1", Source: entry.SrcAgent}
	rec := &entry.Dig{ID: "d-1", Signal: entry.SigRetry, Day: day, FromSeq: 2, ThruSeq: 3}
	shown := map[int]hubapi.Item{2: {Seq: 2, Kind: kToolResult, Text: "error: port 22 closed"}, 1: {Seq: 1, Kind: kAgent, Text: "好的"}}
	ok := mined{Facet: "tools", Kind: "pitfall", Object: "host-a", Text: "host-a 的 22 端口不通，改用占位入口", Pitfall: "22 端口不通", Seq: 2, Quote: "port 22 closed"}
	if e, why := toEntry(&o, st, s, rec, ok, shown); e == nil {
		t.Fatalf("dropped: %s", why)
	}
	for name, m := range map[string]mined{
		"quote not shown": {Facet: "tools", Kind: "pitfall", Object: "host-a", Text: "占位条目文字比较长一点", Pitfall: "x", Seq: 2, Quote: "not there"},
		"bare fact":       {Facet: "tools", Kind: "fact", Object: "host-a", Text: "本次一共用了很多占位令牌", Seq: 2, Quote: "port 22"},
		"no object":       {Facet: "tools", Kind: "pitfall", Text: "占位条目文字比较长一点", Pitfall: "x", Seq: 2, Quote: "port 22"},
		"not the user's":  {Facet: "workflow", Text: "处理占位事情时先跑检查", Seq: 1, Quote: "好的"},
		"wrong language":  {Facet: "tools", Kind: "pitfall", Object: "host-a", Text: "port twenty two on host a is closed so use another way", Pitfall: "x", Seq: 2, Quote: "port 22"},
		"secret":          {Facet: "tools", Kind: "approach", Object: "host-a", Text: "用 token sk-abcdefghijklmnopqrstuvwxyz0123456789 连 host-a", Seq: 2, Quote: "port 22"},
	} {
		if e, _ := toEntry(&o, st, s, rec, m, shown); e != nil {
			t.Errorf("%s: kept %+v", name, e)
		}
	}
}

// Readings with fewer than 20 chances say so.
func TestReadingsUnmeasurable(t *testing.T) {
	st := newState()
	dir := t.TempDir()
	sd, _ := loadSide(dir)
	sd.day(day).UserMsgs = 10
	_ = sd.save(dir)
	st.Corrections = []entry.Correction{{ID: "c-1", Day: day}, {ID: "c-2", Day: day, Entry: "i-1", Recurrence: true}}
	rs, err := Readings(st, dir, day)
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Code != "R1" || rs[0].Measure || rs[0].Note != NotMeasurable || rs[0].Num != 2 || rs[0].Den != 10 {
		t.Fatalf("R1 %+v", rs[0])
	}
	sd.day(day).UserMsgs = 100
	_ = sd.save(dir)
	rs, _ = Readings(st, dir, day)
	if !rs[0].Measure || rs[0].Value != 2 {
		t.Fatalf("R1 %+v", rs[0])
	}
	if len(st.Readings) != 5 {
		t.Fatalf("readings replaced, not added: %d", len(st.Readings))
	}
}

func TestSampleWeek(t *testing.T) {
	st := newState()
	for i := 0; i < 40; i++ {
		st.Corrections = append(st.Corrections, entry.Correction{ID: fmt.Sprintf("c-%02d", i), Day: day})
	}
	a := SampleWeek(st, "2026-W02", []string{day})
	b := SampleWeek(st, "2026-W02", []string{day})
	if len(a) != 30 || strings.Join(a, ",") != strings.Join(b, ",") || len(st.Samples["corrections/2026-W02"]) != 30 {
		t.Fatalf("sample %v", a)
	}
}
