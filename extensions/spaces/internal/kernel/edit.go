package kernel

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SpaceInput changes a space, or creates one when ID does not name an existing space. Creating needs a Name.
type SpaceInput struct {
	ID    string  `json:"id,omitempty"`
	Name  *string `json:"name,omitempty"`
	Icon  *string `json:"icon,omitempty"`
	About *string `json:"about,omitempty"`
}

// EntityInput creates an entity (ID empty) or changes one (ID set). Values are merged; null unsets a value and a
// relation value replaces the list.
type EntityInput struct {
	ID     string         `json:"id,omitempty"`
	Type   string         `json:"type,omitempty"` // create only
	Title  *string        `json:"title,omitempty"`
	Status *string        `json:"status,omitempty"`
	Values map[string]any `json:"values,omitempty"`
}

// Space resolves a space by id. An empty id resolves when there is exactly one space.
func (b *Base) Space(id string) (*Space, error) {
	if id == "" {
		switch len(b.Spaces) {
		case 0:
			return nil, errors.New("还没有空间")
		case 1:
			return b.Spaces[0], nil
		}
		return nil, errors.New("有多个空间，请指定空间编号")
	}
	if i := b.spaceIndex(id); i >= 0 {
		return b.Spaces[i], nil
	}
	return nil, fmt.Errorf("没有空间「%s」", id)
}

// Find returns an entity by id, across the spaces.
func (b *Base) Find(id string) (*Space, *Entity, error) {
	for _, s := range b.Spaces {
		if s == nil {
			continue
		}
		if e := s.entity(id); e != nil {
			return s, e, nil
		}
	}
	return nil, nil, fmt.Errorf("没有记录「%s」", id)
}

// PutSpace creates a space, or changes one. A failed change leaves the base as it was.
func (b *Base) PutSpace(in SpaceInput) (*Space, error) {
	nb := *b
	nb.Spaces = slices.Clone(b.Spaces)
	if i := b.spaceIndex(in.ID); in.ID != "" && i >= 0 {
		ns := *b.Spaces[i]
		applySpace(&ns, in)
		nb.Spaces[i] = &ns
		if err := b.replace(nb); err != nil {
			return nil, err
		}
		return &ns, nil
	}
	if in.Name == nil {
		return nil, errors.New("空间需要一个名称")
	}
	seq := b.Seq
	id := in.ID
	if id == "" {
		// Skip the ids that callers already took; the counter advances past each one.
		for {
			seq++
			id = "s" + strconv.FormatUint(seq, 36)
			if b.spaceIndex(id) < 0 {
				break
			}
		}
	}
	ns := &Space{ID: id, Types: []*Type{}, Entities: []*Entity{}}
	applySpace(ns, in)
	nb.Spaces = append(nb.Spaces, ns)
	nb.Seq = seq
	if err := b.replace(nb); err != nil {
		return nil, err
	}
	return ns, nil
}

// DropSpace removes a space with everything in it.
func (b *Base) DropSpace(id string) error {
	s, err := b.Space(id)
	if err != nil {
		return err
	}
	nb := *b
	nb.Spaces = slices.DeleteFunc(slices.Clone(b.Spaces), func(x *Space) bool { return x == s })
	*b = nb
	return nil
}

// replace checks a changed base and, only if it is valid, makes it the base.
func (b *Base) replace(nb Base) error {
	if err := nb.Check(); err != nil {
		return err
	}
	*b = nb
	return nil
}

func applySpace(s *Space, in SpaceInput) {
	if in.Name != nil {
		s.Name = *in.Name
	}
	if in.Icon != nil {
		s.Icon = *in.Icon
	}
	if in.About != nil {
		s.About = *in.About
	}
}

func (b *Base) spaceIndex(id string) int {
	return slices.IndexFunc(b.Spaces, func(s *Space) bool { return s != nil && s.ID == id })
}

// commit checks a changed space and, only if it is valid, makes it the space. A failed change leaves s untouched.
func (s *Space) commit(ns *Space) error {
	if err := ns.check(map[string]bool{}); err != nil {
		return err
	}
	*s = *ns
	return nil
}

func (s *Space) typeIndex(key string) int {
	return slices.IndexFunc(s.Types, func(t *Type) bool { return t != nil && t.Key == key })
}

