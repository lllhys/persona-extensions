package kernel

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Limits. The type limit is 7 so that a space's types plus its overview always fit one tab row (8); it is the one
// limit chosen with the page in mind.
const (
	MaxSpaces   = 50
	MaxTypes    = 7
	MaxProps    = 24
	MaxStages   = 8
	MaxOptions  = 50
	MaxViews    = 5
	MaxPills    = 4
	MaxEntities = 5000
	MaxTargets  = 100
	MaxTitle    = 200
	MaxText     = 2000
	MaxName     = 40
	MaxShort    = 12
	MaxAbout    = 200
	MaxWhere    = 8
	MaxSort     = 3
	MaxAgg      = 4
)

// viewKinds are the five view kinds, in the order the vocabulary lists them.
var viewKinds = []string{"list", "board", "table", "progress", "feed"}

// Base is everything: the whole file.
type Base struct {
	Format int      `json:"format"` // 1
	Seq    uint64   `json:"seq"`    // last id issued; ids are never reused
	Spaces []*Space `json:"spaces"`
}

type Space struct {
	ID       string    `json:"id"`              // ^[a-z0-9][a-z0-9-]{0,31}$, given or "s"+base36(seq)
	Name     string    `json:"name"`            // 1..40 runes
	Icon     string    `json:"icon,omitempty"`  // an icon name; opaque to the kernel
	About    string    `json:"about,omitempty"` // <= 200 runes
	Types    []*Type   `json:"types"`
	Entities []*Entity `json:"entities"`
}

type Type struct {
	Key   string  `json:"key"`             // ^[a-z][a-z0-9_]{0,31}$, unique in the space
	Name  string  `json:"name"`            // 1..40 runes
	Icon  string  `json:"icon,omitempty"`  // an icon name; opaque to the kernel
	Flow  []Stage `json:"flow,omitempty"`  // none, or 2..8 stages
	Props []Prop  `json:"props,omitempty"` // <= 24, order is display order
	Card  Card    `json:"card,omitzero"`
	Views []View  `json:"views,omitempty"` // <= 5; Views[0] is the default
}

type Phase string

const (
	Todo    Phase = "todo"
	Doing   Phase = "doing"
	Done    Phase = "done"
	Dropped Phase = "dropped"
)

type Stage struct {
	Name  string `json:"name"` // 1..12 runes, unique in the flow; the stored status value
	Phase Phase  `json:"phase"`
}

// Prop is one property definition. Kind selects a row of the descriptor table; that row's Opts says which of the
// option fields below may be set. A field outside Opts must be zero (Check refuses it).
type Prop struct {
	Key     string   `json:"key"`               // ^[a-z][a-z0-9_]{0,31}$, not a built-in field name
	Name    string   `json:"name"`              // 1..40 runes
	Kind    string   `json:"kind"`              // a row of Kinds
	Unit    string   `json:"unit,omitempty"`    // number, rollup: a suffix such as "页"
	Options []string `json:"options,omitempty"` // enum: 1..50 distinct labels
	Target  string   `json:"target,omitempty"`  // relation: a type key of the same space
	Many    bool     `json:"many,omitempty"`    // relation: more than one target
	Back    string   `json:"back,omitempty"`    // relation: what the backlinks are called on the target
	Via     string   `json:"via,omitempty"`     // rollup: "prop" (own relation) or "type.prop" (its backlinks)
	Op      string   `json:"op,omitempty"`      // rollup: an aggregate name
	Of      string   `json:"of,omitempty"`      // rollup: a numeric stored property of the related type
}

// Card is how one entity of the type is projected into a row, everywhere.
type Card struct {
	Lead     string   `json:"lead,omitempty"`     // field shown as the subtitle
	Pills    []string `json:"pills,omitempty"`    // <= 4 fields shown as compact metadata
	Progress *Ratio   `json:"progress,omitempty"` // the progress source
}

// Ratio is Of/Over; with no Over, Of is already a ratio in 0..1 (a percent_done rollup).
type Ratio struct {
	Of   string `json:"of"`
	Over string `json:"over,omitempty"`
}

// Lens is the part of a query a view keeps.
type Lens struct {
	Where []Cond  `json:"where,omitempty"` // all must hold; <= 8
	Sort  []Order `json:"sort,omitempty"`  // <= 3
	Group string  `json:"group,omitempty"` // a field
	Agg   []Agg   `json:"agg,omitempty"`   // <= 4
}

