package ui

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// Limits of the screens. A wire string holds at most maxText runes; a row's title at most maxTitle.
const (
	maxText   = 2000
	maxTitle  = 200
	maxSearch = 200
	maxMore   = 16 // the most extra pages a page loads
	pageRows  = 20 // entities of a page of a list, board, progress or feed
	tableRows = 50 // entities of a page of a table
	tableMost = 100
	pairMost  = 140 // entities of a progress or a feed: each one is two components, so the page stays under the item limit
	feedText  = 280 // the most runes of a long lead shown under a feed row
	longLead  = 60  // a lead longer than this is shown under the feed row, not as its subtitle
)

// viewLabels name the view kinds on the type tab and in the overview.
var viewLabels = map[string]string{
	"list": "列表", "board": "看板", "table": "表格", "progress": "进度", "feed": "动态",
}

// phaseLabels are the stats of a type with a flow, in flow order.
var phaseLabels = []struct{ phase, label string }{
	{string(kernel.Todo), "待办"}, {string(kernel.Doing), "进行中"}, {string(kernel.Done), "已完成"},
}

// typePage is the type's tab: the search, the view segmented, the stats of the view, the view's body, the quick-add
// fold when it is open, and the buttons that add an entity or open the type's structure.
func (c *ctx) typePage(t *kernel.Type) (*wire.View, error) {
	filter, tabs := c.chrome()
	views := t.ViewsOrDefault()
	if c.st.V < 0 || c.st.V >= len(views) {
		c.st.V, c.st.N = 0, 0
	}
	view := views[c.st.V]
	pg, err := c.page(t, view)
	if err != nil {
		return nil, err
	}
	stats, err := c.viewStats(t, view, pg)
	if err != nil {
		return nil, err
	}
	v := &wire.View{
		Sections: []wire.Section{}, Filter: filter, Tabs: tabs,
		Search: &wire.Search{Placeholder: "搜索" + t.Name, Value: c.st.Q, Action: wire.Action{ID: "search"}},
	}
	head := []wire.Component{}
	if len(views) >= 2 {
		head = append(head, segmented(views, c.st.V))
	}
	head = append(head, statGrid(stats))
	v.Sections = append(v.Sections, wire.Section{Items: head})
	if pg.res.Total == 0 {
		v.Sections = append(v.Sections, c.emptyRows(t))
	} else {
		v.Sections = append(v.Sections, c.body(t, view, pg)...)
	}
	if c.st.F == "new" {
		v.Sections = append(v.Sections, c.quickAdd(t))
	}
	v.Sections = append(v.Sections, wire.Section{Items: []wire.Component{
		button("新建"+t.Name, &wire.Action{ID: "fold:new"}, wire.ButtonRoleNormal),
		button("结构", ext.Open("schema:"+t.Key, "", state{S: c.sp.ID, D: t.Key}, "结构"), wire.ButtonRoleNormal),
	}})
	return v, nil
}

// page is one run of a view's query: the groups of the page, the grouping used, and whether more entities follow.
type page struct {
	res   kernel.Result
	group string
	more  bool
}

// page runs the view's lens with the search text and the pages loaded so far. The table kind has its own page size;
// the grouping is the view's, except for the kinds that do not group (table, progress, feed) and the board, which
// groups by its own rule.
func (c *ctx) page(t *kernel.Type, view kernel.View) (page, error) {
	size, most := pageRows, kernel.MaxLimit
	switch view.Kind {
	case "table":
		size, most = tableRows, tableMost
	case "progress", "feed":
		most = pairMost
	}
	limit := min(size*(1+c.st.N), most)
	lens := view.Lens
	switch view.Kind {
	case "board":
		lens.Group = c.boardGroup(t, view)
	case "list":
	default:
		lens.Group = ""
	}
	if view.Kind == "feed" {
		lens.Sort = []kernel.Order{{By: c.feedKey(t, view), Desc: true}}
	}
	res, err := c.w.Run(kernel.Query{Type: t.Key, Text: c.st.Q, Lens: lens, Limit: limit})
	if err != nil {
		return page{}, err
	}
	shown := 0
	for _, g := range res.Groups {
		shown += len(g.Items)
	}
	return page{res: res, group: lens.Group, more: shown < res.Total && limit < most}, nil
}

