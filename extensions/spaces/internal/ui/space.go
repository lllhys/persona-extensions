package ui

import (
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/spaces/internal/templates"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// overTab is the value of the overview tab, which no type key can take.
const overTab = "/"

// cardRows is the most entities in progress the overview card lists.
const cardRows = 5

// welcome is the screen with no space: a header and a row per template. Samples come with every template; the blank
// one has none.
func (c *ctx) welcome() *wire.View {
	items := []wire.Component{{
		Type: wire.ComponentHeader, Title: "空间", Subtitle: "用类型、属性和关联，管理任何一类事情",
	}}
	for _, t := range templates.All() {
		value := fmt.Sprintf("%d 类", len(t.Types))
		if len(t.Samples) > 0 {
			value += " · 含示例"
		}
		items = append(items, wire.Component{
			Type: wire.ComponentRow, Icon: iconName(t.Icon), Title: t.Name, Subtitle: cut(t.About, maxText),
			Value: value, Action: &wire.Action{ID: "create:" + t.Key},
		})
	}
	return &wire.View{Sections: []wire.Section{{Items: items}}}
}

// overview is the space page with no type tab: the space's header and its totals, a row per type, the latest changes,
// the settings fold when it is open, and the buttons that add a type or open the settings.
func (c *ctx) overview() (*wire.View, error) {
	sp := c.sp
	filter, tabs := c.chrome()
	v := &wire.View{Sections: []wire.Section{}, Filter: filter, Tabs: tabs}
	head := wire.Section{Items: []wire.Component{
		c.spaceHeader(),
		statGrid(spaceStats(tallyOf(c.w, sp.Entities))),
	}}
	v.Sections = append(v.Sections, head)
	if len(sp.Types) == 0 {
		v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{c.noTypes()}})
	} else {
		v.Sections = append(v.Sections, wire.Section{Title: "类型", Items: c.typeRows()})
		recent, err := c.recent()
		if err != nil {
			return nil, err
		}
		if len(recent) > 0 {
			v.Sections = append(v.Sections, wire.Section{Title: "最近更新", Items: c.rows(recent, true)})
		}
	}
	if c.st.F == "space" {
		v.Sections = append(v.Sections, c.spaceFold())
	}
	v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{
		button("新建类型", ext.Open("schema:+", "", state{S: sp.ID, D: "+"}, "新建类型"), wire.ButtonRoleNormal),
		button("空间设置", &wire.Action{ID: "fold:space"}, wire.ButtonRoleNormal),
	}})
	return v, nil
}

// chrome is the space's filter (only with two or more spaces) and its tabs: the overview and a tab per type, each
// with its count. A space without types has no tabs.
func (c *ctx) chrome() (*wire.Filter, *wire.Tabs) {
	var filter *wire.Filter
	if len(c.b.Spaces) >= 2 {
		choices := []wire.Choice{}
		for _, sp := range c.b.Spaces {
			choices = append(choices, wire.Choice{Value: sp.ID, Title: sp.Name})
		}
		filter = &wire.Filter{Name: "space", Title: "空间", Choices: choices, Value: c.sp.ID, Action: wire.Action{ID: "space"}}
	}
	if len(c.sp.Types) == 0 {
		return filter, nil
	}
	items := []wire.Tab{{Value: overTab, Title: "概览"}}
	for _, t := range c.sp.Types {
		items = append(items, wire.Tab{Value: t.Key, Title: t.Name, Badge: strconv.Itoa(len(c.entitiesOf(t.Key)))})
	}
	cur := overTab
	if c.typeOf(c.st.T) != nil {
		cur = c.st.T
	}
	return filter, &wire.Tabs{Name: "tab", Value: cur, Items: items, Action: wire.Action{ID: "tab"}}
}

// spaceHeader names the space, with its about and its facts: the number of types and entities and the latest change.
func (c *ctx) spaceHeader() wire.Component {
	sp := c.sp
	facts := []wire.Fact{
		{Label: "类型", Value: fmt.Sprintf("%d 类", len(sp.Types))},
		{Label: "条目", Value: fmt.Sprintf("%d 条", len(sp.Entities))},
	}
	if last := latest(sp.Entities); last != nil {
		facts = append(facts, wire.Fact{Label: "最近更新", Value: c.ago(last.Updated)})
	}
	return wire.Component{Type: wire.ComponentHeader, Title: sp.Name, Subtitle: cut(sp.About, maxText), Facts: facts}
}

