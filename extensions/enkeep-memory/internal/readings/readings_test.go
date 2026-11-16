package readings

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

const day = "2026-11-10"

var at = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

func yes() *bool { b := true; return &b }

func newState() *store.State {
	return &store.State{Entries: map[string]*entry.Entry{}, Switches: map[string]entry.SwitchSetting{}}
}

// TestR4CountsOnlyTheUsersOwnAnswers: the hub #5876 reading. Of the cards with a recommendation, adoptions are the
// user's own answers that chose it; answered_by absent is the user; an expired card answered late is the user's;
// the assistant's answers for the user and defaults at the deadline are counted apart and never adoptions.
func TestR4CountsOnlyTheUsersOwnAnswers(t *testing.T) {
	cards := []hubapi.Interaction{
		{ID: "c1", At: at, Recommend: "option-a", Answers: []string{"option-a"}, AnsweredBy: "user"},                       // adopted
		{ID: "c2", At: at, Recommend: "option-a", Answers: []string{"option-b"}},                                           // user (absent), not adopted
		{ID: "c3", At: at, Recommend: "option-a", Answers: []string{"option-a"}, Status: "expired", Late: true},            // late: user, adopted
		{ID: "c4", At: at, Recommend: "option-a", Answers: []string{"option-a"}, AnsweredBy: "assistant"},                  // apart
		{ID: "c5", At: at, Recommend: "option-a", Answers: []string{"option-a"}, AnsweredBy: "default", Status: "expired"}, // apart
		{ID: "c6", At: at, Recommend: "option-a", Status: "expired"},                                                       // unanswered
		{ID: "c7", At: at, Answers: []string{"option-a"}},                                                                  // no recommendation: not a chance
		{ID: "c8", At: at.AddDate(0, 0, -30), Recommend: "option-a", Answers: []string{"option-a"}},                        // outside the period
		{ID: "c9", At: at, Recommend: "option-a", Answers: []string{"option-a"}, Private: true},                            // private: never read
	}
	r := R4Of(newState(), cards, time.UTC, DaySet(day))
	if r.WithRecommend != 6 || r.User != (Ratio{Num: 2, Den: 3}) || r.Late != 1 || r.Assistant != 1 || r.Default != 1 || r.Unanswered != 1 {
		t.Fatalf("got %+v", r)
	}
	if r.ByType[Unclassified] != (Ratio{Num: 2, Den: 3}) {
		t.Fatalf("by type: %+v", r.ByType)
	}
}

// TestR4ByForkType: a card maps to the judgement class its answer was filed under (the class's type), by the
// evidence of the action entry in the card's unit on the card's day; a card no class holds is unclassified.
func TestR4ByForkType(t *testing.T) {
	st := newState()
	st.Entries["j-class"] = &entry.Entry{ID: "j-class", Facet: entry.Judgement, Grain: entry.Class, Type: "rollout-pace", Members: []string{"j-act"}}
	st.Entries["j-act"] = &entry.Entry{ID: "j-act", Facet: entry.Judgement, Grain: entry.Action, Parent: "j-class",
		Evidence: []entry.Evidence{{Ref: "asks/" + day, Day: day, Quote: "选了：Ship to project-x first"}}}
	st.Entries["j-sess"] = &entry.Entry{ID: "j-sess", Facet: entry.Judgement, Grain: entry.Action, Type: "lonely",
		Evidence: []entry.Evidence{{Ref: "agents/local/sess-1", Day: day, Quote: "user-a: keep the old config"}}}
	cards := []hubapi.Interaction{
		{ID: "a", At: at, Recommend: "Ship to project-x first", Answers: []string{"Ship to project-x first"}},
		{ID: "b", At: at, Session: "sess-1", Recommend: "Use the new config", Answers: []string{"keep the old config"}},
		{ID: "c", At: at, Recommend: "x1", Answers: []string{"something nobody quoted"}},
	}
	r := R4Of(st, cards, time.UTC, DaySet(day))
	if r.ByType["rollout-pace"] != (Ratio{1, 1}) {
		t.Errorf("rollout-pace: %+v", r.ByType)
	}
	// j-sess has no class: its card is unclassified, not filed under the action's own type.
	if r.ByType[Unclassified] != (Ratio{0, 2}) {
		t.Errorf("unclassified: %+v", r.ByType)
	}
}

func typedCards(n, adopted int, reversible *bool) []hubapi.Interaction {
	var cs []hubapi.Interaction
	for i := 0; i < n; i++ {
		ans := "keep"
		if i < adopted {
			ans = "go"
		}
		cs = append(cs, hubapi.Interaction{ID: fmt.Sprint(i), At: at, Recommend: "go", Answers: []string{ans}, Reversible: reversible,
			Question: fmt.Sprintf("question number %02d about project-x", i)})
	}
	return cs
}

func typedState(n int) *store.State {
	st := newState()
	cls := &entry.Entry{ID: "j-class", Facet: entry.Judgement, Grain: entry.Class, Type: "retry-or-stop"}
	st.Entries[cls.ID] = cls
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("j-%d", i)
		st.Entries[id] = &entry.Entry{ID: id, Facet: entry.Judgement, Grain: entry.Action, Parent: cls.ID,
			Evidence: []entry.Evidence{{Ref: "asks/" + day, Day: day, Quote: fmt.Sprintf("问「question number %02d about project-x」", i)}}}
		cls.Members = append(cls.Members, id)
	}
	return st
}