// entities are the entities of the page, in order.
func (pg page) entities() []*kernel.Entity {
	var out []*kernel.Entity
	for _, g := range pg.res.Groups {
		out = append(out, g.Items...)
	}
	return out
}

// viewStats are the stats above the body: the view's own aggregates when it declares any, else the count and, for a
// type with a flow, the count of each phase.
func (c *ctx) viewStats(t *kernel.Type, view kernel.View, pg page) ([]wire.Stat, error) {
	if len(view.Agg) > 0 {
		stats := []wire.Stat{}
		for _, a := range pg.res.Aggs {
			stats = append(stats, wire.Stat{Label: a.Name, Value: textOr(a.Text, "—")})
		}
		return stats, nil
	}
	group := ""
	if len(t.Flow) > 0 {
		group = "phase"
	}
	res, err := c.w.Run(kernel.Query{Type: t.Key, Text: c.st.Q, Lens: kernel.Lens{Group: group}, Limit: 1})
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	total := 0
	for _, g := range res.Groups {
		counts[g.Key] = g.Count
		total += g.Count
	}
	stats := []wire.Stat{{Label: "数量", Value: strconv.Itoa(total)}}
	if group != "" {
		for _, p := range phaseLabels {
			stats = append(stats, wire.Stat{Label: p.label, Value: strconv.Itoa(counts[p.phase])})
		}
	}
	return stats, nil
}

// boardGroup is the field a board groups by: the view's, else the status of a type with a flow, else its first enum
// property, else nothing (one column).
func (c *ctx) boardGroup(t *kernel.Type, view kernel.View) string {
	if view.Group != "" {
		return view.Group
	}
	if len(t.Flow) > 0 {
		return "status"
	}
	for _, p := range t.Props {
		if k, ok := kernel.KindOf(p.Kind); ok && k.Enum != nil {
			return p.Key
		}
	}
	return ""
}

// feedKey is the field a feed is ordered and divided into days by: the view's first sort field when it is a date,
// else the time of the last change.
func (c *ctx) feedKey(t *kernel.Type, view kernel.View) string {
	if len(view.Sort) > 0 {
		if p, ok := c.sp.Field(t, view.Sort[0].By); ok {
			if k, ok := kernel.KindOf(p.Kind); ok && isDate(k, &p) {
				return view.Sort[0].By
			}
		}
	}
	return "updated"
}

// isDate tells a property whose schema is a date.
func isDate(k *kernel.Kind, p *kernel.Prop) bool {
	f, _ := k.Schema(p)["format"].(string)
	return f == "date"
}

// body is the view's sections, with More on the last one when more entities follow.
func (c *ctx) body(t *kernel.Type, view kernel.View, pg page) []wire.Section {
	var out []wire.Section
	switch view.Kind {
	case "board":
		out = c.boardSections(t, pg)
	case "table":
		out = c.tableSections(t, pg)
	case "progress":
		out = c.progressSections(t, pg)
	case "feed":
		out = c.feedSections(t, view, pg)
	default:
		out = c.listSections(pg)
	}
	if pg.more && len(out) > 0 {
		out[len(out)-1].More = &wire.More{Title: "更多", Action: wire.Action{ID: "more"}}
	}
	return out
}

// listSections is the list: one section of rows, or one per group titled with its name and count.
func (c *ctx) listSections(pg page) []wire.Section {
	out := []wire.Section{}
	for _, g := range pg.res.Groups {
		if len(g.Items) == 0 {
			continue
		}
		s := wire.Section{Items: c.rows(g.Items, false)}
		if pg.group != "" {
			s.Title = fmt.Sprintf("%s · %d", groupName(g), g.Count)
		}
		out = append(out, s)
	}
	return out
}

