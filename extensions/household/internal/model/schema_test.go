package model

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

var testNow = time.Date(2026, time.November, 10, 9, 30, 0, 0, time.UTC)

// wantProp is one declared property of a type as the design document lists it.
type wantProp struct{ Key, Kind string }

// wantTypes is the schema of docs/extensions/household.md, in the order of Types.
var wantTypes = []struct {
	Key   string
	Name  string
	Flow  []string // stage names in flow order, nil for none
	Props []wantProp
}{
	{TPlace, "位置", nil, []wantProp{
		{"note", kernel.KindText}, {"items", kernel.KindRollup},
	}},
	{TItem, "物品", nil, []wantProp{
		{"category", kernel.KindEnum}, {"place", kernel.KindRelation}, {"qty", kernel.KindNumber},
		{"unit", kernel.KindText}, {"min", kernel.KindNumber}, {"expires", kernel.KindDate},
		{"daily", kernel.KindNumber}, {"counted", kernel.KindDate}, {"lead", kernel.KindNumber},
		{"refill", kernel.KindNumber}, {"note", kernel.KindText},
	}},
	{TBuy, "采购", []string{BuyTodo, BuyDone, BuySkip}, []wantProp{
		{"item", kernel.KindRelation}, {"amount", kernel.KindNumber}, {"unit", kernel.KindText},
		{"category", kernel.KindEnum}, {"reason", kernel.KindEnum}, {"note", kernel.KindText},
	}},
	{TChore, "家务", nil, []wantProp{
		{"every", kernel.KindNumber}, {"last", kernel.KindDate}, {"next", kernel.KindDate},
		{"who", kernel.KindText}, {"place", kernel.KindRelation}, {"minutes", kernel.KindNumber},
		{"note", kernel.KindText},
	}},
	{TRecipe, "菜谱", nil, []wantProp{
		{"needs", kernel.KindRelation}, {"minutes", kernel.KindNumber}, {"url", kernel.KindURL},
		{"note", kernel.KindText},
	}},
	{TMeal, "餐食", []string{MealPlan, MealDone, MealCancel}, []wantProp{
		{"date", kernel.KindDate}, {"slot", kernel.KindEnum}, {"recipe", kernel.KindRelation},
		{"note", kernel.KindText},
	}},
}

// ensured creates a fresh base with the household space in it.
func ensured(t *testing.T) (*kernel.Base, *kernel.Space) {
	t.Helper()
	b := &kernel.Base{Format: 1}
	sp, err := Ensure(b, testNow)
	if err != nil {
		t.Fatalf("Ensure on an empty base: %v", err)
	}
	return b, sp
}

// typeOf returns the type with the key of a space, failing the test when there is none.
func typeOf(t *testing.T, sp *kernel.Space, key string) *kernel.Type {
	t.Helper()
	for _, ty := range sp.Types {
		if ty.Key == key {
			return ty
		}
	}
	t.Fatalf("space has no type %q", key)
	return nil
}

func TestTypesMatchDesign(t *testing.T) {
	types := Types()
	if len(types) != len(wantTypes) {
		t.Fatalf("Types() has %d types, want %d", len(types), len(wantTypes))
	}
	for i, want := range wantTypes {
		got := types[i]
		if got.Key != want.Key || got.Name != want.Name {
			t.Errorf("type %d = %q %q, want %q %q", i, got.Key, got.Name, want.Key, want.Name)
		}
		var flow []string
		for _, st := range got.Flow {
			flow = append(flow, st.Name)
		}
		if !slices.Equal(flow, want.Flow) {
			t.Errorf("%s flow = %v, want %v", want.Key, flow, want.Flow)
		}
		if len(got.Props) != len(want.Props) {
			t.Errorf("%s has %d properties, want %d", want.Key, len(got.Props), len(want.Props))
			continue
		}
		for j, wp := range want.Props {
			if got.Props[j].Key != wp.Key || got.Props[j].Kind != wp.Kind {
				t.Errorf("%s property %d = %s/%s, want %s/%s", want.Key, j,
					got.Props[j].Key, got.Props[j].Kind, wp.Key, wp.Kind)
			}
		}
		if len(got.Views) == 0 {
			t.Errorf("%s declares no view", want.Key)
		}
		if got.Card.Lead == "" && len(got.Card.Pills) == 0 {
			t.Errorf("%s declares no card fields", want.Key)
		}
	}
}

func TestTypesRelationsAndRollups(t *testing.T) {
	types := Types()
	byKey := map[string]kernel.Type{}
	for _, ty := range types {
		byKey[ty.Key] = ty
	}
	relations := []struct {
		Type, Prop, Target, Back string
		Many                     bool
	}{
		{TItem, "place", TPlace, "存放物品", false},
		{TBuy, "item", TItem, "采购记录", false},
		{TChore, "place", TPlace, "家务", false},
		{TRecipe, "needs", TItem, "可做的菜", true},
		{TMeal, "recipe", TRecipe, "排期", false},
	}
	for _, r := range relations {
		var p kernel.Prop
		for _, q := range byKey[r.Type].Props {
			if q.Key == r.Prop {
				p = q
			}
		}
		if p.Target != r.Target || p.Back != r.Back || p.Many != r.Many {
			t.Errorf("%s.%s = target %q back %q many %v; want %q %q %v",
				r.Type, r.Prop, p.Target, p.Back, p.Many, r.Target, r.Back, r.Many)
		}
	}
	items := byKey[TPlace].Props[1]
	if items.Key != "items" || items.Via != TItem+".place" || items.Op != "count" {
		t.Errorf("place.items rollup = via %q op %q, want via item.place op count", items.Via, items.Op)
	}
}

