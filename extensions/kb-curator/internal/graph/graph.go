// Package graph builds the entity and relation graph out of verified daily assertions: it resolves names to
// nodes (deterministically first), keeps every relation as a dated, cited assertion, supersedes instead of
// overwriting, and ages what nobody mentions any more.
package graph

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/store"
)

// Types are the built-in node types; the configuration may add more.
var Types = []string{"person", "org", "project", "system", "repo", "doc", "meeting", "chat", "decision", "task", "resource", "topic"}

// Predicates are the built-in relations. Functional ones have one current value per subject: a new value
// supersedes the old.
var Predicates = []Predicate{
	{schema.Predicate{Name: "owns", Meaning: "is responsible for", Subject: []string{"person", "org"}, Object: []string{"project", "system", "repo", "task", "doc"}}, false},
	{schema.Predicate{Name: "participates", Meaning: "takes part in", Subject: []string{"person", "org"}, Object: []string{"project", "meeting", "decision"}}, false},
	{schema.Predicate{Name: "member_of", Meaning: "belongs to", Subject: []string{"person", "system", "repo", "project"}, Object: []string{"org", "project"}}, false},
	{schema.Predicate{Name: "reports_to", Meaning: "reports to", Subject: []string{"person"}, Object: []string{"person"}}, true},
	{schema.Predicate{Name: "depends_on", Meaning: "depends on", Subject: []string{"project", "system"}, Object: []string{"system", "resource", "project"}}, false},
	{schema.Predicate{Name: "blocked_by", Meaning: "is held up by", Subject: []string{"project", "task"}, Object: []string{"person", "task", "resource", "system", "decision"}}, true},
	{schema.Predicate{Name: "waiting_on", Meaning: "waits for a reply or action from", Subject: []string{"task", "project"}, Object: []string{"person", "org"}}, true},
	{schema.Predicate{Name: "decided", Meaning: "made the decision", Subject: []string{"person", "meeting", "org"}, Object: []string{"decision"}}, false},
	{schema.Predicate{Name: "about", Meaning: "concerns", Subject: []string{"decision", "task", "doc", "meeting"}, Object: []string{"project", "system", "repo", "org", "resource", "topic"}}, false},
	{schema.Predicate{Name: "committed", Meaning: "promised to do (attrs.to: for whom, attrs.due)", Subject: []string{"person"}, Object: []string{"task"}}, false},
	{schema.Predicate{Name: "assigned", Meaning: "was given the task (attrs.by: by whom, attrs.due)", Subject: []string{"person"}, Object: []string{"task"}}, false},
	{schema.Predicate{Name: "references", Meaning: "points at", Subject: []string{"doc", "chat", "repo", "meeting"}, Object: Types}, false},
	{schema.Predicate{Name: "allocated", Meaning: "is allocated to (attrs.quantity)", Subject: []string{"resource"}, Object: []string{"project", "org", "person"}}, false},
}

// Predicate is a relation type.
type Predicate struct {
	schema.Predicate
	Functional bool
}

// Registry is the types and relations in force: built-in plus the user's.
type Registry struct {
	Types      []string
	Predicates map[string]Predicate
}

// NewRegistry adds custom types ("name" or "name: meaning") and predicates
// ("name: subj1|subj2 -> obj1|obj2: meaning") to the built-in ones. Malformed entries are returned as problems.
func NewRegistry(customTypes, customPredicates []string) (*Registry, []string) {
	r := &Registry{Types: slices.Clone(Types), Predicates: map[string]Predicate{}}
	for _, p := range Predicates {
		r.Predicates[p.Name] = p
	}
	var problems []string
	for _, t := range customTypes {
		name, _, _ := strings.Cut(t, ":")
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || strings.ContainsAny(name, " |>") {
			problems = append(problems, "类型写法不对："+t)
			continue
		}
		if !slices.Contains(r.Types, name) {
			r.Types = append(r.Types, name)
		}
	}
	for _, p := range customPredicates {
		parts := strings.SplitN(p, ":", 3)
		if len(parts) < 2 {
			problems = append(problems, "关系写法不对："+p)
			continue
		}
		name := strings.ToLower(strings.TrimSpace(parts[0]))
		subj, obj, ok := strings.Cut(parts[1], "->")
		if name == "" || !ok {
			problems = append(problems, "关系写法不对："+p)
			continue
		}
		pr := Predicate{Predicate: schema.Predicate{Name: name, Subject: splitTypes(subj), Object: splitTypes(obj)}}
		if len(parts) == 3 {
			pr.Meaning = strings.TrimSpace(parts[2])
		}
		r.Predicates[name] = pr
	}
	return r, problems
}