type View struct {
	Name string `json:"name"` // 1..12 runes, unique in the type
	Kind string `json:"kind"` // list | board | table | progress | feed
	Lens
}

// Cond is one condition of a Lens or a Query: Field Op Value.
type Cond struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
}

// Order is one sort key of a Lens or a Query.
type Order struct {
	By   string `json:"by"`
	Desc bool   `json:"desc,omitempty"`
}

// Agg is one aggregate of a Lens or a Query.
type Agg struct {
	Op string `json:"op"`
	Of string `json:"of,omitempty"`
}

type Entity struct {
	ID      string         `json:"id"` // "e"+base36(seq); unique in the Base
	Type    string         `json:"type"`
	Title   string         `json:"title"`            // 1..200 runes, trimmed
	Status  string         `json:"status,omitempty"` // a Stage.Name; empty exactly when the type has no flow
	Values  map[string]any `json:"values,omitempty"` // prop key -> canonical value; no entry = unset
	Origin  string         `json:"origin,omitempty"` // the entity this one was derived from
	Created time.Time      `json:"created"`
	Updated time.Time      `json:"updated"`
}

// Link is derived, never stored.
type Link struct{ From, Prop, To string }

// builtinProps describes the built-in fields. Their values are supplied by builtins (world.go).
var builtinProps = map[string]func(s *Space, t *Type) Prop{
	"title": func(*Space, *Type) Prop {
		return Prop{Key: "title", Name: "标题", Kind: KindText}
	},
	"status": func(_ *Space, t *Type) Prop {
		var opts []string
		for _, st := range t.Flow {
			opts = append(opts, st.Name)
		}
		return Prop{Key: "status", Name: "状态", Kind: KindEnum, Options: opts}
	},
	"phase": func(*Space, *Type) Prop {
		return Prop{Key: "phase", Name: "阶段", Kind: KindEnum,
			Options: []string{string(Todo), string(Doing), string(Done), string(Dropped)}}
	},
	"type": func(s *Space, _ *Type) Prop {
		var opts []string
		for _, t := range s.Types {
			opts = append(opts, t.Key)
		}
		return Prop{Key: "type", Name: "类型", Kind: KindEnum, Options: opts}
	},
	"origin": func(*Space, *Type) Prop {
		return Prop{Key: "origin", Name: "源自", Kind: KindRelation}
	},
	"created": func(*Space, *Type) Prop {
		return Prop{Key: "created", Name: "创建", Kind: KindDate}
	},
	"updated": func(*Space, *Type) Prop {
		return Prop{Key: "updated", Name: "更新", Kind: KindDate}
	},
}

// Field answers a declared property or a built-in one. Built-in fields are synthetic Props, so query, grouping,
// sorting, cards and tables treat them exactly like declared properties.
func (s *Space) Field(t *Type, key string) (Prop, bool) {
	if t == nil {
		return Prop{}, false
	}
	if mk, ok := builtinProps[key]; ok {
		return mk(s, t), true
	}
	for i := range t.Props {
		if t.Props[i].Key == key {
			return t.Props[i], true
		}
	}
	return Prop{}, false
}

// typeByKey finds a type of the space by its key.
func (s *Space) typeByKey(key string) (*Type, bool) {
	for _, t := range s.Types {
		if t != nil && t.Key == key {
			return t, true
		}
	}
	return nil, false
}

// CardOrDefault fills every unset part of the card: Lead is the first text property; Pills are the first three
// other properties; Progress is the first percent_done rollup.
func (t *Type) CardOrDefault() Card {
	c := t.Card
	if c.Lead == "" {
		for i := range t.Props {
			if isTextProp(&t.Props[i]) {
				c.Lead = t.Props[i].Key
				break
			}
		}
	}
	if len(c.Pills) == 0 {
		for i := range t.Props {
			if !isTextProp(&t.Props[i]) && len(c.Pills) < 3 {
				c.Pills = append(c.Pills, t.Props[i].Key)
			}
		}
	}
	if c.Progress == nil {
		for i := range t.Props {
			if isPercentRollup(&t.Props[i]) {
				c.Progress = &Ratio{Of: t.Props[i].Key}
				break
			}
		}
	}
	return c
}

// ViewsOrDefault is the declared views, or one list when none is declared.
func (t *Type) ViewsOrDefault() []View {
	if len(t.Views) == 0 {
		return []View{{Name: "列表", Kind: "list"}}
	}
	return t.Views
}

