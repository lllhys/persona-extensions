package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	exits "github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/export"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Placeholder data only (user-a, project-x): nothing from anyone's real records.

func v4App(t *testing.T) *app {
	t.Helper()
	fx, err := kbsrc.LoadFixture("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &app{c: &curate.Curator{Src: fx, St: st, Cfg: curate.Defaults(), Loc: time.UTC, Live: &curate.Live{}, Lang: "zh-Hans"}}
}

func ev(src, day, quote string) []entry.Evidence {
	return []entry.Evidence{{Source: src, Ref: "chats/persona-1", Day: day, Who: entry.WhoSelf, Role: "user", Quote: quote}}
}

// seed fills the state with placeholder entries of every facet, a card, sources (one waiting for the hub, one not
// granted, one offline), speaker checks and contacts.
func seed(a *app) map[string]string {
	c := a.c
	c.St.Lock()
	defer c.St.Unlock()
	s := c.St.S
	ids := map[string]string{}
	put := func(name string, e *entry.Entry) {
		ids[name] = c.Observe(e, "test")
	}
	put("ask", &entry.Entry{Facet: entry.Interaction, Text: "选项要写全称，不能只写代号", Status: entry.Stable, Type: "options",
		Impact: entry.Impact{Moment: "ask_user", Action: "write options in full"}, Evidence: ev(entry.SrcAssistant, "2026-11-03", "选项别只写代号")})
	put("tool", &entry.Entry{Facet: entry.Tools, Text: "host-a 的 SSH 不在默认端口", Status: entry.Stable, Object: "host-a", Pitfall: "default port refused", Fix: "use the configured port",
		Impact: entry.Impact{Moment: "use_object", Action: "use the right port", Object: "host-a"}, Evidence: ev(entry.SrcTools, "2026-11-04", "port 22 refused")})
	put("judge", &entry.Entry{Facet: entry.Judgement, Grain: entry.Class, Type: "deploy", Text: "上线前先灰度", Situation: "要上线", Choice: "先灰度", Rejected: "全量",
		Status: entry.Candidate, Count: 3, Impact: entry.Impact{Moment: "ask_user", Action: "suggest a canary"}, Evidence: ev(entry.SrcAssistant, "2026-11-05", "先灰度再说")})
	put("work", &entry.Entry{Facet: entry.Workflow, Type: "review", Text: "评审前先自己跑一遍测试", Status: entry.Candidate,
		Impact: entry.Impact{Moment: "task_start", Action: "run the tests first"}, Evidence: ev(entry.SrcAgent, "2026-11-05", "先跑测试")})
	put("fact", &entry.Entry{Facet: entry.World, Text: "user-a 负责 project-x 的发布", Status: entry.Stable, SingleSource: true,
		Impact: entry.Impact{Moment: "kb_lookup", Action: "name the owner"}, Evidence: ev(entry.SrcChat, "2026-11-05", "project-x 发布找 user-a")})
	put("me", &entry.Entry{Facet: entry.Persona, Grain: entry.Class, Text: "偏好先看结论", Status: entry.Stable,
		Impact: entry.Impact{Moment: "report", Action: "lead with the answer"}, Evidence: ev(entry.SrcAssistant, "2026-11-05", "先说结论")})
	put("cardfact", &entry.Entry{Facet: entry.People, Person: "person:lark-u2", Part: entry.PartFact, Text: "user-a 负责发布", Status: entry.Stable,
		Impact: entry.Impact{Moment: "reply_person", Action: "route release questions"}, Evidence: ev(entry.SrcChat, "2026-11-05", "发布我来")})
	put("cardinf", &entry.Entry{Facet: entry.People, Person: "person:lark-u2", Part: entry.PartInferred, Inferred: true, Text: "回复通常很快", Status: entry.Candidate,
		Impact: entry.Impact{Moment: "reply_person", Action: "expect a quick answer"}, Evidence: ev(entry.SrcChat, "2026-11-05", "马上看")})
	put("cardrep", &entry.Entry{Facet: entry.People, Person: "person:lark-u2", Part: entry.PartInferred, Inferred: true, ReportedOnly: true, Text: "据说周五不回消息", Status: entry.Candidate,
		Impact: entry.Impact{Moment: "reply_person", Action: "avoid fridays"}, Evidence: []entry.Evidence{{Source: entry.SrcChat, Ref: "chats/g", Day: "2026-11-05", Speaker: "user-b", Who: "person:lark-u3", Quote: "他周五不回", Reported: true}}})
	s.Contacts["person:lark-u2"] = &entry.Contact{Person: "person:lark-u2", Name: "user-a", Ties: map[string]int{"dm": 3, "group": 2}, Score: 11, First: "2026-11-01", Last: "2026-11-05"}
	s.Contacts["person:lark-u3"] = &entry.Contact{Person: "person:lark-u3", Name: "user-b", Ties: map[string]int{"group": 1}, Score: 1, First: "2026-11-02", Last: "2026-11-02"}
	s.Sources["S1/persona"] = &entry.SourceStatus{Code: entry.SrcAssistant, Name: "persona", Available: true, Units: 4, Lines: 80, Bytes: 4000, LearnPeople: true}
	s.Sources["S6/memory"] = &entry.SourceStatus{Code: entry.SrcMemory, Name: "memory", Missing: "waiting_hub", Waiting: "H2", LearnPeople: true}
	s.Sources["S3/agents"] = &entry.SourceStatus{Code: entry.SrcAgent, Name: "agents", Missing: "no_scope", LearnPeople: true}
	s.Sources["S7/about"] = &entry.SourceStatus{Code: entry.SrcAbout, Name: "about", Missing: "offline", LearnPeople: true}
	s.Sources["S8/lark"] = &entry.SourceStatus{Code: entry.SrcChat, Name: "lark", Available: true, Units: 9, Lines: 300, Bytes: 9000, Sampled: 2, Right: 1, Unresolved: 0.03, LearnPeople: true}
	s.SpeakerChecks = []entry.SpeakerCheck{{Source: "lark", Ref: "chats/g", Day: "2026-11-05", N: 3, Who: "person:lark-u2", Name: "user-a", Quote: "发布我来"},
		{Source: "lark", Ref: "chats/g", Day: "2026-11-05", N: 4, Who: entry.WhoSelf, Name: "me", Quote: "好的"}}
	s.Exits.Waiting = map[string]string{"A": "H1", "B": "H1", "D": "H4", "E": "H5", "F": "H1"}
	return ids
}

func render(t *testing.T, a *app, s page, locale string) (*wire.View, string) {
	t.Helper()
	r := &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(s), Locale: locale, UIVersion: 6}}
	v, err := a.home(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if err := wire.CheckView(v); err != nil {
		t.Fatalf("page %+v (%s) fails the hub's check: %v", s, locale, err)
	}
	b, _ := json.Marshal(v)
	return v, string(b)
}

