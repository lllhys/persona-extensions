package curate

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// The entry API every package writes through (internal/entry/CONTRACT.md). Every change is an event in events.jsonl
// that carries the entry as it is after the change, so replaying the log over an empty state gives the same entries
// (ReplayEntries). Filing, counter-examples, merges, invalidation and status changes live here; recall and the
// model's judgement are in consolidate.go, the thresholds in thresholds.go, the abstract gate in abstract.go.

// EntryID is the content address of an entry: facet initial, then a hash of facet, grain, normalised text and person.
func EntryID(f entry.Facet, g entry.Grain, text, person string) string {
	initial := "x"
	if len(f) > 0 {
		initial = string(f[0])
		if f == entry.Persona {
			initial = "u" // p is people
		}
	}
	return initial + "-" + hash(string(f), string(g), norm.Q(text), person)[:10]
}

// tombKey is the key a tombstone keeps: the normalised person and text.
func tombKey(person, text string) string { return norm.Q(person + text) }

// tombstoned says whether the user deleted the same content before.
func tombstoned(s *store.State, f entry.Facet, text, person string) bool {
	key := tombKey(person, text)
	return slices.ContainsFunc(s.Tombstones, func(t entry.Tombstone) bool { return t.Facet == f && t.Key == key })
}

// maxEvidence bounds the evidence an entry keeps: one line per unit and day beyond it is dropped oldest first, so the
// counts (which rest on distinct units and days) stay right for a long time.
const maxEvidence = 60

// Observe keeps one entry seen in a unit (or self-reported): a new entry, or one more sighting of the same one.
// It returns the id, or "" when the user deleted the same content before. Call it with the store's lock held.
func (c *Curator) Observe(e *entry.Entry, run string) string {
	s := c.St.S
	if tombstoned(s, e.Facet, e.Text, e.Person) {
		return ""
	}
	if e.Grain == "" {
		e.Grain = entry.Action
	}
	if e.Origin == "" {
		e.Origin = entry.Behavior
	}
	id := EntryID(e.Facet, e.Grain, e.Text, e.Person)
	now := c.stamp()
	day := ""
	if len(e.Evidence) > 0 {
		day = e.Evidence[0].Day
	}
	if old := s.Entries[id]; old != nil {
		if old.Deleted {
			return ""
		}
		mergeSighting(old, e, day)
		old.Scopes = unionScopes(old.Scopes, e.Scopes)
		old.Updated = now
		c.commit(entry.EvObserve, old, "", "again", run)
		return id
	}
	e.ID = id
	if e.Status == "" {
		e.Status = entry.Candidate
		if e.Origin != entry.Behavior {
			e.Status = entry.Stable // the user's own words about themself, and the hand-written memory
		}
	}
	if e.First == "" {
		e.First, e.Last = day, day
	}
	if e.Count == 0 {
		e.Count = 1
	}
	if e.Facet == entry.World && e.Predicate != "" && e.ValidFrom == "" {
		e.ValidFrom = e.First // a relation holds from when it was first seen until newer evidence says otherwise
	}
	e.Scopes = unionScopes(nil, e.Scopes)
	recountOwn(e)
	e.Updated = now
	s.Entries[id] = e
	c.commit(entry.EvObserve, e, "", "", run)
	return id
}

// mergeSighting adds a new sighting's evidence and count (one per session and day).
func mergeSighting(old, e *entry.Entry, day string) {
	seen := map[string]bool{}
	lines := map[string]bool{}
	for _, ev := range old.Evidence {
		seen[ev.Ref+"@"+ev.Day] = true
		lines[fmt.Sprintf("%s@%s#%d#%d", ev.Ref, ev.Day, ev.N, ev.Seq)] = true
	}
	fresh := false
	for _, ev := range e.Evidence {
		if !seen[ev.Ref+"@"+ev.Day] {
			fresh = true
		}
		if k := fmt.Sprintf("%s@%s#%d#%d", ev.Ref, ev.Day, ev.N, ev.Seq); !lines[k] {
			lines[k] = true
			old.Evidence = append(old.Evidence, ev)
		}
	}
	old.Evidence = trimEvidence(old.Evidence)
	if fresh {
		old.Count++
	}
	if day != "" && (old.Last == "" || day > old.Last) {
		old.Last = day
	}
	if day != "" && (old.First == "" || day < old.First) {
		old.First = day
	}
	recountOwn(old)
}

