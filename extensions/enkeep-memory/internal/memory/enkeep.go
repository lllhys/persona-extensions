package memory

import (
	"fmt"
	"slices"
	"sort"
)

// EnkeepRecord is one enkeep record as an export gives it: a rule, a pattern, a knowledge entry, a profile fact or
// a workspace state, with the evidence events it has. The migration runs once (6.18 §7).
type EnkeepRecord struct {
	Table  string `json:"table"`             // rules | patterns | knowledge_entries | profile_facts | workspace_state
	ID     string `json:"id"`                // enkeep's id
	Text   string `json:"text"`              // the rule's text (written by an AI: never the user's words by itself)
	Status string `json:"status"`            // enkeep's own status
	Source string `json:"source,omitempty"`  // knowledge_entries: extract | legacy_kb | legacy_index
	IsCore bool   `json:"is_core,omitempty"` // patterns
	IsHard bool   `json:"is_hard,omitempty"` // rules
	Domain string `json:"domain,omitempty"`  // profile_facts
	// SameAs: a legacy import that says what an existing entry says joins it as side evidence, and is not an entry
	// of its own.
	SameAs string `json:"same_as,omitempty"`
	// Entry is the classification of the record (layer, subject, use, kind of matter, …), done before the import.
	Entry  Entry         `json:"entry"`
	Events []EnkeepEvent `json:"events,omitempty"`
}

// EnkeepEvent is one enkeep evidence event: rule_events with an observation (new, trigger, reactivate, violate,
// promote, user_confirm, user_modify, user_reject), pattern_evidence, or an obs_ids_json observation.
type EnkeepEvent struct {
	Day       string `json:"day"`
	Workspace string `json:"workspace"`
	Kind      string `json:"kind"`
	Ref       string `json:"ref,omitempty"`   // the observation id
	Quote     string `json:"quote,omitempty"` // the user's words in the observation, if any
	Tags      *Tags  `json:"tags,omitempty"`  // rule D's tags, when the sentence was judged
}

// enkeepStatus maps a record's status (6.18 §7's table).
func enkeepStatus(r EnkeepRecord) EnkeepStatus {
	switch r.Table {
	case "rules":
		switch r.Status {
		case "confirmed":
			return EnkeepConfirmed
		case "dormant", "latent":
			return EnkeepDormant
		case "deprecated":
			return EnkeepRetired
		}
	case "patterns":
		switch {
		case r.Status == "merged" || r.Status == "retired":
			return EnkeepRetired
		case r.Status == "dormant":
			return EnkeepDormant
		case r.IsCore:
			return EnkeepConfirmed
		}
	case "knowledge_entries":
		if r.Status == "superseded" || r.Status == "retired" {
			return EnkeepRetired
		}
	case "profile_facts":
		switch r.Status {
		case "confirmed":
			return EnkeepConfirmed
		case "stale", "retired":
			return EnkeepRetired
		}
	}
	return EnkeepOpen
}

// Import brings enkeep records into the book on importDay. Its history is accumulated by the same weights and
// formula, without decay before the import day (the grace starts then); the state follows enkeep's, so behaviour
// does not change across the migration. A confirmed rule counts as rule B (the migration exemption, once: new
// evidence afterwards is judged as usual). What enkeep had retired comes in obsolete, and enkeep's later
// confirmations do not bring it back. A user_reject leaves a veto.
func Import(b *Book, recs []EnkeepRecord, importDay string) (imported, joined int, err error) {
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].SameAs == "" && recs[j].SameAs != "" })
	for _, r := range recs {
		if r.ID == "" || r.Table == "" {
			return imported, joined, fmt.Errorf("enkeep record without table or id: %+v", r)
		}
		legacy := r.Table == "knowledge_entries" && (r.Source == "legacy_kb" || r.Source == "legacy_index")
		ev, events := enkeepEvidence(r, legacy, importDay)
		if r.SameAs != "" && legacy {
			if cur := b.Entries[r.SameAs]; cur != nil {
				for _, x := range ev {
					x.Imported = true
					if !hasEvidence(cur.Evidence, x) {
						cur.Evidence = append(cur.Evidence, x)
					}
				}
				joined++
				continue
			}
		}
		e := r.Entry
		if e.ID == "" {
			e.ID = "ek-" + r.Table + "-" + r.ID
		}
		if e.Text == "" {
			e.Text = r.Text
		}
		e.Imported = r.Table + ":" + r.ID
		e.ImportDay = importDay
		e.Hard = e.Hard || r.IsHard
		if e.Domain == "" {
			e.Domain = r.Domain
		}
		switch r.Table {
		case "workspace_state":
			e.Layer, e.FactCategory = Fact, Progress
		case "patterns":
			if e.Layer == "" {
				e.Layer, e.Descriptive, e.FactCategory, e.Use = Fact, true, Attribute, UseUnderstand
			}
		}
		e.Evidence = append(e.Evidence, ev...)
		e.Events = append(e.Events, events...)
		status := enkeepStatus(r)
		if slices.ContainsFunc(events, func(x EntryEvent) bool { return x.Kind == EvReject }) {
			status = EnkeepRejected
		}
		e.Events = append(e.Events, EntryEvent{Day: importDay, Kind: EvImport, Status: status, Anchor: e.Imported})
		b.Put(&e)
		imported++
	}
	return imported, joined, nil
}

// enkeepEvidence turns the record's events into evidence, one per (day, workspace) as enkeep counts them.
func enkeepEvidence(r EnkeepRecord, legacy bool, importDay string) ([]Evidence, []EntryEvent) {
	var ev []Evidence
	var events []EntryEvent
	seen := map[string]bool{}
	for _, x := range r.Events {
		anchor := "enkeep:" + x.Ref
		switch x.Kind {
		case "user_confirm", "user_modify":
			events = append(events, EntryEvent{Day: x.Day, Kind: EvConfirm, Anchor: anchor, Quote: x.Quote, Imported: true})
			continue
		case "user_reject":
			events = append(events, EntryEvent{Day: x.Day, Kind: EvReject, Anchor: anchor, Quote: x.Quote, Imported: true})
			continue
		}
		k := x.Day + "\x00" + x.Workspace
		if seen[k] {
			continue
		}
		seen[k] = true
		e := Evidence{Day: x.Day, Source: "enkeep:" + x.Workspace, Anchor: anchor, Quote: x.Quote, Imported: true,
			Violation: x.Kind == "violate"}
		switch {
		case x.Tags != nil:
			e.Speaker, e.Tags = SpeakerUser, *x.Tags
		case x.Quote != "":
			e.Speaker = SpeakerUser
		case legacy:
			e.Speaker = SpeakerLegacy
		case x.Kind == "violate":
			e.Speaker = SpeakerUser // the user caught the AI: a correction even without its words
		default:
			e.Speaker = SpeakerAI
		}
		ev = append(ev, e)
	}
	if len(ev) == 0 && legacy {
		// A legacy import has no observations: one low registration on the import itself.
		ev = append(ev, Evidence{Day: importDay, Source: "enkeep:legacy", Anchor: "enkeep:" + r.Table + ":" + r.ID,
			Speaker: SpeakerLegacy, Imported: true})
	}
	return ev, events
}
