package wire

import (
	"encoding/json"
	"testing"
)

func TestCheckViewJSONPreservesRequiredEmptyNativeFields(t *testing.T) {
	raw := []byte(`{"sections":[{"items":[{"type":"code","text":""},{"type":"table","columns":[{"title":""}],"rows":[]},{"type":"comment_thread","comments":[]},{"type":"file_tree","entries":[]}]}]}`)
	if err := CheckViewJSON(raw); err != nil {
		t.Fatal(err)
	}
	var view View
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if err := CheckView(&view); err == nil {
		t.Fatal("legacy snapshot behavior unexpectedly changed")
	}
	for _, bad := range []string{
		`{"sections":[{"items":[{"type":"code"}]}]}`,
		`{"sections":[{"items":[{"type":"table","columns":[{"title":"A"}],"rows":[[]]}]}]}`,
		`{"sections":[{"items":[{"type":"field","name":"x","kind":"secret","label":"Secret","value":"filled"}]}]}`,
		`{"sections":[{"items":[{"type":"code","text":null}]}]}`,
	} {
		if err := CheckViewJSON([]byte(bad)); err == nil {
			t.Fatalf("accepted invalid source/semantics: %s", bad)
		}
	}
}
