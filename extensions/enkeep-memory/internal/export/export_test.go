package export

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// Placeholder entries only (user-a, project-x, host-a…). The model is a fake that returns each item's own text, so the
// tests see the gates, budgets, checks and files, not a model's wording.

type echo struct{ calls int }

func (m *echo) Complete(_ context.Context, r model.Request) (model.Response, error) {
	m.calls++
	var in struct {
		Items []struct {
			I    int    `json:"i"`
			Text string `json:"text"`
			Mode string `json:"mode"`
		} `json:"items"`
	}
	_ = json.Unmarshal([]byte(r.User), &in)
	var out []map[string]any
	for _, it := range in.Items {
		t := it.Text
		if it.Mode == ModeTip {
			t = fmt.Sprintf("给占位人发消息时先写结论再写细节（%d）", len(it.Text))
		}
		out = append(out, map[string]any{"i": it.I, "text": t})
	}
	b, _ := json.Marshal(map[string]any{"items": out})
	return model.Response{Text: string(b)}, nil
}

var now = time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)

func ent(id string, f entry.Facet, g entry.Grain, text string) *entry.Entry {
	return &entry.Entry{ID: id, Facet: f, Grain: g, Text: text, Status: entry.Stable, Origin: entry.Behavior, Count: 3, Kinds: []string{"S1", "S3"}, Last: "2026-01-04"}
}

// fixture has stable entries in every facet, plus what must never go out.
func fixture() *store.State {
	st := &store.State{Entries: map[string]*entry.Entry{}, Contacts: map[string]*entry.Contact{
		"person:p1": {Person: "person:p1", Name: "用户乙", Score: 9},
	}}
	add := func(e *entry.Entry) { st.Entries[e.ID] = e }
	add(ent("i-0000000001", entry.Interaction, entry.Action, "汇报时先给结论，再给细节（占位一）"))
	add(ent("i-0000000002", entry.Interaction, entry.Class, "用户说过的事不要再问一遍（占位二）"))
	t := ent("t-0000000001", entry.Tools, entry.Action, "连 host-a 用占位跳板 entry-1，直连会超时")
	t.Object = "host-a"
	add(t)
	j := ent("j-0000000001", entry.Judgement, entry.Class, "遇到占位岔口时，用户通常选先上线再优化，因为要先看真实负载")
	j.Scopes = []entry.Scope{{Kind: entry.ScopeProject, Name: "project-x"}}
	add(j)
	add(ent("j-0000000002", entry.Judgement, entry.Action, "一次性的占位拍板，不该进说明块"))
	w := ent("w-0000000001", entry.Workflow, entry.Class, "处理占位发布时，用户的做法是先在测试环境跑一遍，因为线上回滚慢")
	w.Type = "release"
	w.Scopes = []entry.Scope{{Kind: entry.ScopeProject, Name: "project-x"}}
	add(w)
	wo := ent("o-0000000001", entry.World, entry.Action, "用户答应用户乙周五前交占位方案")
	wo.Commitment = &entry.Commitment{From: "self", To: "person:p1", What: "周五前交占位方案", Due: "2026-01-09"}
	add(wo)
	fact := ent("o-0000000002", entry.World, entry.Action, "project-x 的占位负责人是用户乙")
	fact.Subject, fact.SingleSource = "project:project-x", true
	add(fact)
	add(ent("u-0000000001", entry.Persona, entry.Class, "用户偏好简短直接的沟通方式（占位）"))
	life := ent("u-0000000002", entry.Persona, entry.Class, "用户周末喜欢在家做占位手工")
	life.Domain = entry.Life
	add(life)
	sens := ent("u-0000000003", entry.Persona, entry.Class, "占位敏感家庭事项不该出现在任何出口")
	sens.Sensitive = entry.SensFamily
	add(sens)
	eval := ent("p-0000000001", entry.People, entry.Action, "用户乙做事拖沓不太靠谱（占位评价）")
	eval.Person, eval.Part, eval.Impact.Moment = "person:p1", entry.PartUserEval, "reply_person"
	eval.Evidence = []entry.Evidence{{Who: entry.WhoSelf, Quote: "他总是拖到最后一刻（占位原话）"}}
	add(eval)
	inf := ent("p-0000000002", entry.People, entry.Action, "推断用户乙晚上回消息更快（占位推断）")
	inf.Person, inf.Part, inf.Impact.Moment, inf.Inferred = "person:p1", entry.PartInferred, "reply_person", true
	add(inf)
	wrong := ent("i-0000000003", entry.Interaction, entry.Action, "被用户点了不对的占位要求")
	wrong.Verdicts = []entry.Verdict{{Mark: "wrong", By: "user"}}
	add(wrong)
	cand := ent("i-0000000004", entry.Interaction, entry.Action, "还是候选的占位要求")
	cand.Status = entry.Candidate
	add(cand)
	return st
}

