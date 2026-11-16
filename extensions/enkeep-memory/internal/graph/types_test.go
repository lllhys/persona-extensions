package graph

import (
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
)

// The type table: common types always; the first-run answer picks the packs (both when unanswered); a type the
// model proposes joins once seen twice; "cites" or "attended" never join.
func TestTypePacksAndLearning(t *testing.T) {
	for _, tc := range []struct {
		use        string
		work, life bool
	}{{"", true, true}, {"both", true, true}, {entry.Work, true, false}, {entry.Life, false, true}} {
		r, _ := NewRegistry(nil, nil)
		r = r.WithPacks(Packs(tc.use), nil)
		for _, common := range []string{"member_of", "owns", "depends_on", "decides", "collaborates_with", "means"} {
			if _, ok := r.Predicates[common]; !ok {
				t.Errorf("use %q: common %s missing", tc.use, common)
			}
		}
		_, rep := r.Predicates["reports_to"]
		_, men := r.Predicates["mentors"]
		_, srv := r.Predicates["serves"]
		_, circ := r.Predicates["same_circle"]
		if rep != tc.work || men != tc.work || srv != tc.life || circ != tc.life {
			t.Errorf("use %q: work %v/%v life %v/%v", tc.use, rep, men, srv, circ)
		}
	}
	var table []entry.TypeDef
	var now bool
	table, now = Propose(table, "Hands Over To", false)
	if now || len(table) != 1 || table[0].Name != "hands_over_to" {
		t.Fatalf("first sighting: %v %+v", now, table)
	}
	table, now = Propose(table, "hands_over_to", true) // the same unit-day again
	if now || table[0].Seen != 1 {
		t.Fatalf("same unit-day counted twice: %+v", table)
	}
	table, now = Propose(table, "hands-over-to", false)
	if !now || table[0].Seen != 2 {
		t.Fatalf("second sighting: %v %+v", now, table)
	}
	r, _ := NewRegistry(nil, nil)
	if _, ok := r.WithPacks(Packs(""), table).Predicates["hands_over_to"]; !ok {
		t.Fatal("a learned type is not in the registry")
	}
	for _, bad := range []string{"cites", "attended", "参加了", "引用了", "mentions"} {
		if tb, _ := Propose(nil, bad, false); len(tb) != 0 {
			t.Errorf("%s was counted", bad)
		}
	}
}

func TestSessionLike(t *testing.T) {
	for name, want := range map[string]bool{
		"chats/lark-grp-1": true, "oc_1a2b3c4d5e": true, "persona-20261101": true, "0123456789abcdef": true,
		"3f2a1b4c-1111-2222-3333-444455556666": true, "Atlas": false, "project-x": false, "Billing": false,
	} {
		if got := SessionLike(name); got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
}
