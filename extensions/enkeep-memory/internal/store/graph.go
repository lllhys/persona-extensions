package store

import "sort"

// Graph is the entity and relation graph: nodes that point at knowledge-base entities where there is one, and
// assertions (typed, dated, cited relations) between them. Nothing is overwritten: a change is a new assertion
// that supersedes the old one, a removal is a status, a merge is a record that can be undone.
type Graph struct {
	Nodes      map[string]*Node      `json:"nodes"`
	Assertions map[string]*Assertion `json:"assertions"`
	Merges     []*MergeRec           `json:"merges"`
	NextNode   int                   `json:"next_node"`
}

// Node is an entity.
type Node struct {
	ID         string            `json:"id"` // n-<type>-<n>, or "me"
	Type       string            `json:"type"`
	Name       string            `json:"name"`
	Aliases    []Alias           `json:"aliases,omitempty"`
	Refs       []string          `json:"refs,omitempty"` // "<kind>/<id>" of knowledge-base entities it is
	MergedInto string            `json:"merged_into,omitempty"`
	Status     string            `json:"status"` // candidate | active | not_entity
	Created    string            `json:"created"`
	LastSeen   string            `json:"last_seen"`
	Attrs      map[string]string `json:"attrs,omitempty"` // task: due, status (open | done | dropped)
}

// Alias is a name a node went by, with the line that used it that way.
type Alias struct {
	Name     string    `json:"name"`
	Evidence *Evidence `json:"evidence,omitempty"`
	From     string    `json:"from,omitempty"`
}

// Assertion is a relation.
type Assertion struct {
	ID         string            `json:"id"` // sha1(s, p, o, first evidence anchor)
	S          string            `json:"s"`
	P          string            `json:"p"`
	O          string            `json:"o"`
	Attrs      map[string]string `json:"attrs,omitempty"`
	ValidFrom  string            `json:"valid_from"`
	ValidTo    string            `json:"valid_to,omitempty"`
	Observed   string            `json:"observed"`
	Status     string            `json:"status"` // candidate | active | stale | superseded | retracted | rejected
	Confidence string            `json:"confidence"`
	Source     string            `json:"source"` // extract | user | kb_import | rule
	Supersedes string            `json:"supersedes,omitempty"`
	Evidence   []Evidence        `json:"evidence"`
	Flags      []string          `json:"flags,omitempty"`
	Prompt     string            `json:"prompt,omitempty"`
	Ruleset    string            `json:"ruleset,omitempty"`
}

// MergeRec is a merge of one node into another; undoing it makes the node stand alone again. Assertions and
// aliases keep pointing at the node they were made about, which is what makes the undo exact.
type MergeRec struct {
	From     string `json:"from"`
	Into     string `json:"into"`
	At       string `json:"at"`
	Proposal string `json:"proposal,omitempty"`
	Undone   string `json:"undone,omitempty"`
}

func (g *Graph) init() {
	if g.Nodes == nil {
		g.Nodes = map[string]*Node{}
	}
	if g.Assertions == nil {
		g.Assertions = map[string]*Assertion{}
	}
}

// Resolve follows merges to the node that stands for id now.
func (g *Graph) Resolve(id string) string {
	for i := 0; i < 32; i++ {
		n := g.Nodes[id]
		if n == nil || n.MergedInto == "" {
			return id
		}
		id = n.MergedInto
	}
	return id
}

// About returns the assertions where id (or a node merged into it) is subject or object, in order.
func (g *Graph) About(id string, keep func(*Assertion) bool) []*Assertion {
	id = g.Resolve(id)
	var out []*Assertion
	for _, a := range g.Assertions {
		if (g.Resolve(a.S) == id || g.Resolve(a.O) == id) && (keep == nil || keep(a)) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ValidFrom != out[j].ValidFrom {
			return out[i].ValidFrom > out[j].ValidFrom
		}
		return out[i].ID < out[j].ID
	})
	return out
}
