package telegram

import (
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/connector"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/prompts"
)

// Every inbox kind the manifest declares has its own description for the models (the plugin's kind_descriptions).
func TestKindsAreDescribed(t *testing.T) {
	m := (*Conn)(nil).Manifest()
	if len(m.Kinds) == 0 {
		t.Fatal("no kinds declared")
	}
	err := connector.CheckKinds(m.Kinds, func(kind string) string {
		return prompts.KindDescriptions[kind]
	})
	if err != nil {
		t.Fatal(err)
	}
	// The hub prepends the localized type prefix "类型：" ("Type:") itself (Persona's prompts/inbox/item.md); adding it here would duplicate it.
	for kind, d := range prompts.KindDescriptions {
		if d == "" || strings.HasPrefix(d, "类型") {
			t.Errorf("%s: %q", kind, d)
		}
	}
}
