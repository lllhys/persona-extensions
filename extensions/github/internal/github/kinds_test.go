package github

import (
	"maps"
	"slices"
	"testing"
)

// Every inbox kind the manifest declares is described for the models (kind_descriptions), and only those.
func TestKindsAreDescribed(t *testing.T) {
	m := (&Connector{}).Manifest()
	if len(m.Kinds) == 0 {
		t.Fatal("no kinds declared")
	}
	if got, want := slices.Sorted(maps.Keys(m.KindDescriptions)), slices.Sorted(slices.Values(m.Kinds)); !slices.Equal(got, want) {
		t.Fatalf("described %v, kinds %v", got, want)
	}
}
