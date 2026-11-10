// Package templates holds the starter templates of a space. Each template is an embedded JSON bundle. Apply creates a
// space from one through the public kernel operations only (PutSpace, PutType, PutEntity, Derive), so a template has
// no privileged path and can never hold anything the kernel would refuse.
package templates

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/spaces/internal/kernel"
)

//go:embed *.json
var bundles embed.FS

// order is the fixed order of the templates; the bundle of a key is the file key + ".json".
var order = []string{"project", "reading", "inventory", "blank"}

// all are the decoded templates. A bundle that does not load is a programming error, caught by the tests.
var all = mustLoad(bundles, order)

// Template is a starter: its types and, optionally, its sample records. Callers must not change a template; the
// slices and maps are shared.
type Template struct {
	Key     string        `json:"key"`
	Name    string        `json:"name"`
	Icon    string        `json:"icon"`
	About   string        `json:"about"`
	Types   []kernel.Type `json:"types"`
	Samples []Sample      `json:"samples,omitempty"`
}

// Sample is one example record. Ref is its local name: a relation value "@ref" names an earlier sample. A date value
// "+30d" or "-20d" is resolved against the time Apply is given. From creates the record with Derive from an earlier
// sample, and then the rest of the sample is applied.
type Sample struct {
	Ref    string         `json:"ref"`
	Type   string         `json:"type"`
	Title  string         `json:"title"`
	Status string         `json:"status,omitempty"`
	Values map[string]any `json:"values,omitempty"`
	From   string         `json:"from,omitempty"`
}

// All returns the templates in their fixed order: project, reading, inventory, blank.
func All() []Template { return slices.Clone(all) }

// Get returns the template with the key.
func Get(key string) (Template, bool) {
	i := slices.IndexFunc(all, func(t Template) bool { return t.Key == key })
	if i < 0 {
		return Template{}, false
	}
	return all[i], true
}

// Apply creates a space from the template with the key, named name (the template's name when empty), with its sample
// records when samples is set. The base changes only when the whole space is created; a failure leaves it as it was.
func Apply(b *kernel.Base, key, name string, samples bool, now time.Time) (*kernel.Space, error) {
	t, ok := Get(key)
	if !ok {
		return nil, fmt.Errorf("没有模板「%s」", key)
	}
	return apply(b, t, name, samples, now)
}

// apply works on a copy of the base, which replaces b only when every step succeeded.
func apply(b *kernel.Base, t Template, name string, samples bool, now time.Time) (*kernel.Space, error) {
	if name == "" {
		name = t.Name
	}
	icon, about := t.Icon, t.About
	work := *b
	sp, err := work.PutSpace(kernel.SpaceInput{Name: &name, Icon: &icon, About: &about})
	if err != nil {
		return nil, err
	}
	if err := putTypes(sp, t.Types, now); err != nil {
		return nil, err
	}
	if samples {
		if err := putSamples(sp, &work, t, now); err != nil {
			return nil, err
		}
	}
	*b = work
	return sp, nil
}

// putTypes adds the types in two passes, so that types which refer to each other can be built. The first pass puts
// every type without its rollups, card and views, and without any relation to a type not yet added. The second pass
// puts every type in full, when all of them exist.
func putTypes(sp *kernel.Space, types []kernel.Type, now time.Time) error {
	have := map[string]bool{}
	for _, t := range types {
		if err := sp.PutType(skeleton(t, have), now); err != nil {
			return err
		}
		have[t.Key] = true
	}
	for _, t := range types {
		if err := sp.PutType(t, now); err != nil {
			return err
		}
	}
	return nil
}

// skeleton is the first-pass form of a type: no rollup, card or view, and no relation to a type that is not there yet.
func skeleton(t kernel.Type, have map[string]bool) kernel.Type {
	out := t
	out.Card = kernel.Card{}
	out.Views = nil
	out.Props = nil
	for _, p := range t.Props {
		k, known := kernel.KindOf(p.Kind)
		if known && k.Eval != nil { // computed from other records: a rollup
			continue
		}
		if known && k.Refs != nil && p.Target != t.Key && !have[p.Target] { // a relation to a type not added yet
			continue
		}
		out.Props = append(out.Props, p)
	}
	return out
}

// putSamples creates the sample records in order. They are stamped one second apart and the last one is at now, so
// that feeds read oldest first.
func putSamples(sp *kernel.Space, b *kernel.Base, t Template, now time.Time) error {
	ids := make(map[string]string, len(t.Samples))
	for i, s := range t.Samples {
		if s.Ref == "" {
			return fmt.Errorf("示例「%s」没有标识", s.Title)
		}
		if _, dup := ids[s.Ref]; dup {
			return fmt.Errorf("示例标识「%s」重复", s.Ref)
		}
		at := now.Add(-time.Duration(len(t.Samples)-1-i) * time.Second)
		id, err := putSample(sp, b, t, s, ids, at)
		if err != nil {
			return fmt.Errorf("示例「%s」：%w", s.Title, err)
		}
		ids[s.Ref] = id
	}
	return nil
}

