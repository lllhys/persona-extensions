package curate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

// The one-off migration of the judgement layer (daily/7 and the old synthesis) into entries: judgements become
// action entries, patterns classes, traits classes marked as such (they had no capable stranger's default, so they
// cannot be abstract until the gate says so), rules ① action entries. The state is backed up first; the migration is
// idempotent (an object already migrated, by its legacy id, is not migrated again), and what the user did — pins,
// deletions, their own wording — comes along. The legacy maps stay in the state as they were, read by nobody.

// MigrateReport is what a migration did.
type MigrateReport struct {
	Judgements, Patterns, Traits, Rules int // in the state
	Memory                              int // entries of the pre-v4 memory layer
	Entries                             int // entries made (or found made) from them
	New                                 int // entries this run made
	Backup                              string
}

// dimensionFacet maps the old judgement dimensions to facets: deciding, thinking and attention are default
// judgements (③), communicating is a way of handling things (④), reacting is the person (⑥).
func dimensionFacet(d string) entry.Facet {
	switch d {
	case "沟通":
		return entry.Workflow
	case "反应":
		return entry.Persona
	}
	return entry.Judgement
}

func legacyEvidence(ev []store.Evidence) []entry.Evidence {
	var out []entry.Evidence
	for _, e := range ev {
		src := entry.SrcChat
		if strings.HasPrefix(e.Chat, "chats/persona-") {
			src = entry.SrcAssistant
		}
		who := ""
		if e.Role == voice.RoleUser || e.Speaker == norm.Me {
			who = entry.WhoSelf
		}
		out = append(out, entry.Evidence{Source: src, Ref: e.Chat, Day: e.Day, N: e.N, Speaker: e.Speaker, Who: who, Role: e.Role, Quote: e.Quote})
	}
	return out
}

func legacyStatus(st string) entry.Status {
	switch st {
	case "habitual", "established", "active", "stable":
		return entry.Stable
	case "shifting":
		return entry.Shifting
	case "dormant":
		return entry.Dormant
	}
	return entry.Candidate
}

// legacyIDs maps each migrated legacy id to its entry.
func legacyIDs(s *store.State) map[string]string {
	out := map[string]string{}
	for id, e := range s.Entries {
		for _, f := range e.Flags {
			if l, ok := cutPrefix(f, flagLegacy); ok {
				out[l] = id
			}
		}
	}
	return out
}