func compile(t *testing.T, st *store.State, dir string, m model.Model) *Report {
	t.Helper()
	rep, err := Compile(context.Background(), st, Options{Dir: dir, Model: m, Lang: "zh-Hans", Loc: time.UTC, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func read(t *testing.T, dir, p string) string {
	b, err := os.ReadFile(filepath.Join(dir, p))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// allExits is every A–E file's content, joined.
func allExits(t *testing.T, dir string) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && p != filepath.Join(dir, ManifestPath) {
			rel, _ := filepath.Rel(dir, p)
			b, _ := os.ReadFile(p)
			out[rel] = string(b)
		}
		return nil
	})
	return out
}

func TestCompileEveryExit(t *testing.T) {
	dir := t.TempDir()
	st := fixture()
	rep := compile(t, st, dir, &echo{})
	a := read(t, dir, InstructionsPath)
	if len(a) > InstructionsMax || a == "" {
		t.Fatalf("instructions: %d bytes", len(a))
	}
	for _, want := range []string{"## 和用户打交道", "汇报时先给结论", "## 机器和工具", "host-a", "## 默认判断", "先上线再优化", "## 做事方式",
		"## 开着的承诺", "周五前交占位方案（到期 2026-01-09）", "## 关于用户", "简短直接", "周末喜欢在家做占位手工",
		"人物页里标了推断和评价的内容只供参考，不对外转述。", "按你以往会选 X"} {
		if !strings.Contains(a, want) {
			t.Errorf("instructions lack %q:\n%s", want, a)
		}
	}
	for _, not := range []string{"一次性的占位拍板", "被用户点了不对", "还是候选", "用户乙做事拖沓", "推断用户乙", "敏感家庭"} {
		if strings.Contains(a, not) {
			t.Errorf("instructions carry %q", not)
		}
	}
	tools := read(t, dir, "assistant/skills/enkeep-machines-and-tools/SKILL.md")
	if !strings.Contains(tools, "## host-a") || !strings.Contains(tools, "description: \"") || !strings.Contains(tools, "host-a") {
		t.Errorf("tools skill:\n%s", tools)
	}
	work := read(t, dir, "assistant/skills/enkeep-ways-of-working-release/SKILL.md")
	if !strings.Contains(work, "处理「release」这类事情") || !strings.Contains(work, "先在测试环境跑一遍") || strings.Contains(work, "必须先") {
		t.Errorf("workflow skill:\n%s", work)
	}
	proj := read(t, dir, "agent/projects/project-x.md")
	if len(proj) > ProjectMax || !strings.Contains(proj, "先上线再优化") || !strings.Contains(proj, "先在测试环境") || !strings.Contains(proj, "（单一来源）") {
		t.Errorf("project:\n%s", proj)
	}
	inbox := read(t, dir, InboxContextPath)
	if len(inbox) > InboxMax || !strings.Contains(inbox, "来往最多的人：用户乙") || strings.Count(inbox, "和 用户乙：给占位人发消息时先写结论") != 2 {
		t.Errorf("inbox:\n%s", inbox)
	}
	never := read(t, dir, NeverQuotePath)
	for _, want := range []string{"用户乙做事拖沓不太靠谱（占位评价）", "推断用户乙晚上回消息更快（占位推断）", "他总是拖到最后一刻（占位原话）"} {
		if !strings.Contains(never, want+"\n") {
			t.Errorf("never_quote lacks %q", want)
		}
	}
	// ⑤′ evaluations and inferred lines: in never_quote only, nowhere in A, B, D or E's context.
	for p, content := range allExits(t, dir) {
		if p == NeverQuotePath {
			continue
		}
		for _, not := range []string{"拖沓", "拖到最后", "推断用户乙", "敏感家庭", "被用户点了不对", "还是候选"} {
			if strings.Contains(content, not) {
				t.Errorf("%s carries %q", p, not)
			}
		}
	}
	// Bookkeeping: manifest, State.Exits, Effective, Export with its basis, waiting without a hub.
	if st.Exits.Files[InstructionsPath] == nil || st.Exits.Waiting["A"] != "H1" || st.Exits.Waiting["D"] != "H4" || st.Exits.Waiting["E"] != "H5" {
		t.Fatalf("exits %+v", st.Exits)
	}
	e := st.Entries["i-0000000001"]
	if e.Effective != now.Format(time.RFC3339) || e.Export == nil || e.Export.Basis != Basis(e, ModePlain) {
		t.Fatalf("entry %+v", e)
	}
	if st.Entries["i-0000000004"].Effective != "" || st.Entries["u-0000000003"].Effective != "" {
		t.Fatal("a candidate or a sensitive entry took effect")
	}
	if rep.Sections["interaction"] == 0 || rep.Sections["tools"] > SectionBudget["tools"] {
		t.Fatalf("sections %+v", rep.Sections)
	}
	var man manifest
	_ = json.Unmarshal([]byte(read(t, dir, ManifestPath)), &man)
	if man.Exits["A"].Hash == "" || len(man.Exits["B"].Files) != 2 {
		t.Fatalf("manifest %+v", man.Exits)
	}
}

// Exit A stays within 6 KB with far too much to say; every section with content keeps a share; a section with
// nothing is not there at all.
func TestInstructionsBudget(t *testing.T) {
	dir := t.TempDir()
	st := &store.State{Entries: map[string]*entry.Entry{}}
	for i := 0; i < 200; i++ {
		st.Entries[fmt.Sprintf("i-%010d", i)] = ent(fmt.Sprintf("i-%010d", i), entry.Interaction, entry.Class, fmt.Sprintf("第 %d 条占位交互要求，写得比较长一些以便占满预算再多写几个字", i))
		st.Entries[fmt.Sprintf("j-%010d", i)] = ent(fmt.Sprintf("j-%010d", i), entry.Judgement, entry.Class, fmt.Sprintf("第 %d 条占位默认判断，写得比较长一些以便占满预算再多写几个字", i))
	}
	rep := compile(t, st, dir, nil)
	a := read(t, dir, InstructionsPath)
	if len(a) > InstructionsMax {
		t.Fatalf("%d bytes", len(a))
	}
	if rep.Sections["interaction"] == 0 || rep.Sections["judgement"] == 0 || rep.Sections["interaction"] > SectionBudget["interaction"] {
		t.Fatalf("sections %+v", rep.Sections)
	}
	for _, absent := range []string{"## 机器和工具", "## 做事方式", "## 开着的承诺", "## 关于用户"} {
		if strings.Contains(a, absent) {
			t.Errorf("empty section %q is there", absent)
		}
	}
	// Nothing at all: an empty file, no placeholder.
	dir2 := t.TempDir()
	compile(t, &store.State{Entries: map[string]*entry.Entry{}}, dir2, nil)
	if a := read(t, dir2, InstructionsPath); a != "" {
		t.Fatalf("empty state wrote %q", a)
	}
}

func TestCheck(t *testing.T) {
	for text, want := range map[string]string{
		"遇到【岔口】时用户通常选先上线":                                                         BadPlaceholder,
		"用户通常选 X 不选 Y，因为占位理由":                                                     BadPlaceholder,
		"处理 <task> 时用户先跑检查":                                                       BadPlaceholder,
		"参见条目 j-3f2a9c01de 的占位说明":                                                 BadInternalID,
		"把占位消息发给 person:ou_123":                                                   BadInternalID,
		"When releasing, the user runs the checks first because rollback is slow": BadLanguage,
		"我喜欢先看结论再看细节的占位说法":                                                        BadSelf,
		"": BadEmpty,
		"When 业务节奏紧张时, 用户 usually chooses 先上线可用版本 over 慢慢打磨, because 交付时间难料": BadLanguage,
	} {
		got := Check(text, "zh-Hans")
		if !strings.Contains(strings.Join(got, ","), want) {
			t.Errorf("%q: %v, want %s", text, got, want)
		}
	}
	if got := Check("给 Agent 建 worktree 时放在 <repo>/.worktrees/<name> 下，免得弹权限框", "zh-Hans"); got != nil {
		t.Errorf("a path template is not a placeholder: %v", got)
	}
	if got := Check("汇报时先给结论，再给 kubectl 和 project-x 的细节", "zh-Hans"); got != nil {
		t.Errorf("good line rejected: %v", got)
	}
	if got := Check("When releasing, the user runs the checks first", "en"); got != nil {
		t.Errorf("good English line rejected: %v", got)
	}
}

// A rewrite that fails the check is not used: the entry's own text when it passes, else the entry stays out.
type bad struct{}

func (bad) Complete(_ context.Context, r model.Request) (model.Response, error) {
	var in struct {
		Items []struct {
			I int `json:"i"`
		} `json:"items"`
	}
	_ = json.Unmarshal([]byte(r.User), &in)
	var out []map[string]any
	for _, it := range in.Items {
		out = append(out, map[string]any{"i": it.I, "text": "遇到【岔口】时参见 j-0000000001"})
	}
	b, _ := json.Marshal(map[string]any{"items": out})
	return model.Response{Text: string(b)}, nil
}

func TestRewriteRejected(t *testing.T) {
	dir := t.TempDir()
	st := fixture()
	rep := compile(t, st, dir, bad{})
	if len(rep.Rejected) == 0 {
		t.Fatal("nothing rejected")
	}
	a := read(t, dir, InstructionsPath)
	if strings.Contains(a, "【岔口】") || strings.Contains(a, "j-0000000001") || !strings.Contains(a, "汇报时先给结论") {
		t.Fatalf("instructions:\n%s", a)
	}
	// People tips have no fallback: a card's own text is not a tip.
	if strings.Contains(read(t, dir, InboxContextPath), "和 用户乙：") {
		t.Fatal("a rejected tip went out")
	}
}

// Switches: off takes the content out of its exit.
func TestSwitchesOff(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		entry.K1PerTurnBlock: func(t *testing.T, dir string) {
			if a := read(t, dir, InstructionsPath); a != "" {
				t.Errorf("K1 off: %q", a)
			}
		},
		entry.K5ToolsSkill: func(t *testing.T, dir string) {
			if s := read(t, dir, "assistant/skills/enkeep-machines-and-tools/SKILL.md"); s != "" {
				t.Error("K5 off: tools skill written")
			}
			if strings.Contains(read(t, dir, InstructionsPath), "## 机器和工具") {
				t.Error("K5 off: index line left")
			}
		},
		entry.K6WorkflowSkill: func(t *testing.T, dir string) {
			if read(t, dir, "assistant/skills/enkeep-ways-of-working-release/SKILL.md") != "" || read(t, dir, "agent/projects/project-x.md") != "" {
				t.Error("K6 off: workflow skill or project context written")
			}
		},
		entry.K4Persona: func(t *testing.T, dir string) {
			if strings.Contains(read(t, dir, InstructionsPath), "简短直接") || strings.Contains(read(t, dir, InboxContextPath), "简短直接") {
				t.Error("K4 off: ⑥ still out")
			}
		},
		entry.K14People: func(t *testing.T, dir string) {
			if strings.Contains(read(t, dir, InboxContextPath), "和 用户乙") {
				t.Error("K14 off: people tips still out")
			}
		},
		entry.K3Abstract: func(t *testing.T, dir string) {
			if strings.Contains(read(t, dir, InstructionsPath), "占位抽象倾向") {
				t.Error("K3 off: abstract still out")
			}
		},
	}
	for key, check := range cases {
		dir := t.TempDir()
		st := fixture()
		ab := ent("j-0000000009", entry.Judgement, entry.Abstract, "在占位情境一和情境二这类事上，用户有占位抽象倾向")
		st.Entries[ab.ID] = ab
		compile(t, st, dir, &echo{}) // on: written
		if key == entry.K3Abstract && !strings.Contains(read(t, dir, InstructionsPath), "占位抽象倾向") {
			t.Fatal("K3 on: abstract not out")
		}
		st.Switches = map[string]entry.SwitchSetting{key: {Value: "off"}}
		compile(t, st, dir, &echo{})
		check(t, dir)
	}
	// K15 off: inferred tips are out, others stay.
	dir := t.TempDir()
	st := fixture()
	st.Switches = map[string]entry.SwitchSetting{entry.K15Inferred: {Value: "off"}}
	compile(t, st, dir, &echo{})
	if n := strings.Count(read(t, dir, InboxContextPath), "和 用户乙"); n != 1 {
		t.Errorf("K15 off: %d people tips", n)
	}
}

