package wire

import (
	"strings"
	"testing"
)

func TestSourceValidatorManifestComposition(t *testing.T) {
	manifest := `{"manifest_version":1,"id":"example","name":"Example","version":"1.0.0","runtime":{"mode":"deno","source":{"entry":"src/main.tsx","bundle":"bundle/main.mjs","sha256":"` + strings.Repeat("a", 64) + `"}}}`
	if err := ValidateJSON("Manifest", []byte(manifest)); err != nil {
		t.Fatal(err)
	}
	for _, addition := range []string{`"env":{"EXAMPLE":"synthetic"}`, `"command":"bin/example"`, `"args":[]`, `"rpc":"none"`, `"http":true`, `"development_resources":true`} {
		bad := strings.Replace(manifest, `"mode":"deno"`, `"mode":"deno",`+addition, 1)
		if ValidateJSON("Manifest", []byte(bad)) == nil {
			t.Fatal("source manifest ignored runtime composition")
		}
	}
}