// Migrate moves the legacy layers into entries. Call it with the store's lock held; it does not save.
func (c *Curator) Migrate() (MigrateReport, error) {
	s := c.St.S
	mem := legacyMemory(s)
	rep := MigrateReport{Judgements: len(s.Judgements), Patterns: len(s.Patterns), Traits: len(s.Traits), Rules: len(s.Rules), Memory: len(mem)}
	done := legacyIDs(s)
	todo := 0
	for _, m := range mem {
		if done[m.legacy] == "" {
			todo++
		}
	}
	for _, ids := range [][]string{sortedKeys(s.Judgements), sortedKeys(s.Patterns), sortedKeys(s.Traits), sortedKeys(s.Rules)} {
		for _, id := range ids {
			if done[id] == "" {
				todo++
			}
		}
	}
	if todo > 0 && len(done) == 0 { // the first migration: later ones only add what the old daily pass kept since
		b, err := json.Marshal(s)
		if err != nil {
			return rep, err
		}
		rep.Backup = filepath.Join(c.St.Dir, "state.json.bak-migrate-"+c.now().UTC().Format("20060102T150405Z"))
		if err := os.WriteFile(rep.Backup, b, 0o600); err != nil {
			return rep, fmt.Errorf("back up before migrating: %w", err)
		}
	}
	run := "migrate-v4"
	now := c.stamp()
	put := func(legacy string, e *entry.Entry) string {
		if id := done[legacy]; id != "" {
			return id
		}
		e.ID = EntryID(e.Facet, e.Grain, e.Text, e.Person)
		e.Flags = appendFlag(e.Flags, flagLegacy+legacy)
		if old := s.Entries[e.ID]; old != nil { // two legacy objects saying the same: one entry
			mergeSighting(old, e, e.First)
			old.Members = append(old.Members, slices.DeleteFunc(e.Members, func(x string) bool { return slices.Contains(old.Members, x) })...)
			old.Flags = appendFlag(old.Flags, flagLegacy+legacy)
			old.Pinned = old.Pinned || e.Pinned
			c.commit(entry.EvObserve, old, "", "migrated", run)
			done[legacy] = old.ID
			return old.ID
		}
		if e.Origin == "" {
			e.Origin = entry.Behavior
		}
		if e.Scopes == nil {
			e.Scopes = []entry.Scope{{Kind: entry.ScopeGlobal}}
		}
		e.Updated = now
		recountOwn(e)
		s.Entries[e.ID] = e
		if e.Deleted && !tombstoned(s, e.Facet, e.Text, e.Person) {
			s.Tombstones = append(s.Tombstones, entry.Tombstone{Facet: e.Facet, Key: tombKey(e.Person, e.Text), Text: e.Text, At: now})
		}
		c.commit(entry.EvCreate, e, "", "migrated", run)
		done[legacy] = e.ID
		rep.New++
		return e.ID
	}
	// Judgements first: patterns and traits point at them.
	for _, id := range sortedKeys(s.Judgements) {
		j := s.Judgements[id]
		e := &entry.Entry{Facet: dimensionFacet(j.Dimension), Grain: entry.Action, Text: firstNonEmpty(j.Claim, j.Choice), Situation: j.Trigger,
			Choice: j.Choice, Rejected: j.Rejected, Why: j.Why, Inferred: j.Inferred, Domain: entry.Work, Evidence: legacyEvidence(j.Evidence),
			First: j.Day, Last: j.Day, Count: 1, Status: entry.Candidate, OriginVersion: "migrated@" + j.Prompt}
		e.Flags = append(e.Flags, "legacy_dimension:"+j.Dimension)
		put(id, e)
	}
	for _, id := range sortedKeys(s.Patterns) {
		p := s.Patterns[id]
		e := &entry.Entry{Facet: dimensionFacet(p.Dimension), Grain: entry.Class, Text: firstNonEmpty(p.Definition, p.Name), Type: p.Name,
			Situation: p.Trigger, Choice: p.Choice, Exception: p.NotWhen, Domain: entry.Work, Evidence: legacyEvidence(p.Prior),
			First: p.FirstSeen, Last: p.LastSeen, Status: legacyStatus(p.Status), Pinned: p.Pinned, Deleted: p.Deleted, OriginVersion: "migrated@synth"}
		if p.Seed {
			e.Flags = append(e.Flags, "legacy_seed")
		}
		members, counters := slices.Clone(p.Members), slices.Clone(p.Counters)
		for _, jid := range sortedKeys(s.Judgements) {
			if j := s.Judgements[jid]; j.Pattern == id && !slices.Contains(members, jid) {
				members = append(members, jid)
			} else if j.Counter == id && !slices.Contains(counters, jid) {
				counters = append(counters, jid)
			}
		}
		for _, jid := range members {
			if x := done[jid]; x != "" && !slices.Contains(e.Members, x) {
				e.Members = append(e.Members, x)
			}
		}
		for _, jid := range counters {
			if x := done[jid]; x != "" && !slices.Contains(e.Counters, x) {
				e.Counters = append(e.Counters, x)
			}
		}
		pid := put(id, e)
		for _, mid := range s.Entries[pid].Members {
			if m := s.Entries[mid]; m != nil && m.Parent == "" {
				m.Parent = pid
				c.commit(entry.EvFile, m, pid, "migrated", run)
			}
		}
		for _, cid := range s.Entries[pid].Counters {
			if m := s.Entries[cid]; m != nil {
				m.Flags = appendFlag(m.Flags, flagCounterOf+pid)
			}
		}
		recount(s, s.Entries[pid])
	}
	for _, id := range sortedKeys(s.Traits) {
		t := s.Traits[id]
		e := &entry.Entry{Facet: entry.Judgement, Grain: entry.Class, Text: firstNonEmpty(t.Definition, t.Name), Type: t.Name,
			Situation: t.Applies, Exception: t.NotApplies, Domain: entry.Work, First: t.FirstSeen, Last: t.LastSeen, Status: entry.Candidate,
			Pinned: t.Pinned, Deleted: t.Deleted, OriginVersion: "migrated@synth"}
		e.Flags = append(e.Flags, "legacy_trait")
		for _, pid := range t.Patterns {
			if x := s.Entries[done[pid]]; x != nil {
				e.Situations = appendFlag(e.Situations, firstNonEmpty(x.Situation, x.Type))
				for _, mid := range x.Members {
					if !slices.Contains(e.Members, mid) {
						e.Members = append(e.Members, mid)
					}
				}
			}
		}
		tid := put(id, e)
		recount(s, s.Entries[tid])
	}
	for _, id := range sortedKeys(s.Rules) {
		r := s.Rules[id]
		e := &entry.Entry{Facet: entry.Interaction, Grain: entry.Action, Text: r.Text, Why: r.Why, Domain: entry.Work, Evidence: legacyEvidence(r.Evidence),
			First: r.FirstSeen, Last: r.LastSeen, Count: max(1, r.Count), Status: legacyStatus(r.Status), Pinned: r.Pinned, Deleted: r.Deleted,
			Impact: entry.Impact{Moment: "every_turn", Action: r.Text}, OriginVersion: "migrated@rules"}
		if !globalScope(r.Scope) && strings.TrimSpace(r.Scope) != "" {
			e.Situation = r.Scope
		}
		if r.Edited {
			e.User = &entry.UserEdit{Text: r.Text, At: now}
		}
		if r.Durable {
			e.Flags = append(e.Flags, "durable")
		}
		if r.Hard {
			e.Flags = append(e.Flags, "hard")
		}
		put(id, e)
	}
	for _, m := range legacyMemory(s) {
		put(m.legacy, m.e)
	}
	rep.Entries = len(slices.Compact(sortedValues(done)))
	return rep, nil
}

