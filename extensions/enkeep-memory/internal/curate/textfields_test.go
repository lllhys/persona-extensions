package curate

import (
	"testing"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
)

// A model that fills a judgement's fields and leaves the sentence out: the sentence is made of the fields; a facet
// without such fields (persona) gets none and stays a schema rejection.
func TestTextFromFields(t *testing.T) {
	for _, c := range []struct {
		f    entry.Facet
		o    schema.Observation
		lang string
		want string
	}{
		{entry.Judgement, schema.Observation{Situation: "发布有风险的改动", Choice: "小步分批合入", Rejected: "一次合大版本", Why: "好回滚"}, "zh-Hans",
			"发布有风险的改动时，小步分批合入，而不是一次合大版本，因为好回滚。"},
		{entry.Workflow, schema.Observation{Situation: "releasing a risky change", Choice: "merges in small steps"}, "en",
			"When releasing a risky change: merges in small steps."},
		{entry.Tools, schema.Observation{Object: "tool-x", Pitfall: "长 sleep 会被拦", Fix: "改用轮询"}, "zh-Hans", "tool-x：长 sleep 会被拦；改用轮询。"},
		{entry.Judgement, schema.Observation{Situation: "只有情境"}, "zh-Hans", ""},
		{entry.Persona, schema.Observation{Choice: "x"}, "zh-Hans", ""},
	} {
		if got := textFromFields(c.f, c.o, c.lang); got != c.want {
			t.Errorf("%s %+v: %q, want %q", c.f, c.o, got, c.want)
		}
	}
}
