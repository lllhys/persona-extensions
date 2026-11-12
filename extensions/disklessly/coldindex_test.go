package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Synthetic session ids; none of them belongs to a real conversation.
const (
	uuidClaude = "11111111-2222-4333-8444-555555555555"
	uuidCodex  = "0199aaaa-bbbb-7ccc-8ddd-eeeeffff0000"
)

func coldListJSON(entries ...map[string]any) string {
	b, _ := json.Marshal(map[string]any{"ok": true, "deviceId": "00000000-0000-4000-8000-000000000001", "entries": entries})
	return string(b)
}

var coldEntries = []map[string]any{
	{"id": "aaaaaaaaaaa1", "local": true, "tool": "claude", "path": "/home/dev/.claude/projects/-home-dev-shop/" + uuidClaude + ".jsonl",
		"sessionId": uuidClaude, "cwd": "/home/dev/shop", "project": "shop", "month": "2026-10", "mtime": "2026-10-20T08:00:00Z",
		"archivedAt": "2026-11-01T08:00:00Z", "bytes": 4096, "state": "archived", "title": "Fix the checkout flow",
		"firstPrompt": "Please fix the checkout flow\nwhen the cart is empty", "segment": "seg-a", "encrypted": false,
		"digest": "checkout cart checkout payment 购物车结算流程 购物车 the the the 2026"},
	{"id": "aaaaaaaaaaa2", "local": true, "tool": "codex", "path": "/home/dev/.codex/sessions/2026/10/01/rollout-2026-10-01T10-00-00-" + uuidCodex + ".jsonl",
		"sessionId": uuidCodex, "cwd": "/home/dev/blog", "project": "blog", "month": "2026-10", "mtime": "2026-10-01T10:00:00Z",
		"archivedAt": "2026-11-01T08:00:00Z", "bytes": 2048, "state": "archived", "title": "迁移博客到静态站点",
		"firstPrompt": "把博客迁移到静态站点生成器", "segment": "seg-missing", "digest": "静态站点 migration hugo hugo"},
	{"id": "aaaaaaaaaaa3", "local": true, "tool": "claude", "path": "/home/dev/.claude/projects/-home-dev-shop/old.jsonl",
		"sessionId": "old", "cwd": "/home/dev/shop", "project": "shop", "mtime": "2026-09-01T10:00:00Z",
		"bytes": 1024, "state": "restored", "title": "Old restored session", "segment": "seg-a"},
	{"id": "aaaaaaaaaaa4", "local": true, "tool": "claude", "path": "/home/dev/.claude/projects/-home-dev-shop/flaky.jsonl",
		"sessionId": "flaky", "cwd": "/home/dev/shop", "project": "shop", "mtime": "2026-08-01T10:00:00Z",
		"bytes": 1024, "state": "archived", "title": "Unchecked segment session", "segment": "seg-slow"},
}

const coldStatusJSON = `{"ok":true,"deviceId":"00000000-0000-4000-8000-000000000001","backend":{"id":"main","type":"rclone","reachable":true,"latencyMs":42,"error":""},"counts":{"archived":3,"remote":0,"restored":1,"keptLocal":0,"pendingDelete":0},"archivedBytes":7168,"segments":{"total":3,"checked":2,"missing":["seg-missing"],"unchecked":["seg-slow"],"timedOut":false}}`

func coldRefreshOut(list, status string) string {
	return "@@SECTION:COLD_LIST\n" + list + "\n@@SECTION:COLD_STATUS\n" + status + "\n@@SECTION:END\n"
}

const coldSearchOut = `@@SECTION:SEARCH
[{"id":"aaaaaaaaaaa1","tool":"claude","path":"/home/dev/x.jsonl","preview":"we talked about the refund webhook","title":"Fix the checkout flow","deviceId":"d","state":"archived","mtime":"2026-10-20T08:00:00Z","bytes":4096,"cwd":"/home/dev/shop","sessionId":"` + uuidClaude + `"},
 {"id":"bbbbbbbbbbb1","tool":"codex","path":"/home/dev/.codex/sessions/old.jsonl","preview":"refund webhook retries","title":"Webhook retries","deviceId":"d","state":"archived","mtime":"2025-01-01T00:00:00Z","bytes":10,"cwd":"/home/dev/pay","sessionId":"webhook-session"}]
@@SECTION:END
`

func restoreOut(rc int, present bool) string {
	s := fmt.Sprintf("@@RESTORE_RC %d\n", rc)
	if rc == 0 {
		s += `@@RESTORE_JSON {"ok":true,"path":"/home/dev/.claude/projects/-home-dev-shop/` + uuidClaude + `.jsonl","id":"aaaaaaaaaaa1","sessionId":"` + uuidClaude + `"}` + "\n"
	} else {
		s += `@@RESTORE_JSON {"ok":false,"error":{"code":4,"message":"backend unreachable: timeout","name":"EX_BACKEND"}}` + "\n"
	}
	if present {
		s += "@@RESTORE_PRESENT\n"
	}
	return s
}

// coldRunner answers the cold-index scripts with synthetic CLI output and defers the rest to fakeRunner.
func coldRunner(scripts *[]string) Runner {
	return func(ctx context.Context, m *Machine, s string, d time.Duration) (string, error) {
		if scripts != nil {
			*scripts = append(*scripts, s)
		}
		switch {
		case strings.Contains(s, "sessions list --state all"):
			return coldRefreshOut(coldListJSON(coldEntries...), coldStatusJSON), nil
		case strings.Contains(s, "sessions search"):
			return coldSearchOut, nil
		case strings.Contains(s, "sessions restore"):
			return restoreOut(0, true), nil
		case strings.Contains(s, "@@REFS_OK"):
			return "@@REF " + uuidClaude + "\n@@REFS_OK\n", nil
		}
		return fakeRunner(ctx, m, s, d)
	}
}

