package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/ops"
	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/store"
)

// TestStockSectionsArePlacesWithUnplacedLast checks each item sits under its own place, and 未放置 comes last.
func TestStockSectionsArePlacesWithUnplacedLast(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	h := home(t, st)
	v := render(t, a, state{T: stockTab})

	placeOf := map[string]string{}
	for _, p := range h.Places {
		placeOf[p.ID] = p.Title
	}
	for _, p := range h.Places {
		if _, ok := sectionTitled(v, p.Title); !ok {
			t.Fatalf("no section for place %q", p.Title)
		}
	}
	for i, s := range v.Sections {
		if s.Title == unsetText && i != len(v.Sections)-1 {
			t.Fatalf("%s section at %d, want it last", unsetText, i)
		}
	}
	for _, s := range v.Sections {
		for _, r := range rowsOf(s) {
			it, ok := h.Item(formID(r.Action))
			if !ok {
				t.Fatalf("row %q is not an item", r.Title)
			}
			want := placeOf[it.PlaceID]
			if want == "" {
				want = unsetText
			}
			if s.Title != want {
				t.Fatalf("item %q under %q, want %q", it.Title, s.Title, want)
			}
		}
	}
}

// TestStockRowsCarryUseEditAndDelete checks each item row offers 编辑 and 删除 (confirmed), and 用掉 only when the item is
// tracked by quantity.
func TestStockRowsCarryUseEditAndDelete(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	h := home(t, st)
	v := render(t, a, state{T: stockTab})
	seen := 0
	for _, s := range v.Sections {
		for _, r := range rowsOf(s) {
			seen++
			it, ok := h.Item(formID(r.Action))
			if !ok {
				t.Fatalf("row %q does not open an item", r.Title)
			}
			if _, ok := rowAction(r, "编辑"); !ok {
				t.Fatalf("row %q has no 编辑", r.Title)
			}
			del, ok := rowAction(r, "删除")
			if !ok || del.Action.ID != "delete:"+it.ID || del.Action.Confirm == "" || del.Role != wire.ButtonRoleDestructive {
				t.Fatalf("row %q delete action %+v, want a confirmed destructive delete", r.Title, del)
			}
			use, ok := rowAction(r, "用掉 1")
			if ok != it.HasQty {
				t.Fatalf("row %q has 用掉 %v, want %v (tracked by quantity)", r.Title, ok, it.HasQty)
			}
			if ok && use.Action.ID != "use:"+it.ID {
				t.Fatalf("row %q use action %q", r.Title, use.Action.ID)
			}
		}
	}
	if seen != len(h.Items) {
		t.Fatalf("%d item rows, want %d", seen, len(h.Items))
	}
}

// TestStockRowBadgesAreTheFindings checks an item row carries the badge of its finding, and only those rows do.
func TestStockRowBadgesAreTheFindings(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	h := home(t, st)
	labels := map[string]string{}
	for _, f := range rules.Findings(h, testToday) {
		labels[f.Item.ID] = f.Kind.Label()
	}
	if len(labels) == 0 {
		t.Fatal("samples have no findings")
	}
	v := render(t, a, state{T: stockTab})
	for _, s := range v.Sections {
		for _, r := range rowsOf(s) {
			want, has := labels[formID(r.Action)]
			switch {
			case !has && r.Badge != nil:
				t.Fatalf("row %q has badge %q and no finding", r.Title, r.Badge.Text)
			case has && (r.Badge == nil || r.Badge.Text != want):
				t.Fatalf("row %q badge %+v, want %q", r.Title, r.Badge, want)
			}
		}
	}
}

// TestStockSearchNarrowsTheRows checks the search matches the title, and a search that finds nothing shows no rows.
func TestStockSearchNarrowsTheRows(t *testing.T) {
	a, _ := newApp(newStore(t, true))
	v := render(t, a, state{T: stockTab, Q: "奶"})
	checkView(t, "search 奶", v, false)
	rows := 0
	for _, s := range v.Sections {
		for _, r := range rowsOf(s) {
			rows++
			if !strings.Contains(r.Title, "奶") {
				t.Fatalf("row %q does not match 奶", r.Title)
			}
		}
	}
	if rows == 0 {
		t.Fatal("search 奶 finds no rows in the samples")
	}

	v = render(t, a, state{T: stockTab, Q: "不存在的东西"})
	checkView(t, "search nothing", v, false)
	for _, s := range v.Sections {
		if len(rowsOf(s)) != 0 {
			t.Fatalf("section %q shows rows for a search that finds nothing", s.Title)
		}
	}
}

