package kernel

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// DefaultLimit is the page size of a query that names none; MaxLimit is the largest page.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Query asks for the entities of one type, or of every type when Type is empty. Then only built-in fields may be
// named.
type Query struct {
	Type   string `json:"type,omitempty"` // "" = every type
	Text   string `json:"text,omitempty"` // case-insensitive substring of the title or of any text property
	Lens          // where, sort, group, agg
	Limit  int    `json:"limit,omitempty"` // default DefaultLimit, at most MaxLimit
	Offset int    `json:"offset,omitempty"`
}

// Cell is one aggregate. Value is nil when the aggregate is undefined; Text is its display form.
type Cell struct {
	Name  string   `json:"name"`
	Value *float64 `json:"value"`
	Text  string   `json:"text"`
}

// Group is one group of a result. Count is the size of the whole group before paging; Items are the group's share of
// the page.
type Group struct {
	Key   string    `json:"key"`   // "" = unset
	Name  string    `json:"name"`  // "" for the unset group
	Count int       `json:"count"` // before paging
	Aggs  []Cell    `json:"aggs,omitempty"`
	Items []*Entity `json:"-"`
}

// Result is the answer to a query: the matches counted and aggregated, and the groups with their page.
type Result struct {
	Total  int     `json:"total"`
	Aggs   []Cell  `json:"aggs,omitempty"`
	Groups []Group `json:"groups"` // ungrouped = one group with Key ""
}

// opNames are the operators in vocabulary order; ops gives each its meaning.
var opNames = []string{"eq", "ne", "lt", "le", "gt", "ge", "has", "empty"}

// ops is the operator table. Each operator reads the canonical value v of a field and the parsed argument arg.
var ops = map[string]func(k *Kind, p *Prop, v, arg any) bool{
	"eq":  func(k *Kind, _ *Prop, v, arg any) bool { return k.Cmp(v, arg) == 0 },
	"ne":  func(k *Kind, _ *Prop, v, arg any) bool { return k.Cmp(v, arg) != 0 },
	"lt":  func(k *Kind, _ *Prop, v, arg any) bool { return v != nil && k.Cmp(v, arg) < 0 },
	"le":  func(k *Kind, _ *Prop, v, arg any) bool { return v != nil && k.Cmp(v, arg) <= 0 },
	"gt":  func(k *Kind, _ *Prop, v, arg any) bool { return v != nil && k.Cmp(v, arg) > 0 },
	"ge":  func(k *Kind, _ *Prop, v, arg any) bool { return v != nil && k.Cmp(v, arg) >= 0 },
	"has": hasOp,
	"empty": func(_ *Kind, _ *Prop, v, arg any) bool {
		want, _ := arg.(bool)
		return (v == nil) == want
	},
}

// hasOp is containment for a relation, a case-insensitive substring for text, and equality for everything else.
func hasOp(k *Kind, p *Prop, v, arg any) bool {
	if k.Refs != nil {
		ids := k.Refs(arg)
		if len(ids) == 0 {
			return false
		}
		have := k.Refs(v)
		for _, id := range ids {
			if !slices.Contains(have, id) {
				return false
			}
		}
		return true
	}
	if isTextProp(p) {
		s, _ := v.(string)
		a, _ := arg.(string)
		return strings.Contains(strings.ToLower(s), strings.ToLower(a))
	}
	return k.Cmp(v, arg) == 0
}

// bound is one condition on one type: its field resolved, its argument parsed.
type bound struct {
	field string
	k     *Kind
	p     Prop
	test  func(k *Kind, p *Prop, v, arg any) bool
	arg   any
}

// aggBound is one aggregate of a query, with its operand resolved.
type aggBound struct {
	agg  aggregate
	of   string
	prop Prop
}

// typePlan is a query bound to one type. err is set when a condition, a sort key or the group field does not fit the
// type; such a type is left out of the query.
type typePlan struct {
	t         *Type
	err       error
	where     []bound
	sortKinds []*Kind
	groupProp Prop
	groupKind *Kind
}