func coldState(t *testing.T) (*State, *Machine) {
	t.Helper()
	st, m := scanFor(t, sampleProbeLocal)
	if _, err := RefreshColdIndex(context.Background(), coldRunner(nil), st, m, t0); err != nil {
		t.Fatal(err)
	}
	return st, m
}

func TestColdIndexRefreshStoresLightEntries(t *testing.T) {
	st, m := coldState(t)
	cat := st.Cold[m.ID]
	if cat == nil || len(cat.Entries) != 4 || cat.Total != 4 || cat.Capped {
		t.Fatalf("catalog: %+v", cat)
	}
	if cat.Backend.Reachable == nil || !*cat.Backend.Reachable || cat.Backend.Type != "rclone" || !cat.Backend.CheckedAt.Equal(t0) {
		t.Fatalf("backend: %+v", cat.Backend)
	}
	if cat.Entries[0].ID != "aaaaaaaaaaa1" || cat.Entries[3].ID != "aaaaaaaaaaa4" {
		t.Fatalf("not newest first: %v %v", cat.Entries[0].ID, cat.Entries[3].ID)
	}
	e := cat.Entries[0]
	if e.FirstPrompt != "Please fix the checkout flow when the cart is empty" {
		t.Fatalf("first prompt: %q", e.FirstPrompt)
	}
	if strings.Join(e.Keywords, ",") != "checkout,cart,payment,购物车结算流,购物车" {
		t.Fatalf("keywords: %v", e.Keywords)
	}
	raw, _ := json.Marshal(st)
	if strings.Contains(string(raw), "digest") || strings.Contains(string(raw), "购物车结算流程") {
		t.Fatal("digest stored in state")
	}

	// Long titles and prompts are clipped; more than 5000 entries are capped, newest first.
	var many []map[string]any
	for i := range maxColdEntries + 3 {
		many = append(many, map[string]any{"id": fmt.Sprintf("%012x", i), "tool": "claude", "state": "archived",
			"mtime": t0.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339), "title": strings.Repeat("长", 300),
			"firstPrompt": strings.Repeat("p", 500)})
	}
	run := func(ctx context.Context, mm *Machine, s string, d time.Duration) (string, error) {
		return coldRefreshOut(coldListJSON(many...), coldStatusJSON), nil
	}
	cat, err := RefreshColdIndex(context.Background(), run, st, m, t0)
	if err != nil {
		t.Fatal(err)
	}
	if !cat.Capped || len(cat.Entries) != maxColdEntries || cat.Total != maxColdEntries+3 || cat.Entries[0].ID != "000000000000" {
		t.Fatalf("cap: capped=%v n=%d total=%d first=%s", cat.Capped, len(cat.Entries), cat.Total, cat.Entries[0].ID)
	}
	if n := utf8.RuneCountInString(cat.Entries[0].Title); n != coldTitleRunes {
		t.Fatalf("title runes %d", n)
	}
	if n := utf8.RuneCountInString(cat.Entries[0].FirstPrompt); n != coldPromptRunes {
		t.Fatalf("prompt runes %d", n)
	}

	// A failed refresh keeps the entries and makes the backend unknown.
	fail := func(ctx context.Context, mm *Machine, s string, d time.Duration) (string, error) {
		return "", fmt.Errorf("SSH 连接失败")
	}
	cat, err = RefreshColdIndex(context.Background(), fail, st, m, t0)
	if err == nil || cat.Backend.Reachable != nil || len(cat.Entries) != maxColdEntries || cat.Error == "" {
		t.Fatalf("failed refresh: err=%v backend=%+v", err, cat.Backend)
	}
}

func TestExtractKeywords(t *testing.T) {
	got := extractKeywords("The the build build build Parser parser 42 2026 ab 修复解析器的内存泄漏问题 好 数据库 数据库", 16)
	want := "build,parser,数据库,修复解析器的"
	if strings.Join(got, ",") != want {
		t.Fatalf("keywords %v, want %s", got, want)
	}
	if extractKeywords("", 16) != nil {
		t.Fatal("empty digest")
	}
}