// Columns are the keys of a table's columns: title, status (with a flow), then the properties in schema order, at
// most eight in all.
func (t *Type) Columns() []string {
	cols := []string{"title"}
	if len(t.Flow) > 0 {
		cols = append(cols, "status")
	}
	for i := range t.Props {
		if len(cols) >= 8 {
			break
		}
		cols = append(cols, t.Props[i].Key)
	}
	return cols
}

// Check verifies every invariant of the Base and returns the first violation as a Chinese sentence. It also
// normalizes every stored value in place to its canonical form (JSON arrays become []string and so on), so it runs
// on every load and after every mutation.
func (b *Base) Check() error {
	if b.Format != 1 {
		return fmt.Errorf("结构文件的格式必须是 1，实际是 %d", b.Format)
	}
	if len(b.Spaces) > MaxSpaces {
		return fmt.Errorf("空间不能超过 %d 个", MaxSpaces)
	}
	spaceIDs := map[string]bool{}
	entityIDs := map[string]bool{}
	for _, s := range b.Spaces {
		if s == nil {
			return errors.New("空间不能为空")
		}
		if !validSpaceID(s.ID) {
			return fmt.Errorf("空间编号「%s」不合法", s.ID)
		}
		if spaceIDs[s.ID] {
			return fmt.Errorf("空间编号「%s」重复", s.ID)
		}
		spaceIDs[s.ID] = true
		if err := s.check(entityIDs); err != nil {
			return err
		}
	}
	return nil
}

func (s *Space) check(entityIDs map[string]bool) error {
	if !inRunes(s.Name, 1, MaxName) {
		return fmt.Errorf("空间「%s」的名称应为 1 到 %d 个字", s.ID, MaxName)
	}
	if runeLen(s.About) > MaxAbout {
		return fmt.Errorf("空间「%s」的简介不能超过 %d 个字", s.Name, MaxAbout)
	}
	if len(s.Types) > MaxTypes {
		return fmt.Errorf("空间「%s」的类型不能超过 %d 个", s.Name, MaxTypes)
	}
	keys := map[string]bool{}
	for _, t := range s.Types {
		if t == nil {
			return fmt.Errorf("空间「%s」的类型不能为空", s.Name)
		}
		if !validKey(t.Key) {
			return fmt.Errorf("类型标识「%s」不合法", t.Key)
		}
		if keys[t.Key] {
			return fmt.Errorf("类型标识「%s」重复", t.Key)
		}
		keys[t.Key] = true
	}
	for _, t := range s.Types {
		if err := t.check(s); err != nil {
			return err
		}
	}
	return s.checkEntities(entityIDs)
}

func (t *Type) check(s *Space) error {
	if !inRunes(t.Name, 1, MaxName) {
		return fmt.Errorf("类型「%s」的名称应为 1 到 %d 个字", t.Key, MaxName)
	}
	if err := t.checkFlow(); err != nil {
		return err
	}
	if len(t.Props) > MaxProps {
		return fmt.Errorf("类型「%s」的属性不能超过 %d 个", t.Name, MaxProps)
	}
	keys := map[string]bool{}
	for i := range t.Props {
		p := &t.Props[i]
		if !validKey(p.Key) {
			return fmt.Errorf("属性标识「%s」不合法", p.Key)
		}
		if _, ok := builtinProps[p.Key]; ok {
			return fmt.Errorf("「%s」是内置字段，不能用作属性标识", p.Key)
		}
		if keys[p.Key] {
			return fmt.Errorf("属性标识「%s」重复", p.Key)
		}
		keys[p.Key] = true
		if err := p.check(t, s); err != nil {
			return err
		}
	}
	if err := s.checkCard(t); err != nil {
		return err
	}
	return s.checkViews(t)
}

// propOptionFields lists the Prop option fields and whether each is set; a kind may only set the ones in its Opts.
var propOptionFields = []struct {
	Name string
	Set  func(p *Prop) bool
}{
	{"unit", func(p *Prop) bool { return p.Unit != "" }},
	{"options", func(p *Prop) bool { return len(p.Options) > 0 }},
	{"target", func(p *Prop) bool { return p.Target != "" }},
	{"many", func(p *Prop) bool { return p.Many }},
	{"back", func(p *Prop) bool { return p.Back != "" }},
	{"via", func(p *Prop) bool { return p.Via != "" }},
	{"op", func(p *Prop) bool { return p.Op != "" }},
	{"of", func(p *Prop) bool { return p.Of != "" }},
}

