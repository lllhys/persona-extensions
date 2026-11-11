package ui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/ops"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/store"
)

// TestPurchasesOpenSectionOffersBuyAndSkip checks the 待买 rows are the open purchases, each with 已买 and 不买.
func TestPurchasesOpenSectionOffersBuyAndSkip(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	h := home(t, st)
	v := render(t, a, state{T: buyTab})
	checkView(t, "purchases", v, false)

	open := 0
	for _, b := range h.Buys {
		if b.Status == model.BuyTodo {
			open++
		}
	}
	rows := rowsTitled(t, v, "待买")
	if len(rows) != open || open == 0 {
		t.Fatalf("%d open rows, want %d (> 0)", len(rows), open)
	}
	for _, r := range rows {
		buy, ok := buyByID(h, formID(r.Action))
		if !ok || buy.Status != model.BuyTodo {
			t.Fatalf("row %q is not an open purchase", r.Title)
		}
		done, ok := rowAction(r, "已买")
		if !ok || done.Action.ID != "buy:"+buy.ID {
			t.Fatalf("row %q 已买 action %+v", r.Title, done)
		}
		skip, ok := rowAction(r, "不买")
		if !ok || skip.Action.ID != "skip:"+buy.ID {
			t.Fatalf("row %q 不买 action %+v", r.Title, skip)
		}
	}
}

// buyByID is the purchase with the id.
func buyByID(h *model.Home, id string) (model.Buy, bool) {
	for _, b := range h.Buys {
		if b.ID == id {
			return b, true
		}
	}
	return model.Buy{}, false
}

// TestRecentlyBoughtListsBoughtPurchases checks buying a purchase moves it from 待买 to 最近已买, without 已买 on the row.
func TestRecentlyBoughtListsBoughtPurchases(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	h := home(t, st)
	open := 0
	for _, b := range h.Buys {
		if b.Status == model.BuyTodo {
			open++
		}
	}
	var first string
	for _, b := range h.Buys {
		if b.Status == model.BuyTodo {
			first = b.ID
			break
		}
	}
	res := act(t, a, state{T: buyTab}, nil, "buy:"+first)
	v := res.View
	if v == nil {
		t.Fatal("no view after buying")
	}
	bought := rowsTitled(t, v, "最近已买")
	if len(bought) != 1 {
		t.Fatalf("%d recently bought rows, want 1", len(bought))
	}
	for _, r := range bought {
		if _, ok := rowAction(r, "已买"); ok {
			t.Fatalf("bought row %q still offers 已买", r.Title)
		}
	}
	if got := rowsTitled(t, v, "待买"); len(got) != open-1 {
		t.Fatalf("%d open rows after buying one, want %d", len(got), open-1)
	}
}

// TestBuyClosesThePurchaseAsBought checks 已买 moves the purchase out of the open list.
func TestBuyClosesThePurchaseAsBought(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	id := entityID(t, st, model.TBuy, "洗衣液")
	res := act(t, a, state{T: buyTab}, nil, "buy:"+id)
	toastHas(t, res, "已买")
	for _, b := range home(t, st).Buys {
		if b.ID == id && b.Status != model.BuyDone {
			t.Fatalf("purchase is %q, want 已买", b.Status)
		}
	}
}

// TestSkipClosesThePurchaseAsDropped checks 不买 closes the purchase without stocking anything.
func TestSkipClosesThePurchaseAsDropped(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	id := entityID(t, st, model.TBuy, "洗衣液")
	before := len(home(t, st).Items)
	res := act(t, a, state{T: buyTab}, nil, "skip:"+id)
	toastHas(t, res, "不买")
	for _, b := range home(t, st).Buys {
		if b.ID == id && b.Status != model.BuySkip {
			t.Fatalf("purchase is %q, want 不买", b.Status)
		}
	}
	if len(home(t, st).Items) != before {
		t.Fatal("不买 changed the items")
	}
	if got := rowsTitled(t, res.View, "待买"); len(got) != 0 {
		t.Fatalf("%d open rows after 不买 of the only one, want 0", len(got))
	}
}

// TestPurchasesRestockButtonCountsTheNeeds checks 一键补货 shows how many it will create, and goes once none is left.
func TestPurchasesRestockButtonCountsTheNeeds(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	want := len(rules.Restock(home(t, st), testToday))
	if want == 0 {
		t.Fatal("samples need no restock")
	}
	v := render(t, a, state{T: buyTab})
	b, ok := buttonByAction(v, "restock")
	if !ok {
		t.Fatal("no 一键补货 button on the purchases tab")
	}
	if !strings.Contains(b.Title, strconv.Itoa(want)) {
		t.Fatalf("button %q does not show %d", b.Title, want)
	}
	act(t, a, state{T: buyTab}, nil, "restock")
	if _, ok := buttonByAction(render(t, a, state{T: buyTab}), "restock"); ok {
		t.Fatal("一键补货 still offered after every need has a purchase")
	}
}

// TestPurchasesOffersAddAndEmptyState checks the add button pushes the purchase form, and an empty household says so.
func TestPurchasesOffersAddAndEmptyState(t *testing.T) {
	a, _ := newApp(newStore(t, false))
	v := render(t, a, state{T: buyTab})
	checkView(t, "empty purchases", v, false)
	b, ok := buttonTitled(v, "添加采购")
	if !ok || b.Action == nil || b.Action.Push == nil {
		t.Fatalf("添加采购 button %+v, want a push to its form", b)
	}
	if got := rowsTitled(t, v, "待买"); len(got) != 0 {
		t.Fatalf("%d open rows in an empty household", len(got))
	}
}

// TestRecentlyBoughtIsTheLatestBoughtNewestFirst checks 最近已买 takes the ten purchases bought last, by the time they
// were bought, not the ten created last: the first purchase created is bought last and is listed first.
func TestRecentlyBoughtIsTheLatestBoughtNewestFirst(t *testing.T) {
	st := store.Open("")
	const n = 12
	var ids []string
	err := st.Update(func(b *kernel.Base, _ time.Time) error {
		for i := range n {
			title := fmt.Sprintf("采购%02d", i)
			e, err := ops.Save(b, kernel.EntityInput{Type: model.TBuy, Title: &title}, testNow, testLoc())
			if err != nil {
				return err
			}
			ids = append(ids, e.ID)
		}
		// Purchases 1 to 11 are bought in creation order, a minute apart; purchase 0 is bought last.
		for i := 1; i < n; i++ {
			if _, err := ops.Purchase(b, ids[i], true, testNow.Add(time.Duration(i)*time.Minute), testLoc()); err != nil {
				return err
			}
		}
		_, err := ops.Purchase(b, ids[0], true, testNow.Add(time.Hour), testLoc())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := newApp(st)
	bought := rowsTitled(t, render(t, a, state{T: buyTab}), "最近已买")
	if len(bought) != recentBought {
		t.Fatalf("%d recently bought rows, want %d", len(bought), recentBought)
	}
	if got := formID(bought[0].Action); got != ids[0] {
		t.Fatalf("first recently bought is %q, want the purchase bought last (%q)", got, ids[0])
	}
	if got := formID(bought[len(bought)-1].Action); got != ids[3] {
		t.Fatalf("last recently bought is %q, want the tenth latest bought (%q)", got, ids[3])
	}
}
