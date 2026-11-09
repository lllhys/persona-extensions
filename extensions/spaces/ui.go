package main

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The page draws whatever the ontology holds: the spaces become the filter, a space's types its tabs, a type's
// view (list, board, progress, table, feed) the shape of its entities, and its fields the forms. Nothing on it is
// written for a particular type.

const (
	pageUI = "spaces"
	cardUI = "spaces-card"
	shown  = 100 // the entities a type's page draws
)

// page is a page's state. Mode says which page it is: the space's home, an entity, a form.
type page struct {
	Space  string `json:"s,omitempty"`
	Type   string `json:"t,omitempty"`
	Entity string `json:"e,omitempty"`
	Mode   string `json:"m,omitempty"`
	Query  string `json:"q,omitempty"`
}

const (
	modeHome     = ""
	modeEntity   = "entity"
	modeEdit     = "edit"      // an entity's form; with no entity, a new one of the type
	modeLinks    = "links"     // an entity's outgoing relations
	modeSpace    = "space"     // a space's settings and its types
	modeType     = "type"      // a type's form (pushed; with no type, a new one)
	modeNewSpace = "new-space" // in place of the home, which it returns to
	modeNewType  = "new-type"  // in place of the home, which it returns to
)

var viewTitles = map[string]string{"list": "列表", "board": "看板", "progress": "进度", "table": "表格", "feed": "动态流"}

// draw builds views from one reading of the state.
type draw struct {
	st  *State
	now time.Time
	loc *time.Location
}

func (a *app) render(_ context.Context, r *ext.UIRequest) (*wire.View, error) {
	var p page
	if err := r.DecodeState(&p); err != nil {
		return nil, &ext.Error{Code: "invalid", Message: "invalid page state"}
	}
	var v *wire.View
	err := a.db().read(func(st *State) { v = a.drawer(st, r).view(p) })
	return v, err
}

func (a *app) drawer(st *State, r *ext.UIRequest) draw {
	loc, err := time.LoadLocation(r.TZ)
	if err != nil {
		loc = time.UTC
	}
	return draw{st: st, now: a.now(), loc: loc}
}

func (d draw) view(p page) *wire.View {
	switch p.Mode {
	case modeEntity, modeEdit, modeLinks:
		if p.Entity != "" {
			e := d.st.entity(p.Entity)
			if e == nil {
				return gone()
			}
			sp, _ := d.st.space(e.Space)
			t := sp.typeDef(e.Type)
			switch p.Mode {
			case modeEdit:
				return d.entityForm(p, sp, t, e)
			case modeLinks:
				return d.linksView(p, sp, e)
			}
			return d.entityView(p, sp, t, e)
		}
		sp, err := d.st.space(p.Space)
		if err != nil || sp.typeDef(p.Type) == nil {
			return gone()
		}
		return d.entityForm(p, sp, sp.typeDef(p.Type), nil)
	case modeSpace, modeType:
		sp, err := d.st.space(p.Space)
		if err != nil {
			return gone()
		}
		if p.Mode == modeSpace {
			return d.spaceView(p, sp)
		}
		return d.typeForm(p, sp)
	case modeNewSpace:
		return d.spaceForm(p, nil)
	case modeNewType:
		if sp, err := d.st.space(p.Space); err == nil {
			return d.typeForm(p, sp)
		}
	}
	return d.home(p)
}

// starters offers the templates a new space starts from, each made in one tap (the blank one opens the form).
func starters() wire.Section {
	sec := wire.Section{Title: "从常见模板开始", Footer: "模板只是起点：建好后类型和字段都能随时改。"}
	for _, t := range templates {
		c := wire.Component{Type: wire.ComponentRow, Key: "tpl-" + t.Key, Icon: t.Icon, Title: t.Name, Subtitle: templateSummary(&t),
			Action: &wire.Action{ID: "from-template", Values: map[string]string{"template": t.Key}}}
		if len(t.Types) == 0 {
			c.Action = &wire.Action{ID: "new-space"}
		}
		sec.Items = append(sec.Items, c)
	}
	return sec
}

// templateSummary is what a template brings: its types and their views, or that it brings none.
func templateSummary(t *Template) string {
	if len(t.Types) == 0 {
		return "不预置类型，完全由你定义"
	}
	parts := make([]string, len(t.Types))
	for i, d := range t.Types {
		parts[i] = d.Label + "（" + viewTitles[d.View] + "）"
	}
	return strings.Join(parts, " · ")
}

func gone() *wire.View {
	return &wire.View{Sections: []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Title: "它已经不在了", Detail: "可能刚被删除，返回上一页看看。"}}}}}
}

// ---- The home: a space, its types as tabs, a type's entities in its view ----

