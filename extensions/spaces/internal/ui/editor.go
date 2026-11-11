package ui

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// The schema editor is the page of one type's structure, pushed from 结构. Every change builds a whole kernel.Type from a
// copy of the type shown and saves it with PutType, the path the type_write tool takes: the kernel checks it and
// migrates the entities, and a refused change saves nothing and shows its sentence as a toast. The editor has no
// validation of its own; it reads what the form sent and nothing else.

// newSlot is the argument that adds an item to a list: a new stage, view or property kind, or a new type ("+" as D).
const newSlot = "+"

// phaseOrder lists the phases in the order the choices offer them.
var phaseOrder = []kernel.Phase{kernel.Todo, kernel.Doing, kernel.Done, kernel.Dropped}

// phaseTitles name the phases in the editor.
var phaseTitles = map[kernel.Phase]string{
	kernel.Todo: "待办", kernel.Doing: "进行中", kernel.Done: "已完成", kernel.Dropped: "已放弃",
}

// viewOrder lists the view kinds in the order the choices offer them.
var viewOrder = []string{"list", "board", "table", "progress", "feed"}

// builtinKeys are the built-in fields of every type, in the order the field choices offer them.
var builtinKeys = []string{"title", "status", "phase", "type", "origin", "created", "updated"}

// optEntry is one option input of a property: the fields that show it, and the setter for the value the form sent.
type optEntry struct {
	name  string
	field func(c *ctx, t *kernel.Type, p kernel.Prop) []wire.Component
	set   func(p *kernel.Prop, raw string)
}

// optInput is the table of the option inputs of a property, in the order they are shown. A kind shows the inputs of
// its Opts only, and the form sends them as "o_" and the name.
var optInput = []optEntry{
	{"unit", unitInput, setUnit},
	{"options", optionsInput, setChoices},
	{"target", targetInput, setTarget},
	{"many", manyInput, setMany},
	{"back", backInput, setBack},
	{"via", viaInput, setVia},
	{"op", opInput, setOp},
	{"of", ofInput, setOf},
}

// viaPath is one path a rollup may count along: an own relation of the type, or the backlinks of a relation that
// points at it. related is the type the path reaches.
type viaPath struct{ value, title, related string }

// editorPage is the schema editor of the type in c.st.D: a page of rows, collapsed by default, each row opening the
// fold of one part of the structure. D "+" is the form of a new type.
func (c *ctx) editorPage() (*wire.View, error) {
	if c.st.D == newSlot {
		return newTypePage(), nil
	}
	t, err := c.editedType()
	if err != nil {
		return nil, err
	}
	return &wire.View{Title: cut(t.Name, maxTitle), Sections: []wire.Section{
		{Items: c.typeHead(t)},
		c.flowSection(t),
		c.propsSection(t),
		c.cardSection(t),
		{Items: []wire.Component{c.typeDelete(t)}},
	}}, nil
}

// newTypePage is the form of a new type: its key and its name, and the button that creates it. Creating it turns the
// page into the editor of the new type.
func newTypePage() *wire.View {
	return &wire.View{Title: "新建类型", Sections: []wire.Section{{Items: []wire.Component{
		{Type: wire.ComponentHeader, Title: "新建类型", Subtitle: "类型有自己的属性、生命周期和视图"},
		textField("t_key", "标识", "", "小写字母开头，如 task"),
		textField("t_name", "名称", "", "例如 任务"),
		button("创建", &wire.Action{ID: "typenew", Submit: true}, wire.ButtonRoleNormal),
	}}}}
}

// typeHead is the first section: the header with the type's name and record count, and the row that folds the name
// and the icon.
func (c *ctx) typeHead(t *kernel.Type) []wire.Component {
	items := []wire.Component{
		{Type: wire.ComponentHeader, Title: cut(t.Name, maxTitle), Subtitle: fmt.Sprintf("结构 · %d 条记录", len(c.entitiesOf(t.Key)))},
		{Type: wire.ComponentRow, Title: "名称与图标", Subtitle: "标识 " + t.Key, Action: &wire.Action{ID: "fold:name"}},
	}
	if c.st.F == "name" {
		items = append(items,
			textField("t_name", "名称", t.Name, "1 到 40 个字"),
			optionalChoice("t_icon", "图标", iconChoices(t.Icon), t.Icon),
			button("保存", &wire.Action{ID: "typeset", Submit: true}, wire.ButtonRoleNormal),
		)
	}
	return items
}

// flowSection is the lifecycle: a row per stage that folds it, and the row that adds one. A type with no stage adds
// two at once, since a lifecycle has two stages at least.
func (c *ctx) flowSection(t *kernel.Type) wire.Section {
	items := []wire.Component{}
	for i, st := range t.Flow {
		id := strconv.Itoa(i)
		items = append(items, wire.Component{
			Type: wire.ComponentRow, Title: cut(st.Name, maxTitle),
			Badge:  &wire.Badge{Text: phaseTitles[st.Phase], Tone: phaseTone(st.Phase)},
			Action: &wire.Action{ID: "fold:stage:" + id},
		})
		if c.st.F == "stage:"+id {
			items = append(items, c.stageFields(t, i)...)
		}
	}
	items = append(items, wire.Component{Type: wire.ComponentRow, Title: "添加阶段", Action: &wire.Action{ID: "fold:stage:" + newSlot}})
	if c.st.F == "stage:"+newSlot {
		items = append(items, c.stageFields(t, -1)...)
	}
	return wire.Section{Title: "生命周期", Items: items}
}

