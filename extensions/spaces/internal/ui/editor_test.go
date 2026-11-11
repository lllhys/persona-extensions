package ui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/spaces/internal/templates"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/store"
)

// storedType is a copy of the type of the space with the key, and whether there is one.
func storedType(t *testing.T, st kernel.Storage, space, key string) (kernel.Type, bool) {
	t.Helper()
	var out kernel.Type
	found := false
	if err := st.View(func(b *kernel.Base) error {
		sp, err := b.Space(space)
		if err != nil {
			return err
		}
		if ty := typeIn(sp, key); ty != nil {
			out, found = *ty, true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out, found
}

// mustType is the stored type with the key; the test fails when there is none.
func mustType(t *testing.T, st kernel.Storage, space, key string) kernel.Type {
	t.Helper()
	ty, ok := storedType(t, st, space, key)
	if !ok {
		t.Fatalf("no type %q", key)
	}
	return ty
}

// baseJSON is the whole base as stored, to compare before and after a change.
func baseJSON(t *testing.T, st kernel.Storage) string {
	t.Helper()
	var raw []byte
	if err := st.View(func(b *kernel.Base) error {
		var err error
		raw, err = json.Marshal(b)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// countStatus is the number of entities of the type with the status.
func countStatus(t *testing.T, st kernel.Storage, typ, status string) int {
	t.Helper()
	n := 0
	if err := st.View(func(b *kernel.Base) error {
		for _, sp := range b.Spaces {
			for _, e := range sp.Entities {
				if e.Type == typ && e.Status == status {
					n++
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// formOf is the values of the fields of a view, as a form sends them, with over set on top.
func formOf(v *wire.View, over map[string]string) map[string]string {
	out := map[string]string{}
	for _, s := range v.Sections {
		for _, it := range s.Items {
			if it.Type == wire.ComponentField {
				out[it.Name] = it.Value
			}
		}
	}
	for k, val := range over {
		out[k] = val
	}
	return out
}

// fieldNamesOf are the names of the fields of a view, sorted.
func fieldNamesOf(v *wire.View) []string {
	var out []string
	for _, s := range v.Sections {
		for _, it := range s.Items {
			if it.Type == wire.ComponentField {
				out = append(out, it.Name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// countComponents is the number of components of a view.
func countComponents(v *wire.View) int {
	n := 0
	for _, s := range v.Sections {
		n += len(s.Items)
	}
	return n
}

// wantRefused asserts that an answer refused a change with a toast, and that the view still shows state s.
func wantRefused(t *testing.T, res *wire.ActionResult, s state) {
	t.Helper()
	if res.Toast == "" {
		t.Fatalf("no toast for a refused change: %+v", res)
	}
	if res.View == nil {
		t.Fatalf("a refused change draws the page again: %+v", res)
	}
	if got := decode(t, res.View); got != s {
		t.Fatalf("state after a refusal = %+v, want %+v", got, s)
	}
}

// TestEditorAddsAType creates a type from the form of a new type, refuses a key that is taken, an invalid key and a
// type past the limit of a space, each with a toast and no change.
func TestEditorAddsAType(t *testing.T) {
	st := storeWith(t, true, "project")
	a := newApp(st)
	sp := firstSpace(t, st)

	res := act(t, a, state{S: sp, D: newSlot}, "typenew", map[string]string{"t_key": "book", "t_name": "书"})
	if res.Toast != "已新建「书」" {
		t.Fatalf("toast = %q", res.Toast)
	}
	if got := decode(t, res.View); got.D != "book" || got.F != "" {
		t.Fatalf("the form turns into the editor of the new type: %+v", got)
	}
	if ty := mustType(t, st, sp, "book"); ty.Name != "书" || len(ty.Props) != 0 || len(ty.Flow) != 0 {
		t.Fatalf("new type = %+v", ty)
	}
	if v := drawPage(t, a, state{S: sp, D: "book"}, 5); v.Title != "书" {
		t.Fatalf("editor title = %q", v.Title)
	}

	before := baseJSON(t, st)
	propsBefore := len(mustType(t, st, sp, "idea").Props)
	refusals := []struct {
		name   string
		values map[string]string
	}{
		{"a key that is taken", map[string]string{"t_key": "idea", "t_name": "又一个"}},
		{"an invalid key", map[string]string{"t_key": "Bad Key", "t_name": "坏"}},
		{"an empty name", map[string]string{"t_key": "empty", "t_name": ""}},
	}
	for _, tc := range refusals {
		res := act(t, a, state{S: sp, D: newSlot}, "typenew", tc.values)
		if res.Toast == "" {
			t.Fatalf("%s: no toast", tc.name)
		}
		if got := decode(t, res.View); got.D != newSlot {
			t.Fatalf("%s: state %+v", tc.name, got)
		}
	}
	if baseJSON(t, st) != before {
		t.Fatal("a refused new type changed the base")
	}
	if n := len(mustType(t, st, sp, "idea").Props); n != propsBefore {
		t.Fatalf("the type with the taken key lost properties: %d left", n)
	}

	// The limit of types in a space: the project template has four, so three more fit.
	for _, key := range []string{"a1", "a2"} {
		act(t, a, state{S: sp, D: newSlot}, "typenew", map[string]string{"t_key": key, "t_name": key})
	}
	res = act(t, a, state{S: sp, D: newSlot}, "typenew", map[string]string{"t_key": "a3", "t_name": "a3"})
	if res.Toast == "" {
		t.Fatal("an eighth type is refused")
	}
}

// TestEditorAddsWithoutASpace shows that a new type needs a space.
func TestEditorAddsWithoutASpace(t *testing.T) {
	st := store.Open("")
	res := act(t, newApp(st), state{D: newSlot}, "typenew", map[string]string{"t_key": "x", "t_name": "x"})
	if res.Toast != "还没有空间" {
		t.Fatalf("toast = %q", res.Toast)
	}
}

// TestEditorAddsOnePropertyOfEachKind opens the two steps of adding a property for each kind, sends the values the
// form shows, and checks the property the kind's options made. The inputs shown are exactly those of the kind's Opts.
func TestEditorAddsOnePropertyOfEachKind(t *testing.T) {
	st := storeWith(t, true, "project")
	a := newApp(st)
	sp := firstSpace(t, st)
	overrides := map[string]map[string]string{
		"number":   {"o_unit": "页"},
		"enum":     {"o_options": "甲、乙、丙"},
		"relation": {"o_target": "task", "o_many": "true", "o_back": "目标任务"},
		"rollup":   {"o_via": "task.goal", "o_op": "sum", "o_of": "=effort", "o_unit": "小时"},
	}
	s := state{S: sp, D: "goal"}
	for _, k := range kernel.Kinds {
		key := "x_" + k.Name
		step := act(t, a, s, "fold:addprop", nil)
		if got := decode(t, step.View); got.F != "addprop" {
			t.Fatalf("%s: addprop fold = %+v", k.Name, got)
		}
		opened := act(t, a, decode(t, step.View), "fold:new:"+k.Name, nil)
		var want []string
		for _, opt := range k.Opts {
			want = append(want, "o_"+opt)
		}
		want = append(want, "p_key", "p_name")
		slices.Sort(want)
		if got := fieldNamesOf(opened.View); !slices.Equal(got, want) {
			t.Fatalf("%s: fields %v, want %v", k.Name, got, want)
		}
		over := map[string]string{"p_key": key, "p_name": "名字" + k.Label}
		for name, v := range overrides[k.Name] {
			over[name] = v
		}
		res := act(t, a, decode(t, opened.View), "propnew:"+k.Name, formOf(opened.View, over))
		if res.Toast != "已添加「名字"+k.Label+"」" {
			t.Fatalf("%s: toast %q", k.Name, res.Toast)
		}
		ty := mustType(t, st, sp, "goal")
		p := ty.Props[len(ty.Props)-1]
		if p.Key != key || p.Kind != k.Name {
			t.Fatalf("%s: added %+v", k.Name, p)
		}
		s = decode(t, res.View)
	}
	ty := mustType(t, st, sp, "goal")
	byKey := map[string]kernel.Prop{}
	for _, p := range ty.Props {
		byKey[p.Key] = p
	}
	if p := byKey["x_number"]; p.Unit != "页" {
		t.Fatalf("number unit = %q", p.Unit)
	}
	if p := byKey["x_enum"]; !slices.Equal(p.Options, []string{"甲", "乙", "丙"}) {
		t.Fatalf("enum options = %v", p.Options)
	}
	if p := byKey["x_relation"]; p.Target != "task" || !p.Many || p.Back != "目标任务" {
		t.Fatalf("relation = %+v", p)
	}
	if p := byKey["x_rollup"]; p.Via != "task.goal" || p.Op != "sum" || p.Of != "effort" || p.Unit != "小时" {
		t.Fatalf("rollup = %+v", p)
	}
	if s.D != "goal" || s.F != "" {
		t.Fatalf("after the last property the editor is closed: %+v", s)
	}
}

// TestEditorPropertyDefaultsAndRefusals checks the defaults of a new property's inputs, a duplicate key and an unknown
// kind, and that a property the card or a view still names is refused on delete.
func TestEditorPropertyDefaultsAndRefusals(t *testing.T) {
	st := storeWith(t, true, "project")
	a := newApp(st)
	sp := firstSpace(t, st)
	ed := state{S: sp, D: "goal", F: "new:rollup"}
	v := drawPage(t, a, ed, 5)
	if f := fieldNamed(t, v, "o_via"); f.Value != "task.goal" {
		t.Fatalf("default path = %q", f.Value)
	}
	if f := fieldNamed(t, v, "o_op"); f.Value != "count" {
		t.Fatalf("default aggregate = %q", f.Value)
	}
	if f := fieldNamed(t, v, "o_of"); f.Value != choicePrefix {
		t.Fatalf("default of = %q", f.Value)
	}

	before := baseJSON(t, st)
	res := act(t, a, ed, "propnew:number", map[string]string{"p_key": "progress", "p_name": "重复"})
	if res.Toast != "已经有属性「progress」" {
		t.Fatalf("duplicate key toast = %q", res.Toast)
	}
	res = act(t, a, ed, "propnew:nope", map[string]string{"p_key": "fine", "p_name": "无"})
	if res.Toast != "没有属性类型「nope」" {
		t.Fatalf("unknown kind toast = %q", res.Toast)
	}
	res = act(t, a, ed, "propnew:text", map[string]string{"p_key": "title", "p_name": "内置"})
	if res.Toast == "" {
		t.Fatal("a built-in name is refused")
	}
	if baseJSON(t, st) != before {
		t.Fatal("a refused property changed the base")
	}

	// The summary of the card names the progress rollup, so it cannot be deleted.
	res = act(t, a, state{S: sp, D: "goal", F: "prop:progress"}, "propdrop:progress", nil)
	if res.Toast == "" {
		t.Fatal("a property the card names is refused")
	}
	// The summary text names summary the card lead of the idea type, so that cannot go either.
	res = act(t, a, state{S: sp, D: "idea", F: "prop:summary"}, "propdrop:summary", nil)
	if res.Toast == "" {
		t.Fatal("the lead of the card is refused")
	}
}

// TestEditorRenamesAndReordersAStage renames a stage in place, so that its records follow it, and moves a dropped stage
// up past a done one. A move that puts a doing stage before a todo one is refused.
func TestEditorRenamesAndReordersAStage(t *testing.T) {
	st := storeWith(t, true, "project")
	a := newApp(st)
	sp := firstSpace(t, st)

	res := act(t, a, state{S: sp, D: "idea", F: "stage:1"}, "stagesave:1:探索",
		map[string]string{"stage_name": "探讨", "stage_phase": "doing"})
	if res.Toast != "已保存" {
		t.Fatalf("toast = %q", res.Toast)
	}
	ty := mustType(t, st, sp, "idea")
	if ty.Flow[1].Name != "探讨" || ty.Flow[1].Phase != kernel.Doing {
		t.Fatalf("renamed stage = %+v", ty.Flow[1])
	}
	if n := countStatus(t, st, "idea", "探讨"); n != 1 {
		t.Fatalf("records that follow the rename = %d", n)
	}
	if n := countStatus(t, st, "idea", "探索"); n != 0 {
		t.Fatalf("records still on the old name = %d", n)
	}
	if got := decode(t, res.View); got.F != "" {
		t.Fatalf("the fold closes after a save: %+v", got)
	}

	res = act(t, a, state{S: sp, D: "idea"}, "stageup:3:搁置", nil)
	if res.Toast != "" {
		t.Fatalf("a move toast = %q", res.Toast)
	}
	var names []string
	for _, s := range mustType(t, st, sp, "idea").Flow {
		names = append(names, s.Name)
	}
	if want := []string{"收集", "探讨", "搁置", "已孵化"}; !slices.Equal(names, want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
	if got := decode(t, res.View); got.F != "stage:2" {
		t.Fatalf("the moved stage keeps its fold open: %+v", got)
	}

	before := baseJSON(t, st)
	ed := state{S: sp, D: "idea", F: "stage:1"}
	res = act(t, a, ed, "stageup:1:探讨", nil)
	wantRefused(t, res, ed)
	if baseJSON(t, st) != before {
		t.Fatal("a refused move changed the base")
	}
}

// TestEditorRefusesInvalidChanges refuses changes the kernel does not allow, each with a toast, a page drawn in the same
// state and a base left as it was.
func TestEditorRefusesInvalidChanges(t *testing.T) {
	st := storeWith(t, true, "project")
	a := newApp(st)
	sp := firstSpace(t, st)
	ed := state{S: sp, D: "idea", F: "stage:0"}
	before := baseJSON(t, st)
	cases := []struct {
		name   string
		action string
		values map[string]string
	}{
		{"a stage that goes back a phase", "stagesave:0:收集", map[string]string{"stage_name": "收集", "stage_phase": "done"}},
		{"an empty stage name", "stagesave:0:收集", map[string]string{"stage_name": "", "stage_phase": "todo"}},
		{"an unknown phase", "stagesave:0:收集", map[string]string{"stage_name": "收集", "stage_phase": "bogus"}},
		{"a stage index past the end", "stagesave:9:x", map[string]string{"stage_name": "x", "stage_phase": "todo"}},
		{"a stage that a record uses is dropped", "stagedrop:1:探索", nil},
		{"an unknown key of a property", "propsave:nope", map[string]string{"p_name": "x"}},
		{"a view index past the end", "viewsave:9:x", map[string]string{"v_name": "x", "v_kind": "list"}},
		{"an unknown view kind", "viewsave:0:看板", map[string]string{"v_name": "看板", "v_kind": "bogus"}},
	}
	for _, tc := range cases {
		res := act(t, a, ed, tc.action, tc.values)
		if res.Toast == "" {
			t.Fatalf("%s: no toast", tc.name)
		}
		wantRefused(t, res, ed)
	}
	if baseJSON(t, st) != before {
		t.Fatal("a refused change saved something")
	}
}

// TestEditorEditsCardAndViews changes the card, a view, and adds and deletes views.
func TestEditorEditsCardAndViews(t *testing.T) {
	st := storeWith(t, true, "project")
	a := newApp(st)
	sp := firstSpace(t, st)

	res := act(t, a, state{S: sp, D: "idea", F: "card"}, "cardsave",
		map[string]string{"c_lead": "=summary", "c_pills": "领域", "c_of": "", "c_over": ""})
	if res.Toast != "已保存" {
		t.Fatalf("card toast = %q", res.Toast)
	}
	card := mustType(t, st, sp, "idea").Card
	if card.Lead != "summary" || !slices.Equal(card.Pills, []string{"area"}) || card.Progress != nil {
		t.Fatalf("card = %+v", card)
	}

	res = act(t, a, state{S: sp, D: "goal", F: "card"}, "cardsave",
		map[string]string{"c_lead": "=", "c_pills": "领域、截止", "c_of": "=progress", "c_over": "=tasks"})
	if res.Toast != "已保存" {
		t.Fatalf("progress toast = %q", res.Toast)
	}
	if pr := mustType(t, st, sp, "goal").Card.Progress; pr == nil || pr.Of != "progress" || pr.Over != "tasks" {
		t.Fatalf("progress = %+v", pr)
	}

	before := baseJSON(t, st)
	ed := state{S: sp, D: "goal", F: "card"}
	res = act(t, a, ed, "cardsave", map[string]string{"c_pills": "没有这一个"})
	wantRefused(t, res, ed)
	if baseJSON(t, st) != before {
		t.Fatal("a card that names no field changed the base")
	}

	res = act(t, a, state{S: sp, D: "idea", F: "view:0"}, "viewsave:0:看板", map[string]string{
		"v_name": "看板二", "v_kind": "board", "v_group": "=area", "v_sort": "=title", "v_desc": "true",
	})
	if res.Toast != "已保存" {
		t.Fatalf("view toast = %q", res.Toast)
	}
	v0 := mustType(t, st, sp, "idea").Views[0]
	if v0.Name != "看板二" || v0.Kind != "board" || v0.Group != "area" ||
		len(v0.Sort) != 1 || v0.Sort[0] != (kernel.Order{By: "title", Desc: true}) {
		t.Fatalf("view = %+v", v0)
	}

	res = act(t, a, state{S: sp, D: "idea", F: "view:" + newSlot}, "viewsave:+", map[string]string{
		"v_name": "列表", "v_kind": "list", "v_group": "=", "v_sort": "=", "v_desc": "false",
	})
	if res.Toast != "已保存" || len(mustType(t, st, sp, "idea").Views) != 3 {
		t.Fatalf("new view: toast %q, views %d", res.Toast, len(mustType(t, st, sp, "idea").Views))
	}

	res = act(t, a, state{S: sp, D: "idea", F: "view:2"}, "viewdrop:2:列表", nil)
	if res.Toast != "已删除视图" || len(mustType(t, st, sp, "idea").Views) != 2 {
		t.Fatalf("view delete: toast %q", res.Toast)
	}
	res = act(t, a, state{S: sp, D: "idea"}, "viewdrop:9:x", nil)
	if res.Toast == "" {
		t.Fatal("a view index past the end is refused")
	}
}

// TestEditorDeletesTypes deletes a type with its records, and refuses to delete one that another type's relation points
// at. A deleted type pops the page that showed it.
func TestEditorDeletesTypes(t *testing.T) {
	st := storeWith(t, true, "project")
	a := newApp(st)
	sp := firstSpace(t, st)
	if countStatus(t, st, "note", "") == 0 {
		t.Fatal("the template has records of the note type")
	}

	res := act(t, a, state{S: sp, D: "note"}, "typedrop", nil)
	if !res.Pop || res.View != nil || res.Toast != "已删除「记录」" {
		t.Fatalf("delete answer = %+v", res)
	}
	if _, ok := storedType(t, st, sp, "note"); ok {
		t.Fatal("the type is still there")
	}
	if n := countEntitiesOf(t, st, "note"); n != 0 {
		t.Fatalf("records of the deleted type left = %d", n)
	}

	before := baseJSON(t, st)
	res = act(t, a, state{S: sp, D: "goal"}, "typedrop", nil)
	if res.Toast == "" || res.Pop {
		t.Fatalf("a type a relation points at is refused: %+v", res)
	}
	if baseJSON(t, st) != before {
		t.Fatal("a refused delete changed the base")
	}
}

// countEntitiesOf is the number of entities of the type, in any space.
func countEntitiesOf(t *testing.T, st kernel.Storage, typ string) int {
	t.Helper()
	n := 0
	if err := st.View(func(b *kernel.Base) error {
		for _, sp := range b.Spaces {
			for _, e := range sp.Entities {
				if e.Type == typ {
					n++
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestEditorTypeNameAndIcon changes the name and icon of a type, and an unset icon.
func TestEditorTypeNameAndIcon(t *testing.T) {
	st := storeWith(t, true, "project")
	a := newApp(st)
	sp := firstSpace(t, st)
	res := act(t, a, state{S: sp, D: "idea", F: "name"}, "typeset",
		map[string]string{"t_name": "想法", "t_icon": "=star"})
	if res.Toast != "已保存" {
		t.Fatalf("toast = %q", res.Toast)
	}
	if ty := mustType(t, st, sp, "idea"); ty.Name != "想法" || ty.Icon != "star" {
		t.Fatalf("type = %+v", ty)
	}
	res = act(t, a, state{S: sp, D: "idea", F: "name"}, "typeset", map[string]string{"t_name": "想法", "t_icon": "="})
	if res.Toast != "已保存" || mustType(t, st, sp, "idea").Icon != "" {
		t.Fatalf("unset icon: toast %q", res.Toast)
	}
	ed := state{S: sp, D: "idea", F: "name"}
	wantRefused(t, act(t, a, ed, "typeset", map[string]string{"t_name": ""}), ed)
	wantRefused(t, act(t, a, state{S: sp, D: "nope"}, "typeset", nil), state{S: sp})
}

// TestEditorPropertyOptionsAndOrder changes a property's name and options, keeps the options a form did not send,
// moves a property up, and deletes one nothing names.
func TestEditorPropertyOptionsAndOrder(t *testing.T) {
	st := storeWith(t, true, "project")
	a := newApp(st)
	sp := firstSpace(t, st)

	res := act(t, a, state{S: sp, D: "idea", F: "prop:area"}, "propsave:area", map[string]string{
		"p_name": "领域", "o_options": "工作、生活、学习、健康、旅行",
	})
	if res.Toast != "已保存" {
		t.Fatalf("toast = %q", res.Toast)
	}
	area := mustType(t, st, sp, "idea").Props[1]
	if !slices.Equal(area.Options, []string{"工作", "生活", "学习", "健康", "旅行"}) {
		t.Fatalf("options = %v", area.Options)
	}
	act(t, a, state{S: sp, D: "idea", F: "prop:area"}, "propsave:area", map[string]string{"p_name": "领域二"})
	if got := mustType(t, st, sp, "idea").Props[1]; got.Name != "领域二" || len(got.Options) != 5 {
		t.Fatalf("a form that did not send options keeps them: %+v", got)
	}

	ed := state{S: sp, D: "idea"}
	wantRefused(t, act(t, a, ed, "propsave:nope", nil), ed)
	wantRefused(t, act(t, a, ed, "propup:summary", nil), ed)
	wantRefused(t, act(t, a, ed, "propup:nope", nil), ed)

	res = act(t, a, state{S: sp, D: "idea", F: "prop:area"}, "propup:area", nil)
	if res.Toast != "" {
		t.Fatalf("a move toast = %q", res.Toast)
	}
	if got := mustType(t, st, sp, "idea").Props[0].Key; got != "area" {
		t.Fatalf("first property after the move = %q", got)
	}

	act(t, a, ed, "propnew:text", map[string]string{"p_key": "extra", "p_name": "额外"})
	res = act(t, a, state{S: sp, D: "idea", F: "prop:extra"}, "propdrop:extra", nil)
	if res.Toast != "已删除属性" {
		t.Fatalf("delete toast = %q", res.Toast)
	}
	wantRefused(t, act(t, a, ed, "propdrop:nope", nil), ed)

	// A text that names a field the type does not have is refused before anything is saved.
	res = act(t, a, state{S: sp, D: "idea", F: "card"}, "cardsave", map[string]string{"c_pills": "没有"})
	if res.Toast != "没有字段「没有」" {
		t.Fatalf("pill toast = %q", res.Toast)
	}
}

// TestEditorStagesFromAnEmptyFlow adds the first two stages of a type with no lifecycle at once, and refuses to leave
// fewer than two on the main line.
func TestEditorStagesFromAnEmptyFlow(t *testing.T) {
	st := storeWith(t, true, "project")
	a := newApp(st)
	sp := firstSpace(t, st)
	act(t, a, state{S: sp, D: newSlot}, "typenew", map[string]string{"t_key": "plain", "t_name": "清单"})

	ed := state{S: sp, D: "plain", F: "stage:+"}
	res := act(t, a, ed, "stagesave:+", map[string]string{
		"stage_name": "待办", "stage_phase": "todo", "stage2_name": "完成", "stage2_phase": "done",
	})
	if res.Toast != "已保存" {
		t.Fatalf("toast = %q", res.Toast)
	}
	if flow := mustType(t, st, sp, "plain").Flow; len(flow) != 2 || flow[1].Phase != kernel.Done {
		t.Fatalf("flow = %+v", flow)
	}

	res = act(t, a, state{S: sp, D: "plain", F: "stage:+"}, "stagesave:+", map[string]string{
		"stage_name": "搁置", "stage_phase": "dropped",
	})
	if res.Toast != "已保存" || len(mustType(t, st, sp, "plain").Flow) != 3 {
		t.Fatalf("third stage: toast %q", res.Toast)
	}

	res = act(t, a, state{S: sp, D: "plain", F: "stage:0"}, "stagedrop:0:待办", nil)
	if res.Toast == "" || len(mustType(t, st, sp, "plain").Flow) != 3 {
		t.Fatalf("leaving one stage on the main line is refused: %+v", res)
	}
	res = act(t, a, state{S: sp, D: "plain"}, "stagedrop:2:搁置", nil)
	if res.Toast != "已删除阶段" || len(mustType(t, st, sp, "plain").Flow) != 2 {
		t.Fatalf("drop the dropped stage: toast %q", res.Toast)
	}
	res = act(t, a, state{S: sp, D: "plain"}, "stagedrop:1:完成", nil)
	if res.Toast == "" || len(mustType(t, st, sp, "plain").Flow) != 2 {
		t.Fatalf("a flow of one stage is refused: %+v", res)
	}
	wantRefused(t, act(t, a, state{S: sp, D: "plain"}, "stageup:0:待办", nil), state{S: sp, D: "plain"})
	wantRefused(t, act(t, a, state{S: sp, D: "plain"}, "stagedrop:9:x", nil), state{S: sp, D: "plain"})
}

// TestEditorScreensAtEveryVersion folds every row of every type's editor at every version: each view is valid, lowered to the
// version it asks for, and the state stays small.
func TestEditorScreensAtEveryVersion(t *testing.T) {
	cases := []struct {
		name  string
		store *store.Store
	}{
		{"project", storeWith(t, true, "project")},
		{"all templates", storeWith(t, true, "project", "reading", "inventory", "blank")},
		{"without samples", storeWith(t, false, "reading")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newApp(tc.store)
			for _, s := range editorStates(t, tc.store) {
				for v := 1; v <= 5; v++ {
					label := fmt.Sprintf("editor %+v v%d", s, v)
					page := drawPage(t, a, s, v)
					checkLowered(t, label, page, v)
					if n := countComponents(page); n > 300 {
						t.Fatalf("%s: %d components", label, n)
					}
				}
			}
		})
	}
}

// editorStates are the states that open the editor of every type with each of its folds, and the form of a new type.
func editorStates(t *testing.T, st kernel.Storage) []state {
	t.Helper()
	var out []state
	if err := st.View(func(b *kernel.Base) error {
		for _, sp := range b.Spaces {
			out = append(out, state{S: sp.ID, D: newSlot})
			for _, ty := range sp.Types {
				base := state{S: sp.ID, D: ty.Key}
				out = append(out, base)
				folds := []string{"name", "stage:" + newSlot, "addprop", "card", "view:" + newSlot}
				for i := range ty.Flow {
					folds = append(folds, "stage:"+strconv.Itoa(i))
				}
				for _, p := range ty.Props {
					folds = append(folds, "prop:"+p.Key)
				}
				for _, k := range kernel.Kinds {
					folds = append(folds, "new:"+k.Name)
				}
				for i := range ty.Views {
					folds = append(folds, "view:"+strconv.Itoa(i))
				}
				for _, f := range folds {
					s := base
					s.F = f
					out = append(out, s)
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// hostileEditorStore is a space with one type of the worst shapes the editor draws: markdown in every name, 24
// properties, 8 stages, 5 views, options that do and do not fit a text, and 300 records.
func hostileEditorStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	st := store.Open("")
	var space string
	err := st.Update(func(b *kernel.Base, _ time.Time) error {
		sp, err := templates.Apply(b, "blank", "**空间**", false, testNow)
		if err != nil {
			return err
		}
		space = sp.ID
		if err := sp.PutType(hostileType(), testNow); err != nil {
			return err
		}
		for i := 0; i < 300; i++ {
			title := fmt.Sprintf("**标题** [x](https://example.com/%d) %s", i, strings.Repeat("长", 150))
			long := strings.Repeat("字", 2000)
			values := map[string]any{"p00": long}
			if _, err := sp.PutEntity(b, kernel.EntityInput{Type: "hostile", Title: &title, Values: values}, testNow); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, space
}

// hostileType is the type of hostileEditorStore.
func hostileType() kernel.Type {
	ty := kernel.Type{Key: "hostile", Name: "**乱码**"}
	for i := 0; i < 8; i++ {
		phase := kernel.Doing
		switch {
		case i == 0:
			phase = kernel.Todo
		case i >= 5:
			phase = kernel.Done
		}
		if i == 7 {
			phase = kernel.Dropped
		}
		ty.Flow = append(ty.Flow, kernel.Stage{Name: fmt.Sprintf("阶段**%d**", i), Phase: phase})
	}
	long := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		long = append(long, fmt.Sprintf("%040d", i))
	}
	for i := 0; i < 24; i++ {
		key := fmt.Sprintf("p%02d", i)
		name := fmt.Sprintf("**%02d** [链接](https://example.com/%d)", i, i)
		p := kernel.Prop{Key: key, Name: name, Kind: kernel.KindText}
		switch {
		case i == 5: // 50 options of 40 runes do not fit a text of 2000 runes
			p.Kind, p.Options = kernel.KindEnum, long
		case i == 22: // 48 options of 40 runes do
			p.Kind, p.Options = kernel.KindEnum, long[:48]
		case i%6 == 1:
			p.Kind, p.Unit = kernel.KindNumber, "页"
		case i%6 == 2:
			p.Kind = kernel.KindBoolean
		case i%6 == 3:
			p.Kind = kernel.KindURL
		case i%6 == 4:
			p.Kind = kernel.KindDate
		}
		ty.Props = append(ty.Props, p)
	}
	ty.Card = kernel.Card{Lead: "p00", Pills: []string{"p02", "p03", "p04"}}
	for i := 0; i < 5; i++ {
		kinds := []string{"list", "board", "table", "progress", "feed"}
		ty.Views = append(ty.Views, kernel.View{Name: fmt.Sprintf("视图**%d**", i), Kind: kinds[i]})
	}
	return ty
}

// TestHostileEditorStaysValid draws the editor of a hostile type with every fold open, at every version. The options
// that do not fit a text are not shown, and the options that do are shown whole.
func TestHostileEditorStaysValid(t *testing.T) {
	st, space := hostileEditorStore(t)
	a := newApp(st)
	var sawLong, sawFit bool
	for _, s := range editorStates(t, st) {
		if s.S != space {
			continue
		}
		for v := 1; v <= 5; v++ {
			label := fmt.Sprintf("hostile %+v v%d", s, v)
			page := drawPage(t, a, s, v)
			checkLowered(t, label, page, v)
			if n := countComponents(page); n > 300 {
				t.Fatalf("%s: %d components", label, n)
			}
			if len(page.State) >= 16*1024 {
				t.Fatalf("%s: state of %d bytes", label, len(page.State))
			}
			if v != 5 {
				continue
			}
			switch s.F {
			case "prop:p05":
				// 50 options of 40 runes do not fit a text of 2000 runes: a caption, and no field.
				if fieldNamedOK(page, "o_options") || !slices.Contains(textOfCaptions(page), "选项太多，不能在这里编辑，请在助手中修改") {
					t.Fatalf("%s: options that do not fit are not offered as a caption", label)
				}
				sawLong = true
			case "prop:p22":
				if !fieldNamedOK(page, "o_options") {
					t.Fatalf("%s: options that fit are not offered as a field", label)
				}
				sawFit = true
			}
		}
	}
	if !sawLong || !sawFit {
		t.Fatalf("the options folds were not drawn: long %v, fit %v", sawLong, sawFit)
	}
}

// fieldNamedOK tells whether the view has a field with the name.
func fieldNamedOK(v *wire.View, name string) bool {
	_, ok := fieldOf(v, name)
	return ok
}

// textOfCaptions are the texts of the view's captions.
func textOfCaptions(v *wire.View) []string {
	var out []string
	for _, s := range v.Sections {
		for _, it := range s.Items {
			if it.Type == wire.ComponentText {
				out = append(out, it.Text)
			}
		}
	}
	return out
}

// TestHostileEditorFieldFitsAText checks that 48 options of 40 runes, which fit a text of 2000 runes, are a field whose
// value is that text.
func TestHostileEditorFieldFitsAText(t *testing.T) {
	st, space := hostileEditorStore(t)
	a := newApp(st)
	v := drawPage(t, a, state{S: space, D: "hostile", F: "prop:p22"}, 5)
	f := fieldNamed(t, v, "o_options")
	if n := utf8.RuneCountInString(f.Value); n != 48*40+47 || n > maxText {
		t.Fatalf("options text of %d runes", n)
	}
}

// TestEditorHelpers covers the small functions of the editor on their own cases.
func TestEditorHelpers(t *testing.T) {
	sp := &kernel.Space{Types: []*kernel.Type{{Key: "task", Name: "任务"}}}
	c := &ctx{sp: sp}

	if got := splitList(""); got != nil {
		t.Fatalf("splitList(\"\") = %v", got)
	}
	if got := splitList(" 甲 、、乙 "); !slices.Equal(got, []string{"甲", "乙"}) {
		t.Fatalf("splitList = %v", got)
	}

	orders := []kernel.Order{{By: "a"}, {By: "b"}, {By: "c"}}
	sortCases := []struct {
		name string
		old  []kernel.Order
		by   string
		want []kernel.Order
	}{
		{"none and none", nil, "", nil},
		{"a new key", nil, "a", []kernel.Order{{By: "a", Desc: true}}},
		{"clear the key keeps the rest", orders, "", []kernel.Order{{By: "b"}, {By: "c"}}},
		{"replace the key keeps the rest", orders, "q", []kernel.Order{{By: "q", Desc: true}, {By: "b"}, {By: "c"}}},
	}
	for _, tc := range sortCases {
		got := sortWith(tc.old, tc.by, true)
		if !slices.Equal(got, tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
			t.Fatalf("%s: sortWith = %v, want %v", tc.name, got, tc.want)
		}
	}

	names := []string{"甲", "乙", "丙"}
	if i, err := slotOf(newSlot, names, "阶段"); i != -1 || err != nil {
		t.Fatalf("slotOf(+) = %d, %v", i, err)
	}
	if i, err := slotOf("2:丙", names, "阶段"); i != 2 || err != nil {
		t.Fatalf("slotOf(2:丙) = %d, %v", i, err)
	}
	// An index without the name of the item there is refused: the name is what the fold was drawn with.
	for _, bad := range []string{"3:x", "-1:甲", "x", "2", "1:甲", ""} {
		if _, err := slotOf(bad, names, "阶段"); err == nil {
			t.Fatalf("slotOf(%q) is refused", bad)
		}
	}

	for ph, want := range map[kernel.Phase]string{
		kernel.Todo: wire.ToneNeutral, kernel.Doing: wire.ToneCaution, kernel.Done: wire.TonePositive, kernel.Dropped: wire.ToneNeutral,
	} {
		if got := phaseTone(ph); got != want {
			t.Fatalf("phaseTone(%s) = %s", ph, got)
		}
	}

	if got := aggLabel("sum"); got != "合计" {
		t.Fatalf("aggLabel(sum) = %q", got)
	}
	if got := aggLabel("nope"); got != "nope" {
		t.Fatalf("aggLabel(nope) = %q", got)
	}

	if got := c.propSummary(kernel.Prop{Kind: "bogus"}); got != "bogus" {
		t.Fatalf("unknown kind summary = %q", got)
	}
	if got := c.propSummary(kernel.Prop{Key: "k", Name: "n", Kind: kernel.KindNumber, Unit: "页"}); got != "数字 · 页" {
		t.Fatalf("number summary = %q", got)
	}
	if got := c.propSummary(kernel.Prop{Kind: kernel.KindNumber}); got != "数字" {
		t.Fatalf("plain number summary = %q", got)
	}
	if got := c.propSummary(kernel.Prop{Kind: kernel.KindRelation, Target: "task", Many: true}); got != "关联 → 任务 · 多个" {
		t.Fatalf("relation summary = %q", got)
	}
	if got := c.propSummary(kernel.Prop{Kind: kernel.KindRelation, Target: "gone"}); got != "关联 → gone" {
		t.Fatalf("relation to a missing type = %q", got)
	}
	if got := c.propSummary(kernel.Prop{Kind: kernel.KindRollup, Op: "percent_done", Via: "task.goal"}); got != "汇总 · 完成度 · task.goal" {
		t.Fatalf("rollup summary = %q", got)
	}
	if got := c.propSummary(kernel.Prop{Kind: kernel.KindRollup, Op: "count"}); got != "汇总 · 数量" {
		t.Fatalf("rollup without a path = %q", got)
	}

	ty := &kernel.Type{Key: "t", Name: "T", Props: []kernel.Prop{{Key: "lead", Name: "主", Kind: kernel.KindText}, {Key: "pct", Name: "百分", Kind: kernel.KindRollup}}}
	sp2 := &kernel.Space{Types: []*kernel.Type{ty}}
	c2 := &ctx{sp: sp2}
	if got := c2.cardSummary(ty); got != "按默认" {
		t.Fatalf("empty card summary = %q", got)
	}
	ty.Card = kernel.Card{Lead: "lead", Pills: []string{"pct"}, Progress: &kernel.Ratio{Of: "pct", Over: "lead"}}
	if got := c2.cardSummary(ty); got != "主行 主 · 标签 百分 · 进度 百分 / 主" {
		t.Fatalf("card summary = %q", got)
	}
	ty.Card = kernel.Card{Progress: &kernel.Ratio{Of: "pct"}}
	if got := c2.cardSummary(ty); got != "进度 百分" {
		t.Fatalf("progress only summary = %q", got)
	}
	if got := c2.viewSummary(ty, kernel.View{Kind: "board", Lens: kernel.Lens{Group: "lead"}}); got != "看板 · 按 主 分组" {
		t.Fatalf("grouped view summary = %q", got)
	}
	if got := c2.viewSummary(ty, kernel.View{Kind: "weird"}); got != "weird" {
		t.Fatalf("unknown view kind summary = %q", got)
	}

	orig := kernel.Type{Card: kernel.Card{Progress: &kernel.Ratio{Of: "a"}}, Flow: []kernel.Stage{{Name: "x", Phase: kernel.Todo}}}
	cl := cloneType(&orig)
	cl.Card.Progress.Of = "changed"
	cl.Flow[0].Name = "changed"
	if orig.Card.Progress.Of != "a" || orig.Flow[0].Name != "x" {
		t.Fatal("cloneType shares its progress or flow with the original")
	}
	if cl := cloneType(&kernel.Type{}); cl.Card.Progress != nil {
		t.Fatal("cloneType invents a progress")
	}

	if ch := requiredChoice("n", "名", nil, "x"); ch.Type != wire.ComponentText {
		t.Fatalf("a required choice without options is a caption: %+v", ch)
	}
	opts := []wire.Choice{{Value: "a", Title: "A"}, {Value: "b"}}
	if ch := requiredChoice("n", "名", opts, "zzz"); ch.Value != "a" {
		t.Fatalf("an unknown current value falls to the first option: %q", ch.Value)
	}
	if ch := requiredChoice("n", "名", opts, "b"); ch.Value != "b" {
		t.Fatalf("a current value is kept: %q", ch.Value)
	}
	if ch := optionalChoice("n", "名", opts, "zzz"); ch.Value != choicePrefix {
		t.Fatalf("an unknown optional value is unset: %q", ch.Value)
	}
	if ch := optionalChoice("n", "名", opts, "b"); ch.Value != choicePrefix+"b" || ch.Choices[2].Title != "b" {
		t.Fatalf("an optional value is kept with its title: %+v", ch)
	}
	if got := viaPathsOf(c2, ty); len(got) != 0 {
		t.Fatalf("paths of a type with no relation: %v", got)
	}
	if got := c.typeName("nope"); got != "nope" {
		t.Fatalf("typeName of a missing type = %q", got)
	}
	if got := c.fieldName(ty, "nope"); got != "nope" {
		t.Fatalf("fieldName of a missing field = %q", got)
	}
}

// viaPathsOf is the paths of the type, for a test that only checks their number.
func viaPathsOf(c *ctx, t *kernel.Type) []viaPath { return c.viaPaths(t) }

// TestEditorKeepsWhatTheFormDidNotSend saves folds whose fields the form did not send: nothing they do not show changes,
// and an argument that names nothing is refused.
func TestEditorKeepsWhatTheFormDidNotSend(t *testing.T) {
	st := storeWith(t, true, "project")
	a := newApp(st)
	sp := firstSpace(t, st)
	before := baseJSON(t, st)

	goal := mustType(t, st, sp, "goal")
	for _, action := range []string{"typeset", "cardsave", "stagesave:0:" + goal.Flow[0].Name, "viewsave:0:" + goal.Views[0].Name} {
		act(t, a, state{S: sp, D: "goal", F: "name"}, action, nil)
	}
	if baseJSON(t, st) != before {
		t.Fatal("a save with no fields changed the base")
	}

	ed := state{S: sp, D: "goal"}
	wantRefused(t, act(t, a, ed, "viewdrop:x", nil), ed)
	wantRefused(t, act(t, a, ed, "stageup:x", nil), ed)
	wantRefused(t, act(t, a, ed, "stagedrop:x", nil), ed)
	wantRefused(t, act(t, a, ed, "propup:", nil), ed)
	wantRefused(t, act(t, a, state{S: sp, D: "goal", F: "view:" + newSlot}, "viewsave:x", nil), state{S: sp, D: "goal", F: "view:" + newSlot})
	wantRefused(t, act(t, a, ed, "stagesave:x", nil), ed)
}

// TestEditorKeepsAnIconTheFoldDoesNotOffer: an icon the apps do not draw is the current choice of the name fold, so that
// saving the name keeps it.
func TestEditorKeepsAnIconTheFoldDoesNotOffer(t *testing.T) {
	st := store.Open("")
	var sp string
	err := st.Update(func(b *kernel.Base, _ time.Time) error {
		s, err := templates.Apply(b, "blank", "书架", false, testNow)
		if err != nil {
			return err
		}
		sp = s.ID
		return s.PutType(kernel.Type{Key: "book", Name: "书", Icon: "book"}, testNow)
	})
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(st)
	ed := state{S: sp, D: "book", F: "name"}
	icon := fieldNamed(t, drawPage(t, a, ed, 5), "t_icon")
	if icon.Value != choicePrefix+"book" {
		t.Fatalf("icon field starts at %q, want the icon of the type", icon.Value)
	}
	res := act(t, a, ed, "typeset", map[string]string{"t_name": "书籍", "t_icon": icon.Value})
	if res.Toast != "已保存" {
		t.Fatalf("toast = %q", res.Toast)
	}
	if ty := mustType(t, st, sp, "book"); ty.Name != "书籍" || ty.Icon != "book" {
		t.Fatalf("saving the name changed the icon: %+v", ty)
	}
}

// TestStaleStageAndViewActionsAreRefused: a stage or view action names its item by index and name. When the item at
// the index no longer has that name, the action is refused with a toast and changes nothing, so that a stale fold
// never acts on another item.
func TestStaleStageAndViewActionsAreRefused(t *testing.T) {
	st := store.Open("")
	var sp string
	err := st.Update(func(b *kernel.Base, _ time.Time) error {
		s, err := templates.Apply(b, "blank", "书架", false, testNow)
		if err != nil {
			return err
		}
		sp = s.ID
		return s.PutType(kernel.Type{Key: "plain", Name: "清单",
			Flow:  []kernel.Stage{{Name: "甲", Phase: kernel.Todo}, {Name: "乙", Phase: kernel.Doing}, {Name: "丙", Phase: kernel.Done}},
			Views: []kernel.View{{Name: "看板", Kind: "board"}, {Name: "列表", Kind: "list"}},
		}, testNow)
	})
	if err != nil {
		t.Fatal(err)
	}
	a := newApp(st)
	ed := state{S: sp, D: "plain", F: "stage:0"}
	before := baseJSON(t, st)
	cases := []struct {
		name   string
		action string
		values map[string]string
	}{
		{"a stage renamed under its fold", "stagesave:0:乙", map[string]string{"stage_name": "丁", "stage_phase": "todo"}},
		{"a stage dropped under its fold", "stagedrop:0:乙", nil},
		{"a stage moved under its fold", "stageup:1:丙", nil},
		{"a stage action without a name", "stagedrop:2", nil},
		{"a view saved under its fold", "viewsave:0:列表", map[string]string{"v_name": "改", "v_kind": "list"}},
		{"a view dropped under its fold", "viewdrop:0:列表", nil},
		{"a view action without a name", "viewdrop:1", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantRefused(t, act(t, a, ed, tc.action, tc.values), ed)
		})
	}
	if baseJSON(t, st) != before {
		t.Fatal("a stale action changed the base")
	}

	// With the names the folds were drawn with, the same actions work.
	if res := act(t, a, state{S: sp, D: "plain", F: "stage:1"}, "stagesave:1:乙",
		map[string]string{"stage_name": "丁", "stage_phase": "doing"}); res.Toast != "已保存" {
		t.Fatalf("named stage save: toast %q", res.Toast)
	}
	if got := mustType(t, st, sp, "plain").Flow[1].Name; got != "丁" {
		t.Fatalf("stage 1 = %q, want 丁", got)
	}
	if res := act(t, a, state{S: sp, D: "plain"}, "viewdrop:1:列表", nil); res.Toast != "已删除视图" {
		t.Fatalf("named view drop: toast %q", res.Toast)
	}
	if views := mustType(t, st, sp, "plain").Views; len(views) != 1 || views[0].Name != "看板" {
		t.Fatalf("views = %+v", views)
	}
}
