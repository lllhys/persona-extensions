package wire

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestCustomContributionsStayOpaque(t *testing.T) {
	raw, err := os.ReadFile("../../../schema/extension/examples/Manifest.custom-contributions.json")
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Contributes.UI) != 1 || m.Contributes.UI[0].ID != "home" || len(m.Contributes.AdditionalProperties) != 8 {
		t.Fatalf("known UI and custom points: %+v", m.Contributes)
	}
	var want struct {
		Contributes map[string]json.RawMessage `json:"contributes"`
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	for key, value := range m.Contributes.AdditionalProperties {
		var got, expected any
		if err := json.Unmarshal(value, &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(want.Contributes[key], &expected); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, expected) {
			t.Errorf("%s changed: %s", key, value)
		}
	}
	// Reusing the same value for a new decode must clear old extras and known fields.
	if err := json.Unmarshal([]byte(`{"ui":[]}`), &m.Contributes); err != nil {
		t.Fatal(err)
	}
	if len(m.Contributes.AdditionalProperties) != 0 || m.Contributes.UI == nil {
		t.Fatalf("stale fields survived another decode: %+v", m.Contributes)
	}
}

func TestAdditionalPropertiesCannotShadowDeclaredFields(t *testing.T) {
	c := Contributes{
		UI: []UIItem{{ID: "home", Title: "Home", Placement: "plugin_page"}},
		AdditionalProperties: map[string]json.RawMessage{
			"ui":                       json.RawMessage(`"not a UI list"`),
			"providers":                json.RawMessage(`[{"id":"injected"}]`),
			"example-core.custompoint": json.RawMessage(`[{"id":"first","config":null}]`),
		},
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var back Contributes
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.UI, back.UI) || len(back.Providers) != 0 || len(back.AdditionalProperties) != 1 {
		t.Fatalf("extras shadowed a declared field: %s", raw)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["providers"]; ok {
		t.Fatalf("an omitted known field was reintroduced through extras: %s", raw)
	}
	if _, ok := fields["additionalProperties"]; ok {
		t.Fatalf("the storage map leaked onto the wire: %s", raw)
	}
}
