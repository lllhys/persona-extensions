package curate

import (
	"encoding/json"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/memory"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
)

// Config is the extension's settings (persona-extension.json "configuration"; the hub fills in the defaults, and
// Defaults does the same for dry runs and tests). Every default is the one docs/design.md §9 settles on.
type Config struct {
	Mode       string `json:"mode"`        // auto (shadow for shadow_days, then review) | shadow | review
	ShadowDays int    `json:"shadow_days"` // 14
	Timezone   string `json:"timezone"`    // empty: the hub's
	Language   string `json:"language"`    // empty: the hub's
	DailyAt    string `json:"daily_at"`    // 03:30, local time: digests the day before
	WeeklyAt   string `json:"weekly_at"`   // 05:30 on Mondays: the ISO week before

	DailyTier  string `json:"daily_tier"`  // small (Haiku class)
	WeeklyTier string `json:"weekly_tier"` // medium (Sonnet class)

	ActivityDays        int      `json:"activity_days"`          // 3
	MentionWindow       int      `json:"mention_window"`         // 15
	AdmitAllBelowTokens int      `json:"admit_all_below_tokens"` // 20000
	AIMinRelevance      int      `json:"ai_min_relevance"`       // 60
	PinChats            []string `json:"pin_chats"`
	BlockChats          []string `json:"block_chats"`
	BlockSenders        []string `json:"block_senders"`
	BulkDomains         []string `json:"bulk_domains"`
	PrivateKeywords     []string `json:"private_keywords"`
	OwnerAliases        []string `json:"owner_aliases"`

	Graph            bool     `json:"graph"` // true: entity-relation graph from every source
	CustomTypes      []string `json:"custom_types"`
	CustomPredicates []string `json:"custom_predicates"`

	AutoApplyKinds        []string `json:"auto_apply_kinds"`         // none
	AutoApplyMinPrecision float64  `json:"auto_apply_min_precision"` // 0.95
	ProposalExpiryDays    int      `json:"proposal_expiry_days"`     // 14
	BackfillDays          int      `json:"backfill_days"`            // 0: no one-off history pass
	StaleDays             int      `json:"stale_days"`               // 30

	// Memory is every number of the memory model (weights, lines, decay, budgets) in one place. It is not a
	// setting in the manifest: the defaults are the design's, and a retune changes memory.DefaultParams.
	Memory memory.Params `json:"memory"`
}

// Defaults are the settings when nothing is set.
func Defaults() Config {
	return Config{
		Mode: "auto", ShadowDays: 14, DailyAt: "03:30", WeeklyAt: "05:30",
		DailyTier: "small", WeeklyTier: "medium",
		ActivityDays: 3, MentionWindow: 15, AdmitAllBelowTokens: 20000, AIMinRelevance: 60,
		Graph: true, AutoApplyMinPrecision: 0.95, ProposalExpiryDays: 14, StaleDays: 30,
		Memory: memory.DefaultParams(),
	}
}

// ParseConfig reads the settings over the defaults.
func ParseConfig(raw json.RawMessage) (Config, error) {
	c := Defaults()
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &c); err != nil {
			return c, err
		}
	}
	return c, nil
}

func (c Config) triage() triage.Config {
	return triage.Config{ActivityDays: c.ActivityDays, MentionWindow: c.MentionWindow, AdmitAllBelow: c.AdmitAllBelowTokens,
		AIMinRelevance: c.AIMinRelevance, PinChats: c.PinChats, BlockChats: c.BlockChats, BlockSenders: c.BlockSenders,
		BulkDomains: c.BulkDomains, Private: c.PrivateKeywords}
}

func tier(s string) model.Tier {
	if s == "medium" {
		return model.Medium
	}
	return model.Small
}

// Shadow says whether the curator only proposes (never writes) on day `now`.
func (c Config) Shadow(installed string, now time.Time) bool {
	switch c.Mode {
	case "shadow":
		return true
	case "review":
		return false
	}
	t, err := time.Parse(time.RFC3339, installed)
	if err != nil {
		return true
	}
	return now.Sub(t) < time.Duration(c.ShadowDays)*24*time.Hour
}
