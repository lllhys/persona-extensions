// Package manifests holds the checks every extension's manifest gets, whether or not the extension has a program
// and tests of its own: each extensions/<id>/persona-extension.json against the SDK's Manifest schema.
package manifests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

// TestManifests validates every source manifest after removing only author-owned
// replaces, and its runtime projection after also removing tags. Unknown runtime
// fields still fail the SDK schema at every depth; Python validates predecessors.
func TestManifests(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "extensions", "*", "persona-extension.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no extensions/*/persona-extension.json")
	}
	for _, p := range paths {
		t.Run(filepath.Base(filepath.Dir(p)), func(t *testing.T) {
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := wire.ValidateJSON("Manifest", withoutAuthorMetadata(t, raw)); err != nil {
				t.Errorf("%s: %v", p, err)
			}
			if err := wire.ValidateJSON("Manifest", packaged(t, raw)); err != nil {
				t.Errorf("%s, packaged: %v", p, err)
			}
		})
	}
}

// withoutAuthorMetadata keeps runtime fields strict without editing the copied SDK.
func withoutAuthorMetadata(t *testing.T, raw []byte) []byte {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "replaces")
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// packaged matches the source-only field projection in scripts/manifest.py.
func packaged(t *testing.T, raw []byte) []byte {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "tags")
	delete(m, "replaces")
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
