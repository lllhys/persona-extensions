package ui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/household/internal/rules"
)

func TestOverviewWithNoRecordsOffersSamplesAndAdd(t *testing.T) {
	a, _ := newApp(newStore(t, false))
	v := render(t, a, state{})
	checkView(t, "empty overview", v, false)
	items := v.Sections[0].Items
	if len(items) != 1 || items[0].Type != wire.ComponentEmpty {
		t.Fatalf("empty overview first section %+v, want one empty component", items)
	}
	empty := items[0]
	if empty.Button == nil || empty.Button.Title != "载入示例" {
		t.Fatalf("empty button %+v, want 载入示例", empty.Button)
	}
	if empty.Button.Action.ID != "load" || empty.Button.Action.Confirm == "" {
		t.Fatalf("载入示例 action %+v, want load with a confirm", empty.Button.Action)
	}
	add, ok := buttonTitled(v, "添加物品")
	if !ok {
		t.Fatal("no 添加物品 button on the empty overview")
	}
	if add.Action == nil || add.Action.Push == nil || add.Action.Push.State == "" {
		t.Fatalf("添加物品 is not a push to the form: %+v", add.Action)
	}
	if hasAction(v, "restock") {
		t.Fatal("empty overview offers 一键补货")
	}
}

func TestOverviewWithSamplesShowsEverySection(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	v := render(t, a, state{})
	checkView(t, "sample overview", v, false)

	for _, title := range []string{"需要补货", "先用掉", "到期家务", "今天吃什么"} {
		if _, ok := sectionTitled(v, title); !ok {
			t.Fatalf("no section %q", title)
		}
	}
	stats := v.Sections[0].Items[0]
	if stats.Type != wire.ComponentStatGrid || len(stats.Stats) != 4 {
		t.Fatalf("first component %+v, want a stat grid of four", stats)
	}
	wantLabels := []string{"需要处理", "待买", "家务到期", "即将过期"}
	for i, s := range stats.Stats {
		if s.Label != wantLabels[i] {
			t.Fatalf("stat %d label %q, want %q", i, s.Label, wantLabels[i])
		}
	}
}

func TestOverviewStatsMatchTheRules(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	h := home(t, st)
	sum := rules.Summarize(h, testToday)
	stats := render(t, a, state{}).Sections[0].Items[0].Stats
	got := map[string]string{}
	for _, s := range stats {
		got[s.Label] = s.Value
	}
	if got["需要处理"] != strconv.Itoa(sum.Alerts) || got["待买"] != strconv.Itoa(sum.OpenBuys) ||
		got["家务到期"] != strconv.Itoa(sum.ChoresDue) || got["即将过期"] != strconv.Itoa(sum.Expiring) {
		t.Fatalf("stats %v do not match summary %+v", got, sum)
	}
}

func TestRestockSectionRowsAreTheBuyableFindings(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	h := home(t, st)
	v := render(t, a, state{})
	sec, ok := sectionTitled(v, "需要补货")
	if !ok {
		t.Fatal("no 需要补货 section")
	}
	var rows []wire.Component
	for _, it := range sec.Items {
		if it.Type == wire.ComponentRow {
			rows = append(rows, it)
		}
	}
	want := 0
	for _, f := range rules.Findings(h, testToday) {
		if f.Kind.Buyable() {
			want++
		}
	}
	if len(rows) != want || want == 0 {
		t.Fatalf("%d rows for 需要补货, want %d (> 0)", len(rows), want)
	}
	for _, r := range rows {
		if r.Badge == nil || r.Badge.Text == "" {
			t.Fatalf("row %q has no kind badge", r.Title)
		}
	}
}

func TestRestockButtonShowsItsCountAndIsHiddenAtZero(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	h := home(t, st)
	want := len(rules.Restock(h, testToday))
	if want == 0 {
		t.Fatal("samples need no restock: the test has nothing to count")
	}
	b, ok := buttonByAction(render(t, a, state{}), "restock")
	if !ok {
		t.Fatal("no 一键补货 button")
	}
	if !strings.Contains(b.Title, strconv.Itoa(want)) {
		t.Fatalf("button %q does not show the %d items it will create", b.Title, want)
	}

	act(t, a, state{}, nil, "restock")
	if hasAction(render(t, a, state{}), "restock") {
		t.Fatal("一键补货 is still offered after every need has a purchase")
	}
}

func TestExpiringSectionUsesDaysUntilExpiry(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	sec, ok := sectionTitled(render(t, a, state{}), "先用掉")
	if !ok {
		t.Fatal("no 先用掉 section")
	}
	for _, it := range sec.Items {
		if it.Type != wire.ComponentRow {
			continue
		}
		if it.Badge == nil || it.Badge.Text != "即将过期" || it.Badge.Tone != wire.ToneCaution {
			t.Fatalf("expiring row %q badge %+v, want 即将过期 caution", it.Title, it.Badge)
		}
		if !strings.Contains(it.Subtitle, "过期") {
			t.Fatalf("expiring row %q subtitle %q does not say when", it.Title, it.Subtitle)
		}
	}
}