func (s *Space) entityIndex(id string) int {
	return slices.IndexFunc(s.Entities, func(e *Entity) bool { return e != nil && e.ID == id })
}

// entity returns the entity with the id, or nil.
func (s *Space) entity(id string) *Entity {
	if i := s.entityIndex(id); i >= 0 {
		return s.Entities[i]
	}
	return nil
}

// allOfType reports whether every id names an entity of the type.
func (s *Space) allOfType(ids []string, typ string) bool {
	for _, id := range ids {
		if e := s.entity(id); e == nil || e.Type != typ {
			return false
		}
	}
	return true
}

func entityID(seq uint64) string { return "e" + strconv.FormatUint(seq, 36) }

// PutType adds a type, or replaces a type as a whole. Replacing migrates every entity of the type to the new
// definition (see migrate). The whole change succeeds or leaves the space as it was.
func (s *Space) PutType(t Type, now time.Time) error {
	ns := *s
	ns.Types = slices.Clone(s.Types)
	nt := t
	i := s.typeIndex(t.Key)
	if i < 0 {
		ns.Types = append(ns.Types, &nt)
	} else {
		old := s.Types[i]
		nt.Views = migrateViews(old, &nt)
		ns.Types[i] = &nt
		// The new type is checked on its own, in the space that will hold it, before any entity is migrated.
		if err := nt.check(&ns); err != nil {
			return err
		}
		ents, err := migrate(s, old, &nt, now)
		if err != nil {
			return err
		}
		ns.Entities = ents
	}
	return s.commit(&ns)
}

// DropType removes a type with its entities. It is refused while another type's relation points to it or a rollup
// counts its entities.
func (s *Space) DropType(key string) error {
	i := s.typeIndex(key)
	if i < 0 {
		return fmt.Errorf("没有类型「%s」", key)
	}
	for _, t := range s.Types {
		if t == nil || t.Key == key {
			continue
		}
		for _, p := range t.Props {
			if isRelationProp(&p) && p.Target == key {
				return fmt.Errorf("「%s」的属性「%s」还关联着它，不能删除这个类型", t.Name, p.Name)
			}
			if tk, _, dotted := strings.Cut(p.Via, "."); dotted && tk == key {
				return fmt.Errorf("汇总「%s」还在统计它，不能删除这个类型", p.Name)
			}
		}
	}
	ns := *s
	ns.Types = slices.DeleteFunc(slices.Clone(s.Types), func(t *Type) bool { return t != nil && t.Key == key })
	ns.Entities = slices.DeleteFunc(slices.Clone(s.Entities), func(e *Entity) bool { return e != nil && e.Type == key })
	return s.commit(&ns)
}

// PutEntity creates an entity (EntityInput.ID empty) or changes one. A status change must be a move the flow allows.
// The new entity's id is issued from b's counter, which advances only when the entity is stored.
func (s *Space) PutEntity(b *Base, in EntityInput, now time.Time) (*Entity, error) {
	if in.ID == "" {
		return s.createEntity(b, in, now)
	}
	return s.changeEntity(in, now)
}

func (s *Space) createEntity(b *Base, in EntityInput, now time.Time) (*Entity, error) {
	t, ok := s.typeByKey(in.Type)
	if !ok {
		return nil, fmt.Errorf("没有类型「%s」", in.Type)
	}
	if in.Title == nil || strings.TrimSpace(*in.Title) == "" {
		return nil, errors.New("记录需要一个标题")
	}
	if err := checkKeys(t, in.Values); err != nil {
		return nil, err
	}
	e := &Entity{Type: t.Key, Title: strings.TrimSpace(*in.Title), Values: mergeValues(nil, in.Values), Created: now, Updated: now}
	if len(t.Flow) > 0 {
		e.Status = t.Flow[0].Name
	}
	if in.Status != nil {
		e.Status = *in.Status
	}
	seq := b.Seq + 1
	e.ID = entityID(seq)
	ns := *s
	ns.Entities = append(slices.Clone(s.Entities), e)
	if err := s.commit(&ns); err != nil {
		return nil, err
	}
	b.Seq = seq
	return e, nil
}