// noTypes is the overview of a space with no types: one empty state, whose button adds the first type.
func (c *ctx) noTypes() wire.Component {
	return wire.Component{
		Type: wire.ComponentEmpty, Title: "这个空间还没有类型", Detail: "先新建一个类型，例如「任务」或「书」",
		Button: &wire.EmptyButton{Title: "新建类型", Action: *ext.Open("schema:+", "", state{S: c.sp.ID, D: "+"}, "新建类型")},
	}
}

// typeRows is a row per type: its icon and name, the kind of its default view with its count, and its value: the
// share done when it has a flow, else the count. A row opens that type's tab.
func (c *ctx) typeRows() []wire.Component {
	out := []wire.Component{}
	for _, t := range c.sp.Types {
		set := c.entitiesOf(t.Key)
		value := strconv.Itoa(len(set))
		if pct, ok := tallyOf(c.w, set).percent(); ok && len(t.Flow) > 0 {
			value = fmt.Sprintf("%d%%", pct)
		}
		subtitle := viewLabels[t.ViewsOrDefault()[0].Kind] + " · " + strconv.Itoa(len(set)) + " 条"
		out = append(out, wire.Component{
			Type: wire.ComponentRow, Icon: iconName(t.Icon), Title: t.Name, Subtitle: subtitle, Value: value,
			Action: &wire.Action{ID: "tab:" + t.Key},
		})
	}
	return out
}

// recent are the five latest entities of the space, of any type.
func (c *ctx) recent() ([]*kernel.Entity, error) {
	res, err := c.w.Run(kernel.Query{Limit: cardRows})
	if err != nil {
		return nil, err
	}
	var out []*kernel.Entity
	for _, g := range res.Groups {
		out = append(out, g.Items...)
	}
	return out, nil
}

// spaceFold is the settings of the space, open: its name and about, save, and the destructive delete, whose
// confirmation names what it deletes.
func (c *ctx) spaceFold() wire.Section {
	sp := c.sp
	return wire.Section{Items: []wire.Component{
		{Type: wire.ComponentField, Name: "name", Label: "名称", Kind: wire.FieldKindText, Value: sp.Name},
		{Type: wire.ComponentField, Name: "about", Label: "简介", Kind: wire.FieldKindText, Value: sp.About,
			Placeholder: "一句话说明这个空间"},
		button("保存", &wire.Action{ID: "save", Submit: true}, wire.ButtonRoleNormal),
		button("删除空间", &wire.Action{ID: "drop", Confirm: fmt.Sprintf(
			"删除空间「%s」？会删除 %d 类、%d 条记录。", sp.Name, len(sp.Types), len(sp.Entities))}, wire.ButtonRoleDestructive),
	}}
}

// card draws the overview card: the totals over all spaces, and up to five entities in progress, newest first.
func (c *ctx) card() *wire.View {
	if len(c.b.Spaces) == 0 {
		return &wire.View{Sections: []wire.Section{{Items: []wire.Component{{
			Type: wire.ComponentEmpty, Title: "还没有空间", Detail: "从一个模板开始",
		}}}}}
	}
	var all tally
	var doing []located
	for _, sp := range c.b.Spaces {
		w := kernel.Open(sp)
		all.add(tallyOf(w, sp.Entities))
		for _, e := range sp.Entities {
			if phaseOf(w, e) == string(kernel.Doing) {
				doing = append(doing, located{sp: sp, e: e})
			}
		}
	}
	slices.SortStableFunc(doing, func(a, b located) int { return b.e.Updated.Compare(a.e.Updated) })
	if len(doing) > cardRows {
		doing = doing[:cardRows]
	}
	pct, detail, tone := "—", "", ""
	if p, ok := all.percent(); ok {
		pct, detail, tone = fmt.Sprintf("%d%%", p), fmt.Sprintf("已完成 %d", all.done), wire.TonePositive
	}
	v := &wire.View{Sections: []wire.Section{{Items: []wire.Component{statGrid([]wire.Stat{
		{Label: "进行中", Value: strconv.Itoa(all.doing), Detail: fmt.Sprintf("%d 个空间", len(c.b.Spaces))},
		{Label: "待办", Value: strconv.Itoa(all.todo)},
		{Label: "完成度", Value: pct, Detail: detail, Tone: tone},
	})}}}}
	if len(doing) > 0 {
		items := []wire.Component{}
		for _, l := range doing {
			items = append(items, c.cardRow(l))
		}
		v.Sections = append(v.Sections, wire.Section{Title: "在办", Items: items})
	}
	return v
}

