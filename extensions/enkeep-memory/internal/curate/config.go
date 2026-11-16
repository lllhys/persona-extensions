package curate

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

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

	ActivityDays        int      `json:"activity_days"`    // 3
	Tiers               bool     `json:"tiers"`            // true: each unit gets a tier that bounds what it may give
	EngagedDays         int      `json:"engaged_days"`     // 30: a group the owner wrote in within these days is the owner's
	NoiseMembers        int      `json:"noise_members"`    // 200: a quiet group this big that does not name the owner is noise
	MentionWindow       int      `json:"mention_window"`   // 15
	AdmitAllBelowTokens int      `json:"admit_all_below"`  // 20000 (estimated tokens); was admit_all_below_tokens
	AIMinRelevance      int      `json:"ai_min_relevance"` // 60
	PinChats            []string `json:"pin_chats"`
	BlockChats          []string `json:"block_chats"`
	BlockSenders        []string `json:"block_senders"`
	BulkDomains         []string `json:"bulk_domains"`
	PrivateKeywords     []string `json:"privacy_keywords"` // was private_keywords
	OwnerAliases        []string `json:"owner_aliases"`

	Graph            bool     `json:"graph"` // true: entity-relation graph from every source
	CustomTypes      []string `json:"custom_types"`
	CustomPredicates []string `json:"custom_predicates"`

	AutoApplyKinds        []string `json:"auto_apply_kinds"`         // none
	AutoApplyMinPrecision float64  `json:"auto_apply_min_precision"` // 0.95
	ProposalExpiryDays    int      `json:"proposal_expiry_days"`     // 14
	BackfillDays          int      `json:"backfill_days"`            // the first-run replay's reach in days (4.1); 0: none
	RerunRange            string   `json:"rerun_range"`              // "YYYY-MM-DD..YYYY-MM-DD": days to digest again with the current daily prompt, once each
	StaleDays             int      `json:"stale_days"`               // 30
}

// Defaults are the settings when nothing is set.
func Defaults() Config {
	return Config{
		Mode: "auto", ShadowDays: 14, DailyAt: "03:30", WeeklyAt: "05:30",
		DailyTier: "small", WeeklyTier: "medium",
		ActivityDays: 3, Tiers: true, EngagedDays: 30, NoiseMembers: 200, MentionWindow: 15, AdmitAllBelowTokens: 20000, AIMinRelevance: 60,
		Graph: true, BackfillDays: DefaultBackfillDays, AutoApplyMinPrecision: 0.95, ProposalExpiryDays: 14, StaleDays: 30,
	}
}

// ParseConfig reads the settings over the defaults.
func ParseConfig(raw json.RawMessage) (Config, error) {
	c := Defaults()
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &c); err != nil {
			return c, err
		}
		// The names before 0.2.0 (the hub's install preview took them for secrets: "token", "private_key"). A hub
		// keeps a configuration as it was saved, so they can still be there: a value set under the old name counts
		// when the new one is at its default, and privacy keywords from both are kept, so none is ever lost.
		var old struct {
			AdmitAllBelowTokens *int     `json:"admit_all_below_tokens"`
			PrivateKeywords     []string `json:"private_keywords"`
		}
		if err := json.Unmarshal(raw, &old); err != nil {
			return c, err
		}
		if old.AdmitAllBelowTokens != nil && c.AdmitAllBelowTokens == Defaults().AdmitAllBelowTokens {
			c.AdmitAllBelowTokens = *old.AdmitAllBelowTokens
		}
		for _, k := range old.PrivateKeywords {
			if !slices.Contains(c.PrivateKeywords, k) {
				c.PrivateKeywords = append(c.PrivateKeywords, k)
			}
		}
	}
	return c, nil
}

func (c Config) triage() triage.Config {
	return triage.Config{ActivityDays: c.ActivityDays, Tiers: c.Tiers, EngagedDays: c.EngagedDays, NoiseMembers: c.NoiseMembers, MentionWindow: c.MentionWindow, AdmitAllBelow: c.AdmitAllBelowTokens,
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

// RerunDays are the days of RerunRange in order, at most 31; empty when it is unset or not a valid range.
func (c Config) RerunDays() []string {
	from, to, ok := strings.Cut(c.RerunRange, "..")
	if !ok {
		return nil
	}
	a, err1 := time.Parse("2006-01-02", from)
	b, err2 := time.Parse("2006-01-02", to)
	if err1 != nil || err2 != nil || b.Before(a) {
		return nil
	}
	var days []string
	for d := a; !d.After(b) && len(days) < 31; d = d.AddDate(0, 0, 1) {
		days = append(days, d.Format("2006-01-02"))
	}
	return days
}
