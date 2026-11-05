package pluginkit

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

// The inbox (the host's inbox contract, nwverse/persona#1730): what every plugin whose connector declares the
// capability "inbox" follows, as ready-made pieces. Three rules are common; how a plugin decides when to hand things
// over (which kinds at once, how often the rest) is its own.
//
//   - Waking the hub: InboxChanged after something moved (the hub also polls every poll_seconds).
//   - The user's own triage rules: the setting triage_rules (TriageRulesProperty, the same in every plugin's form),
//     read with TriageRules and carried on every thing as rules (StampRules).
//   - An optional fallback: a connector that declares the capability "fallback" (CapFallback) gives every thing a
//     tier of its own rules in extra.rule_tier (ThingExtra.RuleTier), used when no model sorts it.
//
// CheckManifest and CheckThings hold a plugin to them in its own tests, as the hub does when it installs one.

// Tier is a tier of the inbox: the only three the hub knows.
type Tier string

const (
	TierAction Tier = "action" // needs you
	TierFYI    Tier = "fyi"    // for your information
	TierNoise  Tier = "noise"  // noise
)

// Known says whether t is one of the hub's tiers ("" is not).
func (t Tier) Known() bool { return t == TierAction || t == TierFYI || t == TierNoise }

// Connector capabilities the inbox rules read.
const (
	CapInbox = "inbox"
	// CapFallback: the connector sorts its own things when no model does, extra.rule_tier on every thing (a hub
	// from before nwverse/persona#1730 refuses it: declare it with that hub's build in engines.persona).
	CapFallback = "fallback"
)

// InboxThing is one thing for the inbox (connector.inbox.collect's things, Persona's connector.InboxThing): the
// fields the hub reads for every source. X is the plugin's own extra block: a struct that embeds ThingExtra, so
// rule_tier sits beside the plugin's fields in "extra".
type InboxThing[X any] struct {
	// Thread is the connector's stable key of the thread; Kind its kind of thing (one of the manifest's kinds), Label
	// the row's tag for people.
	Thread string `json:"thread"`
	Kind   string `json:"kind"`
	Label  string `json:"label,omitempty"`
	Title  string `json:"title,omitempty"`
	// Sender made the newest move, Preview is it in a line, Context the thread as text (for triage and drafting).
	Sender  string `json:"sender,omitempty"`
	Preview string `json:"preview,omitempty"`
	Context string `json:"context,omitempty"`
	// At is when the newest move happened; Last marks it (a different Last adds to the open item).
	At      time.Time `json:"at,omitempty"`
	Last    string    `json:"last,omitempty"`
	URL     string    `json:"url,omitempty"`
	OpenURL string    `json:"open_url,omitempty"`
	// ReplyTo says where a reply goes and who sees it; CanReply: connector.inbox.reply works for the thread.
	ReplyTo  string `json:"reply_to,omitempty"`
	CanReply bool   `json:"can_reply,omitempty"`
	// Tier is a tier the connector's rules decided for sure ("" leaves it to the model); Why says which rule.
	Tier Tier   `json:"tier,omitempty"`
	Why  string `json:"why,omitempty"`
	// Rules are the user's own triage rules for this source (StampRules): the hub sorts the thing by them instead of
	// its global rules.
	Rules string `json:"rules,omitempty"`
	// Done ends the open item ("read" or "replied"); Read: seen somewhere else.
	Done string `json:"done,omitempty"`
	Read bool   `json:"read,omitempty"`
	// Tags are what the thing is about (chat, sender, repo…), for muting and rules.
	Tags  []InboxTag `json:"tags,omitempty"`
	Extra *X         `json:"extra,omitempty"`
	// Group makes the thing a batch of a conversation's new messages to relate to its events (Persona's inbox events,
	// nwverse/persona#1680): the hub does not make it an item.
	Group *ThingGroup `json:"group,omitempty"`
	// ID and Meta are an event's (group.append's answer): the item's id, and the connector's record of the event, which
	// the hub stores and hands back as it is.
	ID   string          `json:"id,omitempty"`
	Meta json.RawMessage `json:"meta,omitempty"`
}

// ThingGroup is a batch to relate: its id (the same while the same batch is handed again) and the new messages' ids,
// opaque to the hub.
type ThingGroup struct {
	Batch string   `json:"batch"`
	New   []string `json:"new"`
}

// ThingExtra is the part of a thing's "extra" the inbox rules define; a plugin embeds it in its own extra block.
type ThingExtra struct {
	// RuleTier is the tier the connector's own rules give the thing, used when no model sorts it: on every thing of
	// a connector that declares CapFallback.
	RuleTier Tier `json:"rule_tier,omitempty"`
}

// InboxTag is one thing an inbox thing is about: Key (chat, sender, repo, branch), the Value it matches by (an id),
// the Name people read.
type InboxTag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Name  string `json:"name,omitempty"`
}

// The setting triage_rules.
const (
	// TriageRulesSetting is its name in the configuration (the hub's assistant edits it by this name).
	TriageRulesSetting = "triage_rules"
	// TriageRulesMost is how long the rules are, in characters (the hub keeps no more).
	TriageRulesMost = 4000
	// TriageRulesTitle is its title in every plugin's settings.
	TriageRulesTitle = "分拣规则"
)

// TriageRulesProperty is the setting as a plugin's manifest declares it under configuration.properties (copy it; a
// test can compare): source names the plugin's things for the user ("Feishu", "GitHub"), example is a rule in the
// user's words. A "\n" written out is a line break (the apps' settings fields are one line): TriageRules reads it so.
func TriageRulesProperty(source, example string) map[string]any {
	return map[string]any{
		"type":      "string",
		"title":     TriageRulesTitle,
		"maxLength": TriageRulesMost,
		"description": source + "的条目按什么分「需要你 / 知悉即可 / 噪音」，用你自己的话写；填了就只用它、不用收件箱的全局规则（只管" + source +
			"）。一般不在这里改：和助手说（比如「" + example + "」），它来改。写 \\n 换行；空着用全局规则",
	}
}

// TriageRules reads the rules from a plugin's configuration (initialize's config): trimmed, a "\n" written out made a
// line break, at most TriageRulesMost characters; "" when unset or blank.
func TriageRules(config []byte) string {
	var c struct {
		Rules string `json:"triage_rules"`
	}
	if len(config) > 0 {
		_ = json.Unmarshal(config, &c)
	}
	rules := strings.TrimSpace(strings.ReplaceAll(c.Rules, `\n`, "\n"))
	if utf8.RuneCountInString(rules) > TriageRulesMost {
		rules = strings.TrimSpace(string([]rune(rules)[:TriageRulesMost]))
	}
	return rules
}

// StampRules puts the rules on every thing (none when they are blank: the hub's global rules then).
func StampRules[X any](things []InboxThing[X], rules string) {
	for i := range things {
		things[i].Rules = rules
	}
}

// InboxChanged wakes the hub: the connector's inbox has something new (connector.changed {scope: inbox}). contribution
// is the connector's id ("" when the plugin has one connector).
func (h *Hub) InboxChanged(contribution string) error {
	params := map[string]string{"scope": "inbox"}
	if contribution != "" {
		params["contribution"] = contribution
	}
	return h.Notify("connector.changed", params)
}
