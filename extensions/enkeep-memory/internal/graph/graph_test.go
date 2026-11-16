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

// TestFunctionalValueComesBack: a functional relation that went from B to C and then back to B makes B current
// again, from the day it came back; B's first period stays as history, and C ends where B came back.
func TestFunctionalValueComesBack(t *testing.T) {
	b := newBuilder(t)
	a := b.Seed("person", "people/p1", "Avery", nil)
	pb := b.Seed("person", "people/p2", "Blake", nil)
	pc := b.Seed("person", "people/p3", "Casey", nil)
	b1, _ := b.Add(a, "reports_to", pb, nil, "", ev("2026-11-01"), "high", nil, "")
	c, out := b.Add(a, "reports_to", pc, nil, "", ev("2026-11-02"), "high", nil, "")
	if out != "superseded" {
		t.Fatalf("B → C: %s", out)
	}
	b2, out := b.Add(a, "reports_to", pb, nil, "", ev("2026-11-03"), "high", nil, "")
	if out != "superseded" || b2 == b1 {
		t.Fatalf("C → B: %s, id %s (first period %s)", out, b2, b1)
	}
	g := b.G.Assertions
	if x := g[b2]; x.Status != "active" || x.ValidFrom != "2026-11-03" || x.ValidTo != "" || x.Supersedes != c {
		t.Fatalf("B again: %+v", x)
	}
	if x := g[c]; x.Status != "superseded" || x.ValidTo != "2026-11-03" {
		t.Fatalf("C after B came back: %+v", x)
	}
	if x := g[b1]; x.Status != "superseded" || x.ValidFrom != "2026-11-01" || x.ValidTo != "2026-11-02" {
		t.Fatalf("B's first period: %+v", x)
	}
	// More evidence for B now goes to its current period; evidence from B's first period still goes to that one.
	if id, out := b.Add(a, "reports_to", pb, nil, "", ev("2026-11-04"), "high", nil, ""); id != b2 || out != "duplicate" {
		t.Fatalf("B again, later: %s %s", id, out)
	}
	if id, out := b.Add(a, "reports_to", pb, nil, "2026-11-01", ev("2026-11-04"), "high", nil, ""); id != b1 || out != "duplicate" || g[b1].Status != "superseded" {
		t.Fatalf("B on its first day: %s %s %+v", id, out, g[b1])
	}
	// And back to C once more: C gets a period of its own too.
	c2, out := b.Add(a, "reports_to", pc, nil, "", ev("2026-11-05"), "high", nil, "")
	if out != "superseded" || c2 == c || g[b2].Status != "superseded" || g[b2].ValidTo != "2026-11-05" || g[c2].Status != "active" {
		t.Fatalf("B → C again: %s %+v %+v", out, g[b2], g[c2])
	}
	active := 0
	for _, x := range g {
		if x.S == a && x.P == "reports_to" && x.Status == "active" {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("%d current values of a functional relation", active)
	}
}

// The hub's default owner name "用户" and aliases like "owner" never become the owner's aliases, and a mention of
// "用户" (end users) never resolves to the owner's node; the real name and "我" do.
func TestOwnerGenericNamesNotResolved(t *testing.T) {
	reg, _ := NewRegistry(nil, nil)
	g := &store.Graph{Nodes: map[string]*store.Node{}, Assertions: map[string]*store.Assertion{}}
	b := &Builder{G: g, Reg: reg, Owner: []string{"用户", "owner", "quillmoss"}, Today: "2026-11-05"}
	b.EnsureMe()
	for _, a := range g.Nodes[Me].Aliases {
		if a.Name == "用户" || a.Name == "owner" {
			t.Fatalf("generic alias on the owner: %+v", g.Nodes[Me].Aliases)
		}
	}
	if id := b.find("用户", "person"); id == Me {
		t.Fatal("\"用户\" resolved to the owner")
	}
	if id := b.find("quillmoss", "person"); id != Me {
		t.Fatalf("the real name resolves to the owner: %q", id)
	}
	if id := b.find("我", "person"); id != Me {
		t.Fatalf("我 resolves to the owner: %q", id)
	}
	for _, n := range b.Names(Me) {
		if n == "用户" || n == "owner" {
			t.Fatalf("names of the owner: %v", b.Names(Me))
		}
	}
}