// boardSections is the board: one section per stage of the grouping, every stage present even when it is empty, and
// the groups the grouping does not enumerate after them.
func (c *ctx) boardSections(t *kernel.Type, pg page) []wire.Section {
	if pg.group == "" {
		return c.listSections(pg)
	}
	index := map[string]kernel.Group{}
	for _, g := range pg.res.Groups {
		index[g.Key] = g
	}
	out := []wire.Section{}
	used := map[string]bool{}
	for _, key := range c.enumOf(t, pg.group) {
		used[key] = true
		g, found := index[key]
		out = append(out, columnSection(key, g, found, c.rows))
	}
	for _, g := range pg.res.Groups {
		if !used[g.Key] {
			out = append(out, columnSection(g.Key, g, true, c.rows))
		}
	}
	return out
}

// columnSection is one column of a board. An empty column has a dash.
func columnSection(key string, g kernel.Group, found bool, rows func([]*kernel.Entity, bool) []wire.Component) wire.Section {
	name, count := key, 0
	if key == "" {
		name = unsetLabel
	}
	if found {
		name, count = groupName(g), g.Count
	}
	s := wire.Section{Title: fmt.Sprintf("%s · %d", name, count)}
	if !found || len(g.Items) == 0 {
		s.Items = []wire.Component{{Type: wire.ComponentText, Text: "—", Style: wire.TextStyleCaption}}
		return s
	}
	s.Items = rows(g.Items, false)
	return s
}

// enumOf are the enumerable values of a field of the type, in order; nil when the field is not enumerable.
func (c *ctx) enumOf(t *kernel.Type, key string) []string {
	p, ok := c.sp.Field(t, key)
	if !ok {
		return nil
	}
	k, ok := kernel.KindOf(p.Kind)
	if !ok || k.Enum == nil {
		return nil
	}
	return k.Enum(&p)
}

// tableSections is the table: its columns and the page's cells, the aggregates as caption; then the same entities
// as rows, so that each one stays reachable (a table has no row actions).
func (c *ctx) tableSections(t *kernel.Type, pg page) []wire.Section {
	keys := t.Columns()
	cols := []wire.TableColumn{}
	for _, key := range keys {
		p, _ := c.sp.Field(t, key)
		align := ""
		if k, ok := kernel.KindOf(p.Kind); ok && k.Num != nil {
			align = wire.TableAlignEnd
		}
		cols = append(cols, wire.TableColumn{Title: p.Name, Align: align})
	}
	set := pg.entities()
	rows := [][]string{}
	for _, e := range set {
		cells := []string{}
		for _, key := range keys {
			cells = append(cells, dashIfEmpty(cut(c.w.Text(e, key), maxText)))
		}
		rows = append(rows, cells)
	}
	var parts []string
	for _, a := range pg.res.Aggs {
		parts = append(parts, a.Name+" "+textOr(a.Text, "—"))
	}
	table := wire.Component{
		Type: wire.ComponentTable, Columns: cols, Rows: rows, Caption: cut(strings.Join(parts, " · "), maxText),
	}
	return []wire.Section{
		{Items: []wire.Component{table}},
		{Title: "打开", Items: c.rows(set, false)},
	}
}

// progressSections is a row per entity followed by its progress, when it has one.
func (c *ctx) progressSections(t *kernel.Type, pg page) []wire.Section {
	items := []wire.Component{}
	for _, e := range pg.entities() {
		items = append(items, c.row(e, false))
		if p, ok := c.progress(t, e); ok {
			items = append(items, p)
		}
	}
	return []wire.Section{{Items: items}}
}

// progress is the bar of an entity: its ratio, its share in words, and positive when it is done.
func (c *ctx) progress(t *kernel.Type, e *kernel.Entity) (wire.Component, bool) {
	ratio, ok := c.w.Ratio(e)
	if !ok {
		return wire.Component{}, false
	}
	tone := wire.ToneNeutral
	if phaseOf(c.w, e) == string(kernel.Done) {
		tone = wire.TonePositive
	}
	return wire.Component{
		Type: wire.ComponentProgress, Label: c.progressLabel(t, e), Detail: fmt.Sprintf("%d%%", pctOf(ratio)),
		Ratio: &ratio, Tone: tone,
	}, true
}

