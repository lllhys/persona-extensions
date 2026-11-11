package model

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

var relativeDay = regexp.MustCompile(`^([+-])(\d+)d$`)

// resolveValue turns one sample value into the value the kernel stores: "@ref" becomes the id of the entity created
// for that ref (in a []string too) and "+Nd" / "-Nd" becomes the day N days from today. Other values stay as they are.
func resolveValue(v any, ids map[string]string, today Day) (any, error) {
	switch x := v.(type) {
	case string:
		if ref, ok := strings.CutPrefix(x, "@"); ok {
			id, found := ids[ref]
			if !found {
				return nil, fmt.Errorf("unresolved ref %q", x)
			}
			return id, nil
		}
		if m := relativeDay.FindStringSubmatch(x); m != nil {
			n, err := strconv.Atoi(m[2])
			if err != nil {
				return nil, err
			}
			if m[1] == "-" {
				n = -n
			}
			return string(today.Add(n)), nil
		}
		return x, nil
	case []string:
		out := make([]string, 0, len(x))
		for _, s := range x {
			r, err := resolveValue(s, ids, today)
			if err != nil {
				return nil, err
			}
			out = append(out, r.(string))
		}
		return out, nil
	}
	return v, nil
}

// materialize creates every sample through the kernel, in order, and returns the ids by ref.
func materialize(t *testing.T, b *kernel.Base, sp *kernel.Space, today Day, samples []Sample) map[string]string {
	t.Helper()
	ids := map[string]string{}
	now := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	for _, s := range samples {
		vals := map[string]any{}
		for k, v := range s.Values {
			r, err := resolveValue(v, ids, today)
			if err != nil {
				t.Fatalf("sample %s, value %s: %v", s.Ref, k, err)
			}
			vals[k] = r
		}
		title := s.Title
		in := kernel.EntityInput{Type: s.Type, Title: &title, Values: vals}
		if s.Status != "" {
			st := s.Status
			in.Status = &st
		}
		e, err := sp.PutEntity(b, in, now)
		if err != nil {
			t.Fatalf("kernel refused sample %s (%s): %v", s.Ref, s.Title, err)
		}
		ids[s.Ref] = e.ID
	}
	return ids
}

func TestSamplesUseDeclaredTypesPropsAndOptions(t *testing.T) {
	types := map[string]kernel.Type{}
	for _, ty := range Types() {
		types[ty.Key] = ty
	}
	seen := map[string]string{} // ref -> type key of the earlier samples
	for _, s := range Samples() {
		if s.Ref == "" || s.Title == "" {
			t.Errorf("sample %+v has no ref or title", s)
		}
		if _, dup := seen[s.Ref]; dup {
			t.Errorf("sample ref %q is used twice", s.Ref)
		}
		ty, ok := types[s.Type]
		if !ok {
			t.Errorf("sample %s has undeclared type %q", s.Ref, s.Type)
			continue
		}
		if len(ty.Flow) == 0 && s.Status != "" {
			t.Errorf("sample %s has a status but %s has no flow", s.Ref, s.Type)
		}
		if len(ty.Flow) > 0 && s.Status != "" {
			if _, ok := stageOf(ty, s.Status); !ok {
				t.Errorf("sample %s status %q is not a stage of %s", s.Ref, s.Status, s.Type)
			}
		}
		for key, v := range s.Values {
			prop, ok := propOfType(ty, key)
			if !ok {
				t.Errorf("sample %s sets undeclared property %s.%s", s.Ref, s.Type, key)
				continue
			}
			checkSampleValue(t, s, prop, v, seen)
		}
		seen[s.Ref] = s.Type
	}
}

