package ui

import (
	"context"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
)

// renderCard draws the overview card in state s.
func renderCard(t *testing.T, a *App) *wire.View {
	t.Helper()
	v, err := a.Card().Render(context.Background(), request(state{}, nil, ""))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCardWithNoRecordsIsAnEmptyState(t *testing.T) {
	a, _ := newApp(newStore(t, false))
	v := renderCard(t, a)
	checkView(t, "empty card", v, true)
	if len(v.Sections) != 1 || len(v.Sections[0].Items) != 1 || v.Sections[0].Items[0].Type != wire.ComponentEmpty {
		t.Fatalf("empty card %+v, want one empty component", v.Sections)
	}
}

func TestCardStaysWithinTheCardLimits(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	v := renderCard(t, a)
	checkView(t, "sample card", v, true)
	if len(v.Sections) == 0 || v.Sections[0].Items[0].Type != wire.ComponentStatGrid {
		t.Fatalf("card does not start with the stat grid: %+v", v.Sections)
	}
	if n := len(components(v)); n > 12 || len(v.Sections) > 2 {
		t.Fatalf("card has %d components in %d sections", n, len(v.Sections))
	}
}

func TestCardRowsAreMostUrgentFirstAndHaveNoActions(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	v := renderCard(t, a)
	sec, ok := sectionTitled(v, "需要处理")
	if !ok {
		t.Fatal("no 需要处理 section on the card")
	}
	h := home(t, st)
	findings := rules.Findings(h, testToday)
	if len(findings) == 0 {
		t.Fatal("samples have no finding")
	}
	first := sec.Items[0]
	if first.Title != findings[0].Item.Title {
		t.Fatalf("first card row %q, want the most urgent finding %q", first.Title, findings[0].Item.Title)
	}
	for _, it := range sec.Items {
		if len(it.Actions) != 0 {
			t.Fatalf("card row %q has row actions; a card has none", it.Title)
		}
		if it.Action == nil || it.Action.Push == nil || it.Action.Push.UI != PageUI {
			t.Fatalf("card row %q does not open its page: %+v", it.Title, it.Action)
		}
	}
}

func TestCardHasNoFieldsOrSearch(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	v := renderCard(t, a)
	if v.Search != nil || v.Filter != nil || v.Tabs != nil {
		t.Fatal("card has a search, a filter or tabs")
	}
	for _, c := range components(v) {
		if c.Type == wire.ComponentField {
			t.Fatal("card has a field")
		}
	}
}