func act(t *testing.T, a *app, s page, action string, values map[string]string, locale string) (*wire.ActionResult, page) {
	t.Helper()
	r := &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(s), Action: action, Values: values, Locale: locale, UIVersion: 6}}
	res, err := a.homeAction(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	var next page
	if res.View != nil {
		_ = json.Unmarshal([]byte(res.View.State), &next)
	}
	return res, next
}

// Every v4 tab and page draws in Chinese and in English and passes the hub's view check; missing sources and
// waiting hub changes are said as they are, not as errors.
func TestV4PagesBothLanguages(t *testing.T) {
	a := v4App(t)
	ids := seed(a)
	pages := []page{{Tab: tabStart}, {Tab: tabPeople}, {Tab: tabSwitches}, {Tab: tabOlder},
		{Tab: tabStart, Open: "onboarding"}, {Tab: tabPeople, Open: "card:person:lark-u2"}, {Tab: tabPeople, Open: "card:person:lark-u2", Show: "reported"},
		{Tab: tabFacets, Open: "types:judgement"}, {Tab: tabFacets, Open: "e:" + ids["judge"]}, {Tab: tabCheck, Check: "u7", Sub: "person:lark-u2"},
		{Tab: tabCheck, Check: "u8", Sub: "lark"}}
	for _, f := range facetsShown {
		pages = append(pages, page{Tab: tabFacets, Facet: string(f)})
	}
	for _, k := range []string{"u1", "u2", "u3", "u4", "u5", "u6", "u7", "u8", "f1"} {
		pages = append(pages, page{Tab: tabCheck, Check: k})
	}
	for _, p := range pages {
		_, zh := render(t, a, p, "zh-Hans")
		_, en := render(t, a, p, "en")
		if zh == en {
			t.Errorf("page %+v is the same in both languages", p)
		}
	}
	_, zh := render(t, a, page{Tab: tabStart}, "zh-Hans")
	for _, want := range []string{"等 hub（H2）", "没授权", "助手电脑离线", "等 hub（H1）", "三个问题", "① 和 AI 的交互", "⑤′ 有来往的人",
		"「deploy」看到过", "没有 Agent 会话", "本周读了多少、挖了几段"} {
		if !strings.Contains(zh, want) {
			t.Errorf("the start page does not say %q", want)
		}
	}
	_, en := render(t, a, page{Tab: tabStart}, "en-US")
	for _, want := range []string{"waiting for the hub (H2)", "not granted", "the assistant's computer is offline", "Getting started", "Three questions"} {
		if !strings.Contains(en, want) {
			t.Errorf("the English start page does not say %q", want)
		}
	}
	if strings.Contains(en, "已有") || strings.Contains(en, "来源") {
		t.Errorf("Chinese left on the English start page: %s", en)
	}
	// The card: facts, inferred lines labelled with little evidence, the reported-only line folded until opened.
	_, card := render(t, a, page{Tab: tabPeople, Open: "card:person:lark-u2"}, "zh-Hans")
	for _, want := range []string{"事实", "推断的侧写", "证据少（1 条）", "只来自转述（默认折叠）"} {
		if !strings.Contains(card, want) {
			t.Errorf("the card does not show %q: %s", want, card)
		}
	}
	if strings.Contains(card, "据说周五不回消息") {
		t.Errorf("a reported-only line is not folded: %s", card)
	}
	_, open := render(t, a, page{Tab: tabPeople, Open: "card:person:lark-u2", Show: "reported"}, "zh-Hans")
	if !strings.Contains(open, "据说周五不回消息") {
		t.Errorf("the reported-only line does not unfold: %s", open)
	}
}

