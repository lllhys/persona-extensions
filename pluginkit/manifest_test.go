package pluginkit

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

func TestPackagedManifestStripsOnlySourceMetadata(t *testing.T) {
	raw := []byte(`{"manifest_version":1,"id":"synthetic","name":"Synthetic","version":"0.1.0","tags":["demo"],"replaces":"predecessor","icon":"assets/icon.svg","unknown_field":{"icon":"nested-value"}}`)
	before := append([]byte(nil), raw...)
	packaged := PackagedManifest(raw)
	if !bytes.Equal(raw, before) {
		t.Fatal("projection mutated the embedded source bytes")
	}
	var source, runtime map[string]json.RawMessage
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(packaged, &runtime); err != nil {
		t.Fatal(err)
	}
	for key, value := range source {
		switch key {
		case "tags", "replaces", "icon":
			if _, ok := runtime[key]; ok {
				t.Errorf("source metadata %s leaked into runtime", key)
			}
		default:
			if !bytes.Equal(runtime[key], value) {
				t.Errorf("runtime field %s changed", key)
			}
		}
	}
	if err := wire.ValidateJSON("Manifest", packaged); err == nil {
		t.Fatal("unknown runtime field was hidden from strict SDK validation")
	}
	if !bytes.Equal(PackagedManifest(packaged), packaged) {
		t.Fatal("runtime projection is not idempotent")
	}
}

func TestPackagedManifestHoldsToUnmodifiedSDK(t *testing.T) {
	for _, raw := range []string{
		`{"manifest_version":1,"id":"synthetic","name":"Synthetic","version":"0.1.0","tags":["demo"],"replaces":"predecessor","icon":"assets/icon.svg"}`,
		`{"manifest_version":1,"id":"synthetic","name":"Synthetic","version":"0.1.0"}`,
	} {
		if err := wire.ValidateJSON("Manifest", PackagedManifest([]byte(raw))); err != nil {
			t.Errorf("projected manifest: %v", err)
		}
	}
	if err := wire.ValidateJSON("Manifest", PackagedManifest([]byte(`{"manifest_version":1,"id":"synthetic","name":"Synthetic","version":"0.1.0","engines":{"extension_protocol":1,"unknown_field":true}}`))); err == nil {
		t.Fatal("unknown nested field was hidden from strict SDK validation")
	}
}

func TestPackagedManifestRefusesMalformedEmbeddedJSON(t *testing.T) {
	for _, raw := range []string{"", "null", "[]", "false", `"text"`, "{", `{"id":}`, `{"id":"one","id":"two"}`, `{"id":"one"} {}`, `{"id":"one"} trailing`} {
		t.Run(raw, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("malformed embedded manifest did not fail initialization")
				}
			}()
			PackagedManifest([]byte(raw))
		})
	}
}