// putSample creates one sample record and returns its id. ids maps the refs of the earlier samples to their ids.
func putSample(sp *kernel.Space, b *kernel.Base, t Template, s Sample, ids map[string]string, now time.Time) (string, error) {
	i := slices.IndexFunc(t.Types, func(k kernel.Type) bool { return k.Key == s.Type })
	if i < 0 {
		return "", fmt.Errorf("没有类型「%s」", s.Type)
	}
	typ := t.Types[i]
	vals, err := resolveValues(typ, s.Values, ids, now)
	if err != nil {
		return "", err
	}
	title := s.Title
	if s.From == "" {
		in := kernel.EntityInput{Type: s.Type, Title: &title, Values: vals}
		if s.Status != "" {
			in.Status = &s.Status
		}
		e, err := sp.PutEntity(b, in, now)
		if err != nil {
			return "", err
		}
		return e.ID, nil
	}
	src, ok := ids[s.From]
	if !ok {
		return "", fmt.Errorf("派生自还没有创建的示例「%s」", s.From)
	}
	e, err := sp.Derive(b, src, s.Type, title, now)
	if err != nil {
		return "", err
	}
	if s.Status != "" {
		if err := walk(sp, b, e, &typ, s.Status, now); err != nil {
			return "", err
		}
	}
	if len(vals) > 0 {
		if _, err := sp.PutEntity(b, kernel.EntityInput{ID: e.ID, Values: vals}, now); err != nil {
			return "", err
		}
	}
	return e.ID, nil
}

// walk moves an entity along its flow to the stage to, one move the flow allows at a time: a direct move when the
// flow allows it, otherwise the next stage of the main line. A derived entity starts at the first stage.
func walk(sp *kernel.Space, b *kernel.Base, e *kernel.Entity, typ *kernel.Type, to string, now time.Time) error {
	for cur := e.Status; cur != to; {
		next := to
		if !slices.Contains(typ.Moves(cur), to) {
			n, ok := typ.Next(cur)
			if !ok {
				return fmt.Errorf("「%s」不能从「%s」变为「%s」", e.Title, cur, to)
			}
			next = n
		}
		if _, err := sp.PutEntity(b, kernel.EntityInput{ID: e.ID, Status: &next}, now); err != nil {
			return err
		}
		cur = next
	}
	return nil
}

// resolveValues replaces the sample references and relative dates of the values of a sample.
func resolveValues(typ kernel.Type, in map[string]any, ids map[string]string, now time.Time) (map[string]any, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(in))
	for key, raw := range in {
		v, err := resolveValue(typ, key, raw, ids, now)
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, nil
}

// resolveValue resolves one value by the kind of its property. A value the kernel must refuse is returned as it is,
// so that the kernel names the problem.
func resolveValue(typ kernel.Type, key string, raw any, ids map[string]string, now time.Time) (any, error) {
	i := slices.IndexFunc(typ.Props, func(p kernel.Prop) bool { return p.Key == key })
	if i < 0 {
		return raw, nil
	}
	p := typ.Props[i]
	k, known := kernel.KindOf(p.Kind)
	if !known {
		return raw, nil
	}
	if k.Refs != nil { // a relation: its values may name earlier samples
		switch v := raw.(type) {
		case string:
			return resolveRef(v, ids)
		case []any:
			out := make([]string, 0, len(v))
			for _, x := range v {
				s, ok := x.(string)
				if !ok {
					return raw, nil
				}
				id, err := resolveRef(s, ids)
				if err != nil {
					return nil, err
				}
				out = append(out, id)
			}
			return out, nil
		}
	}
	if s, ok := raw.(string); ok && isDateKind(k, &p) {
		if d, ok := relativeDate(s, now); ok {
			return d, nil
		}
	}
	return raw, nil
}

// resolveRef turns "@ref" into the id of the earlier sample ref; any other text is returned as it is.
func resolveRef(s string, ids map[string]string) (string, error) {
	ref, isRef := strings.CutPrefix(s, "@")
	if !isRef {
		return s, nil
	}
	id, ok := ids[ref]
	if !ok {
		return "", fmt.Errorf("关联的示例「%s」还没有创建", ref)
	}
	return id, nil
}

// isDateKind tells a property whose values are dates, by the JSON Schema format of its kind.
func isDateKind(k *kernel.Kind, p *kernel.Prop) bool {
	f, _ := k.Schema(p)["format"].(string)
	return f == "date"
}

// relativeDate resolves "+30d" or "-20d" against now, as a day in time.DateOnly form.
func relativeDate(s string, now time.Time) (string, bool) {
	if len(s) < 3 || (s[0] != '+' && s[0] != '-') || s[len(s)-1] != 'd' {
		return "", false
	}
	digits := s[1 : len(s)-1]
	for _, c := range digits {
		if c < '0' || c > '9' {
			return "", false
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return "", false
	}
	if s[0] == '-' {
		n = -n
	}
	return now.AddDate(0, 0, n).Format(time.DateOnly), true
}

// load decodes the bundles of the keys from fsys, in order. An unknown field is an error, so a misspelled key in a
// bundle cannot pass unnoticed.
func load(fsys fs.FS, keys []string) ([]Template, error) {
	out := make([]Template, 0, len(keys))
	for _, key := range keys {
		data, err := fs.ReadFile(fsys, key+".json")
		if err != nil {
			return nil, err
		}
		var t Template
		d := json.NewDecoder(bytes.NewReader(data))
		d.DisallowUnknownFields()
		if err := d.Decode(&t); err != nil {
			return nil, fmt.Errorf("模板文件 %s.json 不合法：%w", key, err)
		}
		if t.Key != key {
			return nil, fmt.Errorf("模板文件 %s.json 的标识是「%s」", key, t.Key)
		}
		out = append(out, t)
	}
	return out, nil
}

func mustLoad(fsys fs.FS, keys []string) []Template {
	ts, err := load(fsys, keys)
	if err != nil {
		panic(err)
	}
	return ts
}