func (s *Space) changeEntity(in EntityInput, now time.Time) (*Entity, error) {
	i := s.entityIndex(in.ID)
	if i < 0 {
		return nil, fmt.Errorf("没有记录「%s」", in.ID)
	}
	old := s.Entities[i]
	t, ok := s.typeByKey(old.Type)
	if !ok {
		return nil, fmt.Errorf("记录「%s」的类型「%s」不存在", old.ID, old.Type)
	}
	if in.Type != "" && in.Type != old.Type {
		return nil, errors.New("记录的类型在创建时指定，之后不能改")
	}
	if err := checkKeys(t, in.Values); err != nil {
		return nil, err
	}
	ne := *old
	if in.Title != nil {
		ne.Title = strings.TrimSpace(*in.Title)
	}
	if in.Status != nil {
		if err := t.move(old.Status, *in.Status); err != nil {
			return nil, err
		}
		ne.Status = *in.Status
	}
	ne.Values = mergeValues(old.Values, in.Values)
	ne.Updated = now
	ns := *s
	ns.Entities = slices.Clone(s.Entities)
	ns.Entities[i] = &ne
	if err := s.commit(&ns); err != nil {
		return nil, err
	}
	return &ne, nil
}

// move checks a status change against the flow. Nothing outside Moves is allowed.
func (t *Type) move(from, to string) error {
	if from == to {
		return nil
	}
	if len(t.Flow) == 0 {
		return fmt.Errorf("类型「%s」没有状态", t.Name)
	}
	if _, ok := t.Stage(to); !ok {
		return fmt.Errorf("「%s」不是「%s」的阶段", to, t.Name)
	}
	moves := t.Moves(from)
	if slices.Contains(moves, to) {
		return nil
	}
	if len(moves) == 0 {
		return fmt.Errorf("「%s」不能直接变为「%s」；没有可以变为的阶段", from, to)
	}
	return fmt.Errorf("「%s」不能直接变为「%s」；可以变为：%s", from, to, strings.Join(moves, "、"))
}

// checkKeys refuses a value key the type does not declare, with the list of its properties.
func checkKeys(t *Type, values map[string]any) error {
	for _, key := range sortedKeys(values) {
		if _, ok := declaredProp(t, key); !ok {
			return fmt.Errorf("类型「%s」没有属性「%s」；它的属性有：%s", t.Name, key, propKeyList(t))
		}
	}
	return nil
}

// mergeValues merges values into a copy of old: null removes a key. It never changes old.
func mergeValues(old, in map[string]any) map[string]any {
	out := make(map[string]any, len(old)+len(in))
	for k, v := range old {
		out[k] = v
	}
	for k, v := range in {
		if v == nil {
			delete(out, k)
		} else {
			out[k] = v
		}
	}
	return tidy(out)
}