// trimEvidence keeps the newest maxEvidence lines (Count remembers the sightings whose lines went).
func trimEvidence(ev []entry.Evidence) []entry.Evidence {
	if len(ev) <= maxEvidence {
		return ev
	}
	return slices.Clone(ev[len(ev)-maxEvidence:])
}

// occurrences are the distinct (unit, day) of an entry's own evidence.
func occurrences(e *entry.Entry) []occurrence {
	var out []occurrence
	for _, ev := range e.Evidence {
		o := occurrence{ev.Ref, ev.Day}
		if ev.Day != "" && !slices.Contains(out, o) {
			out = append(out, o)
		}
	}
	return out
}

// recountOwn sets an entry's counts from its own evidence (an action, or a hand-written entry before anything is
// filed under it). Count is kept when it is higher: evidence is trimmed, sightings are not forgotten.
func recountOwn(e *entry.Entry) {
	occ := occurrences(e)
	e.Count = max(e.Count, len(occ))
	e.Chances = max(e.Chances, e.Count)
	e.Kinds = evidenceKinds(e.Evidence)
	var refs []string
	for _, o := range occ {
		if !slices.Contains(refs, o.ref) {
			refs = append(refs, o.ref)
		}
		if !slices.Contains(e.ActiveDays, o.day) {
			e.ActiveDays = append(e.ActiveDays, o.day)
		}
	}
	sort.Strings(e.ActiveDays)
	e.Sessions = max(e.Sessions, len(refs))
}

func evidenceKinds(ev []entry.Evidence) []string {
	var out []string
	for _, e := range ev {
		if e.Source != "" && !slices.Contains(out, e.Source) {
			out = append(out, e.Source)
		}
	}
	sort.Strings(out)
	return out
}