func sortedValues(m map[string]string) []string {
	var out []string
	for _, k := range sortedKeys(m) {
		out = append(out, m[k])
	}
	slices.Sort(out)
	return out
}

type legacyEntry struct {
	legacy string
	e      *entry.Entry
}

// legacyMemory reads the entries of the pre-v4 memory layer (kept raw in State.LegacyMemory) as entries: the category
// gives the facet (cognitive ③, interaction ①, profile ⑥, knowledge ⑤), the evidence keeps its lines and who said
// them, and a dropped or obsolete entry comes over retired, for its history.
func legacyMemory(s *store.State) []legacyEntry {
	if len(s.LegacyMemory) == 0 {
		return nil
	}
	var book struct {
		Entries map[string]struct {
			ID       string `json:"id"`
			Text     string `json:"text"`
			Category string `json:"category"`
			Evidence []struct {
				Day     string `json:"day"`
				Source  string `json:"source"`
				Anchor  string `json:"anchor"`
				Quote   string `json:"quote"`
				Speaker string `json:"speaker"`
			} `json:"evidence"`
			Status struct {
				State string `json:"state"`
			} `json:"status"`
		} `json:"entries"`
	}
	if json.Unmarshal(s.LegacyMemory, &book) != nil {
		return nil
	}
	facet := map[string]entry.Facet{"cognitive": entry.Judgement, "interaction": entry.Interaction, "profile": entry.Persona, "knowledge": entry.World}
	var out []legacyEntry
	for _, id := range sortedKeys(book.Entries) {
		m := book.Entries[id]
		f := facet[m.Category]
		if f == "" || strings.TrimSpace(m.Text) == "" {
			continue
		}
		e := &entry.Entry{Facet: f, Grain: entry.Action, Text: m.Text, Domain: entry.Work, Status: legacyStatus(m.Status.State), OriginVersion: "migrated@memory"}
		if m.Status.State == "obsolete" || m.Status.State == "dropped" {
			e.Status = entry.Retired
		}
		for _, x := range m.Evidence {
			src := entry.SrcChat
			if strings.HasPrefix(x.Source, "chats/persona-") {
				src = entry.SrcAssistant
			}
			n := 0
			if _, num, ok := strings.Cut(x.Anchor, "#"); ok {
				fmt.Sscanf(num, "%d", &n)
			}
			ev := entry.Evidence{Source: src, Ref: x.Source, Day: x.Day, N: n, Quote: x.Quote, Speaker: x.Speaker}
			if x.Speaker == "user" {
				ev.Who, ev.Role = entry.WhoSelf, voice.RoleUser
			}
			e.Evidence = append(e.Evidence, ev)
			if e.First == "" || x.Day < e.First {
				e.First = x.Day
			}
			if x.Day > e.Last {
				e.Last = x.Day
			}
		}
		out = append(out, legacyEntry{"memory:" + id, e})
	}
	return out
}
