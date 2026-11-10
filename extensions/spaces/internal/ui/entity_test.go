package ui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/spaces/internal/kernel"
	"github.com/nwverse/persona-extensions/extensions/spaces/internal/store"
	"github.com/nwverse/persona-extensions/extensions/spaces/internal/templates"
)

// entityID is the id of the entity with the title, in any space of st; the test fails when there is none.
func entityID(t *testing.T, st kernel.Storage, title string) string {
	t.Helper()
	var id string
	if err := st.View(func(b *kernel.Base) error {
		for _, sp := range b.Spaces {
			for _, e := range sp.Entities {
				if e.Title == title {
					id = e.ID
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatalf("no entity titled %q", title)
	}
	return id
}

// stored is a copy of the stored entity with the id, and whether there is one.
func stored(t *testing.T, st kernel.Storage, id string) (kernel.Entity, bool) {
	t.Helper()
	var e kernel.Entity
	found := false
	if err := st.View(func(b *kernel.Base) error {
		if _, got, err := b.Find(id); err == nil {
			e, found = *got, true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return e, found
}

// entityIDs lists every entity of the store with the id of its space.
func entityIDs(t *testing.T, st kernel.Storage) [][2]string {
	t.Helper()
	var out [][2]string
	if err := st.View(func(b *kernel.Base) error {
		for _, sp := range b.Spaces {
			for _, e := range sp.Entities {
				out = append(out, [2]string{sp.ID, e.ID})
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// sectionTitled is the first section of the view with the title; the test fails when there is none.
func sectionTitled(t *testing.T, v *wire.View, title string) wire.Section {
	t.Helper()
	for _, s := range v.Sections {
		if s.Title == title {
			return s
		}
	}
	t.Fatalf("no section %q in %v", title, sectionTitles(v))
	return wire.Section{}
}

// fieldOf is the field of the view with the name.
func fieldOf(v *wire.View, name string) (wire.Component, bool) {
	for _, s := range v.Sections {
		for _, it := range s.Items {
			if it.Type == wire.ComponentField && it.Name == name {
				return it, true
			}
		}
	}
	return wire.Component{}, false
}

// fieldNamed is the field of the view with the name; the test fails when there is none.
func fieldNamed(t *testing.T, v *wire.View, name string) wire.Component {
	t.Helper()
	f, ok := fieldOf(v, name)
	if !ok {
		t.Fatalf("no field %q", name)
	}
	return f
}

// componentsOf are the components of a type in the view, in order.
func componentsOf(v *wire.View, typ string) []wire.Component {
	var out []wire.Component
	for _, s := range v.Sections {
		for _, it := range s.Items {
			if it.Type == typ {
				out = append(out, it)
			}
		}
	}
	return out
}

// buttonTitled is the button of the view with the title.
func buttonTitled(v *wire.View, title string) (wire.Component, bool) {
	for _, s := range v.Sections {
		for _, it := range s.Items {
			if it.Type == wire.ComponentButton && it.Title == title {
				return it, true
			}
		}
	}
	return wire.Component{}, false
}

// pushedState decodes the state of the page a result pushes, which must be a page of this extension.
func pushedState(t *testing.T, res *wire.ActionResult) state {
	t.Helper()
	if res.Push == nil || res.Push.UI != PageUI {
		t.Fatalf("no push to the page: %+v", res)
	}
	var s state
	if err := json.Unmarshal([]byte(res.Push.State), &s); err != nil {
		t.Fatalf("pushed state %q: %v", res.Push.State, err)
	}
	return s
}

// rowTitled is the row of a section with the title.
func rowTitled(t *testing.T, s wire.Section, title string) wire.Component {
	t.Helper()
	for _, it := range s.Items {
		if it.Type == wire.ComponentRow && it.Title == title {
			return it
		}
	}
	t.Fatalf("no row %q in section %q", title, s.Title)
	return wire.Component{}
}

// projectStore is the project template with its samples, and its space.
func projectStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	st := storeWith(t, true, "project")
	return st, firstSpace(t, st)
}

// kindsStore is a blank space with a target type, a type holding a property of every kind (and a rollup), two targets
// and two entities of the type: 条目 (nothing set) and 条目二 (nothing set either).
func kindsStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	st := store.Open("")
	var sid string
	err := st.Update(func(b *kernel.Base, _ time.Time) error {
		sp, err := templates.Apply(b, "blank", "各种", false, testNow)
		if err != nil {
			return err
		}
		sid = sp.ID
		if err := sp.PutType(kernel.Type{Key: "target", Name: "对象"}, testNow); err != nil {
			return err
		}
		all := kernel.Type{
			Key: "all", Name: "全部",
			Flow: []kernel.Stage{{Name: "待办", Phase: kernel.Todo}, {Name: "进行", Phase: kernel.Doing},
				{Name: "完成", Phase: kernel.Done}, {Name: "放弃", Phase: kernel.Dropped}},
			Props: []kernel.Prop{
				{Key: "txt", Name: "文字", Kind: kernel.KindText},
				{Key: "num", Name: "数量", Kind: kernel.KindNumber, Unit: "页"},
				{Key: "flag", Name: "是否", Kind: kernel.KindBoolean},
				{Key: "day", Name: "日期", Kind: kernel.KindDate},
				{Key: "pick", Name: "选项", Kind: kernel.KindEnum, Options: []string{"甲", "乙"}},
				{Key: "site", Name: "网址", Kind: kernel.KindURL},
				{Key: "one", Name: "单个", Kind: kernel.KindRelation, Target: "target", Back: "全部"},
				{Key: "many", Name: "多个", Kind: kernel.KindRelation, Target: "target", Many: true, Back: "多项"},
				{Key: "cnt", Name: "数目", Kind: kernel.KindRollup, Via: "many", Op: "count"},
			},
		}
		if err := sp.PutType(all, testNow); err != nil {
			return err
		}
		for _, title := range []string{"对象甲", "对象乙"} {
			if _, err := sp.PutEntity(b, kernel.EntityInput{Type: "target", Title: &title}, testNow); err != nil {
				return err
			}
		}
		for _, title := range []string{"条目", "条目二"} {
			if _, err := sp.PutEntity(b, kernel.EntityInput{Type: "all", Title: &title}, testNow); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, sid
}

// flowStore is a blank space with a type step: a flow of five main-line stages and three dropped ones, four enumerated
// properties shown as pills, and two entities: 第一步 at the first stage and 第三步 at the third.
func flowStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	st := store.Open("")
	var sid string
	err := st.Update(func(b *kernel.Base, _ time.Time) error {
		sp, err := templates.Apply(b, "blank", "步骤", false, testNow)
		if err != nil {
			return err
		}
		sid = sp.ID
		flow := []kernel.Stage{
			{Name: "甲", Phase: kernel.Todo}, {Name: "乙", Phase: kernel.Doing}, {Name: "丙", Phase: kernel.Doing},
			{Name: "丁", Phase: kernel.Done}, {Name: "戊", Phase: kernel.Done},
			{Name: "停一", Phase: kernel.Dropped}, {Name: "停二", Phase: kernel.Dropped}, {Name: "停三", Phase: kernel.Dropped},
		}
		opts := []string{"好", "坏"}
		step := kernel.Type{
			Key: "step", Name: "步骤", Flow: flow,
			Props: []kernel.Prop{
				{Key: "k1", Name: "甲类", Kind: kernel.KindEnum, Options: opts},
				{Key: "k2", Name: "乙类", Kind: kernel.KindEnum, Options: opts},
				{Key: "k3", Name: "丙类", Kind: kernel.KindEnum, Options: opts},
				{Key: "k4", Name: "丁类", Kind: kernel.KindEnum, Options: opts},
			},
			Card: kernel.Card{Pills: []string{"k1", "k2", "k3", "k4"}},
		}
		if err := sp.PutType(step, testNow); err != nil {
			return err
		}
		third, first := "第三步", "第一步"
		if _, err := sp.PutEntity(b, kernel.EntityInput{Type: "step", Title: &third, Status: strPtr("丙"),
			Values: map[string]any{"k1": "好", "k2": "坏", "k3": "好", "k4": "坏"}}, testNow); err != nil {
			return err
		}
		_, err = sp.PutEntity(b, kernel.EntityInput{Type: "step", Title: &first}, testNow)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, sid
}

// strPtr is a pointer to s, for the optional fields of an input.
func strPtr(s string) *string { return &s }

// eid is the id the kernel gives the entity with the sequence number n.
func eid(n int) string { return "e" + strconv.FormatUint(uint64(n), 36) }

// hostileStore is one hub with ten text properties, a long text, an address, a hundred links to items and a hundred and
// five items pointing at it; with five thousand items in all. It is built directly, so that it checks once.
func hostileStore(t *testing.T) (*store.Store, string, string) {
	t.Helper()
	const items = 4999 // with the hub, five thousand entities: the most a space holds
	hub := kernel.Type{
		Key: "hub", Name: "中心",
		Flow: []kernel.Stage{{Name: "甲", Phase: kernel.Todo}, {Name: "乙", Phase: kernel.Doing}, {Name: "丙", Phase: kernel.Done}},
		Props: []kernel.Prop{
			{Key: "links", Name: "链接的", Kind: kernel.KindRelation, Target: "item", Many: true, Back: "中心"},
			{Key: "url", Name: "网址", Kind: kernel.KindURL},
			{Key: "long", Name: "长文", Kind: kernel.KindText},
		},
	}
	hubVals := map[string]any{"url": "https://example.com/hub", "long": strings.Repeat("长", maxText)}
	var links []string
	for i := 2; i <= 101; i++ {
		links = append(links, eid(i))
	}
	hubVals["links"] = links
	for i := 1; i <= 10; i++ {
		key := fmt.Sprintf("f%d", i)
		hub.Props = append(hub.Props, kernel.Prop{Key: key, Name: "字段" + fmt.Sprint(i), Kind: kernel.KindText})
		hubVals[key] = "值"
	}
	item := kernel.Type{
		Key: "item", Name: "条目",
		Props: []kernel.Prop{{Key: "owner", Name: "所属", Kind: kernel.KindRelation, Target: "hub", Back: "条目"}},
	}
	ents := []*kernel.Entity{{ID: eid(1), Type: "hub", Title: strings.Repeat("题", maxTitle), Status: "甲",
		Values: hubVals, Created: testNow, Updated: testNow}}
	for i := 2; i <= items+1; i++ {
		e := &kernel.Entity{ID: eid(i), Type: "item", Title: fmt.Sprintf("条目 %d", i), Created: testNow, Updated: testNow}
		if i <= 106 {
			e.Values = map[string]any{"owner": []string{eid(1)}}
		}
		ents = append(ents, e)
	}
	sp := &kernel.Space{ID: "big", Name: "压力", Types: []*kernel.Type{&hub, &item}, Entities: ents}
	base := &kernel.Base{Format: 1, Seq: uint64(items + 1), Spaces: []*kernel.Space{sp}}
	st := store.Open("")
	if err := st.Update(func(dst *kernel.Base, _ time.Time) error {
		*dst = *base
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return st, sp.ID, eid(1)
}

func TestEntityPageShowsEveryPart(t *testing.T) {
	st, sid := projectStore(t)
	a := newApp(st)

	// An idea with nothing linked: its badges are the status and its area, its lead a paragraph, and its progress is
	// where it stands along the flow. Both link sections are shown, empty; the lifecycle is a segmented control.
	swim := entityID(t, st, "学会自由泳")
	v := drawPage(t, a, state{S: sid, E: swim}, 5)
	head := v.Sections[0].Items[0]
	if head.Type != wire.ComponentHeader || head.Title != "学会自由泳" || v.Title != "学会自由泳" {
		t.Fatalf("header = %+v, title %q", head, v.Title)
	}
	if len(head.Badges) != 2 || head.Badges[0].Text != "收集" || head.Badges[0].Tone != wire.ToneNeutral || head.Badges[1].Text != "健康" {
		t.Fatalf("badges = %+v", head.Badges)
	}
	if len(head.Facts) != 0 {
		t.Fatalf("facts = %+v", head.Facts)
	}
	if texts := componentsOf(v, wire.ComponentText); len(texts) == 0 || !strings.HasPrefix(texts[0].Text, "先在浅水区") {
		t.Fatalf("the lead is not a paragraph under the header: %+v", texts)
	}
	if bars := componentsOf(v, wire.ComponentProgress); len(bars) != 1 || bars[0].Detail != "0%" {
		t.Fatalf("progress = %+v", bars)
	}
	life := sectionTitled(t, v, "生命周期")
	seg := life.Items[0]
	if seg.Type != wire.ComponentSegmented || seg.Name != "stage" || seg.Value != "收集" || len(seg.Choices) != 3 ||
		seg.Choices[1].Title != "探索" || seg.Choices[2].Title != "搁置" {
		t.Fatalf("lifecycle = %+v", seg)
	}
	for _, title := range []string{"关联 →", "← 被关联"} {
		if s := sectionTitled(t, v, title); len(s.Items) != 1 || s.Items[0].Type != wire.ComponentText {
			t.Fatalf("section %q with nothing linked = %+v", title, s.Items)
		}
	}
	last := v.Sections[len(v.Sections)-1]
	if len(last.Items) != 3 || last.Items[2].Role != wire.ButtonRoleDestructive || last.Items[2].Action.ID != "delete" {
		t.Fatalf("buttons = %+v", last.Items)
	}
	if last.Items[2].Action.Confirm != "删除「学会自由泳」？" {
		t.Fatalf("delete confirm = %q", last.Items[2].Action.Confirm)
	}

	// A goal derived from an idea: its origin reads 源自, its tasks and its note read as backlinks, and its progress is
	// the share of its tasks done.
	goal := entityID(t, st, "连续写满 12 篇周复盘")
	v = drawPage(t, a, state{S: sid, E: goal}, 5)
	out := sectionTitled(t, v, "关联 →")
	if len(out.Items) != 1 || out.Items[0].Subtitle != "源自" || out.Items[0].Title != "每周写一篇复盘" {
		t.Fatalf("outgoing = %+v", out.Items)
	}
	back, _ := stored(t, st, goal)
	if back.Origin == "" || out.Items[0].Action.Push == nil {
		t.Fatalf("origin row = %+v", out.Items[0])
	}
	in := sectionTitled(t, v, "← 被关联")
	tasks, notes := 0, 0
	for _, it := range in.Items {
		switch it.Subtitle {
		case "任务":
			tasks++
		case "记录":
			notes++
		}
	}
	if tasks != 4 || notes != 1 {
		t.Fatalf("backlinks = %d tasks, %d notes: %+v", tasks, notes, in.Items)
	}
	if bars := componentsOf(v, wire.ComponentProgress); len(bars) != 1 || bars[0].Label != "完成度" || bars[0].Detail != "50%" || bars[0].Ratio == nil {
		t.Fatalf("goal progress = %+v", bars)
	}
	if fact := head2Facts(t, v); fact["任务数"] != "4" {
		t.Fatalf("goal facts = %v", fact)
	}

	// The idea it came from: the goal reads as a derivation.
	idea := entityID(t, st, "每周写一篇复盘")
	v = drawPage(t, a, state{S: sid, E: idea}, 5)
	if in := sectionTitled(t, v, "← 被关联"); len(in.Items) != 1 || in.Items[0].Subtitle != "衍生" {
		t.Fatalf("derivation backlink = %+v", in.Items)
	}

	// A task with a goal and a many relation: both outgoing rows, and the task it is blocked by names its back label.
	friend := entityID(t, st, "约一位朋友互相督促")
	v = drawPage(t, a, state{S: sid, E: friend}, 5)
	out = sectionTitled(t, v, "关联 →")
	if got := rowTitled(t, out, "连续写满 12 篇周复盘").Subtitle; got != "目标" {
		t.Fatalf("goal row subtitle = %q", got)
	}
	if got := rowTitled(t, out, "写第 2 篇").Subtitle; got != "被阻塞" {
		t.Fatalf("blocked row subtitle = %q", got)
	}
	second := entityID(t, st, "写第 2 篇")
	v = drawPage(t, a, state{S: sid, E: second}, 5)
	if got := rowTitled(t, sectionTitled(t, v, "← 被关联"), "约一位朋友互相督促").Subtitle; got != "阻塞了" {
		t.Fatalf("back label = %q", got)
	}
	if got := rowTitled(t, sectionTitled(t, v, "关联 →"), "连续写满 12 篇周复盘").Subtitle; got != "目标" {
		t.Fatalf("single relation row = %q", got)
	}
}

// head2Facts are the facts of the header of the view, by label.
func head2Facts(t *testing.T, v *wire.View) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range v.Sections[0].Items[0].Facts {
		out[f.Label] = f.Value
	}
	return out
}

func TestEntityHeaderAndBackLimits(t *testing.T) {
	st, sid := flowStore(t)
	a := newApp(st)
	third := entityID(t, st, "第三步")
	v := drawPage(t, a, state{S: sid, E: third}, 5)
	head := v.Sections[0].Items[0]
	// The status and at most three pills are badges; the fourth pill is left out.
	if len(head.Badges) != 4 || head.Badges[0].Text != "丙" || head.Badges[1].Text != "好" || head.Badges[3].Text != "好" {
		t.Fatalf("badges = %+v", head.Badges)
	}
}

func TestLifecycleIsASegmentOrButtons(t *testing.T) {
	st, sid := flowStore(t)
	a := newApp(st)

	// From the first stage: the next stage and the three dropped ones make five choices with the current one, so the
	// control is a segmented one.
	first := entityID(t, st, "第一步")
	v := drawPage(t, a, state{S: sid, E: first}, 5)
	seg := sectionTitled(t, v, "生命周期").Items[0]
	if seg.Type != wire.ComponentSegmented || len(seg.Choices) != 5 || seg.Choices[1].Title != "乙" {
		t.Fatalf("segmented lifecycle = %+v", seg)
	}

	// From the third stage: five moves and the current stage are too many for a control. The advance comes first.
	third := entityID(t, st, "第三步")
	v = drawPage(t, a, state{S: sid, E: third}, 5)
	buttons := sectionTitled(t, v, "生命周期").Items
	if len(buttons) != 5 || buttons[0].Title != "→ 丁" || buttons[0].Action.ID != "status:丁" || buttons[1].Title != "乙" {
		t.Fatalf("button lifecycle = %+v", buttons)
	}

	// Moved to a dropped stage, every main-line stage is a move, in flow order, and none is the advance.
	res := act(t, a, state{S: sid, E: third}, "status:停二", nil)
	if res.View == nil {
		t.Fatal("no page after a move")
	}
	v = drawPage(t, a, state{S: sid, E: third}, 5)
	buttons = sectionTitled(t, v, "生命周期").Items
	if len(buttons) != 5 || buttons[0].Title != "甲" || buttons[4].Title != "戊" {
		t.Fatalf("dropped lifecycle = %+v", buttons)
	}
	if e, _ := stored(t, st, third); e.Status != "停二" {
		t.Fatalf("status after the move = %q", e.Status)
	}
}

func TestAdvanceAndRefusedMoves(t *testing.T) {
	st, sid := projectStore(t)
	a := newApp(st)
	swim := entityID(t, st, "学会自由泳")
	page := state{S: sid, E: swim}

	cases := []struct {
		name   string
		stage  string
		toast  string // a refusal's sentence, or "" for a move that is made
		status string // the status after the action
	}{
		{"a move that is not allowed is refused", "已孵化", "不能直接变为", "收集"},
		{"the same stage changes nothing", "收集", "", "收集"},
		{"the next stage is allowed", "探索", "", "探索"},
		{"back to the first stage is allowed", "收集", "", "收集"},
		{"a dropped stage is allowed", "搁置", "", "搁置"},
		{"a dropped stage goes to any main-line stage", "已孵化", "", "已孵化"},
		{"an unknown stage is refused", "不存在", "不是「点子」的阶段", "已孵化"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := act(t, a, page, "status", map[string]string{"stage": tc.stage})
			if tc.toast == "" && res.Toast != "" {
				t.Fatalf("toast %q for a move that is allowed", res.Toast)
			}
			if tc.toast != "" && !strings.Contains(res.Toast, tc.toast) {
				t.Fatalf("toast %q, want %q", res.Toast, tc.toast)
			}
			if e, _ := stored(t, st, swim); e.Status != tc.status {
				t.Fatalf("status = %q, want %q", e.Status, tc.status)
			}
		})
	}

	// The refused move names the stages it may go to, and the page is drawn again unchanged. A fresh idea starts at the
	// first stage, where the next stage and the dropped one are the moves.
	fresh, fsid := projectStore(t)
	fa := newApp(fresh)
	idea := entityID(t, fresh, "学会自由泳")
	res := act(t, fa, state{S: fsid, E: idea}, "status", map[string]string{"stage": "已孵化"})
	if !strings.Contains(res.Toast, "可以变为：探索、搁置") || res.View == nil {
		t.Fatalf("refusal = %q", res.Toast)
	}

	// Without a stage there is nothing to move to.
	if res := act(t, fa, state{S: fsid, E: idea}, "status", nil); res.Toast != "没有选择阶段" {
		t.Fatalf("no stage = %q", res.Toast)
	}
}

func TestEditEveryKindOfValue(t *testing.T) {
	st, sid := kindsStore(t)
	a := newApp(st)
	item := entityID(t, st, "条目")
	one := entityID(t, st, "对象甲")
	two := entityID(t, st, "对象乙")
	edit := state{S: sid, E: item, F: "edit"}

	// Every stored kind has its input, built from the descriptor. The rollup has none.
	v := drawPage(t, a, edit, 5)
	if f := fieldNamed(t, v, "p_txt"); f.Kind != wire.FieldKindText || f.Value != "" {
		t.Fatalf("text field = %+v", f)
	}
	if f := fieldNamed(t, v, "p_num"); f.Kind != wire.FieldKindNumber {
		t.Fatalf("number field = %+v", f)
	}
	if f := fieldNamed(t, v, "p_flag"); f.Kind != wire.FieldKindToggle || f.Value != "false" {
		t.Fatalf("toggle field = %+v", f)
	}
	if f := fieldNamed(t, v, "p_day"); f.Kind != wire.FieldKindText || f.Placeholder != "YYYY-MM-DD" {
		t.Fatalf("date field = %+v", f)
	}
	if f := fieldNamed(t, v, "p_pick"); f.Kind != wire.FieldKindChoice || f.Value != "=" || len(f.Choices) != 3 || f.Choices[0].Title != "未设置" {
		t.Fatalf("enum field = %+v", f)
	}
	if f := fieldNamed(t, v, "p_site"); f.Kind != wire.FieldKindText || f.Placeholder != "https://" {
		t.Fatalf("url field = %+v", f)
	}
	if f := fieldNamed(t, v, "p_one"); f.Kind != wire.FieldKindChoice || f.Value != "=" || len(f.Choices) != 3 {
		t.Fatalf("single relation field = %+v", f)
	}
	many := fieldNamed(t, v, "p_many")
	if many.Value != "=" || many.Choices[0].Title != addLabel || len(many.Choices) != 3 {
		t.Fatalf("many relation field = %+v", many)
	}
	if _, ok := fieldOf(v, "p_cnt"); ok {
		t.Fatal("a rollup has a field")
	}

	// Every kind is saved: text is trimmed, numbers and dates parse, a choice and relations take their values.
	res := act(t, a, edit, "update", map[string]string{
		"title": "新标题", "p_txt": " 你好 ", "p_num": "12.5", "p_flag": "true", "p_day": "2026-01-02",
		"p_pick": "=乙", "p_site": "https://example.com/a", "p_one": "=" + one, "p_many": "=" + one,
	})
	if res.Toast != "已保存" || decode(t, res.View).F != "" {
		t.Fatalf("saved = %q, state %+v", res.Toast, decode(t, res.View))
	}
	got, _ := stored(t, st, item)
	if got.Title != "新标题" || got.Values["txt"] != "你好" || got.Values["num"] != 12.5 || got.Values["flag"] != true ||
		got.Values["day"] != "2026-01-02" || got.Values["pick"] != "乙" || got.Values["site"] != "https://example.com/a" {
		t.Fatalf("saved values = %+v", got)
	}
	if fmt.Sprint(got.Values["one"]) != fmt.Sprint([]string{one}) || fmt.Sprint(got.Values["many"]) != fmt.Sprint([]string{one}) {
		t.Fatalf("saved relations = %v / %v", got.Values["one"], got.Values["many"])
	}
	v = drawPage(t, a, state{S: sid, E: item}, 5)
	if links := componentsOf(v, wire.ComponentLink); len(links) != 1 || links[0].URL != "https://example.com/a" {
		t.Fatalf("link of an https address = %+v", links)
	}

	// Another target is added to the list, and the one already held is not added twice.
	act(t, a, edit, "update", map[string]string{"p_many": "=" + two})
	act(t, a, edit, "update", map[string]string{"p_many": "=" + one})
	if got, _ := stored(t, st, item); fmt.Sprint(got.Values["many"]) != fmt.Sprint([]string{one, two}) {
		t.Fatalf("many after adding = %v", got.Values["many"])
	}
	// With the fold open, the targets it holds are not offered again: both are held, so only 添加… is left.
	v = drawPage(t, a, edit, 5)
	if f := fieldNamed(t, v, "p_many"); len(f.Choices) != 1 || f.Choices[0].Title != addLabel {
		t.Fatalf("many choices with both held = %+v", f.Choices)
	}

	// Clearing a value unsets it; a relation with no choice keeps its list; a toggle that was true is saved as false.
	act(t, a, edit, "update", map[string]string{
		"title": "新标题", "p_txt": "", "p_num": "", "p_flag": "false", "p_day": "", "p_pick": "=", "p_site": "", "p_one": "=", "p_many": "=",
	})
	got, _ = stored(t, st, item)
	for _, key := range []string{"txt", "num", "day", "pick", "site", "one"} {
		if _, ok := got.Values[key]; ok {
			t.Fatalf("%s is still set: %+v", key, got.Values)
		}
	}
	if got.Values["flag"] != false || fmt.Sprint(got.Values["many"]) != fmt.Sprint([]string{one, two}) {
		t.Fatalf("after clearing = %+v", got.Values)
	}

	// A plain http address is saved and shown as a fact, not a link.
	act(t, a, edit, "update", map[string]string{"p_site": "http://example.com/a"})
	v = drawPage(t, a, state{S: sid, E: item}, 5)
	if links := componentsOf(v, wire.ComponentLink); len(links) != 0 {
		t.Fatalf("link of an http address = %+v", links)
	}
	if got := head2Facts(t, v); got["网址"] != "example.com/a" {
		t.Fatalf("http fact = %v", got)
	}
}

func TestToggleStaysUnsetUntilSet(t *testing.T) {
	st, sid := kindsStore(t)
	a := newApp(st)
	second := entityID(t, st, "条目二")
	edit := state{S: sid, E: second, F: "edit"}

	// Saving a toggle that was never set, as false, leaves it unset.
	act(t, a, edit, "update", map[string]string{"title": "条目二", "p_flag": "false"})
	if got, _ := stored(t, st, second); hasKey(got.Values, "flag") {
		t.Fatalf("an unset toggle was saved: %+v", got.Values)
	}
	act(t, a, edit, "update", map[string]string{"title": "条目二", "p_flag": "true"})
	if got, _ := stored(t, st, second); got.Values["flag"] != true {
		t.Fatalf("toggle on = %+v", got.Values)
	}
}

// hasKey reports whether the map holds the key.
func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

func TestEditRefusesBadValues(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]string
	}{
		{"an empty title", map[string]string{"title": "  "}},
		{"a number that is not one", map[string]string{"p_num": "十二"}},
		{"a date that is not one", map[string]string{"p_day": "2026-13-01"}},
		{"a choice that is not an option", map[string]string{"p_pick": "=丙"}},
		{"an address that is not http", map[string]string{"p_site": "ftp://example.com"}},
		{"a relation to an entity of another type", map[string]string{"p_one": "=ITEM"}},
		{"a relation with many to another type", map[string]string{"p_many": "=ITEM"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, sid := kindsStore(t)
			a := newApp(st)
			item := entityID(t, st, "条目")
			before, _ := stored(t, st, item)
			// A value naming ITEM names the entity itself, which is of the wrong type for the relations.
			values := map[string]string{}
			for k, v := range tc.values {
				values[k] = strings.ReplaceAll(v, "ITEM", item)
			}
			res := act(t, a, state{S: sid, E: item, F: "edit"}, "update", values)
			if res.Toast == "" || res.Toast == "已保存" {
				t.Fatalf("refusal = %q", res.Toast)
			}
			after, _ := stored(t, st, item)
			if after.Title != before.Title || fmt.Sprint(after.Values) != fmt.Sprint(before.Values) {
				t.Fatalf("a refused change saved: %+v", after)
			}
		})
	}
}

func TestRelationTargetsCanBeRemovedOneByOne(t *testing.T) {
	st, sid := projectStore(t)
	a := newApp(st)
	friend := entityID(t, st, "约一位朋友互相督促")
	second := entityID(t, st, "写第 2 篇")
	first := entityID(t, st, "写第 1 篇")
	edit := state{S: sid, E: friend, F: "edit"}

	// The many relation holds the second; the edit fold offers the newest targets it does not hold, under 添加….
	v := drawPage(t, a, edit, 5)
	blocked := fieldNamed(t, v, "p_blocked_by")
	if blocked.Value != "=" || blocked.Choices[0].Title != addLabel {
		t.Fatalf("blocked_by field = %+v", blocked)
	}
	for _, ch := range blocked.Choices {
		if ch.Value == "="+second {
			t.Fatalf("a held target is offered: %+v", blocked.Choices)
		}
	}

	// Adding a target keeps the ones held.
	act(t, a, edit, "update", map[string]string{"p_blocked_by": "=" + first})
	got, _ := stored(t, st, friend)
	if fmt.Sprint(got.Values["blocked_by"]) != fmt.Sprint([]string{second, first}) {
		t.Fatalf("blocked_by after adding = %v", got.Values["blocked_by"])
	}

	// With the fold open, each target of a many relation reads 移除 and unlinks by its id.
	v = drawPage(t, a, edit, 5)
	row := rowTitled(t, sectionTitled(t, v, "关联 →"), "写第 2 篇")
	if row.Value != "移除" || row.Action.ID != "unlink:blocked_by:"+second {
		t.Fatalf("removable row = %+v", row)
	}
	// The single relation of the same page is not removed from here.
	if goal := rowTitled(t, sectionTitled(t, v, "关联 →"), "连续写满 12 篇周复盘"); goal.Value != "" || goal.Action.Push == nil {
		t.Fatalf("single relation row = %+v", goal)
	}
	res := act(t, a, edit, row.Action.ID, nil)
	if res.View == nil {
		t.Fatal("no page after an unlink")
	}
	got, _ = stored(t, st, friend)
	if fmt.Sprint(got.Values["blocked_by"]) != fmt.Sprint([]string{first}) {
		t.Fatalf("blocked_by after unlinking = %v", got.Values["blocked_by"])
	}

	// The last target goes, and the relation is unset.
	act(t, a, edit, "unlink:blocked_by:"+first, nil)
	if got, _ := stored(t, st, friend); hasKey(got.Values, "blocked_by") {
		t.Fatalf("blocked_by after the last target = %v", got.Values["blocked_by"])
	}

	// A target that is not held, a key that is not a relation, and a key that is not a property are refused.
	for _, action := range []string{"unlink:blocked_by:" + first, "unlink:title:" + second, "unlink:nope:" + second, "unlink:blocked_by"} {
		if res := act(t, a, edit, action, nil); res.Toast != "没有这个关联" {
			t.Fatalf("%s: toast %q", action, res.Toast)
		}
	}
}

func TestDeriveIntoAnotherTypeLandsOnIt(t *testing.T) {
	st, sid := projectStore(t)
	a := newApp(st)
	swim := entityID(t, st, "学会自由泳")
	swimEntity, _ := stored(t, st, swim)

	// The fold offers every type of the space, and the first one that is not the entity's own is chosen.
	v := drawPage(t, a, state{S: sid, E: swim, F: "derive"}, 5)
	typ := fieldNamed(t, v, "type")
	if typ.Value != "=goal" || len(typ.Choices) != 4 {
		t.Fatalf("derive fold = %+v", typ)
	}

	// The copy is a goal with the title given, the values of the same names and the first stage; the source does not change.
	res := act(t, a, state{S: sid, E: swim, F: "derive"}, "derive", map[string]string{"type": "=goal", "title": "  游泳计划 "})
	if res.Toast != "已衍生「游泳计划」" {
		t.Fatalf("derive toast = %q", res.Toast)
	}
	landed := pushedState(t, res)
	derived, ok := stored(t, st, landed.E)
	if !ok || derived.Type != "goal" || derived.Title != "游泳计划" || derived.Origin != swim || derived.Status != "规划中" {
		t.Fatalf("derived = %+v", derived)
	}
	if derived.Values["summary"] != swimEntity.Values["summary"] || derived.Values["area"] != "健康" {
		t.Fatalf("inherited values = %+v", derived.Values)
	}
	if after, _ := stored(t, st, swim); after.Status != swimEntity.Status || after.Title != swimEntity.Title {
		t.Fatalf("the source changed: %+v", after)
	}

	// The page it lands on is the new entity, with its origin as a link out and the source as a derivation.
	v = drawPage(t, a, landed, 5)
	if head := v.Sections[0].Items[0]; head.Title != "游泳计划" {
		t.Fatalf("landed on %q", head.Title)
	}
	if got := rowTitled(t, sectionTitled(t, v, "关联 →"), "学会自由泳").Subtitle; got != "源自" {
		t.Fatalf("origin row = %q", got)
	}
	v = drawPage(t, a, state{S: sid, E: swim}, 5)
	if got := rowTitled(t, sectionTitled(t, v, "← 被关联"), "游泳计划").Subtitle; got != "衍生" {
		t.Fatalf("derivation row = %q", got)
	}

	// Without a type, or into a type that is not there, nothing is made.
	if res := act(t, a, state{S: sid, E: swim, F: "derive"}, "derive", map[string]string{"title": "x"}); res.Toast != "先选择一个类型" {
		t.Fatalf("no type = %q", res.Toast)
	}
	if res := act(t, a, state{S: sid, E: swim}, "derive", map[string]string{"type": "=nope"}); !strings.Contains(res.Toast, "没有类型「nope」") {
		t.Fatalf("unknown type = %q", res.Toast)
	}
}

func TestFoldsOpenAndCloseOneAtATime(t *testing.T) {
	st, sid := projectStore(t)
	a := newApp(st)
	swim := entityID(t, st, "学会自由泳")
	page := state{S: sid, E: swim, F: "edit"}
	if res := act(t, a, page, "fold:edit", nil); decode(t, res.View).F != "" {
		t.Fatalf("the open fold did not close: %+v", decode(t, res.View))
	}
	if res := act(t, a, page, "fold:derive", nil); decode(t, res.View).F != "derive" {
		t.Fatalf("the other fold did not open: %+v", decode(t, res.View))
	}
}

func TestDeleteRemovesTheEntityAndUnlinksIt(t *testing.T) {
	st, sid := projectStore(t)
	a := newApp(st)
	second := entityID(t, st, "写第 2 篇")
	friend := entityID(t, st, "约一位朋友互相督促")

	v := drawPage(t, a, state{S: sid, E: second}, 5)
	confirm := v.Sections[len(v.Sections)-1].Items[2].Action.Confirm
	if confirm != "删除「写第 2 篇」？有 1 条关联会被解除。" {
		t.Fatalf("confirm = %q", confirm)
	}

	res := act(t, a, state{S: sid, E: second}, "delete", nil)
	if !res.Pop || res.View != nil || res.Toast != "已删除「写第 2 篇」" {
		t.Fatalf("delete = %+v", res)
	}
	if _, ok := stored(t, st, second); ok {
		t.Fatal("the entity is still there")
	}
	if got, _ := stored(t, st, friend); hasKey(got.Values, "blocked_by") {
		t.Fatalf("the blocking link stayed: %+v", got.Values)
	}
}

func TestDeleteKeepsDerivedEntities(t *testing.T) {
	st, sid := projectStore(t)
	a := newApp(st)
	idea := entityID(t, st, "每周写一篇复盘")
	goal := entityID(t, st, "连续写满 12 篇周复盘")

	// The idea's only link in is the origin of the goal: it is not unlinked, and the delete says nothing about it.
	v := drawPage(t, a, state{S: sid, E: idea}, 5)
	if confirm := v.Sections[len(v.Sections)-1].Items[2].Action.Confirm; confirm != "删除「每周写一篇复盘」？" {
		t.Fatalf("confirm = %q", confirm)
	}
	if res := act(t, a, state{S: sid, E: idea}, "delete", nil); !res.Pop {
		t.Fatalf("delete = %+v", res)
	}
	// The goal keeps its origin, which now names nothing: its page still draws, without the link.
	v = drawPage(t, a, state{S: sid, E: goal}, 5)
	if out := sectionTitled(t, v, "关联 →"); len(out.Items) != 1 || out.Items[0].Type != wire.ComponentText {
		t.Fatalf("outgoing with a dangling origin = %+v", out.Items)
	}
	if e, _ := stored(t, st, goal); e.Origin != idea {
		t.Fatalf("origin = %q, want the deleted idea %q", e.Origin, idea)
	}
}

func TestEntityActionsRefuseWithoutAnEntity(t *testing.T) {
	st, sid := projectStore(t)
	a := newApp(st)
	swim := entityID(t, st, "学会自由泳")
	stale := state{S: sid, E: "nope"}
	cases := []struct {
		name   string
		s      state
		action string
		values map[string]string
	}{
		{"status of a stale entity", stale, "status", map[string]string{"stage": "探索"}},
		{"update of a stale entity", stale, "update", map[string]string{"title": "x"}},
		{"derive of a stale entity", stale, "derive", map[string]string{"type": "=goal"}},
		{"delete of a stale entity", stale, "delete", nil},
		{"unlink of a stale entity", stale, "unlink:blocked_by:x", nil},
		{"status with no entity", state{S: sid}, "status", map[string]string{"stage": "探索"}},
		{"delete with no entity", state{S: sid}, "delete", nil},
		{"unlink of a target that is not held", state{S: sid, E: swim}, "unlink:blocked_by:x", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := act(t, a, tc.s, tc.action, tc.values)
			if res.Toast == "" {
				t.Fatalf("no refusal: %+v", res)
			}
		})
	}
	// A stale entity is an empty page on the screen below, not an error.
	if res := act(t, a, stale, "more", nil); res.View == nil {
		t.Fatal("no page for a stale entity")
	}
	if got := act(t, a, state{S: sid, E: swim, F: "edit"}, "update", map[string]string{"title": ""}); got.Toast != "记录需要一个标题" {
		t.Fatalf("empty title = %q", got.Toast)
	}
}

func TestEveryEntityPageAtEveryVersion(t *testing.T) {
	st := storeWith(t, true, "project", "reading", "inventory")
	a := newApp(st)
	for _, pair := range entityIDs(t, st) {
		for _, f := range []string{"", "edit", "derive"} {
			for v := 1; v <= 5; v++ {
				s := state{S: pair[0], E: pair[1], F: f}
				checkLowered(t, fmt.Sprintf("%+v v%d", s, v), drawPage(t, a, s, v), v)
			}
		}
	}
}

func TestHostileEntityPageStaysValid(t *testing.T) {
	st, sid, hub := hostileStore(t)
	a := newApp(st)
	for _, f := range []string{"", "edit", "derive"} {
		for v := 1; v <= 5; v++ {
			s := state{S: sid, E: hub, F: f}
			checkLowered(t, fmt.Sprintf("hostile %q v%d", f, v), drawPage(t, a, s, v), v)
		}
	}

	// Ten facts at most, the long text and the address out of the header; a hundred links out, a hundred and five in
	// with a caption for the last five.
	v := drawPage(t, a, state{S: sid, E: hub}, 5)
	head := v.Sections[0].Items[0]
	if len(head.Facts) != factMost {
		t.Fatalf("facts = %d, want %d", len(head.Facts), factMost)
	}
	var bodies []wire.Component
	for _, it := range componentsOf(v, wire.ComponentText) {
		if it.Style == wire.TextStyleBody {
			bodies = append(bodies, it)
		}
	}
	if len(bodies) != 1 || len(bodies[0].Text) != len(strings.Repeat("长", maxText)) {
		t.Fatalf("long text = %d components", len(bodies))
	}
	if links := componentsOf(v, wire.ComponentLink); len(links) != 1 {
		t.Fatalf("links = %d", len(links))
	}
	out := sectionTitled(t, v, "关联 →")
	if len(out.Items) != 100 {
		t.Fatalf("outgoing rows = %d", len(out.Items))
	}
	in := sectionTitled(t, v, "← 被关联")
	if len(in.Items) != linksMost+1 || in.Items[linksMost].Text != "还有 5 条关联没有显示" {
		t.Fatalf("incoming = %d rows, last %+v", len(in.Items), in.Items[len(in.Items)-1])
	}

	// With the edit fold open, every link out of the hub is removable.
	v = drawPage(t, a, state{S: sid, E: hub, F: "edit"}, 5)
	for _, it := range sectionTitled(t, v, "关联 →").Items {
		if it.Value != "移除" {
			t.Fatalf("link without a removal: %+v", it)
		}
	}

	// An item the hub links to, which points back at the hub: one link each way, both to the hub.
	v = drawPage(t, a, state{S: sid, E: eid(2)}, 5)
	hubTitle := strings.Repeat("题", maxTitle)
	if got := rowTitled(t, sectionTitled(t, v, "关联 →"), hubTitle).Subtitle; got != "所属" {
		t.Fatalf("item's link out reads %q", got)
	}
	if got := rowTitled(t, sectionTitled(t, v, "← 被关联"), hubTitle).Subtitle; got != "中心" {
		t.Fatalf("item's link in reads %q", got)
	}
}

func TestEntityNeedsAKnownEntityAndType(t *testing.T) {
	c := &ctx{}
	if _, _, err := c.shown(); err == nil {
		t.Fatal("no entity: no refusal")
	}
	c = &ctx{e: &kernel.Entity{Type: "nope", Title: "孤"}, sp: &kernel.Space{}}
	if _, _, err := c.shown(); err == nil {
		t.Fatal("a type missing from the space: no refusal")
	}
	if _, err := c.entityPage(); err == nil {
		t.Fatal("the page of a type missing from the space: no refusal")
	}
}

func TestEntityHelpers(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"https://example.com/a", true},
		{"http://example.com/a", false},
		{"https://", false},
		{"https://user@example.com/a", false},
		{"::bad", false},
	} {
		if got := httpsURL(tc.in); got != tc.want {
			t.Fatalf("httpsURL(%q) = %v", tc.in, got)
		}
	}

	few := make([]wire.Component, 3)
	if got := capped(few); len(got) != 3 {
		t.Fatalf("capped few = %d", len(got))
	}
	many := make([]wire.Component, linksMost+5)
	got := capped(many)
	if len(got) != linksMost+1 || got[linksMost].Text != "还有 5 条关联没有显示" {
		t.Fatalf("capped many = %d, last %+v", len(got), got[len(got)-1])
	}
	if got := orEmpty(nil, "无"); len(got) != 1 || got[0].Type != wire.ComponentText || got[0].Text != "无" {
		t.Fatalf("orEmpty nil = %+v", got)
	}
	if got := orEmpty(few, "无"); len(got) != 3 {
		t.Fatalf("orEmpty few = %d", len(got))
	}

	if got := addIDs(nil, []string{"a", "a", "b"}); fmt.Sprint(got) != "[a b]" {
		t.Fatalf("addIDs = %v", got)
	}
	if got := addIDs([]string{"a"}, []string{"a"}); fmt.Sprint(got) != "[a]" {
		t.Fatalf("addIDs held = %v", got)
	}

	typ := &kernel.Type{Key: "t", Name: "类", Props: []kernel.Prop{{Key: "p", Name: "属", Kind: kernel.KindText}}}
	if _, ok := propOf(typ, "p"); !ok {
		t.Fatal("declared property not found")
	}
	if _, ok := propOf(typ, "q"); ok {
		t.Fatal("unknown property found")
	}

	urlKind, _ := kernel.KindOf(kernel.KindURL)
	textKind, _ := kernel.KindOf(kernel.KindText)
	if !isURI(urlKind, &kernel.Prop{Kind: kernel.KindURL}) || isURI(textKind, &kernel.Prop{Kind: kernel.KindText}) {
		t.Fatal("isURI")
	}

	sp := &kernel.Space{ID: "s", Types: []*kernel.Type{typ, {Key: "g", Name: "目标", Props: []kernel.Prop{
		{Key: "by", Name: "关", Kind: kernel.KindRelation, Target: "t", Back: "反向"},
		{Key: "plain", Name: "普通", Kind: kernel.KindRelation, Target: "t"},
	}}}}
	c := &ctx{sp: sp}
	if got := c.backLabel(&kernel.Entity{Type: "missing"}, "by"); got != "by" {
		t.Fatalf("back label of a missing type = %q", got)
	}
	if got := c.backLabel(&kernel.Entity{Type: "g"}, "origin"); got != "衍生" {
		t.Fatalf("back label of a derivation = %q", got)
	}
	if got := c.backLabel(&kernel.Entity{Type: "g"}, "by"); got != "反向" {
		t.Fatalf("back label = %q", got)
	}
	if got := c.backLabel(&kernel.Entity{Type: "g"}, "plain"); got != "目标 · 普通" {
		t.Fatalf("back label without a back = %q", got)
	}
}
