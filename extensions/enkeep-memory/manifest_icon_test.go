package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRuntimeManifestOmitsSourceOnlyArtworkMetadata(t *testing.T) {
	var expected, got map[string]any
	if err := json.Unmarshal(authorManifest, &expected); err != nil {
		t.Fatal(err)
	}
	if expected["icon"] != "assets/icon.png" {
		t.Fatal("source must declare its bundled PNG")
	}
	delete(expected, "icon")
	delete(expected, "tags")
	if err := json.Unmarshal(manifest, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatal("runtime manifest must retain all fields except source-only icon/tags")
	}
}