func (d draw) home(p page) *wire.View {
	if len(d.st.Spaces) == 0 {
		return &wire.View{Title: "空间", State: ext.EncodeState(p), Sections: []wire.Section{{Items: []wire.Component{{
			Type: wire.ComponentEmpty, Icon: "folder", Title: "还没有空间",
			Detail: "空间是一块按你的想法组织的数据：有哪些类型、每种类型记哪些字段、怎样呈现，都由你来定。" +
				"在对话里告诉助手你想管理什么，它会把结构搭好；也可以直接新建。",
			Button: &wire.EmptyButton{Title: "新建空间", Action: wire.Action{ID: "new-space"}},
		}}}, starters()}}
	}
	sp, err := d.st.space(p.Space)
	if err != nil {
		sp = d.st.Spaces[0]
	}
	p.Space = sp.ID
	v := &wire.View{Title: sp.Name}
	choices := make([]wire.Choice, 0, len(d.st.Spaces)+1)
	for _, s := range d.st.Spaces {
		choices = append(choices, wire.Choice{Value: s.ID, Title: s.Name})
	}
	choices = append(choices, wire.Choice{Value: "+", Title: "＋ 新建空间"})
	v.Filter = &wire.Filter{Name: "space", Title: "空间", Choices: choices, Value: sp.ID, Action: wire.Action{ID: "space"}}
	settings := wire.Section{Items: []wire.Component{{Type: wire.ComponentRow, Icon: "settings", Title: "空间设置",
		Subtitle: "名称、类型与字段", Action: push(page{Space: sp.ID, Mode: modeSpace}, sp.Name)}}}

	if len(sp.Types) == 0 {
		v.State = ext.EncodeState(p)
		v.Sections = []wire.Section{{Items: []wire.Component{{
			Type: wire.ComponentEmpty, Icon: sp.Icon, Title: "「" + sp.Name + "」还没有类型",
			Detail: "类型规定这里记什么、有哪些字段、怎样呈现。在对话里描述你想管理的事情，助手会定义好结构；也可以自己新建。",
			Button: &wire.EmptyButton{Title: "新建类型", Action: wire.Action{ID: "new-type"}},
		}}}, settings}
		return v
	}
	t := sp.typeDef(p.Type)
	if t == nil {
		t = &sp.Types[0]
	}
	p.Type = t.Key
	v.State = ext.EncodeState(p)
	tabs := make([]wire.Tab, 0, len(sp.Types)+1)
	for i := range sp.Types {
		tabs = append(tabs, wire.Tab{Value: sp.Types[i].Key, Title: sp.Types[i].Label, Badge: count(len(d.st.entities(sp.ID, &sp.Types[i])))})
	}
	if len(sp.Types) < maxTypes {
		tabs = append(tabs, wire.Tab{Value: "+", Title: "＋"})
	}
	v.Tabs = &wire.Tabs{Name: "type", Value: t.Key, Items: tabs, Action: wire.Action{ID: "type"}}
	v.Search = &wire.Search{Placeholder: "搜索" + t.Label, Value: p.Query, Action: wire.Action{ID: "search"}}

	all := d.st.entities(sp.ID, t)
	ents, _, _ := d.st.query(Query{Space: sp.ID, Type: t.Key, Text: p.Query, Limit: shown})
	create := push(page{Space: sp.ID, Type: t.Key, Mode: modeEdit}, "新建"+t.Label)
	if len(all) == 0 {
		v.Sections = []wire.Section{{Items: []wire.Component{{
			Type: wire.ComponentEmpty, Icon: sp.Icon, Title: "还没有" + t.Label,
			Detail: "在对话里让助手记下来，或者直接新建。", Button: &wire.EmptyButton{Title: "新建" + t.Label, Action: *create},
		}}}, settings}
		return v
	}
	top := wire.Section{}
	if sp.Description != "" && p.Query == "" {
		top.Items = append(top.Items, wire.Component{Type: wire.ComponentText, Style: wire.TextStyleSecondary, Text: sp.Description})
	}
	if stats := summary(t, all); len(stats) > 0 && p.Query == "" {
		top.Items = append(top.Items, wire.Component{Type: wire.ComponentStatGrid, Stats: stats})
	}
	top.Items = append(top.Items, wire.Component{Type: wire.ComponentButton, Title: "新建" + t.Label, Action: create})
	v.Sections = append([]wire.Section{top}, d.body(t, ents, p.Query != "")...)
	if len(all) > shown && p.Query == "" {
		v.Sections[len(v.Sections)-1].Footer = fmt.Sprintf("显示最近的 %d 条，共 %d 条；搜索可以找到其余的。", shown, len(all))
	}
	v.Sections = append(v.Sections, settings)
	return v
}