// The first-run questions: skippable, answerable in part, editable; answers become ① and ⑥ entries of the user's own,
// shown apart from behaviour.
func TestOnboarding(t *testing.T) {
	a := v4App(t)
	s := page{Tab: tabStart}
	act(t, a, s, "ob:skip", nil, "zh-Hans")
	if ob := a.c.St.S.Onboarding; ob == nil || !ob.Skipped {
		t.Fatalf("skip not kept: %+v", ob)
	}
	_, start := render(t, a, s, "zh-Hans")
	if !strings.Contains(start, "首次引导的回答") || strings.Contains(start, "三个问题（都可以跳过）") {
		t.Errorf("skipped questions are asked again: %s", start)
	}
	// Only question 2.
	op := page{Tab: tabStart, Open: "onboarding"}
	act(t, a, op, "ob:save", map[string]string{"musts": "回答先给结论\n不要替我对外发消息"}, "zh-Hans")
	ob := a.c.St.S.Onboarding
	if ob.Use != "" || len(ob.Musts) != 2 || len(ob.Focus) != 0 {
		t.Fatalf("a partial answer: %+v", ob)
	}
	first := curate.EntryID(entry.Interaction, entry.Action, "回答先给结论", "")
	e := a.c.St.S.Entries[first]
	if e == nil || e.Origin != entry.FromOnboarding || e.Status != entry.Stable {
		t.Fatalf("an answer to question 2 is not a stable ① entry of the user's own: %+v", e)
	}
	_, facet := render(t, a, page{Tab: tabFacets, Facet: string(entry.Interaction)}, "zh-Hans")
	if !strings.Contains(facet, "你自己说的（2）") {
		t.Errorf("self-reported entries are not shown apart: %s", facet)
	}
	// Edit: one ask out, use and focus in (the knowledge base's project found by name).
	act(t, a, op, "ob:save", map[string]string{"use": "work", "musts": "不要替我对外发消息", "focus": "Atlas 上线\nproject-x 迁移"}, "zh-Hans")
	ob = a.c.St.S.Onboarding
	if ob.Use != entry.Work || len(ob.Musts) != 1 || len(ob.Focus) != 2 {
		t.Fatalf("the edit is not kept: %+v", ob)
	}
	if !a.c.St.S.Entries[first].Deleted {
		t.Errorf("an ask taken out of the answer stays in effect")
	}
	atlas := a.c.St.S.Entries[curate.EntryID(entry.Persona, entry.Action, "正在推进：Atlas 上线", "")]
	if atlas == nil || atlas.Object != "project:atlas" {
		t.Errorf("a focus naming a project is not tied to it: %+v", atlas)
	}
	if x := a.c.St.S.Entries[curate.EntryID(entry.Persona, entry.Action, "正在推进：project-x 迁移", "")]; x == nil || x.Object != "" {
		t.Errorf("a focus with no project is not kept as something in progress: %+v", x)
	}
	// Given again, the ask comes back.
	act(t, a, op, "ob:save", map[string]string{"musts": "回答先给结论\n不要替我对外发消息"}, "zh-Hans")
	if a.c.St.S.Entries[first].Deleted {
		t.Errorf("an ask given again is not restored")
	}
	// English page, English entry text.
	b := v4App(t)
	b.c.Lang = "en"
	act(t, b, op, "ob:save", map[string]string{"focus": "the project-x launch"}, "en")
	if b.c.St.S.Entries[curate.EntryID(entry.Persona, entry.Action, "Working on: the project-x launch", "")] == nil {
		t.Errorf("the focus entry is not in the target language")
	}
}