// checkSampleValue checks one sample value against the kind, options and relation target of its property. A ref
// must name an earlier sample of the target type.
func checkSampleValue(t *testing.T, s Sample, p kernel.Prop, v any, seen map[string]string) {
	t.Helper()
	switch p.Kind {
	case kernel.KindText:
		if _, ok := v.(string); !ok {
			t.Errorf("sample %s %s is not text: %#v", s.Ref, p.Key, v)
		}
	case kernel.KindNumber:
		switch v.(type) {
		case int, float64:
		default:
			t.Errorf("sample %s %s is not a number: %#v", s.Ref, p.Key, v)
		}
	case kernel.KindURL:
		str, _ := v.(string)
		if !strings.HasPrefix(str, "https://example.com/") {
			t.Errorf("sample %s %s = %q is not a made-up example.com link", s.Ref, p.Key, str)
		}
	case kernel.KindDate:
		str, _ := v.(string)
		if m := relativeDay.FindStringSubmatch(str); m == nil {
			if _, ok := ParseDay(str); !ok {
				t.Errorf("sample %s %s is not a date: %#v", s.Ref, p.Key, v)
			}
		}
	case kernel.KindEnum:
		str, _ := v.(string)
		if !slices.Contains(p.Options, str) {
			t.Errorf("sample %s %s = %q is not one of %v", s.Ref, p.Key, str, p.Options)
		}
	case kernel.KindRelation:
		var refs []string
		switch x := v.(type) {
		case string:
			refs = []string{x}
		case []string:
			refs = x
		default:
			t.Errorf("sample %s %s is not a ref: %#v", s.Ref, p.Key, v)
			return
		}
		if !p.Many && len(refs) > 1 {
			t.Errorf("sample %s %s holds %d targets, one is allowed", s.Ref, p.Key, len(refs))
		}
		for _, r := range refs {
			ref, ok := strings.CutPrefix(r, "@")
			if !ok {
				t.Errorf("sample %s %s = %q is not an @ref", s.Ref, p.Key, r)
				continue
			}
			typ, found := seen[ref]
			if !found {
				t.Errorf("sample %s %s: @%s does not name an earlier sample", s.Ref, p.Key, ref)
			} else if typ != p.Target {
				t.Errorf("sample %s %s: @%s is a %s, want %s", s.Ref, p.Key, ref, typ, p.Target)
			}
		}
	default:
		t.Errorf("sample %s %s has kind %q, which samples must not set", s.Ref, p.Key, p.Kind)
	}
}

func stageOf(ty kernel.Type, name string) (kernel.Stage, bool) {
	for _, st := range ty.Flow {
		if st.Name == name {
			return st, true
		}
	}
	return kernel.Stage{}, false
}

func propOfType(ty kernel.Type, key string) (kernel.Prop, bool) {
	for _, p := range ty.Props {
		if p.Key == key {
			return p, true
		}
	}
	return kernel.Prop{}, false
}

func TestSamplesAreAcceptedByKernel(t *testing.T) {
	b, sp := ensured(t)
	today := DayOf(testNow, time.UTC)
	ids := materialize(t, b, sp, today, Samples())
	if err := b.Check(); err != nil {
		t.Fatalf("Check after samples: %v", err)
	}
	if len(ids) != len(Samples()) {
		t.Errorf("created %d records, want %d", len(ids), len(Samples()))
	}
}

func TestSamplesCount(t *testing.T) {
	b, sp := ensured(t)
	materialize(t, b, sp, DayOf(testNow, time.UTC), Samples())
	h := Load(sp, time.UTC)
	counts := map[string]int{
		"places": len(h.Places), "items": len(h.Items), "buys": len(h.Buys),
		"chores": len(h.Chores), "recipes": len(h.Recipes), "meals": len(h.Meals),
	}
	want := map[string]int{"places": 3, "items": 10, "buys": 1, "chores": 3, "recipes": 3, "meals": 2}
	for k, n := range want {
		if counts[k] != n {
			t.Errorf("%s = %d, want %d", k, counts[k], n)
		}
	}
}

// estimateOf is the rule of the design for the sample checks: daily use since the counting day, never below zero.
func estimateOf(it Item, today Day) float64 {
	if !it.HasQty {
		return 0
	}
	if it.Daily <= 0 {
		return it.Qty
	}
	since := it.Counted
	if since == "" {
		since = it.Updated
	}
	est := it.Qty - it.Daily*float64(today.Sub(since))
	if est < 0 {
		return 0
	}
	return est
}