// summary is what is worth counting about a type: its statuses, or how far its entities have come.
func summary(t *TypeDef, ents []*Entity) []wire.Stat {
	if t.View == "progress" {
		sum, done := 0.0, 0
		for _, e := range ents {
			r := ratio(t, e)
			sum += r
			if r >= 1 {
				done++
			}
		}
		return []wire.Stat{
			{Label: "平均进度", Value: percent(sum / float64(len(ents)))},
			{Label: "已完成", Value: fmt.Sprintf("%d / %d", done, len(ents)), Tone: tone(done > 0, wire.TonePositive)},
		}
	}
	if t.View == "board" || len(t.Statuses) < 2 || len(t.Statuses) > wire.StatsMost {
		return nil
	}
	stats := make([]wire.Stat, len(t.Statuses))
	for i, s := range t.Statuses {
		n := 0
		for _, e := range ents {
			if e.Status == s {
				n++
			}
		}
		stats[i] = wire.Stat{Label: s, Value: strconv.Itoa(n)}
	}
	return stats
}

// body draws the entities in the type's view. Rows open an entity; a table opens nothing, so a search turns it
// into rows.
func (d draw) body(t *TypeDef, ents []*Entity, searching bool) []wire.Section {
	if len(ents) == 0 {
		return []wire.Section{{Items: []wire.Component{{Type: wire.ComponentEmpty, Icon: "search", Title: "没有找到"}}}}
	}
	view := t.View
	if searching && (view == "table" || view == "board") {
		view = "list"
	}
	switch view {
	case "board":
		var out []wire.Section
		for _, s := range t.Statuses {
			sec := wire.Section{}
			for _, e := range ents {
				if e.Status == s {
					sec.Items = append(sec.Items, d.row(t, e, false))
				}
			}
			if len(sec.Items) > 0 {
				sec.Title = fmt.Sprintf("%s · %d", s, len(sec.Items))
				out = append(out, sec)
			}
		}
		return out
	case "table":
		fields := t.Fields
		cols := []wire.TableColumn{{Title: "标题"}}
		if len(t.Statuses) > 0 {
			cols = append(cols, wire.TableColumn{Title: "状态"})
		}
		fields = fields[:min(len(fields), 8-len(cols))]
		for _, f := range fields {
			col := wire.TableColumn{Title: f.Label}
			if f.Kind == "number" {
				col.Align = "end"
			}
			cols = append(cols, col)
		}
		rows := make([][]string, 0, len(ents))
		for _, e := range ents {
			row := []string{clip(e.Title, 200)}
			if len(t.Statuses) > 0 {
				row = append(row, e.Status)
			}
			for _, f := range fields {
				row = append(row, clip(display(&f, e.Values[f.Key]), 200))
			}
			rows = append(rows, row)
		}
		return []wire.Section{{Items: []wire.Component{{Type: wire.ComponentTable, Columns: cols, Rows: rows}},
			Footer: "搜索可以打开单条记录。"}}
	case "progress":
		sec := wire.Section{}
		for _, e := range ents {
			r := ratio(t, e)
			value, target := progressOf(t, e)
			sec.Items = append(sec.Items, d.row(t, e, true), wire.Component{Type: wire.ComponentProgress,
				Label: value + " / " + target, Ratio: &r, Tone: tone(r >= 1, wire.TonePositive)})
		}
		return []wire.Section{sec}
	}
	sec := wire.Section{}
	for _, e := range ents {
		sec.Items = append(sec.Items, d.row(t, e, true))
	}
	return []wire.Section{sec}
}

// row is an entity as a row: its title, a few of its values, its status, and in a feed when it came.
func (d draw) row(t *TypeDef, e *Entity, badge bool) wire.Component {
	c := wire.Component{Type: wire.ComponentRow, Key: "e-" + e.ID, Title: clip(e.Title, 200),
		Action: push(page{Entity: e.ID, Mode: modeEntity}, e.Title)}
	var parts []string
	for _, f := range t.Fields {
		v, ok := e.Values[f.Key]
		if !ok || len(parts) == 3 || (t.View == "progress" && f.Kind == "number") {
			continue
		}
		switch {
		case f.Kind == "boolean" && v == true:
			parts = append(parts, f.Label)
		case f.Kind == "boolean":
		case t.View == "feed" && f.Kind == "text":
			parts = append(parts, clip(display(&f, v), 140))
		default:
			parts = append(parts, f.Label+" "+clip(display(&f, v), 40))
		}
	}
	c.Subtitle = strings.Join(parts, " · ")
	if t.View == "feed" {
		c.Value = d.when(e.Created)
	}
	if badge && e.Status != "" {
		c.Badge = &wire.Badge{Text: e.Status}
	}
	return c
}

// ---- An entity ----