// plan is a query checked against one space. The query fails only when no type binds.
type plan struct {
	sorts     []Order
	types     []*typePlan          // the types the query reads, in space order
	byKey     map[string]*typePlan // the same, by key
	grouped   bool
	group     string
	groupKind *Kind
	ordered   bool     // the group field is enumerable: its groups follow Enum
	order     []string // the enumerable values across the types, in order (when ordered)
	aggs      []aggBound
}

// row is one matching entity with the values its sort keys read.
type row struct {
	e    *Entity
	tp   *typePlan
	vals []any
}

// membership is one group an entity falls into: key "" is the unset group.
type membership struct {
	key string
	rep any // the canonical value the key stands for
}

// bucket is one group of a result: its members in query order and the share of them on the page.
type bucket struct {
	key, name string
	rep       any
	members   []*Entity
	page      []*Entity
}

// Check refuses a query that does not fit the space: an unknown type, field, operator or aggregate, an argument that
// no type accepts, or a limit out of range.
func (w *World) Check(q Query) error {
	_, err := w.plan(q)
	return err
}

// Run answers a query: the matching entities in order (sort keys, then updated descending, then id), grouped, with the
// aggregates over the matches and over each group, and the page of the flattened groups.
func (w *World) Run(q Query) (Result, error) {
	pl, err := w.plan(q)
	if err != nil {
		return Result{}, err
	}
	needle := strings.ToLower(strings.TrimSpace(q.Text))
	var rows []row
	var entities []*Entity
	for _, e := range w.s.Entities {
		if e == nil {
			continue
		}
		tp := pl.byKey[e.Type]
		if tp == nil {
			continue
		}
		if needle != "" && !w.matchText(e, needle) {
			continue
		}
		if !tp.matches(w, e) {
			continue
		}
		r := row{e: e, tp: tp, vals: make([]any, len(pl.sorts))}
		for i, o := range pl.sorts {
			r.vals[i] = w.sortValue(e, o.By)
		}
		rows = append(rows, r)
	}
	slices.SortFunc(rows, pl.compare)
	for _, r := range rows {
		entities = append(entities, r.e)
	}
	buckets := pl.bucketize(w, rows)
	limit := q.Limit
	if limit == 0 {
		limit = DefaultLimit
	}
	pageBuckets(buckets, q.Offset, limit)
	res := Result{Total: len(rows), Aggs: pl.cells(w, entities), Groups: make([]Group, 0, len(buckets))}
	for _, b := range buckets {
		res.Groups = append(res.Groups, Group{
			Key: b.key, Name: b.name, Count: len(b.members),
			Aggs: pl.cells(w, b.members), Items: b.page,
		})
	}
	return res, nil
}

// plan checks a query against the space and binds it to every type it may read.
func (w *World) plan(q Query) (*plan, error) {
	if q.Limit < 0 || q.Limit > MaxLimit {
		return nil, fmt.Errorf("每页条数应在 0 到 %d 之间", MaxLimit)
	}
	if q.Offset < 0 {
		return nil, errors.New("偏移量不能是负数")
	}
	if len(q.Where) > MaxWhere {
		return nil, fmt.Errorf("筛选条件不能超过 %d 个", MaxWhere)
	}
	if len(q.Sort) > MaxSort {
		return nil, fmt.Errorf("排序不能超过 %d 个", MaxSort)
	}
	if len(q.Agg) > MaxAgg {
		return nil, fmt.Errorf("汇总不能超过 %d 个", MaxAgg)
	}
	types, err := w.queryTypes(q.Type)
	if err != nil {
		return nil, err
	}
	if q.Type == "" {
		if err := crossFields(q); err != nil {
			return nil, err
		}
	}
	scope := types
	if len(scope) == 0 {
		// An empty space still resolves the built-in fields, so that a bad query is refused. No entity has the
		// synthetic type, so it reads nothing.
		scope = []*Type{{}}
	}
	pl := &plan{sorts: q.Sort, byKey: map[string]*typePlan{}, grouped: q.Group != "", group: q.Group}
	var bounds []*typePlan
	var firstErr error
	ok := false
	for _, t := range scope {
		tp := w.bindType(t, q)
		bounds = append(bounds, tp)
		if tp.err != nil {
			if firstErr == nil {
				firstErr = tp.err
			}
			continue
		}
		ok = true
		if len(types) > 0 {
			pl.types = append(pl.types, tp)
			pl.byKey[t.Key] = tp
		}
	}
	if !ok {
		return nil, firstErr
	}
	pl.aggs, err = w.bindAggs(q, scope)
	if err != nil {
		return nil, err
	}
	if q.Group != "" {
		for _, tp := range bounds {
			if tp.err == nil {
				pl.groupKind = tp.groupKind
				break
			}
		}
		if pl.groupKind.Enum != nil {
			pl.ordered = true
			pl.order = []string{}
			for _, tp := range pl.types {
				pl.order = appendMissing(pl.order, pl.groupKind.Enum(&tp.groupProp))
			}
		}
	}
	return pl, nil
}