// stageFields is the fold of a stage: its name, its phase, and the buttons that save, move up and delete it. For a new
// stage (i < 0) there is no move or delete.
func (c *ctx) stageFields(t *kernel.Type, i int) []wire.Component {
	if i < 0 {
		items := []wire.Component{textField("stage_name", "名称", "", "例如 探索"), phaseField("stage_phase", kernel.Todo)}
		if len(t.Flow) == 0 {
			items = append(items, textField("stage2_name", "第二阶段", "", "例如 完成"), phaseField("stage2_phase", kernel.Done))
		}
		return append(items, button("添加", &wire.Action{ID: "stagesave:" + newSlot, Submit: true}, wire.ButtonRoleNormal))
	}
	st := t.Flow[i]
	ref := nameRef(i, st.Name)
	items := []wire.Component{
		textField("stage_name", "名称", st.Name, ""),
		phaseField("stage_phase", st.Phase),
		button("保存", &wire.Action{ID: "stagesave:" + ref, Submit: true}, wire.ButtonRoleNormal),
	}
	if i > 0 {
		items = append(items, button("上移", &wire.Action{ID: "stageup:" + ref}, wire.ButtonRoleNormal))
	}
	return append(items, button("删除", &wire.Action{ID: "stagedrop:" + ref,
		Confirm: fmt.Sprintf("删除阶段「%s」？", cut(st.Name, maxTitle))}, wire.ButtonRoleDestructive))
}

// propsSection is the properties: a row per property that folds it, the row that adds one, and under it the eight
// kinds to choose from. The chosen kind's fold follows its row.
func (c *ctx) propsSection(t *kernel.Type) wire.Section {
	items := []wire.Component{}
	for i := range t.Props {
		p := t.Props[i]
		items = append(items, c.propRow(p))
		if c.st.F == "prop:"+p.Key {
			items = append(items, c.propFields(t, i)...)
		}
	}
	items = append(items, wire.Component{Type: wire.ComponentRow, Title: "添加属性", Action: &wire.Action{ID: "fold:addprop"}})
	if c.st.F == "addprop" || strings.HasPrefix(c.st.F, "new:") {
		items = append(items, c.kindRows(t)...)
	}
	return wire.Section{Title: "属性", Items: items}
}

// propRow is a property: its kind's icon, its name, its summary from the descriptor, and its key as the value.
func (c *ctx) propRow(p kernel.Prop) wire.Component {
	row := wire.Component{
		Type: wire.ComponentRow, Title: cut(p.Name, maxTitle), Subtitle: c.propSummary(p), Value: p.Key,
		Action: &wire.Action{ID: "fold:prop:" + p.Key},
	}
	if k, ok := kernel.KindOf(p.Kind); ok {
		row.Icon = iconName(k.Icon)
	}
	return row
}

// kindRows are the descriptor rows, one per kind: icon, label and hint. Choosing one opens the property fold for that
// kind, which follows the row.
func (c *ctx) kindRows(t *kernel.Type) []wire.Component {
	var items []wire.Component
	for _, k := range kernel.Kinds {
		items = append(items, wire.Component{
			Type: wire.ComponentRow, Title: k.Label, Subtitle: k.Hint, Icon: iconName(k.Icon),
			Action: &wire.Action{ID: "fold:new:" + k.Name},
		})
		if c.st.F == "new:"+k.Name {
			items = append(items, c.newPropFields(t, k)...)
		}
	}
	return items
}

// propFields is the fold of a property: its name, the inputs of its kind's options, and the buttons that save, move up
// and delete it.
func (c *ctx) propFields(t *kernel.Type, i int) []wire.Component {
	p := t.Props[i]
	items := []wire.Component{textField("p_name", "名称", p.Name, "")}
	items = append(items, c.optFields(t, p)...)
	items = append(items, button("保存", &wire.Action{ID: "propsave:" + p.Key, Submit: true}, wire.ButtonRoleNormal))
	if i > 0 {
		items = append(items, button("上移", &wire.Action{ID: "propup:" + p.Key}, wire.ButtonRoleNormal))
	}
	return append(items, button("删除", &wire.Action{ID: "propdrop:" + p.Key,
		Confirm: fmt.Sprintf("删除属性「%s」？它在每条记录上的值会一起删除。", cut(p.Name, maxTitle))}, wire.ButtonRoleDestructive))
}

// newPropFields is the fold that creates a property of a kind: its key, its name, the inputs of its options, and the
// button that adds it.
func (c *ctx) newPropFields(t *kernel.Type, k *kernel.Kind) []wire.Component {
	p := kernel.Prop{Kind: k.Name}
	items := []wire.Component{textField("p_key", "标识", "", "小写字母开头，如 due"), textField("p_name", "名称", "", "")}
	items = append(items, c.optFields(t, p)...)
	return append(items, button("添加", &wire.Action{ID: "propnew:" + k.Name, Submit: true}, wire.ButtonRoleNormal))
}

// optFields are the inputs of the options a kind reads, in the order of optInput.
func (c *ctx) optFields(t *kernel.Type, p kernel.Prop) []wire.Component {
	k, ok := kernel.KindOf(p.Kind)
	if !ok {
		return nil
	}
	var out []wire.Component
	for _, o := range optInput {
		if slices.Contains(k.Opts, o.name) {
			out = append(out, o.field(c, t, p)...)
		}
	}
	return out
}

