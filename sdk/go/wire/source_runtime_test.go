package wire

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGeneratedSourceRuntimeRoundTrip(t *testing.T) {
	manifest := Manifest{ManifestVersion: 1, ID: "example", Name: "Example", Version: "1.0.0", Runtime: &Runtime{Mode: "deno", Source: &SourceRuntime{Entry: "src/main.tsx", Bundle: "bundle/main.mjs", Sha256: strings.Repeat("a", 64)}, Resources: &Resources{MemoryMb: 128, CpuPercent: 20}}}
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateJSON("Manifest", b); err != nil {
		t.Fatal(err)
	}
	var decoded Manifest
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Runtime.Mode != "deno" || *decoded.Runtime.Source != *manifest.Runtime.Source || decoded.Runtime.Resources.MemoryMb != 128 {
		t.Fatalf("generated model lost source runtime: %s", b)
	}
	for _, addition := range []string{`"env":{"EXAMPLE":"synthetic"}`, `"command":"bin/example"`, `"args":[]`, `"rpc":"none"`, `"http":true`} {
		bad := strings.Replace(string(b), `"mode":"deno"`, `"mode":"deno",`+addition, 1)
		if err := ValidateJSON("Manifest", []byte(bad)); err == nil {
			t.Fatalf("source manifest accepted %s", addition)
		}
	}
	// Development eligibility is host-owned and never an extension manifest field.
	bad := strings.Replace(string(b), `"mode":"deno"`, `"mode":"deno","development_resources":true`, 1)
	if err := ValidateJSON("Manifest", []byte(bad)); err == nil {
		t.Fatal("manifest enabled development fallback")
	}
}