// TestAutoK2OnlyOnReversibleCards: K2 opens by itself only for a measurable type whose reversible cards were
// adopted ≥10 times at ≥90% with none overturned; cards that did not say they are reversible never count.
func TestAutoK2OnlyOnReversibleCards(t *testing.T) {
	key := entry.K2Judgement + ".retry-or-stop"
	// Unknown reversibility: 20 adoptions, nothing opens.
	st := typedState(20)
	r := R4Of(st, typedCards(20, 20, nil), time.UTC, DaySet(day))
	if r.ByType["retry-or-stop"] != (Ratio{20, 20}) || r.Reversible["retry-or-stop"].Den != 0 {
		t.Fatalf("got %+v", r)
	}
	if ch := AutoK2(st, r, nil, day); len(ch) != 0 {
		t.Fatalf("opened on cards of unknown reversibility: %v", ch)
	}
	// Reversible and adopted 19 of 20: opens.
	r = R4Of(st, typedCards(20, 19, yes()), time.UTC, DaySet(day))
	if ch := AutoK2(st, r, map[string]int{}, day); len(ch) != 1 || st.Switches[key].Value != "act" || st.Switches[key].By != "auto" {
		t.Fatalf("not opened: %v %+v", ch, st.Switches)
	}
	// An overturn closes the automatic opening.
	if ch := AutoK2(st, r, map[string]int{"retry-or-stop": 1}, day); len(ch) != 1 || st.Switches[key].Value != "suggest" {
		t.Fatalf("not closed: %v %+v", ch, st.Switches)
	}
	// Below 90%, or not measurable (<20 chances): stays shut.
	st = typedState(20)
	if ch := AutoK2(st, R4Of(st, typedCards(20, 17, yes()), time.UTC, DaySet(day)), nil, day); len(ch) != 0 {
		t.Fatalf("opened at 85%%: %v", ch)
	}
	st = typedState(12)
	if ch := AutoK2(st, R4Of(st, typedCards(12, 12, yes()), time.UTC, DaySet(day)), nil, day); len(ch) != 0 {
		t.Fatalf("opened on 12 chances: %v", ch)
	}
	// The user's setting is never touched.
	st = typedState(20)
	st.Switches[key] = entry.SwitchSetting{Value: "off", By: "user"}
	if ch := AutoK2(st, R4Of(st, typedCards(20, 20, yes()), time.UTC, DaySet(day)), nil, day); len(ch) != 0 || st.Switches[key].Value != "off" {
		t.Fatalf("overrode the user: %v", ch)
	}
}

// TestComputeEveryFacetSeparately: every facet has its own reading; too few chances read "量不出"; nothing missing
// is filled from another facet.
func TestComputeEveryFacetSeparately(t *testing.T) {
	st := typedState(20)
	// One correction after the recommendation was taken (an overturn), one before it (not one).
	st.Corrections = []entry.Correction{
		{ID: "x", Day: day, At: at.Add(time.Hour).Format(time.RFC3339), Entry: "j-3"},
		{ID: "y", Day: day, At: at.Add(-time.Hour).Format(time.RFC3339), Entry: "j-3"},
	}
	st.Entries["w-1"] = &entry.Entry{ID: "w-1", Facet: entry.World, Grain: entry.Action, Verdicts: []entry.Verdict{{Mark: "right", By: "user"}}}
	st.Entries["p-c"] = &entry.Entry{ID: "p-c", Facet: entry.People, Part: entry.PartCommitment, Commitment: &entry.Commitment{What: "send the draft", Due: day}}
	rs, err := Compute(Input{St: st, Dir: t.TempDir(), Day: day, Loc: time.UTC, Cards: typedCards(20, 18, yes()),
		F1Model: map[string]string{"w-1": "right", "w-2": "unsupported"}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]entry.Reading{}
	for _, r := range rs {
		got[r.Code] = r
	}
	for _, code := range []string{"R1", "R1.recur", "R2", "R3", "R3.repeat", "R4", "R4.overturn", "R4.retry-or-stop", "R5", "R6", "R6.corr", "R7", "R8", "F1", "F1.model", "U1", "U2", "U3", "U4", "U5", "U6", "U7", "U8"} {
		if _, ok := got[code]; !ok {
			t.Errorf("missing %s", code)
		}
	}
	if r := got["R4"]; !r.Measure || r.Num != 18 || r.Den != 20 {
		t.Errorf("R4: %+v", r)
	}
	if r := got["R4.overturn"]; r.Num != 1 || r.Den != 18 {
		t.Errorf("R4.overturn: %+v", r)
	}
	if r := got["R6"]; r.Measure || !strings.HasPrefix(r.Note, "量不出") {
		t.Errorf("R6: %+v", r)
	}
	if r := got["R8"]; r.Num != 1 || r.Den != 1 || r.Measure {
		t.Errorf("R8: %+v", r)
	}
	if r := got["F1.model"]; r.Num != 1 || r.Den != 2 || !strings.Contains(r.Note, "出处不支持 1") {
		t.Errorf("F1.model: %+v", r)
	}
	if r := got["U5"]; r.Num != 1 || r.Den != 1 {
		t.Errorf("U5: %+v", r)
	}
	if len(st.Readings) != len(rs) {
		t.Errorf("stored %d of %d", len(st.Readings), len(rs))
	}
	// Computing again replaces, not appends.
	if _, err := Compute(Input{St: st, Dir: t.TempDir(), Day: day, Loc: time.UTC, CardsMissing: "no hub"}); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range st.Readings {
		if r.Code == "R4" {
			n++
			if !strings.Contains(r.Note, "卡片没读到") {
				t.Errorf("R4 without cards: %+v", r)
			}
		}
	}
	if n != 1 {
		t.Errorf("R4 stored %d times", n)
	}
}
