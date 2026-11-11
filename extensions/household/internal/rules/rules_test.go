package rules

import (
	"reflect"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
)

const today model.Day = "2026-11-10"

// tracked is an item with a quantity.
func tracked(id string, qty float64) model.Item {
	return model.Item{ID: id, Title: id, Qty: qty, HasQty: true}
}

// home is a home with the given items and purchases.
func home(items []model.Item, buys ...model.Buy) *model.Home {
	return &model.Home{Items: items, Buys: buys}
}

func TestKindLabelReasonBuyable(t *testing.T) {
	tests := []struct {
		kind    Kind
		label   string
		reason  string
		buyable bool
	}{
		{Expired, "已过期", model.ReasonExpired, true},
		{Out, "已用完", model.ReasonOut, true},
		{Low, "低库存", model.ReasonLow, true},
		{Runout, "用量预警", model.ReasonRunout, true},
		{Expiring, "即将过期", "", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			if got := tt.kind.Label(); got != tt.label {
				t.Errorf("Label() = %q, want %q", got, tt.label)
			}
			if got := tt.kind.Reason(); got != tt.reason {
				t.Errorf("Reason() = %q, want %q", got, tt.reason)
			}
			if got := tt.kind.Buyable(); got != tt.buyable {
				t.Errorf("Buyable() = %v, want %v", got, tt.buyable)
			}
		})
	}
}

