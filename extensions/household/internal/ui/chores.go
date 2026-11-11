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

// renderChores is the 家务 tab: the chores due first, most overdue first, then the others by their next day, with 完成,
// 编辑 and 删除 on each row, and the button that adds a chore.
func (c *ctx) renderChores() []wire.Section {
	b := newBudget()
	due := rules.ChoresDue(c.home, c.today)
	isDue := map[string]bool{}
	for _, d := range due {
		isDue[d.Chore.ID] = true
	}
	var later []model.Chore
	for _, ch := range c.home.Chores {
		if !isDue[ch.ID] {
			later = append(later, ch)
		}
	}
	slices.SortFunc(later, compareLater)

	dueRows := takeRows(b, due, func(d rules.Due) wire.Component {
		return choreTabRow(d.Chore, dueBadge(d))
	})
	laterRows := takeRows(b, later, func(ch model.Chore) wire.Component {
		return choreTabRow(ch, c.laterBadge(ch))
	})
	return []wire.Section{
		{Title: "到期", Footer: footerCut(len(due), len(dueRows)), Items: orQuiet(dueRows, "没有到期的家务")},
		{Title: "以后", Footer: footerCut(len(later), len(laterRows)), Items: orQuiet(laterRows, "没有其他家务")},
		{Items: []wire.Component{button("添加"+typeName(model.TChore), addAction(model.TChore), wire.ButtonRoleNormal)}},
	}
}

// compareLater orders the chores not yet due: by their next day, the ones with none last, then by title.
func compareLater(a, b model.Chore) int {
	switch {
	case a.Next == "" && b.Next != "":
		return 1
	case a.Next != "" && b.Next == "":
		return -1
	}
	return cmp.Or(strings.Compare(string(a.Next), string(b.Next)), strings.Compare(a.Title, b.Title), strings.Compare(a.ID, b.ID))
}

// dueBadge says how late a chore due is: 今天, or the days it is overdue.
func dueBadge(d rules.Due) *wire.Badge {
	if d.Overdue > 0 {
		return &wire.Badge{Text: fmt.Sprintf("逾期 %d 天", d.Overdue), Tone: wire.ToneDanger}
	}
	return &wire.Badge{Text: "今天", Tone: wire.ToneCaution}
}

// laterBadge says in how many days a chore falls due, "" when it has no next day.
func (c *ctx) laterBadge(ch model.Chore) *wire.Badge {
	if ch.Next == "" {
		return nil
	}
	return &wire.Badge{Text: fmt.Sprintf("%d 天后", ch.Next.Sub(c.today)), Tone: wire.ToneNeutral}
}

// choreTabRow is a chore: its period, who does it and its next day, with the actions 完成, 编辑 and 删除.
func choreTabRow(ch model.Chore, badge *wire.Badge) wire.Component {
	return wire.Component{
		Type: wire.ComponentRow, Title: cut(ch.Title, maxTitle),
		Subtitle: choreLine(ch), Badge: badge, Action: editAction(ch.ID, ch.Title),
		Actions: []wire.RowAction{
			{Title: "完成", Swipe: true, Action: wire.Action{ID: "chore:" + ch.ID}},
			editRow(ch.ID, ch.Title),
			deleteRow(ch.ID, ch.Title),
		},
	}
}