func TestTypesOptionLists(t *testing.T) {
	types := Types()
	byKey := map[string]kernel.Type{}
	for _, ty := range types {
		byKey[ty.Key] = ty
	}
	check := func(typ, prop string, want []string) {
		t.Helper()
		for _, p := range byKey[typ].Props {
			if p.Key == prop {
				if !slices.Equal(p.Options, want) {
					t.Errorf("%s.%s options = %v, want %v", typ, prop, p.Options, want)
				}
				return
			}
		}
		t.Errorf("%s has no property %s", typ, prop)
	}
	check(TItem, "category", Categories)
	check(TBuy, "category", Categories)
	check(TMeal, "slot", Slots)
	check(TBuy, "reason", []string{ReasonLow, ReasonOut, ReasonExpired, ReasonRunout, ReasonManual})
}

func TestEnsureOnEmptyBase(t *testing.T) {
	b, sp := ensured(t)
	if len(b.Spaces) != 1 {
		t.Fatalf("base has %d spaces, want 1", len(b.Spaces))
	}
	if sp.ID != SpaceID || sp.Name != SpaceName || sp.Icon != SpaceIcon {
		t.Errorf("space = %q %q %q, want %q %q %q", sp.ID, sp.Name, sp.Icon, SpaceID, SpaceName, SpaceIcon)
	}
	if len(sp.Types) != len(wantTypes) {
		t.Errorf("space has %d types, want %d", len(sp.Types), len(wantTypes))
	}
	if len(sp.Entities) != 0 {
		t.Errorf("empty base gained %d entities", len(sp.Entities))
	}
	if err := b.Check(); err != nil {
		t.Fatalf("Check after Ensure: %v", err)
	}
	for _, want := range wantTypes {
		ty := typeOf(t, sp, want.Key)
		for _, p := range ty.Props {
			if _, ok := kernel.KindOf(p.Kind); !ok {
				t.Errorf("%s.%s has unknown kind %q", want.Key, p.Key, p.Kind)
			}
		}
	}
}

func TestEnsureTwiceLeavesBaseUnchanged(t *testing.T) {
	b, _ := ensured(t)
	first, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	later := testNow.Add(time.Hour)
	if _, err := Ensure(b, later); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	second, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("second Ensure changed the base:\nfirst:  %s\nsecond: %s", first, second)
	}
}

func TestEnsureAfterEntitiesExist(t *testing.T) {
	b, sp := ensured(t)
	title := "纯牛奶"
	milk, err := sp.PutEntity(b, kernel.EntityInput{Type: TItem, Title: &title, Values: map[string]any{
		"qty": 2, "expires": "2026-11-20", "category": "食材",
	}}, testNow)
	if err != nil {
		t.Fatalf("PutEntity: %v", err)
	}
	before, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	sp, err = Ensure(b, testNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("Ensure with entities: %v", err)
	}
	after, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("Ensure changed a base that holds entities:\nbefore: %s\nafter:  %s", before, after)
	}
	var got *kernel.Entity
	for _, e := range sp.Entities {
		if e.ID == milk.ID {
			got = e
		}
	}
	if got == nil || got.Title != title || !got.Updated.Equal(testNow) {
		t.Errorf("entity after Ensure = %+v, want unchanged", got)
	}
}

func TestEnsureRestoresMissingTypes(t *testing.T) {
	b, sp := ensured(t)
	title := "番茄"
	if _, err := sp.PutEntity(b, kernel.EntityInput{Type: TItem, Title: &title}, testNow); err != nil {
		t.Fatalf("PutEntity: %v", err)
	}
	if err := sp.DropType(TMeal); err != nil {
		t.Fatalf("DropType: %v", err)
	}
	sp, err := Ensure(b, testNow)
	if err != nil {
		t.Fatalf("Ensure with a missing type: %v", err)
	}
	if err := b.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(sp.Types) != len(wantTypes) {
		t.Errorf("space has %d types after Ensure, want %d", len(sp.Types), len(wantTypes))
	}
	typeOf(t, sp, TMeal)
	if len(sp.Entities) != 1 || sp.Entities[0].Title != title {
		t.Errorf("entities after Ensure = %d, want the one item kept", len(sp.Entities))
	}
}