// TestStockCategoryFilterNarrowsTheRows checks the filter keeps only the items of the category.
func TestStockCategoryFilterNarrowsTheRows(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	h := home(t, st)
	v := render(t, a, state{T: stockTab, G: model.CategoryBaby})
	checkView(t, "filter 母婴", v, false)
	rows := 0
	for _, s := range v.Sections {
		for _, r := range rowsOf(s) {
			rows++
			it, _ := h.Item(formID(r.Action))
			if it.Category != model.CategoryBaby {
				t.Fatalf("row %q is %q, want only 母婴", r.Title, it.Category)
			}
		}
	}
	if rows == 0 {
		t.Fatal("the samples have no 母婴 item")
	}
}

// TestStockOffersAddButtons checks the add buttons are on the tab with samples, and an empty household says so.
func TestStockOffersAddButtons(t *testing.T) {
	for _, samples := range []bool{true, false} {
		a, _ := newApp(newStore(t, samples))
		v := render(t, a, state{T: stockTab})
		checkView(t, "stock add", v, false)
		for _, title := range []string{"添加物品", "添加位置"} {
			b, ok := buttonTitled(v, title)
			if !ok {
				t.Fatalf("samples=%v: no %s button", samples, title)
			}
			if b.Action == nil || b.Action.Push == nil {
				t.Fatalf("%s is not a push to its form: %+v", title, b.Action)
			}
		}
		if !samples {
			if _, ok := sectionTitled(v, unsetText); ok {
				t.Fatal("empty household shows a 未放置 section")
			}
			if !strings.Contains(v.Sections[0].Items[0].Text, "还没有物品") {
				t.Fatalf("empty stock first line %+v, want 还没有物品", v.Sections[0].Items[0])
			}
		}
	}
}

// stockedPlaces is a household of made-up places: place i holds itemsIn[i] made-up items, and unplaced items have
// no place.
func stockedPlaces(t *testing.T, itemsIn []int, unplaced int) *store.Store {
	t.Helper()
	st := store.Open("")
	err := st.Update(func(b *kernel.Base, _ time.Time) error {
		for i, n := range itemsIn {
			p, err := ops.Save(b, kernel.EntityInput{Type: model.TPlace, Title: strPtr(fmt.Sprintf("位置%03d", i))}, testNow, testLoc())
			if err != nil {
				return err
			}
			for j := range n {
				title := fmt.Sprintf("物品%03d-%03d", i, j)
				if _, err := ops.Save(b, kernel.EntityInput{Type: model.TItem, Title: &title,
					Values: map[string]any{"place": []string{p.ID}}}, testNow, testLoc()); err != nil {
					return err
				}
			}
		}
		for j := range unplaced {
			title := fmt.Sprintf("未放物品%03d", j)
			if _, err := ops.Save(b, kernel.EntityInput{Type: model.TItem, Title: &title}, testNow, testLoc()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// strPtr is a pointer to the string, for the optional title of an input.
func strPtr(s string) *string { return &s }

// TestStockFooterSaysWhatTheBudgetLeftOut checks the places cut off by the row budget are said to be left out, with
// the number of items, instead of vanishing from the tab.
func TestStockFooterSaysWhatTheBudgetLeftOut(t *testing.T) {
	a, _ := newApp(stockedPlaces(t, []int{30, 30, 30, 30, 30}, 0))
	v := render(t, a, state{T: stockTab})
	checkView(t, "five crowded places", v, false)
	if _, ok := sectionTitled(v, "位置004"); ok {
		t.Fatal("place 4 is shown although the row budget is spent")
	}
	if s, ok := sectionTitled(v, "位置003"); !ok || s.Footer == "" {
		t.Fatalf("place 3 section %+v, want a footer that it is cut", s)
	}
	found := false
	for _, c := range components(v) {
		if strings.Contains(c.Text, "还有 50 件物品没有显示") {
			found = true
		}
	}
	if !found {
		t.Fatal("no line says that 50 items are not shown")
	}
}

// TestStockManyPlacesStayWithinTheSDKLimits checks a household with a few hundred places passes the view validation,
// with the empty places listed up to a small cap.
func TestStockManyPlacesStayWithinTheSDKLimits(t *testing.T) {
	counts := make([]int, 300)
	for i := range counts {
		if i%3 == 0 {
			counts[i] = 1
		}
	}
	cases := map[string][]int{"mixed": counts, "every place with items": slices.Repeat([]int{1}, 300)}
	for name, c := range cases {
		a, _ := newApp(stockedPlaces(t, c, 2))
		v := render(t, a, state{T: stockTab})
		checkView(t, name, v, false)
		empty := 0
		for _, s := range v.Sections {
			if len(s.Items) == 1 && s.Items[0].Text == "没有物品" {
				empty++
			}
		}
		if empty > emptyPlacesMost {
			t.Fatalf("%s: %d empty places listed, want at most %d", name, empty, emptyPlacesMost)
		}
	}
}
