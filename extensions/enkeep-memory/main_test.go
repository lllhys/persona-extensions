package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/memory"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"

	"github.com/nwverse/persona/sdk/go/exttest"
)

// The program speaks the protocol and draws every UI contribution in its slot (with no hub data yet).
func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newApp())))
}

// The manifest's configuration defaults are the ones the curator falls back to.
func TestManifestDefaults(t *testing.T) {
	var m struct {
		Configuration struct {
			Properties map[string]struct {
				Default any `json:"default"`
			} `json:"properties"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"daily_budget_usd", "weekly_budget_usd", "graph_sources"} {
		if _, ok := m.Configuration.Properties[k]; ok {
			t.Errorf("%s is gone: no budget caps, no per-source pipelines", k)
		}
	}
	if d := m.Configuration.Properties["mode"].Default; d != "auto" {
		t.Errorf("mode default %v", d)
	}
}

// The migration reads an export into the state's memory once, and maps enkeep's states.
func TestImportEnkeep(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/export.json"
	recs := `[{"table":"rules","id":"R-1","status":"confirmed","text":"不要用代号","is_hard":true,
	  "entry":{"layer":"guideline","subject":"ai_requirement","use":"follow","global":true,"prohibition":true},
	  "events":[{"day":"2026-09-01","workspace":"ws","kind":"violate"}]},
	 {"table":"knowledge_entries","id":"K-1","status":"retired","text":"旧知识"}]`
	if err := os.WriteFile(file, []byte(recs), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := importEnkeep([]string{"-file", file, "-state", dir + "/state", "-day", "2026-10-08"}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir + "/state")
	if err != nil {
		t.Fatal(err)
	}
	r := st.S.Memory.Entries["ek-rules-R-1"]
	k := st.S.Memory.Entries["ek-knowledge_entries-K-1"]
	if r == nil || r.Status.State != memory.Active || k == nil || k.Status.State != memory.Obsolete {
		t.Fatalf("imported: %+v %+v", r, k)
	}
	if err := importEnkeep([]string{"-file", file}); err == nil {
		t.Fatal("missing flags accepted")
	}
}
