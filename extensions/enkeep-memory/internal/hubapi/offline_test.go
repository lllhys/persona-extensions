package hubapi

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// transcript writes a synthetic Claude Code transcript (placeholder data only) and returns its directory.
func transcript(t *testing.T, root, dir, id string, recs []map[string]any) {
	t.Helper()
	p := filepath.Join(root, dir)
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, r := range recs {
		r["sessionId"] = id
		raw, _ := json.Marshal(r)
		b.Write(raw)
		b.WriteByte('\n')
	}
	b.WriteString(`{"type":"api-request","message":{"content":"a request copy"}}` + "\n")
	if err := os.WriteFile(filepath.Join(p, id+".jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func at(h, m int) string { return time.Date(2026, 11, 3, h, m, 0, 0, time.UTC).Format(time.RFC3339) }

func user(ts, text string) map[string]any {
	return map[string]any{"type": "user", "timestamp": ts, "origin": map[string]any{"kind": "human"}, "message": map[string]any{"role": "user", "content": text}}
}

func asst(ts string, blocks ...map[string]any) map[string]any {
	return map[string]any{"type": "assistant", "timestamp": ts, "message": map[string]any{"role": "assistant", "content": blocks}}
}

func result(ts, id, text string, isErr bool, tur any) map[string]any {
	r := map[string]any{"type": "user", "timestamp": ts, "message": map[string]any{"role": "user", "content": []map[string]any{
		{"type": "tool_result", "tool_use_id": id, "content": text, "is_error": isErr}}}}
	if tur != nil {
		r["toolUseResult"] = tur
	}
	return r
}

func sampleAgents(t *testing.T) string {
	root := t.TempDir()
	transcript(t, root, "-repo-project-x", "sess-1", []map[string]any{
		{"type": "ai-title", "aiTitle": "project-x build"},
		user(at(9, 0), "<persona-context kind=\"context\">background</persona-context>please build project-x for user-a"),
		asst(at(9, 1), map[string]any{"type": "thinking", "thinking": "plan"},
			map[string]any{"type": "tool_use", "id": "tu1", "name": "Bash", "input": map[string]any{"command": "make"}}),
		result(at(9, 2), "tu1", "make: error", true, nil),
		asst(at(9, 3), map[string]any{"type": "tool_use", "id": "tu2", "name": "Edit", "input": map[string]any{"file_path": "/repo/project-x/main.go"}}),
		result(at(9, 4), "tu2", "ok", false, nil),
		asst(at(9, 5), map[string]any{"type": "text", "text": "Fixed the build."},
			map[string]any{"type": "tool_use", "id": "tu3", "name": "AskUserQuestion", "input": map[string]any{"questions": []map[string]any{
				{"question": "deploy now?", "options": []map[string]any{{"label": "yes (Recommended)"}, {"label": "no"}}}}}}),
		result(at(9, 6), "tu3", "answered", false, map[string]any{"answers": map[string]string{"deploy now?": "no"}}),
		asst(at(9, 7), map[string]any{"type": "tool_use", "id": "tu4", "name": "mcp__persona__ask_user", "input": map[string]any{"questions": []map[string]any{
			{"question": "which port?", "options": []map[string]any{{"label": "8080"}, {"label": "9090"}}}}}}),
		result(at(9, 8), "tu4", "The user answered:\n- which port? → 9090\n", false, nil),
		user(at(9, 9), "<pasted_content id=\"x\">some pasted log</pasted_content id=\"x\">"),
		user(at(9, 10), "<command-name>/clear</command-name>"),
		user(at(9, 11), "thanks, that is right"),
		user(at(9, 12), "Persona relayed: please check the deploy of project-x on machine-a and report back"),
	})
	transcript(t, root, "-assistant", "sess-main", []map[string]any{user(at(8, 0), "hello"),
		asst(at(9, 12), map[string]any{"type": "tool_use", "id": "rl", "name": "mcp__persona__agent_send", "input": map[string]any{
			"machine": "m1", "session_id": "sess-1", "text": "please check the deploy of project-x on machine-a and report back"}})})
	return root
}

func TestOfflineSessionsAndItems(t *testing.T) {
	o := &Offline{AgentsDir: sampleAgents(t), AssistantDirs: []string{"-assistant"}, Machine: "m1"}
	ctx := context.Background()
	ss, err := o.Sessions(ctx, time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC))
	if err != nil || len(ss) != 2 {
		t.Fatalf("sessions: %+v %v", ss, err)
	}
	var work Session
	for _, s := range ss {
		if s.ID == "sess-1" {
			work = s
		} else if !s.Assistant {
			t.Error("the assistant's own session is marked")
		}
	}
	if work.Title != "project-x build" || !work.ByPersona || work.Machine != "m1" {
		t.Errorf("session: %+v", work)
	}
	all, err := o.Items(ctx, "m1", "sess-1", ItemsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	folded, _ := o.Items(ctx, "m1", "sess-1", ItemsQuery{Fold: true})
	kinds := map[string]int{}
	var folds []*Fold
	for _, it := range folded {
		kinds[it.Kind]++
		if it.Kind == KindFold {
			folds = append(folds, it.Fold)
		}
		if it.Kind == KindToolUse || it.Kind == KindToolResult || it.Kind == KindThinking {
			t.Errorf("a folded page carries %s", it.Kind)
		}
	}
	// Notices: the persona-context task, the paste, and the text the assistant relayed with agent_send.
	if kinds[KindUser] != 1 || kinds[KindNotice] != 3 || kinds[KindAgent] != 1 || len(folds) != 2 {
		t.Fatalf("folded kinds %v (all %d)", kinds, len(all))
	}
	f := folds[0]
	if f.Calls != 2 || f.Errors != 1 || len(f.Files) != 1 || f.Files[0] != "/repo/project-x/main.go" || len(f.Tools) != 2 || !f.Known {
		t.Errorf("first fold: %+v", f)
	}
	// after_seq: only what came after.
	after, _ := o.Items(ctx, "m1", "sess-1", ItemsQuery{Fold: true, After: f.To})
	for _, it := range after {
		if it.Seq <= f.To {
			t.Errorf("item %d at or before after_seq %d", it.Seq, f.To)
		}
	}
	// A segment, unfolded: exactly its tool calls and results.
	seg, _ := o.Items(ctx, "m1", "sess-1", ItemsQuery{From: f.From, Through: f.To})
	if len(seg) == 0 || seg[0].Seq != f.From || seg[len(seg)-1].Seq != f.To {
		t.Fatalf("segment: %+v", seg)
	}
	calls := 0
	for _, it := range seg {
		if it.Kind == KindToolUse {
			calls++
		}
	}
	if calls != 2 {
		t.Errorf("segment calls: %d", calls)
	}
	o.FoldUnknown = true
	folded, _ = o.Items(ctx, "m1", "sess-1", ItemsQuery{Fold: true})
	for _, it := range folded {
		if it.Kind == KindFold && (it.Fold.Known || it.Fold.Errors != 0 || len(it.Fold.Tools) != 0) {
			t.Errorf("a hub without H3 gives no error count: %+v", it.Fold)
		}
	}
}

func TestOfflineInteractions(t *testing.T) {
	o := &Offline{AgentsDir: sampleAgents(t), Machine: "m1"}
	cs, err := o.Interactions(context.Background(), InteractionsQuery{})
	if err != nil || len(cs) != 2 {
		t.Fatalf("cards: %+v %v", cs, err)
	}
	if cs[0].Question != "deploy now?" || cs[0].Recommend != "yes (Recommended)" || cs[0].Answers[0] != "no" || cs[0].AnsweredBy != "user" || cs[0].Session != "sess-1" {
		t.Errorf("AskUserQuestion: %+v", cs[0])
	}
	if cs[1].Question != "which port?" || len(cs[1].Answers) != 1 || cs[1].Answers[0] != "9090" {
		t.Errorf("ask_user: %+v", cs[1])
	}
	none, _ := o.Interactions(context.Background(), InteractionsQuery{Since: time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)})
	if len(none) != 0 {
		t.Errorf("since: %+v", none)
	}
}

func TestOfflineMissing(t *testing.T) {
	o := &Offline{}
	ctx := context.Background()
	if _, err := o.Sessions(ctx, time.Time{}); !errors.Is(err, ErrNotConnected) {
		t.Errorf("sessions: %v", err)
	}
	if _, err := o.Todos(ctx, time.Time{}); !errors.Is(err, ErrNotConnected) {
		t.Errorf("todos: %v", err)
	}
	if _, err := o.AboutMe(ctx); !errors.Is(err, ErrNotConnected) {
		t.Errorf("about: %v", err)
	}
	if _, err := o.Memories(ctx, true); !errors.Is(err, ErrNotConnected) {
		t.Errorf("memories: %v", err)
	}
	// A memory directory that is not there: memory off, an empty list.
	o.MemoryDir = filepath.Join(t.TempDir(), "none")
	if ms, err := o.Memories(ctx, true); err != nil || len(ms) != 0 {
		t.Errorf("no memory dir: %v %v", ms, err)
	}
}

func TestOfflineMemories(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("- [x](x.md)\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "port-rule.md"), []byte("---\nname: port-rule\ndescription: use port 9090 on machine-a\nmetadata:\n  type: feedback\n---\n\nAlways use 9090.\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "plain.md"), []byte("no frontmatter here\n"), 0o600)
	o := &Offline{MemoryDir: dir}
	ms, err := o.Memories(context.Background(), true)
	if err != nil || len(ms) != 2 {
		t.Fatalf("memories (index left out): %+v %v", ms, err)
	}
	for _, m := range ms {
		switch m.Name {
		case "port-rule.md":
			if m.Description != "use port 9090 on machine-a" || m.Type != "feedback" || strings.TrimSpace(m.Content) != "Always use 9090." {
				t.Errorf("frontmatter: %+v", m)
			}
		case "plain.md":
			if m.Description != "" || m.Type != "" || m.Content == "" {
				t.Errorf("no frontmatter: %+v", m)
			}
		}
		if m.Modified.IsZero() || m.Size == 0 {
			t.Errorf("times and size: %+v", m)
		}
	}
	ms, _ = o.Memories(context.Background(), false)
	if ms[0].Content != "" {
		t.Error("content without include_content")
	}
}

func TestUserWords(t *testing.T) {
	cases := []struct{ in, origin, notice string }{
		{"fix the build", "human", ""},
		{"<system-reminder>x</system-reminder>fix it", "human", ""},
		{"<persona-context kind=\"x\">y</persona-context><pasted_content id=\"a\">task</pasted_content id=\"a\">", "human", "persona"},
		{"<pasted_content id=\"a\">a log</pasted_content id=\"a\">", "human", "paste"},
		{"<command-name>/clear</command-name>", "", "command"},
		{"关于你上面的问题，我的回答： - 选 A", "human", "persona"},
		{"<task-notification>done</task-notification>", "task-notification", "notification"},
	}
	for _, c := range cases {
		if _, n := UserWords(c.in, c.origin); n != c.notice {
			t.Errorf("%q: notice %q, want %q", c.in, n, c.notice)
		}
	}
	if text, n := UserWords("see <pasted_content id=\"a\">abc</pasted_content id=\"a\"> please", "human"); n != "" || !strings.Contains(text, "[粘贴材料 3 字]") {
		t.Errorf("own words around a paste: %q %q", text, n)
	}
}

// HubNow answers as today's hub: memory waits for H2, a range of cards and fold error counts wait for H3.
func TestOfflineHubNow(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.md"), []byte("x"), 0o600)
	o := &Offline{AgentsDir: sampleAgents(t), MemoryDir: dir, Machine: "m1", HubNow: true}
	ctx := context.Background()
	var w *WaitingHub
	if _, err := o.Memories(ctx, true); !errors.As(err, &w) || w.Change != "H2" {
		t.Errorf("memories: %v", err)
	}
	cs, err := o.Interactions(ctx, InteractionsQuery{Since: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)})
	if !errors.As(err, &w) || w.Change != "H3" || len(cs) != 2 {
		t.Errorf("cards: %d %v", len(cs), err)
	}
	items, _ := o.Items(ctx, "m1", "sess-1", ItemsQuery{Fold: true})
	for _, it := range items {
		if it.Kind == KindFold && it.Fold.Known {
			t.Errorf("fold known before H3: %+v", it.Fold)
		}
	}
}