func (d draw) entityView(p page, sp *Space, t *TypeDef, e *Entity) *wire.View {
	head := wire.Component{Type: wire.ComponentHeader, Title: clip(e.Title, 200), Subtitle: t.Label + " · 更新于 " + d.when(e.Updated)}
	if e.Status != "" {
		head.Badges = []wire.Badge{{Text: e.Status}}
	}
	var more []wire.Component
	for _, f := range t.Fields {
		v, ok := e.Values[f.Key]
		if !ok {
			continue
		}
		text := display(&f, v)
		switch {
		case f.Kind == "url" && strings.HasPrefix(text, "https://"):
			more = append(more, wire.Component{Type: wire.ComponentLink, Title: f.Label, URL: text})
		case f.Kind == "url" || utf8.RuneCountInString(text) > 24 || len(head.Facts) == 8:
			more = append(more, wire.Component{Type: wire.ComponentRow, Title: f.Label, Subtitle: text})
		default:
			head.Facts = append(head.Facts, wire.Fact{Label: f.Label, Value: text})
		}
	}
	secs := []wire.Section{{Items: []wire.Component{head}}}
	if len(more) > 0 {
		secs = append(secs, wire.Section{Items: more})
	}

	rel := wire.Section{Title: "关系"}
	out, in := d.st.links(e.ID)
	for _, r := range out {
		rel.Items = d.linkRow(rel.Items, r.To, "→ "+r.Type)
	}
	for _, r := range in {
		rel.Items = d.linkRow(rel.Items, r.From, "← "+r.Type)
	}
	rel.Items = rel.Items[:min(len(rel.Items), shown)]
	rel.Items = append(rel.Items, wire.Component{Type: wire.ComponentButton, Title: "管理关系",
		Action: push(page{Entity: e.ID, Mode: modeLinks}, "关系")})
	secs = append(secs, rel)

	// Derive: the entity grows into another type of the space (an idea into a goal), pointing back at it.
	var others []wire.Choice
	for _, o := range sp.Types {
		if o.Key != t.Key {
			others = append(others, wire.Choice{Value: o.Key, Title: o.Label})
		}
	}
	if len(others) > 0 {
		secs = append(secs, wire.Section{Title: "衍生", Footer: "新记录带上两种类型共有的字段，并以「源自」关系指回这一条。", Items: []wire.Component{
			{Type: wire.ComponentField, Name: "derive_to", Label: "衍生为", Kind: wire.FieldKindChoice, Choices: others, Value: others[0].Value},
			{Type: wire.ComponentButton, Title: "衍生新记录", Action: &wire.Action{ID: "derive", Submit: true}},
		}})
	}

	acts := wire.Section{}
	if i := slices.Index(t.Statuses, e.Status); i >= 0 && i+1 < len(t.Statuses) {
		acts.Items = append(acts.Items, wire.Component{Type: wire.ComponentButton, Title: "移到「" + t.Statuses[i+1] + "」",
			Action: &wire.Action{ID: "advance"}})
	}
	acts.Items = append(acts.Items,
		wire.Component{Type: wire.ComponentButton, Title: "编辑", Action: push(page{Entity: e.ID, Mode: modeEdit}, "编辑")},
		wire.Component{Type: wire.ComponentButton, Title: "删除", Action: &wire.Action{ID: "delete-entity",
			Confirm: "删除「" + clip(e.Title, 40) + "」和它的关系？"}})
	secs = append(secs, acts)
	p.Space, p.Type = sp.ID, t.Key
	return &wire.View{Title: clip(e.Title, 200), State: ext.EncodeState(p), Sections: secs}
}

func (d draw) linkRow(rows []wire.Component, id, label string) []wire.Component {
	other := d.st.entity(id)
	if other == nil {
		return rows
	}
	sp, _ := d.st.space(other.Space)
	if t := sp.typeDef(other.Type); t != nil {
		label += " · " + t.Label
	}
	return append(rows, wire.Component{Type: wire.ComponentRow, Title: clip(other.Title, 200), Subtitle: label,
		Action: push(page{Entity: id, Mode: modeEntity}, other.Title)})
}

// linksView lists an entity's outgoing relations, each removed by a tap, and adds one.
func (d draw) linksView(p page, sp *Space, e *Entity) *wire.View {
	out, _ := d.st.links(e.ID)
	have := wire.Section{Title: "它指向的"}
	for i, r := range out[:min(len(out), shown)] {
		other := d.st.entity(r.To)
		if other == nil {
			continue
		}
		have.Items = append(have.Items, wire.Component{Type: wire.ComponentRow, Title: r.Type + " → " + clip(other.Title, 200),
			Subtitle: "点按移除", Action: &wire.Action{ID: "unlink", Values: map[string]string{"i": strconv.Itoa(i)},
				Confirm: "移除「" + r.Type + "」这条关系？"}})
	}
	if len(have.Items) == 0 {
		have.Items = []wire.Component{{Type: wire.ComponentText, Style: wire.TextStyleSecondary, Text: "还没有。"}}
	}
	var targets []wire.Choice
	for _, o := range d.st.Entities {
		if o.Space == sp.ID && o.ID != e.ID && len(targets) < 200 {
			targets = append(targets, wire.Choice{Value: o.ID, Title: clip(sp.typeDef(o.Type).Label+" · "+o.Title, 80)})
		}
	}
	add := wire.Section{Title: "添加"}
	if len(targets) == 0 {
		add.Items = []wire.Component{{Type: wire.ComponentText, Style: wire.TextStyleSecondary, Text: "这个空间里还没有别的记录。"}}
	} else {
		add.Items = []wire.Component{
			{Type: wire.ComponentField, Name: "rel", Label: "关系", Kind: wire.FieldKindText, Placeholder: "例如 属于、依赖、参考"},
			{Type: wire.ComponentField, Name: "to", Label: "指向", Kind: wire.FieldKindChoice, Choices: targets, Value: targets[0].Value},
			{Type: wire.ComponentButton, Title: "添加", Action: &wire.Action{ID: "link", Submit: true}},
		}
	}
	return &wire.View{Title: "关系 · " + clip(e.Title, 60), State: ext.EncodeState(p), Sections: []wire.Section{have, add}}
}

