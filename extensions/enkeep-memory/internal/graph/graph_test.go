package graph

import (
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

func ev(day string) []store.Evidence {
	return []store.Evidence{{Chat: "chats/c", Day: day, N: 3, Speaker: "A", Quote: "q"}}
}

func newBuilder(t *testing.T) *Builder {
	reg, problems := NewRegistry([]string{"ticket: a support ticket"}, []string{"handles: person -> ticket: works on", "broken"})
	if len(problems) != 1 {
		t.Fatalf("problems: %v", problems)
	}
	g := &store.Graph{Nodes: map[string]*store.Node{}, Assertions: map[string]*store.Assertion{}}
	b := &Builder{G: g, Reg: reg, Owner: []string{"Sam"}, Today: "2026-11-05"}
	b.EnsureMe()
	return b
}

func TestSupersedeHistoryConflict(t *testing.T) {
	b := newBuilder(t)
	sys, _ := b.Resolve("Billing", "system", "", nil)
	p1 := b.Seed("person", "people/p1", "Avery", nil)
	p2 := b.Seed("person", "people/p2", "Blake", nil)
	p3 := b.Seed("person", "people/p3", "Casey", nil)
	// reports_to is functional: a later value supersedes, an earlier one is history, the same day conflicts.
	a1, _ := b.Add(p1, "reports_to", p2, nil, "", ev("2026-11-01"), "high", nil, "")
	a2, out := b.Add(p1, "reports_to", p3, nil, "", ev("2026-11-03"), "high", nil, "")
	if out != "superseded" || b.G.Assertions[a1].Status != "superseded" || b.G.Assertions[a1].ValidTo != "2026-11-03" || b.G.Assertions[a2].Status != "active" {
		t.Fatalf("supersede: %s %+v", out, b.G.Assertions[a1])
	}
	_, out = b.Add(p1, "reports_to", sys, nil, "2026-10-01", ev("2026-11-04"), "high", nil, "")
	if out != "history" {
		t.Fatalf("older value: %s", out)
	}
	_, out = b.Add(p1, "reports_to", p2, nil, "2026-11-03", ev("2026-11-03"), "high", nil, "")
	if out != "duplicate" { // same triple as a1: evidence added
		t.Fatalf("same triple: %s", out)
	}
	// Custom types and relations are usable.
	tk, ok := b.Resolve("TCK-1", "ticket", "", nil)
	if !ok {
		t.Fatal("custom type")
	}
	if _, out := b.Add(p2, "handles", tk, nil, "", ev("2026-11-04"), "medium", []string{"X"}, ""); out != "new" || b.G.Assertions[assertionID(p2, "handles", tk)].Status != "candidate" {
		t.Fatal("a flagged medium relation stays a candidate")
	}
}

func TestMergeUndoAndOwner(t *testing.T) {
	b := newBuilder(t)
	if id, _ := b.Resolve("Sam", "person", "", nil); id != Me {
		t.Fatalf("the owner's name resolves to me: %s", id)
	}
	if _, ok := b.Resolve("周会", "meeting", "", nil); ok {
		t.Fatal("a generic word never becomes a node")
	}
	x := b.Seed("project", "projects/x", "Atlas", nil)
	y, _ := b.Resolve("Atlas", "project", "", nil)
	if x != y {
		t.Fatal("a name resolves to the seeded node")
	}
	z := b.newNode("project", "Atlas", "candidate")
	if len(b.SameNameCandidates()) != 1 {
		t.Fatal("same-name nodes are merge candidates")
	}
	if err := b.Merge(z, x, "K-1"); err != nil || b.G.Resolve(z) != x {
		t.Fatal(err)
	}
	if err := b.Unmerge(z); err != nil || b.G.Resolve(z) != z {
		t.Fatal(err)
	}
}