func TestColdLocations(t *testing.T) {
	st, m := coldState(t)
	cat := st.Cold[m.ID]
	loc := func(id string, now time.Time) string {
		for _, e := range cat.Entries {
			if e.ID == id {
				return cat.location(e, m, now)
			}
		}
		t.Fatalf("no entry %s", id)
		return ""
	}
	for id, want := range map[string]string{
		"aaaaaaaaaaa1": LocCloud, "aaaaaaaaaaa2": LocMissing, "aaaaaaaaaaa3": LocLocal, "aaaaaaaaaaa4": LocUnknown,
	} {
		if got := loc(id, t0); got != want {
			t.Fatalf("%s: %s, want %s", id, got, want)
		}
	}
	if got := loc("aaaaaaaaaaa1", t0.Add(25*time.Hour)); got != LocUnknown {
		t.Fatalf("stale check: %s", got)
	}
	m.Reachable = false
	if got := loc("aaaaaaaaaaa1", t0); got != LocUnknown {
		t.Fatalf("offline machine: %s", got)
	}
	m.Reachable = true
	f := false
	cat.Backend.Reachable = &f
	if got := loc("aaaaaaaaaaa1", t0); got != LocUnknown {
		t.Fatalf("unreachable backend: %s", got)
	}
	cat.Backend.Reachable = nil
	if got := loc("aaaaaaaaaaa1", t0); got != LocUnknown {
		t.Fatalf("unknown backend: %s", got)
	}
	// A Missing list left from an earlier check never reports 云端缺失 once the backend is
	// unreachable, unknown or the check is stale.
	tr := true
	cat.Backend.Reachable = &tr
	if got := loc("aaaaaaaaaaa2", t0.Add(25*time.Hour)); got != LocUnknown {
		t.Fatalf("stale missing: %s", got)
	}
	cat.Backend.Reachable = &f
	if got := loc("aaaaaaaaaaa2", t0); got != LocUnknown {
		t.Fatalf("missing with unreachable backend: %s", got)
	}
	cat.Backend.Reachable = nil
	if got := loc("aaaaaaaaaaa2", t0); got != LocUnknown {
		t.Fatalf("missing after failed refresh: %s", got)
	}
	m.Reachable = false
	cat.Backend.Reachable = &tr
	if got := loc("aaaaaaaaaaa2", t0); got != LocUnknown {
		t.Fatalf("missing on offline machine: %s", got)
	}
	m.Reachable = true
	if got := loc("aaaaaaaaaaa2", t0); got != LocMissing {
		t.Fatalf("missing after a fresh successful check: %s", got)
	}
	var never *ColdCatalog
	if got := never.location(cat.Entries[0], m, t0); got != LocUnknown {
		t.Fatalf("never checked: %s", got)
	}
}

func TestSearchCold(t *testing.T) {
	st, m := coldState(t)
	m.Name = "dev box"
	ids := func(hits []ColdHit) string {
		var s []string
		for _, h := range hits {
			s = append(s, h.ID)
		}
		return strings.Join(s, ",")
	}
	for q, want := range map[string]string{
		"":                 "aaaaaaaaaaa1,aaaaaaaaaaa2,aaaaaaaaaaa3,aaaaaaaaaaa4",
		"CHECKOUT":         "aaaaaaaaaaa1",
		"checkout payment": "aaaaaaaaaaa1",
		"checkout hugo":    "",
		"静态站点":             "aaaaaaaaaaa2",
		"博客":               "aaaaaaaaaaa2",
		"codex":            "aaaaaaaaaaa2",
		"dev box shop":     "aaaaaaaaaaa1,aaaaaaaaaaa3,aaaaaaaaaaa4",
		uuidClaude[:8]:     "aaaaaaaaaaa1",
	} {
		if got := ids(SearchCold(st, q, "", 0, t0)); got != want {
			t.Fatalf("search %q: %s, want %s", q, got, want)
		}
	}
	if got := SearchCold(st, "shop", "local", 2, t0); len(got) != 2 || got[0].Location != LocCloud || got[0].MachineName != "dev box" {
		t.Fatalf("limited: %+v", got)
	}
	if got := SearchCold(st, "shop", "nope", 0, t0); got != nil {
		t.Fatalf("unknown machine: %+v", got)
	}

	var scripts []string
	hits, errs := DeepSearchCold(context.Background(), coldRunner(&scripts), st, "refund webhook", "local", 10, t0)
	if len(errs) != 0 || ids(hits) != "aaaaaaaaaaa1,bbbbbbbbbbb1" || !hits[0].Deep || !strings.Contains(hits[0].Preview, "refund") {
		t.Fatalf("deep: %s %v %+v", ids(hits), errs, hits)
	}
	if hits[0].Title != "Fix the checkout flow" || hits[1].Location != LocUnknown {
		t.Fatalf("deep merge: %+v", hits)
	}
	if len(scripts) != 1 || !strings.Contains(scripts[0], "sessions search 'refund' 'webhook' --limit 10") {
		t.Fatalf("deep script: %v", scripts)
	}
}