func (p *Prop) check(t *Type, s *Space) error {
	if !inRunes(p.Name, 1, MaxName) {
		return fmt.Errorf("属性「%s」的名称应为 1 到 %d 个字", p.Key, MaxName)
	}
	k, ok := KindOf(p.Kind)
	if !ok {
		return fmt.Errorf("属性「%s」的类型「%s」不存在", p.Name, p.Kind)
	}
	for _, o := range propOptionFields {
		if !slices.Contains(k.Opts, o.Name) && o.Set(p) {
			return fmt.Errorf("「%s」类型的属性「%s」不能设置「%s」", k.Label, p.Name, o.Name)
		}
	}
	return k.Check(p, t, s)
}

// checkField refuses a reference to a property that does not resolve on the type.
func (s *Space) checkField(t *Type, key string) error {
	if _, ok := s.Field(t, key); !ok {
		return fmt.Errorf("类型「%s」没有属性「%s」", t.Name, key)
	}
	return nil
}

// checkNumeric refuses a reference that does not resolve or is not a numeric reading.
func (s *Space) checkNumeric(t *Type, key string) error {
	p, ok := s.Field(t, key)
	if !ok {
		return fmt.Errorf("类型「%s」没有属性「%s」", t.Name, key)
	}
	if k, ok := KindOf(p.Kind); !ok || k.Num == nil {
		return fmt.Errorf("属性「%s」不是数字", p.Name)
	}
	return nil
}

