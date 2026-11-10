package kernel

import (
	"math"
	"slices"
	"strings"
	"time"
)

// World is the index of one space: its entities by id, their types, and every link, in both directions. It is
// built by Open and never changed afterwards.
type World struct {
	s     *Space
	types map[string]*Type
	byID  map[string]*Entity
	out   map[string][]Link // by source entity id, in schema order, then origin
	in    map[string][]Link // by target entity id, grouped by (source type, prop)
}

// Open builds the index of a space. Every value is walked through its kind's Refs, so the links are exactly the
// ids held by relation values and by Origin.
func Open(s *Space) *World {
	w := &World{
		s:     s,
		types: make(map[string]*Type, len(s.Types)),
		byID:  make(map[string]*Entity, len(s.Entities)),
		out:   make(map[string][]Link, len(s.Entities)),
		in:    map[string][]Link{},
	}
	for _, t := range s.Types {
		if t != nil {
			w.types[t.Key] = t
		}
	}
	for _, e := range s.Entities {
		if e != nil {
			w.byID[e.ID] = e
		}
	}
	for _, e := range s.Entities {
		if e == nil {
			continue
		}
		var links []Link
		if t := w.typeOf(e); t != nil {
			for i := range t.Props {
				p := &t.Props[i]
				k, ok := KindOf(p.Kind)
				if !ok || k.Refs == nil || k.Eval != nil {
					continue
				}
				for _, id := range k.Refs(e.Values[p.Key]) {
					links = append(links, Link{From: e.ID, Prop: p.Key, To: id})
				}
			}
		}
		if e.Origin != "" {
			links = append(links, Link{From: e.ID, Prop: "origin", To: e.Origin})
		}
		w.out[e.ID] = links
		for _, l := range links {
			w.in[l.To] = append(w.in[l.To], l)
		}
	}
	for id := range w.in {
		slices.SortStableFunc(w.in[id], w.backlinkOrder)
	}
	return w
}

// backlinkOrder orders incoming links by the source type, then by the prop; ties keep the entity order.
func (w *World) backlinkOrder(a, b Link) int {
	ta, tb := "", ""
	if e := w.byID[a.From]; e != nil {
		ta = e.Type
	}
	if e := w.byID[b.From]; e != nil {
		tb = e.Type
	}
	if c := strings.Compare(ta, tb); c != 0 {
		return c
	}
	return strings.Compare(a.Prop, b.Prop)
}

// typeOf is the type of an entity, nil when the entity is nil or its type is unknown.
func (w *World) typeOf(e *Entity) *Type {
	if e == nil {
		return nil
	}
	return w.types[e.Type]
}

// prop resolves a field of an entity's type, built-in fields included.
func (w *World) prop(e *Entity, key string) (Prop, bool) {
	t := w.typeOf(e)
	if t == nil {
		return Prop{}, false
	}
	return w.s.Field(t, key)
}

// title names an entity by its id; "" when the id is not in the space.
func (w *World) title(id string) string {
	if e := w.byID[id]; e != nil {
		return e.Title
	}
	return ""
}

// number is the numeric reading of a field of an entity; false when the field is unset or not numeric.
func (w *World) number(e *Entity, key string) (float64, bool) {
	p, ok := w.prop(e, key)
	if !ok {
		return 0, false
	}
	k, ok := KindOf(p.Kind)
	if !ok || k.Num == nil {
		return 0, false
	}
	v := w.Value(e, key)
	if v == nil {
		return 0, false
	}
	return k.Num(v)
}

// Entity returns the entity with the given id, or nil.
func (w *World) Entity(id string) *Entity { return w.byID[id] }

// builtins supplies the values of the built-in fields. A nil result is unset.
var builtins = map[string]func(w *World, e *Entity) any{
	"title": func(_ *World, e *Entity) any { return e.Title },
	"status": func(_ *World, e *Entity) any {
		if e.Status == "" {
			return nil
		}
		return e.Status
	},
	"phase": func(w *World, e *Entity) any {
		t := w.typeOf(e)
		if t == nil {
			return nil
		}
		st, ok := t.Stage(e.Status)
		if !ok {
			return nil
		}
		return string(st.Phase)
	},
	"type": func(_ *World, e *Entity) any { return e.Type },
	"origin": func(_ *World, e *Entity) any {
		if e.Origin == "" {
			return nil
		}
		return []string{e.Origin}
	},
	"created": func(_ *World, e *Entity) any { return e.Created.UTC().Format(time.DateOnly) },
	"updated": func(_ *World, e *Entity) any { return e.Updated.UTC().Format(time.DateOnly) },
}

// sortValue is the value a sort key orders an entity by. The built-in created and updated fields order by their full
// timestamp, so that two entities of one day keep their order; every other key is its Value.
func (w *World) sortValue(e *Entity, key string) any {
	switch key {
	case "created":
		return e.Created
	case "updated":
		return e.Updated
	}
	return w.Value(e, key)
}

// Value is the canonical value of a field of an entity: a built-in, a computed rollup, or the stored value. Total:
// an unknown key or a nil entity gives nil.
func (w *World) Value(e *Entity, key string) any {
	p, ok := w.prop(e, key)
	if !ok {
		return nil
	}
	if fn, ok := builtins[key]; ok {
		return fn(w, e)
	}
	k, ok := KindOf(p.Kind)
	if !ok {
		return nil
	}
	if k.Eval != nil {
		return k.Eval(w, e, &p)
	}
	return e.Values[key]
}

