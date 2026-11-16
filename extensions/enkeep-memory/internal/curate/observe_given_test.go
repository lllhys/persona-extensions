package curate

import (
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
)

// A person called by the given name only resolves to the one member (or contact) whose full name it is part of; two
// candidates, a one-character name or a stranger resolve to nobody.
func TestPersonByGivenName(t *testing.T) {
	k := &known{people: map[string]string{"pa": "王小明", "pb": "李小明", "pc": "Avery Lin", "pd": "欧阳子墨", "pe": "张三"},
		byName: map[string][]string{}, contacts: []string{"pb", "pd"}}
	u := &Unit{Meta: kbsrc.Meta{People: []string{"pa", "pc", "pe"}}}
	for name, want := range map[string]string{
		"小明":    "person:pa", // a member first, though a contact has the same given name
		"Avery": "person:pc",
		"子墨":    "person:pd", // not a member: the intersection list
		"三":     "",          // one character
		"Lin":   "",          // not the first word
		"墨":     "",
	} {
		if got := personOf(u, k, name); got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
	u2 := &Unit{Meta: kbsrc.Meta{People: []string{"pa", "pb"}}}
	if got := personOf(u2, k, "小明"); got != "" {
		t.Errorf("two members with that given name: %q", got)
	}
}
