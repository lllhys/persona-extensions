package main

import (
	"encoding/json"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/prompts"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/serve"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/tg"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/tg/spec"
	"github.com/nwverse/persona-extensions/pluginkit"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The hub reads the connector from persona-extension.json (persona-plugin.json before Persona #1844, the same) before the program runs (a switched-off connector can still
// be signed in to) and uses it over what the program says: the two must be the same, with the inbox kinds described
// and tg declared, and the telegram skill packed with it.
func TestManifestIsWhatTheProgramDeclares(t *testing.T) {
	raw, err := os.ReadFile("persona-extension.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID          string                     `json:"id"`
		Contributes map[string]json.RawMessage `json:"contributes"`
		Runtime     struct {
			Command map[string]string `json:"command"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var items []wire.ConnectorItem
	if err := json.Unmarshal(m.Contributes["connectors"], &items); err != nil || len(items) != 1 {
		t.Fatalf("connectors: %s (%v)", m.Contributes["connectors"], err)
	}
	want := serve.ItemOf(serve.ManifestOf(telegram.New(nil, nil)))
	if !reflect.DeepEqual(items[0], want) {
		t.Fatalf("persona-extension.json declares\n%+v\nthe program\n%+v", items[0], want)
	}
	if m.ID != spec.ID || want.CLI != "tg" || !reflect.DeepEqual(want.KindDescriptions, prompts.KindDescriptions) || !slices.Equal(slices.Sorted(maps.Keys(want.KindDescriptions)), slices.Sorted(slices.Values(want.Kinds))) {
		t.Fatalf("id %s, cli %q, kinds %v described %v", m.ID, want.CLI, want.Kinds, want.KindDescriptions)
	}
	// The inbox rules, as the hub's install check holds them (nwverse/persona#1807): the user's own triage rules in
	// the standard setting, and the fallback, so Telegram never waits for the triage model.
	if in, err := pluginkit.CheckManifest(raw); err != nil || !in.Fallback(spec.ID) {
		t.Fatalf("inbox rules: %v (fallback %v)", err, in.Fallback(spec.ID))
	}
	if err := pluginkit.CheckStandardSettings(raw); err != nil {
		t.Fatal(err)
	}
	if len(m.Runtime.Command) != 4 {
		t.Fatalf("platforms: %v", m.Runtime.Command)
	}
	var assistant []struct {
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal(m.Contributes["assistant"], &assistant); err != nil || len(assistant) != 1 || len(assistant[0].Skills) != 1 {
		t.Fatalf("assistant: %s (%v)", m.Contributes["assistant"], err)
	}
	skill, err := os.ReadFile(assistant[0].Skills[0] + "/SKILL.md")
	if err != nil || !strings.HasPrefix(string(skill), "---\nname: telegram\n") || !strings.Contains(string(skill), "tg chats") {
		t.Fatalf("the telegram skill: %v", err)
	}
}

// The program holds to the process protocol as the SDK's own check has it: the handshake (who it is, as the manifest
// says), the error codes, and that it stops when the hub goes away. Signed out, so nothing dials Telegram.
func TestConformance(t *testing.T) {
	init := exttest.Check(t, manifest, exttest.Serve(t, serve.Program(spec.ID, version, manifest, build)))
	var items []wire.ConnectorItem
	if err := json.Unmarshal(init.Contributes["connectors"], &items); err != nil || len(items) != 1 || items[0].ID != spec.ID {
		t.Fatalf("contributes %s (%v)", init.Contributes["connectors"], err)
	}
}