// Text is the display text of a field of an entity; "" when it is unset.
func (w *World) Text(e *Entity, key string) string {
	p, ok := w.prop(e, key)
	if !ok {
		return ""
	}
	v := w.Value(e, key)
	if v == nil {
		return ""
	}
	k, ok := KindOf(p.Kind)
	if !ok {
		return ""
	}
	return k.Text(&p, v, w.title)
}

// Out are the links from an entity: in schema order, then origin. A nil entity has none.
func (w *World) Out(e *Entity) []Link {
	if e == nil {
		return nil
	}
	return slices.Clone(w.out[e.ID])
}

// In are the backlinks of an entity, grouped by (source type, prop).
func (w *World) In(e *Entity) []Link {
	if e == nil {
		return nil
	}
	return slices.Clone(w.in[e.ID])
}

// Related follows a relation: "prop" gives the entities the entity points to through its own relation (or origin);
// "type.prop" gives the entities of that type whose relation prop points to it (the backlinks). Dangling ids are
// skipped.
func (w *World) Related(e *Entity, via string) []*Entity {
	if e == nil {
		return nil
	}
	typeKey, prop, dotted := strings.Cut(via, ".")
	var out []*Entity
	if !dotted {
		p, ok := w.prop(e, via)
		if !ok {
			return nil
		}
		k, ok := KindOf(p.Kind)
		if !ok || k.Refs == nil {
			return nil
		}
		for _, id := range k.Refs(w.Value(e, via)) {
			if t := w.byID[id]; t != nil {
				out = append(out, t)
			}
		}
		return out
	}
	for _, l := range w.in[e.ID] {
		if l.Prop != prop {
			continue
		}
		if src := w.byID[l.From]; src != nil && src.Type == typeKey {
			out = append(out, src)
		}
	}
	return out
}

// Ratio is the progress of an entity in 0..1: the type's card progress when it has one (its value, or undefined),
// else the position along the flow.
func (w *World) Ratio(e *Entity) (float64, bool) {
	t := w.typeOf(e)
	if t == nil {
		return 0, false
	}
	if pr := t.CardOrDefault().Progress; pr != nil {
		a, ok := w.number(e, pr.Of)
		if !ok {
			return 0, false
		}
		if pr.Over == "" {
			return clamp01(a), true
		}
		b, ok := w.number(e, pr.Over)
		if !ok || b == 0 {
			return 0, false
		}
		return clamp01(a / b), true
	}
	return t.Position(e.Status)
}

func clamp01(f float64) float64 { return math.Max(0, math.Min(1, f)) }

// aggregate is one aggregate function. A rollup and a query aggregate are the same function over a different set.
type aggregate struct {
	Name, Label string // "sum", "合计"
	Operand     bool   // needs Of
	Fn          func(w *World, set []*Entity, of string) (float64, bool)
}

// Aggregates is the aggregate table, in vocabulary order.
var Aggregates = []aggregate{
	{Name: "count", Label: "数量", Fn: aggCount},
	{Name: "sum", Label: "合计", Operand: true, Fn: aggSum},
	{Name: "avg", Label: "平均", Operand: true, Fn: aggAvg},
	{Name: "min", Label: "最小", Operand: true, Fn: aggMin},
	{Name: "max", Label: "最大", Operand: true, Fn: aggMax},
	{Name: "percent_done", Label: "完成度", Fn: aggPercentDone},
}

// aggOf finds an aggregate by name.
func aggOf(name string) (aggregate, bool) {
	i := slices.IndexFunc(Aggregates, func(a aggregate) bool { return a.Name == name })
	if i < 0 {
		return aggregate{}, false
	}
	return Aggregates[i], true
}

// readings are the numeric readings of a field over a set; unset values are skipped.
func readings(w *World, set []*Entity, of string) []float64 {
	var out []float64
	for _, e := range set {
		if f, ok := w.number(e, of); ok {
			out = append(out, f)
		}
	}
	return out
}

func aggCount(_ *World, set []*Entity, _ string) (float64, bool) {
	return float64(len(set)), true
}

// aggSum is defined on every set; the sum of nothing is 0.
func aggSum(w *World, set []*Entity, of string) (float64, bool) {
	total := 0.0
	for _, f := range readings(w, set, of) {
		total += f
	}
	return finiteResult(total)
}

func aggAvg(w *World, set []*Entity, of string) (float64, bool) {
	vals := readings(w, set, of)
	if len(vals) == 0 {
		return 0, false
	}
	total := 0.0
	for _, f := range vals {
		total += f
	}
	return finiteResult(total / float64(len(vals)))
}

// finiteResult is an aggregate result that is a finite number. An overflow is undefined, since it cannot be stored
// or encoded.
func finiteResult(f float64) (float64, bool) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, false
	}
	return f, true
}

func aggMin(w *World, set []*Entity, of string) (float64, bool) {
	vals := readings(w, set, of)
	if len(vals) == 0 {
		return 0, false
	}
	return slices.Min(vals), true
}

func aggMax(w *World, set []*Entity, of string) (float64, bool) {
	vals := readings(w, set, of)
	if len(vals) == 0 {
		return 0, false
	}
	return slices.Max(vals), true
}

// aggPercentDone is the share of entities in phase done among those with a flow that are not dropped; undefined
// when there are none.
func aggPercentDone(w *World, set []*Entity, _ string) (float64, bool) {
	den, num := 0, 0
	for _, e := range set {
		t := w.typeOf(e)
		if t == nil || len(t.Flow) == 0 {
			continue
		}
		st, ok := t.Stage(e.Status)
		if !ok || st.Phase == Dropped {
			continue
		}
		den++
		if st.Phase == Done {
			num++
		}
	}
	if den == 0 {
		return 0, false
	}
	return float64(num) / float64(den), true
}
