package wire

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Each case in schema/extension/examples/persona-card is a card and the view it becomes (null: the apps keep the code
// block). The views hold to the schema. UPDATE=1 writes what View gives instead (then read the diff).
func TestPersonaCardCases(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "..", "schema", "extension", "examples", "persona-card", "*.json"))
	if len(files) < 5 {
		t.Fatalf("only %d cases", len(files))
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var c struct {
				Card json.RawMessage `json:"card"`
				View json.RawMessage `json:"view"`
			}
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatal(err)
			}
			if err := ValidateJSON("PersonaCard", c.Card); err != nil {
				t.Fatalf("card: %v", err)
			}
			var card PersonaCard
			if err := json.Unmarshal(c.Card, &card); err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(card.View())
			if os.Getenv("UPDATE") != "" {
				var b bytes.Buffer
				enc := json.NewEncoder(&b)
				enc.SetEscapeHTML(false)
				enc.SetIndent("", "  ")
				_ = enc.Encode(map[string]json.RawMessage{"card": c.Card, "view": got})
				if err := os.WriteFile(f, b.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			var want, have any
			_ = json.Unmarshal(c.View, &want)
			_ = json.Unmarshal(got, &have)
			if string(mustJSON(want)) != string(mustJSON(have)) {
				t.Errorf("view\nwant %s\n got %s", mustJSON(want), mustJSON(have))
			}
			if string(c.View) != "null" {
				if err := ValidateJSON("View", c.View); err != nil {
					t.Errorf("view: %v", err)
				}
			}
		})
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestNumber(t *testing.T) {
	for v, want := range map[float64]string{0: "0", 2250: "2,250", 7.25: "7.3", 7.04: "7", 1234567.89: "1,234,567.9", -1200.3: "-1,200.3", 999.96: "1,000", -0.01: "0"} {
		if got := Number(v); got != want {
			t.Errorf("Number(%v) = %q, want %q", v, got, want)
		}
	}
}
