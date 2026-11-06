package main

import (
	"encoding/json"
	"testing"

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
