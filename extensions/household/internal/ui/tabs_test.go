package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/ops"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// rowsOf are the row components of a section.
func rowsOf(s wire.Section) []wire.Component {
	var out []wire.Component
	for _, c := range s.Items {
		if c.Type == wire.ComponentRow {
			out = append(out, c)
		}
	}
	return out
}

// rowsTitled are the rows of the section with the title, failing the test when there is no such section.
func rowsTitled(t *testing.T, v *wire.View, title string) []wire.Component {
	t.Helper()
	sec, ok := sectionTitled(v, title)
	if !ok {
		t.Fatalf("no section %q", title)
	}
	return rowsOf(sec)
}

// rowAction is the row action with the title.
func rowAction(c wire.Component, title string) (wire.RowAction, bool) {
	for _, a := range c.Actions {
		if a.Title == title {
			return a, true
		}
	}
	return wire.RowAction{}, false
}

// formID is the record a component opens for editing: the id of its pushed "edit:<id>" form, "" for none.
func formID(a *wire.Action) string {
	if a == nil || a.Push == nil {
		return ""
	}
	var s state
	if err := json.Unmarshal([]byte(a.Push.State), &s); err != nil {
		return ""
	}
	id, ok := strings.CutPrefix(s.F, "edit:")
	if !ok {
		return ""
	}
	return id
}

// bodyFor draws the tab in state s with the samples or an empty household and checks the view.
func bodyFor(t *testing.T, samples bool, s state) *wire.View {
	t.Helper()
	a, _ := newApp(newStore(t, samples))
	v := render(t, a, s)
	checkView(t, fmt.Sprintf("%s samples=%v", s.T, samples), v, false)
	return v
}

// TestStockTabHasSearchAndCategoryFilter checks the stock tab carries the search and the category filter, and the
// other tabs carry neither.
func TestStockTabHasSearchAndCategoryFilter(t *testing.T) {
	v := bodyFor(t, true, state{T: stockTab, Q: "牛奶", G: "食材"})
	if v.Search == nil || v.Search.Value != "牛奶" || v.Search.Action.ID != "search" {
		t.Fatalf("search %+v, want the search text 牛奶 with action search", v.Search)
	}
	if v.Filter == nil || v.Filter.Name != "category" || v.Filter.Value != "=食材" || v.Filter.Action.ID != "filter" {
		t.Fatalf("filter %+v, want category filtered to =食材", v.Filter)
	}
	if len(v.Filter.Choices) != len(model.Categories)+1 {
		t.Fatalf("%d filter choices, want all categories plus 全部", len(v.Filter.Choices))
	}
	for _, tab := range []string{overTab, buyTab, choreTab, mealTab} {
		w := bodyFor(t, true, state{T: tabStateOf(tab)})
		if w.Search != nil || w.Filter != nil {
			t.Fatalf("tab %s has a search or filter", tab)
		}
	}
}

// TestUnknownCategoryIsDroppedFromTheFilter keeps the filter valid when the state names a category that does not exist.
func TestUnknownCategoryIsDroppedFromTheFilter(t *testing.T) {
	v := bodyFor(t, true, state{T: stockTab, G: "不存在"})
	if v.Filter == nil || v.Filter.Value != "=" {
		t.Fatalf("filter %+v, want the unset value", v.Filter)
	}
	if got := viewState(t, v).G; got != "" {
		t.Fatalf("state keeps category %q, want none", got)
	}
}

// TestTabRowsStayWithinTheBudget fills one place with more records than a tab shows and checks the tab stops at the
// budget and says how many it left out.
func TestTabRowsStayWithinTheBudget(t *testing.T) {
	st := newStore(t, false)
	err := st.Update(func(b *kernel.Base, _ time.Time) error {
		for i := range tabRowsMost + 20 {
			title := fmt.Sprintf("测试物品%03d", i)
			if _, err := ops.Save(b, kernel.EntityInput{Type: model.TItem, Title: &title}, testNow, testLoc()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := newApp(st)
	v := render(t, a, state{T: stockTab})
	checkView(t, "budget", v, false)
	shown := 0
	for _, s := range v.Sections {
		shown += len(rowsOf(s))
	}
	if shown != tabRowsMost {
		t.Fatalf("%d rows shown, want the budget of %d", shown, tabRowsMost)
	}
	if !strings.Contains(footerOfAll(v), "只显示前") {
		t.Fatalf("no footer says rows were left out: %q", footerOfAll(v))
	}
}

// footerOfAll joins the footers of the sections of a view.
func footerOfAll(v *wire.View) string {
	var parts []string
	for _, s := range v.Sections {
		if s.Footer != "" {
			parts = append(parts, s.Footer)
		}
	}
	return strings.Join(parts, "|")
}
