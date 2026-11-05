package pluginkit

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// The inbox rules' self-check: a plugin's tests run CheckManifest on its manifest and CheckThings on what its
// connector.inbox.collect answers, and find out in their own repository what the hub's install check (and its inbox)
// would refuse.

// InboxManifest is what the inbox rules read in a manifest: its connectors that declare an inbox.
type InboxManifest struct {
	Connectors []InboxConnector
}

// InboxConnector is a connector with an inbox, and whether it brings its own fallback.
type InboxConnector struct {
	ID       string
	Fallback bool
}

// Fallback says whether connector id declares CapFallback.
func (m InboxManifest) Fallback(id string) bool {
	return slices.ContainsFunc(m.Connectors, func(c InboxConnector) bool { return c.ID == id && c.Fallback })
}

// CheckManifest holds a manifest (persona-extension.json) to the inbox rules as the hub's install check does
// (nwverse/persona#1730): a plugin with a connector that declares "inbox" has the setting triage_rules, a string at
// the top of its configuration; "fallback" goes with an inbox. It answers which connectors have an inbox and which a
// fallback. CheckStandardSettings holds the setting to the standard look on top.
func CheckManifest(manifest []byte) (InboxManifest, error) {
	m, err := readInboxManifest(manifest)
	if err != nil {
		return InboxManifest{}, err
	}
	out := InboxManifest{}
	var errs []error
	for _, c := range m.Contributes.Connectors {
		inbox, fallback := slices.Contains(c.Capabilities, CapInbox), slices.Contains(c.Capabilities, CapFallback)
		if fallback && !inbox {
			errs = append(errs, fmt.Errorf("connector %s declares %s without %s", c.ID, CapFallback, CapInbox))
		}
		if inbox {
			out.Connectors = append(out.Connectors, InboxConnector{ID: c.ID, Fallback: fallback})
		}
	}
	if len(out.Connectors) > 0 {
		var p struct {
			Type string `json:"type"`
		}
		raw, ok := m.Configuration.Properties[TriageRulesSetting]
		if !ok || json.Unmarshal(raw, &p) != nil || p.Type != "string" {
			errs = append(errs, fmt.Errorf("connector %s has an inbox: the settings need %s, a string (the user's own triage rules; the hub refuses to install it otherwise)",
				out.Connectors[0].ID, TriageRulesSetting))
		}
	}
	return out, errors.Join(errs...)
}

// CheckStandardSettings holds a plugin's triage_rules (when it has one) to the standard look, so every plugin's
// settings page reads the same: TriageRulesProperty's title, a string of at most TriageRulesMost characters, a
// description. The hub does not refuse a plugin for these; the standard does.
func CheckStandardSettings(manifest []byte) error {
	m, err := readInboxManifest(manifest)
	if err != nil {
		return err
	}
	raw, ok := m.Configuration.Properties[TriageRulesSetting]
	if !ok {
		return nil
	}
	var p struct {
		Type        string `json:"type"`
		Title       string `json:"title"`
		Description string `json:"description"`
		MaxLength   *int   `json:"maxLength"`
	}
	var errs []error
	if err := json.Unmarshal(raw, &p); err != nil || p.Type != "string" {
		errs = append(errs, fmt.Errorf("setting %s: a string", TriageRulesSetting))
	}
	if p.Title != TriageRulesTitle {
		errs = append(errs, fmt.Errorf("setting %s: title %q, as every plugin's (%q)", TriageRulesSetting, p.Title, TriageRulesTitle))
	}
	if p.MaxLength == nil || *p.MaxLength <= 0 || *p.MaxLength > TriageRulesMost {
		errs = append(errs, fmt.Errorf("setting %s: maxLength 1…%d", TriageRulesSetting, TriageRulesMost))
	}
	if strings.TrimSpace(p.Description) == "" {
		errs = append(errs, fmt.Errorf("setting %s: a description (TriageRulesProperty writes one)", TriageRulesSetting))
	}
	return errors.Join(errs...)
}

type inboxManifest struct {
	Contributes struct {
		Connectors []struct {
			ID           string   `json:"id"`
			Capabilities []string `json:"capabilities"`
		} `json:"connectors"`
	} `json:"contributes"`
	Configuration struct {
		Properties map[string]json.RawMessage `json:"properties"`
	} `json:"configuration"`
}

func readInboxManifest(manifest []byte) (inboxManifest, error) {
	var m inboxManifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		return m, fmt.Errorf("manifest: %w", err)
	}
	return m, nil
}

// CheckThings holds what connector.inbox.collect answers to the inbox rules: rules on every thing when the user set
// some (and the same as set, as TriageRules reads them), none when not; with a fallback, a tier the hub knows on every
// thing: extra.rule_tier (the fallback), or tier (a tier its rules decided for sure, which the hub takes as one too). things is the answer's things, as JSON or as a slice of InboxThing. A batch to
// relate (a thing with "group", Persona's inbox events) is not an item and needs no tier.
func CheckThings(things any, rules string, fallback bool) error {
	var raw []byte
	switch t := things.(type) {
	case json.RawMessage:
		raw = t
	case []byte:
		raw = t
	default:
		var err error
		if raw, err = json.Marshal(things); err != nil {
			return err
		}
	}
	var list []struct {
		Thread string          `json:"thread"`
		Rules  *string         `json:"rules"`
		Group  json.RawMessage `json:"group"`
		Tier   Tier            `json:"tier"`
		Extra  *struct {
			RuleTier *string `json:"rule_tier"`
		} `json:"extra"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		var answer struct {
			Things json.RawMessage `json:"things"`
		}
		if json.Unmarshal(raw, &answer) != nil || answer.Things == nil {
			return fmt.Errorf("things: %w", err)
		}
		return CheckThings(answer.Things, rules, fallback)
	}
	var errs []error
	for i, t := range list {
		at := fmt.Sprintf("thing %d (%s)", i, t.Thread)
		switch {
		case rules == "" && t.Rules != nil && *t.Rules != "":
			errs = append(errs, fmt.Errorf("%s: rules, but none are set", at))
		case rules != "" && (t.Rules == nil || *t.Rules != rules):
			errs = append(errs, fmt.Errorf("%s: no rules, or not the ones set (StampRules)", at))
		}
		if t.Rules != nil && utf8.RuneCountInString(*t.Rules) > TriageRulesMost {
			errs = append(errs, fmt.Errorf("%s: rules longer than %d characters", at, TriageRulesMost))
		}
		if !fallback || len(t.Group) > 0 && string(t.Group) != "null" {
			continue
		}
		if t.Tier.Known() && (t.Extra == nil || t.Extra.RuleTier == nil) {
			continue
		}
		if t.Extra == nil || t.Extra.RuleTier == nil || !Tier(*t.Extra.RuleTier).Known() {
			got := "none"
			if t.Extra != nil && t.Extra.RuleTier != nil {
				got = fmt.Sprintf("%q", *t.Extra.RuleTier)
			}
			errs = append(errs, fmt.Errorf("%s: a connector with a fallback gives every thing extra.rule_tier action, fyi or noise (%s)", at, got))
		}
	}
	return errors.Join(errs...)
}
