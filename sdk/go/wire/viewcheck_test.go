package wire

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDuplicateComponentKeySurvivesTypeChecks(t *testing.T) {
	for _, last := range []Component{
		{Type: "chart", Kind: "line", Series: []ChartSeries{{Points: []ChartPoint{{Label: "a", Value: 1}}}}},
		{Type: "stat_grid", Stats: []Stat{{Label: "Count", Value: "1"}}},
		{Type: "markdown", Text: "Example"},
	} {
		last.Key = "same"
		v := &View{Sections: []Section{{Items: []Component{{Type: "text", Key: "same", Text: "first"}, last}}}}
		if err := CheckView(v); err == nil || !strings.Contains(err.Error(), "key") {
			t.Fatalf("%s: %v", last.Type, err)
		}
	}
}

func TestSafeMarkdownReferences(t *testing.T) {
	for _, text := range []string{
		"![x][img]\n\n[img]: https://example.com/image",
		"[x][ref]\n\n[ref]: javascript:alert(1)",
		"[ref]:\n javascript:alert(1)\n[x][ref]",
		"<javascript:alert(1)>",
		"[bad](https://:443)",
	} {
		if err := CheckView(&View{Sections: []Section{{Items: []Component{{Type: "markdown", Text: text}}}}}); err == nil {
			t.Errorf("accepted %q", text)
		}
	}
	for _, text := range []string{"[x](https://example.com)", "<https://example.com>", "`![x][img]`", "```\n[ref]: javascript:alert(1)\n```"} {
		if err := CheckView(&View{Sections: []Section{{Items: []Component{{Type: "markdown", Text: text}}}}}); err != nil {
			t.Errorf("%q: %v", text, err)
		}
	}
}

func TestAssistantVersionSafeAcrossLanguages(t *testing.T) {
	for _, version := range []string{"9007199254740992", "9223372036854775808"} {
		raw := []byte(`{"id":"view_example","conversation":"example","version":` + version + `,"view":{"sections":[]},"created_at":"2026-11-05T00:00:00Z","updated_at":"2026-11-05T00:00:00Z"}`)
		if err := ValidateJSON("AssistantView", raw); err == nil {
			t.Fatalf("accepted unsafe version %s", version)
		}
	}
	raw := []byte(`{"id":"view_example","conversation":"example","version":9007199254740991,"view":{"sections":[]},"created_at":"2026-11-05T00:00:00Z","updated_at":"2026-11-05T00:00:00Z"}`)
	if err := ValidateJSON("AssistantView", raw); err != nil {
		t.Fatal(err)
	}
	var v AssistantView
	if err := json.Unmarshal(raw, &v); err != nil || v.Version != 9007199254740991 {
		t.Fatalf("%+v, %v", v, err)
	}
}