func TestRestoreCold(t *testing.T) {
	st, m := coldState(t)
	var scripts []string
	e, hint, err := RestoreCold(context.Background(), coldRunner(&scripts), st, m, "aaaaaaaaaaa1", t0)
	if err != nil {
		t.Fatal(err)
	}
	if hint != "claude --resume "+uuidClaude || e.State != "restored" {
		t.Fatalf("restore: %+v %s", e, hint)
	}
	if st.Cold[m.ID].location(st.Cold[m.ID].Entries[0], m, t0) != LocLocal {
		t.Fatal("restored entry not local")
	}
	if len(st.History) != 1 || !strings.Contains(st.History[0].Summary, hint) {
		t.Fatalf("history: %+v", st.History)
	}
	s := scripts[0]
	if !strings.Contains(s, "sessions restore 'aaaaaaaaaaa1' 2>") || !strings.Contains(s, "[ -s '/home/dev/.claude/projects/") || strings.Contains(s, "--to") {
		t.Fatalf("restore script:\n%s", s)
	}
	if got := resumeHint(ColdEntry{Tool: "codex", SessionID: uuidCodex}); got != "codex resume "+uuidCodex {
		t.Fatal(got)
	}
	// Agents without a resume-by-id command get no (wrong) hint.
	if got := resumeHint(ColdEntry{Tool: "pi", SessionID: "x"}); got != "" {
		t.Fatalf("pi hint %q", got)
	}
	// Another device's row is refused: its original path belongs to that device.
	cat := st.Cold[m.ID]
	cat.Entries = append(cat.Entries, ColdEntry{ID: "ffffffffff01", Path: "/Users/other/.claude/projects/-x/s.jsonl",
		Tool: "claude", State: "remote", DeviceID: "00000000-0000-4000-8000-000000000009"})
	scripts = nil
	if _, _, err := RestoreCold(context.Background(), coldRunner(&scripts), st, m, "ffffffffff01", t0); err == nil ||
		!strings.Contains(err.Error(), "另一台设备") || len(scripts) != 0 {
		t.Fatalf("foreign restore: %v %d", err, len(scripts))
	}
	if s := restoreScript(ColdEntry{ID: "x", Path: "/home/dev/a b.jsonl"}); strings.Contains(s, "--to") || !strings.Contains(s, "[ -s '/home/dev/a b.jsonl' ]") {
		t.Fatalf("restore script:\n%s", s)
	}
	// An id beyond the catalog (a full-text hit past the cap) is restored by id, its returned
	// path is checked, and it joins the catalog as local.
	scripts = nil
	run := func(ctx context.Context, mm *Machine, s string, d time.Duration) (string, error) {
		scripts = append(scripts, s)
		if strings.Contains(s, "sessions restore") {
			return "@@RESTORE_RC 0\n" + `@@RESTORE_JSON {"ok":true,"path":"/home/dev/.codex/sessions/old/rollout-x.jsonl","id":"cccccccccc01","sessionId":"` + uuidCodex + `"}` + "\n", nil
		}
		return "@@RESTORE_PRESENT\n", nil
	}
	e, hint, err = RestoreCold(context.Background(), run, st, m, "cccccccccc01", t0)
	if err != nil || hint != "codex resume "+uuidCodex || e.Tool != "codex" || len(scripts) != 2 ||
		!strings.Contains(scripts[1], "[ -s '/home/dev/.codex/sessions/old/rollout-x.jsonl' ]") {
		t.Fatalf("uncatalogued restore: %v %q %+v %d", err, hint, e, len(scripts))
	}
	if cat.Entries[0].ID != "cccccccccc01" || cat.location(cat.Entries[0], m, t0) != LocLocal {
		t.Fatalf("uncatalogued entry: %+v", cat.Entries[0])
	}
	if _, _, err := RestoreCold(context.Background(), run, st, m, "--to", t0); err == nil {
		t.Fatal("option-like id accepted")
	}

	for name, out := range map[string]string{"backend": restoreOut(4, false), "absent": restoreOut(0, false), "silent": ""} {
		st, m := coldState(t)
		run := func(ctx context.Context, mm *Machine, s string, d time.Duration) (string, error) { return out, nil }
		_, _, err := RestoreCold(context.Background(), run, st, m, "aaaaaaaaaaa1", t0)
		if err == nil || len(st.History) != 0 || st.Cold[m.ID].Entries[0].State != "archived" {
			t.Fatalf("%s: err=%v history=%d", name, err, len(st.History))
		}
		if name == "backend" && !strings.Contains(err.Error(), "备份后端不可达") {
			t.Fatalf("backend err: %v", err)
		}
	}
	if _, _, err := RestoreCold(context.Background(), coldRunner(nil), st, m, "nope", t0); err == nil {
		t.Fatal("unknown entry restored")
	}
}

func TestReferencedCountInHistory(t *testing.T) {
	st, m := scanFor(t, sampleProbeLocal)
	out := strings.Replace(sampleSessionApply, "@@SESSIONS_RC 0\n", "@@REFS 3\n@@SESSIONS_RC 0\n", 1)
	run := func(ctx context.Context, mm *Machine, s string, d time.Duration) (string, error) {
		if strings.Contains(s, "sessions --apply") {
			return out, nil
		}
		return coldRunner(nil)(ctx, mm, s, d)
	}
	_, rec, err := OffloadSessions(context.Background(), run, st, m, false, nil, t0)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Referenced != 1 || rec.RefsFound != 3 || !strings.Contains(rec.Summary, "保护 1 个被引用会话") {
		t.Fatalf("record: %+v", rec)
	}
	if st.Cold[m.ID] == nil || len(st.Cold[m.ID].Entries) != 4 {
		t.Fatal("cold index not refreshed after offload")
	}

	// A run aborted by the reference probe is an error and records nothing.
	st, m = scanFor(t, sampleProbeLocal)
	run = func(ctx context.Context, mm *Machine, s string, d time.Duration) (string, error) {
		if strings.Contains(s, "sessions --apply") {
			return "@@REFS_FAIL\n@@RC 0\n", nil
		}
		return coldRunner(nil)(ctx, mm, s, d)
	}
	if _, rec, err := OffloadSessions(context.Background(), run, st, m, false, nil, t0); err == nil || rec != nil || !strings.Contains(err.Error(), "无法列出") {
		t.Fatalf("fail closed: err=%v rec=%+v", err, rec)
	}
	if len(st.History) != 0 {
		t.Fatal("history after aborted run")
	}
}

