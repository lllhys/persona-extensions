package prompts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// The signals' and exits' prompts are pinned like the others (TestGolden), in their own golden file.
func TestGoldenSignals(t *testing.T) {
	got := map[string]string{}
	for v, p := range map[string]struct {
		text   string
		schema []byte
	}{CorrectPrompt: {Correct, CorrectSchema}, IssuesPrompt: {Issues, IssuesSchema}, DigPrompt: {Dig, DigSchema}, MinePrompt: {Mine, MineSchema}, ExportPrompt: {Export, ExportSchema}, EmptyPrompt: {Emptiness, EmptinessSchema}} {
		var x any
		if err := json.Unmarshal(p.schema, &x); err != nil {
			t.Errorf("%s schema: %v", v, err)
		}
		h := sha256.Sum256(append([]byte(p.text), p.schema...))
		got[v] = hex.EncodeToString(h[:8])
	}
	if os.Getenv("EKM_UPDATE_GOLDEN") != "" {
		b, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile("testdata/golden_signals.json", append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile("testdata/golden_signals.json")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	_ = json.Unmarshal(raw, &want)
	for v, h := range got {
		if want[v] != h {
			t.Errorf("prompt %s changed (%s, golden %s): bump its version, then EKM_UPDATE_GOLDEN=1", v, h, want[v])
		}
	}
}
