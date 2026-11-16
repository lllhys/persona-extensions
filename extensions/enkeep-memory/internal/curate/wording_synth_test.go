package curate

import (
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
)

// A missing Text is synthesized before the entry is built; wording validation must inspect that synthesized text,
// not the model's original empty Text field, so an Object that says 我 cannot bypass the first-person check.
func TestSynthesizedObservationTextGetsWordingCheck(t *testing.T) {
	c, _ := newCurator(t, nil, "shadow")
	u, d := d8unit("chats/a", "dm", "10:00 助手: 超时后重试")
	o := obs("tools", "", 1, "超时后重试")
	o.Object, o.Pitfall, o.Fix, o.Domain = "我", "超时", "重试", entry.Work
	run := d8ingest(t, c, u, d, schema.DailyOutput{Observations: []schema.Observation{o}}, nil)
	if run.Counts["obs_rejected:"+obsSelfWord] != 1 || run.Counts["obs_kept"] != 0 {
		t.Fatalf("synthesized first-person text bypassed wording validation: %+v", run.Counts)
	}
}