// "Wrong" on the check page takes the entry out of the exits at once, not at the next daily pass.
func TestCheckWrongWithdraws(t *testing.T) {
	a := v4App(t)
	ids := seed(a)
	c := a.c
	c.St.Lock()
	if _, err := exits.Compile(context.Background(), c.St.S, exits.Options{Dir: c.St.Dir, Lang: "zh-Hans", Loc: time.UTC}); err != nil {
		t.Fatal(err)
	}
	c.St.Unlock()
	id := ids["ask"]
	if len(exitIndex(c.St.Dir)[id]) == 0 {
		t.Fatalf("the stable ① entry did not go out to start with: %v", exitIndex(c.St.Dir))
	}
	s := page{Tab: tabCheck, Check: "u1"}
	_, list := render(t, a, s, "zh-Hans")
	if !strings.Contains(list, "m:"+id) {
		t.Fatalf("the entry is not on the check page: %s", list)
	}
	res, _ := act(t, a, s, "mark", map[string]string{"m:" + id: "wrong"}, "zh-Hans")
	if !strings.Contains(res.Toast, "撤出") {
		t.Errorf("toast: %q", res.Toast)
	}
	if x := exitIndex(c.St.Dir)[id]; len(x) > 0 {
		t.Errorf("marked wrong, still in exits %v", x)
	}
	b, _ := os.ReadFile(filepath.Join(c.St.Dir, exits.InstructionsPath))
	if strings.Contains(string(b), "选项要写全称") {
		t.Errorf("the per-turn block still carries it: %s", b)
	}
	if m := lastMark(c.St.S.Entries[id].Verdicts); m != "wrong" {
		t.Errorf("mark %q", m)
	}
	// Still listed, showing the mark; the facet's tally counts it.
	_, after := render(t, a, s, "zh-Hans")
	if !strings.Contains(after, `"value":"wrong"`) || !strings.Contains(after, "判了 1 / ") {
		t.Errorf("the check page does not keep the mark: %s", after)
	}
	// F1 "unsupported" is a wrong mark with its note.
	act(t, a, page{Tab: tabCheck, Check: "f1"}, "mark", map[string]string{"m:" + ids["fact"]: "unsupported"}, "zh-Hans")
	if vs := c.St.S.Entries[ids["fact"]].Verdicts; len(vs) == 0 || vs[len(vs)-1].Mark != "wrong" || vs[len(vs)-1].Note != "unsupported" {
		t.Errorf("unsupported: %+v", vs)
	}
	// U8: a line marked on the speaker check goes to the curator's attribution checks.
	chk := c.St.S.SpeakerChecks[0]
	act(t, a, page{Tab: tabCheck, Check: "u8", Sub: "lark"}, "mark", map[string]string{speakerKey(chk): "right"}, "zh-Hans")
	if v := c.St.S.SpeakerChecks[0].Verdict; v == nil || v.Mark != "right" {
		t.Errorf("speaker check not marked: %+v", v)
	}
}