func TestSessionsPageUI(t *testing.T) {
	a := newTestApp(t)
	a.runner = coldRunner(nil)
	ctx := context.Background()
	check := func(v *wire.View) {
		t.Helper()
		if err := wire.CheckView(v); err != nil {
			t.Fatalf("CheckView: %v", err)
		}
		if len(v.State) > 16<<10 {
			t.Fatalf("state %d bytes", len(v.State))
		}
	}
	act := func(state string, action string, values map[string]string) *wire.ActionResult {
		t.Helper()
		res, err := a.actionSessions(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: state, Action: action, Values: values}})
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		check(res.View)
		return res
	}
	v, err := a.renderSessions(ctx, &ext.UIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	check(v)
	if v.Search == nil || v.Search.Placeholder != coldSearchHint || v.Filter == nil {
		t.Fatalf("page: %+v", v)
	}

	res := act(v.State, "cold-refresh", map[string]string{"m": ""})
	if !strings.Contains(res.Toast, "已刷新 1 台机器的云端索引（4 个会话）") {
		t.Fatalf("refresh toast: %s", res.Toast)
	}
	rows := rowsOf(res.View)
	if len(rows) != 4 {
		t.Fatalf("rows: %d", len(rows))
	}
	r0 := rows[0]
	if r0.Badge == nil || r0.Badge.Text != LocCloud || r0.Badge.Tone != wire.TonePositive || len(r0.Actions) != 1 ||
		!strings.Contains(r0.Actions[0].Action.Confirm, "从云端恢复到原路径 /home/dev/.claude/projects/") ||
		!strings.Contains(r0.Actions[0].Action.Confirm, "claude --resume "+uuidClaude) || !strings.Contains(r0.Subtitle, "Claude Code · shop · 2026-10-20") {
		t.Fatalf("row: %+v", r0)
	}
	for _, r := range rows {
		if r.Badge.Text == LocLocal && len(r.Actions) != 0 {
			t.Fatal("local entry offers restore")
		}
	}

	res = act(res.View.State, "search", map[string]string{"q": "静态站点"})
	if rows := rowsOf(res.View); len(rows) != 1 || rows[0].Badge.Text != LocMissing || rows[0].Badge.Tone != wire.ToneDanger {
		t.Fatalf("search rows: %+v", rows)
	}
	if !hasButton(res.View, coldDeepText) {
		t.Fatal("no deep search button")
	}
	res = act(res.View.State, "cold-deep", map[string]string{"q": "refund webhook"})
	if rows := rowsOf(res.View); len(rows) != 2 {
		t.Fatalf("deep rows: %d", len(rows))
	}
	res = act(res.View.State, "machine", map[string]string{"machine": "local"})
	if !hasButton(res.View, coldOffloadText) {
		t.Fatal("no offload button for a machine")
	}
	res = act(res.View.State, "cold-restore", map[string]string{"m": "local", "id": "aaaaaaaaaaa1"})
	if !strings.Contains(res.Toast, "claude --resume "+uuidClaude) {
		t.Fatalf("restore toast: %s", res.Toast)
	}
	res = act(res.View.State, "cold-offload", map[string]string{"m": "local"})
	if !strings.Contains(res.Toast, "冷会话已上云") {
		t.Fatalf("offload toast: %s", res.Toast)
	}

	// The main page's 会话与台账 tab has the same search and rows.
	main, err := a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(page{Machine: "local", Tab: tabSessions}), Action: "cold-search", Values: map[string]string{"q": "checkout"}}})
	if err != nil {
		t.Fatal(err)
	}
	check(main.View)
	if main.View.Search == nil || main.View.Search.Value != "checkout" || len(rowsWithPrefix(main.View, "cold-")) != 1 {
		t.Fatalf("main tab: %+v", main.View.Search)
	}
	deep, err := a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: main.View.State, Action: "cold-deep", Values: map[string]string{"q": "refund"}}})
	if err != nil {
		t.Fatal(err)
	}
	check(deep.View)
	if len(rowsWithPrefix(deep.View, "cold-")) != 2 {
		t.Fatal("main tab deep search")
	}
}

func rowsOf(v *wire.View) []wire.Component { return rowsWithPrefix(v, "cold-") }

func rowsWithPrefix(v *wire.View, prefix string) []wire.Component {
	var out []wire.Component
	for _, s := range v.Sections {
		for _, c := range s.Items {
			if c.Type == wire.ComponentRow && strings.HasPrefix(c.Key, prefix) {
				out = append(out, c)
			}
		}
	}
	return out
}

func hasButton(v *wire.View, title string) bool {
	for _, s := range v.Sections {
		for _, c := range s.Items {
			if c.Type == wire.ComponentButton && c.Title == title {
				return true
			}
		}
	}
	return false
}

