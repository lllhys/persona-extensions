package curate

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// The weekly merge of classes (design 5.0 step 3, 5.2): classes are opened one action at a time, so one kind of
// situation ends up split over classes worded differently, or opened around one event. Once a week each facet's
// classes are put to the model in batches (all of a facet's when they fit, else grouped by type; a person's card
// classes only with that person's), and what it groups is merged: members, counter-examples, counts, active days and
// scopes come together, every merge an event and undoable (Unmerge).

//go:embed merge.md
var mergePrompt string

//go:embed merge.schema.json
var mergeSchema []byte

// mergeRule is what may be one class in each facet. ③④ are kinds of situation with the user's default way in
// them; ① is one rule for the assistant at an occasion; ② one machine, tool or service and one way with it or one
// pitfall; ⑥ and ⑤′ one aspect of a person saying one thing.
var mergeRule = map[entry.Facet]string{
	entry.Judgement:   "one kind of situation (a recurring kind of fork) and the user's default choice in it; choices on different parts of one kind of situation that point the same way are one class",
	entry.Workflow:    "one kind of task or occasion and the user's default way of handling it; steps of one way of handling it are one class",
	entry.Interaction: "one occasion of working with the assistant and one rule for the assistant there; different rules stay apart even at the same occasion",
	entry.Tools:       "one machine, tool, runtime, model or service and one way to use it or one pitfall with its fix; different objects or different points stay apart",
	entry.Persona:     "one aspect of the user (role, a responsibility, where they are, their schedule, a goal, a value, a preference) saying the same thing; different aspects stay apart",
	entry.People:      "one aspect of the same person (role, a responsibility, how they work, a commitment) saying the same thing; different aspects stay apart",
}

const (
	mergeBatch     = 80 // classes per call
	mergeMaxTokens = 6000
)

type (
	mergeClass struct {
		ID        string `json:"id"`
		Type      string `json:"type,omitempty"`
		Situation string `json:"situation,omitempty"`
		Text      string `json:"text"`
		Count     int    `json:"count"`
		Person    string `json:"person,omitempty"`
	}
	mergeInput struct {
		Facet   string       `json:"facet"`
		Meaning string       `json:"facet_meaning"`
		Lang    string       `json:"language"`
		Rule    string       `json:"rule"`
		Classes []mergeClass `json:"classes"`
	}
	mergeKind struct {
		Situation   string   `json:"situation"`
		Text        string   `json:"text"`
		Classes     []string `json:"classes"`
		Conflicting []string `json:"conflicting,omitempty"`
		Reason      string   `json:"reason"`
	}
	mergeOutput struct {
		Kinds []mergeKind `json:"kinds"`
	}
)