// K13 per category, K2 per fork type, K16 per source: each item its own switch, set by the user.
func TestSwitchesPerItem(t *testing.T) {
	a := v4App(t)
	seed(a)
	s := page{Tab: tabSwitches}
	_, v := render(t, a, s, "zh-Hans")
	for _, want := range []string{"sw:K13.health", "sw:K13.family", "sw:K13.emotion", "sw:K13.finance", "k2:deploy", "sw:K16.lark", "量不出", "只由你打开"} {
		if !strings.Contains(v, want) {
			t.Errorf("the switches page has no %q", want)
		}
	}
	act(t, a, s, "switch", map[string]string{"sw:K13.health": "true"}, "zh-Hans")
	sw := a.c.St.S.Switches
	if !entry.On(sw, "K13.health") || entry.On(sw, "K13.family") || sw["K13.health"].By != "user" {
		t.Errorf("K13 is not per category: %+v", sw)
	}
	act(t, a, s, "k2", map[string]string{"k2:deploy": "act"}, "zh-Hans")
	if entry.Switch(sw, "K2.deploy") != "act" || entry.Switch(sw, "K2.other") != "suggest" {
		t.Errorf("K2 is not per fork type: %+v", sw)
	}
	act(t, a, s, "switch", map[string]string{"sw:K16.lark": "false"}, "zh-Hans")
	if curate.LearnPeople(a.c.St.S, "lark") || !curate.LearnPeople(a.c.St.S, "wechat") {
		t.Errorf("K16 is not per source: %+v", sw)
	}
	// Many fork types: the switches page keeps within the hub's 300 items, every type on the K2 pages.
	a.c.St.Lock()
	for i := 0; i < 130; i++ {
		typ := "type-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		a.c.St.S.Entries["j-"+typ] = &entry.Entry{ID: "j-" + typ, Facet: entry.Judgement, Grain: entry.Action, Type: typ, Text: "placeholder " + typ, Status: entry.Candidate, Evidence: ev(entry.SrcAssistant, "2026-11-05", typ)}
	}
	a.c.Publish()
	a.c.St.Unlock()
	render(t, a, s, "zh-Hans")
	_, k2 := render(t, a, page{Tab: tabSwitches, Open: "k2"}, "zh-Hans")
	_, k2b := render(t, a, page{Tab: tabSwitches, Open: "k2", Sub: "60"}, "zh-Hans")
	_, k2c := render(t, a, page{Tab: tabSwitches, Open: "k2", Sub: "120"}, "zh-Hans")
	if !strings.Contains(k2, "后面 71 类") || !strings.Contains(k2b, "第 61–120 类") || !strings.Contains(k2c, "k2:deploy") && !strings.Contains(k2+k2b, "k2:deploy") {
		t.Errorf("K2 pages: %s", k2c)
	}
	_, after := render(t, a, s, "en")
	if !strings.Contains(after, "set by you") {
		t.Errorf("who set it is not said: %s", after)
	}
	if res, _ := act(t, a, s, "switch", map[string]string{"sw:K99": "true"}, "zh-Hans"); !strings.Contains(res.Toast, "unknown switch") || len(sw) != 3 {
		t.Errorf("an unknown switch: %q %+v", res.Toast, sw)
	}
}