// entityForm is drawn from the type's fields: a new entity of the type, or e.
func (d draw) entityForm(p page, sp *Space, t *TypeDef, e *Entity) *wire.View {
	items := []wire.Component{{Type: wire.ComponentField, Name: "title", Label: "标题", Kind: wire.FieldKindText}}
	vals := map[string]any{}
	status := ""
	if e != nil {
		items[0].Value, vals, status = e.Title, e.Values, e.Status
	}
	if len(t.Statuses) > 0 {
		choices := make([]wire.Choice, len(t.Statuses))
		for i, s := range t.Statuses {
			choices[i] = wire.Choice{Value: s}
		}
		items = append(items, wire.Component{Type: wire.ComponentField, Name: "status", Label: "状态", Kind: wire.FieldKindChoice,
			Choices: choices, Value: cmp.Or(status, t.Statuses[0])})
	}
	for _, f := range t.Fields {
		c := wire.Component{Type: wire.ComponentField, Name: "f_" + f.Key, Label: f.Label, Kind: wire.FieldKindText, Value: plain(vals[f.Key])}
		if f.Required {
			c.Label += " *"
		}
		switch f.Kind {
		case "number":
			c.Kind = wire.FieldKindNumber
		case "boolean":
			c.Kind, c.Value = wire.FieldKindToggle, strconv.FormatBool(vals[f.Key] == true)
		case "date":
			c.Placeholder = "YYYY-MM-DD"
		case "url":
			c.Placeholder = "https://"
		case "enum":
			c.Kind = wire.FieldKindChoice
			if !f.Required {
				c.Choices = append(c.Choices, wire.Choice{Value: "", Title: "—"})
			} else if c.Value == "" {
				c.Value = f.Options[0]
			}
			for _, o := range f.Options {
				c.Choices = append(c.Choices, wire.Choice{Value: o})
			}
		}
		items = append(items, c)
	}
	items = append(items, wire.Component{Type: wire.ComponentButton, Title: "保存", Action: &wire.Action{ID: "save-entity", Submit: true}})
	title := "新建" + t.Label
	if e != nil {
		title = "编辑" + t.Label
	}
	p.Space, p.Type = sp.ID, t.Key
	return &wire.View{Title: title, State: ext.EncodeState(p), Sections: []wire.Section{{Items: items}}}
}

// ---- A space and its types ----

func (d draw) spaceView(p page, sp *Space) *wire.View {
	v := d.spaceForm(p, sp)
	types := wire.Section{Title: "类型"}
	for _, t := range sp.Types {
		types.Items = append(types.Items, wire.Component{Type: wire.ComponentRow, Title: t.Label,
			Subtitle: fmt.Sprintf("%s · %s · %d 个字段 · %d 条", t.Key, viewTitles[t.View], len(t.Fields), len(d.st.entities(sp.ID, &t))),
			Action:   push(page{Space: sp.ID, Type: t.Key, Mode: modeType}, t.Label)})
	}
	if len(sp.Types) < maxTypes {
		types.Items = append(types.Items, wire.Component{Type: wire.ComponentButton, Title: "新建类型",
			Action: push(page{Space: sp.ID, Mode: modeType}, "新建类型")})
	}
	n := 0
	for _, e := range d.st.Entities {
		if e.Space == sp.ID {
			n++
		}
	}
	danger := wire.Section{Items: []wire.Component{{Type: wire.ComponentButton, Title: "删除空间", Action: &wire.Action{ID: "delete-space",
		Confirm: fmt.Sprintf("删除「%s」和其中的 %d 条记录？这无法撤销。", sp.Name, n)}}}}
	v.Sections = append(v.Sections, types, danger)
	return v
}

