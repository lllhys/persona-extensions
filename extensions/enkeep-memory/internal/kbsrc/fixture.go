package kbsrc

import (
	"context"
	"encoding/json"
	"errors"
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

// Fixture is a knowledge base read from a directory laid out like the hub's (synthetic data, or a read-only copy
// of a hub's data directory for an offline rerun):
//
//	owner.json                         {name, aliases, handles}
//	<kind>/<id>/meta.json              an entity's metadata
//	<kind>/<id>/summary.md             its summary (optional)
//	<kind>/<id>.json                   an entity kept as one JSON file (the hub's docs, events and meetings): the
//	                                   object is its metadata, its "summary" (else "text" / "content") its summary
//	chats/<id>/messages/<day>.md       a chat's messages of a day: "- 14:03 Name: text" lines
//
// A directory without meta.json is not an entity and is skipped. Writes change the in-memory copy only, never the
// directory, so tests can check what an update did and undo it.
type Fixture struct {
	Dir string

	mu      sync.Mutex
	meta    map[string]map[string]any // "<kind>/<id>" → metadata
	summary map[string]string
	writes  []wire.KBUpdateParams
	// profiles are the person profiles written, by person id and writer ("ext:<id>", "user").
	profiles map[string]map[string]ProfileRecord

	// NoProfiles plays a hub without person profiles (before nwverse/persona#5934): a profile write is answered
	// as if it went through and nothing is kept. Ignored is answered with every profile write, as a hub would for
	// fields it did not use.
	NoProfiles bool
	Ignored    []IgnoredField
}

// maxSummary bounds the summary taken from a single-file entity's text (runes): a long document's head is what a
// day's curation can use.
const maxSummary = 6000

// LoadFixture reads a fixture directory. It never writes to it.
func LoadFixture(dir string) (*Fixture, error) {
	f := &Fixture{Dir: dir, meta: map[string]map[string]any{}, summary: map[string]string{}}
	for _, kind := range []string{"chats", "people", "projects", "docs", "meetings", "events", "recordings"} {
		ents, err := os.ReadDir(filepath.Join(dir, kind))
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() {
				id, ok := strings.CutSuffix(e.Name(), ".json")
				if !ok || id == "" || kind == "chats" || strings.HasPrefix(e.Name(), ".") {
					continue
				}
				if err := f.loadFile(kind, id, filepath.Join(dir, kind, e.Name())); err != nil {
					return nil, err
				}
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, kind, e.Name(), "meta.json"))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
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

// loadFile reads an entity kept as one JSON file. A directory of the same id (with meta.json) wins.
func (f *Fixture) loadFile(kind, id, path string) error {
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), id, "meta.json")); err == nil {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s/%s.json: %w", kind, id, err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("%s/%s.json: %w", kind, id, err)
	}
	f.meta[kind+"/"+id] = m
	for _, field := range []string{"summary", "text", "content"} {
		if s := strings.TrimSpace(str(m[field])); s != "" {
			f.summary[kind+"/"+id] = clip(s, maxSummary)
			break
		}
	}
	return nil
}

// clip keeps the first n runes of s and says how much it left out.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + fmt.Sprintf("\n…[略去 %d 字]", len(r)-n)
}