// K13: an opened category reaches the user's own understanding only; no exit file has it.
func TestSensitiveNeverInExits(t *testing.T) {
	dir := t.TempDir()
	st := fixture()
	st.Switches = map[string]entry.SwitchSetting{entry.K13Sensitive + "." + entry.SensFamily: {Value: "on"}}
	p := ent("p-0000000009", entry.People, entry.Action, "用户乙的占位家庭情况评价")
	p.Person, p.Part, p.Sensitive, p.Impact.Moment = "person:p1", entry.PartUserEval, entry.SensFamily, "reply_person"
	st.Entries[p.ID] = p
	compile(t, st, dir, &echo{})
	for path, content := range allExits(t, dir) {
		if strings.Contains(content, "敏感家庭") || strings.Contains(content, "家庭情况") {
			t.Errorf("%s carries a sensitive entry", path)
		}
	}
}

// Life content (not sensitive) goes out like work; with "work only" chosen at the first-run questions it does not.
func TestLifeContent(t *testing.T) {
	dir := t.TempDir()
	st := fixture()
	compile(t, st, dir, &echo{})
	if !strings.Contains(read(t, dir, InstructionsPath), "周末喜欢在家做占位手工") {
		t.Fatal("life entry not out")
	}
	st.Onboarding = &entry.Onboarding{Use: "work"}
	compile(t, st, dir, &echo{})
	for p, content := range allExits(t, dir) {
		if strings.Contains(content, "占位手工") {
			t.Errorf("work only: %s still has the life entry", p)
		}
	}
}