func TestEnsureRepairsChangedType(t *testing.T) {
	b, sp := ensured(t)
	// A recipe type that drifted from the design, still valid for the kernel, is written back to it.
	drifted := typeOf(t, sp, TRecipe)
	drifted.Card = kernel.Card{}
	drifted.Views = nil
	drifted.Props = drifted.Props[:1]
	if err := b.Check(); err != nil {
		t.Fatalf("drifted base is not valid: %v", err)
	}
	sp, err := Ensure(b, testNow)
	if err != nil {
		t.Fatalf("Ensure over a drifted type: %v", err)
	}
	if got := len(typeOf(t, sp, TRecipe).Props); got != 4 {
		t.Errorf("recipe has %d properties after Ensure, want 4", got)
	}
	if got := len(typeOf(t, sp, TRecipe).Views); got == 0 {
		t.Errorf("recipe has no views after Ensure")
	}
}

// TestEnsureKeepsADriftedTypeItsRecordsDoNotFit: a hand-edited type that holds a record the design does not accept is
// kept as it is, the record keeps its value, the other differing types are still restored, and Ensure succeeds.
func TestEnsureKeepsADriftedTypeItsRecordsDoNotFit(t *testing.T) {
	b, sp := ensured(t)
	// The item type gains a category the design lacks, and an item uses it.
	item := *typeOf(t, sp, TItem)
	item.Props = slices.Clone(item.Props)
	for i := range item.Props {
		if item.Props[i].Key == "category" {
			item.Props[i].Options = append(slices.Clone(item.Props[i].Options), "零食")
		}
	}
	if err := sp.PutType(item, testNow); err != nil {
		t.Fatalf("PutType with the extra category: %v", err)
	}
	title := "薯片"
	snack, err := sp.PutEntity(b, kernel.EntityInput{Type: TItem, Title: &title, Values: map[string]any{
		"category": "零食", "qty": 1,
	}}, testNow)
	if err != nil {
		t.Fatalf("PutEntity with the extra category: %v", err)
	}
	// The recipe type drifts too, and must still be restored.
	recipe := *typeOf(t, sp, TRecipe)
	recipe.Card = kernel.Card{}
	recipe.Views = nil
	recipe.Props = recipe.Props[:1]
	if err := sp.PutType(recipe, testNow); err != nil {
		t.Fatalf("PutType with the drifted recipe: %v", err)
	}

	sp, err = Ensure(b, testNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("Ensure over a drifted type its records do not fit: %v", err)
	}
	if err := b.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	kept := typeOf(t, sp, TItem)
	if i := slices.IndexFunc(kept.Props, func(p kernel.Prop) bool { return p.Key == "category" }); i < 0 ||
		!slices.Contains(kept.Props[i].Options, "零食") {
		t.Errorf("drifted item type was restored; want it kept with 零食")
	}
	if got := len(typeOf(t, sp, TRecipe).Props); got != 4 {
		t.Errorf("recipe has %d properties after Ensure, want 4", got)
	}
	var got *kernel.Entity
	for _, e := range sp.Entities {
		if e.ID == snack.ID {
			got = e
		}
	}
	if got == nil || got.Values["category"] != "零食" {
		t.Errorf("record after Ensure = %+v, want its 零食 value kept", got)
	}

	// Another record can still be saved after that: the change is made on a fresh Ensure, as ops does.
	sp, err = Ensure(b, testNow.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	place := "厨房"
	if _, err := sp.PutEntity(b, kernel.EntityInput{Type: TPlace, Title: &place}, testNow.Add(2*time.Hour)); err != nil {
		t.Fatalf("PutEntity on another record after Ensure: %v", err)
	}
}

func TestEnsureSchemaPassesKernelValidation(t *testing.T) {
	b, sp := ensured(t)
	for _, want := range wantTypes {
		ty := typeOf(t, sp, want.Key)
		if len(ty.Views) > kernel.MaxViews || len(ty.Props) > kernel.MaxProps {
			t.Errorf("%s exceeds the kernel's view or property limit", want.Key)
		}
	}
	if err := b.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

// TestEnsureWritesNoTypeWhenTheSchemaMatches: a second Ensure over a space that already matches the design replaces no
// type object, so nothing is written or migrated.
func TestEnsureWritesNoTypeWhenTheSchemaMatches(t *testing.T) {
	b, sp := ensured(t)
	title := "纯牛奶"
	if _, err := sp.PutEntity(b, kernel.EntityInput{Type: TItem, Title: &title, Values: map[string]any{
		"qty": 2, "daily": 0.5, "counted": "2026-11-09",
	}}, testNow); err != nil {
		t.Fatalf("PutEntity: %v", err)
	}
	before := slices.Clone(sp.Types)
	sp, err := Ensure(b, testNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if len(sp.Types) != len(before) {
		t.Fatalf("space has %d types after the second Ensure, want %d", len(sp.Types), len(before))
	}
	for i, ty := range sp.Types {
		if ty != before[i] {
			t.Errorf("type %q was written again by a second Ensure", ty.Key)
		}
	}
	for _, e := range sp.Entities {
		if e.Title == title && !e.Updated.Equal(testNow) {
			t.Errorf("entity %q migrated by a second Ensure: updated %v, want %v", title, e.Updated, testNow)
		}
	}
}