// queryTypes are the types a query reads: the one it names, or every type of the space.
func (w *World) queryTypes(typ string) ([]*Type, error) {
	if typ == "" {
		var out []*Type
		for _, t := range w.s.Types {
			if t != nil {
				out = append(out, t)
			}
		}
		return out, nil
	}
	t, ok := w.s.typeByKey(typ)
	if !ok {
		return nil, fmt.Errorf("没有类型「%s」", typ)
	}
	return []*Type{t}, nil
}

// crossFields refuses a query over every type that names a field other than a built-in one: a declared property
// exists on one type only.
func crossFields(q Query) error {
	var names []string
	for _, c := range q.Where {
		names = append(names, c.Field)
	}
	for _, o := range q.Sort {
		names = append(names, o.By)
	}
	if q.Group != "" {
		names = append(names, q.Group)
	}
	for _, a := range q.Agg {
		if a.Of != "" {
			names = append(names, a.Of)
		}
	}
	for _, n := range names {
		if _, ok := builtinProps[n]; !ok {
			return fmt.Errorf("跨类型查询只能使用内置字段，「%s」不是内置字段；请指定类型", n)
		}
	}
	return nil
}

// bindType binds the conditions, sort keys and group field of a query to one type.
func (w *World) bindType(t *Type, q Query) *typePlan {
	tp := &typePlan{t: t}
	for _, c := range q.Where {
		b, err := bindCond(w.s, t, c)
		if err != nil {
			tp.err = err
			return tp
		}
		tp.where = append(tp.where, b)
	}
	for _, o := range q.Sort {
		_, k, err := bindField(w.s, t, o.By)
		if err != nil {
			tp.err = err
			return tp
		}
		tp.sortKinds = append(tp.sortKinds, k)
	}
	if q.Group != "" {
		p, k, err := bindField(w.s, t, q.Group)
		if err != nil {
			tp.err = err
			return tp
		}
		tp.groupProp, tp.groupKind = p, k
	}
	return tp
}

// bindAggs resolves the aggregates of a query: each names an aggregate, and an operand is a numeric field on every
// type in scope.
func (w *World) bindAggs(q Query, scope []*Type) ([]aggBound, error) {
	out := make([]aggBound, 0, len(q.Agg))
	for _, a := range q.Agg {
		agg, ok := aggOf(a.Op)
		if !ok {
			return nil, fmt.Errorf("汇总方式「%s」不存在", a.Op)
		}
		ab := aggBound{agg: agg, of: a.Of}
		switch {
		case agg.Operand:
			if a.Of == "" {
				return nil, fmt.Errorf("汇总「%s」需要一个数字属性", agg.Label)
			}
			for i, t := range scope {
				p, k, err := bindField(w.s, t, a.Of)
				if err != nil {
					return nil, err
				}
				if k.Num == nil {
					return nil, fmt.Errorf("属性「%s」不是数字", p.Name)
				}
				if i == 0 {
					ab.prop = p
				}
			}
		case a.Of != "":
			return nil, fmt.Errorf("汇总「%s」不需要属性", agg.Label)
		}
		out = append(out, ab)
	}
	return out, nil
}

