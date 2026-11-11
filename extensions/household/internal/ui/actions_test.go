package ui

import (
	"testing"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
)

func TestRestockCreatesOnePurchasePerNeed(t *testing.T) {
	st := newStore(t, true)
	a, changed := newApp(st)
	h := home(t, st)
	need := rules.Restock(h, testToday)
	if len(need) == 0 {
		t.Fatal("samples need no restock")
	}
	res := act(t, a, state{}, nil, "restock")
	toastHas(t, res, "已加入")
	if *changed != 1 {
		t.Fatalf("changed called %d times, want 1", *changed)
	}

	after := home(t, st)
	if got, want := len(after.Buys)-len(h.Buys), len(need); got != want {
		t.Fatalf("%d purchases created, want %d", got, want)
	}
	reasons := map[string]bool{}
	for _, f := range need {
		reasons[f.Kind.Reason()] = true
	}
	for _, b := range after.Buys[len(h.Buys):] {
		if b.Status != model.BuyTodo {
			t.Fatalf("new purchase %q is %q, want 待买", b.Title, b.Status)
		}
		if b.ItemID == "" || !reasons[b.Reason] {
			t.Fatalf("new purchase %q item %q reason %q not from a finding", b.Title, b.ItemID, b.Reason)
		}
		if b.Amount <= 0 {
			t.Fatalf("new purchase %q amount %v, want the refill or 1", b.Title, b.Amount)
		}
	}
}

func TestRestockTwiceCreatesNothingNew(t *testing.T) {
	st := newStore(t, true)
	a, changed := newApp(st)
	act(t, a, state{}, nil, "restock")
	before := snapshot(t, st)
	res := act(t, a, state{}, nil, "restock")
	toastHas(t, res, "没有需要补的货")
	if snapshot(t, st) != before {
		t.Fatal("a second restock changed the base")
	}
	if *changed != 1 {
		t.Fatalf("changed called %d times over two restocks, want 1", *changed)
	}
}

// restockPurchaseWithQuantity is an open purchase of an item that has a quantity, made by a restock.
func restockPurchaseWithQuantity(t *testing.T, h *model.Home) (model.Buy, model.Item) {
	t.Helper()
	for _, b := range h.Buys {
		if b.Status != model.BuyTodo || b.ItemID == "" {
			continue
		}
		if it, ok := h.Item(b.ItemID); ok && it.HasQty {
			return b, it
		}
	}
	t.Fatal("no open purchase of an item with a quantity")
	return model.Buy{}, model.Item{}
}

func TestBuyStocksTheItemAndCountsItToday(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	act(t, a, state{}, nil, "restock")
	buy, it := restockPurchaseWithQuantity(t, home(t, st))
	// The amount is the purchase's, or the item's refill, or 1. A past expiry throws the old batch away, so the stock is
	// the amount alone; otherwise the amount is added to the estimate.
	amount := buy.Amount
	if amount == 0 {
		amount = max(it.Refill, 1)
	}
	wantQty := rules.Estimate(it, testToday) + amount
	if it.Expires != "" && it.Expires < testToday {
		wantQty = amount
	}

	res := act(t, a, state{}, nil, "buy:"+buy.ID)
	toastHas(t, res, "已买")
	h := home(t, st)
	got, _ := h.Item(it.ID)
	if got.Qty != wantQty {
		t.Fatalf("quantity %v after buying, want %v", got.Qty, wantQty)
	}
	if got.Counted != testToday {
		t.Fatalf("counted %q after buying, want today", got.Counted)
	}
	for _, b := range h.Buys {
		if b.ID == buy.ID && b.Status != model.BuyDone {
			t.Fatalf("purchase status %q, want 已买", b.Status)
		}
	}
}

func TestBuyAPastExpiryIsCleared(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	act(t, a, state{}, nil, "restock")
	h := home(t, st)
	var expiredBuy model.Buy
	var expiredItem model.Item
	for _, b := range h.Buys {
		if b.Status != model.BuyTodo || b.Reason != model.ReasonExpired {
			continue
		}
		if it, ok := h.Item(b.ItemID); ok && it.HasQty && it.Expires != "" {
			expiredBuy, expiredItem = b, it
			break
		}
	}
	if expiredBuy.ID == "" {
		t.Fatal("samples have no expired item with a quantity")
	}
	act(t, a, state{}, nil, "buy:"+expiredBuy.ID)
	got, _ := home(t, st).Item(expiredItem.ID)
	if got.Expires != "" {
		t.Fatalf("expires %q still set after buying the new batch", got.Expires)
	}
}

