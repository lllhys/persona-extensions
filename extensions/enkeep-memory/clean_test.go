package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"encoding/json"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona/sdk/go/wire"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// oldState writes a knowledge base and a state of version 1 (before source roles) and returns their paths and the
// state as written.
func oldState(t *testing.T) (string, string, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	kb := filepath.Join(dir, "kb")
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(kb, "owner.json"), `{"name":"用户","aliases":["demo-owner"]}`)
	write(filepath.Join(kb, "chats/github-acme-app-7/meta.json"), `{"id":"github-acme-app-7","source":"github","kind":"pr","name":"app#7"}`)
	write(filepath.Join(kb, "chats/github-acme-app-7/messages/2026-11-06.md"),
		"# x\n\n- 10:00 我: Fix the window\n  \n  🤖 Generated with [Claude Code](https://claude.com/claude-code)\n- 10:05 我: Under the workflow rules for CI that never runs, merged on local results.\n")
	write(filepath.Join(kb, "chats/persona-ab12/meta.json"), `{"id":"persona-ab12","source":"persona","kind":"assistant","name":"Persona"}`)
	write(filepath.Join(kb, "chats/persona-ab12/messages/2026-11-06.md"),
		"# x\n\n- 09:00 我: 以后汇报都先说结论\n- 09:01 助手: 好的，以后先给结论。\n- 09:02 助手: 你之前说过部署都先灰度\n- 09:03 我: 好\n")
	write(filepath.Join(kb, "chats/lark-grp/meta.json"), `{"id":"lark-grp","source":"lark","kind":"group","name":"G"}`)
	write(filepath.Join(kb, "chats/lark-grp/messages/2026-11-06.md"),
		"# x\n\n- 08:00 CI Bot: @demo-owner 构建失败\n- 08:05 我: 周五前我把压测报告发出来\n- 08:06 Avery: 下周迁移集群\n")

	ev := func(chat string, n int, quote string) map[string]any {
		return map[string]any{"day": "2026-11-06", "source": chat, "anchor": chat + "#" + itoa(n), "quote": quote, "speaker": "user"}
	}
	state := map[string]any{
		"version":   1,
		"old_field": "kept as it is",
		"memory": map[string]any{"entries": map[string]any{
			"m-agent": map[string]any{"id": "m-agent", "text": "CI 不调度时按本地验证合并", "evidence": []any{ev("chats/github-acme-app-7", 6, "merged on local results")}},
			"m-mixed": map[string]any{"id": "m-mixed", "text": "汇报先说结论", "x_unknown": 1, "evidence": []any{
				ev("chats/persona-ab12", 3, "以后汇报都先说结论"), ev("chats/persona-ab12", 4, "好的，以后先给结论")}},
			"m-agreed": map[string]any{"id": "m-agreed", "text": "部署都先灰度", "evidence": []any{
				ev("chats/persona-ab12", 5, "部署都先灰度"), ev("chats/persona-ab12", 6, "好")}},
		}, "conflicts": []any{map[string]any{"a": "m-agent", "b": "m-mixed"}}},
		"items": map[string]any{
			"i-bot": map[string]any{"id": "i-bot", "claim": "用户的构建失败了", "status": "active", "evidence": []any{
				map[string]any{"chat": "chats/lark-grp", "day": "2026-11-06", "n": 3, "speaker": "CI Bot", "quote": "构建失败"}}},
			"i-others": map[string]any{"id": "i-others", "claim": "群里说下周迁移集群", "status": "active", "class": "fact", "evidence": []any{
				map[string]any{"chat": "chats/lark-grp", "day": "2026-11-06", "n": 5, "speaker": "Avery", "quote": "下周迁移集群"}}},
			"i-own": map[string]any{"id": "i-own", "claim": "用户周五前发压测报告", "status": "active", "evidence": []any{
				map[string]any{"chat": "chats/lark-grp", "day": "2026-11-06", "n": 4, "speaker": "我", "quote": "周五前我把压测报告发出来"}}},
		},
		"proposals": []any{
			map[string]any{"id": "K-1", "kind": "pin_fact", "status": "pending", "items": []any{"i-bot"}},
			map[string]any{"id": "K-2", "kind": "pin_fact", "status": "pending", "items": []any{"i-own"}},
			map[string]any{"id": "K-3", "kind": "remove_alias", "status": "pending"},
		},
	}
	raw, _ := json.Marshal(state)
	sd := filepath.Join(dir, "state")
	write(filepath.Join(sd, "state.json"), string(raw))
	return dir, kb, sd, raw
}

