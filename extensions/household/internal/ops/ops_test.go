package ops

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

var (
	shanghai = time.FixedZone("UTC+8", 8*3600)
	// now is 09:00 on 2026-11-10 in the hub's zone; every test works on that day.
	now = time.Date(2026, 11, 10, 9, 0, 0, 0, shanghai)
)

const today = model.Day("2026-11-10")

func newBase() *kernel.Base { return &kernel.Base{Format: 1} }

// mustSave runs Save and fails the test on error.
func mustSave(t *testing.T, b *kernel.Base, in kernel.EntityInput) *kernel.Entity {
	t.Helper()
	e, err := Save(b, in, now, shanghai)
	if err != nil {
		t.Fatalf("Save(%q): %v", in.Type, err)
	}
	return e
}

// create makes one record of the type with the title and values.
func create(t *testing.T, b *kernel.Base, typ, title string, values map[string]any) *kernel.Entity {
	t.Helper()
	return mustSave(t, b, kernel.EntityInput{Type: typ, Title: strPtr(title), Values: values})
}

// homeOf reads the household space of b; the space must exist.
func homeOf(t *testing.T, b *kernel.Base) *model.Home {
	t.Helper()
	sp, err := b.Space(model.SpaceID)
	if err != nil {
		t.Fatalf("household space: %v", err)
	}
	return model.Load(sp, shanghai)
}

// snapshot is the JSON of the base, to compare bases before and after a call.
func snapshot(t *testing.T, b *kernel.Base) string {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func itemByTitle(t *testing.T, h *model.Home, title string) model.Item {
	t.Helper()
	for _, it := range h.Items {
		if it.Title == title {
			return it
		}
	}
	t.Fatalf("no item %q", title)
	return model.Item{}
}

func placeByTitle(t *testing.T, b *kernel.Base, title string) string {
	t.Helper()
	for _, p := range homeOf(t, b).Places {
		if p.Title == title {
			return p.ID
		}
	}
	t.Fatalf("no place %q", title)
	return ""
}

// openBuyOf is the 待买 purchase of the item with the title.
func openBuyOf(t *testing.T, h *model.Home, title string) model.Buy {
	t.Helper()
	id := itemByTitle(t, h, title).ID
	for _, b := range h.Buys {
		if b.ItemID == id && b.Status == model.BuyTodo {
			return b
		}
	}
	t.Fatalf("no open purchase of %q", title)
	return model.Buy{}
}

// samplesBase is a base holding the sample set.
func samplesBase(t *testing.T) *kernel.Base {
	t.Helper()
	b := newBase()
	if _, err := LoadSamples(b, now, shanghai); err != nil {
		t.Fatal(err)
	}
	return b
}

// fixture is a base with one place and one item of it, for the refusal tests.
func fixture(t *testing.T) (b *kernel.Base, place, item string) {
	t.Helper()
	b = newBase()
	place = create(t, b, model.TPlace, "厨房冰箱", nil).ID
	item = create(t, b, model.TItem, "纯牛奶", map[string]any{"place": []string{place}, "qty": 2}).ID
	return b, place, item
}

func TestSaveItemCounted(t *testing.T) {
	tests := []struct {
		name   string
		create map[string]any // values of the new item
		change map[string]any // values of a second save of the item; nil for none
		want   model.Day
	}{
		{"qty without counted counts today", map[string]any{"qty": 3}, nil, "2026-11-10"},
		{"explicit counted is kept", map[string]any{"qty": 3, "counted": "2026-11-01"}, nil, "2026-11-01"},
		{"no qty leaves counted unset", map[string]any{"unit": "盒"}, nil, ""},
		{"null qty leaves counted unset", map[string]any{"qty": nil}, nil, ""},
		{"a change that sets qty counts today", map[string]any{"unit": "盒"}, map[string]any{"qty": 2}, "2026-11-10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBase()
			e := create(t, b, model.TItem, "纯牛奶", tt.create)
			if tt.change != nil {
				mustSave(t, b, kernel.EntityInput{ID: e.ID, Values: tt.change})
			}
			it, ok := homeOf(t, b).Item(e.ID)
			if !ok {
				t.Fatal("item not in the home")
			}
			if it.Counted != tt.want {
				t.Errorf("counted = %q, want %q", it.Counted, tt.want)
			}
		})
	}
}

