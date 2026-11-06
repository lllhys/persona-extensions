package exttest

import (
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

// CheckLooks holds an extension's looks (contributes.looks, docs/extensions.md "Looks") to what the hub checks when it
// installs the package at root: the manifest against the schema, every picture a square PNG of its side (dot frames at
// most 512 px), every SVG one the hub can clean, each look within 16 MB.
func CheckLooks(t testing.TB, manifest []byte, root string) {
	t.Helper()
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if err := wire.CheckLooks(manifest, root); err != nil {
		t.Fatal(err)
	}
}