// Marked wrong on the check page: out of every exit at once (a withdrawal is not held to once a day).
func TestWrongWithdrawsAtOnce(t *testing.T) {
	dir := t.TempDir()
	st := fixture()
	compile(t, st, dir, &echo{})
	st.Entries["i-0000000001"].Verdicts = append(st.Entries["i-0000000001"].Verdicts, entry.Verdict{Mark: "wrong", By: "user"})
	rep := compile(t, st, dir, &echo{})
	if rep.Deferred || strings.Contains(read(t, dir, InstructionsPath), "汇报时先给结论") {
		t.Fatal("wrong entry still out")
	}
}

// Unchanged: nothing rewritten, no file written. Exit A at most once a day for additions; the next day it goes.
func TestWritesOnlyChanges(t *testing.T) {
	dir := t.TempDir()
	st := fixture()
	m := &echo{}
	compile(t, st, dir, m)
	calls := m.calls
	rep := compile(t, st, dir, m)
	for p, f := range rep.Files {
		if f.Written {
			t.Errorf("%s rewritten unchanged", p)
		}
	}
	if m.calls != calls || rep.Reused == 0 {
		t.Fatalf("rewrote unchanged entries: %d calls, %d reused", m.calls-calls, rep.Reused)
	}
	// The body changed: rewritten (basis differs), but the user's own exit wording is kept.
	e := st.Entries["i-0000000002"]
	e.Export.User = "用户自己写的占位出口文字"
	e.Text = "用户说过的事不要再问（改过的占位）"
	add := ent("i-0000000010", entry.Interaction, entry.Class, "新加的占位交互要求")
	st.Entries[add.ID] = add
	rep = compile(t, st, dir, m)
	if !rep.Deferred || strings.Contains(read(t, dir, InstructionsPath), "新加的占位") {
		t.Fatal("exit A written twice in a day")
	}
	later := now.Add(24 * time.Hour)
	rep, _ = Compile(context.Background(), st, Options{Dir: dir, Model: m, Lang: "zh-Hans", Loc: time.UTC, Now: func() time.Time { return later }})
	a := read(t, dir, InstructionsPath)
	if rep.Deferred || !strings.Contains(a, "新加的占位") || !strings.Contains(a, "用户自己写的占位出口文字") {
		t.Fatalf("next day:\n%s", a)
	}
	if st.Entries["i-0000000001"].Effective != now.Format(time.RFC3339) || add.Effective != later.Format(time.RFC3339) {
		t.Fatal("Effective is the first time out")
	}
}