// A state written before roles: a memory entry built on an agent's pull request text, one on the user's words with
// the assistant's reply cited too, an item from a bot, an item on the user's words from one chat, a pin proposal for
// each item. clean-sources drops the agent's and the bot's, trims the assistant's line, makes the single-source item
// a candidate, expires both proposals, keeps every field it does not know, backs up first and keeps three backups.
func TestCleanSources(t *testing.T) {
	dir, kb, sd, raw := oldState(t)
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, old := range []string{"20260101T000000Z", "20260102T000000Z", "20260103T000000Z"} {
		write(filepath.Join(sd, "state.json.bak-"+old), "{}")
	}
	report := filepath.Join(dir, "report.json")

	// A dry run writes nothing.
	if err := cleanSources([]string{"-state", sd, "-kb", kb, "-dry"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(sd, "state.json")); string(got) != string(raw) {
		t.Fatal("a dry run changed the state")
	}
	if err := cleanSources([]string{"-state", sd, "-kb", kb, "-report", report}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		OldField string `json:"old_field"`
		Memory   struct {
			Entries   map[string]map[string]any `json:"entries"`
			Conflicts []any                     `json:"conflicts"`
		} `json:"memory"`
		Items     map[string]map[string]any `json:"items"`
		Proposals []map[string]any          `json:"proposals"`
	}
	b, _ := os.ReadFile(filepath.Join(sd, "state.json"))
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.OldField != "kept as it is" {
		t.Error("a field this build does not know was lost")
	}
	if _, ok := got.Memory.Entries["m-agent"]; ok {
		t.Error("the entry on the agent's pull request text stays")
	}
	if _, ok := got.Memory.Entries["m-agreed"]; ok {
		t.Error("the entry on the assistant's words and the user's 好 stays")
	}
	m := got.Memory.Entries["m-mixed"]
	if evs, _ := m["evidence"].([]any); len(evs) != 1 || m["x_unknown"] == nil {
		t.Errorf("m-mixed: %+v", m)
	}
	if len(got.Memory.Conflicts) != 0 {
		t.Errorf("a conflict with a removed entry stays: %v", got.Memory.Conflicts)
	}
	if _, ok := got.Items["i-bot"]; ok {
		t.Error("the bot's item stays")
	}
	if it := got.Items["i-others"]; it == nil || it["status"] != "candidate" {
		t.Errorf("an item on other people's words: %v", it)
	}
	if it := got.Items["i-own"]; it["status"] != "candidate" {
		t.Errorf("the single-source item: %v", it["status"])
	} else if evs, _ := it["evidence"].([]any); evs[0].(map[string]any)["role"] != "user" {
		t.Errorf("no role: %v", evs)
	}
	status := map[string]string{}
	for _, p := range got.Proposals {
		status[p["id"].(string)] = p["status"].(string)
	}
	if status["K-1"] != "expired" || status["K-2"] != "expired" || status["K-3"] != "pending" {
		t.Errorf("proposals: %v", status)
	}
	baks, _ := filepath.Glob(filepath.Join(sd, "state.json.bak-*"))
	if len(baks) != 3 || strings.HasSuffix(baks[0], "20260101T000000Z") {
		t.Errorf("backups: %v", baks)
	}
	if last, _ := os.ReadFile(baks[len(baks)-1]); string(last) != string(raw) {
		t.Error("the newest backup is not the state before")
	}
	var rep cleanReport
	rb, _ := os.ReadFile(report)
	if json.Unmarshal(rb, &rep) != nil || rep.Before.Memory != 3 || rep.After.Memory != 1 || rep.Before.Items != 3 || rep.After.Items != 2 ||
		rep.Demoted != 2 || rep.ProposalsExpired != 2 || len(rep.Samples) == 0 {
		t.Errorf("report: %+v", rep)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// On start, a state of version 1 is cleaned once, in the running extension: backed up next to itself, cleaned as
// clean-sources does, version 2, the counts kept for the health page; the next start does nothing; a knowledge base
// that cannot be read changes nothing.
func TestMigrateSourcesOnStart(t *testing.T) {
	_, kb, sd, raw := oldState(t)
	fx, err := kbsrc.LoadFixture(kb)
	if err != nil {
		t.Fatal(err)
	}
	// A knowledge base that fails: nothing changes.
	st, err := store.Open(sd)
	if err != nil {
		t.Fatal(err)
	}
	if sc, err := migrateSources(context.Background(), st, failing{fx}, time.Now()); err == nil || sc != nil || st.S.Version != 1 {
		t.Fatalf("a failing knowledge base: %v %v version %d", sc, err, st.S.Version)
	}
	if got, _ := os.ReadFile(filepath.Join(sd, "state.json")); string(got) != string(raw) {
		t.Fatal("the state changed on a failed clean")
	}
	sc, err := migrateSources(context.Background(), st, fx, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))
	if err != nil || sc == nil {
		t.Fatalf("clean: %v %v", sc, err)
	}
	if st.S.Version != store.Version || st.S.SourceClean == nil || sc.MemoryBefore != 3 || sc.MemoryAfter != 1 ||
		sc.ItemsBefore != 3 || sc.ItemsAfter != 2 || sc.Removed["memory:agent"] != 1 || sc.Removed["memory:assistant"] != 1 ||
		sc.Removed["item:bot"] != 1 || sc.Candidates != 2 || sc.ProposalsExpired != 2 {
		t.Fatalf("clean: %+v", sc)
	}
	if b, _ := os.ReadFile(sc.Backup); string(b) != string(raw) {
		t.Error("the backup is not the state before")
	}
	again, err := store.Open(sd)
	if err != nil || again.S.Version != store.Version || again.S.SourceClean == nil {
		t.Fatalf("saved: %v %+v", err, again.S.SourceClean)
	}
	if sc2, err := migrateSources(context.Background(), again, fx, time.Now()); sc2 != nil || err != nil {
		t.Errorf("a second start cleaned again: %v %v", sc2, err)
	}
	sec := sourceCleanSection(sc)
	if !strings.Contains(sec.Title, "2026-11-09") || len(sec.Items) != 2 {
		t.Errorf("health section: %+v", sec)
	}
}

func TestMigrateSourcesForgetScrubsBackups(t *testing.T) {
	dir := t.TempDir()
	kb := filepath.Join(dir, "kb")
	sd := filepath.Join(dir, "state")
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(kb, "owner.json"), `{"name":"Demo Owner"}`)
	for _, chat := range []struct {
		id, source, quote string
	}{
		{"lark-forget", "lark", "LARK_QUOTE_MARKER"},
		{"wechat-keep", "wechat", "OTHER_QUOTE_MARKER"},
	} {
		write(filepath.Join(kb, "chats", chat.id, "meta.json"), fmt.Sprintf(
			`{"id":%q,"source":%q,"kind":"dm","name":"Synthetic chat"}`, chat.id, chat.source))
		write(filepath.Join(kb, "chats", chat.id, "messages", "2026-11-06.md"),
			"# synthetic\n\n- 08:00 我: "+chat.quote+"\n")
	}

	const day = "2026-11-06"
	itemEv := func(chat, quote string) map[string]any {
		return map[string]any{"chat": "chats/" + chat, "day": day, "n": 3, "speaker": "user", "quote": quote}
	}
	memoryEv := func(chat, quote string) map[string]any {
		return map[string]any{"day": day, "source": "chats/" + chat, "anchor": "chats/" + chat + "#3", "quote": quote, "speaker": "user"}
	}
	state := map[string]any{
		"version": 1,
		"memory": map[string]any{"entries": map[string]any{
			"m-lark":  map[string]any{"id": "m-lark", "text": "LARK_MEMORY_MARKER", "layer": "fact", "evidence": []any{memoryEv("lark-forget", "LARK_QUOTE_MARKER")}},
			"m-other": map[string]any{"id": "m-other", "text": "OTHER_MEMORY_MARKER", "layer": "fact", "evidence": []any{memoryEv("wechat-keep", "OTHER_QUOTE_MARKER")}},
		}},
		"items": map[string]any{
			"i-lark":  map[string]any{"id": "i-lark", "claim": "LARK_CLAIM_MARKER", "class": "fact", "status": "candidate", "evidence": []any{itemEv("lark-forget", "LARK_QUOTE_MARKER")}},
			"i-other": map[string]any{"id": "i-other", "claim": "OTHER_CLAIM_MARKER", "class": "fact", "status": "candidate", "evidence": []any{itemEv("wechat-keep", "OTHER_QUOTE_MARKER")}},
		},
		"done": map[string]any{
			"lark-call":  map[string]any{"key": "lark-call", "step": "daily", "chat": "chats/lark-forget", "day": day, "raw": "lark-call.json"},
			"other-call": map[string]any{"key": "other-call", "step": "daily", "chat": "chats/wechat-keep", "day": day, "raw": "other-call.json"},
		},
		"rejections": []any{
			map[string]any{"key": "lark-call", "claim": "LARK_REJECTION_MARKER"},
			map[string]any{"key": "other-call", "claim": "OTHER_REJECTION_MARKER"},
		},
		"digests": map[string]any{
			"chats/lark-forget@" + day: map[string]any{"chat": "chats/lark-forget", "day": day, "points": []any{
				map[string]any{"text": "LARK_DIGEST_MARKER", "evidence": []any{itemEv("lark-forget", "LARK_QUOTE_MARKER")}},
			}},
			"chats/wechat-keep@" + day: map[string]any{"chat": "chats/wechat-keep", "day": day, "points": []any{
				map[string]any{"text": "OTHER_DIGEST_MARKER", "evidence": []any{itemEv("wechat-keep", "OTHER_QUOTE_MARKER")}},
			}},
		},
		"triage": map[string]any{
			"chats/lark-forget@" + day: map[string]any{"chat": "chats/lark-forget", "reason": "LARK_TRIAGE_MARKER"},
			"chats/wechat-keep@" + day: map[string]any{"chat": "chats/wechat-keep", "reason": "OTHER_TRIAGE_MARKER"},
		},
		"proposals": []any{
			map[string]any{"id": "K-LARK", "kind": "pin_fact", "status": "pending", "target": "chats/lark-forget", "items": []any{"i-lark"}, "evidence": []any{itemEv("lark-forget", "LARK_PROPOSAL_QUOTE_MARKER")}},
			map[string]any{"id": "K-OTHER", "kind": "pin_fact", "status": "pending", "target": "chats/wechat-keep", "items": []any{"i-other"}, "evidence": []any{itemEv("wechat-keep", "OTHER_PROPOSAL_QUOTE_MARKER")}},
		},
		"runs": []any{map[string]any{
			"id": "synthetic-run", "kind": "weekly", "report": "LARK_REPORT_MARKER: LARK_CLAIM_MARKER\nOTHER_REPORT_MARKER",
			"refusals": []any{
				map[string]any{"unit": "chats/lark-forget", "reason": "LARK_REFUSAL_MARKER"},
				map[string]any{"unit": "chats/wechat-keep", "reason": "OTHER_REFUSAL_MARKER"},
			},
		}},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(sd, "state.json"), string(raw))
	for _, stamp := range []string{"20260101T000000Z", "20260102T000000Z"} {
		write(filepath.Join(sd, "state.json.bak-"+stamp), string(raw))
	}
	fx, err := kbsrc.LoadFixture(kb)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(sd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutRaw("lark-call", []byte(`{"text":"LARK_RAW_MARKER"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutRaw("other-call", []byte(`{"text":"OTHER_RAW_MARKER"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := migrateSources(context.Background(), st, fx, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(filepath.Join(sd, "state.json.bak-*"))
	if err != nil || len(backups) != 3 {
		t.Fatalf("migration backups: %d, %v", len(backups), err)
	}
	stateDocs := append([]string{filepath.Join(sd, "state.json")}, backups...)
	for _, path := range stateDocs {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "LARK_CLAIM_MARKER") || !strings.Contains(string(b), "OTHER_CLAIM_MARKER") {
			t.Fatalf("fixture lacks both sources in %s", filepath.Base(path))
		}
	}

	c := &curate.Curator{St: st, Cfg: curate.Defaults(), Loc: time.UTC, Now: func() time.Time {
		return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	}}
	if _, err := c.Forget("lark"); err != nil {
		t.Fatal(err)
	}

	larkMarkers := []string{
		"LARK_QUOTE_MARKER", "LARK_MEMORY_MARKER", "LARK_CLAIM_MARKER", "LARK_REJECTION_MARKER",
		"LARK_DIGEST_MARKER", "LARK_TRIAGE_MARKER", "LARK_PROPOSAL_QUOTE_MARKER", "LARK_REPORT_MARKER",
		"LARK_REFUSAL_MARKER", "LARK_RAW_MARKER",
	}
	if err := filepath.Walk(sd, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, marker := range larkMarkers {
			if strings.Contains(string(b), marker) {
				t.Errorf("%s remains in %s", marker, filepath.Base(path))
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	otherStateMarkers := []string{
		"OTHER_QUOTE_MARKER", "OTHER_MEMORY_MARKER", "OTHER_CLAIM_MARKER", "OTHER_REJECTION_MARKER",
		"OTHER_DIGEST_MARKER", "OTHER_TRIAGE_MARKER", "OTHER_PROPOSAL_QUOTE_MARKER", "OTHER_REPORT_MARKER",
		"OTHER_REFUSAL_MARKER",
	}
	for _, path := range stateDocs {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range otherStateMarkers {
			if !strings.Contains(string(b), marker) {
				t.Errorf("%s was removed from %s", marker, filepath.Base(path))
			}
		}
		var got store.State
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		status := map[string]string{}
		for _, p := range got.Proposals {
			status[p.ID] = p.Status
		}
		if status["K-LARK"] != "expired" || status["K-OTHER"] != "pending" {
			t.Errorf("proposals in %s: %v", filepath.Base(path), status)
		}
	}
	if _, err := os.Stat(filepath.Join(sd, "raw", "lark-call.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("forgotten raw answer still exists: %v", err)
	}
	otherRaw, err := os.ReadFile(filepath.Join(sd, "raw", "other-call.json"))
	if err != nil || !strings.Contains(string(otherRaw), "OTHER_RAW_MARKER") {
		t.Errorf("other raw answer was removed: %v", err)
	}
}

// failing is a knowledge base whose days cannot be read.
type failing struct{ kbsrc.Source }

func (failing) Day(context.Context, string, string) (wire.KBDayResult, error) {
	return wire.KBDayResult{}, errors.New("hub away")
}

// clean-sources -reject-others marks an item that rests only on other people's words, none naming the user, rejected
// (the default leaves it a candidate); the item, its citations and every other item stay, the state is backed up
// first, and a dry run writes nothing.
func TestCleanRejectOthers(t *testing.T) {
	_, kb, sd, raw := oldState(t)
	if err := cleanSources([]string{"-state", sd, "-kb", kb, "-dry", "-reject-others"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(sd, "state.json")); string(got) != string(raw) {
		t.Fatal("a dry run changed the state")
	}
	report := filepath.Join(t.TempDir(), "report.json")
	if err := cleanSources([]string{"-state", sd, "-kb", kb, "-reject-others", "-report", report}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Items map[string]map[string]any `json:"items"`
	}
	b, _ := os.ReadFile(filepath.Join(sd, "state.json"))
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if it := got.Items["i-others"]; it == nil || it["status"] != "rejected" || it["claim"] != "群里说下周迁移集群" {
		t.Errorf("an item on other people's words: %v", it)
	} else if evs, _ := it["evidence"].([]any); len(evs) != 1 {
		t.Errorf("its citations stay: %v", evs)
	}
	if it := got.Items["i-own"]; it == nil || it["status"] != "candidate" {
		t.Errorf("an item on the user's words is not touched by it: %v", it)
	}
	if baks, _ := filepath.Glob(filepath.Join(sd, "state.json.bak-*")); len(baks) != 1 {
		t.Errorf("backups: %v", baks)
	}
	var rep cleanReport
	rb, _ := os.ReadFile(report)
	if json.Unmarshal(rb, &rep) != nil || rep.Rejected != 1 {
		t.Errorf("report: %+v", rep)
	}
}
