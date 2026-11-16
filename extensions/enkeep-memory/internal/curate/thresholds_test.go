package curate

import (
	"fmt"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

func TestStableK(t *testing.T) {
	for n, want := range map[int]int{0: 2, 1: 2, 4: 2, 5: 3, 20: 3, 21: 4, 40: 6, 100: 15} {
		if got := StableK(n); got != want {
			t.Errorf("StableK(%d) = %d, want %d", n, got, want)
		}
	}
}

// days returns n active days from 2026-01-01.
func days(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("2026-%02d-%02d", 1+i/28, 1+i%28))
	}
	return out
}

func occ(ds ...string) []occurrence {
	var out []occurrence
	for i, d := range ds {
		out = append(out, occurrence{fmt.Sprintf("chats/%d", i), d})
	}
	return out
}

// A user with little data: two consistent occurrences on two active days are enough; one is not; two on one day
// are not.
func TestSettleLittleData(t *testing.T) {
	s := &store.State{ActiveDays: days(3)}
	e := &entry.Entry{Grain: entry.Class, Origin: entry.Behavior, Status: entry.Candidate, Last: s.ActiveDays[2]}
	today := s.ActiveDays[2]
	ds := s.ActiveDays
	if d, _ := Settle(s, e, Tally{For: occ(ds[0]), Days: ds[:1]}, today); d.Status != entry.Candidate {
		t.Errorf("one occurrence: %s", d.Status)
	}
	if d, _ := Settle(s, e, Tally{For: occ(ds[0], ds[0]), Days: ds[:1]}, today); d.Status != entry.Candidate {
		t.Errorf("one active day: %s", d.Status)
	}
	if d, _ := Settle(s, e, Tally{For: occ(ds[0], ds[2]), Days: []string{ds[0], ds[2]}}, today); d.Status != entry.Stable {
		t.Errorf("two on two days: %s", d.Status)
	}
	// the user's own words and the hand-written memory start stable
	own := &entry.Entry{Grain: entry.Action, Origin: entry.SelfReport, Last: today}
	if d, _ := Settle(s, own, Tally{For: occ(ds[0]), Days: ds[:1]}, today); d.Status != entry.Stable {
		t.Errorf("self-report: %s", d.Status)
	}
}

// A user with much data: 40 chances need 6 consistent ones; a counter-example in the last 20 active days keeps it
// from stable, and ≥25% of them make it shifting; one long ago does not.
func TestSettleMuchData(t *testing.T) {
	s := &store.State{ActiveDays: days(60)}
	ds := s.ActiveDays
	today := ds[59]
	e := &entry.Entry{Grain: entry.Class, Origin: entry.Behavior, Status: entry.Candidate, Last: today}
	var five, six []occurrence
	for i := 0; i < 6; i++ {
		six = append(six, occurrence{fmt.Sprintf("chats/%d", i), ds[50+i]})
	}
	five = six[:5]
	against := func(n int, from int) []occurrence {
		var out []occurrence
		for i := 0; i < n; i++ {
			out = append(out, occurrence{fmt.Sprintf("chats/x%d", i), ds[from+i]})
		}
		return out
	}
	// 40 chances: 34 against long ago (outside the last 20 active days)
	if d, _ := Settle(s, e, Tally{For: five, Against: against(35, 0), Days: ds}, today); d.Status != entry.Candidate {
		t.Errorf("5 of 40: %s", d.Status)
	}
	if d, _ := Settle(s, e, Tally{For: six, Against: against(34, 0), Days: ds}, today); d.Status != entry.Stable {
		t.Errorf("6 of 40, counters long ago: %s", d.Status)
	}
	if d, _ := Settle(s, e, Tally{For: six, Against: against(1, 45), Days: ds}, today); d.Status != entry.Candidate {
		t.Errorf("a recent counter keeps it from stable: %s", d.Status)
	}
	if d, _ := Settle(s, e, Tally{For: six, Against: against(2, 45), Days: ds}, today); d.Status != entry.Shifting {
		t.Errorf("2 of 8 recent against: %s", d.Status)
	}
	// quiet for 40 active days: dormant; a confirmed one is asked about instead
	quiet := &entry.Entry{Grain: entry.Class, Origin: entry.Behavior, Status: entry.Stable, Last: ds[10]}
	if d, _ := Settle(s, quiet, Tally{For: occ(ds[9], ds[10]), Days: ds[9:11]}, today); d.Status != entry.Dormant {
		t.Errorf("quiet: %s", d.Status)
	}
	quiet.Verdicts = []entry.Verdict{{Mark: "right", By: "user"}}
	if d, ask := Settle(s, quiet, Tally{For: occ(ds[9], ds[10]), Days: ds[9:11]}, today); d.Status != entry.Stable || !ask {
		t.Errorf("confirmed and quiet: %s ask %v", d.Status, ask)
	}
	// calendar gaps do not count: 3 active days spread over months are 3 active days
	sparse := &store.State{ActiveDays: []string{"2026-01-01", "2026-03-01", "2026-06-01"}}
	se := &entry.Entry{Grain: entry.Class, Origin: entry.Behavior, Status: entry.Stable, Last: "2026-01-01"}
	if d, _ := Settle(sparse, se, Tally{For: occ("2026-01-01", "2026-01-01")}, "2026-06-01"); d.Status == entry.Dormant {
		t.Error("dormant by the calendar")
	}
}