func TestSaveChoreNextOnCreate(t *testing.T) {
	b := newBase()
	none := create(t, b, model.TChore, "拖地", map[string]any{"every": 7})
	given := create(t, b, model.TChore, "除尘", map[string]any{"every": 30, "next": "2026-11-20"})
	nulled := create(t, b, model.TChore, "擦窗", map[string]any{"next": nil})
	mustSave(t, b, kernel.EntityInput{ID: given.ID, Values: map[string]any{"who": "家人甲"}})

	h := homeOf(t, b)
	want := map[string]model.Day{none.ID: today, given.ID: "2026-11-20", nulled.ID: today}
	for _, c := range h.Chores {
		if c.Next != want[c.ID] {
			t.Errorf("chore %q next = %q, want %q", c.Title, c.Next, want[c.ID])
		}
	}
}

func TestSaveBuyDefaults(t *testing.T) {
	b := newBase()
	plain := create(t, b, model.TBuy, "洗衣液", nil)
	given := create(t, b, model.TBuy, "鸡蛋", map[string]any{"reason": model.ReasonLow})

	h := homeOf(t, b)
	for _, buy := range h.Buys {
		if buy.Status != model.BuyTodo {
			t.Errorf("purchase %q status = %q, want %q", buy.Title, buy.Status, model.BuyTodo)
		}
		switch buy.ID {
		case plain.ID:
			if buy.Reason != model.ReasonManual {
				t.Errorf("reason without one = %q, want %q", buy.Reason, model.ReasonManual)
			}
		case given.ID:
			if buy.Reason != model.ReasonLow {
				t.Errorf("given reason = %q, want %q", buy.Reason, model.ReasonLow)
			}
		}
	}
}

func TestSaveRefusals(t *testing.T) {
	tests := []struct {
		name string
		in   func(place, item string) kernel.EntityInput
	}{
		{"unknown type", func(_, _ string) kernel.EntityInput {
			return kernel.EntityInput{Type: "车", Title: strPtr("汽车")}
		}},
		{"no title", func(_, _ string) kernel.EntityInput {
			return kernel.EntityInput{Type: model.TPlace}
		}},
		{"undeclared property", func(_, _ string) kernel.EntityInput {
			return kernel.EntityInput{Type: model.TPlace, Title: strPtr("阳台"), Values: map[string]any{"colour": "红"}}
		}},
		{"enum outside its options", func(_, _ string) kernel.EntityInput {
			return kernel.EntityInput{Type: model.TItem, Title: strPtr("玩具"), Values: map[string]any{"category": "玩具"}}
		}},
		{"relation to a record of another type", func(_, item string) kernel.EntityInput {
			return kernel.EntityInput{Type: model.TItem, Title: strPtr("鸡蛋"), Values: map[string]any{"place": []string{item}}}
		}},
		{"bad date", func(_, _ string) kernel.EntityInput {
			return kernel.EntityInput{Type: model.TChore, Title: strPtr("拖地"), Values: map[string]any{"next": "2026-02-30"}}
		}},
		{"unknown status", func(_, _ string) kernel.EntityInput {
			return kernel.EntityInput{Type: model.TBuy, Title: strPtr("洗衣液"), Status: strPtr("乱")}
		}},
		{"unknown record to change", func(_, _ string) kernel.EntityInput {
			return kernel.EntityInput{ID: "e999", Title: strPtr("改名")}
		}},
		{"type of an existing record", func(place, _ string) kernel.EntityInput {
			return kernel.EntityInput{ID: place, Type: model.TItem}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, place, item := fixture(t)
			before := snapshot(t, b)
			if _, err := Save(b, tt.in(place, item), now, shanghai); err == nil {
				t.Fatal("Save succeeded, want an error")
			}
			if after := snapshot(t, b); after != before {
				t.Error("a refused Save changed the base")
			}
		})
	}
}