// updatedOf is when an entity changed, from the first of the metadata fields the hub writes it in.
func updatedOf(m map[string]any) time.Time {
	for _, field := range []string{"updated", "updated_at", "modified", "start"} {
		if s, ok := m[field].(string); ok {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t
			}
		}
	}
	return time.Time{}
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
		if e.Name == "" {
			e.Name = str(m["title"])
		}
		if r, ok := m["relevance"].(float64); ok {
			e.Relevance = int(r)
		}
		if kind == "chats" {
			if ds := f.days(id); len(ds) > 0 {
				t, _ := time.Parse("2006-01-02", ds[len(ds)-1])
				e.Updated = t.Add(23 * time.Hour)
			}
		}
		if e.Updated.IsZero() {
			e.Updated = updatedOf(m)
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

// Extension is the id the fixture attributes extension writes to (the hub takes it from the connection).
const Extension = "enkeep-memory"

// Update is an extension's write, held to the rules of nwverse/persona#6110 (docs/knowledge.md "Who holds a
// field"): a field the user or another extension holds refuses the whole update with a Conflict; written fields
// become the extension's (with its evidence) unless lock is false; unlock hands fields back (unlocking pinned
// clears it); the answer is each written field's previous value.
func (f *Fixture) Update(_ context.Context, p wire.KBUpdateParams) (map[string]json.RawMessage, error) {
	return f.write(p, "ext:"+Extension)
}

// UserUpdate is the user's own edit (the app, the assistant): it writes anything and takes over what it touches.
func (f *Fixture) UserUpdate(p wire.KBUpdateParams) error {
	_, err := f.write(p, "user")
	return err
}

func (f *Fixture) write(p wire.KBUpdateParams, by string) (map[string]json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.meta[p.Kind+"/"+p.ID]
	if !ok {
		return nil, ErrNotFound
	}
	fields := Fields(p)
	prov, _ := m["provenance"].(map[string]any)
	if prov == nil {
		prov = map[string]any{}
	}
	holder := func(field string) string {
		if e, ok := prov[field].(map[string]any); ok {
			return str(e["by"])
		}
		if slices.Contains(strs(m["locked_fields"]), field) || (field == "pinned" && str(m["pinned"]) != "") {
			return "user"
		}
		return ""
	}
	if by != "user" {
		held := map[string]string{}
		for _, fl := range append(slices.Clone(fields), p.Unlock...) {
			if h := holder(fl); h != "" && h != by {
				held[fl] = h
			}
		}
		if len(held) > 0 {
			return nil, &Conflict{Holders: held}
		}
	}
	f.writes = append(f.writes, p)
	before := map[string]json.RawMessage{}
	for _, fl := range fields {
		b, _ := json.Marshal(m[fl])
		if fl == "summary" {
			b, _ = json.Marshal(f.summary[p.Kind+"/"+p.ID])
		}
		before[fl] = b
	}
	if p.Pinned != nil {
		m["pinned"] = *p.Pinned
	}
	for field, v := range map[string]string{"note": p.Note, "status": p.Status, "importance": p.Importance, "name": p.Name, "goal": p.Goal, "my_role": p.MyRole} {
		if v != "" {
			m[field] = v
		}
	}
	if p.Relevance != nil {
		m["relevance"] = float64(*p.Relevance)
	}
	if p.Tags != nil {
		m["tags"] = toAny(p.Tags)
	}
	if p.Summary != "" {
		f.summary[p.Kind+"/"+p.ID] = p.Summary
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
		m[field] = toAny(cur)
	}
	remove := func(field string, vs []string) {
		if len(vs) > 0 {
			m[field] = toAny(slices.DeleteFunc(strs(m[field]), func(s string) bool { return slices.Contains(vs, s) }))
		}
	}
	add("aliases", p.Aliases)
	add("people", p.People)
	add("chats", p.Chats)
	add("projects", p.Projects)
	remove("aliases", p.RemoveAliases)
	remove("people", p.RemovePeople)
	remove("chats", p.RemoveChats)
	remove("projects", p.RemoveProjects)
	locked := strs(m["locked_fields"])
	lock := p.Lock == nil || *p.Lock
	for _, fl := range fields {
		switch {
		case by == "user":
			delete(prov, fl) // the user takes it over
			if !slices.Contains(locked, fl) {
				locked = append(locked, fl)
			}
		case lock:
			ev := []any{}
			for _, e := range p.Evidence {
				ev = append(ev, map[string]any{"chat": e.Chat, "day": e.Day, "n": float64(e.N)})
			}
			prov[fl] = map[string]any{"by": by, "evidence": ev}
			if !slices.Contains(locked, fl) {
				locked = append(locked, fl)
			}
		}
	}
	for _, fl := range p.Unlock {
		delete(prov, fl)
		locked = slices.DeleteFunc(locked, func(x string) bool { return x == fl })
		if fl == "pinned" {
			m["pinned"] = ""
		}
	}
	m["locked_fields"] = toAny(locked)
	m["provenance"] = prov
	return before, nil
}

// Fields are the metadata fields an update writes, by kb.update parameter.
func Fields(u wire.KBUpdateParams) []string {
	var out []string
	if u.Pinned != nil {
		out = append(out, "pinned")
	}
	if u.Relevance != nil {
		out = append(out, "relevance")
	}
	for f, v := range map[string]string{"status": u.Status, "note": u.Note, "importance": u.Importance, "name": u.Name, "goal": u.Goal, "my_role": u.MyRole, "summary": u.Summary} {
		if v != "" {
			out = append(out, f)
		}
	}
	if len(u.RemovePeople) > 0 || len(u.People) > 0 {
		out = append(out, "people")
	}
	if len(u.RemoveAliases) > 0 || len(u.Aliases) > 0 {
		out = append(out, "aliases")
	}
	if len(u.RemoveChats) > 0 || len(u.Chats) > 0 {
		out = append(out, "chats")
	}
	if len(u.RemoveProjects) > 0 || len(u.Projects) > 0 {
		out = append(out, "projects")
	}
	if u.Tags != nil {
		out = append(out, "tags")
	}
	sort.Strings(out)
	return out
}

// Recent lists entities of every kind updated in [since, until), newest first; chats carry their source.
func (f *Fixture) Recent(ctx context.Context, since, until time.Time) ([]wire.KBEntry, error) {
	var out []wire.KBEntry
	for _, kind := range []string{"chats", "docs", "meetings", "events", "recordings"} {
		es, _ := f.List(ctx, kind)
		for _, e := range es {
			if e.Updated.Before(since) || (!until.IsZero() && !e.Updated.Before(until)) {
				continue
			}
			if kind == "chats" {
				f.mu.Lock()
				e.Source = str(f.meta["chats/"+e.ID]["source"])
				f.mu.Unlock()
			}
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
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

// UpdateProfile replaces the extension's profile of a person under the hub's rules: refused whole when it breaks
// one (ErrInvalidProfile), an empty one removes it; the answer is the one it replaced (null for none).
func (f *Fixture) UpdateProfile(_ context.Context, personID string, p *PersonProfile) (json.RawMessage, []IgnoredField, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.meta["people/"+personID]; !ok {
		return nil, nil, ErrNotFound
	}
	if err := CheckProfile(p); err != nil {
		return nil, nil, err
	}
	ignored := append([]IgnoredField(nil), f.Ignored...)
	if f.NoProfiles {
		return json.RawMessage("null"), ignored, nil
	}
	writer := ProfileWriterOf(Extension)
	all := f.profiles[personID]
	before := json.RawMessage("null")
	if old, ok := all[writer]; ok {
		before, _ = json.Marshal(old.Profile)
	}
	if f.profiles == nil {
		f.profiles = map[string]map[string]ProfileRecord{}
	}
	if all == nil {
		all = map[string]ProfileRecord{}
		f.profiles[personID] = all
	}
	if p.Empty() {
		delete(all, writer)
	} else {
		all[writer] = ProfileRecord{Profile: *clonePerson(p), At: time.Now().UTC()}
	}
	return before, ignored, nil
}

// ReadProfile reads the extension's profile of a person back (nil: none).
func (f *Fixture) ReadProfile(_ context.Context, personID string) (*PersonProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.meta["people/"+personID]; !ok {
		return nil, ErrNotFound
	}
	r, ok := f.profiles[personID][ProfileWriterOf(Extension)]
	if !ok {
		return nil, nil
	}
	return clonePerson(&r.Profile), nil
}

// Profiles are a person's profiles by writer, as kb.get gives them.
func (f *Fixture) Profiles(personID string) map[string]ProfileRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]ProfileRecord{}
	for w, r := range f.profiles[personID] {
		out[w] = r
	}
	return out
}