// spaceForm edits sp, or makes a new space in place of the home.
func (d draw) spaceForm(p page, sp *Space) *wire.View {
	icons := make([]wire.Choice, len(wire.NavIcons))
	for i, ic := range wire.NavIcons {
		icons[i] = wire.Choice{Value: ic}
	}
	if sp != nil {
		return &wire.View{Title: sp.Name, State: ext.EncodeState(p), Sections: []wire.Section{{Items: []wire.Component{
			{Type: wire.ComponentField, Name: "name", Label: "名称", Kind: wire.FieldKindText, Value: sp.Name},
			{Type: wire.ComponentField, Name: "icon", Label: "图标", Kind: wire.FieldKindChoice, Choices: icons, Value: sp.Icon},
			{Type: wire.ComponentField, Name: "description", Label: "说明", Kind: wire.FieldKindText, Value: sp.Description},
			{Type: wire.ComponentButton, Title: "保存", Action: &wire.Action{ID: "save-space", Submit: true}},
		}}}}
	}
	// A new space: a template, and the name, icon and description, which the template's fill when left empty.
	tpls := make([]wire.Choice, len(templates))
	brings := wire.Section{Title: "模板里有什么", Footer: "模板只是起点：它先建好常用的类型、字段和状态，之后都能在空间设置里改。"}
	for i, t := range templates {
		// The choice keeps to the short name so the picker is not cut off; what a template brings is read below.
		tpls[i] = wire.Choice{Value: t.Key, Title: t.Name}
		if len(t.Types) == 0 {
			continue
		}
		brings.Items = append(brings.Items,
			wire.Component{Type: wire.ComponentRow, Icon: t.Icon, Title: t.Name, Subtitle: t.Description},
			wire.Component{Type: wire.ComponentText, Style: wire.TextStyleCaption, Text: "包含：" + templateSummary(&t)})
	}
	icons = append([]wire.Choice{{Value: "", Title: "跟随模板"}}, icons...)
	items := []wire.Component{
		{Type: wire.ComponentField, Name: "template", Label: "模板", Kind: wire.FieldKindChoice, Choices: tpls, Value: "blank"},
		{Type: wire.ComponentField, Name: "examples", Label: "附带几条示例记录", Kind: wire.FieldKindToggle, Value: "false"},
		{Type: wire.ComponentField, Name: "name", Label: "名称", Kind: wire.FieldKindText, Placeholder: "不填就用模板名，例如 读书、装修、旅行"},
		{Type: wire.ComponentField, Name: "icon", Label: "图标", Kind: wire.FieldKindChoice, Choices: icons, Value: ""},
		{Type: wire.ComponentField, Name: "description", Label: "说明", Kind: wire.FieldKindText, Placeholder: "不填就用模板的说明"},
		{Type: wire.ComponentButton, Title: "创建", Action: &wire.Action{ID: "save-space", Submit: true}},
	}
	if len(d.st.Spaces) > 0 {
		items = append(items, wire.Component{Type: wire.ComponentButton, Title: "取消", Action: &wire.Action{ID: "cancel"}})
	}
	return &wire.View{Title: "新建空间", State: ext.EncodeState(p), Sections: []wire.Section{{Items: items}, brings}}
}

// typeForm edits the page's type, or defines a new one. Fields are written one a line (or split by ;) as
// key:名称:kind, with :选项|选项 for an enum and a trailing ! for a required field.
func (d draw) typeForm(p page, sp *Space) *wire.View {
	t := TypeDef{View: "list"}
	isNew := p.Mode == modeNewType || sp.typeDef(p.Type) == nil
	if !isNew {
		t = *sp.typeDef(p.Type)
	}
	views := make([]wire.Choice, len(viewKinds))
	for i, k := range viewKinds {
		views[i] = wire.Choice{Value: k, Title: viewTitles[k]}
	}
	var items []wire.Component
	if isNew {
		items = append(items, wire.Component{Type: wire.ComponentField, Name: "key", Label: "key", Kind: wire.FieldKindText, Placeholder: "小写英文，例如 book"})
	}
	items = append(items,
		wire.Component{Type: wire.ComponentField, Name: "label", Label: "名称", Kind: wire.FieldKindText, Value: t.Label, Placeholder: "例如 书"},
		wire.Component{Type: wire.ComponentField, Name: "view", Label: "呈现为", Kind: wire.FieldKindChoice, Choices: views, Value: t.View},
		wire.Component{Type: wire.ComponentField, Name: "statuses", Label: "状态（依次流转，可不填）", Kind: wire.FieldKindText,
			Value: strings.Join(t.Statuses, ", "), Placeholder: "想读, 在读, 读完"},
		wire.Component{Type: wire.ComponentField, Name: "fields", Label: "字段", Kind: wire.FieldKindText, Value: formatFields(t.Fields),
			Placeholder: "author:作者:text; pages:页数:number; read:已读:number; tag:分类:enum:小说|历史!"},
		wire.Component{Type: wire.ComponentText, Style: wire.TextStyleCaption,
			Text: "字段写成 key:名称:类型，用 ; 隔开。类型有 text、number、boolean、date、enum、url；enum 在后面加 :选项|选项，末尾加 ! 表示必填。进度视图把第一个 number 当当前值、第二个当目标值（没有则按 100）。"},
		wire.Component{Type: wire.ComponentButton, Title: "保存", Action: &wire.Action{ID: "save-type", Submit: true}},
	)
	title := "新建类型"
	switch {
	case p.Mode == modeNewType:
		items = append(items, wire.Component{Type: wire.ComponentButton, Title: "取消", Action: &wire.Action{ID: "cancel"}})
	case !isNew:
		title = t.Label
		items = append(items, wire.Component{Type: wire.ComponentButton, Title: "删除类型", Action: &wire.Action{ID: "delete-type",
			Confirm: "删除类型「" + t.Label + "」？只有没有记录的类型可以删除。"}})
	}
	return &wire.View{Title: title, State: ext.EncodeState(p), Sections: []wire.Section{{Items: items}}}
}