func TestUse(t *testing.T) {
	tests := []struct {
		name    string
		values  map[string]any
		n       float64
		want    float64
		wantErr bool
	}{
		{"estimate after decay", map[string]any{"qty": 10, "daily": 2, "counted": "2026-11-07"}, 1, 3, false},
		{"no daily use takes the amount", map[string]any{"qty": 10}, 4, 6, false},
		{"more than the estimate stops at zero", map[string]any{"qty": 2}, 5, 0, false},
		{"zero is refused", map[string]any{"qty": 2}, 0, 0, true},
		{"negative is refused", map[string]any{"qty": 2}, -1, 0, true},
		{"an item without quantity is refused", map[string]any{"unit": "包"}, 1, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBase()
			it := create(t, b, model.TItem, "纸巾", tt.values)
			before := snapshot(t, b)
			_, err := Use(b, it.ID, tt.n, now, shanghai)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Use succeeded, want an error")
				}
				if snapshot(t, b) != before {
					t.Error("a refused Use changed the base")
				}
				return
			}
			if err != nil {
				t.Fatalf("Use: %v", err)
			}
			got, _ := homeOf(t, b).Item(it.ID)
			if got.Qty != tt.want {
				t.Errorf("qty = %v, want %v", got.Qty, tt.want)
			}
			if got.Counted != today {
				t.Errorf("counted = %q, want %q", got.Counted, today)
			}
		})
	}
}

func TestUseRefusesWrongRecords(t *testing.T) {
	b, place, _ := fixture(t)
	before := snapshot(t, b)
	for _, id := range []string{place, "e999"} {
		if _, err := Use(b, id, 1, now, shanghai); err == nil {
			t.Errorf("Use(%q) succeeded, want an error", id)
		}
	}
	if snapshot(t, b) != before {
		t.Error("a refused Use changed the base")
	}
}

func TestRestockCreatesOnePurchasePerFinding(t *testing.T) {
	b := samplesBase(t)
	h := homeOf(t, b)
	created, err := Restock(b, now, shanghai)
	if err != nil {
		t.Fatal(err)
	}
	// The samples: 纯牛奶 expired, 鸡蛋 out, 酱油 low, 纸尿裤 runout (refill 40); the others need no purchase.
	want := map[string]struct {
		kind   rules.Kind
		amount float64
	}{
		"纯牛奶": {rules.Expired, 1},
		"鸡蛋":  {rules.Out, 1},
		"酱油":  {rules.Low, 1},
		"纸尿裤": {rules.Runout, 40},
	}
	if len(created) != len(want) {
		t.Fatalf("Restock created %d purchases, want %d", len(created), len(want))
	}
	buys := homeOf(t, b).Buys
	for _, e := range created {
		w, ok := want[e.Title]
		if !ok {
			t.Fatalf("unexpected purchase %q", e.Title)
		}
		i := slices.IndexFunc(buys, func(x model.Buy) bool { return x.ID == e.ID })
		if i < 0 {
			t.Fatalf("purchase %q not in the home", e.Title)
		}
		buy := buys[i]
		item := itemByTitle(t, h, e.Title)
		if buy.Status != model.BuyTodo {
			t.Errorf("%s: status = %q, want 待买", e.Title, buy.Status)
		}
		if buy.Origin != item.ID || buy.ItemID != item.ID {
			t.Errorf("%s: origin %q and item %q, want %q", e.Title, buy.Origin, buy.ItemID, item.ID)
		}
		if buy.Amount != w.amount {
			t.Errorf("%s: amount = %v, want %v", e.Title, buy.Amount, w.amount)
		}
		if buy.Reason != w.kind.Reason() {
			t.Errorf("%s: reason = %q, want %q", e.Title, buy.Reason, w.kind.Reason())
		}
		if buy.Unit != item.Unit || buy.Category != item.Category {
			t.Errorf("%s: unit %q category %q, want the item's %q %q", e.Title, buy.Unit, buy.Category, item.Unit, item.Category)
		}
	}
	if len(rules.Restock(homeOf(t, b), today)) != 0 {
		t.Error("after Restock some finding still needs a purchase")
	}
}

func TestRestockIsIdempotent(t *testing.T) {
	b := samplesBase(t)
	if first, err := Restock(b, now, shanghai); err != nil || len(first) == 0 {
		t.Fatalf("first Restock: %d created, %v", len(first), err)
	}
	before := snapshot(t, b)
	again, err := Restock(b, now, shanghai)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Errorf("second Restock created %d purchases, want none", len(again))
	}
	if snapshot(t, b) != before {
		t.Error("second Restock changed the base")
	}
}