// unionScopes merges scopes: a global scope covers every other one.
func unionScopes(a, b []entry.Scope) []entry.Scope {
	var out []entry.Scope
	for _, x := range append(slices.Clone(a), b...) {
		if x.Kind == "" {
			continue
		}
		if x.Kind == entry.ScopeGlobal {
			return []entry.Scope{{Kind: entry.ScopeGlobal}}
		}
		if !slices.ContainsFunc(out, func(y entry.Scope) bool { return y.Kind == x.Kind && norm.Q(y.Name) == norm.Q(x.Name) }) {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// tally is what a class or abstract entry's counts rest on: the occurrences of its members (and of its own evidence,
// for a hand-written or self-reported entry), and those of the actions that went against it.
func tally(s *store.State, e *entry.Entry) Tally {
	var t Tally
	addFor := func(x *entry.Entry) {
		for _, o := range occurrences(x) {
			if !slices.Contains(t.For, o) {
				t.For = append(t.For, o)
			}
		}
		for _, k := range x.Kinds {
			if !slices.Contains(t.Kinds, k) {
				t.Kinds = append(t.Kinds, k)
			}
		}
	}
	var members func(x *entry.Entry, depth int)
	members = func(x *entry.Entry, depth int) {
		for _, id := range x.Members {
			m := s.Entries[id]
			if m == nil || m.Deleted || depth > 2 {
				continue
			}
			if m.Grain == entry.Action {
				addFor(m)
			} else {
				members(m, depth+1)
			}
		}
	}
	if e.Grain == entry.Action || len(e.Members) == 0 || e.Origin != entry.Behavior {
		addFor(e)
	}
	members(e, 0)
	if e.Grain == entry.Action && e.Count > len(t.For) {
		// sightings whose lines were trimmed still count
		for i := len(t.For); i < e.Count; i++ {
			t.For = append(t.For, occurrence{"trimmed", e.First})
		}
	}
	for _, id := range e.Counters {
		if m := s.Entries[id]; m != nil {
			for _, o := range occurrences(m) {
				if !slices.Contains(t.Against, o) {
					t.Against = append(t.Against, o)
				}
			}
		}
	}
	for _, o := range append(slices.Clone(t.For), t.Against...) {
		if o.ref != "trimmed" && !slices.Contains(t.Refs, o.ref) {
			t.Refs = append(t.Refs, o.ref)
		}
		if !slices.Contains(t.Days, o.day) {
			t.Days = append(t.Days, o.day)
		}
	}
	sort.Strings(t.Days)
	sort.Strings(t.Kinds)
	return t
}

// recount sets a class's or abstract entry's counts from its members (5.0): one per entry, session and day; chances
// are the times the kind of situation came up, whatever was chosen; active days are the days it was seen on; kinds
// are the distinct sources; scopes are the union of the members'.
func recount(s *store.State, e *entry.Entry) {
	if e.Grain == entry.Action {
		recountOwn(e)
		return
	}
	t := tally(s, e)
	e.Count = len(t.For)
	e.Chances = len(t.For) + len(t.Against)
	var refs, days []string
	for _, o := range t.For {
		if !slices.Contains(refs, o.ref) {
			refs = append(refs, o.ref)
		}
		if !slices.Contains(days, o.day) {
			days = append(days, o.day)
		}
	}
	sort.Strings(days)
	e.Sessions, e.ActiveDays, e.Kinds = len(refs), days, t.Kinds
	if len(days) > 0 {
		e.First, e.Last = days[0], days[len(days)-1]
	}
	for _, id := range e.Members {
		if m := s.Entries[id]; m != nil {
			e.Scopes = unionScopes(e.Scopes, m.Scopes)
		}
	}
	if e.Facet == entry.World && e.Predicate != "" {
		e.SingleSource = len(refs) < 2
	}
}

// stamp is the time an event is recorded at.
func (c *Curator) stamp() string { return c.now().UTC().Format(time.RFC3339) }

// commit records a change to e: the event carries the entry as it is now, so the log replays to the same state.
func (c *Curator) commit(kind string, e *entry.Entry, other, detail, run string) {
	data, _ := json.Marshal(e)
	c.event(entry.Event{At: c.stamp(), Run: run, Kind: kind, Entry: e.ID, Other: other, Detail: detail, Data: data})
}

// event appends to the event log; a failure to write is logged, not fatal (the state itself is saved).
func (c *Curator) event(evs ...entry.Event) {
	if err := c.St.AppendEvents(evs...); err != nil {
		c.logf("event log: %v", err)
	}
}

// ReplayEntries rebuilds the entries from the event log, oldest first: each event carries the entry after the change.
func ReplayEntries(evs []entry.Event) (map[string]*entry.Entry, error) {
	out := map[string]*entry.Entry{}
	for i, ev := range evs {
		if len(ev.Data) == 0 {
			continue
		}
		e := &entry.Entry{}
		if err := json.Unmarshal(ev.Data, e); err != nil {
			return nil, fmt.Errorf("event %d (%s %s): %w", i, ev.Kind, ev.Entry, err)
		}
		if e.ID == "" {
			continue // a payload that is not an entry (an older user event)
		}
		out[e.ID] = e
	}
	return out, nil
}

// forgetEntries removes what came from forgotten sources: their lines go from every entry, an action left with no
// line goes (and out of its class), the classes are counted again, and the event log is rewritten the same way so no
// past snapshot keeps the forgotten lines. The user's own, hand-written and self-reported entries stay without them.
// It returns how many lines and entries went, and the texts that went with them.
func (c *Curator) forgetEntries(from func(ref string) bool) (removed int, gone []string) {
	s := c.St.S
	dropped, changed := map[string]bool{}, map[string]bool{}
	scrub := func(e *entry.Entry) bool { // false: the entry goes
		n := len(e.Evidence)
		e.Evidence = slices.DeleteFunc(e.Evidence, func(ev entry.Evidence) bool { return from(ev.Ref) })
		if len(e.Evidence) == n {
			return true
		}
		return len(e.Evidence) > 0 || e.Grain != entry.Action || e.User != nil || e.Origin != entry.Behavior
	}
	for _, id := range sortedKeys(s.Entries) {
		e := s.Entries[id]
		n := len(e.Evidence)
		if !scrub(e) {
			dropped[id] = true
			gone = append(gone, e.Text, e.Situation, e.Choice, e.Why)
			delete(s.Entries, id)
			removed++
			continue
		}
		if len(e.Evidence) != n {
			removed += n - len(e.Evidence)
			changed[id] = true
			recountOwn(e)
		}
	}
	for _, id := range sortedKeys(s.Entries) {
		e := s.Entries[id]
		m, k := len(e.Members), len(e.Counters)
		e.Members = slices.DeleteFunc(e.Members, func(x string) bool { return dropped[x] })
		e.Counters = slices.DeleteFunc(e.Counters, func(x string) bool { return dropped[x] })
		if dropped[e.Parent] {
			e.Parent, changed[id] = "", true
		}
		if len(e.Members) != m || len(e.Counters) != k || changed[id] && e.Grain != entry.Action {
			recount(s, e)
			changed[id] = true
		}
	}
	if len(s.LegacyMemory) > 0 {
		wrapped := forgetLegacyMemory(marshalBackupObject(map[string]json.RawMessage{"memory": s.LegacyMemory}), from)
		if top, ok := backupObject(wrapped); ok {
			s.LegacyMemory = top["memory"]
		}
	}
	err := c.St.RewriteEvents(func(ev *entry.Event) bool {
		if dropped[ev.Entry] {
			return false
		}
		if len(ev.Data) == 0 {
			return true
		}
		e := &entry.Entry{}
		if json.Unmarshal(ev.Data, e) != nil || e.ID == "" {
			return true
		}
		if !scrub(e) {
			return false
		}
		e.Members = slices.DeleteFunc(e.Members, func(x string) bool { return dropped[x] })
		e.Counters = slices.DeleteFunc(e.Counters, func(x string) bool { return dropped[x] })
		ev.Data, _ = json.Marshal(e)
		return true
	})
	if err != nil {
		c.logf("event log: %v", err)
	}
	for _, id := range sortedKeys(changed) {
		if e := s.Entries[id]; e != nil {
			c.commit(entry.EvObserve, e, "", "forgotten source", "")
		}
	}
	return removed, gone
}

// forgetLegacyMemory removes forgotten sources from the "memory" layer a backup written before v4 still carries (the
// layer is gone, so the typed state does not see it): an entry's lines from them go, an entry left with none goes.
// Every other byte stays as it was; a backup without such lines comes back unchanged.
func forgetLegacyMemory(raw []byte, from func(ref string) bool) []byte {
	top, ok := backupObject(raw)
	if !ok || len(top["memory"]) == 0 {
		return raw
	}
	mem, ok := backupObject(top["memory"])
	if !ok {
		return raw
	}
	entries, ok := backupObject(mem["entries"])
	if !ok {
		return raw
	}
	changed := false
	for id, er := range entries {
		e, ok := backupObject(er)
		if !ok {
			continue
		}
		evs, ok := backupArray(e["evidence"])
		if !ok {
			continue
		}
		var keep []json.RawMessage
		for _, x := range evs {
			var ev struct {
				Source string `json:"source"`
			}
			if json.Unmarshal(x, &ev) == nil && ev.Source != "" && from(ev.Source) {
				continue
			}
			keep = append(keep, x)
		}
		if len(keep) == len(evs) {
			continue
		}
		changed = true
		if len(keep) == 0 {
			delete(entries, id)
			continue
		}
		e["evidence"] = marshalBackupArray(keep)
		entries[id] = marshalBackupObject(e)
	}
	if !changed {
		return raw
	}
	mem["entries"] = marshalBackupObject(entries)
	top["memory"] = marshalBackupObject(mem)
	return marshalBackupObject(top)
}

// setStatus moves an entry to a status, as an event.
func (c *Curator) setStatus(e *entry.Entry, to entry.Status, why, run string) {
	if e.Status == to {
		return
	}
	from := e.Status
	e.Status, e.StatusAt, e.Updated = to, c.stamp(), c.stamp()
	data, _ := json.Marshal(e)
	c.event(entry.Event{At: c.stamp(), Run: run, Kind: entry.EvStatus, Entry: e.ID, From: string(from), To: string(to), Detail: why, Data: data})
}

// File puts an action under a class (or a hand-written or self-reported entry, which is only counted: its text is
// never touched). Undo with Unfile.
func (c *Curator) File(actionID, classID, run string) error {
	s := c.St.S
	a, k := s.Entries[actionID], s.Entries[classID]
	if a == nil || k == nil {
		return fmt.Errorf("file %s under %s: no such entry", actionID, classID)
	}
	if a.Parent != "" && a.Parent != classID {
		c.detach(a, run)
	}
	a.Parent = classID
	a.Flags = slices.DeleteFunc(a.Flags, func(f string) bool { return f == flagStandalone })
	a.Updated = c.stamp()
	if !slices.Contains(k.Members, actionID) {
		k.Members = append(k.Members, actionID)
	}
	k.Counters = slices.DeleteFunc(k.Counters, func(x string) bool { return x == actionID })
	recount(s, k)
	k.Updated = c.stamp()
	c.commit(entry.EvFile, a, classID, "", run)
	c.commit(entry.EvFile, k, actionID, "member", run)
	return nil
}

// Counter keeps an action as a counter-example of a class: the same kind of situation, chosen the other way. It is
// kept apart and shown beside the class; it does not become a member.
func (c *Curator) Counter(actionID, classID, run string) error {
	s := c.St.S
	a, k := s.Entries[actionID], s.Entries[classID]
	if a == nil || k == nil {
		return fmt.Errorf("counter %s to %s: no such entry", actionID, classID)
	}
	if a.Parent != "" {
		c.detach(a, run)
	}
	a.Flags = appendFlag(a.Flags, flagCounterOf+classID)
	a.Updated = c.stamp()
	if !slices.Contains(k.Counters, actionID) {
		k.Counters = append(k.Counters, actionID)
	}
	recount(s, k)
	k.Updated = c.stamp()
	c.commit(entry.EvCounter, a, classID, "", run)
	c.commit(entry.EvCounter, k, actionID, "counter", run)
	return nil
}

// detach takes an action out of the class it is filed under.
func (c *Curator) detach(a *entry.Entry, run string) {
	k := c.St.S.Entries[a.Parent]
	a.Parent = ""
	if k == nil {
		return
	}
	k.Members = slices.DeleteFunc(k.Members, func(x string) bool { return x == a.ID })
	recount(c.St.S, k)
	k.Updated = c.stamp()
	c.commit(entry.EvFile, k, a.ID, "unfiled", run)
}

// Unfile undoes a filing or a counter-example: the action stands alone again and is not filed automatically again.
func (c *Curator) Unfile(actionID, run string) error {
	s := c.St.S
	a := s.Entries[actionID]
	if a == nil {
		return fmt.Errorf("no entry %s", actionID)
	}
	if a.Parent != "" {
		c.detach(a, run)
	}
	for _, f := range a.Flags {
		if id, ok := cutPrefix(f, flagCounterOf); ok {
			if k := s.Entries[id]; k != nil {
				k.Counters = slices.DeleteFunc(k.Counters, func(x string) bool { return x == actionID })
				recount(s, k)
				c.commit(entry.EvCounter, k, actionID, "uncounted", run)
			}
		}
	}
	a.Flags = slices.DeleteFunc(a.Flags, func(f string) bool { _, ok := cutPrefix(f, flagCounterOf); return ok })
	a.Flags = appendFlag(a.Flags, flagStandalone)
	a.Updated = c.stamp()
	c.commit(entry.EvUser, a, "", "unfile", run)
	return nil
}

// Merge folds one entry into another of the same facet: members, counter-examples and evidence move over, the
// merged one is retired with MergedInto set. A pinned or user-edited entry is never merged away. Undo with Unmerge.
func (c *Curator) Merge(fromID, intoID, why, run string) error {
	s := c.St.S
	from, into := s.Entries[fromID], s.Entries[intoID]
	if from == nil || into == nil || fromID == intoID {
		return fmt.Errorf("merge %s into %s: no such entries", fromID, intoID)
	}
	if from.Facet != into.Facet {
		return fmt.Errorf("merge %s into %s: different facets", fromID, intoID)
	}
	if userTouched(from) {
		return fmt.Errorf("merge %s: the user's own entry is not merged away", fromID)
	}
	for _, id := range from.Members {
		if m := s.Entries[id]; m != nil && m.Parent == fromID {
			m.Parent = intoID
			c.commit(entry.EvFile, m, intoID, "moved", run)
		}
		if !slices.Contains(into.Members, id) {
			into.Members = append(into.Members, id)
		}
	}
	for _, id := range from.Counters {
		if !slices.Contains(into.Counters, id) {
			into.Counters = append(into.Counters, id)
		}
	}
	if from.Grain == entry.Action {
		mergeSighting(into, from, from.First)
		into.Count = max(into.Count, len(occurrences(into)))
	}
	into.Scopes = unionScopes(into.Scopes, from.Scopes)
	for _, id := range sortedKeys(s.Entries) { // an abstract entry resting on the merged class rests on the kept one
		if x := s.Entries[id]; x.Grain == entry.Abstract && slices.Contains(x.Members, fromID) {
			x.Members = slices.DeleteFunc(x.Members, func(m string) bool { return m == fromID || m == intoID })
			x.Members = append(x.Members, intoID)
			c.commit(entry.EvMerge, x, intoID, "member merged", run)
		}
	}
	recount(s, into)
	into.Updated = c.stamp()
	from.MergedInto, from.Updated = intoID, c.stamp()
	from.Flags = appendFlag(from.Flags, flagMergedStatus+string(from.Status))
	from.Status, from.StatusAt = entry.Retired, c.stamp()
	c.commit(entry.EvMerge, from, intoID, why, run)
	c.commit(entry.EvMerge, into, fromID, "kept", run)
	return nil
}

// Unmerge undoes a merge: the entry is live again with the members it had (those still filed under the other).
func (c *Curator) Unmerge(id, run string) error {
	s := c.St.S
	e := s.Entries[id]
	if e == nil || e.MergedInto == "" {
		return fmt.Errorf("%s was not merged", id)
	}
	into := s.Entries[e.MergedInto]
	if into != nil {
		into.Members = slices.DeleteFunc(into.Members, func(x string) bool { return slices.Contains(e.Members, x) })
		into.Counters = slices.DeleteFunc(into.Counters, func(x string) bool { return slices.Contains(e.Counters, x) })
		for _, m := range e.Members {
			if x := s.Entries[m]; x != nil && x.Parent == into.ID {
				x.Parent = id
				c.commit(entry.EvFile, x, id, "moved back", run)
			}
		}
		recount(s, into)
		c.commit(entry.EvMerge, into, id, "unmerged", run)
	}
	e.MergedInto = ""
	e.Status = entry.Candidate
	for _, f := range e.Flags {
		if st, ok := cutPrefix(f, flagMergedStatus); ok {
			e.Status = entry.Status(st)
		}
	}
	e.Flags = slices.DeleteFunc(e.Flags, func(f string) bool { _, ok := cutPrefix(f, flagMergedStatus); return ok })
	e.Flags = appendFlag(e.Flags, flagNoAutoMerge)
	recount(s, e)
	e.Updated = c.stamp()
	c.commit(entry.EvMerge, e, "", "unmerged", run)
	return nil
}

// Invalidate marks an entry no longer true from day on (newer evidence says otherwise, 5.4, 5.5): ValidTo is set and
// the entry is kept, with the newer entry recorded, never deleted.
func (c *Curator) Invalidate(oldID, newerID, day, run string) error {
	s := c.St.S
	old := s.Entries[oldID]
	if old == nil {
		return fmt.Errorf("no entry %s", oldID)
	}
	if old.Pinned || old.User != nil {
		return fmt.Errorf("%s is the user's: not invalidated by the curator", oldID)
	}
	if old.ValidFrom == "" {
		old.ValidFrom = old.First
	}
	old.ValidTo = day
	old.Flags = appendFlag(old.Flags, flagSupersededBy+newerID)
	old.Updated = c.stamp()
	if n := s.Entries[newerID]; n != nil && n.ValidFrom == "" {
		n.ValidFrom = day
		c.commit(entry.EvObserve, n, "", "valid_from", run)
	}
	c.commit(entry.EvInvalid, old, newerID, "", run)
	return nil
}

// create adds a class or abstract entry made by consolidation.
func (c *Curator) create(e *entry.Entry, run string) *entry.Entry {
	s := c.St.S
	if e.Origin == "" {
		e.Origin = entry.Behavior
	}
	e.ID = EntryID(e.Facet, e.Grain, e.Text, e.Person)
	if old := s.Entries[e.ID]; old != nil {
		return old
	}
	if e.Status == "" {
		e.Status = entry.Candidate
	}
	e.Updated = c.stamp()
	s.Entries[e.ID] = e
	recount(s, e)
	c.commit(entry.EvCreate, e, "", "", run)
	return e
}

// userTouched: the user wrote, pinned, deleted or confirmed it: consolidation never overwrites it (5.0).
func userTouched(e *entry.Entry) bool {
	return e.User != nil || e.Pinned || e.Deleted || userConfirmed(e) || e.Origin == entry.Handwritten
}

// Flags consolidation sets.
const (
	flagStandalone   = "standalone"     // looked at: no class to file it under (a one-off), or the user unfiled it
	flagCounterOf    = "counter_of:"    // the class it went against
	flagMergedStatus = "merged_status:" // the status it had before it was merged (Unmerge restores it)
	flagNoAutoMerge  = "no_auto_merge"  // the user undid a merge: not merged automatically again
	flagSupersededBy = "superseded_by:" // newer evidence replaced it
	flagAutoMerged   = "auto_merged"    // a new class the hard check filed under an existing one of the same name
	flagLegacy       = "legacy:"        // migrated from the judgement layer (id of the judgement, pattern, trait or rule)
	flagImpactNone   = "impact_none:"   // the weekly review found no moment it changes (basis hash)
	flagImpactOK     = "impact_ok:"     // the weekly review found one (basis hash)
	flagRelationOff  = "relation_off:"  // a relation retired by the weekly clean-up (reason)
)

func appendFlag(fs []string, f string) []string {
	if slices.Contains(fs, f) {
		return fs
	}
	return append(fs, f)
}

func cutPrefix(s, p string) (string, bool) {
	if len(s) >= len(p) && s[:len(p)] == p {
		return s[len(p):], true
	}
	return "", false
}

// User acts on an entry (the page): edit, pin, unpin, delete, restore, right, wrong, unsure.
const (
	ActEdit    = "edit"
	ActPin     = "pin"
	ActUnpin   = "unpin"
	ActDelete  = "delete"
	ActRestore = "restore"
	ActRight   = "right"
	ActWrong   = "wrong"
	ActUnsure  = "unsure"
)

// UserAct applies the user's change to an entry. Call it with the store's lock held. A hand-written entry's text is
// the assistant's memory file: the user edits it there, not here.
func (c *Curator) UserAct(id, act, value string) error {
	s := c.St.S
	e := s.Entries[id]
	if e == nil {
		return fmt.Errorf("no entry %s", id)
	}
	now := c.stamp()
	switch act {
	case ActEdit:
		if e.Origin == entry.Handwritten {
			return fmt.Errorf("%s is the assistant's hand-written memory: edit it there", id)
		}
		e.User = &entry.UserEdit{Text: value, At: now}
	case ActPin:
		e.Pinned = true
	case ActUnpin:
		e.Pinned = false
	case ActDelete:
		e.Deleted = true
		if !tombstoned(s, e.Facet, e.Text, e.Person) {
			s.Tombstones = append(s.Tombstones, entry.Tombstone{Facet: e.Facet, Key: tombKey(e.Person, e.Text), Text: e.Text, At: now})
		}
	case ActRestore:
		e.Deleted = false
		key := tombKey(e.Person, e.Text)
		s.Tombstones = slices.DeleteFunc(s.Tombstones, func(t entry.Tombstone) bool { return t.Facet == e.Facet && t.Key == key })
	case ActRight, ActWrong, ActUnsure:
		e.Verdicts = append(e.Verdicts, entry.Verdict{Mark: act, At: now, By: "user", Note: value})
	default:
		return fmt.Errorf("unknown action %q", act)
	}
	e.Updated = now
	c.commit(entry.EvUser, e, "", act, "")
	return nil
}

// EntriesOf lists a facet's live entries (keep may narrow them), ordered as every view and exit orders them:
// count × source kinds, then last seen, then id.
func EntriesOf(s *store.State, f entry.Facet, keep func(*entry.Entry) bool) []*entry.Entry {
	var out []*entry.Entry
	for _, e := range s.Entries {
		if e.Facet == f && e.Live() && (keep == nil || keep(e)) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		wi, wj := out[i].Count*max(1, len(out[i].Kinds)), out[j].Count*max(1, len(out[j].Kinds))
		if wi != wj {
			return wi > wj
		}
		if out[i].Last != out[j].Last {
			return out[i].Last > out[j].Last
		}
		return out[i].ID < out[j].ID
	})
	return out
}
