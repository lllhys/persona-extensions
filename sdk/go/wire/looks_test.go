package wire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The example looks extension (schema/extension/examples/example-looks) holds to the checks; a picture that does not,
// fails them.
func TestCheckLooks(t *testing.T) {
	root := filepath.Join("..", "..", "..", "schema", "extension", "examples", "example-looks")
	manifest, err := os.ReadFile(filepath.Join(root, "persona-extension.json"))
	if err != nil {
		t.Skip("not in the main repository:", err)
	}
	if err := ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	if err := CheckLooks(manifest, root); err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(manifest), `"128": "looks/aurora/avatar-128.png"`, `"128": "looks/aurora/avatar-256.png"`, 1)
	if err := CheckLooks([]byte(bad), root); err == nil || !strings.Contains(err.Error(), "its key says 128") {
		t.Fatal(err)
	}
	if err := CheckLooks([]byte(strings.Replace(string(manifest), "avatar.svg", "../avatar.svg", 1)), root); err == nil {
		t.Fatal("a file out of the package passed")
	}
}

func TestCleanSVGDropsScripts(t *testing.T) {
	out, err := CleanSVG([]byte(`<svg viewBox="0 0 1 1" onload="x()"><script>x()</script><circle r="1"/></svg>`))
	if err != nil || strings.Contains(string(out), "x()") || !strings.Contains(string(out), "<circle") {
		t.Fatalf("%v %s", err, out)
	}
}
