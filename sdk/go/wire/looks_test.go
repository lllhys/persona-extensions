package wire

import (
	"bytes"
	"image"
	"image/png"
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

// Two sides naming one picture: each side is held to it (the hub does the same), every time, not one of them as a
// map's order falls (#3370).
func TestCheckLooksEverySide(t *testing.T) {
	root := t.TempDir()
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 256, 256)))
	if err := os.WriteFile(filepath.Join(root, "a.png"), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, looks := range map[string]string{
		"light": `{"id": "x", "name": "x", "avatar": {"sizes": {"128": "a.png", "256": "a.png"}}, "app_icon_fallback": "persona"}`,
		"dark":  `{"id": "x", "name": "x", "avatar": {"sizes": {"256": "a.png"}, "dark": {"sizes": {"128": "a.png"}}}, "app_icon_fallback": "persona"}`,
		"dot":   `{"id": "x", "name": "x", "avatar": {"sizes": {"256": "a.png"}}, "dot": {"size": 144, "states": {"idle": {"file": "a.png"}}}, "app_icon_fallback": "persona"}`,
	} {
		manifest := []byte(`{"contributes": {"looks": [` + looks + `]}}`)
		for i := 0; i < 20; i++ {
			if err := CheckLooks(manifest, root); err == nil {
				t.Fatalf("%s: a 256 px picture passed for another side (run %d)", name, i)
			}
		}
	}
	ok := []byte(`{"contributes": {"looks": [{"id": "x", "name": "x", "avatar": {"sizes": {"256": "a.png"}, "dark": {"sizes": {"256": "a.png"}}}, "app_icon_fallback": "persona"}]}}`)
	if err := CheckLooks(ok, root); err != nil {
		t.Fatal(err)
	}
}
