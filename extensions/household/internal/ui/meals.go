package ui

import (
	"cmp"
	"slices"
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
)

// renderMeals is the 餐食 tab: the meal ideas for the ingredients at hand, the planned meals by date, and the recipes.
// Each idea and each planned meal has its actions; the add buttons for recipes and meals are at the end.
func (c *ctx) renderMeals() []wire.Section {
	b := newBudget()
	ideas := rules.MealIdeas(c.home, c.today, tabRowsMost)
	ideaRows := takeRows(b, ideas, ideaRow)

	var planned []model.Meal
	for _, m := range c.home.Meals {
		if m.Status == model.MealPlan {
			planned = append(planned, m)
		}
	}
	slices.SortFunc(planned, func(x, y model.Meal) int {
		return cmp.Or(strings.Compare(string(x.Date), string(y.Date)), strings.Compare(x.Title, y.Title), strings.Compare(x.ID, y.ID))
	})
	plannedRows := takeRows(b, planned, plannedRow)
	recipeRows := takeRows(b, c.home.Recipes, recipeRow)

	return []wire.Section{
		{Title: "今天吃什么", Footer: footerCut(len(ideas), len(ideaRows)),
			Items: orQuiet(ideaRows, "还没有可以做的菜，先添加菜谱")},
		{Title: "计划", Footer: footerCut(len(planned), len(plannedRows)),
			Items: orQuiet(plannedRows, "没有计划的餐食")},
		{Title: "菜谱", Footer: footerCut(len(c.home.Recipes), len(recipeRows)),
			Items: orQuiet(recipeRows, "还没有菜谱")},
		{Items: []wire.Component{
			button("添加"+typeName(model.TRecipe), addAction(model.TRecipe), wire.ButtonRoleNormal),
			button("添加"+typeName(model.TMeal), addAction(model.TMeal), wire.ButtonRoleNormal),
		}},
	}
}

// plannedRow is a planned meal: its date and slot, and the actions 已做 and 取消, which close it.
func plannedRow(m model.Meal) wire.Component {
	return wire.Component{
		Type: wire.ComponentRow, Title: cut(m.Title, maxTitle),
		Subtitle: join(shortDay(m.Date), m.Slot), Action: editAction(m.ID, m.Title),
		Actions: []wire.RowAction{
			{Title: "已做", Swipe: true, Action: wire.Action{ID: "mealdone:" + m.ID}},
			{Title: "取消", Swipe: true, Action: wire.Action{ID: "mealcancel:" + m.ID}},
		},
	}
}

// recipeRow is a recipe: how many ingredients it lists and how long it takes.
func recipeRow(r model.Recipe) wire.Component {
	minutes := ""
	if r.Minutes > 0 {
		minutes = num(r.Minutes) + " 分钟"
	}
	return wire.Component{
		Type: wire.ComponentRow, Title: cut(r.Title, maxTitle),
		Subtitle: join(countOf(len(r.Needs), "样食材"), minutes), Action: editAction(r.ID, r.Title),
	}
}