func TestRestockAmountsAndOpenPurchases(t *testing.T) {
	b := newBase()
	soy := create(t, b, model.TItem, "酱油", map[string]any{"qty": 1, "min": 1, "refill": 6})
	create(t, b, model.TItem, "鸡蛋", map[string]any{"qty": 0})
	open := create(t, b, model.TBuy, "酱油", map[string]any{"item": []string{soy.ID}, "amount": 2})
	created, err := Restock(b, now, shanghai)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 || created[0].Title != "鸡蛋" {
		t.Fatalf("Restock created %d purchases, want only the one for 鸡蛋", len(created))
	}
	buys := homeOf(t, b).Buys
	for _, buy := range buys {
		if buy.ID == open.ID && buy.Amount != 2 {
			t.Errorf("open purchase amount changed to %v", buy.Amount)
		}
		if buy.Origin == "" {
			continue
		}
		if buy.Title == "鸡蛋" && buy.Amount != 1 {
			t.Errorf("amount without refill = %v, want 1", buy.Amount)
		}
	}
}

func TestRestockOnEmptyBaseCreatesNothing(t *testing.T) {
	b := newBase()
	create(t, b, model.TItem, "大米", map[string]any{"qty": 5, "min": 2})
	created, err := Restock(b, now, shanghai)
	if err != nil || len(created) != 0 {
		t.Fatalf("Restock = %d, %v; want none", len(created), err)
	}
}

func TestPurchaseStocksTheItem(t *testing.T) {
	b := samplesBase(t)
	if _, err := Restock(b, now, shanghai); err != nil {
		t.Fatal(err)
	}
	buy := openBuyOf(t, homeOf(t, b), "酱油")
	e, err := Purchase(b, buy.ID, true, now, shanghai)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != model.BuyDone {
		t.Errorf("status = %q, want 已买", e.Status)
	}
	h := homeOf(t, b)
	soy := itemByTitle(t, h, "酱油")
	// Qty 1 and no use, so the estimate is 1; the refill is 1 when unset.
	if soy.Qty != 2 || soy.Counted != today {
		t.Errorf("soy qty %v counted %q, want 2 and %s", soy.Qty, soy.Counted, today)
	}
	for _, f := range rules.Findings(h, today) {
		if f.Item.ID == soy.ID {
			t.Errorf("soy sauce still has a finding: %s", f.Kind)
		}
	}
}

func TestPurchaseAddsTheEstimateAfterDecay(t *testing.T) {
	b := samplesBase(t)
	if _, err := Restock(b, now, shanghai); err != nil {
		t.Fatal(err)
	}
	h := homeOf(t, b)
	diaper := itemByTitle(t, h, "纸尿裤")
	if est := rules.Estimate(diaper, today); est != 25 {
		t.Fatalf("estimate before purchase = %v, want 25", est)
	}
	buy := openBuyOf(t, h, "纸尿裤")
	if _, err := Purchase(b, buy.ID, true, now, shanghai); err != nil {
		t.Fatal(err)
	}
	h = homeOf(t, b)
	diaper = itemByTitle(t, h, "纸尿裤")
	if diaper.Qty != 65 || diaper.Counted != today {
		t.Errorf("qty %v counted %q, want 65 and today", diaper.Qty, diaper.Counted)
	}
	if est := rules.Estimate(diaper, today); est != 65 {
		t.Errorf("estimate after purchase = %v, want 65", est)
	}
	for _, f := range rules.Findings(h, today) {
		if f.Item.ID == diaper.ID {
			t.Errorf("diapers still have a finding: %s", f.Kind)
		}
	}
}