// ---- Actions ----

func (a *app) action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	var p page
	if err := r.DecodeState(&p); err != nil {
		return nil, &ext.Error{Code: "invalid", Message: "invalid page state"}
	}
	res := &wire.ActionResult{}
	switch r.Action {
	case "space":
		if r.Values["space"] == "+" {
			p.Mode = modeNewSpace
		} else {
			p = page{Space: r.Values["space"]}
		}
	case "type":
		if r.Values["type"] == "+" {
			p.Mode = modeNewType
		} else {
			p.Type, p.Query = r.Values["type"], ""
		}
	case "search":
		p.Query = strings.TrimSpace(r.Values["q"])
	case "new-space":
		p.Mode = modeNewSpace
	case "new-type":
		p.Mode = modeNewType
	case "cancel":
		p.Mode = modeHome
	default:
		err := a.db().update(func(st *State, now time.Time) error { return change(st, now, r, &p, res) })
		if err != nil {
			return nil, &ext.Error{Code: "invalid", Message: err.Error()}
		}
	}
	if res.Pop {
		return res, nil
	}
	var err error
	res.View, err = a.render(ctx, &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(p), TZ: r.TZ}})
	return res, err
}

// change does what an action asks of the state; it moves the page (p) or sets what the result says.
func change(st *State, now time.Time, r *ext.UIRequest, p *page, res *wire.ActionResult) error {
	val := func(k string) *string { s := r.Values[k]; return &s }
	saved := func() { res.Pop, res.Toast = true, "已保存" }
	switch r.Action {
	case "save-space":
		in := SpaceInput{Name: val("name"), Icon: val("icon"), Description: val("description")}
		if p.Mode != modeNewSpace {
			in.ID = p.Space
		} else {
			in.Template, in.Examples = cmp.Or(r.Values["template"], "blank"), r.Values["examples"] == "true"
		}
		sp, err := st.upsertSpace(in, now)
		if err != nil {
			return err
		}
		if p.Mode != modeNewSpace {
			saved()
		}
		*p = page{Space: sp.ID}
	case "from-template":
		sp, err := st.upsertSpace(SpaceInput{Template: r.Values["template"]}, now)
		if err != nil {
			return err
		}
		*p, res.Toast = page{Space: sp.ID}, "已从模板建好「"+sp.Name+"」"
	case "derive":
		e, err := st.deriveEntity(p.Entity, r.Values["derive_to"], "", "", now)
		if err != nil {
			return err
		}
		sp, _ := st.space(e.Space)
		res.Toast = "已衍生为" + sp.typeDef(e.Type).Label
		res.Push = &wire.Push{State: ext.EncodeState(page{Entity: e.ID, Mode: modeEntity}), Title: clip(e.Title, 60)}
	case "delete-space":
		res.Pop, res.Toast = true, "已删除"
		return st.deleteSpace(p.Space)
	case "save-type":
		fields, err := parseFields(r.Values["fields"])
		if err != nil {
			return err
		}
		key := p.Type
		if p.Mode == modeNewType || key == "" {
			key = strings.TrimSpace(r.Values["key"])
			if sp, err := st.space(p.Space); err == nil && sp.typeDef(key) != nil {
				return fmt.Errorf("已经有类型 %q 了", key)
			}
		}
		t, err := st.upsertType(p.Space, TypeDef{Key: key, Label: r.Values["label"], View: r.Values["view"],
			Statuses: split(r.Values["statuses"], ",，、;；\n"), Fields: fields})
		if err != nil {
			return err
		}
		if p.Mode != modeNewType {
			saved()
		}
		p.Mode, p.Type, p.Query = modeHome, t.Key, ""
	case "delete-type":
		res.Pop, res.Toast = true, "已删除"
		return st.deleteType(p.Space, p.Type)
	case "save-entity":
		in := EntityInput{ID: p.Entity, Space: p.Space, Type: p.Type, Title: val("title"), Status: r.Values["status"], Values: map[string]any{}}
		if e := st.entity(p.Entity); e != nil {
			in.Space, in.Type = e.Space, e.Type
		}
		if sp, err := st.space(in.Space); err == nil && sp.typeDef(in.Type) != nil {
			for _, f := range sp.typeDef(in.Type).Fields {
				if v, ok := r.Values["f_"+f.Key]; ok {
					in.Values[f.Key] = v
				}
			}
		}
		if _, err := st.upsertEntity(in, now); err != nil {
			return err
		}
		saved()
	case "advance":
		e := st.entity(p.Entity)
		if e == nil {
			return fmt.Errorf("这条记录已经不在了")
		}
		sp, _ := st.space(e.Space)
		t := sp.typeDef(e.Type)
		i := slices.Index(t.Statuses, e.Status)
		if i < 0 || i+1 >= len(t.Statuses) {
			return fmt.Errorf("已经是最后一个状态")
		}
		res.Toast = "已移到「" + t.Statuses[i+1] + "」"
		_, err := st.upsertEntity(EntityInput{ID: e.ID, Status: t.Statuses[i+1]}, now)
		return err
	case "delete-entity":
		res.Pop, res.Toast = true, "已删除"
		_, err := st.deleteEntity(p.Entity)
		return err
	case "link", "unlink":
		links := []Link{}
		for _, l := range st.outgoing(p.Entity) {
			links = append(links, Link{Type: l.Type, To: l.To})
		}
		if r.Action == "link" {
			links = append(links, Link{Type: r.Values["rel"], To: r.Values["to"]})
		} else if i, err := strconv.Atoi(r.Values["i"]); err == nil && i >= 0 && i < len(links) {
			links = slices.Delete(links, i, i+1)
		}
		_, err := st.upsertEntity(EntityInput{ID: p.Entity, Links: links}, now)
		return err
	default:
		return fmt.Errorf("unknown action %q", r.Action)
	}
	return nil
}

