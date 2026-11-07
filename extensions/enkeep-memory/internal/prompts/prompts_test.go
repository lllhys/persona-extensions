package prompts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
)

// Golden: a prompt's text is pinned to its version. Changing a prompt without bumping its version in the schema
// package would make old and new answers share ledger keys; run with EKM_UPDATE_GOLDEN=1 after bumping.
func TestGolden(t *testing.T) {
	got := map[string]string{}
	for name, p := range map[string]struct {
		text    string
		version string
		schema  []byte
	}{"triage": {Triage, schema.TriagePrompt, TriageSchema}, "daily": {Daily, schema.DailyPrompt, DailySchema}, "weekly": {Weekly, schema.WeeklyPrompt, WeeklySchema},
		"memory": {Memory, schema.MemoryPrompt, MemorySchema}} {
		var v any
		if err := json.Unmarshal(p.schema, &v); err != nil {
			t.Errorf("%s schema: %v", name, err)
		}
		h := sha256.Sum256(append([]byte(p.text), p.schema...))
		got[p.version] = hex.EncodeToString(h[:8])
	}
	if os.Getenv("EKM_UPDATE_GOLDEN") != "" {
		b, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile("testdata/golden.json", append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	_ = json.Unmarshal(raw, &want)
	for v, h := range got {
		if want[v] != h {
			t.Errorf("prompt %s changed (%s, golden %s): bump its version in internal/schema, then EKM_UPDATE_GOLDEN=1", v, h, want[v])
		}
	}
}