func splitTypes(s string) []string {
	var out []string
	for _, t := range strings.Split(s, "|") {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// List is the predicates for the prompt, in a stable order.
func (r *Registry) List() []schema.Predicate {
	var out []schema.Predicate
	for _, p := range r.Predicates {
		out = append(out, p.Predicate)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Builder changes a graph.
type Builder struct {
	G     *store.Graph
	Reg   *Registry
	Owner []string
	Today string
}

// Me is the owner's node.
const Me = "me"

// generic names never become aliases (a chat name or a word like "周会" is not a project's name).
var generic = []string{"周会", "例会", "这个需求", "这个项目", "项目", "需求", "群", "大家", "所有人", "全体成员", "team", "the team", "project", "this", "meeting", "weekly", "standup"}

// EnsureMe makes the owner's node.
func (b *Builder) EnsureMe() {
	if b.G.Nodes[Me] == nil {
		b.G.Nodes[Me] = &store.Node{ID: Me, Type: "person", Name: "我", Status: "active", Created: b.Today, LastSeen: b.Today}
	}
	n := b.G.Nodes[Me]
	for _, o := range b.Owner {
		if !hasAlias(n, o) {
			n.Aliases = append(n.Aliases, store.Alias{Name: o, From: "owner"})
		}
	}
}

// Seed adds a knowledge-base entity as a node (or finds the one that already points at it). Nodes are made only for
// entities that take part in a relation, so a group's ten thousand silent members never become nodes.
func (b *Builder) Seed(typ, ref, name string, aliases []string) string {
	for _, n := range b.G.Nodes {
		if slices.Contains(n.Refs, ref) {
			return b.G.Resolve(n.ID)
		}
	}
	if id := b.find(name, typ); id != "" {
		n := b.G.Nodes[id]
		n.Refs = append(n.Refs, ref)
		return id
	}
	id := b.newNode(typ, name, "active")
	n := b.G.Nodes[id]
	n.Refs = []string{ref}
	for _, a := range aliases {
		if !isGeneric(a) && !hasAlias(n, a) {
			n.Aliases = append(n.Aliases, store.Alias{Name: a, From: "kb"})
		}
	}
	return id
}

func (b *Builder) newNode(typ, name, status string) string {
	b.G.NextNode++
	id := fmt.Sprintf("n-%s-%d", typ, b.G.NextNode)
	b.G.Nodes[id] = &store.Node{ID: id, Type: typ, Name: name, Status: status, Created: b.Today, LastSeen: b.Today}
	return id
}

// find resolves a name to a node: the owner's names first, then exact names and aliases (canonical form) of the
// same type (or any type when typ is empty). Ambiguous names resolve to nothing.
func (b *Builder) find(name, typ string) string {
	q := norm.Q(name)
	if q == "" {
		return ""
	}
	if name == norm.Me || slices.ContainsFunc(b.Owner, func(o string) bool { return norm.Q(o) == q }) {
		return Me
	}
	var hits []string
	for _, n := range b.G.Nodes {
		if n.MergedInto != "" || n.Status == "not_entity" || (typ != "" && n.Type != typ) {
			continue
		}
		if norm.Q(n.Name) == q || slices.ContainsFunc(n.Aliases, func(a store.Alias) bool { return norm.Q(a.Name) == q }) {
			hits = append(hits, n.ID)
		}
	}
	sort.Strings(hits)
	if len(hits) == 1 {
		return hits[0]
	}
	return ""
}

// Resolve turns a mention into a node, making a candidate node when nothing matches.
func (b *Builder) Resolve(surface, typ, knownID string, ev *store.Evidence) (string, bool) {
	if knownID != "" {
		if n := b.G.Nodes[knownID]; n != nil {
			return b.G.Resolve(knownID), true
		}
		for _, n := range b.G.Nodes {
			if slices.Contains(n.Refs, knownID) {
				return b.G.Resolve(n.ID), true
			}
		}
	}
	if id := b.find(surface, typ); id != "" {
		return id, true
	}
	if id := b.find(surface, ""); id != "" {
		return id, true
	}
	if !slices.Contains(b.Reg.Types, typ) || isGeneric(surface) {
		return "", false
	}
	id := b.newNode(typ, surface, "candidate")
	b.G.Nodes[id].Aliases = []store.Alias{{Name: surface, Evidence: ev, From: "extract"}}
	return id, true
}

// TypeOf is a node's type.
func (b *Builder) TypeOf(id string) string {
	if n := b.G.Nodes[b.G.Resolve(id)]; n != nil {
		return n.Type
	}
	return ""
}

// Names are the names a node goes by.
func (b *Builder) Names(id string) []string {
	n := b.G.Nodes[b.G.Resolve(id)]
	if n == nil {
		return nil
	}
	out := []string{n.Name}
	for _, a := range n.Aliases {
		out = append(out, a.Name)
	}
	if n.ID == Me {
		out = append(out, b.Owner...)
	}
	return out
}

// Add records a verified assertion and returns its id and what happened: "new", "duplicate" (same relation,
// more evidence), "superseded" (it replaced an older value), "history" (older than the current value, kept as
// history), or "conflict" (a different value on the same day: both wait for a person).
func (b *Builder) Add(s, p, o string, attrs map[string]string, when string, ev []store.Evidence, conf string, flags []string, prompt string) (string, string) {
	s, o = b.G.Resolve(s), b.G.Resolve(o)
	day := ev[0].Day
	valid := when
	if valid == "" {
		valid = day
	}
	id := assertionID(s, p, o)
	if a := b.G.Assertions[id]; a != nil {
		for _, e := range ev {
			if !slices.ContainsFunc(a.Evidence, func(x store.Evidence) bool { return x.Chat == e.Chat && x.Day == e.Day && x.N == e.N }) {
				a.Evidence = append(a.Evidence, e)
			}
		}
		a.Observed = day
		if a.Status == "stale" {
			a.Status = "active"
		}
		b.promote(a)
		return id, "duplicate"
	}
	a := &store.Assertion{ID: id, S: s, P: p, O: o, Attrs: attrs, ValidFrom: valid, Observed: day, Confidence: conf,
		Source: "extract", Evidence: ev, Flags: flags, Prompt: prompt, Ruleset: schema.Ruleset, Status: "candidate"}
	b.promote(a)
	outcome := "new"
	if pr, ok := b.Reg.Predicates[p]; ok && pr.Functional {
		for _, old := range b.G.Assertions {
			if old.S != s || old.P != p || old.O == o || (old.Status != "active" && old.Status != "candidate") {
				continue
			}
			switch {
			case old.ValidFrom < valid:
				old.Status, old.ValidTo = "superseded", valid
				a.Supersedes = old.ID
				outcome = "superseded"
			case old.ValidFrom > valid:
				a.Status, a.ValidTo = "superseded", old.ValidFrom
				outcome = "history"
			default:
				old.Status, a.Status = "candidate", "candidate"
				outcome = "conflict"
			}
		}
	}
	b.G.Assertions[id] = a
	for _, nid := range []string{s, o} {
		if n := b.G.Nodes[nid]; n != nil {
			n.LastSeen = day
			if n.Status == "candidate" && a.Status == "active" {
				n.Status = "active"
			}
		}
	}
	return id, outcome
}

// promote: candidate → active when the evidence is plain (high confidence, no flags) or comes from two independent
// sources (different chats, or different people).
func (b *Builder) promote(a *store.Assertion) {
	if a.Status != "candidate" {
		return
	}
	if (a.Confidence == "high" && len(a.Flags) == 0) || independent(a.Evidence) >= 2 {
		a.Status = "active"
	}
}

func independent(ev []store.Evidence) int {
	seen := map[string]bool{}
	for _, e := range ev {
		seen[e.Chat+"\x00"+e.Speaker] = true
	}
	return len(seen)
}

// Age marks relations nobody mentioned for `days` as stale (functional ones stay: they hold until replaced), and
// closes tasks whose due date passed long ago only as a flag; nothing is deleted.
func (b *Builder) Age(today string, days int, before func(day string, days int) string) (staled int) {
	cut := before(today, days)
	for _, a := range b.G.Assertions {
		if a.Status != "active" {
			continue
		}
		if pr, ok := b.Reg.Predicates[a.P]; ok && pr.Functional {
			continue
		}
		if a.Observed < cut {
			a.Status = "stale"
			staled++
		}
	}
	return staled
}

// Merge records that from is the same as into. Assertions keep their subject; queries follow MergedInto.
func (b *Builder) Merge(from, into, proposal string) error {
	from, into = b.G.Resolve(from), b.G.Resolve(into)
	if from == into || b.G.Nodes[from] == nil || b.G.Nodes[into] == nil {
		return fmt.Errorf("cannot merge %s into %s", from, into)
	}
	if from == Me {
		from, into = into, from
	}
	b.G.Nodes[from].MergedInto = into
	b.G.Merges = append(b.G.Merges, &store.MergeRec{From: from, Into: into, At: b.Today, Proposal: proposal})
	return nil
}

// Unmerge undoes the last merge of from.
func (b *Builder) Unmerge(from string) error {
	for i := len(b.G.Merges) - 1; i >= 0; i-- {
		m := b.G.Merges[i]
		if m.From == from && m.Undone == "" {
			m.Undone = b.Today
			b.G.Nodes[from].MergedInto = ""
			return nil
		}
	}
	return fmt.Errorf("%s was not merged", from)
}

// SameNameCandidates are pairs of nodes of one type whose names are the same in canonical form but were not
// merged (two knowledge-base people with one name, a candidate node beside a seeded one): merge proposals.
func (b *Builder) SameNameCandidates() [][2]string {
	by := map[string][]string{}
	for _, n := range b.G.Nodes {
		if n.MergedInto != "" || n.Status == "not_entity" || n.ID == Me {
			continue
		}
		by[n.Type+"\x00"+norm.Q(n.Name)] = append(by[n.Type+"\x00"+norm.Q(n.Name)], n.ID)
	}
	var out [][2]string
	for _, ids := range by {
		sort.Strings(ids)
		for i := 1; i < len(ids); i++ {
			out = append(out, [2]string{ids[i], ids[0]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func assertionID(s, p, o string) string {
	h := sha1.Sum([]byte(s + "\x00" + p + "\x00" + o))
	return "a-" + hex.EncodeToString(h[:6])
}

func hasAlias(n *store.Node, a string) bool {
	return norm.Q(n.Name) == norm.Q(a) || slices.ContainsFunc(n.Aliases, func(x store.Alias) bool { return norm.Q(x.Name) == norm.Q(a) })
}

func isGeneric(s string) bool {
	q := norm.Q(s)
	if len([]rune(q)) < 2 {
		return true
	}
	return slices.ContainsFunc(generic, func(g string) bool { return norm.Q(g) == q })
}