// ---- The overview card ----

func (a *app) card(_ context.Context, r *ext.UIRequest) (*wire.View, error) {
	var items []wire.Component
	err := a.db().read(func(st *State) {
		for _, sp := range st.Spaces[:min(len(st.Spaces), 4)] {
			n := 0
			for _, e := range st.Entities {
				if e.Space == sp.ID {
					n++
				}
			}
			items = append(items, wire.Component{Type: wire.ComponentRow, Icon: sp.Icon, Title: sp.Name,
				Subtitle: fmt.Sprintf("%d 种类型 · %d 条记录", len(sp.Types), n)})
		}
	})
	if len(items) == 0 {
		items = []wire.Component{{Type: wire.ComponentText, Style: wire.TextStyleSecondary, Text: "还没有空间。在对话里告诉助手你想管理什么，它会把结构搭好。"}}
	}
	return &wire.View{Sections: []wire.Section{{Items: items}}}, err
}

// ---- Formatting ----

func push(p page, title string) *wire.Action {
	return &wire.Action{ID: "open", Push: &wire.Push{State: ext.EncodeState(p), Title: clip(title, 60)}}
}

// display is a value as the page shows it.
func display(f *FieldDef, v any) string {
	if f.Kind == "boolean" {
		if v == true {
			return "是"
		}
		return "否"
	}
	return plain(v)
}

// progressOf is an entity's value and target: its type's first and second number fields, the target 100 when the
// type has one only.
func progressOf(t *TypeDef, e *Entity) (value, target string) {
	var nums []string
	for _, f := range t.Fields {
		if f.Kind == "number" {
			nums = append(nums, f.Key)
		}
	}
	value, target = cmp.Or(plain(e.Values[nums[0]]), "0"), "100"
	if len(nums) > 1 {
		target = cmp.Or(plain(e.Values[nums[1]]), "—")
	}
	return value, target
}

func ratio(t *TypeDef, e *Entity) float64 {
	value, target := progressOf(t, e)
	v, _ := strconv.ParseFloat(value, 64)
	of, err := strconv.ParseFloat(target, 64)
	if err != nil || of <= 0 {
		return 0
	}
	return math.Max(0, math.Min(1, v/of))
}

func percent(r float64) string { return strconv.Itoa(int(math.Round(r*100))) + "%" }

func tone(ok bool, t string) string {
	if ok {
		return t
	}
	return ""
}

func count(n int) string {
	if n > 9999 {
		return "9999+"
	}
	return strconv.Itoa(n)
}

// when is a time the way a person says it: just now, minutes, hours or days ago, else the date.
func (d draw) when(t time.Time) string {
	ago := d.now.Sub(t)
	switch {
	case ago < time.Minute:
		return "刚刚"
	case ago < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(ago.Minutes()))
	case ago < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(ago.Hours()))
	case ago < 7*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(ago.Hours()/24))
	}
	return t.In(d.loc).Format(time.DateOnly)
}
