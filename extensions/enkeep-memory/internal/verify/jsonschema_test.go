package verify

import (
	"strings"
	"testing"
)

const testSchema = `{"type":"object","additionalProperties":false,"required":["items"],"properties":{"items":{"type":"array","maxItems":2,
"items":{"$ref":"#/$defs/it"}}},"$defs":{"it":{"type":"object","additionalProperties":false,"required":["n","kind"],"properties":{
"n":{"type":"integer"},"kind":{"type":"string","enum":["a","b"]},"tags":{"type":"object","additionalProperties":{"type":"string"}}}}}}`

func TestConforms(t *testing.T) {
	if errs := Conforms([]byte(testSchema), `{"items":[{"n":1,"kind":"a","tags":{"x":"y"}}]}`, 0); len(errs) != 0 {
		t.Errorf("valid: %v", errs)
	}
	for answer, want := range map[string]string{
		`{"items":[{"n":1}]}`:                                                  `missing "kind"`,
		`{"items":[{"n":1.5,"kind":"a"}]}`:                                     "not an integer",
		`{"items":[{"n":1,"kind":"c"}]}`:                                       "is not one of",
		`{"items":[{"n":1,"kind":"a","x":1}]}`:                                 `unexpected "x"`,
		`{"items":[{"n":1,"kind":"a","tags":{"x":1}}]}`:                        "not a string",
		`{"items":[{"n":1,"kind":"a"},{"n":2,"kind":"a"},{"n":3,"kind":"b"}]}`: "at most",
		`{}`:       `missing "items"`,
		`[1]`:      "not an object",
		`{"items"`: "answer:",
	} {
		errs := Conforms([]byte(testSchema), answer, 0)
		if len(errs) == 0 || !strings.Contains(strings.Join(errs, ";"), want) {
			t.Errorf("%s: %v, want %q", answer, errs, want)
		}
	}
}
