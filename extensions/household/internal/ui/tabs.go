package ui

import (
	"fmt"
	"strconv"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
)

// The tabs of the nav page. The overview's state is the empty tab; its value in the tab bar is overTab.
const (
	overTab  = "over"
	stockTab = "stock"
	buyTab   = "buy"
	choreTab = "chore"
	mealTab  = "meal"
)

// tabTitles are the tabs of the nav page, in order.
var tabTitles = []struct{ value, title string }{
	{overTab, "总览"}, {stockTab, "库存"}, {buyTab, "采购"}, {choreTab, "家务"}, {mealTab, "餐食"},
}

// page is the nav page: the tab bar and the body of the tab the state names.
func (c *ctx) page() *wire.View {
	body := c.body()
	v := &wire.View{Sections: body, Tabs: c.tabs()}
	if c.st.T == stockTab {
		v.Search = &wire.Search{Placeholder: "搜索物品", Value: c.st.Q, Action: wire.Action{ID: "search"}}
		v.Filter = c.categoryFilter()
	}
	return v
}

// categoryFilter is the drop-down of the stock tab: all categories, or the one the state names.
func (c *ctx) categoryFilter() *wire.Filter {
	choices := []wire.Choice{{Value: choicePrefix, Title: "全部类别"}}
	for _, cat := range model.Categories {
		choices = append(choices, wire.Choice{Value: choicePrefix + cat, Title: cat})
	}
	return &wire.Filter{Name: "category", Title: "类别", Choices: choices, Value: choicePrefix + c.st.G,
		Action: wire.Action{ID: "filter"}}
}

// body is the sections of the tab the state names; an unknown tab falls back to the overview.
func (c *ctx) body() []wire.Section {
	switch c.st.T {
	case stockTab:
		return c.renderStock()
	case buyTab:
		return c.renderPurchases()
	case choreTab:
		return c.renderChores()
	case mealTab:
		return c.renderMeals()
	}
	c.st.T = ""
	return c.overviewSections()
}

// tabs is the tab bar: one tab per page, with a badge for the purchases still open and the chores due.
func (c *ctx) tabs() *wire.Tabs {
	items := make([]wire.Tab, 0, len(tabTitles))
	for _, t := range tabTitles {
		items = append(items, wire.Tab{Value: t.value, Title: t.title, Badge: c.tabBadge(t.value)})
	}
	return &wire.Tabs{Name: "tab", Value: tabValue(c.st.T), Items: items, Action: wire.Action{ID: "tab"}}
}

// tabValue is the value of the tab the state names in the tab bar.
func tabValue(t string) string {
	if t == "" {
		return overTab
	}
	return t
}

// tabBadge counts what the tab is waiting for, or "" for none.
func (c *ctx) tabBadge(tab string) string {
	n := 0
	switch tab {
	case buyTab:
		for _, b := range c.home.Buys {
			if b.Status == model.BuyTodo {
				n++
			}
		}
	case choreTab:
		n = len(rules.ChoresDue(c.home, c.today))
	}
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// actTab switches the tab. Searching and filtering start again on the new tab.
func actTab(c *ctx, arg string) (*wire.ActionResult, error) {
	key := c.pick(arg, "tab")
	if key == overTab {
		c.st = state{}
		return nil, nil
	}
	if !isTab(key) {
		return nil, fmt.Errorf("没有这个页面「%s」", key)
	}
	c.st = state{T: key}
	return nil, nil
}

// isTab reports whether key names a tab other than the overview.
func isTab(key string) bool {
	switch key {
	case stockTab, buyTab, choreTab, mealTab:
		return true
	}
	return false
}

// rowBudget is how many rows a tab draws in all. Rows with actions are at most 100 on a page, and a page at most 300
// components; a tab stops at this budget and its footers say how many rows were left out.
type rowBudget struct{ left int }

const tabRowsMost = 100

// newBudget is the budget of one tab.
func newBudget() *rowBudget { return &rowBudget{left: tabRowsMost} }

// take reports whether one more row fits in the budget, and takes it when it does.
func (b *rowBudget) take() bool {
	if b.left <= 0 {
		return false
	}
	b.left--
	return true
}

// takeRows draws a row for each of the records while the budget lasts.
func takeRows[T any](b *rowBudget, xs []T, row func(T) wire.Component) []wire.Component {
	var out []wire.Component
	for _, x := range xs {
		if !b.take() {
			break
		}
		out = append(out, row(x))
	}
	return out
}

// footerCut says how many rows a section left out, "" when it shows them all.
func footerCut(total, shown int) string {
	if total <= shown {
		return ""
	}
	return fmt.Sprintf("共 %d 项，只显示前 %d 项", total, shown)
}
