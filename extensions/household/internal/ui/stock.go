package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
)

// emptyPlacesMost is how many places with no items the stock tab lists; the rest are counted in a line.
const emptyPlacesMost = 20

// placeGroup is one section of the stock tab: a place, or the items with none.
type placeGroup struct {
	title string
	items []model.Item
}

// renderStock is the 库存 tab: the items under their places, the places in their order and 未放置 last. The search
// text and the category the state names narrow the items; a place with nothing left is not shown then. The rows stop
// at the budget of the tab, and the places left out are said in a line; empty places are listed up to emptyPlacesMost.
func (c *ctx) renderStock() []wire.Section {
	if c.st.G != "" && !slices.Contains(model.Categories, c.st.G) {
		c.st.G = ""
	}
	if len(c.home.Items) == 0 {
		return []wire.Section{{Items: []wire.Component{quiet("还没有物品，先添加一件")}}, c.stockButtons()}
	}
	findings := map[string]rules.Finding{}
	for _, f := range rules.Findings(c.home, c.today) {
		findings[f.Item.ID] = f
	}
	filtering := c.st.Q != "" || c.st.G != ""
	b := newBudget()
	out := []wire.Section{}
	matched := 0    // items under the filters
	notShown := 0   // items of the places the row budget left out
	emptyShown := 0 // places with no items listed so far
	emptyLeft := 0  // places with no items the cap left out
	for _, g := range c.placeGroups() {
		hits := slices.DeleteFunc(slices.Clone(g.items), func(it model.Item) bool { return !c.inStock(it) })
		matched += len(hits)
		if len(hits) == 0 {
			if filtering || g.title == unsetText {
				continue
			}
			if emptyShown >= emptyPlacesMost {
				emptyLeft++
				continue
			}
			emptyShown++
			out = append(out, wire.Section{Title: g.title, Items: []wire.Component{quiet("没有物品")}})
			continue
		}
		if b.left == 0 {
			notShown += len(hits)
			continue
		}
		rows := takeRows(b, hits, func(it model.Item) wire.Component { return stockRow(it, findings, c.today) })
		notShown += len(hits) - len(rows)
		out = append(out, wire.Section{Title: g.title, Footer: footerCut(len(hits), len(rows)), Items: rows})
	}
	if matched == 0 {
		out = append(out, wire.Section{Items: []wire.Component{quiet("没有找到符合的物品")}})
	}
	if notShown > 0 {
		out = append(out, wire.Section{Items: []wire.Component{quiet(
			fmt.Sprintf("还有 %d 件物品没有显示，请用搜索或类别筛选缩小范围", notShown))}})
	}
	if emptyLeft > 0 {
		out = append(out, wire.Section{Items: []wire.Component{quiet(
			fmt.Sprintf("还有 %d 个没有物品的位置没有列出", emptyLeft))}})
	}
	return append(out, c.stockButtons())
}

// placeGroups are the items grouped by their place, in the places' order, then the items with no place (or a place
// that is gone). Within a group the items are ordered by title.
func (c *ctx) placeGroups() []placeGroup {
	groups := make([]placeGroup, 0, len(c.home.Places)+1)
	index := map[string]int{}
	for _, p := range c.home.Places {
		index[p.ID] = len(groups)
		groups = append(groups, placeGroup{title: p.Title})
	}
	unset := placeGroup{title: unsetText}
	for _, it := range c.home.Items {
		if i, ok := index[it.PlaceID]; ok {
			groups[i].items = append(groups[i].items, it)
		} else {
			unset.items = append(unset.items, it)
		}
	}
	for i := range groups {
		sortItems(groups[i].items)
	}
	sortItems(unset.items)
	return append(groups, unset)
}

// sortItems orders items by title, then by id.
func sortItems(items []model.Item) {
	slices.SortFunc(items, func(a, b model.Item) int {
		return cmp.Or(strings.Compare(a.Title, b.Title), strings.Compare(a.ID, b.ID))
	})
}

// inStock reports whether an item is shown under the search text and the category of the state.
func (c *ctx) inStock(it model.Item) bool {
	if c.st.G != "" && it.Category != c.st.G {
		return false
	}
	return c.st.Q == "" || strings.Contains(strings.ToLower(it.Title), strings.ToLower(c.st.Q))
}

// stockRow is an item: its estimate and expiry, its finding as the badge when it has one, and the actions 用掉 1 (for an
// item tracked by quantity), 编辑 and 删除.
func stockRow(it model.Item, findings map[string]rules.Finding, today model.Day) wire.Component {
	acts := []wire.RowAction{}
	if it.HasQty {
		acts = append(acts, wire.RowAction{Title: "用掉 1", Swipe: true, Action: wire.Action{ID: "use:" + it.ID}})
	}
	acts = append(acts, editRow(it.ID, it.Title), deleteRow(it.ID, it.Title))
	row := wire.Component{
		Type: wire.ComponentRow, Title: cut(it.Title, maxTitle),
		Subtitle: stockSubtitle(it, rules.Estimate(it, today)),
		Action:   editAction(it.ID, it.Title), Actions: acts,
	}
	if f, ok := findings[it.ID]; ok {
		row.Badge = &wire.Badge{Text: f.Kind.Label(), Tone: kindTone(f.Kind)}
	}
	return row
}

// stockSubtitle is the line under an item: its estimated quantity with the unit, and its expiry date.
func stockSubtitle(it model.Item, est float64) string {
	qty := ""
	if it.HasQty {
		qty = fmt.Sprintf("约 %s%s", num(est), it.Unit)
	}
	exp := ""
	if d := shortDay(it.Expires); d != "" {
		exp = "保质期 " + d
	}
	return join(qty, exp)
}

// stockButtons are the buttons of the stock tab: a new item and a new place.
func (c *ctx) stockButtons() wire.Section {
	return wire.Section{Items: []wire.Component{
		button("添加"+typeName(model.TItem), addAction(model.TItem), wire.ButtonRoleNormal),
		button("添加"+typeName(model.TPlace), addAction(model.TPlace), wire.ButtonRoleNormal),
	}}
}