// The first-run replay not done: nothing is written.
func TestBackfillWritesNothing(t *testing.T) {
	dir := t.TempDir()
	st := fixture()
	st.Backfill = &store.Backfill{From: "2025-12-01", Next: "2025-12-10"}
	rep := compile(t, st, dir, &echo{})
	if !rep.Backfill || len(allExits(t, dir)) != 0 {
		t.Fatalf("backfill: %+v", allExits(t, dir))
	}
}

type adopter struct {
	hubapi.None
	ads []hubapi.Adoption
}

func (a adopter) Adoptions(context.Context) ([]hubapi.Adoption, error) { return a.ads, nil }

// Readback: the hub's hash matches, or 24 hours later it does not (a warning); a cut version is a warning too.
func TestReadback(t *testing.T) {
	dir := t.TempDir()
	st := fixture()
	compile(t, st, dir, &echo{})
	a := st.Exits.Files[InstructionsPath].Hash
	var man manifest
	_ = json.Unmarshal([]byte(read(t, dir, ManifestPath)), &man)
	h := adopter{ads: []hubapi.Adoption{{Exit: "instructions", Hash: man.Exits["A"].Hash, At: now}, {Exit: "skills", Hash: "old", At: now, Truncated: true}}}
	_ = a
	opt := Options{Dir: dir, Model: &echo{}, Lang: "zh-Hans", Loc: time.UTC, Hub: h, Now: func() time.Time { return now.Add(25 * time.Hour) }}
	if _, err := Compile(context.Background(), st, opt); err != nil {
		t.Fatal(err)
	}
	w := strings.Join(st.Exits.Warnings, "\n")
	if st.Exits.Waiting != nil || !strings.Contains(w, "截断了技能") || !strings.Contains(w, "Agent 项目背景（出口 D）写出 24 小时后，hub 还没有采纳") ||
		strings.Contains(w, "出口 A") || st.Exits.Adopted["A"].Hash == "" {
		t.Fatalf("warnings:\n%s\nwaiting %v", w, st.Exits.Waiting)
	}
}

