package github

import (
	"context"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/github/internal/connector"
)

// Every inbox thing carries the user's rules and a fallback tier: the rules' own, fyi where they leave it to the model.
func TestCollectStampsRulesAndFallback(t *testing.T) {
	c := New(Config{}, "", nil).WithRules("机器人的 PR 都算噪音")
	c.inbox.things = map[string]*connector.InboxThing{
		"o/r#1": {Thread: "o/r#1", Kind: "review_requested", Tier: connector.TierAction},
		"o/r#2": {Thread: "o/r#2", Kind: "author"},
	}
	things, _ := c.Collect(context.Background())
	got := map[string]string{}
	for _, th := range things {
		if th.Rules != "机器人的 PR 都算噪音" || th.Extra == nil {
			t.Fatalf("%+v", th)
		}
		got[th.Thread] = th.Extra.RuleTier
	}
	if got["o/r#1"] != "action" || got["o/r#2"] != "fyi" {
		t.Fatalf("fallback tiers %v", got)
	}
	if c.inbox.things["o/r#2"].Extra != nil {
		t.Fatal("stamping changed what the connector keeps")
	}
}
