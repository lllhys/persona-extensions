package kbsrc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
)

// Fixture is a knowledge base read from a directory laid out like the hub's (synthetic data only):
//
//	owner.json                         {name, aliases, handles}
//	<kind>/<id>/meta.json              an entity's metadata
//	<kind>/<id>/summary.md             its summary (optional)
//	chats/<id>/messages/<day>.md       a chat's messages of a day: "- 14:03 Name: text" lines
//
// Writes change the in-memory copy only, never the directory, so tests can check what an update did and undo it.
type Fixture struct {
	Dir string

	mu      sync.Mutex
	meta    map[string]map[string]any // "<kind>/<id>" → metadata
	summary map[string]string
	writes  []wire.KBUpdateParams
}

// LoadFixture reads a fixture directory.
func LoadFixture(dir string) (*Fixture, error) {
	f := &Fixture{Dir: dir, meta: map[string]map[string]any{}, summary: map[string]string{}}
	for _, kind := range []string{"chats", "people", "projects", "docs", "meetings", "events"} {
		ents, err := os.ReadDir(filepath.Join(dir, kind))
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, kind, e.Name(), "meta.json"))
			if err != nil {
				return nil, fmt.Errorf("%s/%s: %w", kind, e.Name(), err)
			}
			m := map[string]any{}
			if err := json.Unmarshal(raw, &m); err != nil {
				return nil, fmt.Errorf("%s/%s/meta.json: %w", kind, e.Name(), err)
			}
			f.meta[kind+"/"+e.Name()] = m
			if s, err := os.ReadFile(filepath.Join(dir, kind, e.Name(), "summary.md")); err == nil {
				f.summary[kind+"/"+e.Name()] = string(s)
			}
		}
	}
	return f, nil
}

func (f *Fixture) days(chat string) []string {
	ents, _ := os.ReadDir(filepath.Join(f.Dir, "chats", chat, "messages"))
	var out []string
	for _, e := range ents {
		if d, ok := strings.CutSuffix(e.Name(), ".md"); ok {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

func (f *Fixture) List(_ context.Context, kind string) ([]wire.KBEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []wire.KBEntry
	for key, m := range f.meta {
		k, id, _ := strings.Cut(key, "/")
		if k != kind {
			continue
		}
		e := wire.KBEntry{Kind: kind, ID: id, Name: str(m["name"]), Importance: str(m["importance"])}
		if r, ok := m["relevance"].(float64); ok {
			e.Relevance = int(r)
		}
		if kind == "chats" {
			if ds := f.days(id); len(ds) > 0 {
				t, _ := time.Parse("2006-01-02", ds[len(ds)-1])
				e.Updated = t.Add(23 * time.Hour)
			}
		}
		if u, ok := m["updated"].(string); ok && e.Updated.IsZero() {
			e.Updated, _ = time.Parse(time.RFC3339, u)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *Fixture) Get(_ context.Context, kind, id string) (wire.KBDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.meta[kind+"/"+id]
	if !ok {
		return wire.KBDetail{}, ErrNotFound
	}
	raw, _ := json.Marshal(m)
	return wire.KBDetail{Kind: kind, ID: id, Meta: raw, Summary: f.summary[kind+"/"+id]}, nil
}

func (f *Fixture) Day(_ context.Context, chat, day string) (wire.KBDayResult, error) {
	raw, err := os.ReadFile(filepath.Join(f.Dir, "chats", chat, "messages", day+".md"))
	if err != nil {
		return wire.KBDayResult{}, ErrNotFound
	}
	r := wire.KBDayResult{Day: day, Messages: []wire.KBDayMessage{}}
	for i, l := range strings.Split(string(raw), "\n") {
		switch {
		case strings.HasPrefix(l, "- "):
			r.Messages = append(r.Messages, wire.KBDayMessage{N: i + 1, Text: strings.TrimPrefix(l, "- ")})
		case strings.HasPrefix(l, "  ") && len(r.Messages) > 0:
			r.Messages[len(r.Messages)-1].Text += "\n" + strings.TrimSpace(l)
		}
	}
	for _, d := range f.days(chat) {
		if d < day {
			r.Prev = d
		} else if d > day && r.Next == "" {
			r.Next = d
		}
	}
	return r, nil
}

func (f *Fixture) Owner(context.Context) (wire.KBOwner, error) {
	o := wire.KBOwner{Aliases: []string{}, Handles: []wire.KBHandle{}}
	raw, err := os.ReadFile(filepath.Join(f.Dir, "owner.json"))
	if err != nil {
		return o, nil
	}
	err = json.Unmarshal(raw, &o)
	return o, err
}

// Update applies the fields the curator writes, the way the hub does (docs/knowledge.md "Corrections, locks and
// unlinking"): additions first, then removals; touched fields are locked unless lock is false.
func (f *Fixture) Update(_ context.Context, p wire.KBUpdateParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.meta[p.Kind+"/"+p.ID]
	if !ok {
		return ErrNotFound
	}
	f.writes = append(f.writes, p)
	var touched []string
	set := func(field string, v any) {
		m[field] = v
		touched = append(touched, field)
	}
	if p.Pinned != nil {
		set("pinned", *p.Pinned)
	}
	for field, v := range map[string]string{"note": p.Note, "status": p.Status, "importance": p.Importance, "name": p.Name, "goal": p.Goal, "my_role": p.MyRole} {
		if v != "" {
			set(field, v)
		}
	}
	if p.Relevance != nil {
		set("relevance", float64(*p.Relevance))
	}
	if p.Tags != nil {
		set("tags", toAny(p.Tags))
	}
	if p.Summary != "" {
		f.summary[p.Kind+"/"+p.ID] = p.Summary
		touched = append(touched, "summary")
	}
	add := func(field string, vs []string) {
		if len(vs) == 0 {
			return
		}
		cur := strs(m[field])
		for _, v := range vs {
			if !slices.Contains(cur, v) {
				cur = append(cur, v)
			}
		}
		set(field, toAny(cur))
	}
	remove := func(field string, vs []string) {
		if len(vs) == 0 {
			return
		}
		cur := slices.DeleteFunc(strs(m[field]), func(s string) bool { return slices.Contains(vs, s) })
		set(field, toAny(cur))
	}
	add("aliases", p.Aliases)
	add("people", p.People)
	add("chats", p.Chats)
	add("projects", p.Projects)
	remove("aliases", p.RemoveAliases)
	remove("people", p.RemovePeople)
	remove("chats", p.RemoveChats)
	remove("projects", p.RemoveProjects)
	if p.Lock == nil || *p.Lock {
		locked := strs(m["locked_fields"])
		for _, t := range touched {
			if !slices.Contains(locked, t) {
				locked = append(locked, t)
			}
		}
		m["locked_fields"] = toAny(locked)
	}
	return nil
}

// Writes are the updates made so far.
func (f *Fixture) Writes() []wire.KBUpdateParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.writes)
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func strs(v any) []string {
	var out []string
	if a, ok := v.([]any); ok {
		for _, x := range a {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func toAny(ss []string) []any {
	out := make([]any, 0, len(ss))
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}
