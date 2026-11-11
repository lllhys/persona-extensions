package ui

import (
	"strconv"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
)

// cardItems is the most components the overview card has: one stat grid and the rows after it.
const cardItems = 12

// card is the overview card: three totals, then the most urgent records, each opening its form. A card has no actions of
// its own, so its rows carry none.
func (c *ctx) card() *wire.View {
	if c.empty() {
		return &wire.View{Sections: []wire.Section{{Items: []wire.Component{{
			Type: wire.ComponentEmpty, Title: "还没有记录", Detail: "在家庭管家里添加物品或家务",
		}}}}}
	}
	sum := rules.Summarize(c.home, c.today)
	v := &wire.View{Sections: []wire.Section{{Items: []wire.Component{statGrid([]wire.Stat{
		{Label: "需要处理", Value: strconv.Itoa(sum.Alerts), Tone: sum.Tone},
		{Label: "家务到期", Value: strconv.Itoa(sum.ChoresDue), Tone: cautionIf(sum.ChoresDue)},
		{Label: "即将过期", Value: strconv.Itoa(sum.Expiring), Tone: cautionIf(sum.Expiring)},
	})}}}}
	if rows := c.cardRows(cardItems - 1); len(rows) > 0 {
		v.Sections = append(v.Sections, wire.Section{Title: "需要处理", Items: rows})
	}
	return v
}

// cardRows are up to limit rows, most urgent first: the findings that call for a purchase, the chores due, then the
// items about to expire.
func (c *ctx) cardRows(limit int) []wire.Component {
	findings := rules.Findings(c.home, c.today)
	var rows []wire.Component
	for _, f := range findings {
		if f.Kind.Buyable() {
			rows = append(rows, c.findingRow(f))
		}
	}
	for _, d := range rules.ChoresDue(c.home, c.today) {
		rows = append(rows, c.choreRow(d))
	}
	for _, f := range findings {
		if f.Kind == rules.Expiring {
			rows = append(rows, c.expiringRow(f))
		}
	}
	for i := range rows {
		rows[i].Actions = nil
	}
	return slicesOf(rows, limit)
}