func TestSlug(t *testing.T) {
	if Slug("Project X") != "project-x" || Slug("发布") == Slug("部署") || !strings.HasPrefix(Slug("persona 插件"), "persona-") {
		t.Fatal(Slug("Project X"), Slug("发布"), Slug("persona 插件"))
	}
}

// The user's own words go out as written whatever the output language: a first-run answer in Chinese stays in an
// English block, while the curator's own wording in the wrong language is still held back.
func TestOwnWordsKeepTheirLanguage(t *testing.T) {
	dir := t.TempDir()
	st := &store.State{Entries: map[string]*entry.Entry{}}
	own := ent("i-own", entry.Interaction, entry.Class, "不要在回复里用英文缩写")
	own.Origin = entry.FromOnboarding
	own.Impact = entry.Impact{Moment: "every_turn", Action: "spell terms out"}
	learned := ent("i-learned", entry.Interaction, entry.Class, "回复时先给结论")
	learned.Impact = entry.Impact{Moment: "every_turn", Action: "conclusion first"}
	st.Entries[own.ID], st.Entries[learned.ID] = own, learned
	_, err := Compile(context.Background(), st, Options{Dir: dir, Model: &echo{}, Lang: "en", Loc: time.UTC, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	a := read(t, dir, InstructionsPath)
	if !strings.Contains(a, "不要在回复里用英文缩写") {
		t.Fatalf("the user's own first-run answer must go out as written:\n%s", a)
	}
	if strings.Contains(a, "回复时先给结论") {
		t.Fatalf("the curator's wording in the wrong language must be held back:\n%s", a)
	}
}

// noChange answers every item with its text and an empty "changes": no moment the assistant would act differently.
type noChange struct{ calls int }

func (m *noChange) Complete(_ context.Context, r model.Request) (model.Response, error) {
	m.calls++
	var in struct {
		Items []struct {
			I    int    `json:"i"`
			Text string `json:"text"`
		} `json:"items"`
	}
	_ = json.Unmarshal([]byte(r.User), &in)
	var out []map[string]any
	for _, it := range in.Items {
		out = append(out, map[string]any{"i": it.I, "text": it.Text, "changes": ""})
	}
	b, _ := json.Marshal(map[string]any{"items": out})
	return model.Response{Text: string(b)}, nil
}

// A line the model can name no moment for stays off the exits (the empty-talk question) and is not asked about again
// while its basis holds; the user's own words go out all the same.
func TestEmptyTalkWithheld(t *testing.T) {
	dir := t.TempDir()
	st := &store.State{Entries: map[string]*entry.Entry{}}
	vague := ent("i-vague", entry.Interaction, entry.Class, "用户注重实际需求（占位）")
	vague.Impact = entry.Impact{Moment: "every_turn", Action: "注意"}
	own := ent("i-own", entry.Interaction, entry.Class, "回复里不要用英文缩写（占位）")
	own.Origin = entry.FromOnboarding
	own.Impact = entry.Impact{Moment: "every_turn", Action: "写全称"}
	st.Entries[vague.ID], st.Entries[own.ID] = vague, own
	m := &noChange{}
	if _, err := Compile(context.Background(), st, Options{Dir: dir, Model: m, Lang: "zh-Hans", Loc: time.UTC, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	a := read(t, dir, InstructionsPath)
	if strings.Contains(a, "注重实际需求") || !strings.Contains(a, "不要用英文缩写") {
		t.Fatalf("instructions:\n%s", a)
	}
	if vague.Export == nil || !vague.Export.Withheld {
		t.Fatalf("not recorded as withheld: %+v", vague.Export)
	}
	calls := m.calls
	if _, err := Compile(context.Background(), st, Options{Dir: dir, Model: m, Lang: "zh-Hans", Loc: time.UTC, Now: func() time.Time { return now.Add(25 * time.Hour) }}); err != nil {
		t.Fatal(err)
	}
	if m.calls != calls {
		t.Fatal("a withheld line with the same basis was asked about again")
	}
}

func TestUnderExportedDropsAClassesBehaviourMembers(t *testing.T) {
	class := &entry.Entry{ID: "i-class", Grain: entry.Class, Origin: entry.Behavior}
	member := &entry.Entry{ID: "i-member", Grain: entry.Action, Parent: "i-class", Origin: entry.Behavior}
	pinned := &entry.Entry{ID: "i-pinned", Grain: entry.Action, Parent: "i-class", Origin: entry.Behavior, Pinned: true}
	orphan := &entry.Entry{ID: "i-orphan", Grain: entry.Action, Parent: "i-elsewhere", Origin: entry.Behavior}
	got := underExported([]*entry.Entry{class, member, pinned, orphan})
	var ids []string
	for _, e := range got {
		ids = append(ids, e.ID)
	}
	if strings.Join(ids, ",") != "i-class,i-pinned,i-orphan" {
		t.Fatalf("underExported = %v", ids)
	}
}

// lineEmpty rewrites every item as it is, with a moment, and then, asked the empty-talk question of the line alone,
// answers no for lines containing "（泛）".
type lineEmpty struct{ steps []string }

func (m *lineEmpty) Complete(_ context.Context, r model.Request) (model.Response, error) {
	m.steps = append(m.steps, r.Step)
	var in struct {
		Items []struct {
			I    int    `json:"i"`
			Text string `json:"text"`
			Line string `json:"line"`
		} `json:"items"`
	}
	_ = json.Unmarshal([]byte(r.User), &in)
	var out []map[string]any
	for _, it := range in.Items {
		if r.Step == "export_emptiness" {
			out = append(out, map[string]any{"i": it.I, "answerable": !strings.Contains(it.Line, "（泛）"), "moment": "", "difference": ""})
		} else {
			out = append(out, map[string]any{"i": it.I, "text": it.Text, "changes": "回复时：照做而不是忽略"})
		}
	}
	b, _ := json.Marshal(map[string]any{"items": out})
	return model.Response{Text: string(b)}, nil
}

// The rewrite names a moment for every item, but the line alone answers the empty-talk question for one only: the
// other is withheld. The user's own words are not asked.
func TestEmptinessOfTheLineAlone(t *testing.T) {
	dir := t.TempDir()
	st := &store.State{Entries: map[string]*entry.Entry{}}
	vague := ent("i-vague", entry.Interaction, entry.Class, "汇报前认真核实（泛）")
	vague.Impact = entry.Impact{Moment: "report", Action: "核实"}
	sharp := ent("i-sharp", entry.Interaction, entry.Class, "列选项时写出每个选项的完整内容（占位）")
	sharp.Impact = entry.Impact{Moment: "ask", Action: "写全"}
	own := ent("i-own", entry.Interaction, entry.Class, "回复要简短（泛）")
	own.Origin = entry.FromOnboarding
	own.Impact = entry.Impact{Moment: "every_turn", Action: "简短"}
	st.Entries[vague.ID], st.Entries[sharp.ID], st.Entries[own.ID] = vague, sharp, own
	m := &lineEmpty{}
	if _, err := Compile(context.Background(), st, Options{Dir: dir, Model: m, Lang: "zh-Hans", Loc: time.UTC, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	a := read(t, dir, InstructionsPath)
	if strings.Contains(a, "认真核实") || !strings.Contains(a, "完整内容") || !strings.Contains(a, "回复要简短") {
		t.Fatalf("instructions:\n%s", a)
	}
	if vague.Export == nil || !vague.Export.Withheld {
		t.Fatalf("not recorded as withheld: %+v", vague.Export)
	}
}

// A line about the user as a person is not withheld for naming no moment (the rewrite's changes), only when, asked as
// a "person" line, it is no specific fact about this user.
func TestPersonaHasItsOwnTest(t *testing.T) {
	dir := t.TempDir()
	st := &store.State{Entries: map[string]*entry.Entry{}}
	role := ent("u-role", entry.Persona, entry.Class, "用户负责 project-x 的数据管线（占位）")
	role.Impact = entry.Impact{Moment: "every_turn", Action: "知道"}
	generic := ent("u-generic", entry.Persona, entry.Class, "用户重视效率（泛）")
	generic.Impact = entry.Impact{Moment: "every_turn", Action: "知道"}
	st.Entries[role.ID], st.Entries[generic.ID] = role, generic
	m := &personLines{}
	if _, err := Compile(context.Background(), st, Options{Dir: dir, Model: m, Lang: "zh-Hans", Loc: time.UTC, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	a := read(t, dir, InstructionsPath)
	if !strings.Contains(a, "数据管线") || strings.Contains(a, "重视效率") {
		t.Fatalf("instructions:\n%s", a)
	}
	if !slices.Contains(m.kinds, "person") || slices.Contains(m.kinds, "behaviour") {
		t.Fatalf("kinds asked: %v", m.kinds)
	}
}

// personLines rewrites with an empty "changes" (no moment) and answers the line-alone question by its "（泛）" mark.
type personLines struct{ kinds []string }

func (m *personLines) Complete(_ context.Context, r model.Request) (model.Response, error) {
	var in struct {
		Items []struct {
			I    int    `json:"i"`
			Text string `json:"text"`
			Kind string `json:"kind"`
			Line string `json:"line"`
		} `json:"items"`
	}
	_ = json.Unmarshal([]byte(r.User), &in)
	var out []map[string]any
	for _, it := range in.Items {
		if r.Step == "export_emptiness" {
			m.kinds = append(m.kinds, it.Kind)
			out = append(out, map[string]any{"i": it.I, "answerable": !strings.Contains(it.Line, "（泛）"), "moment": "", "difference": ""})
		} else {
			out = append(out, map[string]any{"i": it.I, "text": it.Text, "changes": ""})
		}
	}
	b, _ := json.Marshal(map[string]any{"items": out})
	return model.Response{Text: string(b)}, nil
}
