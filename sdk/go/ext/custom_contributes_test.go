package ext

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

// Run the SDK's real JSON-lines manifest/initialize handlers using a manifest constructed with the generated types.
// Custom contributions must survive both the typed encoding and the SDK's responses without renaming nested keys.
func TestCustomContributionsManifestAndInitialize(t *testing.T) {
	raw, err := os.ReadFile("../../../schema/extension/examples/Manifest.custom-contributions.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest wire.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	c := start(t, Program{ID: manifest.ID, Version: manifest.Version, Manifest: encoded,
		Init: func(ctx context.Context, init *Init, hub *Hub) (map[string]any, error) {
			if init.Info().ID != manifest.ID || init.Granted.HubAPI || len(init.Granted.Hub) != 0 {
				t.Errorf("identity or permissions changed: %+v", init.InitializeParams)
			}
			// The typed UI stays available even when the same object has case-sensitive extra keys like UI.
			if len(manifest.Contributes.UI) != 1 || manifest.Contributes.UI[0].ID != "home" {
				t.Errorf("lost typed UI: %+v", manifest.Contributes.UI)
			}
			data, err := json.Marshal(manifest.Contributes)
			if err != nil {
				return nil, err
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				return nil, err
			}
			contributes := map[string]any{}
			for key, value := range fields {
				contributes[key] = value
			}
			return contributes, nil
		},
	})
	f := c.call("manifest", nil)
	if f.Error != nil {
		t.Fatal(f.Error)
	}
	var gotManifest, wantManifest map[string]any
	if err := json.Unmarshal(f.Result, &gotManifest); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &wantManifest); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotManifest, wantManifest) {
		t.Fatalf("manifest response changed: %s", f.Result)
	}
	f = c.call("initialize", wire.InitializeParams{Protocol: 1,
		Hub:       wire.HubInfo{Build: 0, ExtensionPoints: map[string]int{"ui": 2}},
		Extension: &wire.ExtensionInfo{ID: manifest.ID, Version: manifest.Version},
		Active:    map[string][]string{"ui": {"home"}, "example-core.custompoint": {"first"}, "example-core.rules": {"second"}},
	})
	if f.Error != nil {
		t.Fatal(f.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(f.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result["contributes"], wantManifest["contributes"]) {
		t.Fatalf("initialize lost custom contributions: %s", f.Result)
	}
}