func TestSessionsMCPTools(t *testing.T) {
	st := newStore(t.TempDir())
	st.homeDir = ""
	st.now = func() time.Time { return t0 }
	srv := &mcpServer{store: st, runner: coldRunner(nil)}
	ctx := context.Background()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(args)
		out, err := srv.call(ctx, name, raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b, _ := json.Marshal(out)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m
	}
	status := call("disklessly_sessions_status", map[string]any{"refresh": true})
	ms := status["machines"].([]any)
	loc := ms[0].(map[string]any)["locations"].(map[string]any)
	if loc[LocCloud] != float64(1) || loc[LocMissing] != float64(1) || loc[LocUnknown] != float64(1) || loc[LocLocal] != float64(1) {
		t.Fatalf("status: %v", status)
	}
	found := call("disklessly_sessions_search", map[string]any{"query": "checkout"})
	if found["count"] != float64(1) || !strings.Contains(fmt.Sprint(found["results"]), "claude --resume "+uuidClaude) {
		t.Fatalf("search: %v", found)
	}
	deep := call("disklessly_sessions_search", map[string]any{"query": "refund", "deep": true, "machine": "local"})
	if deep["count"] != float64(2) {
		t.Fatalf("deep: %v", deep)
	}
	if _, err := srv.call(ctx, "disklessly_sessions_search", json.RawMessage(`{"query":" "}`)); err == nil {
		t.Fatal("empty query accepted")
	}
	if _, err := srv.call(ctx, "disklessly_sessions_search", json.RawMessage(`{"query":"x","machine":"nope"}`)); err == nil {
		t.Fatal("unknown machine accepted")
	}
	dry := call("disklessly_sessions_offload", map[string]any{"machine": "local", "dry_run": true, "exclude_sessions": []string{"keep-me"}})
	if dry["dry_run"] != true || fmt.Sprint(dry["referenced"]) != "["+uuidClaude+"]" || dry["plan"] == nil {
		t.Fatalf("dry run: %v", dry)
	}
	off := call("disklessly_sessions_offload", map[string]any{"machine": "local"})
	if rec, _ := off["record"].(map[string]any); rec == nil || rec["referenced"] != float64(1) {
		t.Fatalf("offload: %v", off)
	}
	res := call("disklessly_sessions_restore", map[string]any{"machine": "local", "id": "aaaaaaaaaaa1"})
	if res["resume"] != "claude --resume "+uuidClaude {
		t.Fatalf("restore: %v", res)
	}
	if _, err := srv.call(ctx, "disklessly_sessions_restore", json.RawMessage(`{"id":"aaaaaaaaaaa1"}`)); err == nil {
		t.Fatal("restore without machine accepted")
	}
}