func TestLead(t *testing.T) {
	tests := []struct {
		name string
		item model.Item
		want int
	}{
		{"default non-baby", model.Item{Category: "食材"}, 3},
		{"default empty category", model.Item{}, 3},
		{"default baby", model.Item{Category: model.CategoryBaby}, 7},
		{"set overrides default", model.Item{Category: "食材", Lead: 5}, 5},
		{"set overrides baby default", model.Item{Category: model.CategoryBaby, Lead: 2}, 2},
		{"negative is unset", model.Item{Category: "食材", Lead: -1}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Lead(tt.item); got != tt.want {
				t.Errorf("Lead() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEstimate(t *testing.T) {
	tests := []struct {
		name string
		item model.Item
		want float64
	}{
		{"no daily is the quantity", tracked("a", 10), 10},
		{"daily since counted", model.Item{Qty: 10, HasQty: true, Daily: 2, Counted: today.Add(-3)}, 4},
		{"daily clamps at zero", model.Item{Qty: 10, HasQty: true, Daily: 2, Counted: today.Add(-10)}, 0},
		{"daily from updated when counted unset", model.Item{Qty: 5, HasQty: true, Daily: 1, Updated: today.Add(-2)}, 3},
		{"counted wins over updated", model.Item{Qty: 5, HasQty: true, Daily: 1, Counted: today.Add(-1), Updated: today.Add(-5)}, 4},
		{"no dates means no days", model.Item{Qty: 5, HasQty: true, Daily: 1}, 5},
		{"counted in the future counts zero days", model.Item{Qty: 5, HasQty: true, Daily: 1, Counted: today.Add(2)}, 5},
		{"fractional daily", model.Item{Qty: 2, HasQty: true, Daily: 0.5, Counted: today.Add(-3)}, 0.5},
		{"unset qty is zero", model.Item{Daily: 1, Counted: today.Add(-1)}, 0},
		{"zero qty stays zero", tracked("a", 0), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Estimate(tt.item, today); got != tt.want {
				t.Errorf("Estimate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindingsSingleItem(t *testing.T) {
	tests := []struct {
		name     string
		item     model.Item
		buys     []model.Buy
		wantKind Kind // "" for no finding
		wantDays int
		wantEst  float64
		wantOpen bool
	}{
		// expired: expires before today
		{"expired yesterday", model.Item{ID: "a", Title: "a", Expires: today.Add(-1)}, nil, Expired, 1, 0, false},
		{"expired five days ago", model.Item{ID: "a", Title: "a", Expires: today.Add(-5)}, nil, Expired, 5, 0, false},
		{"expires today is expiring", model.Item{ID: "a", Title: "a", Expires: today}, nil, Expiring, 0, 0, false},
		{"expired even with stock", model.Item{ID: "a", Title: "a", Qty: 9, HasQty: true, Expires: today.Add(-1)}, nil, Expired, 1, 9, false},

		// expiring: within lead days, default 3 for non-baby
		{"expiring at lead", model.Item{ID: "a", Title: "a", Expires: today.Add(3)}, nil, Expiring, 3, 0, false},
		{"expiring one day past lead is none", model.Item{ID: "a", Title: "a", Expires: today.Add(4)}, nil, "", 0, 0, false},
		{"expiring with explicit lead", model.Item{ID: "a", Title: "a", Lead: 10, Expires: today.Add(10)}, nil, Expiring, 10, 0, false},
		{"expiring one past explicit lead is none", model.Item{ID: "a", Title: "a", Lead: 10, Expires: today.Add(11)}, nil, "", 0, 0, false},
		{"baby expiring at seven days", model.Item{ID: "a", Title: "a", Category: model.CategoryBaby, Expires: today.Add(7)}, nil, Expiring, 7, 0, false},
		{"baby eight days is none", model.Item{ID: "a", Title: "a", Category: model.CategoryBaby, Expires: today.Add(8)}, nil, "", 0, 0, false},

		// out: quantity set and estimate 0
		{"out with zero quantity", tracked("a", 0), nil, Out, 0, 0, false},
		{"out through daily use", model.Item{ID: "a", Title: "a", Qty: 2, HasQty: true, Daily: 1, Counted: today.Add(-2)}, nil, Out, 0, 0, false},
		{"untracked item with no quantity is not out", model.Item{ID: "a", Title: "a"}, nil, "", 0, 0, false},

		// low: min set and estimate at or below it
		{"low exactly at min", model.Item{ID: "a", Title: "a", Qty: 5, HasQty: true, Min: 5, HasMin: true}, nil, Low, 0, 5, false},
		{"low below min", model.Item{ID: "a", Title: "a", Qty: 4, HasQty: true, Min: 5, HasMin: true}, nil, Low, 0, 4, false},
		{"one above min is none", model.Item{ID: "a", Title: "a", Qty: 6, HasQty: true, Min: 5, HasMin: true}, nil, "", 0, 0, false},
		{"min zero with stock is none", model.Item{ID: "a", Title: "a", Qty: 1, HasQty: true, Min: 0, HasMin: true}, nil, "", 0, 0, false},
		{"min zero at zero stock is out", model.Item{ID: "a", Title: "a", Qty: 0, HasQty: true, Min: 0, HasMin: true}, nil, Out, 0, 0, false},
		{"min without quantity is none", model.Item{ID: "a", Title: "a", Min: 5, HasMin: true}, nil, "", 0, 0, false},
		{"low uses the daily estimate", model.Item{ID: "a", Title: "a", Qty: 10, HasQty: true, Min: 5, HasMin: true, Daily: 1, Counted: today.Add(-5)}, nil, Low, 0, 5, false},

		// runout: daily > 0 and estimate / daily at or below lead
		{"runout of an adult item at lead", model.Item{ID: "a", Title: "a", Qty: 6, HasQty: true, Daily: 2, Counted: today}, nil, Runout, 3, 6, false},
		{"runout one day over lead is none", model.Item{ID: "a", Title: "a", Qty: 7, HasQty: true, Daily: 2, Counted: today}, nil, "", 0, 0, false},
		{"runout days are floored", model.Item{ID: "a", Title: "a", Qty: 7, HasQty: true, Daily: 2, Counted: today, Lead: 4}, nil, Runout, 3, 7, false},
		{"baby runout a week ahead", model.Item{ID: "a", Title: "a", Category: model.CategoryBaby, Qty: 14, HasQty: true, Daily: 2, Counted: today}, nil, Runout, 7, 14, false},
		{"baby just past a week is none", model.Item{ID: "a", Title: "a", Category: model.CategoryBaby, Qty: 15, HasQty: true, Daily: 2, Counted: today}, nil, "", 0, 0, false},
		{"runout uses estimate from counted", model.Item{ID: "a", Title: "a", Qty: 10, HasQty: true, Daily: 2, Counted: today.Add(-2)}, nil, Runout, 3, 6, false},
		{"runout needs quantity", model.Item{ID: "a", Title: "a", Daily: 2, Counted: today}, nil, "", 0, 0, false},
		{"zero daily never runs out", model.Item{ID: "a", Title: "a", Qty: 1, HasQty: true, Daily: 0, Lead: 100}, nil, "", 0, 0, false},

		// precedence
		{"expired beats out", model.Item{ID: "a", Title: "a", Qty: 0, HasQty: true, Expires: today.Add(-1)}, nil, Expired, 1, 0, false},
		{"out beats low", model.Item{ID: "a", Title: "a", Qty: 0, HasQty: true, Min: 1, HasMin: true}, nil, Out, 0, 0, false},
		{"low beats runout", model.Item{ID: "a", Title: "a", Qty: 6, HasQty: true, Min: 6, HasMin: true, Daily: 2, Counted: today}, nil, Low, 0, 6, false},
		{"runout beats expiring", model.Item{ID: "a", Title: "a", Qty: 6, HasQty: true, Daily: 2, Counted: today, Expires: today.Add(1)}, nil, Runout, 3, 6, false},
		{"expired beats low", model.Item{ID: "a", Title: "a", Qty: 1, HasQty: true, Min: 5, HasMin: true, Expires: today.Add(-1)}, nil, Expired, 1, 1, false},
		{"out beats expiring", model.Item{ID: "a", Title: "a", Qty: 0, HasQty: true, Expires: today.Add(1)}, nil, Out, 0, 0, false},

		// open purchases
		{"open 待买 purchase on the item", tracked("a", 0), []model.Buy{{ID: "b", Status: model.BuyTodo, ItemID: "a"}}, Out, 0, 0, true},
		{"done purchase is not open", tracked("a", 0), []model.Buy{{ID: "b", Status: model.BuyDone, ItemID: "a"}}, Out, 0, 0, false},
		{"skipped purchase is not open", tracked("a", 0), []model.Buy{{ID: "b", Status: model.BuySkip, ItemID: "a"}}, Out, 0, 0, false},
		{"open purchase on another item", tracked("a", 0), []model.Buy{{ID: "b", Status: model.BuyTodo, ItemID: "z"}}, Out, 0, 0, false},
		{"open purchase without item", tracked("a", 0), []model.Buy{{ID: "b", Status: model.BuyTodo}}, Out, 0, 0, false},
		{"open purchase on expiring item", model.Item{ID: "a", Title: "a", Expires: today.Add(1)}, []model.Buy{{ID: "b", Status: model.BuyTodo, ItemID: "a"}}, Expiring, 1, 0, true},

		// nothing
		{"plenty of stock is none", tracked("a", 100), nil, "", 0, 0, false},
		{"no quantity no date is none", model.Item{ID: "a", Title: "a"}, nil, "", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			it := tt.item
			it.ID = "a"
			if it.Title == "" {
				it.Title = "a"
			}
			fs := Findings(home([]model.Item{it}, tt.buys...), today)
			if tt.wantKind == "" {
				if len(fs) != 0 {
					t.Fatalf("Findings() = %+v, want none", fs)
				}
				return
			}
			if len(fs) != 1 {
				t.Fatalf("Findings() has %d findings, want 1: %+v", len(fs), fs)
			}
			f := fs[0]
			if f.Kind != tt.wantKind {
				t.Errorf("Kind = %q, want %q", f.Kind, tt.wantKind)
			}
			if f.Days != tt.wantDays {
				t.Errorf("Days = %d, want %d", f.Days, tt.wantDays)
			}
			if f.Est != tt.wantEst {
				t.Errorf("Est = %v, want %v", f.Est, tt.wantEst)
			}
			if f.OpenBuy != tt.wantOpen {
				t.Errorf("OpenBuy = %v, want %v", f.OpenBuy, tt.wantOpen)
			}
			if f.Item.ID != "a" {
				t.Errorf("Item.ID = %q, want a", f.Item.ID)
			}
		})
	}
}

func TestFindingsOrder(t *testing.T) {
	items := []model.Item{
		{ID: "e", Title: "酸奶", Qty: 9, HasQty: true, Expires: today.Add(1)},
		{ID: "b", Title: "酸奶", Qty: 1, HasQty: true, Min: 2, HasMin: true},
		{ID: "a", Title: "牛奶", Qty: 1, HasQty: true, Min: 2, HasMin: true},
		{ID: "d", Title: "鸡蛋", Qty: 0, HasQty: true},
		{ID: "c", Title: "番茄", Expires: today.Add(-2)},
	}
	fs := Findings(home(items), today)
	var got []string
	for _, f := range fs {
		got = append(got, f.Kind.Label()+":"+f.Item.ID)
	}
	// Severity first, then title (牛奶 before 酸奶), then id (b before e).
	want := []string{"已过期:c", "已用完:d", "低库存:a", "低库存:b", "即将过期:e"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Findings order = %v, want %v", got, want)
	}
}

func TestFindingsEmptyHome(t *testing.T) {
	if fs := Findings(&model.Home{}, today); len(fs) != 0 {
		t.Errorf("Findings(empty) = %+v, want none", fs)
	}
}

func TestRestock(t *testing.T) {
	items := []model.Item{
		{ID: "expired", Title: "过期", Expires: today.Add(-1)},
		{ID: "expired-open", Title: "过期已买", Expires: today.Add(-1)},
		{ID: "expiring", Title: "将过期", Expires: today.Add(1)},
		{ID: "out-closed", Title: "用完", Qty: 0, HasQty: true},
		{ID: "low-skipped", Title: "低", Qty: 1, HasQty: true, Min: 2, HasMin: true},
		{ID: "runout-other", Title: "预警", Qty: 4, HasQty: true, Daily: 2, Counted: today},
		{ID: "fine", Title: "够", Qty: 100, HasQty: true},
	}
	buys := []model.Buy{
		{ID: "b1", Status: model.BuyTodo, ItemID: "expired-open"},
		{ID: "b2", Status: model.BuyDone, ItemID: "out-closed"},
		{ID: "b3", Status: model.BuySkip, ItemID: "low-skipped"},
		{ID: "b4", Status: model.BuyTodo, ItemID: "fine"},
	}
	got := Restock(home(items, buys...), today)
	var ids []string
	for _, f := range got {
		ids = append(ids, f.Item.ID)
		if f.OpenBuy {
			t.Errorf("Restock returned %s with OpenBuy set", f.Item.ID)
		}
		if !f.Kind.Buyable() {
			t.Errorf("Restock returned non-buyable kind %q", f.Kind)
		}
	}
	want := []string{"expired", "out-closed", "low-skipped", "runout-other"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("Restock ids = %v, want %v", ids, want)
	}
}

func TestRestockExcludesOpenPurchases(t *testing.T) {
	h := home([]model.Item{tracked("a", 0)}, model.Buy{ID: "b", Status: model.BuyTodo, ItemID: "a"})
	if got := Restock(h, today); len(got) != 0 {
		t.Errorf("Restock() = %+v, want none", got)
	}
}

func TestChoresDue(t *testing.T) {
	h := &model.Home{Chores: []model.Chore{
		{ID: "later", Title: "除尘", Next: today.Add(1)},
		{ID: "none", Title: "无期限"},
		{ID: "mop", Title: "拖地", Next: today.Add(-1)},
		{ID: "dishes", Title: "洗碗", Next: today.Add(-1)},
		{ID: "today", Title: "浇花", Next: today},
		{ID: "wipe", Title: "擦窗", Next: today.Add(-3)},
		{ID: "two", Title: "修剪", Next: today.Add(-2)},
	}}
	got := ChoresDue(h, today)
	var ids []string
	var overdue []int
	for _, d := range got {
		ids = append(ids, d.Chore.ID)
		overdue = append(overdue, d.Overdue)
	}
	wantIDs := []string{"wipe", "two", "mop", "dishes", "today"}
	wantOverdue := []int{3, 2, 1, 1, 0}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Errorf("ChoresDue ids = %v, want %v", ids, wantIDs)
	}
	if !reflect.DeepEqual(overdue, wantOverdue) {
		t.Errorf("ChoresDue overdue = %v, want %v", overdue, wantOverdue)
	}
}

func TestChoresDueEmpty(t *testing.T) {
	if got := ChoresDue(&model.Home{}, today); len(got) != 0 {
		t.Errorf("ChoresDue(empty) = %+v, want none", got)
	}
}

func TestSummarize(t *testing.T) {
	tests := []struct {
		name string
		home *model.Home
		want Summary
	}{
		{
			name: "empty",
			home: &model.Home{},
			want: Summary{},
		},
		{
			name: "expiring only is quiet",
			home: home([]model.Item{{ID: "a", Title: "a", Expires: today.Add(1)}}),
			want: Summary{Expiring: 1},
		},
		{
			name: "out is danger",
			home: home([]model.Item{tracked("a", 0)}),
			want: Summary{Out: 1, Alerts: 1, Tone: "danger"},
		},
		{
			name: "low only is caution",
			home: home([]model.Item{{ID: "a", Title: "a", Qty: 1, HasQty: true, Min: 2, HasMin: true}}),
			want: Summary{Low: 1, Alerts: 1, Tone: "caution"},
		},
		{
			name: "expired is danger",
			home: home([]model.Item{{ID: "a", Title: "a", Expires: today.Add(-1)}}),
			want: Summary{Expired: 1, Alerts: 1, Tone: "danger"},
		},
		{
			name: "runout is caution",
			home: home([]model.Item{{ID: "a", Title: "a", Qty: 4, HasQty: true, Daily: 2, Counted: today}}),
			want: Summary{Runout: 1, Alerts: 1, Tone: "caution"},
		},
		{
			name: "chore due is caution",
			home: &model.Home{Chores: []model.Chore{{ID: "c", Title: "拖地", Next: today}}},
			want: Summary{ChoresDue: 1, Alerts: 1, Tone: "caution"},
		},
		{
			name: "open buys are counted, not alerts",
			home: &model.Home{Buys: []model.Buy{
				{ID: "b1", Status: model.BuyTodo}, {ID: "b2", Status: model.BuyTodo}, {ID: "b3", Status: model.BuyDone},
				{ID: "b4", Status: model.BuySkip},
			}},
			want: Summary{OpenBuys: 2},
		},
		{
			name: "findings with open purchase still count as alerts",
			home: &model.Home{
				Items: []model.Item{{ID: "a", Title: "a", Expires: today.Add(-1)}},
				Buys:  []model.Buy{{ID: "b", Status: model.BuyTodo, ItemID: "a"}},
			},
			want: Summary{Expired: 1, OpenBuys: 1, Alerts: 1, Tone: "danger"},
		},
		{
			name: "every kind together",
			home: &model.Home{
				Items: []model.Item{
					{ID: "a", Title: "a", Expires: today.Add(-1)},
					tracked("b", 0),
					{ID: "c", Title: "c", Qty: 1, HasQty: true, Min: 2, HasMin: true},
					{ID: "d", Title: "d", Qty: 4, HasQty: true, Daily: 2, Counted: today},
					{ID: "e", Title: "e", Expires: today.Add(2)},
				},
				Chores: []model.Chore{{ID: "c1", Title: "拖地", Next: today.Add(-4)}, {ID: "c2", Title: "除尘", Next: today.Add(9)}},
				Buys:   []model.Buy{{ID: "b1", Status: model.BuyTodo}},
			},
			want: Summary{Expired: 1, Out: 1, Low: 1, Runout: 1, Expiring: 1, OpenBuys: 1, ChoresDue: 1, Alerts: 5, Tone: "danger"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Summarize(tt.home, today); got != tt.want {
				t.Errorf("Summarize() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