// progressLabel is the progress of the card's values ("80页 / 320页") or, without them, the status.
func (c *ctx) progressLabel(t *kernel.Type, e *kernel.Entity) string {
	if pr := t.CardOrDefault().Progress; pr != nil {
		// A share with no total would only repeat the percentage beside it, so it is named instead.
		if pr.Over == "" {
			if p, ok := c.sp.Field(t, pr.Of); ok {
				return p.Name
			}
		} else if of := c.w.Text(e, pr.Of); of != "" {
			return cut(of+" / "+c.w.Text(e, pr.Over), maxText)
		}
	}
	if e.Status != "" {
		return e.Status
	}
	return e.Title
}

// feedSections is the feed: newest first, one section per day in the request's time zone. A row shows the time; a
// long lead follows it as a secondary text.
func (c *ctx) feedSections(t *kernel.Type, view kernel.View, pg page) []wire.Section {
	key := c.feedKey(t, view)
	now := c.now.In(c.loc)
	today, yesterday := now.Format(time.DateOnly), now.AddDate(0, 0, -1).Format(time.DateOnly)
	out := []wire.Section{}
	last, started := "", false
	for _, e := range pg.entities() {
		day, at, hasTime := c.dayOf(e, key)
		if !started || day != last {
			out = append(out, wire.Section{Title: dayTitle(day, today, yesterday), Items: []wire.Component{}})
			last, started = day, true
		}
		s := &out[len(out)-1]
		r := c.row(e, false)
		if hasTime {
			r.Value = at.Format("15:04")
		}
		if lead := c.lead(t, e); utf8.RuneCountInString(lead) > longLead {
			r.Subtitle = c.pillText(t, e)
			s.Items = append(s.Items, r, wire.Component{
				Type: wire.ComponentText, Text: cut(lead, feedText), Style: wire.TextStyleSecondary,
			})
			continue
		}
		s.Items = append(s.Items, r)
	}
	return out
}

// dayOf is the day an entity falls on by a date or a time field, and the time when the field has one.
func (c *ctx) dayOf(e *kernel.Entity, key string) (string, time.Time, bool) {
	s, _ := c.w.Value(e, key).(string)
	if at, err := time.Parse(time.RFC3339, s); err == nil {
		at = at.In(c.loc)
		return at.Format(time.DateOnly), at, true
	}
	if d, err := time.ParseInLocation(time.DateOnly, s, c.loc); err == nil {
		return d.Format(time.DateOnly), d, false
	}
	return "", time.Time{}, false
}

// dayTitle names a day: today, yesterday, or its date; "未设置" for no day.
func dayTitle(day, today, yesterday string) string {
	switch day {
	case "":
		return unsetLabel
	case today:
		return "今天"
	case yesterday:
		return "昨天"
	}
	return day
}

// emptyRows is what a type's tab shows with no entity: a search that found nothing, or the first entity to add.
func (c *ctx) emptyRows(t *kernel.Type) wire.Section {
	if c.st.Q != "" {
		return wire.Section{Items: []wire.Component{{
			Type: wire.ComponentEmpty, Title: "没有找到「" + c.st.Q + "」",
		}}}
	}
	return wire.Section{Items: []wire.Component{{
		Type: wire.ComponentEmpty, Title: "还没有" + t.Name,
		Button: &wire.EmptyButton{Title: "新建" + t.Name, Action: wire.Action{ID: "fold:new"}},
	}}}
}

// quickAdd is the fold that adds an entity: one title and a primary create. The entity starts at the first stage.
func (c *ctx) quickAdd(t *kernel.Type) wire.Section {
	return wire.Section{Items: []wire.Component{
		{Type: wire.ComponentField, Name: "title", Label: "标题", Kind: wire.FieldKindText, Placeholder: "写下一条" + t.Name},
		button("创建", &wire.Action{ID: "add", Submit: true}, wire.ButtonRoleNormal),
	}}
}

