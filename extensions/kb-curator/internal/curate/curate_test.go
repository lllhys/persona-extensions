package curate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/model"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/store"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/stub"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/verify"
	"github.com/nwverse/persona/sdk/go/wire"
)

func newCurator(t *testing.T, m model.Model, mode string) (*Curator, *kbsrc.Fixture) {
	t.Helper()
	fx, err := kbsrc.LoadFixture("../../testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := Defaults()
	cfg.Mode = mode
	cfg.AdmitAllBelowTokens = 0 // the sample is small; test the rules, not the light-user shortcut
	now := time.Date(2026, 11, 9, 4, 0, 0, 0, time.UTC)
	return &Curator{Src: fx, Model: m, St: st, Cfg: cfg, Loc: time.UTC, Now: func() time.Time { return now }}, fx
}

func decisions(c *Curator, day string) map[string]int { return c.St.S.Funnel[day].Chats }

func TestDailyTriageOnePipeline(t *testing.T) {
	c, _ := newCurator(t, &stub.Model{}, "shadow")
	ctx := context.Background()
	if _, err := c.RunDaily(ctx, "2026-11-03", false); err != nil {
		t.Fatal(err)
	}
	got := decisions(c, "2026-11-03")
	want := map[string]int{
		"R2_dm→include":              1, // Lark DM
		"R5_owner_active→include":    1, // Atlas group, owner spoke
		"R6_silent_group→drop":       1, // the company-wide group
		"R3_mention→window":          1,
		"R8_at_all_only→digest_only": 1,
		"R9_hub_ranked→include":      1, // ranked by the hub, the model said include
		"M1_bulk_mail→drop":          1,
		"M3_other_mail→drop":         1, // marketing, by the model
		"B1_bot→include":             1, // the stub includes anything not marketing
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
	// The window around a mention is the lines around it, not the whole day (61 lines).
	for _, d := range c.St.S.Done {
		if d.Chat == "chats/lark-grp-mention" && (len(d.Shown) > 31 || len(d.Shown) < 10) {
			t.Errorf("mention window: %d lines", len(d.Shown))
		}
	}
	var mail *store.Proposal
	for _, p := range c.St.S.Proposals {
		if p.Kind == "mail_downgrade" {
			mail = p
		}
	}
	if mail == nil || !mail.Shadow {
		t.Fatalf("marketing mail is proposed for a downgrade, in shadow: %+v", mail)
	}
}

func TestDailyItemsGraphAndIdempotence(t *testing.T) {
	m := &stub.Model{}
	c, _ := newCurator(t, m, "shadow")
	ctx := context.Background()
	for _, d := range []string{"2026-11-03", "2026-11-04"} {
		if _, err := c.RunDaily(ctx, d, false); err != nil {
			t.Fatal(err)
		}
	}
	calls := m.Calls["daily"]
	if _, err := c.RunDaily(ctx, "2026-11-04", false); err != nil {
		t.Fatal(err)
	}
	if m.Calls["daily"] != calls {
		t.Errorf("a day already digested is not digested again: %d → %d calls", calls, m.Calls["daily"])
	}
	// WeChat, mail, GitHub, meetings: all in the same pipeline.
	seen := map[string]bool{}
	for _, it := range c.St.S.Items {
		for _, e := range it.Evidence {
			seen[strings.SplitN(e.Chat, "-", 2)[0]] = true
		}
	}
	for _, src := range []string{"chats/lark", "chats/wechat", "chats/mail", "chats/github", "meetings/m1"} {
		if !seen[src] {
			t.Errorf("no item from %s (seen %v)", src, seen)
		}
	}
	// The practice said on two days became recurring.
	var practice *store.Item
	for _, it := range c.St.S.Items {
		if it.Class == store.Practice && strings.Contains(it.Claim, "评审单") {
			practice = it
		}
	}
	if practice == nil || practice.Status != "recurring" || len(practice.Days) != 2 {
		t.Fatalf("practice: %+v", practice)
	}
	// Graph: Blake owns the payment gateway (seeded from the knowledge base's person), the owner committed.
	var owns, committed bool
	for _, a := range c.St.S.Graph.Assertions {
		s := c.St.S.Graph.Nodes[a.S]
		o := c.St.S.Graph.Nodes[a.O]
		if a.P == "owns" && s.Name == "Blake Wu" && strings.Contains(o.Name, "支付网关") && len(s.Refs) == 1 {
			owns = true
		}
		if a.P == "committed" && a.S == "me" {
			committed = true
		}
	}
	if !owns || !committed {
		t.Errorf("graph: owns %v committed %v; %d assertions", owns, committed, len(c.St.S.Graph.Assertions))
	}
}

func TestChecksRejectBadAnswers(t *testing.T) {
	bad := stub.Scripted(func(r model.Request) (string, error) {
		if r.Step != "daily" {
			return `{"admit":"include","category":"work_fyi","importance":"P2","relevance":50,"reason":"x","evidence":[]}`, nil
		}
		out := map[string]any{
			"summary": []any{}, "mentions": []any{}, "assertions": []any{
				// Casey's line cited for Blake deciding: nobody decides on another person's words.
				map[string]any{"s": "Blake Wu", "p": "decided", "o": "对账模块", "evidence": []any{map[string]any{"n": 4, "quote": "对账模块现在卡在"}}, "confidence": "high"},
			},
			"facts": []any{
				map[string]any{"claim": "发布改到周四", "evidence": []any{map[string]any{"n": 3, "quote": "发布改到周四晚上"}}, "confidence": "high"},         // not in the line
				map[string]any{"claim": "x", "evidence": []any{map[string]any{"n": 99, "quote": "whatever text"}}, "confidence": "high"},        // no such line
				map[string]any{"claim": "上线时间是 2026-12-24", "evidence": []any{map[string]any{"n": 3, "quote": "发布窗口定了"}}, "confidence": "high"}, // date from nowhere
			},
			"practices": []any{}, "knowledge": []any{},
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	})
	c, _ := newCurator(t, bad, "shadow")
	if _, err := c.RunDaily(context.Background(), "2026-11-03", false); err != nil {
		t.Fatal(err)
	}
	codes := c.rejectionCounts("2026-11-03")
	for _, code := range []string{verify.QuoteNotFound, verify.AnchorNotFound} {
		if codes[code] == 0 {
			t.Errorf("no %s: %v", code, codes)
		}
	}
	if codes[verify.SpeakerMismatch] == 0 {
		t.Errorf("a decision on someone else's line is rejected: %v", codes)
	}
	flaggedDate := false
	for _, it := range c.St.S.Items {
		if strings.Contains(it.Claim, "2026-12-24") {
			flaggedDate = strings.Contains(strings.Join(it.Flags, ","), verify.DateMismatch) && it.Status == "candidate"
		}
	}
	if !flaggedDate {
		t.Error("a date the lines do not give is flagged and the item stays a candidate")
	}
}

func TestNoModelStillTriagesAndChecks(t *testing.T) {
	down := stub.Scripted(func(model.Request) (string, error) { return "", model.ErrUnavailable })
	c, _ := newCurator(t, down, "shadow")
	run, err := c.RunDaily(context.Background(), "2026-11-03", false)
	if err != nil {
		t.Fatal(err)
	}
	if run.Counts["digested"] != 0 || c.St.S.Funnel["2026-11-03"].Admitted == 0 {
		t.Fatalf("without a model: digested %d, admitted %d", run.Counts["digested"], c.St.S.Funnel["2026-11-03"].Admitted)
	}
	if !strings.Contains(run.Report, "模型不可用") {
		t.Errorf("the report says the model is unavailable:\n%s", run.Report)
	}
}

func TestWeeklyHealthPinsApplyUndo(t *testing.T) {
	c, fx := newCurator(t, &stub.Model{}, "review")
	ctx := context.Background()
	for _, d := range []string{"2026-11-03", "2026-11-04"} {
		if _, err := c.RunDaily(ctx, d, false); err != nil {
			t.Fatal(err)
		}
	}
	run, err := c.RunWeekly(ctx, "2026-W45", false)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string][]*store.Proposal{}
	for _, p := range c.St.S.Proposals {
		kinds[p.Kind] = append(kinds[p.Kind], p)
	}
	for _, k := range []string{"remove_alias", "project_pause", "not_a_person", "owner_as_person", "pin_fact"} {
		if len(kinds[k]) == 0 {
			t.Errorf("no %s proposal; report:\n%s", k, run.Report)
		}
	}
	if p := kinds["not_a_person"]; len(p) > 0 && p[0].Status != "blocked" {
		t.Errorf("marking a person as not a person waits for the hub: %s", p[0].Status)
	}
	alias := kinds["remove_alias"][0]
	if !strings.Contains(alias.Title, "Atlas 项目群") || !strings.Contains(alias.Title, "周会") {
		t.Errorf("alias proposal: %s", alias.Title)
	}
	if len(c.St.S.Samples["2026-W45"]) == 0 {
		t.Error("a spot-check sample is picked")
	}

	// Accept a pin: the entity's confirmed facts get the line; undo restores them.
	pin := kinds["pin_fact"][0]
	if _, err := c.Decide(ctx, pin.ID, "accept", ""); err != nil {
		t.Fatal(err)
	}
	var ch Change
	_ = json.Unmarshal(pin.Change, &ch)
	d, _ := fx.Get(ctx, ch.Update.Kind, ch.Update.ID)
	if m := kbsrc.DecodeMeta(d); !strings.Contains(m.Pinned, ch.PinLine) {
		t.Fatalf("pinned after accept: %q", m.Pinned)
	}
	if err := c.Undo(ctx, pin.ApplyLog); err != nil {
		t.Fatal(err)
	}
	d, _ = fx.Get(ctx, ch.Update.Kind, ch.Update.ID)
	if m := kbsrc.DecodeMeta(d); m.Pinned != "" {
		t.Fatalf("pinned after undo: %q", m.Pinned)
	}

	// Accept the alias removal, then someone edits the aliases: undo refuses to overwrite.
	if _, err := c.Decide(ctx, alias.ID, "accept", ""); err != nil {
		t.Fatal(err)
	}
	keep := []string{"Atlas 平台"}
	if err := fx.UserUpdate(wire.KBUpdateParams{Kind: "projects", ID: "atlas", Aliases: keep}); err != nil {
		t.Fatal(err)
	}
	if err := c.Undo(ctx, alias.ApplyLog); !errors.Is(err, ErrChanged) {
		t.Fatalf("undo over a later edit: %v", err)
	}
}

func TestShadowAcceptOnlyRecordsVerdict(t *testing.T) {
	c, fx := newCurator(t, &stub.Model{}, "shadow")
	ctx := context.Background()
	if _, err := c.RunDaily(ctx, "2026-11-03", false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RunWeekly(ctx, "2026-W45", false); err != nil {
		t.Fatal(err)
	}
	for _, p := range c.St.S.Proposals {
		if p.Kind == "remove_alias" {
			msg, err := c.Decide(ctx, p.ID, "accept", "")
			if err != nil || !strings.Contains(msg, "影子期") || p.Verdict != "right" {
				t.Fatalf("shadow accept: %q %v %+v", msg, err, p)
			}
		}
	}
	if len(fx.Writes()) != 0 {
		t.Fatalf("shadow mode wrote to the knowledge base: %+v", fx.Writes())
	}
}

func TestExpiryStartsWhenShown(t *testing.T) {
	c, _ := newCurator(t, &stub.Model{}, "review")
	p := c.propose("remove_alias", "t", "", "projects/atlas", Change{Update: &wire.KBUpdateParams{Kind: "projects", ID: "atlas"}}, nil, nil, "")
	c.expire()
	if p.Status != "pending" {
		t.Fatal("never shown: does not expire")
	}
	p.Shown = addDays(c.today(), -15)
	c.expire()
	if p.Status != "expired" {
		t.Fatalf("shown 15 days ago: %s", p.Status)
	}
}

func TestWeekDays(t *testing.T) {
	d, err := WeekDays("2026-W45")
	if err != nil || d[0] != "2026-11-02" || d[6] != "2026-11-08" {
		t.Fatalf("%v %v", d, err)
	}
	if ISOWeek("2026-11-08") != "2026-W45" {
		t.Fatal(ISOWeek("2026-11-08"))
	}
}

func TestForgetSource(t *testing.T) {
	c, _ := newCurator(t, &stub.Model{}, "shadow")
	ctx := context.Background()
	if _, err := c.RunDaily(ctx, "2026-11-04", false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Forget("wechat"); err != nil {
		t.Fatal(err)
	}
	for _, it := range c.St.S.Items {
		for _, e := range it.Evidence {
			if strings.HasPrefix(e.Chat, "chats/wechat-") {
				t.Fatalf("evidence of a forgotten source is kept: %+v", it)
			}
		}
	}
	for _, d := range c.St.S.Done {
		if strings.HasPrefix(d.Chat, "chats/wechat-") {
			t.Fatal("the ledger keeps a forgotten source")
		}
	}
}

func TestSmallDayStillDropsMarketing(t *testing.T) {
	c, _ := newCurator(t, &stub.Model{}, "shadow")
	c.Cfg.AdmitAllBelowTokens = 1 << 20
	if _, err := c.RunDaily(context.Background(), "2026-11-03", false); err != nil {
		t.Fatal(err)
	}
	got := decisions(c, "2026-11-03")
	if got["M3_other_mail→drop"] != 1 || got["M1_bulk_mail→drop"] != 1 || got["S0_small_volume→include"] == 0 {
		t.Fatalf("small day: %v", got)
	}
}

// Writes follow nwverse/persona#6110: evidence goes with the write, the curator then holds the field, undo hands a
// field that was free back (unlocking pinned clears it), and a field the user holds is a question, not a write.
func TestHoldersEvidenceAndConflict(t *testing.T) {
	c, fx := newCurator(t, &stub.Model{}, "review")
	ctx := context.Background()
	for _, d := range []string{"2026-11-03", "2026-11-04"} {
		if _, err := c.RunDaily(ctx, d, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.RunWeekly(ctx, "2026-W45", false); err != nil {
		t.Fatal(err)
	}
	var pins []*store.Proposal
	for _, p := range c.St.S.Proposals {
		if p.Kind == "pin_fact" && strings.HasPrefix(p.Target, "chats/") {
			pins = append(pins, p)
		}
	}
	if len(pins) < 2 {
		t.Fatalf("%d chat pins", len(pins))
	}
	// 1. Accepted: written with evidence, held by the curator.
	if _, err := c.Decide(ctx, pins[0].ID, "accept", ""); err != nil {
		t.Fatal(err)
	}
	w := fx.Writes()[len(fx.Writes())-1]
	if len(w.Evidence) == 0 || w.Evidence[0].Day == "" {
		t.Fatalf("no evidence on the write: %+v", w)
	}
	kind, id, _ := strings.Cut(pins[0].Target, "/")
	d, _ := fx.Get(ctx, kind, id)
	var meta map[string]any
	_ = json.Unmarshal(d.Meta, &meta)
	if prov, _ := meta["provenance"].(map[string]any); prov["pinned"] == nil {
		t.Fatalf("the curator holds what it wrote: %v", meta["provenance"])
	}
	// 2. Undo: the field was free before, so it is handed back and cleared.
	if err := c.Undo(ctx, pins[0].ApplyLog); err != nil {
		t.Fatal(err)
	}
	d, _ = fx.Get(ctx, kind, id)
	meta = map[string]any{}
	_ = json.Unmarshal(d.Meta, &meta)
	if meta["pinned"] != "" || strings.Contains(fmt.Sprint(meta["locked_fields"]), "pinned") {
		t.Fatalf("after undo: pinned %q locked %v", meta["pinned"], meta["locked_fields"])
	}
	// 3. The user pins something on another chat; the curator's pin there becomes a question.
	kind, id, _ = strings.Cut(pins[1].Target, "/")
	mine := "我自己确认的事实"
	if err := fx.UserUpdate(wire.KBUpdateParams{Kind: kind, ID: id, Pinned: &mine}); err != nil {
		t.Fatal(err)
	}
	msg, err := c.Decide(ctx, pins[1].ID, "accept", "")
	if err != nil || pins[1].Status != "held" || !strings.Contains(msg, "pinned") {
		t.Fatalf("conflict: %q %v %s", msg, err, pins[1].Status)
	}
	d, _ = fx.Get(ctx, kind, id)
	if m := kbsrc.DecodeMeta(d); m.Pinned != mine {
		t.Fatalf("the user's pin was overwritten: %q", m.Pinned)
	}
}