// bindField resolves a field of a type, built-in fields included, to its property and its kind.
func bindField(s *Space, t *Type, key string) (Prop, *Kind, error) {
	p, ok := s.Field(t, key)
	if !ok {
		return Prop{}, nil, fmt.Errorf("类型「%s」没有属性「%s」", t.Name, key)
	}
	k, ok := KindOf(p.Kind)
	if !ok {
		return Prop{}, nil, fmt.Errorf("属性「%s」的类型「%s」不存在", p.Name, p.Kind)
	}
	return p, k, nil
}

// bindCond resolves one condition on a type: its field, its operator and its argument.
func bindCond(s *Space, t *Type, c Cond) (bound, error) {
	p, k, err := bindField(s, t, c.Field)
	if err != nil {
		return bound{}, err
	}
	test, ok := ops[c.Op]
	if !ok {
		return bound{}, fmt.Errorf("没有运算「%s」", c.Op)
	}
	arg, err := condArg(c.Op, k, &p, c.Value)
	if err != nil {
		return bound{}, err
	}
	return bound{field: c.Field, k: k, p: p, test: test, arg: arg}, nil
}

// condArg is the argument of a condition as its operator reads it. It is parsed by the field's kind, except that
// "empty" takes a boolean (no value means true) and "has" on a relation takes a list of ids. Only "eq" and "ne" may
// have no value, which then means "unset".
func condArg(op string, k *Kind, p *Prop, raw any) (any, error) {
	if op == "empty" {
		if raw == nil {
			return true, nil
		}
		kb, _ := KindOf(KindBoolean)
		b, err := kb.Parse(&Prop{Key: p.Key, Name: p.Name, Kind: KindBoolean}, raw)
		if err != nil {
			return nil, err
		}
		if b == nil {
			return true, nil
		}
		return b, nil
	}
	ap := *p
	if op == "has" && k.Refs != nil {
		ap.Many = true
	}
	arg, err := k.Parse(&ap, raw)
	if err != nil {
		return nil, err
	}
	if arg == nil && op != "eq" && op != "ne" {
		return nil, fmt.Errorf("「%s」的运算「%s」需要一个值", p.Name, op)
	}
	return arg, nil
}

// matches reports whether an entity of the type satisfies every condition.
func (tp *typePlan) matches(w *World, e *Entity) bool {
	for i := range tp.where {
		b := &tp.where[i]
		if !b.test(b.k, &b.p, w.Value(e, b.field), b.arg) {
			return false
		}
	}
	return true
}

// matchText reports whether a lower-cased needle is in the title or in a text property of the entity.
func (w *World) matchText(e *Entity, needle string) bool {
	if strings.Contains(strings.ToLower(e.Title), needle) {
		return true
	}
	t := w.typeOf(e)
	if t == nil {
		return false
	}
	for i := range t.Props {
		if !isTextProp(&t.Props[i]) {
			continue
		}
		if s, ok := e.Values[t.Props[i].Key].(string); ok && strings.Contains(strings.ToLower(s), needle) {
			return true
		}
	}
	return false
}

// compare orders matching rows: the sort keys, then updated descending, then id.
func (pl *plan) compare(a, b row) int {
	for i, o := range pl.sorts {
		c := sortCmp(a.tp.sortKinds[i], a.vals[i], b.vals[i])
		if o.Desc {
			c = -c
		}
		if c != 0 {
			return c
		}
	}
	if c := b.e.Updated.Compare(a.e.Updated); c != 0 {
		return c
	}
	return strings.Compare(a.e.ID, b.e.ID)
}

// sortCmp orders two sort values: the timestamps of created and updated by their instant, anything else by its kind.
func sortCmp(k *Kind, a, b any) int {
	if x, ok := a.(time.Time); ok {
		if y, ok := b.(time.Time); ok {
			return x.Compare(y)
		}
	}
	return k.Cmp(a, b)
}