// Pin, edit, delete and restore go through the entry API; the user's wording survives.
func TestEntryActions(t *testing.T) {
	a := v4App(t)
	ids := seed(a)
	id := ids["me"]
	s := page{Tab: tabFacets, Facet: string(entry.Persona), Open: "e:" + id}
	act(t, a, s, "act:pin:"+id, nil, "zh-Hans")
	act(t, a, s, "act:edit:"+id, map[string]string{"text": "先看结论，再看细节"}, "zh-Hans")
	e := a.c.St.S.Entries[id]
	if !e.Pinned || e.Shown() != "先看结论，再看细节" {
		t.Fatalf("pin / edit: %+v", e)
	}
	act(t, a, s, "act:delete:"+id, nil, "zh-Hans")
	_, list := render(t, a, page{Tab: tabFacets, Facet: string(entry.Persona)}, "zh-Hans")
	if !e.Deleted || !strings.Contains(list, "删掉的（可以恢复）") {
		t.Errorf("delete is not restorable on the page: %s", list)
	}
	act(t, a, s, "act:restore:"+id, nil, "zh-Hans")
	if e.Deleted {
		t.Errorf("not restored")
	}
	// People: add, remove, merge and split.
	ps := page{Tab: tabPeople}
	act(t, a, ps, "people:remove:person:lark-u3", nil, "zh-Hans")
	if c := a.c.St.S.Contacts["person:lark-u3"]; c.Manual != "removed" {
		t.Errorf("remove: %+v", c)
	}
	act(t, a, ps, "people:add", map[string]string{"add_person": "person:lark-u3"}, "zh-Hans")
	act(t, a, ps, "people:merge", map[string]string{"merge_people": "person:lark-u2,person:lark-u3"}, "zh-Hans")
	if len(a.c.St.S.PersonMerges) != 1 {
		t.Fatalf("merge: %+v", a.c.St.S.PersonMerges)
	}
	_, people := render(t, a, ps, "zh-Hans")
	if !strings.Contains(people, "user-a + user-b") {
		t.Errorf("merged cards are not one row: %s", people)
	}
	act(t, a, ps, "people:split:"+a.c.St.S.PersonMerges[0].ID, nil, "zh-Hans")
	if len(a.c.St.S.PersonMerges) != 0 {
		t.Errorf("split: %+v", a.c.St.S.PersonMerges)
	}
	// Types: until P3's functions are there, the page says so and changes nothing.
	res, _ := act(t, a, page{Tab: tabFacets, Open: "types:judgement"}, "type:rename:judgement", map[string]string{"type_from": "deploy", "type_to": "release"}, "zh-Hans")
	if _, ok := any(a.c).(typeEditor); !ok && !strings.Contains(res.Toast, "P3") {
		t.Errorf("rename without P3: %q", res.Toast)
	}
}

// A self-reported entry and a behaviour entry against it are both shown, and the user picks one.
func TestConflictPick(t *testing.T) {
	a := v4App(t)
	c := a.c
	c.St.Lock()
	self := c.Observe(&entry.Entry{Facet: entry.Interaction, Origin: entry.FromOnboarding, Text: "汇报写长一点", Evidence: ev("", "2026-11-01", "汇报写长一点")}, "t")
	beh := c.Observe(&entry.Entry{Facet: entry.Interaction, Text: "汇报只要三行", Status: entry.Stable, Evidence: ev(entry.SrcAssistant, "2026-11-04", "太长了，三行就够")}, "t")
	c.St.S.Entries[self].Counters = []string{beh}
	c.St.Unlock()
	s := page{Tab: tabFacets, Facet: string(entry.Interaction)}
	_, v := render(t, a, s, "zh-Hans")
	if !strings.Contains(v, "你说的和做的不一致") || !strings.Contains(v, "汇报写长一点") || !strings.Contains(v, "汇报只要三行") {
		t.Fatalf("the conflict is not shown: %s", v)
	}
	act(t, a, s, "pick:"+beh+":"+self, nil, "zh-Hans")
	if !c.St.S.Entries[beh].Pinned || c.St.S.Entries[self].Live() {
		t.Errorf("pick: kept %+v, other %+v", c.St.S.Entries[beh], c.St.S.Entries[self])
	}
}

type notifyHub struct {
	hubapi.None
	sent []string
}

func (h *notifyHub) Notify(_ context.Context, title, body string) error {
	h.sent = append(h.sent, title+"\n"+body)
	return nil
}

