package graph

import (
	"regexp"
	"slices"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
)

// The relation types are an open table (design 5.4): every user has the common types; packs by situation start on
// by the first-run answer (work, life, or both when unanswered); a type the model proposes joins the user's table
// once it has come up twice. Types that say nothing ("cites", "attended") never join.

// Pack names.
const (
	PackCommon = "common"
	PackWork   = "work"
	PackLife   = "life"
)

// packOf says which pack each built-in predicate is in. The common types are belongs to (member_of), responsible for
// (owns), depends on, decides, works with (collaborates_with) and what a term means (means); the work pack adds
// reports to and mentors; the life pack adds serves (a service provider) and same circle. The other built-ins
// (blocked_by, waiting_on, decided, about, committed, assigned, allocated) describe work in progress and stay common.
var packOf = map[string]string{
	"reports_to": PackWork, "mentors": PackWork,
	"serves": PackLife, "same_circle": PackLife,
}

// extra built-in predicates of v4: the common "means" and the packs' own.
var extra = []Predicate{
	{schema.Predicate{Name: "means", Meaning: "the term or code name means (attrs.meaning)", Subject: []string{"topic", "resource", "system", "project"}, Object: []string{"topic", "system", "project", "resource"}}, false},
	{schema.Predicate{Name: "mentors", Meaning: "mentors, brings up", Subject: []string{"person"}, Object: []string{"person"}}, false},
	{schema.Predicate{Name: "serves", Meaning: "provides a service to (attrs.service: which)", Subject: []string{"person", "org"}, Object: []string{"person", "org"}}, false},
	{schema.Predicate{Name: "same_circle", Meaning: "is in the same circle of friends or community as (attrs.circle)", Subject: []string{"person"}, Object: []string{"person"}}, false},
}

// Packs are the packs a first-run answer starts with: "work" → common + work, "life" → common + life, anything else
// (not answered, "both") → all.
func Packs(use string) []string {
	switch use {
	case entry.Work:
		return []string{PackCommon, PackWork}
	case entry.Life:
		return []string{PackCommon, PackLife}
	}
	return []string{PackCommon, PackWork, PackLife}
}

// Uninformative types are never taken from the model: what references, mentions, attended or took part in what
// (needs v2: "引用了", "参加了").
var uninformative = regexp.MustCompile(`(?i)^(cites?|cited|references?|referenced|refers_to|mentions?|mentioned|links?_to|linked|` +
	`attends?|attended|participates?_in|participated|joined|took_part|in_meeting|引用|引用了|提到|提到了|参加|参加了|参与|参与了)$`)

// Uninformative says whether a proposed type is one that is never kept.
func Uninformative(name string) bool { return uninformative.MatchString(strings.TrimSpace(name)) }

// LearnAfter is how many times a type the model proposed must come up before it joins the user's table.
const LearnAfter = 2

// learnedEnds are the ends a learned type may join: any entity but a chat, a meeting or a document (a session is never
// an entity of a relation).
var learnedEnds = []string{"person", "org", "project", "system", "repo", "resource", "topic", "task", "decision"}

// TypeName is the canonical form of a proposed type: lower case, words joined by "_".
func TypeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.Join(strings.FieldsFunc(name, func(r rune) bool { return r == ' ' || r == '-' || r == '/' }), "_")
	return name
}

// WithPacks narrows a registry to the packs given (the user's custom predicates stay) and adds the predicates the
// user's table has learned (State.Types[world] with Origin "model" or "user" and seen enough).
func (r *Registry) WithPacks(packs []string, learned []entry.TypeDef) *Registry {
	for _, p := range extra {
		if _, ok := r.Predicates[p.Name]; !ok {
			r.Predicates[p.Name] = p
		}
	}
	for name := range r.Predicates {
		if pack, ok := packOf[name]; ok && !slices.Contains(packs, pack) {
			delete(r.Predicates, name)
		}
	}
	for _, t := range learned {
		if t.Origin == "seed" {
			continue
		}
		if t.Origin == "model" && t.Seen < LearnAfter {
			continue
		}
		name := TypeName(t.Name)
		if t.Renamed != "" || name == "" || Uninformative(name) {
			continue
		}
		if _, ok := r.Predicates[name]; !ok {
			r.Predicates[name] = Learned(name)
		}
	}
	return r
}

// Seeds are the type table's starting rows for the packs: every predicate of the registry, origin seed.
func (r *Registry) Seeds() []entry.TypeDef {
	var out []entry.TypeDef
	for _, p := range r.List() {
		out = append(out, entry.TypeDef{Name: p.Name, Origin: "seed"})
	}
	return out
}

// Propose counts one sighting of a type the model proposed into the user's table and says whether it is in force
// now (seen LearnAfter times). An uninformative one is never counted. again: the caller already counted this type
// for the same unit and day (one unit-day counts once).
func Propose(table []entry.TypeDef, name string, again bool) ([]entry.TypeDef, bool) {
	name = TypeName(name)
	if name == "" || Uninformative(name) {
		return table, false
	}
	for i := range table {
		if TypeName(table[i].Name) == name {
			if !again {
				table[i].Seen++
			}
			return table, table[i].Origin != "model" || table[i].Seen >= LearnAfter
		}
	}
	return append(table, entry.TypeDef{Name: name, Origin: "model", Seen: 1}), LearnAfter <= 1
}

// sessionLike: a conversation's or session's id is not an entity ("oc_…", "persona-…", a long hex or uuid, a chat
// ref).
var sessionLike = regexp.MustCompile(`(?i)^(chats|docs|meetings|events|recordings|agents)/|^(oc|ou|om|on)_[0-9a-z]{6,}$|` +
	`^(persona|lark|mail|github|[a-z]*wechat|gh)-[\w@.\-]{4,}$|^[0-9a-f]{12,}$|^[0-9a-f]{8}-[0-9a-f]{4}-`)

// SessionLike says whether a name is a session or conversation id rather than an entity.
func SessionLike(name string) bool { return sessionLike.MatchString(strings.TrimSpace(name)) }

// Learned is the predicate a learned type is given in the registry.
func Learned(name string) Predicate {
	return Predicate{Predicate: schema.Predicate{Name: TypeName(name), Meaning: "learned from the user's data", Subject: learnedEnds, Object: learnedEnds}}
}