// bucketize groups the rows in their order. An entity under a many relation falls into each of its groups.
func (pl *plan) bucketize(w *World, rows []row) []*bucket {
	index := map[string]*bucket{}
	var buckets []*bucket
	if !pl.grouped {
		index[""] = &bucket{}
		buckets = append(buckets, index[""])
	}
	for _, r := range rows {
		ms := []membership{{}}
		if pl.grouped {
			ms = memberships(pl.groupKind, w.Value(r.e, pl.group))
		}
		for _, m := range ms {
			b := index[m.key]
			if b == nil {
				b = &bucket{key: m.key, rep: m.rep}
				if m.key != "" {
					b.name = pl.groupKind.Text(&r.tp.groupProp, m.rep, w.title)
				}
				index[m.key] = b
				buckets = append(buckets, b)
			}
			b.members = append(b.members, r.e)
		}
	}
	slices.SortStableFunc(buckets, pl.compareBuckets)
	return buckets
}

// compareBuckets orders groups: in Enum order when the field is enumerable, else by Cmp; the unset group last.
func (pl *plan) compareBuckets(a, b *bucket) int {
	switch {
	case a.key == b.key:
		return 0
	case a.key == "":
		return 1
	case b.key == "":
		return -1
	case pl.ordered:
		if c := cmp.Compare(rank(pl.order, a.key), rank(pl.order, b.key)); c != 0 {
			return c
		}
	default:
		if c := pl.groupKind.Cmp(a.rep, b.rep); c != 0 {
			return c
		}
	}
	return strings.Compare(a.key, b.key)
}

// rank is the position of a value in an order; values not in it come last.
func rank(order []string, key string) int {
	if i := slices.Index(order, key); i >= 0 {
		return i
	}
	return len(order)
}

// pageBuckets keeps, in each group, the members whose place in the flattened groups falls in the page.
func pageBuckets(buckets []*bucket, offset, limit int) {
	slot := 0
	for _, b := range buckets {
		for _, e := range b.members {
			if slot >= offset && slot < offset+limit {
				b.page = append(b.page, e)
			}
			slot++
		}
	}
}

// cells evaluates the query's aggregates over a set of entities.
func (pl *plan) cells(w *World, set []*Entity) []Cell {
	if len(pl.aggs) == 0 {
		return nil
	}
	out := make([]Cell, 0, len(pl.aggs))
	for _, a := range pl.aggs {
		out = append(out, a.cell(w, set))
	}
	return out
}

// cell is one aggregate over a set. Its text is shown the way a rollup of the same aggregate is.
func (a aggBound) cell(w *World, set []*Entity) Cell {
	c := Cell{Name: a.agg.Label}
	if a.of != "" {
		c.Name += " " + a.prop.Name
	}
	f, ok := a.agg.Fn(w, set, a.of)
	if !ok {
		return c
	}
	c.Value = &f
	kr, _ := KindOf(KindRollup)
	c.Text = kr.Text(&Prop{Kind: KindRollup, Op: a.agg.Name, Unit: a.prop.Unit}, f, noName)
	return c
}

// noName is the name function of a text that names no entity.
func noName(string) string { return "" }

// memberships are the groups an entity falls into under one field: one per id of a relation, one for any other value,
// and the unset group for nothing.
func memberships(k *Kind, v any) []membership {
	if k.Refs != nil {
		ids := k.Refs(v)
		if len(ids) == 0 {
			return []membership{{}}
		}
		out := make([]membership, 0, len(ids))
		for _, id := range ids {
			out = append(out, membership{key: id, rep: []string{id}})
		}
		return out
	}
	if v == nil {
		return []membership{{}}
	}
	return []membership{{key: scalarKey(v), rep: v}}
}

// scalarKey is the group key of a canonical scalar value.
func scalarKey(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return fmt.Sprint(v)
}

// appendMissing appends the values of src that dst does not hold yet.
func appendMissing(dst, src []string) []string {
	for _, s := range src {
		if !slices.Contains(dst, s) {
			dst = append(dst, s)
		}
	}
	return dst
}