func (s *Space) checkCard(t *Type) error {
	c := t.Card
	if c.Lead != "" {
		if err := s.checkField(t, c.Lead); err != nil {
			return err
		}
	}
	if len(c.Pills) > MaxPills {
		return fmt.Errorf("类型「%s」的卡片标签不能超过 %d 个", t.Name, MaxPills)
	}
	for _, key := range c.Pills {
		if err := s.checkField(t, key); err != nil {
			return err
		}
	}
	if c.Progress != nil {
		if err := s.checkNumeric(t, c.Progress.Of); err != nil {
			return err
		}
		if c.Progress.Over != "" {
			if err := s.checkNumeric(t, c.Progress.Over); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Space) checkViews(t *Type) error {
	if len(t.Views) > MaxViews {
		return fmt.Errorf("类型「%s」的视图不能超过 %d 个", t.Name, MaxViews)
	}
	names := map[string]bool{}
	for _, v := range t.Views {
		if !inRunes(v.Name, 1, MaxShort) {
			return fmt.Errorf("视图「%s」的名称应为 1 到 %d 个字", v.Name, MaxShort)
		}
		if names[v.Name] {
			return fmt.Errorf("视图「%s」重复", v.Name)
		}
		names[v.Name] = true
		if !slices.Contains(viewKinds, v.Kind) {
			return fmt.Errorf("视图「%s」的种类「%s」不存在", v.Name, v.Kind)
		}
		if err := s.checkLens(t, v.Lens); err != nil {
			return err
		}
	}
	return nil
}

// checkLens verifies a lens the way a query binds it: each condition's field, operator and argument, each sort key and
// the group field resolve on the type, and aggregate operands are numeric.
func (s *Space) checkLens(t *Type, l Lens) error {
	if len(l.Where) > MaxWhere {
		return fmt.Errorf("筛选条件不能超过 %d 个", MaxWhere)
	}
	for _, c := range l.Where {
		if _, err := bindCond(s, t, c); err != nil {
			return err
		}
	}
	if len(l.Sort) > MaxSort {
		return fmt.Errorf("排序不能超过 %d 个", MaxSort)
	}
	for _, o := range l.Sort {
		if _, _, err := bindField(s, t, o.By); err != nil {
			return err
		}
	}
	if l.Group != "" {
		if _, _, err := bindField(s, t, l.Group); err != nil {
			return err
		}
	}
	if len(l.Agg) > MaxAgg {
		return fmt.Errorf("汇总不能超过 %d 个", MaxAgg)
	}
	for _, a := range l.Agg {
		agg, ok := aggOf(a.Op)
		if !ok {
			return fmt.Errorf("汇总方式「%s」不存在", a.Op)
		}
		switch {
		case agg.Operand:
			if err := s.checkNumeric(t, a.Of); err != nil {
				return err
			}
		case a.Of != "":
			return fmt.Errorf("汇总「%s」不需要属性", agg.Label)
		}
	}
	return nil
}

func (s *Space) checkEntities(entityIDs map[string]bool) error {
	if len(s.Entities) > MaxEntities {
		return fmt.Errorf("空间「%s」的记录不能超过 %d 条", s.Name, MaxEntities)
	}
	byID := make(map[string]*Entity, len(s.Entities))
	for _, e := range s.Entities {
		if e == nil {
			return fmt.Errorf("空间「%s」的记录不能为空", s.Name)
		}
		if e.ID == "" || entityIDs[e.ID] {
			return fmt.Errorf("记录编号「%s」为空或重复", e.ID)
		}
		entityIDs[e.ID] = true
		byID[e.ID] = e
	}
	for _, e := range s.Entities {
		if err := s.checkEntity(e, byID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Space) checkEntity(e *Entity, byID map[string]*Entity) error {
	t, ok := s.typeByKey(e.Type)
	if !ok {
		return fmt.Errorf("记录「%s」的类型「%s」不存在", e.ID, e.Type)
	}
	if e.Title != strings.TrimSpace(e.Title) || !inRunes(e.Title, 1, MaxTitle) {
		return fmt.Errorf("记录「%s」的标题应为 1 到 %d 个字，且没有首尾空白", e.ID, MaxTitle)
	}
	if len(t.Flow) == 0 {
		if e.Status != "" {
			return fmt.Errorf("「%s」的类型「%s」没有状态", e.Title, t.Name)
		}
	} else if _, ok := t.Stage(e.Status); !ok {
		return fmt.Errorf("「%s」的状态「%s」不在流程中", e.Title, e.Status)
	}
	if e.Origin != "" && e.Origin == e.ID {
		return fmt.Errorf("「%s」不能源自它自己", e.Title)
	}
	return s.checkValues(e, t, byID)
}

// checkValues parses every stored value through its kind and checks each relation target.
func (s *Space) checkValues(e *Entity, t *Type, byID map[string]*Entity) error {
	keys := make([]string, 0, len(e.Values))
	for key := range e.Values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		p, ok := declaredProp(t, key)
		if !ok {
			return fmt.Errorf("「%s」没有属性「%s」；类型「%s」的属性有：%s", e.Title, key, t.Name, propKeyList(t))
		}
		k, ok := KindOf(p.Kind)
		if !ok {
			return fmt.Errorf("属性「%s」的类型「%s」不存在", p.Name, p.Kind)
		}
		if k.Eval != nil {
			return fmt.Errorf("汇总「%s」不能存储值", p.Name)
		}
		canon, err := k.Parse(&p, e.Values[key])
		if err != nil {
			return fmt.Errorf("「%s」的属性不合法：%w", e.Title, err)
		}
		if canon == nil {
			delete(e.Values, key)
			continue
		}
		e.Values[key] = canon
		if k.Refs == nil {
			continue
		}
		for _, id := range k.Refs(canon) {
			if id == e.ID {
				return fmt.Errorf("「%s」不能关联它自己", e.Title)
			}
			if target := byID[id]; target == nil || target.Type != p.Target {
				return fmt.Errorf("「%s」的属性「%s」关联的记录不存在或类型不对", e.Title, p.Name)
			}
		}
	}
	return nil
}

func declaredProp(t *Type, key string) (Prop, bool) {
	for i := range t.Props {
		if t.Props[i].Key == key {
			return t.Props[i], true
		}
	}
	return Prop{}, false
}

func propKeyList(t *Type) string {
	if len(t.Props) == 0 {
		return "（无）"
	}
	keys := make([]string, 0, len(t.Props))
	for _, p := range t.Props {
		keys = append(keys, p.Key)
	}
	return strings.Join(keys, "、")
}

// validKey is ^[a-z][a-z0-9_]{0,31}$.
func validKey(s string) bool {
	if len(s) < 1 || len(s) > 32 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}

// validSpaceID is ^[a-z0-9][a-z0-9-]{0,31}$.
func validSpaceID(s string) bool {
	if len(s) < 1 || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		lower := c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
		if i == 0 && !lower || i > 0 && !lower && c != '-' {
			return false
		}
	}
	return true
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// inRunes reports whether s has between lo and hi runes, both included.
func inRunes(s string, lo, hi int) bool {
	n := runeLen(s)
	return n >= lo && n <= hi
}