// segmented switches the views of a type.
func segmented(views []kernel.View, vi int) wire.Component {
	choices := []wire.Choice{}
	for i, v := range views {
		choices = append(choices, wire.Choice{Value: strconv.Itoa(i), Title: v.Name})
	}
	return wire.Component{
		Type: wire.ComponentSegmented, Name: "view", Value: strconv.Itoa(vi), Choices: choices,
		Action: &wire.Action{ID: "view"},
	}
}

// rows is the row projection of entities: one row each, with the type's icon when icon is set.
func (c *ctx) rows(set []*kernel.Entity, icon bool) []wire.Component {
	out := []wire.Component{}
	for _, e := range set {
		out = append(out, c.row(e, icon))
	}
	return out
}

// row is the projection of an entity that every list shares. The subtitle is the lead of the type's card, or else its
// pills; the value is the pills when a lead took the subtitle. The badge is the status, toned by its phase.
func (c *ctx) row(e *kernel.Entity, icon bool) wire.Component {
	out := wire.Component{
		Type: wire.ComponentRow, Title: cut(e.Title, maxTitle),
		Action: ext.Open("open:"+e.ID, PageUI, state{S: c.sp.ID, E: e.ID}, e.Title),
	}
	t := typeIn(c.sp, e.Type)
	if t == nil {
		return out
	}
	if lead := c.lead(t, e); lead != "" {
		out.Subtitle = lead
		out.Value = c.pillText(t, e)
	} else {
		out.Subtitle = c.pillText(t, e)
	}
	if e.Status != "" {
		out.Badge = &wire.Badge{Text: e.Status, Tone: toneOf(t, e.Status)}
	}
	if icon {
		out.Icon = iconName(t.Icon)
	}
	return out
}

// lead is the text of the type's card lead for an entity, "" for none.
func (c *ctx) lead(t *kernel.Type, e *kernel.Entity) string {
	lead := t.CardOrDefault().Lead
	if lead == "" {
		return ""
	}
	return cut(c.w.Text(e, lead), maxText)
}

// pillText joins the non-empty texts of the type's card pills with " · ".
func (c *ctx) pillText(t *kernel.Type, e *kernel.Entity) string {
	var parts []string
	for _, p := range t.CardOrDefault().Pills {
		if s := c.w.Text(e, p); s != "" {
			parts = append(parts, c.named(t, p, s))
		}
	}
	return cut(strings.Join(parts, " · "), maxText)
}

// named puts the property's name before a value that says nothing alone: a bare number or a yes/no. A value with a
// unit, an option, a date or a record's title reads well without it.
func (c *ctx) named(t *kernel.Type, key, text string) string {
	p, ok := c.sp.Field(t, key)
	k, known := kernel.KindOf(p.Kind)
	if !ok || !known || k.Num == nil || k.Refs != nil || p.Unit != "" || strings.HasSuffix(text, "%") {
		return text
	}
	return p.Name + " " + text
}

// toneOf tones a status by its phase: a doing status is a caution, a done one positive, the rest neutral.
func toneOf(t *kernel.Type, status string) string {
	st, ok := t.Stage(status)
	if !ok {
		return wire.ToneNeutral
	}
	switch st.Phase {
	case kernel.Doing:
		return wire.ToneCaution
	case kernel.Done:
		return wire.TonePositive
	}
	return wire.ToneNeutral
}

// groupName is the name of a group; the unset group is named 未设置.
func groupName(g kernel.Group) string {
	if g.Key == "" {
		return unsetLabel
	}
	return g.Name
}

// iconName is the icon when the apps draw it, else "": a type's icon is an opaque name in the base.
func iconName(name string) string {
	if slices.Contains(wire.NavIcons, name) {
		return name
	}
	return ""
}

// cut keeps at most n runes of s.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// textOr is s, or the fallback when s is empty.
func textOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// dashIfEmpty is the table's text for an unset cell.
func dashIfEmpty(s string) string { return textOr(s, "—") }

// pctOf is a ratio in 0..1 as a whole percent.
func pctOf(ratio float64) int { return int(math.Round(ratio * 100)) }
