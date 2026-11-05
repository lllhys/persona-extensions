// Package extensions holds the checks every extension's manifest gets, whether or not the extension has a program
// and tests of its own: each extensions/<id>/persona-extension.json against the SDK's Manifest schema.
package extensions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

// TestManifests validates every manifest as committed (with its market tags) and as packages and indexes carry it
// (without them, scripts/manifest.py packaged): the schema allows no field it does not know, at any depth.
func TestManifests(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("*", "persona-extension.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no extensions/*/persona-extension.json")
	}
	for _, p := range paths {
		t.Run(filepath.Dir(p), func(t *testing.T) {
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := wire.ValidateJSON("Manifest", raw); err != nil {
				t.Errorf("%s: %v", p, err)
			}
			if err := wire.ValidateJSON("Manifest", packaged(t, raw)); err != nil {
				t.Errorf("%s, packaged: %v", p, err)
			}
		})
	}
}

// packaged is the manifest without its tags, as scripts/manifest.py packaged writes it.
func packaged(t *testing.T, raw []byte) []byte {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "tags")
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
