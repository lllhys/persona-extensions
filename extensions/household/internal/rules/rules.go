// Package rules computes what the household needs from its records: the estimated quantities, the findings per item,
// the chores due and the summary. It is pure: the same records and the same day always give the same result, and
// nothing derived is stored. Meal suggestions are in meals.go.
package rules

import (
	"cmp"
	"math"
	"slices"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
)

// Kind is the kind of a finding. The constants are in order of severity, most severe first.
type Kind string

// The finding kinds.
const (
	Expired  Kind = "expired"
	Out      Kind = "out"
	Low      Kind = "low"
	Runout   Kind = "runout"
	Expiring Kind = "expiring"
)

// kinds lists the kinds in order of severity; a kind's rank is its index.
var kinds = []Kind{Expired, Out, Low, Runout, Expiring}

// Label is the Chinese name of the kind, as shown to the user.
func (k Kind) Label() string {
	switch k {
	case Expired:
		return "已过期"
	case Out:
		return "已用完"
	case Low:
		return "低库存"
	case Runout:
		return "用量预警"
	case Expiring:
		return "即将过期"
	}
	return ""
}

// Reason is the purchase reason for the kind, or "" for Expiring, which is not bought for.
func (k Kind) Reason() string {
	switch k {
	case Expired:
		return model.ReasonExpired
	case Out:
		return model.ReasonOut
	case Low:
		return model.ReasonLow
	case Runout:
		return model.ReasonRunout
	}
	return ""
}

// Buyable reports whether the kind calls for a purchase. Expiring does not: the item is to be used first.
func (k Kind) Buyable() bool {
	return k != Expiring
}

func (k Kind) rank() int {
	return slices.Index(kinds, k)
}

// Lead is the number of days of warning for the item: its own lead when set, else 7 for baby supplies and 3 for
// everything else.
func Lead(it model.Item) int {
	if it.Lead > 0 {
		return int(it.Lead)
	}
	if it.Category == model.CategoryBaby {
		return 7
	}
	return 3
}

// Estimate is the estimated quantity of the item on the day. An item with daily use is assumed to lose that much per
// day since it was counted, or since it last changed when it was never counted; the estimate never goes below zero
// and never counts days before the count. An item without a quantity has an estimate of 0; it is not tracked by
// quantity.
func Estimate(it model.Item, today model.Day) float64 {
	if !it.HasQty {
		return 0
	}
	est := it.Qty
	if it.Daily > 0 {
		since := it.Counted
		if since == "" {
			since = it.Updated
		}
		est -= it.Daily * float64(max(0, today.Sub(since)))
	}
	return max(0, est)
}

// Finding is the most severe problem with one item.
type Finding struct {
	Item model.Item
	Kind Kind
	Est  float64
	// Days is the days past expiry for Expired (at least 1), the days until expiry for Expiring (at least 0), the
	// whole days of stock left for Runout, and 0 otherwise.
	Days int
	// OpenBuy reports that a 待买 purchase already points at the item.
	OpenBuy bool
}

// Findings returns the finding of every item that has one, at most one per item, sorted by severity, then title,
// then id.
func Findings(h *model.Home, today model.Day) []Finding {
	open := map[string]bool{}
	for _, b := range h.Buys {
		if b.Status == model.BuyTodo && b.ItemID != "" {
			open[b.ItemID] = true
		}
	}
	var fs []Finding
	for _, it := range h.Items {
		f, ok := finding(it, today)
		if !ok {
			continue
		}
		f.OpenBuy = open[it.ID]
		fs = append(fs, f)
	}
	slices.SortFunc(fs, func(a, b Finding) int {
		return cmp.Or(
			cmp.Compare(a.Kind.rank(), b.Kind.rank()),
			cmp.Compare(a.Item.Title, b.Item.Title),
			cmp.Compare(a.Item.ID, b.Item.ID),
		)
	})
	return fs
}

// finding is the most severe finding of one item, if it has one.
func finding(it model.Item, today model.Day) (Finding, bool) {
	est := Estimate(it, today)
	f := Finding{Item: it, Est: est}
	lead := Lead(it)
	switch {
	case it.Expires != "" && it.Expires < today:
		f.Kind, f.Days = Expired, today.Sub(it.Expires)
	case it.HasQty && est == 0:
		f.Kind = Out
	case it.HasQty && it.HasMin && est <= it.Min:
		f.Kind = Low
	case it.HasQty && it.Daily > 0 && est/it.Daily <= float64(lead):
		f.Kind, f.Days = Runout, int(math.Floor(est/it.Daily))
	case it.Expires != "" && it.Expires <= today.Add(lead):
		f.Kind, f.Days = Expiring, it.Expires.Sub(today)
	default:
		return Finding{}, false
	}
	return f, true
}

// Restock returns the findings that call for a purchase and have no open 待买 purchase yet, in Findings order.
func Restock(h *model.Home, today model.Day) []Finding {
	var fs []Finding
	for _, f := range Findings(h, today) {
		if f.Kind.Buyable() && !f.OpenBuy {
			fs = append(fs, f)
		}
	}
	return fs
}

// Due is a chore that is due.
type Due struct {
	Chore model.Chore
	// Overdue is the days past its next date; 0 means due today.
	Overdue int
}

// ChoresDue returns the chores whose next date is today or earlier, most overdue first, then by title and id.
func ChoresDue(h *model.Home, today model.Day) []Due {
	var due []Due
	for _, c := range h.Chores {
		if c.Next == "" || c.Next > today {
			continue
		}
		due = append(due, Due{Chore: c, Overdue: today.Sub(c.Next)})
	}
	slices.SortFunc(due, func(a, b Due) int {
		return cmp.Or(
			cmp.Compare(b.Overdue, a.Overdue),
			cmp.Compare(a.Chore.Title, b.Chore.Title),
			cmp.Compare(a.Chore.ID, b.Chore.ID),
		)
	})
	return due
}

// Summary counts what the household needs, for the badge, the notification and the overview.
type Summary struct {
	// Expired, Out, Low, Runout and Expiring count the findings of each kind.
	Expired, Out, Low, Runout, Expiring int
	// OpenBuys counts the 待买 purchases.
	OpenBuys int
	// ChoresDue counts the chores due today or earlier.
	ChoresDue int
	// Alerts is the number of buyable findings plus the chores due.
	Alerts int
	// Tone is "danger" when something is expired or used up, "caution" when there are alerts, and "" otherwise.
	Tone string
}

// Summarize counts the findings, open purchases and chores due on the day.
func Summarize(h *model.Home, today model.Day) Summary {
	var s Summary
	for _, f := range Findings(h, today) {
		switch f.Kind {
		case Expired:
			s.Expired++
		case Out:
			s.Out++
		case Low:
			s.Low++
		case Runout:
			s.Runout++
		case Expiring:
			s.Expiring++
		}
		if f.Kind.Buyable() {
			s.Alerts++
		}
	}
	for _, b := range h.Buys {
		if b.Status == model.BuyTodo {
			s.OpenBuys++
		}
	}
	s.ChoresDue = len(ChoresDue(h, today))
	s.Alerts += s.ChoresDue
	switch {
	case s.Expired+s.Out > 0:
		s.Tone = "danger"
	case s.Alerts > 0:
		s.Tone = "caution"
	}
	return s
}