// tidy returns nil for an empty value map, so that the stored form has no empty object.
func tidy(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	return m
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Derive creates an entity of another type of the same space, from an entity of this space. It carries every stored
// property of the target type whose key the source type also declares, when the target kind accepts the source value
// (and a relation's targets are of the target's type). The status is the first stage; origin records the source.
// The source does not change.
func (s *Space) Derive(b *Base, id, typ, title string, now time.Time) (*Entity, error) {
	src := s.entity(id)
	if src == nil {
		return nil, fmt.Errorf("没有记录「%s」", id)
	}
	dst, ok := s.typeByKey(typ)
	if !ok {
		return nil, fmt.Errorf("没有类型「%s」", typ)
	}
	srcType, ok := s.typeByKey(src.Type)
	if !ok {
		return nil, fmt.Errorf("记录「%s」的类型「%s」不存在", src.ID, src.Type)
	}
	e := &Entity{Type: dst.Key, Title: src.Title, Origin: src.ID, Created: now, Updated: now}
	if t := strings.TrimSpace(title); t != "" {
		e.Title = t
	}
	if len(dst.Flow) > 0 {
		e.Status = dst.Flow[0].Name
	}
	vals := map[string]any{}
	for i := range dst.Props {
		p := dst.Props[i]
		if _, ok := declaredProp(srcType, p.Key); !ok {
			continue
		}
		raw, ok := src.Values[p.Key]
		if !ok {
			continue
		}
		k, _ := KindOf(p.Kind)
		if k.Eval != nil {
			continue
		}
		v, err := k.Parse(&p, raw)
		if err != nil || v == nil {
			continue
		}
		if k.Refs != nil && !s.allOfType(k.Refs(v), p.Target) {
			continue
		}
		vals[p.Key] = v
	}
	e.Values = tidy(vals)
	seq := b.Seq + 1
	e.ID = entityID(seq)
	ns := *s
	ns.Entities = append(slices.Clone(s.Entities), e)
	if err := s.commit(&ns); err != nil {
		return nil, err
	}
	b.Seq = seq
	return e, nil
}

// Drop removes an entity. Every relation that held it loses its id (a relation left empty is unset); origins that
// point to it are kept, since ids are never reused. It returns how many links were removed.
func (s *Space) Drop(id string) (unlinked int, err error) {
	i := s.entityIndex(id)
	if i < 0 {
		return 0, fmt.Errorf("没有记录「%s」", id)
	}
	ns := *s
	ns.Entities = make([]*Entity, 0, len(s.Entities))
	for j, e := range s.Entities {
		if j == i {
			continue
		}
		ne, n := s.unlinkEntity(e, id)
		unlinked += n
		ns.Entities = append(ns.Entities, ne)
	}
	if err := s.commit(&ns); err != nil {
		return 0, err
	}
	return unlinked, nil
}

// unlinkEntity removes an id from every relation value of the entity. It returns the entity itself when nothing held
// the id.
func (s *Space) unlinkEntity(e *Entity, id string) (*Entity, int) {
	if e == nil {
		return e, 0
	}
	t, ok := s.typeByKey(e.Type)
	if !ok {
		return e, 0
	}
	var vals map[string]any
	n := 0
	for key, v := range e.Values {
		p, ok := declaredProp(t, key)
		if !ok {
			continue
		}
		k, _ := KindOf(p.Kind)
		if k.Refs == nil {
			continue
		}
		ids := k.Refs(v)
		if !slices.Contains(ids, id) {
			continue
		}
		if vals == nil {
			vals = make(map[string]any, len(e.Values))
			for kk, vv := range e.Values {
				vals[kk] = vv
			}
		}
		n++
		rest := slices.DeleteFunc(slices.Clone(ids), func(x string) bool { return x == id })
		if len(rest) == 0 {
			delete(vals, key)
		} else {
			vals[key] = rest
		}
	}
	if vals == nil {
		return e, 0
	}
	ne := *e
	ne.Values = tidy(vals)
	return &ne, n
}

// migrate maps the entities of a type to its new definition and returns the new entity list; the old entities are
// not changed. Every value goes through the new Parse. Stage names and option names are carried over by name, or by
// position when the old and new lists have the same length. Anything that cannot follow fails the change, and the
// failure names the entity.
func migrate(s *Space, old, nt *Type, now time.Time) ([]*Entity, error) {
	stages := renames(stageNames(old.Flow), stageNames(nt.Flow))
	byID := make(map[string]*Entity, len(s.Entities))
	for _, e := range s.Entities {
		if e != nil {
			byID[e.ID] = e
		}
	}
	out := make([]*Entity, len(s.Entities))
	for i, e := range s.Entities {
		out[i] = e
		if e == nil || e.Type != old.Key {
			continue
		}
		ne, err := migrateEntity(e, old, nt, stages, byID)
		if err != nil {
			return nil, err
		}
		if ne != nil {
			ne.Updated = now
			out[i] = ne
		}
	}
	return out, nil
}

// migrateEntity returns the migrated copy of an entity, or nil when nothing about it changes.
func migrateEntity(e *Entity, old, nt *Type, stages map[string]string, byID map[string]*Entity) (*Entity, error) {
	status, err := migrateStatus(e, nt, stages)
	if err != nil {
		return nil, err
	}
	vals, changed, err := migrateValues(e, old, nt, byID)
	if err != nil {
		return nil, err
	}
	if !changed && status == e.Status {
		return nil, nil
	}
	ne := *e
	ne.Status = status
	ne.Values = vals
	return &ne, nil
}

// migrateStatus maps the status of an entity. An entity with no status gets the first stage of a newly added flow.
func migrateStatus(e *Entity, nt *Type, stages map[string]string) (string, error) {
	if e.Status == "" {
		if len(nt.Flow) > 0 {
			return nt.Flow[0].Name, nil
		}
		return "", nil
	}
	to, ok := stages[e.Status]
	if !ok {
		return "", fmt.Errorf("「%s」正处于阶段「%s」，新的流程里没有这个阶段", e.Title, e.Status)
	}
	return to, nil
}

// migrateValues maps the stored values of an entity. It returns the values to store and whether they differ from the
// old ones; a changed map is a copy.
func migrateValues(e *Entity, old, nt *Type, byID map[string]*Entity) (map[string]any, bool, error) {
	vals := e.Values
	copied := false
	for _, key := range sortedKeys(e.Values) {
		raw := e.Values[key]
		var canon any
		p, declared := declaredProp(nt, key)
		if declared {
			c, err := migrateValue(e, old, &p, key, raw, byID)
			if err != nil {
				return nil, false, err
			}
			canon = c
		}
		if canon != nil {
			k, _ := KindOf(p.Kind)
			if k.Cmp(canon, raw) == 0 {
				continue
			}
		}
		if !copied {
			vals = make(map[string]any, len(e.Values))
			for kk, vv := range e.Values {
				vals[kk] = vv
			}
			copied = true
		}
		if canon == nil {
			delete(vals, key)
		} else {
			vals[key] = canon
		}
	}
	if !copied {
		return e.Values, false, nil
	}
	return tidy(vals), true, nil
}

// migrateValue parses one stored value against the new property. An option renamed in the migration is carried over
// first.
func migrateValue(e *Entity, old *Type, p *Prop, key string, raw any, byID map[string]*Entity) (any, error) {
	k, ok := KindOf(p.Kind)
	if !ok {
		return nil, fmt.Errorf("属性「%s」的类型「%s」不存在", p.Name, p.Kind)
	}
	if k.Eval != nil {
		return nil, fmt.Errorf("「%s」的 %s 不符合新结构：汇总不能存储值", e.Title, p.Name)
	}
	value := raw
	if op, ok := declaredProp(old, key); ok {
		if str, isStr := value.(string); isStr {
			if to, found := renames(op.Options, p.Options)[str]; found {
				value = to
			}
		}
	}
	canon, err := k.Parse(p, value)
	if err != nil {
		return nil, fmt.Errorf("「%s」的 %s 不符合新结构：%w", e.Title, p.Name, err)
	}
	if k.Refs != nil {
		for _, id := range k.Refs(canon) {
			if t := byID[id]; t == nil || t.Type != p.Target {
				return nil, fmt.Errorf("「%s」的 %s 不符合新结构：关联的记录不存在或类型不对", e.Title, p.Name)
			}
		}
	}
	return canon, nil
}

// migrateViews carries the stage and option renames of a type replacement over to the view conditions that name a
// stage (the status field) or an option. A condition whose value has no successor is left as it is, and Check then
// refuses it.
func migrateViews(old, nt *Type) []View {
	if len(nt.Views) == 0 {
		return nt.Views
	}
	stages := renames(stageNames(old.Flow), stageNames(nt.Flow))
	views := make([]View, len(nt.Views))
	for i, v := range nt.Views {
		v.Where = slices.Clone(v.Where)
		for j := range v.Where {
			c := &v.Where[j]
			m := stages
			if c.Field != "status" {
				m = nil
				op, okOld := declaredProp(old, c.Field)
				np, okNew := declaredProp(nt, c.Field)
				if okOld && okNew {
					m = renames(op.Options, np.Options)
				}
			}
			if str, ok := scalar(c.Value); ok {
				if to, found := m[str]; found {
					c.Value = to
				}
			}
		}
		views[i] = v
	}
	return views
}

// renames maps old names to new ones. A name still in the new list maps to itself. A name that is gone maps to the
// name at the same position, when both lists have the same length. A name with no successor has no entry.
func renames(old, nxt []string) map[string]string {
	m := make(map[string]string, len(old))
	for i, o := range old {
		switch {
		case slices.Contains(nxt, o):
			m[o] = o
		case len(old) == len(nxt):
			m[o] = nxt[i]
		}
	}
	return m
}

func stageNames(flow []Stage) []string {
	out := make([]string, 0, len(flow))
	for _, st := range flow {
		out = append(out, st.Name)
	}
	return out
}