// What the user said holds from now on ("always", "remember this") is stable at once, in their own words only.
func TestSettleDurable(t *testing.T) {
	s := &store.State{ActiveDays: days(3)}
	e := &entry.Entry{Grain: entry.Action, Origin: entry.Behavior, Flags: []string{"durable"}, Last: s.ActiveDays[0],
		Evidence: []entry.Evidence{{Ref: "chats/a", Day: s.ActiveDays[0], Who: entry.WhoSelf}}}
	if d, _ := Settle(s, e, Tally{For: occ(s.ActiveDays[0])}, s.ActiveDays[2]); d.Status != entry.Stable {
		t.Errorf("durable: %s", d.Status)
	}
	e.Evidence[0].Who = "person:b"
	if d, _ := Settle(s, e, Tally{For: occ(s.ActiveDays[0])}, s.ActiveDays[2]); d.Status != entry.Candidate {
		t.Errorf("durable, not the user's words: %s", d.Status)
	}
}

// One sighting settles a fact the user states about themself, a fact or a promise on a person's or the user's own
// line, and anything from "about me"; a view, a habit, an evaluation or a reading needs a second.
func TestSettleOneSightingFacts(t *testing.T) {
	s := &store.State{ActiveDays: days(3)}
	d0, today := s.ActiveDays[0], s.ActiveDays[2]
	own := []entry.Evidence{{Ref: "chats/a", Day: d0, Who: entry.WhoSelf, Role: "user", Quote: "我负责 project-x 的数据管线"}}
	one := Tally{For: occ(d0), Days: []string{d0}}
	for _, c := range []struct {
		name string
		e    *entry.Entry
		want entry.Status
	}{
		{"self role", &entry.Entry{Facet: entry.Persona, Grain: entry.Action, Origin: entry.Behavior, Type: "responsibility", Evidence: own}, entry.Stable},
		{"self opinion", &entry.Entry{Facet: entry.Persona, Grain: entry.Action, Origin: entry.Behavior, Type: "opinion", Evidence: own}, entry.Candidate},
		{"role said by another", &entry.Entry{Facet: entry.Persona, Grain: entry.Action, Origin: entry.Behavior, Type: "role",
			Evidence: []entry.Evidence{{Ref: "chats/a", Day: d0, Who: "person:b", Role: "other", Quote: "他负责 project-x"}}}, entry.Candidate},
		{"about me", &entry.Entry{Facet: entry.Persona, Grain: entry.Action, Origin: entry.SelfReport, Type: "values", Evidence: own}, entry.Stable},
		{"person fact", &entry.Entry{Facet: entry.People, Grain: entry.Action, Origin: entry.Behavior, Part: entry.PartFact, Evidence: own}, entry.Stable},
		{"person promise", &entry.Entry{Facet: entry.People, Grain: entry.Action, Origin: entry.Behavior, Part: entry.PartCommitment, Evidence: own}, entry.Stable},
		{"person fact, reported", &entry.Entry{Facet: entry.People, Grain: entry.Action, Origin: entry.Behavior, Part: entry.PartFact, ReportedOnly: true, Evidence: own}, entry.Candidate},
		{"person evaluation", &entry.Entry{Facet: entry.People, Grain: entry.Action, Origin: entry.Behavior, Part: entry.PartUserEval, Evidence: own}, entry.Candidate},
		{"person reading", &entry.Entry{Facet: entry.People, Grain: entry.Action, Origin: entry.Behavior, Part: entry.PartInferred, Inferred: true, Evidence: own}, entry.Candidate},
	} {
		c.e.Last = d0
		if d, _ := Settle(s, c.e, one, today); d.Status != c.want {
			t.Errorf("%s: %s, want %s", c.name, d.Status, c.want)
		}
	}
}
