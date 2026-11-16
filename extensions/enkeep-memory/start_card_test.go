package main

import (
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// The start card says how far the nearest candidate of a facet is from stable, by the adaptive bar.
func TestClosestToStable(t *testing.T) {
	st := &store.State{Entries: map[string]*entry.Entry{
		"j-a": {ID: "j-a", Facet: entry.Judgement, Grain: entry.Class, Status: entry.Candidate, Origin: entry.Behavior, Count: 1, ActiveDays: []string{"2026-11-01"}},
		"j-b": {ID: "j-b", Facet: entry.Judgement, Grain: entry.Class, Status: entry.Candidate, Origin: entry.Behavior, Count: 3, Chances: 12, ActiveDays: []string{"2026-11-01"}},
		"j-c": {ID: "j-c", Facet: entry.Judgement, Grain: entry.Class, Status: entry.Stable, Origin: entry.Behavior, Count: 9},
	}}
	// j-a: 1 chance → bar 2, has 1: 1 more. j-b: 12 chances → bar 3, has 3, but one active day: 1 more.
	if n, ok := closestToStable(st, entry.Judgement); !ok || n != 1 {
		t.Fatalf("judgement: %d %v", n, ok)
	}
	if _, ok := closestToStable(st, entry.Workflow); ok {
		t.Fatal("a facet with no candidate says nothing")
	}
}