// cardSection is the card and the views: the card's row that folds it, a row per view, and the row that adds one.
func (c *ctx) cardSection(t *kernel.Type) wire.Section {
	items := []wire.Component{{Type: wire.ComponentRow, Title: "卡片", Subtitle: c.cardSummary(t), Action: &wire.Action{ID: "fold:card"}}}
	if c.st.F == "card" {
		items = append(items, c.cardFields(t)...)
	}
	for i, v := range t.Views {
		id := strconv.Itoa(i)
		items = append(items, wire.Component{
			Type: wire.ComponentRow, Title: cut(v.Name, maxTitle), Subtitle: c.viewSummary(t, v),
			Action: &wire.Action{ID: "fold:view:" + id},
		})
		if c.st.F == "view:"+id {
			items = append(items, c.viewFields(t, i, v)...)
		}
	}
	items = append(items, wire.Component{Type: wire.ComponentRow, Title: "添加视图", Action: &wire.Action{ID: "fold:view:" + newSlot}})
	if c.st.F == "view:"+newSlot {
		items = append(items, c.viewFields(t, -1, kernel.View{})...)
	}
	return wire.Section{Title: "卡片与视图", Items: items}
}

// cardFields is the fold of the card: the lead, the pills as names, and the progress as a ratio of two fields.
func (c *ctx) cardFields(t *kernel.Type) []wire.Component {
	of, over := "", ""
	if t.Card.Progress != nil {
		of, over = t.Card.Progress.Of, t.Card.Progress.Over
	}
	choices := c.fieldChoices(t)
	return []wire.Component{
		optionalChoice("c_lead", "主行", choices, t.Card.Lead),
		textField("c_pills", "标签", c.pillNames(t, t.Card.Pills), "用、分隔的字段名"),
		optionalChoice("c_of", "进度", choices, of),
		optionalChoice("c_over", "进度分母", choices, over),
		button("保存", &wire.Action{ID: "cardsave", Submit: true}, wire.ButtonRoleNormal),
	}
}

// viewFields is the fold of a view (i < 0 for a new one): its name, kind, group, sort and descending, the caption of
// the filters and aggregates that are changed through the tool, and the buttons that save and delete it.
func (c *ctx) viewFields(t *kernel.Type, i int, v kernel.View) []wire.Component {
	sortBy, desc := "", false
	if len(v.Sort) > 0 {
		sortBy, desc = v.Sort[0].By, v.Sort[0].Desc
	}
	choices := c.fieldChoices(t)
	items := []wire.Component{
		textField("v_name", "名称", v.Name, ""),
		requiredChoice("v_kind", "类型", viewKindChoices(), textOr(v.Kind, viewOrder[0])),
		optionalChoice("v_group", "分组", choices, v.Group),
		optionalChoice("v_sort", "排序", choices, sortBy),
		toggleField("v_desc", "倒序", desc),
	}
	if len(v.Where) > 0 || len(v.Agg) > 0 {
		items = append(items, caption(fmt.Sprintf("还有 %d 条筛选、%d 项汇总，在助手中修改", len(v.Where), len(v.Agg))))
	}
	slot := newSlot
	if i >= 0 {
		slot = nameRef(i, v.Name)
	}
	items = append(items, button("保存", &wire.Action{ID: "viewsave:" + slot, Submit: true}, wire.ButtonRoleNormal))
	if i >= 0 {
		items = append(items, button("删除视图", &wire.Action{ID: "viewdrop:" + slot,
			Confirm: fmt.Sprintf("删除视图「%s」？", cut(v.Name, maxTitle))}, wire.ButtonRoleDestructive))
	}
	return items
}

// typeDelete is the button that deletes the type with its records; the confirmation says how many.
func (c *ctx) typeDelete(t *kernel.Type) wire.Component {
	return button("删除类型", &wire.Action{ID: "typedrop", Confirm: fmt.Sprintf("删除类型「%s」？会删除 %d 条记录。",
		cut(t.Name, maxTitle), len(c.entitiesOf(t.Key)))}, wire.ButtonRoleDestructive)
}