func TestSkipClosesThePurchaseAndStocksNothing(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	act(t, a, state{}, nil, "restock")
	buy, it := restockPurchaseWithQuantity(t, home(t, st))
	before, _ := home(t, st).Item(it.ID)

	res := act(t, a, state{}, nil, "skip:"+buy.ID)
	toastHas(t, res, "不买")
	h := home(t, st)
	for _, b := range h.Buys {
		if b.ID == buy.ID && b.Status != model.BuySkip {
			t.Fatalf("purchase status %q, want 不买", b.Status)
		}
	}
	after, _ := h.Item(it.ID)
	if after.Qty != before.Qty || after.Counted != before.Counted {
		t.Fatalf("skipping changed the item: %v -> %v", before.Qty, after.Qty)
	}
}

func TestBuyingAClosedPurchaseIsAToast(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	act(t, a, state{}, nil, "restock")
	buy, _ := restockPurchaseWithQuantity(t, home(t, st))
	act(t, a, state{}, nil, "skip:"+buy.ID)
	before := snapshot(t, st)
	res := act(t, a, state{}, nil, "buy:"+buy.ID)
	if res.Toast == "" {
		t.Fatal("buying a closed purchase gave no toast")
	}
	if snapshot(t, st) != before {
		t.Fatal("a refused purchase changed the base")
	}
}

func TestChoreDoneSchedulesTheNextRound(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	h := home(t, st)
	var chore model.Chore
	for _, c := range h.Chores {
		if c.Every > 0 {
			chore = c
			break
		}
	}
	if chore.ID == "" {
		t.Fatal("no repeating chore in the samples")
	}
	res := act(t, a, state{}, nil, "chore:"+chore.ID)
	toastHas(t, res, "已完成")
	got := findChore(home(t, st), chore.ID)
	if got.Last != testToday {
		t.Fatalf("last %q, want today", got.Last)
	}
	if want := testToday.Add(int(chore.Every)); got.Next != want {
		t.Fatalf("next %q, want %q", got.Next, want)
	}
}

func TestChoreDoneOneOffClearsNext(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	id := entityID(t, st, model.TChore, "拖地")
	// Turn the chore into a one-off through the form, then complete it.
	act(t, a, state{F: "edit:" + id}, map[string]string{"title": "拖地", "p_every": ""}, "save")
	act(t, a, state{}, nil, "chore:"+id)
	got := findChore(home(t, st), id)
	if got.Next != "" {
		t.Fatalf("one-off chore kept next %q", got.Next)
	}
}

func findChore(h *model.Home, id string) model.Chore {
	for _, c := range h.Chores {
		if c.ID == id {
			return c
		}
	}
	return model.Chore{}
}

func TestUseTakesAnAmountOffTheEstimate(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	var item model.Item
	for _, it := range home(t, st).Items {
		if it.HasQty && it.Qty > 0 {
			item = it
			break
		}
	}
	if item.ID == "" {
		t.Fatal("no item with a quantity")
	}
	want := rules.Estimate(item, testToday) - 1
	if want < 0 {
		want = 0
	}
	res := act(t, a, state{}, nil, "use:"+item.ID)
	toastHas(t, res, "已用掉")
	got, _ := home(t, st).Item(item.ID)
	if got.Qty != want {
		t.Fatalf("quantity %v after using 1, want %v", got.Qty, want)
	}
	if got.Counted != testToday {
		t.Fatalf("counted %q after use, want today", got.Counted)
	}
}

func TestUseWithoutQuantityIsAToast(t *testing.T) {
	st := newStore(t, false)
	a, _ := newApp(st)
	act(t, a, state{F: "new:item"}, map[string]string{"title": "牙刷"}, "save")
	id := entityID(t, st, model.TItem, "牙刷")
	before := snapshot(t, st)
	res := act(t, a, state{}, nil, "use:"+id)
	toastHas(t, res, "没有数量")
	if snapshot(t, st) != before {
		t.Fatal("a refused use changed the base")
	}
}