// The weekly report: by the hub's time zone, in the target language, not pushed in a week with nothing new.
func TestWeeklyReport(t *testing.T) {
	a := v4App(t)
	ids := seed(a)
	h := &notifyHub{}
	a.hubAPI = h
	la, _ := time.LoadLocation("America/Los_Angeles")
	a.hubLoc = la
	// Monday 01:00 UTC is still Sunday in Los Angeles: the week before is W45 there (W46 in UTC).
	now := time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC)
	a.c.St.S.Runs = append(a.c.St.S.Runs, &store.Run{ID: "w45", Kind: "weekly", Period: "2026-W45", Finished: "x"})
	// Nothing took effect in W45: no push.
	if err := a.weeklyTick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(h.sent) != 0 {
		t.Fatalf("a week with nothing new was pushed: %v", h.sent)
	}
	if led := loadLedger(a.c.St.Dir); led.Week != "2026-W45" || led.Pushed {
		t.Fatalf("ledger: %+v", led)
	}
	// W46 ran too; something took effect in it (by Los Angeles days), and English is the target language.
	a.c.Lang = "en"
	a.c.St.S.Runs = append(a.c.St.S.Runs, &store.Run{ID: "w46", Kind: "weekly", Period: "2026-W46", Finished: "x"})
	a.c.St.S.Entries[ids["ask"]].Effective = "2026-11-12T20:00:00Z"
	a.c.St.S.Readings = append(a.c.St.S.Readings, entry.Reading{Code: "R1", Period: "2026-11-15", Num: 2, Den: 5, Measure: false})
	next := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	if err := a.weeklyTick(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if len(h.sent) != 1 {
		t.Fatalf("sent %d", len(h.sent))
	}
	body := h.sent[0]
	for _, want := range []string{"week 2026-W46", "1 new in effect", "选项要写全称", "not measurable", "How the people cards are used"} {
		if !strings.Contains(body, want) {
			t.Errorf("the report does not say %q: %s", want, body)
		}
	}
	if strings.Contains(body, "新生效") {
		t.Errorf("not in the target language: %s", body)
	}
	// Looked at once a week.
	if err := a.weeklyTick(context.Background(), next.Add(time.Hour)); err != nil || len(h.sent) != 1 {
		t.Errorf("sent again: %d %v", len(h.sent), err)
	}
	// Chinese, by build: the cards note only once.
	led := loadLedger(a.c.St.Dir)
	days, _ := curate.WeekDays("2026-W46")
	rep := buildWeekly(a.c.St.S, "2026-W46", days, "2026-11-23", "zh-Hans", la, led)
	if !strings.Contains(rep.Body, "新生效的 1 条") || strings.Contains(rep.Body, "画像卡会怎么用") {
		t.Errorf("Chinese report: %s", rep.Body)
	}
}

// TestWriteShowcase writes every v4 page, drawn from the placeholder state, as view JSON for the hub web app's
// showcase page (docs/archive): ENKEEP_SHOWCASE_DIR=<dir> go test -run TestWriteShowcase. It also checks drawAll
// finds no view the hub's check rejects.
func TestWriteShowcase(t *testing.T) {
	a := v4App(t)
	seed(a)
	views, counts := drawAll(a, "2026-11-12")
	if bad := counts["view_check_failures"].([]string); len(bad) > 0 {
		t.Fatalf("views the hub rejects: %v", bad)
	}
	dir := os.Getenv("ENKEEP_SHOWCASE_DIR")
	if dir == "" {
		return
	}
	for name, v := range views {
		b, _ := json.MarshalIndent(v, "", " ")
		file := "enkeep-" + strings.NewReplacer("/", "-", "zh-Hans", "zh").Replace(name) + ".json"
		if err := os.WriteFile(filepath.Join(dir, file), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A line that starts with a year or another number is not a list item: only a real list marker is stripped.
func TestLinesKeepsLeadingNumbers(t *testing.T) {
	if got := lines("2026 年上线 Atlas", 3); len(got) != 1 || got[0] != "2026 年上线 Atlas" {
		t.Fatalf("a year was stripped: %q", got)
	}
	got := lines("1. first\n2、second\n3) third\n- fourth\n• fifth", 0)
	want := []string{"first", "second", "third", "fourth", "fifth"}
	if len(got) != len(want) {
		t.Fatalf("%q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%q", got)
		}
	}
}