// leadOf is the warning window of the design: the item's lead, else 7 days for baby items and 3 for the rest.
func leadOf(it Item) int {
	if it.Lead > 0 {
		return int(it.Lead)
	}
	if it.Category == CategoryBaby {
		return 7
	}
	return 3
}

func TestSamplesCoverEveryFinding(t *testing.T) {
	b, sp := ensured(t)
	today := DayOf(testNow, time.UTC)
	materialize(t, b, sp, today, Samples())
	h := Load(sp, time.UTC)

	kinds := map[string][]Item{}
	for _, it := range h.Items {
		est := estimateOf(it, today)
		var kind string
		switch {
		case it.Expires != "" && it.Expires < today:
			kind = "expired"
		case it.HasQty && est == 0:
			kind = "out"
		case it.HasMin && est <= it.Min:
			kind = "low"
		case it.Daily > 0 && it.HasQty && est/it.Daily <= float64(leadOf(it)):
			kind = "runout"
		case it.Expires != "" && today.Sub(it.Expires) >= -leadOf(it) && it.Expires >= today:
			kind = "expiring"
		default:
			kind = "fine"
		}
		kinds[kind] = append(kinds[kind], it)
	}
	for _, k := range []string{"expired", "out", "low", "runout", "expiring", "fine"} {
		if len(kinds[k]) == 0 {
			t.Errorf("no sample item gives the %s finding or stays fine", k)
		}
	}
	for _, it := range kinds["runout"] {
		if it.Category != CategoryBaby {
			t.Errorf("runout sample %q is %q, want a baby item", it.Title, it.Category)
		}
	}
}

func TestSamplesCoverChoresAndRecipes(t *testing.T) {
	b, sp := ensured(t)
	today := DayOf(testNow, time.UTC)
	materialize(t, b, sp, today, Samples())
	h := Load(sp, time.UTC)

	var overdue, dueToday, later int
	for _, c := range h.Chores {
		switch {
		case c.Next == "":
		case c.Next < today:
			overdue++
		case c.Next == today:
			dueToday++
		default:
			later++
		}
	}
	if overdue != 1 || dueToday != 1 || later != 1 {
		t.Errorf("chores overdue %d, due today %d, later %d; want 1 each", overdue, dueToday, later)
	}

	itemOf := func(id string) (Item, bool) { return h.Item(id) }
	var fullyAtHand, usesExpiring, missing int
	for _, r := range h.Recipes {
		if len(r.Needs) == 0 {
			t.Errorf("recipe %q has no needs", r.Title)
		}
		atHand, expiring := true, false
		for _, id := range r.Needs {
			it, ok := itemOf(id)
			if !ok {
				t.Fatalf("recipe %q needs an unknown item %s", r.Title, id)
			}
			if it.Expires != "" && it.Expires < today || estimateOf(it, today) == 0 && it.HasQty {
				atHand = false
			}
			if it.Expires != "" && it.Expires >= today && today.Sub(it.Expires) >= -leadOf(it) {
				expiring = true
			}
		}
		switch {
		case atHand && !expiring:
			fullyAtHand++
		case atHand && expiring:
			usesExpiring++
		default:
			missing++
		}
	}
	if fullyAtHand < 1 || usesExpiring < 1 || missing < 1 {
		t.Errorf("recipes fully at hand %d, using an expiring item %d, missing one %d; want each at least 1",
			fullyAtHand, usesExpiring, missing)
	}
	var manual int
	for _, bu := range h.Buys {
		if bu.Reason == ReasonManual && bu.Status == BuyTodo {
			manual++
		}
	}
	if manual != 1 {
		t.Errorf("manual open purchases = %d, want 1", manual)
	}
	var planned int
	for _, m := range h.Meals {
		if m.Status == MealPlan {
			planned++
		}
		if _, ok := h.Recipe(m.RecipeID); !ok {
			t.Errorf("meal %q points to no recipe", m.Title)
		}
	}
	if planned == 0 {
		t.Error("no planned meal in the samples")
	}
}