func TestPurchaseClearsOnlyAPastExpiry(t *testing.T) {
	b := samplesBase(t)
	if _, err := Restock(b, now, shanghai); err != nil {
		t.Fatal(err)
	}
	h := homeOf(t, b)
	yogurt := itemByTitle(t, h, "酸奶")
	bought := mustSave(t, b, kernel.EntityInput{Type: model.TBuy, Title: strPtr("酸奶"),
		Values: map[string]any{"item": []string{yogurt.ID}, "amount": 2}})

	if _, err := Purchase(b, openBuyOf(t, homeOf(t, b), "纯牛奶").ID, true, now, shanghai); err != nil {
		t.Fatal(err)
	}
	if _, err := Purchase(b, bought.ID, true, now, shanghai); err != nil {
		t.Fatal(err)
	}
	h = homeOf(t, b)
	milk := itemByTitle(t, h, "纯牛奶")
	if milk.Expires != "" {
		t.Errorf("expired milk kept expiry %q", milk.Expires)
	}
	// The expired batch is thrown away: the new quantity is the amount alone, not the 2 left plus 1.
	if milk.Qty != 1 {
		t.Errorf("milk qty = %v, want 1 (the amount alone)", milk.Qty)
	}
	yogurt = itemByTitle(t, h, "酸奶")
	if yogurt.Expires != "2026-11-12" {
		t.Errorf("future expiry = %q, want 2026-11-12", yogurt.Expires)
	}
	if yogurt.Qty != 5 {
		t.Errorf("yogurt qty = %v, want 5 (3 + 2)", yogurt.Qty)
	}
}