// TestReferenceProbeShell runs the real probe under /bin/sh with a temporary HOME holding a
// synthetic herdr session.json, a stub herdr, a process resuming a made-up session and an open
// transcript. PATH leaves out any real herdr.
func TestReferenceProbeShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	const (
		herdrUUID = "aaaaaaaa-1111-4222-8333-444444444444"
		paneUUID  = "bbbbbbbb-1111-4222-8333-444444444444"
		argUUID   = "cccccccc-1111-4222-8333-444444444444"
		codexUUID = "dddddddd-1111-4222-8333-444444444444"
	)
	home, bin, tmp := t.TempDir(), t.TempDir(), t.TempDir()
	herdrDir := filepath.Join(home, ".config", "herdr")
	if err := os.MkdirAll(herdrDir, 0o755); err != nil {
		t.Fatal(err)
	}
	session := `{"panes":[{"agent":"claude","agent_resume":["claude","--resume","` + herdrUUID + `"]},{"transcript":"/home/dev/.codex/sessions/x/rollout-1.jsonl"}]}`
	if err := os.WriteFile(filepath.Join(herdrDir, "session.json"), []byte(session), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte("#!/bin/sh\necho \"pane 1 agent_session="+paneUUID+"\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(tmp, "open-transcript.jsonl")
	f, err := os.Create(open)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Each sh stays a process whose argv is the faked agent's: with one simple command, bash (macOS's
	// /bin/sh) would exec sleep in place and drop that argv, so a second command follows it.
	for _, args := range [][]string{
		{"-c", "sleep 30; :", "claude", "--resume", argUUID},
		{"-c", "sleep 30; :", "codex", "exec", "resume", codexUUID},
	} {
		p := exec.Command("sh", args...)
		if err := p.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = p.Process.Kill(); _ = p.Wait() }()
	}
	time.Sleep(100 * time.Millisecond)

	env := []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "TMPDIR=" + tmp}
	cmd := exec.Command("/bin/sh", "-s")
	cmd.Stdin = strings.NewReader(ReferencedSessionsScript)
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	refs, err := parseReferences(string(out))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{herdrUUID, paneUUID, argUUID, codexUUID, "/home/dev/.codex/sessions/x/rollout-1.jsonl"} {
		if !slices.Contains(refs, want) {
			t.Fatalf("probe missed %s (%d references)", want, len(refs))
		}
	}
	if _, err := os.Stat("/proc/self/fd"); err == nil && !slices.Contains(refs, open) {
		t.Fatalf("probe missed the open transcript (%d references)", len(refs))
	}

	// Fail closed: without /proc and with a failing ps the probe reports it, and the archive
	// script stops before the disklessly CLI runs.
	if err := os.WriteFile(filepath.Join(bin, "ps"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	logf := filepath.Join(tmp, "cli.log")
	if err := os.WriteFile(filepath.Join(bin, "disklessly"), []byte("#!/bin/sh\necho called >> \""+logf+"\"\necho '{\"ok\":true}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	failEnv := append(env, "DISKLESSLY_PROC_ROOT="+filepath.Join(tmp, "no-proc"))
	cmd = exec.Command("/bin/sh", "-s")
	cmd.Stdin = strings.NewReader(ReferencedSessionsScript)
	cmd.Env = failEnv
	out, _ = cmd.Output()
	if _, err := parseReferences(string(out)); err == nil || !strings.Contains(err.Error(), "无法列出") {
		t.Fatalf("fail closed: %v\n%s", err, out)
	}
	item := ScanItem{ID: "sessions-cold-claude", Category: CatColdAgentSession, Path: filepath.Join(home, ".claude", "projects")}
	cmd = exec.Command("/bin/sh", "-s")
	cmd.Stdin = strings.NewReader(BuildItemCleanScript(item, nil))
	cmd.Env = failEnv
	out, _ = cmd.Output()
	if e := parseSessionRun(string(out)).err(); e == nil || !strings.Contains(e.Error(), "无法列出") {
		t.Fatalf("archive not aborted: %v\n%s", e, out)
	}
	if _, err := os.Stat(logf); !os.IsNotExist(err) {
		t.Fatal("disklessly CLI ran although references could not be checked")
	}
}

// TestReferenceProbeAgentCwd checks, with a fake proc root, that an agent process without a
// session id in its argv keeps the transcripts of its working directory, and that codex
// resume with options before the id is still caught.
func TestReferenceProbeAgentCwd(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	const (
		optUUID   = "eeeeeeee-1111-4222-8333-444444444444"
		otherUUID = "ffffffff-1111-4222-8333-444444444444"
	)
	home, bin, tmp, proc := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	write := func(p, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(proc, "self", "cmdline"), "sh\x00")
	write(filepath.Join(proc, "101", "cmdline"), "node\x00/opt/agent/bin/claude\x00-c\x00")
	write(filepath.Join(proc, "102", "cmdline"), "codex\x00resume\x00--last\x00")
	write(filepath.Join(proc, "103", "cmdline"), "codex\x00resume\x00--dangerously-bypass-approvals-and-sandbox\x00"+optUUID+"\x00")
	for pid, cwd := range map[string]string{"101": "/work/demo-app", "102": "/work/other.app"} {
		if err := os.Symlink(cwd, filepath.Join(proc, pid, "cwd")); err != nil {
			t.Fatal(err)
		}
	}
	claudeOld := filepath.Join(home, ".claude", "projects", "-work-demo-app", "old-session.jsonl")
	claudeElse := filepath.Join(home, ".claude", "projects", "-work-elsewhere", "keep-cold.jsonl")
	codexHit := filepath.Join(home, ".codex", "sessions", "2024", "01", "02", "rollout-2024-01-02T00-00-00-"+otherUUID+".jsonl")
	codexMiss := filepath.Join(home, ".codex", "sessions", "2024", "01", "03", "rollout-2024-01-03T00-00-00-x.jsonl")
	write(claudeOld, "{}\n")
	write(claudeElse, "{}\n")
	write(codexHit, `{"type":"session_meta","payload":{"id":"`+otherUUID+`","cwd":"/work/other.app"}}`+"\n")
	write(codexMiss, `{"type":"session_meta","payload":{"cwd":"/work/unrelated"}}`+"\n")

	cmd := exec.Command("/bin/sh", "-s")
	cmd.Stdin = strings.NewReader(ReferencedSessionsScript)
	cmd.Env = []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "TMPDIR=" + tmp, "DISKLESSLY_PROC_ROOT=" + proc}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	refs, err := parseReferences(string(out))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{optUUID, claudeOld, codexHit} {
		if !slices.Contains(refs, want) {
			t.Fatalf("probe missed %s: %v", want, refs)
		}
	}
	for _, not := range []string{claudeElse, codexMiss} {
		if slices.Contains(refs, not) {
			t.Fatalf("probe kept unrelated %s", not)
		}
	}
}

func TestColdIndexReviewFixes(t *testing.T) {
	// Digests are pulled only for rows the catalog has no keywords for, and the list is limited.
	st, m := scanFor(t, sampleProbeLocal)
	var scripts []string
	if _, err := RefreshColdIndex(context.Background(), coldRunner(&scripts), st, m, t0); err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 2 || strings.Contains(scripts[0], "--digest") || !strings.Contains(scripts[0], "--limit 5000") ||
		!strings.Contains(scripts[1], "--digest") || strings.Contains(scripts[1], "sessions status") {
		t.Fatalf("first refresh scripts: %d", len(scripts))
	}
	if kw := st.Cold[m.ID].Entries[0].Keywords; len(kw) == 0 {
		t.Fatal("keywords not fetched")
	}
	scripts = nil
	if _, err := RefreshColdIndex(context.Background(), coldRunner(&scripts), st, m, t0); err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 1 || len(st.Cold[m.ID].Entries[0].Keywords) == 0 {
		t.Fatalf("second refresh pulled digests again: %d", len(scripts))
	}
	// A scan refreshes a fresh catalog no more than every few hours.
	scripts = nil
	refreshColdIfStale(context.Background(), coldRunner(&scripts), st, m, t0.Add(time.Hour), coldScanRefreshAge)
	if len(scripts) != 0 {
		t.Fatal("fresh catalog refreshed")
	}
	refreshColdIfStale(context.Background(), coldRunner(&scripts), st, m, t0.Add(coldScanRefreshAge+time.Minute), coldScanRefreshAge)
	if len(scripts) != 1 {
		t.Fatalf("stale catalog not refreshed: %d", len(scripts))
	}
	// Total comes from the status counts when the list is limited.
	if cat := st.Cold[m.ID]; cat.Total != 4 || cat.Capped {
		t.Fatalf("total %d capped %v", cat.Total, cat.Capped)
	}

	// pendingDelete is confirmed only through a segment --check-segments stats.
	cat := st.Cold[m.ID]
	cat.Entries = append(cat.Entries,
		ColdEntry{ID: "pd1", State: "pendingDelete", Segment: "seg-a", Local: true},
		ColdEntry{ID: "pd2", State: "pendingDelete", Segment: "seg-only-pending", Local: true})
	cat.checked = nil
	now := cat.Backend.CheckedAt
	if got := cat.location(cat.Entries[len(cat.Entries)-2], m, now); got != LocCloud {
		t.Fatalf("pendingDelete in checked segment: %s", got)
	}
	if got := cat.location(cat.Entries[len(cat.Entries)-1], m, now); got != LocUnknown {
		t.Fatalf("pendingDelete in unchecked segment: %s", got)
	}

	// Another managed machine's rows are shown only under that machine.
	m2 := &Machine{ID: "peer", Host: "peer.example", Name: "peer", Enabled: true, Reachable: true}
	st.Machines = append(st.Machines, m2)
	st.Cold["peer"] = &ColdCatalog{MachineID: "peer", DeviceID: "00000000-0000-4000-8000-000000000002",
		Entries: []ColdEntry{{ID: "peer000001", Local: true, State: "archived", Title: "peer session"}}}
	cat.Entries = append(cat.Entries,
		ColdEntry{ID: "peer000001", State: "remote", Title: "peer session", DeviceID: "00000000-0000-4000-8000-000000000002"},
		ColdEntry{ID: "gone000001", State: "remote", Title: "unmanaged device session", DeviceID: "00000000-0000-4000-8000-000000000003"})
	hits := SearchCold(st, "session", "", 0, t0)
	var where []string
	for _, h := range hits {
		if h.ID == "peer000001" || h.ID == "gone000001" {
			where = append(where, h.ID+"@"+h.MachineID)
		}
	}
	if strings.Join(where, ",") != "peer000001@peer,gone000001@local" && strings.Join(where, ",") != "gone000001@local,peer000001@peer" {
		t.Fatalf("foreign rows: %v", where)
	}

	// Keywords skip hashes and long tokens.
	kw := extractKeywords("deadbeef1234567 deadbeef1234567 abcdefghijklmnopqrstuvwxyz0123 parser parser cafe1234", 16)
	if strings.Join(kw, ",") != "parser" {
		t.Fatalf("keywords %v", kw)
	}

	// Deep search drops leading dashes and reports the CLI's own error.
	if got := deepSearchWords("--resume -home-dev-app - x"); strings.Join(got, "|") != "resume|home-dev-app|x" {
		t.Fatalf("words %v", got)
	}
	if _, err := parseDeepSearch(`{"ok":false,"error":{"code":2,"message":"sessions search requires a query","name":"EX_USAGE"}}`); err == nil || !strings.Contains(err.Error(), "requires a query") {
		t.Fatalf("cli error: %v", err)
	}
	scripts = nil
	if _, errs := DeepSearchCold(context.Background(), coldRunner(&scripts), st, "--", "local", 10, t0); len(errs) != 1 || len(scripts) != 0 {
		t.Fatalf("dash-only deep search: %v %d", errs, len(scripts))
	}
}

func TestSessionsPageLongQueryAndForeignRows(t *testing.T) {
	a := newTestApp(t)
	a.runner = coldRunner(nil)
	ctx := context.Background()
	long := strings.Repeat("长查询 ", 1000)
	res, err := a.actionSessions(ctx, &ext.UIRequest{UIParams: wire.UIParams{Action: "search", Values: map[string]string{"q": long}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(res.View); err != nil {
		t.Fatalf("long query view: %v", err)
	}
	if n := utf8.RuneCountInString(res.View.Search.Value); n > coldQueryRunes {
		t.Fatalf("query %d runes", n)
	}
	main, err := a.action(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(page{Machine: "local", Tab: tabSessions}), Action: "cold-search", Values: map[string]string{"q": long}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(main.View); err != nil {
		t.Fatalf("main tab long query: %v", err)
	}

	st, m := coldState(t)
	d := a.drawer(st, &ext.UIRequest{})
	foreign := ColdHit{ColdEntry: ColdEntry{ID: "x1", State: "remote", Tool: "claude", Path: "/Users/other/.claude/projects/-a/s.jsonl", DeviceID: "dev-2"},
		MachineID: m.ID, MachineName: "local", Location: LocUnknown}
	if row := d.coldRow(foreign); len(row.Actions) != 0 || !strings.Contains(row.Subtitle, "来自其他设备") {
		t.Fatalf("foreign row: %+v", row)
	}
	pi := ColdHit{ColdEntry: ColdEntry{ID: "x2", State: "archived", Tool: "pi", Path: "/home/dev/.pi/agent/sessions/s.jsonl", Local: true},
		MachineID: m.ID, MachineName: "local", Location: LocUnknown}
	if row := d.coldRow(pi); len(row.Actions) != 1 || strings.Contains(row.Actions[0].Action.Confirm, "--resume") {
		t.Fatalf("pi row: %+v", row)
	}
}

func TestColdIndexStoredPerMachine(t *testing.T) {
	dir := t.TempDir()
	s := newStore(dir)
	s.homeDir = ""
	err := s.update(func(st *State, now time.Time) error {
		m := st.machine("local")
		_, err := RefreshColdIndex(context.Background(), coldRunner(nil), st, m, t0)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil || strings.Contains(string(raw), "aaaaaaaaaaa1") {
		t.Fatalf("cold index in the state file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, coldDir, "local.json")); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.read(func(st *State) { n = len(st.cold("local").Entries) }); err != nil || n != 4 {
		t.Fatalf("reload: %d %v", n, err)
	}
	// A fresh store (another process) reads it from disk; dropping removes the file.
	s2 := newStore(dir)
	s2.homeDir = ""
	if err := s2.update(func(st *State, now time.Time) error {
		if c := st.cold("local"); c == nil || len(c.Entries) != 4 {
			t.Fatal("not loaded from disk")
		}
		st.dropCold("local")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, coldDir, "local.json")); !os.IsNotExist(err) {
		t.Fatalf("dropped index still on disk: %v", err)
	}
}
