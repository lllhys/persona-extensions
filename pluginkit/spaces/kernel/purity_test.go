package kernel

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// kernelImports are the only standard packages the kernel may import: it is pure (no file system, no network, no
// clock, no locks, no randomness of its own).
var kernelImports = map[string]bool{
	"cmp": true, "errors": true, "fmt": true, "math": true, "net/url": true, "slices": true,
	"strconv": true, "strings": true, "time": true, "unicode/utf8": true,
}

// TestPurity parses every non-test file of the package and fails on any import outside the allowed set.
func TestPurity(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !kernelImports[path] {
				t.Errorf("%s imports %q, which the pure kernel may not use", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no source files were checked")
	}
}

// TestStorageImportsOnlyTime: the Storage interface may mention nothing but kernel types and time.
func TestStorageImportsOnlyTime(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "storage.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Imports) != 1 || f.Imports[0].Path.Value != `"time"` {
		t.Errorf("storage.go must import only time")
	}
}

// jsonFieldText matches the JSON name "text" used as a struct tag or a schema key.
var jsonFieldText = regexp.MustCompile(`json:"text"|"text":`)

// TestNoKindNameOutsideKindGo: the descriptor table is the only place that names the kinds in code. Other source
// files may use the exported constants but must not spell a kind name as a string literal.
func TestNoKindNameOutsideKindGo(t *testing.T) {
	names := []string{`"text"`, `"number"`, `"boolean"`, `"date"`, `"enum"`, `"url"`, `"relation"`, `"rollup"`}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if name == "kind.go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		// "text" is also a JSON name of a field (the query's text, a result's text): a struct tag or a schema key
		// names no kind, so those spellings are left out.
		src := jsonFieldText.ReplaceAllString(string(data), "")
		for _, lit := range names {
			if strings.Contains(src, lit) {
				t.Errorf("%s spells the kind name %s; use the Kind constants", name, lit)
			}
		}
	}
}