// cardSummary is the card in words: its lead, its pills and its progress; "按默认" when none is set.
func (c *ctx) cardSummary(t *kernel.Type) string {
	var parts []string
	if t.Card.Lead != "" {
		parts = append(parts, "主行 "+c.fieldName(t, t.Card.Lead))
	}
	if len(t.Card.Pills) > 0 {
		parts = append(parts, "标签 "+c.pillNames(t, t.Card.Pills))
	}
	if pr := t.Card.Progress; pr != nil {
		s := "进度 " + c.fieldName(t, pr.Of)
		if pr.Over != "" {
			s += " / " + c.fieldName(t, pr.Over)
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return "按默认"
	}
	return strings.Join(parts, " · ")
}

// viewSummary is a view's kind and its group, as in 看板 · 按 状态 分组.
func (c *ctx) viewSummary(t *kernel.Type, v kernel.View) string {
	s := textOr(viewLabels[v.Kind], v.Kind)
	if v.Group != "" {
		s += " · 按 " + c.fieldName(t, v.Group) + " 分组"
	}
	return s
}

// propSummary is a property's kind in words: a relation's target, a rollup's aggregate and path, or a number's unit.
func (c *ctx) propSummary(p kernel.Prop) string {
	k, ok := kernel.KindOf(p.Kind)
	if !ok {
		return p.Kind
	}
	switch {
	case k.Input == kernel.InPick:
		s := k.Label + " → " + c.typeName(p.Target)
		if p.Many {
			s += " · 多个"
		}
		return s
	case k.Eval != nil:
		s := k.Label + " · " + aggLabel(p.Op)
		if p.Via != "" {
			s += " · " + p.Via
		}
		return s
	case p.Unit != "":
		return k.Label + " · " + p.Unit
	}
	return k.Label
}

// fieldChoices are the fields of the type as choices: the built-in fields, then the declared properties.
func (c *ctx) fieldChoices(t *kernel.Type) []wire.Choice {
	var out []wire.Choice
	for _, p := range c.fieldsOf(t) {
		out = append(out, wire.Choice{Value: p.Key, Title: p.Name})
	}
	return out
}

// fieldsOf are the fields of the type: the built-in fields, then the declared properties.
func (c *ctx) fieldsOf(t *kernel.Type) []kernel.Prop {
	var out []kernel.Prop
	for _, key := range builtinKeys {
		if p, ok := c.sp.Field(t, key); ok {
			out = append(out, p)
		}
	}
	return append(out, t.Props...)
}

// fieldName is the name of a field of the type, or the key when there is none.
func (c *ctx) fieldName(t *kernel.Type, key string) string {
	if p, ok := c.sp.Field(t, key); ok {
		return p.Name
	}
	return key
}

// pillNames are the names of the pills' fields, separated by 、.
func (c *ctx) pillNames(t *kernel.Type, keys []string) string {
	names := make([]string, 0, len(keys))
	for _, key := range keys {
		names = append(names, c.fieldName(t, key))
	}
	return strings.Join(names, "、")
}

// pillKeys are the keys of the fields named by a text separated by 、; a name that is no field of the type is refused.
func (c *ctx) pillKeys(t *kernel.Type, raw string) ([]string, error) {
	var out []string
	for _, name := range splitList(raw) {
		key := ""
		for _, p := range c.fieldsOf(t) {
			if p.Name == name {
				key = p.Key
				break
			}
		}
		if key == "" {
			return nil, fmt.Errorf("没有字段「%s」", name)
		}
		out = append(out, key)
	}
	return out, nil
}

// typeChoices are the types of the space as choices.
func (c *ctx) typeChoices() []wire.Choice {
	var out []wire.Choice
	for _, t := range c.sp.Types {
		if t != nil {
			out = append(out, wire.Choice{Value: t.Key, Title: t.Name})
		}
	}
	return out
}

// typeName is the name of the type of the space with the key, or the key when there is none.
func (c *ctx) typeName(key string) string {
	if t := typeIn(c.sp, key); t != nil {
		return t.Name
	}
	return key
}

// viaPaths are the paths a rollup of the type may count along: each own relation, and each relation that points at the
// type (its backlinks, written type.prop).
func (c *ctx) viaPaths(t *kernel.Type) []viaPath {
	var out []viaPath
	for _, p := range t.Props {
		if k, ok := kernel.KindOf(p.Kind); ok && k.Input == kernel.InPick {
			out = append(out, viaPath{value: p.Key, title: p.Name, related: p.Target})
		}
	}
	for _, st := range c.sp.Types {
		if st == nil {
			continue
		}
		for _, p := range st.Props {
			if k, ok := kernel.KindOf(p.Kind); ok && k.Input == kernel.InPick && p.Target == t.Key {
				out = append(out, viaPath{value: st.Key + "." + p.Key, title: st.Name + " · " + p.Name, related: st.Key})
			}
		}
	}
	return out
}

// numericChoices are the stored numeric properties of the types the paths of the type reach, each key once.
func (c *ctx) numericChoices(t *kernel.Type) []wire.Choice {
	var out []wire.Choice
	seen := map[string]bool{}
	for _, pa := range c.viaPaths(t) {
		rt := typeIn(c.sp, pa.related)
		if rt == nil {
			continue
		}
		for _, p := range rt.Props {
			if k, ok := kernel.KindOf(p.Kind); ok && k.Num != nil && k.Eval == nil && !seen[p.Key] {
				seen[p.Key] = true
				out = append(out, wire.Choice{Value: p.Key, Title: p.Name})
			}
		}
	}
	return out
}

// aggChoices are the aggregates as choices, in vocabulary order.
func aggChoices() []wire.Choice {
	out := make([]wire.Choice, 0, len(kernel.Aggregates))
	for _, a := range kernel.Aggregates {
		out = append(out, wire.Choice{Value: a.Name, Title: a.Label})
	}
	return out
}

// aggLabel is the label of an aggregate, or its name when it has none.
func aggLabel(name string) string {
	for _, a := range kernel.Aggregates {
		if a.Name == name {
			return a.Label
		}
	}
	return name
}

// viewKindChoices are the view kinds as choices.
func viewKindChoices() []wire.Choice {
	out := make([]wire.Choice, 0, len(viewOrder))
	for _, k := range viewOrder {
		out = append(out, wire.Choice{Value: k, Title: viewLabels[k]})
	}
	return out
}

// iconChoices are the icons the apps draw, as choices, and the current icon of a type when the apps do not draw it: the
// icon is an opaque name in the base, and a save of the name must keep it.
func iconChoices(current string) []wire.Choice {
	out := make([]wire.Choice, 0, len(wire.NavIcons)+1)
	for _, name := range wire.NavIcons {
		out = append(out, wire.Choice{Value: name, Title: name})
	}
	if current != "" && !slices.Contains(wire.NavIcons, current) {
		out = append(out, wire.Choice{Value: current, Title: current})
	}
	return out
}

// phaseChoices are the four phases as choices.
func phaseChoices() []wire.Choice {
	out := make([]wire.Choice, 0, len(phaseOrder))
	for _, p := range phaseOrder {
		out = append(out, wire.Choice{Value: string(p), Title: phaseTitles[p]})
	}
	return out
}

// phaseTone tones a phase: a doing stage is a caution, a done one positive, the rest neutral.
func phaseTone(p kernel.Phase) string {
	switch p {
	case kernel.Doing:
		return wire.ToneCaution
	case kernel.Done:
		return wire.TonePositive
	}
	return wire.ToneNeutral
}

// Inputs. A field is named as the form sends it; an optional choice sends "=" and its value, "=" alone is unset.

func textField(name, label, value, placeholder string) wire.Component {
	return wire.Component{Type: wire.ComponentField, Name: name, Label: label, Kind: wire.FieldKindText, Value: value, Placeholder: placeholder}
}

func toggleField(name, label string, on bool) wire.Component {
	return wire.Component{Type: wire.ComponentField, Name: name, Label: label, Kind: wire.FieldKindToggle, Value: strconv.FormatBool(on)}
}

// requiredChoice is a choice that always has a value; current is held when it is one of the options, else the first.
// No option at all is a caption: a choice field needs choices.
func requiredChoice(name, label string, options []wire.Choice, current string) wire.Component {
	if len(options) == 0 {
		return caption(label + "没有可以选的项")
	}
	value := options[0].Value
	if slices.ContainsFunc(options, func(ch wire.Choice) bool { return ch.Value == current }) {
		value = current
	}
	return wire.Component{Type: wire.ComponentField, Name: name, Label: label, Kind: wire.FieldKindChoice, Value: value, Choices: options}
}

// optionalChoice is a choice that may be unset: its first choice is 未设置, sent as "=", and each option is sent as
// "=" and its value.
func optionalChoice(name, label string, options []wire.Choice, current string) wire.Component {
	choices := []wire.Choice{{Value: choicePrefix, Title: unsetLabel}}
	value := choicePrefix
	for _, ch := range options {
		choices = append(choices, wire.Choice{Value: choicePrefix + ch.Value, Title: textOr(ch.Title, ch.Value)})
		if current != "" && ch.Value == current {
			value = choicePrefix + current
		}
	}
	return wire.Component{Type: wire.ComponentField, Name: name, Label: label, Kind: wire.FieldKindChoice, Value: value, Choices: choices}
}

// phaseField is the choice of a stage's phase.
func phaseField(name string, p kernel.Phase) wire.Component {
	return requiredChoice(name, "阶段", phaseChoices(), string(p))
}

// caption is a small text that says something about the fold it is in.
func caption(text string) wire.Component {
	return wire.Component{Type: wire.ComponentText, Text: text, Style: wire.TextStyleCaption}
}

// The option inputs of optInput. Each one shows its field and reads its value back.

func unitInput(_ *ctx, _ *kernel.Type, p kernel.Prop) []wire.Component {
	return []wire.Component{textField("o_unit", "单位", p.Unit, "例如 页")}
}

func setUnit(p *kernel.Prop, raw string) { p.Unit = strings.TrimSpace(raw) }

// optionsInput is the options as one text separated by 、. Options that would not fit a text of a screen are not
// shown; their field is then not sent, so they are kept as they are.
func optionsInput(_ *ctx, _ *kernel.Type, p kernel.Prop) []wire.Component {
	text := strings.Join(p.Options, "、")
	if utf8.RuneCountInString(text) > maxText {
		return []wire.Component{caption("选项太多，不能在这里编辑，请在助手中修改")}
	}
	return []wire.Component{textField("o_options", "选项", text, "用、分隔，如 甲、乙")}
}

func setChoices(p *kernel.Prop, raw string) { p.Options = splitList(raw) }

func targetInput(c *ctx, _ *kernel.Type, p kernel.Prop) []wire.Component {
	return []wire.Component{requiredChoice("o_target", "指向", c.typeChoices(), p.Target)}
}

func setTarget(p *kernel.Prop, raw string) { p.Target = raw }

func manyInput(_ *ctx, _ *kernel.Type, p kernel.Prop) []wire.Component {
	return []wire.Component{toggleField("o_many", "多个", p.Many)}
}

func setMany(p *kernel.Prop, raw string) { p.Many = raw == "true" }

func backInput(_ *ctx, _ *kernel.Type, p kernel.Prop) []wire.Component {
	return []wire.Component{textField("o_back", "反向名称", p.Back, "例如 任务")}
}

func setBack(p *kernel.Prop, raw string) { p.Back = strings.TrimSpace(raw) }

func viaInput(c *ctx, t *kernel.Type, p kernel.Prop) []wire.Component {
	paths := c.viaPaths(t)
	if len(paths) == 0 {
		return []wire.Component{caption("没有可以汇总的关联，先给某个类型添加关联属性")}
	}
	choices := make([]wire.Choice, 0, len(paths))
	for _, pa := range paths {
		choices = append(choices, wire.Choice{Value: pa.value, Title: pa.title})
	}
	return []wire.Component{requiredChoice("o_via", "经由", choices, p.Via)}
}

func setVia(p *kernel.Prop, raw string) { p.Via = raw }

func opInput(_ *ctx, _ *kernel.Type, p kernel.Prop) []wire.Component {
	return []wire.Component{requiredChoice("o_op", "汇总方式", aggChoices(), p.Op)}
}

func setOp(p *kernel.Prop, raw string) { p.Op = raw }

func ofInput(c *ctx, t *kernel.Type, p kernel.Prop) []wire.Component {
	return []wire.Component{optionalChoice("o_of", "汇总字段", c.numericChoices(t), p.Of)}
}

func setOf(p *kernel.Prop, raw string) { p.Of = strings.TrimPrefix(raw, choicePrefix) }

// splitList splits a text the form sent, separated by 、, dropping the blanks.
func splitList(raw string) []string {
	var out []string
	for _, s := range strings.Split(raw, "、") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// The actions of the editor. Each saves one whole type, built from a copy of the type shown.

// editedType is the type the editor shows.
func (c *ctx) editedType() (*kernel.Type, error) {
	if t := c.typeOf(c.st.D); t != nil {
		return t, nil
	}
	return nil, errors.New("没有这个类型")
}

// putType saves a whole type: PutType checks it and migrates its records, and a refused change leaves the space as it was.
func (c *ctx) putType(nt kernel.Type) error {
	return c.sp.PutType(nt, c.now)
}

// sentOr is the value the form sent under name, or old when the form did not send it.
func (c *ctx) sentOr(name, old string) string {
	if v, ok := c.r.Values[name]; ok {
		return v
	}
	return old
}

// choiceOr is the value of an optional choice the form sent under name ("=" alone is unset), or old when not sent.
func (c *ctx) choiceOr(name, old string) string {
	if v, ok := c.r.Values[name]; ok {
		return strings.TrimPrefix(v, choicePrefix)
	}
	return old
}

// cloneType copies a type so that the copy can change without touching the type it came from.
func cloneType(t *kernel.Type) kernel.Type {
	nt := *t
	nt.Flow = slices.Clone(t.Flow)
	nt.Props = slices.Clone(t.Props)
	nt.Views = slices.Clone(t.Views)
	nt.Card.Pills = slices.Clone(t.Card.Pills)
	if t.Card.Progress != nil {
		pr := *t.Card.Progress
		nt.Card.Progress = &pr
	}
	return nt
}

// nameRef is the argument of an action that names an existing stage or view: its index and its name, "<index>:<name>".
// The index alone would name another item once the list has changed under a fold that is still open.
func nameRef(i int, name string) string {
	return strconv.Itoa(i) + ":" + name
}

// slotOf is the item an action names among the names of the list; newSlot is -1, a new item.
func slotOf(arg string, names []string, what string) (int, error) {
	if arg == newSlot {
		return -1, nil
	}
	return nameAt(arg, names, what)
}

// nameAt is the index an action names among the names of a list, as nameRef writes it. The item at that index must
// still have the name the action carries; otherwise the action is refused, so that a fold drawn before a change never
// changes another item. Anything else is refused too.
func nameAt(arg string, names []string, what string) (int, error) {
	idx, name, _ := strings.Cut(arg, ":")
	i, err := strconv.Atoi(idx)
	if err != nil || i < 0 || i >= len(names) {
		return 0, errors.New("没有这个" + what)
	}
	if names[i] != name {
		return 0, errors.New("这个" + what + "已经变了，请重新打开再试")
	}
	return i, nil
}

// stageNames are the names of the stages of a type, in flow order.
func stageNames(t *kernel.Type) []string {
	out := make([]string, 0, len(t.Flow))
	for _, s := range t.Flow {
		out = append(out, s.Name)
	}
	return out
}

// viewNames are the names of the views of a type, in order.
func viewNames(t *kernel.Type) []string {
	out := make([]string, 0, len(t.Views))
	for _, v := range t.Views {
		out = append(out, v.Name)
	}
	return out
}

// propAt is the index of the property with the key, or -1.
func propAt(t *kernel.Type, key string) int {
	return slices.IndexFunc(t.Props, func(p kernel.Prop) bool { return p.Key == key })
}

// applyProp is p with the name and the options the form sent. An option the form did not send is kept, and an option
// the kind does not read is never set.
func (c *ctx) applyProp(p kernel.Prop, k *kernel.Kind) kernel.Prop {
	p.Name = strings.TrimSpace(c.sentOr("p_name", p.Name))
	for _, o := range optInput {
		if raw, sent := c.r.Values["o_"+o.name]; sent && slices.Contains(k.Opts, o.name) {
			o.set(&p, raw)
		}
	}
	return p
}

// stageFrom is the stage the form sent under prefix, with old as the value of what it did not send.
func (c *ctx) stageFrom(prefix string, old kernel.Stage) kernel.Stage {
	return kernel.Stage{
		Name:  strings.TrimSpace(c.sentOr(prefix+"_name", old.Name)),
		Phase: kernel.Phase(c.sentOr(prefix+"_phase", string(old.Phase))),
	}
}

// actTypeNew creates a type from the form of a new type, and turns the page into its editor. A key that is taken is
// refused: PutType would replace that type instead.
func actTypeNew(c *ctx, _ string) (*wire.ActionResult, error) {
	if c.sp == nil {
		return nil, errors.New("还没有空间")
	}
	nt := kernel.Type{Key: strings.TrimSpace(c.sentOr("t_key", "")), Name: strings.TrimSpace(c.sentOr("t_name", ""))}
	if typeIn(c.sp, nt.Key) != nil {
		return nil, fmt.Errorf("已经有类型「%s」", nt.Key)
	}
	if err := c.putType(nt); err != nil {
		return nil, err
	}
	c.st = state{S: c.sp.ID, D: nt.Key}
	return &wire.ActionResult{Toast: "已新建「" + nt.Name + "」"}, nil
}

// actTypeSet changes the name and the icon of the type shown.
func actTypeSet(c *ctx, _ string) (*wire.ActionResult, error) {
	t, err := c.editedType()
	if err != nil {
		return nil, err
	}
	nt := cloneType(t)
	nt.Name = strings.TrimSpace(c.sentOr("t_name", t.Name))
	nt.Icon = c.choiceOr("t_icon", t.Icon)
	if err := c.putType(nt); err != nil {
		return nil, err
	}
	c.st.F = ""
	return &wire.ActionResult{Toast: "已保存"}, nil
}

// actTypeDrop deletes the type shown with its records, and leaves its editor.
func actTypeDrop(c *ctx, _ string) (*wire.ActionResult, error) {
	t, err := c.editedType()
	if err != nil {
		return nil, err
	}
	name := cut(t.Name, maxTitle)
	if err := c.sp.DropType(t.Key); err != nil {
		return nil, err
	}
	c.st = state{S: c.sp.ID}
	return &wire.ActionResult{Toast: "已删除「" + name + "」", Pop: true}, nil
}

// actStageSave changes a stage, or adds one at the end (newSlot). A type with no stage gets two at once: a lifecycle
// needs two stages, and the kernel refuses one.
func actStageSave(c *ctx, arg string) (*wire.ActionResult, error) {
	t, err := c.editedType()
	if err != nil {
		return nil, err
	}
	i, err := slotOf(arg, stageNames(t), "阶段")
	if err != nil {
		return nil, err
	}
	nt := cloneType(t)
	if i < 0 {
		nt.Flow = append(nt.Flow, c.stageFrom("stage", kernel.Stage{Phase: kernel.Todo}))
		if len(t.Flow) == 0 {
			nt.Flow = append(nt.Flow, c.stageFrom("stage2", kernel.Stage{Phase: kernel.Done}))
		}
	} else {
		nt.Flow[i] = c.stageFrom("stage", t.Flow[i])
	}
	if err := c.putType(nt); err != nil {
		return nil, err
	}
	c.st.F = ""
	return &wire.ActionResult{Toast: "已保存"}, nil
}

// actStageUp moves a stage one place earlier; its fold stays open.
func actStageUp(c *ctx, arg string) (*wire.ActionResult, error) {
	t, err := c.editedType()
	if err != nil {
		return nil, err
	}
	i, err := nameAt(arg, stageNames(t), "阶段")
	if err != nil {
		return nil, err
	}
	if i == 0 {
		return nil, errors.New("已经在最前面")
	}
	nt := cloneType(t)
	nt.Flow[i-1], nt.Flow[i] = nt.Flow[i], nt.Flow[i-1]
	if err := c.putType(nt); err != nil {
		return nil, err
	}
	c.st.F = "stage:" + strconv.Itoa(i-1)
	return nil, nil
}

// actStageDrop deletes a stage. The kernel refuses it while a record is in it.
func actStageDrop(c *ctx, arg string) (*wire.ActionResult, error) {
	t, err := c.editedType()
	if err != nil {
		return nil, err
	}
	i, err := nameAt(arg, stageNames(t), "阶段")
	if err != nil {
		return nil, err
	}
	nt := cloneType(t)
	nt.Flow = slices.Delete(nt.Flow, i, i+1)
	if len(nt.Flow) == 0 {
		nt.Flow = nil
	}
	if err := c.putType(nt); err != nil {
		return nil, err
	}
	c.st.F = ""
	return &wire.ActionResult{Toast: "已删除阶段"}, nil
}

// actPropNew adds a property of the kind named by the argument. A key the type already has is refused: PutType would
// replace that property.
func actPropNew(c *ctx, arg string) (*wire.ActionResult, error) {
	t, err := c.editedType()
	if err != nil {
		return nil, err
	}
	k, ok := kernel.KindOf(arg)
	if !ok {
		return nil, fmt.Errorf("没有属性类型「%s」", arg)
	}
	key := strings.TrimSpace(c.sentOr("p_key", ""))
	if propAt(t, key) >= 0 {
		return nil, fmt.Errorf("已经有属性「%s」", key)
	}
	nt := cloneType(t)
	nt.Props = append(nt.Props, c.applyProp(kernel.Prop{Key: key, Kind: k.Name}, k))
	if err := c.putType(nt); err != nil {
		return nil, err
	}
	c.st.F = ""
	return &wire.ActionResult{Toast: "已添加「" + cut(c.sentOr("p_name", key), maxTitle) + "」"}, nil
}

// actPropSave changes the name and the options of the property with the key. The key is the property's identity.
func actPropSave(c *ctx, key string) (*wire.ActionResult, error) {
	t, err := c.editedType()
	if err != nil {
		return nil, err
	}
	i := propAt(t, key)
	if i < 0 {
		return nil, errors.New("没有这个属性")
	}
	k, ok := kernel.KindOf(t.Props[i].Kind)
	if !ok {
		return nil, errors.New("没有这个属性类型")
	}
	nt := cloneType(t)
	nt.Props[i] = c.applyProp(t.Props[i], k)
	if err := c.putType(nt); err != nil {
		return nil, err
	}
	c.st.F = ""
	return &wire.ActionResult{Toast: "已保存"}, nil
}

// actPropUp moves a property one place earlier in the display order.
func actPropUp(c *ctx, key string) (*wire.ActionResult, error) {
	t, err := c.editedType()
	if err != nil {
		return nil, err
	}
	i := propAt(t, key)
	if i < 0 {
		return nil, errors.New("没有这个属性")
	}
	if i == 0 {
		return nil, errors.New("已经在最前面")
	}
	nt := cloneType(t)
	nt.Props[i-1], nt.Props[i] = nt.Props[i], nt.Props[i-1]
	return nil, c.putType(nt)
}

// actPropDrop deletes the property with the key and its values. The kernel refuses it while a card, a view or a rollup
// still names it.
func actPropDrop(c *ctx, key string) (*wire.ActionResult, error) {
	t, err := c.editedType()
	if err != nil {
		return nil, err
	}
	i := propAt(t, key)
	if i < 0 {
		return nil, errors.New("没有这个属性")
	}
	nt := cloneType(t)
	nt.Props = slices.Delete(nt.Props, i, i+1)
	if len(nt.Props) == 0 {
		nt.Props = nil
	}
	if err := c.putType(nt); err != nil {
		return nil, err
	}
	c.st.F = ""
	return &wire.ActionResult{Toast: "已删除属性"}, nil
}

// actCardSave changes the lead, the pills and the progress of the card. A pill names a field of the type.
func actCardSave(c *ctx, _ string) (*wire.ActionResult, error) {
	t, err := c.editedType()
	if err != nil {
		return nil, err
	}
	nt := cloneType(t)
	nt.Card.Lead = c.choiceOr("c_lead", t.Card.Lead)
	if raw, ok := c.r.Values["c_pills"]; ok {
		pills, err := c.pillKeys(t, raw)
		if err != nil {
			return nil, err
		}
		nt.Card.Pills = pills
	}
	of, over := "", ""
	if t.Card.Progress != nil {
		of, over = t.Card.Progress.Of, t.Card.Progress.Over
	}
	of, over = c.choiceOr("c_of", of), c.choiceOr("c_over", over)
	nt.Card.Progress = nil
	if of != "" || over != "" {
		nt.Card.Progress = &kernel.Ratio{Of: of, Over: over}
	}
	if err := c.putType(nt); err != nil {
		return nil, err
	}
	c.st.F = ""
	return &wire.ActionResult{Toast: "已保存"}, nil
}

// actViewSave changes a view, or adds one at the end (newSlot). Its filters and aggregates are kept as they are; the
// sort is its first key, and the keys after it are kept too.
func actViewSave(c *ctx, arg string) (*wire.ActionResult, error) {
	t, err := c.editedType()
	if err != nil {
		return nil, err
	}
	i, err := slotOf(arg, viewNames(t), "视图")
	if err != nil {
		return nil, err
	}
	var old kernel.View
	if i >= 0 {
		old = t.Views[i]
	}
	sortBy, desc := "", false
	if len(old.Sort) > 0 {
		sortBy, desc = old.Sort[0].By, old.Sort[0].Desc
	}
	if v, ok := c.r.Values["v_desc"]; ok {
		desc = v == "true"
	}
	nv := old
	nv.Name = strings.TrimSpace(c.sentOr("v_name", old.Name))
	nv.Kind = c.sentOr("v_kind", old.Kind)
	nv.Group = c.choiceOr("v_group", old.Group)
	nv.Sort = sortWith(old.Sort, c.choiceOr("v_sort", sortBy), desc)
	nt := cloneType(t)
	if i < 0 {
		nt.Views = append(nt.Views, nv)
	} else {
		nt.Views[i] = nv
	}
	if err := c.putType(nt); err != nil {
		return nil, err
	}
	c.st.F = ""
	return &wire.ActionResult{Toast: "已保存"}, nil
}

// sortWith is the sort of a view with its first key replaced by by (none when by is empty); the keys after the first
// are kept.
func sortWith(old []kernel.Order, by string, desc bool) []kernel.Order {
	var rest []kernel.Order
	if len(old) > 1 {
		rest = old[1:]
	}
	if by == "" {
		return slices.Clone(rest)
	}
	return append([]kernel.Order{{By: by, Desc: desc}}, rest...)
}

// actViewDrop deletes a view of the type.
func actViewDrop(c *ctx, arg string) (*wire.ActionResult, error) {
	t, err := c.editedType()
	if err != nil {
		return nil, err
	}
	i, err := nameAt(arg, viewNames(t), "视图")
	if err != nil {
		return nil, err
	}
	nt := cloneType(t)
	nt.Views = slices.Delete(nt.Views, i, i+1)
	if len(nt.Views) == 0 {
		nt.Views = nil
	}
	if err := c.putType(nt); err != nil {
		return nil, err
	}
	c.st.F = ""
	return &wire.ActionResult{Toast: "已删除视图"}, nil
}