func TestChoreRowsCompleteFromTheRowAndOpenTheForm(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	sec, ok := sectionTitled(render(t, a, state{}), "到期家务")
	if !ok {
		t.Fatal("no 到期家务 section")
	}
	rows := 0
	for _, it := range sec.Items {
		if it.Type != wire.ComponentRow {
			continue
		}
		rows++
		if len(it.Actions) != 1 || it.Actions[0].Title != "完成" || !strings.HasPrefix(it.Actions[0].Action.ID, "chore:") {
			t.Fatalf("chore row %q actions %+v, want 完成 as chore:<id>", it.Title, it.Actions)
		}
		if it.Action == nil || it.Action.Push == nil || it.Action.Push.UI != PageUI {
			t.Fatalf("chore row %q does not open its form: %+v", it.Title, it.Action)
		}
	}
	if rows == 0 {
		t.Fatal("no chore due in the samples")
	}
}

func TestChoreRowBadgeSaysOverdueDays(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	sec, _ := sectionTitled(render(t, a, state{}), "到期家务")
	seenOverdue := false
	for _, it := range sec.Items {
		if it.Type != wire.ComponentRow {
			continue
		}
		if it.Badge == nil {
			t.Fatalf("chore row %q has no badge", it.Title)
		}
		if strings.HasPrefix(it.Badge.Text, "逾期") {
			seenOverdue = true
			if it.Badge.Tone != wire.ToneDanger {
				t.Fatalf("overdue chore tone %q, want danger", it.Badge.Tone)
			}
		}
	}
	if !seenOverdue {
		t.Fatal("samples have an overdue chore; no row says 逾期")
	}
}

func TestMealIdeasAreTopThreeWithPlanActions(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	h := home(t, st)
	ideas := rules.MealIdeas(h, testToday, 3)
	sec, ok := sectionTitled(render(t, a, state{}), "今天吃什么")
	if !ok {
		t.Fatal("no 今天吃什么 section")
	}
	var rows []wire.Component
	for _, it := range sec.Items {
		if it.Type == wire.ComponentRow {
			rows = append(rows, it)
		}
	}
	if len(rows) != len(ideas) {
		t.Fatalf("%d meal rows, want %d", len(rows), len(ideas))
	}
	for i, r := range rows {
		if r.Title != ideas[i].Recipe.Title {
			t.Fatalf("row %d title %q, want %q", i, r.Title, ideas[i].Recipe.Title)
		}
		if len(r.Actions) != 1 || r.Actions[0].Title != "排到今晚" {
			t.Fatalf("row %q actions %+v, want 排到今晚", r.Title, r.Actions)
		}
		if want := "plan:" + ideas[i].Recipe.ID; r.Actions[0].Action.ID != want {
			t.Fatalf("row %q plan action %q, want %q", r.Title, r.Actions[0].Action.ID, want)
		}
	}
}

func TestMealSectionSaysWhatIsMissingOrToUseFirst(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	h := home(t, st)
	ideas := rules.MealIdeas(h, testToday, 3)
	sec, _ := sectionTitled(render(t, a, state{}), "今天吃什么")
	for i, it := range sec.Items {
		if it.Type != wire.ComponentRow || i >= len(ideas) {
			continue
		}
		if len(ideas[i].Missing) > 0 && !strings.Contains(it.Subtitle, "缺：") {
			t.Fatalf("row %q subtitle %q does not list what is missing", it.Title, it.Subtitle)
		}
		if len(ideas[i].UseFirst) > 0 && !strings.Contains(it.Subtitle, "先用：") {
			t.Fatalf("row %q subtitle %q does not list what to use first", it.Title, it.Subtitle)
		}
	}
}

func TestOverviewAlwaysOffersAddItem(t *testing.T) {
	st := newStore(t, true)
	a, _ := newApp(st)
	v := render(t, a, state{})
	if _, ok := buttonTitled(v, "添加物品"); !ok {
		t.Fatal("no 添加物品 on a populated overview")
	}
	checkView(t, "populated overview", v, false)
}

func TestSectionsSayWhenEmpty(t *testing.T) {
	st := newStore(t, false)
	a, _ := newApp(st)
	// One record that needs nothing: a fine item with no quantity, no expiry and no threshold.
	act(t, a, state{F: "new:item"}, map[string]string{"title": "牙刷", "p_category": "=日用"}, "save")
	v := render(t, a, state{})
	checkView(t, "quiet overview", v, false)
	for _, title := range []string{"需要补货", "先用掉", "到期家务"} {
		sec, ok := sectionTitled(v, title)
		if !ok {
			t.Fatalf("no %s section", title)
		}
		if len(sec.Items) == 0 || sec.Items[0].Type != wire.ComponentText {
			t.Fatalf("%s with nothing to show has items %+v, want one quiet text", title, sec.Items)
		}
	}
}

// buttonTitled is the first button with the title, anywhere on the view.
func buttonTitled(v *wire.View, title string) (wire.Component, bool) {
	for _, c := range components(v) {
		if c.Type == wire.ComponentButton && c.Title == title {
			return c, true
		}
	}
	return wire.Component{}, false
}

// buttonByAction is the first button whose action has the id.
func buttonByAction(v *wire.View, id string) (wire.Component, bool) {
	for _, c := range components(v) {
		if c.Type == wire.ComponentButton && c.Action != nil && c.Action.ID == id {
			return c, true
		}
	}
	return wire.Component{}, false
}
