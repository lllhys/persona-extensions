package ui

import (
	"testing"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
)

// allTabs are the tab values of the nav page, in order.
var allTabs = []string{"over", "stock", "buy", "chore", "meal"}

func TestEveryTabRendersValidViews(t *testing.T) {
	for _, samples := range []bool{true, false} {
		st := newStore(t, samples)
		a, _ := newApp(st)
		for _, tab := range allTabs {
			tab := tab
			label := tab
			if samples {
				label += "/samples"
			} else {
				label += "/empty"
			}
			v := render(t, a, state{T: tabStateOf(tab)})
			checkView(t, label, v, false)
			if v.Tabs == nil {
				t.Fatalf("%s: no tabs", label)
			}
			if v.Tabs.Value != tab {
				t.Fatalf("%s: tab value %q, want %q", label, v.Tabs.Value, tab)
			}
			if len(v.Tabs.Items) != len(allTabs) {
				t.Fatalf("%s: %d tabs, want %d", label, len(v.Tabs.Items), len(allTabs))
			}
			if len(v.Sections) == 0 {
				t.Fatalf("%s: no sections", label)
			}
		}
	}
}

// tabStateOf is the page state's tab for a tab value: the overview has none.
func tabStateOf(tab string) string {
	if tab == "over" {
		return ""
	}
	return tab
}

func TestTabBarNamesFiveTabsInOrder(t *testing.T) {
	a, _ := newApp(newStore(t, false))
	v := render(t, a, state{})
	want := []string{"总览", "库存", "采购", "家务", "餐食"}
	for i, it := range v.Tabs.Items {
		if it.Title != want[i] {
			t.Fatalf("tab %d title %q, want %q", i, it.Title, want[i])
		}
	}
	if v.Tabs.Name != "tab" || v.Tabs.Action.ID != "tab" {
		t.Fatalf("tabs name %q action %q, want tab", v.Tabs.Name, v.Tabs.Action.ID)
	}
}

func TestTabSwitchKeepsStateAndResetsSearch(t *testing.T) {
	st := newStore(t, true)
	a, changed := newApp(st)
	before := snapshot(t, st)

	res := act(t, a, state{T: "", Q: "牛奶"}, map[string]string{"tab": "buy"}, "tab")
	if res.View == nil {
		t.Fatal("tab switch returned no view")
	}
	s := viewState(t, res.View)
	if s.T != "buy" {
		t.Fatalf("tab state %q, want buy", s.T)
	}
	if s.Q != "" {
		t.Fatalf("search kept across tabs: %q", s.Q)
	}
	if res.View.Tabs.Value != "buy" {
		t.Fatalf("tab value %q, want buy", res.View.Tabs.Value)
	}
	if snapshot(t, st) != before {
		t.Fatal("a tab switch changed the stored base")
	}
	if *changed != 0 {
		t.Fatalf("changed hook called %d times by a tab switch", *changed)
	}

	back := act(t, a, state{T: "buy"}, map[string]string{"tab": "over"}, "tab")
	if got := viewState(t, back.View).T; got != "" {
		t.Fatalf("overview tab state %q, want empty", got)
	}
}

func TestUnknownTabIsAToast(t *testing.T) {
	a, _ := newApp(newStore(t, false))
	res := act(t, a, state{T: "stock"}, map[string]string{"tab": "nope"}, "tab")
	toastHas(t, res, "没有")
	if got := viewState(t, res.View).T; got != "stock" {
		t.Fatalf("tab changed to %q by a refused tab", got)
	}
}

func TestActionErrorIsAToastAndChangesNothing(t *testing.T) {
	st := newStore(t, true)
	a, changed := newApp(st)
	before := snapshot(t, st)
	// The sample place is not an item: using it is refused by the transaction.
	placeID := entityID(t, st, model.TPlace, "厨房冰箱")
	res := act(t, a, state{T: ""}, nil, "use:"+placeID)
	if res.Toast == "" {
		t.Fatal("refused action gave no toast")
	}
	if res.View == nil {
		t.Fatal("refused action gave no view")
	}
	if snapshot(t, st) != before {
		t.Fatal("refused action changed the stored base")
	}
	if *changed != 0 {
		t.Fatalf("changed hook called %d times by a refused action", *changed)
	}
}

func TestRefusedActionKeepsThePageState(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	res := act(t, a, state{T: "meal", Q: "番茄"}, map[string]string{"q": "x"}, "delete:e_missing")
	if res.Toast == "" {
		t.Fatal("delete of a missing record gave no toast")
	}
	got := viewState(t, res.View)
	if got.T != "meal" || got.Q != "番茄" {
		t.Fatalf("state after refused action %+v, want the state sent", got)
	}
}

func TestUnknownActionRedrawsWithoutToast(t *testing.T) {
	a, _ := newApp(newStore(t, true))
	res := act(t, a, state{}, nil, "nothing-here")
	if res.Toast != "" {
		t.Fatalf("unknown action toast %q", res.Toast)
	}
	if res.View == nil {
		t.Fatal("unknown action gave no view")
	}
	checkView(t, "unknown action", res.View, false)
}

func TestChangedHookCalledOnlyForWrites(t *testing.T) {
	st := newStore(t, true)
	a, changed := newApp(st)
	act(t, a, state{}, nil, "restock")
	if *changed != 1 {
		t.Fatalf("changed called %d times after a restock, want 1", *changed)
	}
	act(t, a, state{}, nil, "restock")
	if *changed != 1 {
		t.Fatalf("changed called by a restock that created nothing: %d", *changed)
	}
}

func TestSearchAndFilterSetTheState(t *testing.T) {
	a, _ := newApp(newStore(t, true))
	res := act(t, a, state{T: "stock"}, map[string]string{"q": "  纯牛奶  "}, "search")
	if got := viewState(t, res.View).Q; got != "纯牛奶" {
		t.Fatalf("search text %q, want trimmed 纯牛奶", got)
	}
	res = act(t, a, state{T: "stock"}, map[string]string{"category": "=食材"}, "filter")
	if got := viewState(t, res.View).G; got != "食材" {
		t.Fatalf("filter %q, want 食材", got)
	}
}

func TestBackPops(t *testing.T) {
	a, _ := newApp(newStore(t, false))
	res := act(t, a, state{}, nil, "back")
	if !res.Pop || res.View != nil {
		t.Fatalf("back result %+v, want a pop with no view", res)
	}
}

func TestPageSectionsAreNeverNil(t *testing.T) {
	a, _ := newApp(newStore(t, false))
	for _, tab := range allTabs {
		v := render(t, a, state{T: tabStateOf(tab)})
		if v.Sections == nil {
			t.Fatalf("tab %s: sections nil", tab)
		}
	}
}