func TestPurchaseAmountZeroUsesTheRefill(t *testing.T) {
	tests := []struct {
		name string
		item map[string]any
		want float64
	}{
		{"the refill", map[string]any{"qty": 0, "refill": 12}, 12},
		{"one when there is no refill", map[string]any{"qty": 2}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBase()
			it := create(t, b, model.TItem, "鸡蛋", tt.item)
			buy := create(t, b, model.TBuy, "鸡蛋", map[string]any{"item": []string{it.ID}, "amount": 0})
			if _, err := Purchase(b, buy.ID, true, now, shanghai); err != nil {
				t.Fatal(err)
			}
			if got := itemByTitle(t, homeOf(t, b), "鸡蛋").Qty; got != tt.want {
				t.Errorf("qty = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPurchaseNotBoughtOnlyCloses(t *testing.T) {
	b := samplesBase(t)
	if _, err := Restock(b, now, shanghai); err != nil {
		t.Fatal(err)
	}
	h := homeOf(t, b)
	soy := itemByTitle(t, h, "酱油")
	buy := openBuyOf(t, h, "酱油")
	e, err := Purchase(b, buy.ID, false, now, shanghai)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != model.BuySkip {
		t.Errorf("status = %q, want 不买", e.Status)
	}
	if got := itemByTitle(t, homeOf(t, b), "酱油"); got != soy {
		t.Errorf("skipping changed the item: %+v, was %+v", got, soy)
	}
}

func TestPurchaseWithoutItemStocksNothing(t *testing.T) {
	b := samplesBase(t)
	h := homeOf(t, b)
	var manual model.Buy
	for _, x := range h.Buys {
		if x.ItemID == "" && x.Status == model.BuyTodo {
			manual = x
		}
	}
	if manual.ID == "" {
		t.Fatal("no manual purchase in the samples")
	}
	items := slices.Clone(h.Items)
	if _, err := Purchase(b, manual.ID, true, now, shanghai); err != nil {
		t.Fatal(err)
	}
	if got := homeOf(t, b).Items; !slices.Equal(got, items) {
		t.Error("a purchase without an item changed the items")
	}
}

func TestPurchaseRefusals(t *testing.T) {
	b := samplesBase(t)
	if _, err := Restock(b, now, shanghai); err != nil {
		t.Fatal(err)
	}
	h := homeOf(t, b)
	buy := openBuyOf(t, h, "酱油")
	if _, err := Purchase(b, buy.ID, true, now, shanghai); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, b)
	soy := itemByTitle(t, h, "酱油")
	for name, id := range map[string]string{
		"unknown purchase":  "e999",
		"an item":           soy.ID,
		"a closed purchase": buy.ID,
	} {
		if _, err := Purchase(b, id, true, now, shanghai); err == nil {
			t.Errorf("%s: Purchase succeeded, want an error", name)
		}
		if _, err := Purchase(b, id, false, now, shanghai); err == nil {
			t.Errorf("%s: Purchase(false) succeeded, want an error", name)
		}
	}
	if snapshot(t, b) != before {
		t.Error("refused purchases changed the base")
	}
}

func TestChoreDone(t *testing.T) {
	b := samplesBase(t)
	h := homeOf(t, b)
	var mop, dust model.Chore
	for _, c := range h.Chores {
		switch c.Title {
		case "拖地":
			mop = c
		case "除尘":
			dust = c
		}
	}
	e, err := ChoreDone(b, mop.ID, now, shanghai)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != "" {
		t.Errorf("chore has a status %q", e.Status)
	}
	got := homeOf(t, b)
	for _, c := range got.Chores {
		if c.ID == mop.ID && (c.Last != today || c.Next != "2026-11-17") {
			t.Errorf("mop last %q next %q, want today and 2026-11-17", c.Last, c.Next)
		}
		if c.ID == dust.ID && c.Next != dust.Next {
			t.Errorf("another chore changed: next %q", c.Next)
		}
	}
	for _, d := range rules.ChoresDue(got, today) {
		if d.Chore.ID == mop.ID {
			t.Error("a chore just done is still due")
		}
	}
}

func TestChoreDoneWithoutRepeat(t *testing.T) {
	tests := []struct {
		name  string
		every any // nil leaves the period unset
	}{
		{"period unset", nil},
		{"period zero", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBase()
			vals := map[string]any{"who": "家人乙"}
			if tt.every != nil {
				vals["every"] = tt.every
			}
			c := create(t, b, model.TChore, "换灯泡", vals)
			if _, err := ChoreDone(b, c.ID, now, shanghai); err != nil {
				t.Fatal(err)
			}
			got := homeOf(t, b).Chores[0]
			if got.Last != today || got.Next != "" {
				t.Errorf("last %q next %q, want today and unset", got.Last, got.Next)
			}
		})
	}
}

func TestChoreDoneRefusals(t *testing.T) {
	b, place, _ := fixture(t)
	before := snapshot(t, b)
	for _, id := range []string{place, "e999"} {
		if _, err := ChoreDone(b, id, now, shanghai); err == nil {
			t.Errorf("ChoreDone(%q) succeeded, want an error", id)
		}
	}
	if snapshot(t, b) != before {
		t.Error("refused ChoreDone changed the base")
	}
}

func TestMealClose(t *testing.T) {
	b := samplesBase(t)
	h := homeOf(t, b)
	var planned, done model.Meal
	for _, m := range h.Meals {
		if m.Status == model.MealPlan {
			planned = m
		} else {
			done = m
		}
	}
	e, err := MealClose(b, planned.ID, true, now, shanghai)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != model.MealDone {
		t.Errorf("status = %q, want 已做", e.Status)
	}

	cancel := create(t, b, model.TMeal, "白粥", map[string]any{"date": "2026-11-11", "slot": "早餐"})
	if e, err := MealClose(b, cancel.ID, false, now, shanghai); err != nil || e.Status != model.MealCancel {
		t.Fatalf("cancel: status %v, err %v; want 取消", e, err)
	}

	before := snapshot(t, b)
	for name, id := range map[string]string{
		"a meal already done":      done.ID,
		"a meal already closed":    planned.ID,
		"an unknown meal":          "e999",
		"a record of another type": h.Items[0].ID,
	} {
		if _, err := MealClose(b, id, true, now, shanghai); err == nil {
			t.Errorf("%s: MealClose(true) succeeded, want an error", name)
		}
		if _, err := MealClose(b, id, false, now, shanghai); err == nil {
			t.Errorf("%s: MealClose(false) succeeded, want an error", name)
		}
	}
	if snapshot(t, b) != before {
		t.Error("refused MealClose changed the base")
	}
}

func TestRemove(t *testing.T) {
	b := samplesBase(t)
	h := homeOf(t, b)
	cabinet := placeByTitle(t, b, "杂物柜")
	if err := Remove(b, cabinet, now); err != nil {
		t.Fatal(err)
	}
	got := homeOf(t, b)
	if _, ok := got.Place(cabinet); ok {
		t.Error("removed place is still there")
	}
	if itemByTitle(t, got, "纸尿裤").PlaceID != "" || itemByTitle(t, got, "创可贴").PlaceID != "" {
		t.Error("items still point to the removed place")
	}
	if len(got.Items) != len(h.Items) || len(got.Chores) != len(h.Chores) {
		t.Error("removing a place removed other records")
	}

	if err := Remove(b, itemByTitle(t, got, "大米").ID, now); err != nil {
		t.Fatal(err)
	}
	for _, r := range homeOf(t, b).Recipes {
		if r.Title == "白粥" && len(r.Needs) != 0 {
			t.Errorf("recipe still needs %v after its item was removed", r.Needs)
		}
	}
}

func TestRemoveRefusesUnknownRecord(t *testing.T) {
	b := samplesBase(t)
	before := snapshot(t, b)
	if err := Remove(b, "e999", now); err == nil {
		t.Fatal("Remove of an unknown record succeeded")
	}
	if snapshot(t, b) != before {
		t.Error("a refused Remove changed the base")
	}
}

func TestRestockLeavesTheItemNoteOff(t *testing.T) {
	b := newBase()
	create(t, b, model.TItem, "牛奶", map[string]any{"qty": 0, "note": "放冰箱第二层"})
	created, err := Restock(b, now, shanghai)
	if err != nil || len(created) != 1 {
		t.Fatalf("Restock = %d, %v; want one purchase", len(created), err)
	}
	if buy := homeOf(t, b).Buys[0]; buy.Note != "" {
		t.Errorf("purchase note = %q, want none: the item's note is not carried over", buy.Note)
	}
}

func TestPurchaseOfUntrackedItemOnlyClearsExpiry(t *testing.T) {
	b := newBase()
	create(t, b, model.TItem, "创可贴", map[string]any{"expires": "2026-11-01"})
	if _, err := Restock(b, now, shanghai); err != nil {
		t.Fatal(err)
	}
	buy := openBuyOf(t, homeOf(t, b), "创可贴")
	if _, err := Purchase(b, buy.ID, true, now, shanghai); err != nil {
		t.Fatal(err)
	}
	got := itemByTitle(t, homeOf(t, b), "创可贴")
	if got.HasQty || got.Qty != 0 {
		t.Errorf("untracked item got qty %v (set %v), want none", got.Qty, got.HasQty)
	}
	if got.Counted != "" {
		t.Errorf("untracked item counted %q, want unset", got.Counted)
	}
	if got.Expires != "" {
		t.Errorf("past expiry %q kept, want cleared", got.Expires)
	}
}

func TestPurchaseOfExpiredItemReplacesTheBatch(t *testing.T) {
	b := newBase()
	create(t, b, model.TItem, "纯牛奶", map[string]any{"qty": 4, "expires": "2026-11-05", "refill": 6})
	if _, err := Restock(b, now, shanghai); err != nil {
		t.Fatal(err)
	}
	buy := openBuyOf(t, homeOf(t, b), "纯牛奶")
	if _, err := Purchase(b, buy.ID, true, now, shanghai); err != nil {
		t.Fatal(err)
	}
	got := itemByTitle(t, homeOf(t, b), "纯牛奶")
	// The old batch is thrown away: the quantity is the amount alone (6), not the 4 left plus 6.
	if got.Qty != 6 || got.Counted != today || got.Expires != "" {
		t.Errorf("qty %v counted %q expires %q, want 6, today and unset", got.Qty, got.Counted, got.Expires)
	}
}

func TestChoreDoneRoundsFractionalPeriodUp(t *testing.T) {
	tests := []struct {
		every float64
		next  model.Day
	}{
		{0.5, "2026-11-11"},
		{1.5, "2026-11-12"},
		{7, "2026-11-17"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.every), func(t *testing.T) {
			b := newBase()
			create(t, b, model.TChore, "倒垃圾", map[string]any{"every": tt.every})
			if _, err := ChoreDone(b, homeOf(t, b).Chores[0].ID, now, shanghai); err != nil {
				t.Fatal(err)
			}
			h := homeOf(t, b)
			if got := h.Chores[0].Next; got != tt.next {
				t.Errorf("next = %q, want %q", got, tt.next)
			}
			if due := rules.ChoresDue(h, today); len(due) != 0 {
				t.Errorf("chore just done is due: %+v", due)
			}
		})
	}
}