// mergeBatches cuts a facet's live classes into the batches the model sees: one batch when they fit; otherwise
// grouped by type (people: by person), whole groups packed into batches, a group larger than a batch cut in turn.
func mergeBatches(s *store.State, f entry.Facet) [][]*entry.Entry {
	var all []*entry.Entry
	for _, e := range EntriesOf(s, f, func(e *entry.Entry) bool {
		return e.Grain == entry.Class && e.Predicate == "" && !slices.Contains(e.Flags, flagNoAutoMerge)
	}) {
		all = append(all, e)
	}
	if len(all) < 2 {
		return nil
	}
	key := func(e *entry.Entry) string {
		if f == entry.People {
			return e.Person
		}
		return norm.Q(resolveType(s, f, e.Type))
	}
	if f != entry.People && len(all) <= mergeBatch {
		return [][]*entry.Entry{all}
	}
	groups := map[string][]*entry.Entry{}
	for _, e := range all {
		groups[key(e)] = append(groups[key(e)], e)
	}
	var keys []string
	for k, g := range groups {
		if len(g) >= 2 || f != entry.People {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out [][]*entry.Entry
	var cur []*entry.Entry
	for _, k := range keys {
		g := groups[k]
		for len(g) > mergeBatch {
			out = append(out, g[:mergeBatch])
			g = g[mergeBatch:]
		}
		if len(cur)+len(g) > mergeBatch {
			out = append(out, cur)
			cur = nil
		}
		cur = append(cur, g...)
	}
	if len(cur) >= 2 {
		out = append(out, cur)
	}
	return out
}

// mergeClasses asks for the groups of classes that are one kind of situation and merges them.
func (c *Curator) mergeClasses(ctx context.Context, m model.Model, run *store.Run) error {
	s := c.St.S
	for _, f := range entry.Facets {
		if f == entry.World {
			continue // ⑤'s classes are relations: merged by their ends, not by wording
		}
		for _, batch := range mergeBatches(s, f) {
			if err := ctx.Err(); err != nil {
				return err
			}
			in := mergeInput{Facet: string(f), Meaning: facetMeaning[f], Lang: c.language(), Rule: mergeRule[f]}
			local := map[string]*entry.Entry{}
			for i, e := range batch {
				id := fmt.Sprintf("c%d", i+1)
				local[id] = e
				in.Classes = append(in.Classes, mergeClass{ID: id, Type: e.Type, Situation: e.Situation, Text: e.Shown(), Count: e.Count, Person: e.Person})
			}
			body, _ := json.Marshal(in)
			resp, err := m.Complete(ctx, model.Request{Step: "merge", Tier: model.Medium, System: mergePrompt, User: string(body),
				MaxTokens: mergeMaxTokens, Schema: mergeSchema})
			if errors.Is(err, model.ErrRefused) {
				run.Counts["merge_refused"]++
				continue
			}
			if err != nil {
				return err
			}
			run.Counts["merge_calls"]++
			var out mergeOutput
			if json.Unmarshal([]byte(extractJSON(resp.Text)), &out) != nil {
				continue
			}
			c.applyMerges(local, out.Kinds, run)
		}
	}
	return nil
}

// applyMerges checks and applies the model's kinds: ids it was shown, each class in one kind only, one person per
// kind, the user's own class kept rather than merged away. The agreeing classes of a kind are merged into the most
// seen (or the user's); the kept class takes the kind's situation and text unless the user wrote it; a kind of one
// class only widens that class's situation. Conflicting classes are left apart.
func (c *Curator) applyMerges(local map[string]*entry.Entry, kinds []mergeKind, run *store.Run) {
	used := map[string]bool{}
	for _, k := range kinds {
		var members []*entry.Entry
		for _, id := range k.Classes {
			if e := local[id]; e != nil && !used[id] && e.Live() {
				members = append(members, e)
				used[id] = true
			}
		}
		for _, id := range k.Conflicting {
			used[id] = true
		}
		if len(members) == 0 {
			continue
		}
		if len(members) > 1 && waysFacet(members[0].Facet) && listOfPoints(k.Text) {
			// The kind's sentence strings alternatives together ("A, or B, or C"): what the classes share is not one
			// default way, and the merged class would say nothing the assistant can act on. They stay apart. (A
			// machine's or a person's classes may list several points about the same object.)
			run.Counts["merges_refused_list"]++
			continue
		}
		keep := members[0]
		for _, e := range members {
			switch {
			case userTouched(e) && !userTouched(keep):
				keep = e
			case userTouched(e) == userTouched(keep) && (e.Count > keep.Count || e.Count == keep.Count && e.ID < keep.ID):
				keep = e
			}
		}
		why := "same kind of situation: " + clip(strings.TrimSpace(k.Reason), 100)
		merged := 0
		for _, e := range members {
			if e == keep || e.Person != keep.Person || userTouched(e) {
				continue
			}
			if err := c.Merge(e.ID, keep.ID, why, run.ID); err == nil {
				merged++
			}
		}
		if merged > 0 {
			run.Counts["classes_merged"] += merged
			run.Counts["class_groups"]++
		}
		if userTouched(keep) {
			continue
		}
		changed := false
		if sit := strings.TrimSpace(k.Situation); sit != "" && sit != keep.Situation {
			keep.Situation, changed = sit, true
		}
		if t := strings.TrimSpace(k.Text); merged > 0 && t != "" && !tombstoned(c.St.S, keep.Facet, t, keep.Person) {
			keep.Text, changed = t, true
		}
		if changed {
			keep.Updated = c.stamp()
			c.commit(entry.EvMerge, keep, "", "kind of situation", run.ID)
			run.Counts["classes_generalised"]++
		}
	}
}

var orWord = regexp.MustCompile(`(?i)\bor\b`)

// listOfPoints: a sentence that joins two or more alternatives with "或" / "or" (three ways or more): a list of
// unrelated points rather than one default way.
func listOfPoints(text string) bool {
	return strings.Count(text, "或") >= 2 || len(orWord.FindAllString(text, -1)) >= 2
}

// waysFacet: a facet whose classes are a kind of situation and one way in it (①③④⑥), not points about an object.
func waysFacet(f entry.Facet) bool {
	return f == entry.Interaction || f == entry.Judgement || f == entry.Workflow || f == entry.Persona
}
