package ui

import (
	"fmt"
	"slices"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
)

// recentBought is how many bought purchases the tab lists.
const recentBought = 10

// renderPurchases is the 采购 tab: the open purchases, each with 已买 and 不买, above them the one-tap restock and the add
// button; then the purchases bought lately, newest first.
func (c *ctx) renderPurchases() []wire.Section {
	b := newBudget()
	var open, bought []model.Buy
	for _, p := range c.home.Buys {
		switch p.Status {
		case model.BuyTodo:
			open = append(open, p)
		case model.BuyDone:
			bought = append(bought, p)
		}
	}
	var head []wire.Component
	if r, ok := c.restockButton(); ok {
		head = append(head, r)
	}
	head = append(head, button("添加"+typeName(model.TBuy), addAction(model.TBuy), wire.ButtonRoleNormal))
	rows := takeRows(b, open, func(p model.Buy) wire.Component { return buyRow(p, true) })
	openItems := append(head, rows...)
	if len(rows) == 0 {
		openItems = append(openItems, quiet("没有待买的东西"))
	}
	out := []wire.Section{{Title: "待买", Footer: footerCut(len(open), len(rows)), Items: openItems}}

	recent := latestBought(bought)
	recentRows := takeRows(b, recent, func(p model.Buy) wire.Component { return buyRow(p, false) })
	return append(out, wire.Section{Title: "最近已买", Items: orQuiet(recentRows, "还没有买过的东西")})
}

// latestBought are the purchases bought last, the latest first: the time a purchase was bought is the time its record
// last changed. Among records changed at the same time the newer record comes first.
func latestBought(bought []model.Buy) []model.Buy {
	sorted := slices.Clone(bought)
	slices.Reverse(sorted)
	slices.SortStableFunc(sorted, func(a, b model.Buy) int { return b.Updated.Compare(a.Updated) })
	return sorted[:min(recentBought, len(sorted))]
}

// restockButton is 一键补货 with the number of purchases it will create; false when there is nothing to create.
func (c *ctx) restockButton() (wire.Component, bool) {
	n := len(rules.Restock(c.home, c.today))
	if n == 0 {
		return wire.Component{}, false
	}
	return button(fmt.Sprintf("一键补货（%d 项）", n), &wire.Action{ID: "restock"}, wire.ButtonRoleNormal), true
}

// buyRow is a purchase: its amount and reason, and for an open one the actions 已买 and 不买, which close it.
func buyRow(p model.Buy, open bool) wire.Component {
	row := wire.Component{
		Type: wire.ComponentRow, Title: cut(p.Title, maxTitle),
		Subtitle: buySubtitle(p), Action: editAction(p.ID, p.Title),
	}
	if open {
		row.Actions = []wire.RowAction{
			{Title: "已买", Swipe: true, Action: wire.Action{ID: "buy:" + p.ID}},
			{Title: "不买", Swipe: true, Action: wire.Action{ID: "skip:" + p.ID}},
		}
	}
	return row
}

// buySubtitle is the line under a purchase: how much, and why it is bought.
func buySubtitle(p model.Buy) string {
	amount := ""
	if p.Amount > 0 {
		amount = fmt.Sprintf("%s%s", num(p.Amount), p.Unit)
	}
	return join(amount, p.Reason)
}
