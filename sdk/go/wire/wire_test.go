package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Every example in schema/extension/examples decodes into its definition's type and encodes back to the same JSON:
// the generated types hold everything the schema says, under the same names.
func TestExamplesRoundTrip(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "..", "schema", "extension", "examples", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no examples: %v", err)
	}
	for _, f := range files {
		def := strings.SplitN(filepath.Base(f), ".", 2)[0]
		t.Run(filepath.Base(f), func(t *testing.T) {
			mk, ok := newDef[def]
			if !ok {
				t.Fatalf("no type %s", def)
			}
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			v := mk()
			if err := json.Unmarshal(raw, v); err != nil {
				t.Fatalf("decode: %v", err)
			}
			out, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			var want, got any
			_ = json.Unmarshal(raw, &want)
			_ = json.Unmarshal(out, &got)
			if !reflect.DeepEqual(want, got) {
				t.Errorf("round trip differs\nwant %s\n got %s", raw, out)
			}
		})
	}
}

// The component types and the UI version that brought each.
func TestComponentTypes(t *testing.T) {
	want := map[string]int{"text": 1, "metric": 1, "progress": 1, "row": 1, "button": 1, "link": 1, "field": 1,
		"divider": 1, "chart": 2, "stat_grid": 2, "segmented": 2, "markdown": 3, "table": 3, "empty": 3, "loading": 3}
	if !reflect.DeepEqual(ComponentSince, want) {
		t.Errorf("ComponentSince = %v", ComponentSince)
	}
	if len(ComponentTypes) != len(want) {
		t.Errorf("ComponentTypes = %v", ComponentTypes)
	}
}

// Validate agrees with the schema's examples: each holds to its definition, each under invalid/ does not.
func TestValidateExamples(t *testing.T) {
	for _, dir := range []string{"", "invalid"} {
		files, _ := filepath.Glob(filepath.Join("..", "..", "..", "schema", "extension", "examples", dir, "*.json"))
		if len(files) == 0 {
			t.Fatalf("no examples in %q", dir)
		}
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			err = ValidateJSON(strings.SplitN(filepath.Base(f), ".", 2)[0], raw)
			if dir == "" && err != nil {
				t.Errorf("%s: %v", filepath.Base(f), err)
			}
			if dir == "invalid" && err == nil {
				t.Errorf("%s: holds, but should not", filepath.Base(f))
			}
		}
	}
}

func TestValidateSays(t *testing.T) {
	err := ValidateJSON("View", []byte(`{"sections":[{"items":[{"type":"metric","label":"a"},{"type":"nope"}]}]}`))
	ve, ok := err.(*ValidationError)
	if !ok || len(ve.Errors) != 2 || ve.Errors[0] != "$.sections[0].items[0]: no value" || ve.Errors[1] != `$.sections[0].items[1]: no variant with type "nope"` {
		t.Fatalf("got %#v", err)
	}
	if err := Validate("View", View{Sections: []Section{{Items: []Component{{Type: "text", Text: "hi"}}}}}); err != nil {
		t.Fatal(err)
	}
}