// cardRow is an entity in progress on the card: its space and type, its toned status, and the page that opens it.
func (c *ctx) cardRow(l located) wire.Component {
	t := typeIn(l.sp, l.e.Type)
	subtitle := l.sp.Name
	row := wire.Component{
		Type: wire.ComponentRow, Title: cut(l.e.Title, maxTitle),
		Action: ext.Open("open:"+l.e.ID, PageUI, state{S: l.sp.ID, E: l.e.ID}, l.e.Title),
	}
	if t != nil {
		subtitle += " · " + t.Name
		if l.e.Status != "" {
			row.Badge = &wire.Badge{Text: l.e.Status, Tone: toneOf(t, l.e.Status)}
		}
	}
	row.Subtitle = subtitle
	return row
}

// located is an entity with the space it is in.
type located struct {
	sp *kernel.Space
	e  *kernel.Entity
}

// tally counts entities by phase. Entities without a flow count for nothing.
type tally struct{ todo, doing, done int }

func tallyOf(w *kernel.World, set []*kernel.Entity) tally {
	var t tally
	for _, e := range set {
		switch phaseOf(w, e) {
		case string(kernel.Todo):
			t.todo++
		case string(kernel.Doing):
			t.doing++
		case string(kernel.Done):
			t.done++
		}
	}
	return t
}

func (t *tally) add(o tally) {
	t.todo += o.todo
	t.doing += o.doing
	t.done += o.done
}

// percent is the share of the entities in phase done among those with a flow that are not dropped; false when there
// are none.
func (t tally) percent() (int, bool) {
	den := t.todo + t.doing + t.done
	if den == 0 {
		return 0, false
	}
	return int(math.Round(float64(t.done) * 100 / float64(den))), true
}

// phaseOf is the phase of an entity's status, "" when it has no flow.
func phaseOf(w *kernel.World, e *kernel.Entity) string {
	p, _ := w.Value(e, "phase").(string)
	return p
}

// spaceStats are the totals of a space: the three phases and the share done.
func spaceStats(t tally) []wire.Stat {
	pct := "—"
	if p, ok := t.percent(); ok {
		pct = fmt.Sprintf("%d%%", p)
	}
	return []wire.Stat{
		{Label: "待办", Value: strconv.Itoa(t.todo)},
		{Label: "进行中", Value: strconv.Itoa(t.doing)},
		{Label: "已完成", Value: strconv.Itoa(t.done)},
		{Label: "完成度", Value: pct},
	}
}

// entitiesOf are the entities of the space shown with the type key.
func (c *ctx) entitiesOf(key string) []*kernel.Entity {
	var out []*kernel.Entity
	for _, e := range c.sp.Entities {
		if e != nil && e.Type == key {
			out = append(out, e)
		}
	}
	return out
}

// latest is the entity changed last; nil for none.
func latest(set []*kernel.Entity) *kernel.Entity {
	var best *kernel.Entity
	for _, e := range set {
		if e != nil && (best == nil || e.Updated.After(best.Updated)) {
			best = e
		}
	}
	return best
}

// typeIn is the type of the space with the key; nil when there is none.
func typeIn(sp *kernel.Space, key string) *kernel.Type {
	for _, t := range sp.Types {
		if t != nil && t.Key == key {
			return t
		}
	}
	return nil
}

// statGrid is a row of stats.
func statGrid(stats []wire.Stat) wire.Component {
	return wire.Component{Type: wire.ComponentStatGrid, Stats: stats}
}

// button is a button with a role.
func button(title string, a *wire.Action, role string) wire.Component {
	return wire.Component{Type: wire.ComponentButton, Title: title, Action: a, Role: role}
}
