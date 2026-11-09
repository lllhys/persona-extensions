package main

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
)

// The ontology. A Space declares its types; a TypeDef declares the fields, the statuses and the view of its
// entities; Entities hold values (entity, attribute, value) and Relations are directed edges between two entities
// of one space. Nothing else is built in: every space, type and field is one the user or the assistant defined.

// State is everything the extension keeps, in spaces_state.json.
type State struct {
	Spaces    []*Space   `json:"spaces"`
	Entities  []*Entity  `json:"entities"`
	Relations []Relation `json:"relations"`
}

type Space struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Icon        string    `json:"icon,omitempty"`
	Description string    `json:"description,omitempty"`
	Types       []TypeDef `json:"types"`
}

type TypeDef struct {
	Key      string     `json:"key"`
	Label    string     `json:"label"`
	View     string     `json:"view"`
	Fields   []FieldDef `json:"fields"`
	Statuses []string   `json:"statuses,omitempty"`
}

type FieldDef struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Kind     string   `json:"kind"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required,omitempty"`
}

type Entity struct {
	ID      string         `json:"id"`
	Space   string         `json:"space"`
	Type    string         `json:"type"`
	Title   string         `json:"title"`
	Status  string         `json:"status,omitempty"`
	Values  map[string]any `json:"values,omitempty"`
	Created time.Time      `json:"created"`
	Updated time.Time      `json:"updated"`
}

type Relation struct {
	From string `json:"from"`
	Type string `json:"type"`
	To   string `json:"to"`
}

// Link is an outgoing edge as an entity's input names it.
type Link struct {
	Type string `json:"type"`
	To   string `json:"to"`
}

var (
	fieldKinds = []string{"text", "number", "boolean", "date", "enum", "url"}
	viewKinds  = []string{"list", "board", "progress", "table", "feed"}
	keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	idPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
)

const (
	maxSpaces    = 50
	maxTypes     = 8
	maxFields    = 24
	maxStatuses  = 8
	maxOptions   = 50
	maxEntities  = 10000
	maxRelations = 20000
	maxLabel     = 40
	maxTitle     = 200
	maxText      = 2000
)

// ---- Lookups ----

// space finds a space by id; with no id it is the only space there is.
func (st *State) space(id string) (*Space, error) {
	if id == "" {
		switch len(st.Spaces) {
		case 0:
			return nil, fmt.Errorf("还没有空间：先建一个空间")
		case 1:
			return st.Spaces[0], nil
		}
		return nil, fmt.Errorf("有 %d 个空间，请指明 space_id", len(st.Spaces))
	}
	for _, sp := range st.Spaces {
		if sp.ID == id {
			return sp, nil
		}
	}
	return nil, fmt.Errorf("没有空间 %q", id)
}

func (sp *Space) typeDef(key string) *TypeDef {
	for i := range sp.Types {
		if sp.Types[i].Key == key {
			return &sp.Types[i]
		}
	}
	return nil
}

func (t *TypeDef) field(key string) *FieldDef {
	for i := range t.Fields {
		if t.Fields[i].Key == key {
			return &t.Fields[i]
		}
	}
	return nil
}

func (st *State) entity(id string) *Entity {
	for _, e := range st.Entities {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// entities are those of a space and type, the most recently changed first (a feed: the newest first).
func (st *State) entities(space string, t *TypeDef) []*Entity {
	var out []*Entity
	for _, e := range st.Entities {
		if e.Space == space && e.Type == t.Key {
			out = append(out, e)
		}
	}
	slices.SortStableFunc(out, func(a, b *Entity) int {
		if t.View == "feed" {
			return b.Created.Compare(a.Created)
		}
		return b.Updated.Compare(a.Updated)
	})
	return out
}

// links are an entity's outgoing and incoming edges.
func (st *State) links(id string) (out, in []Relation) {
	for _, r := range st.Relations {
		if r.From == id {
			out = append(out, r)
		}
		if r.To == id {
			in = append(in, r)
		}
	}
	return out, in
}

// ---- Spaces ----

// SpaceInput creates a space (an unknown or empty id) or changes one; a nil field stays as it is. A new space can
// start from a template: its types, and with Examples its example entities; a name, icon or description left out
// or empty is the template's.
type SpaceInput struct {
	ID          string  `json:"id"`
	Name        *string `json:"name"`
	Icon        *string `json:"icon"`
	Description *string `json:"description"`
	Template    string  `json:"template"`
	Examples    bool    `json:"examples"`
}

func (st *State) upsertSpace(in SpaceInput, now time.Time) (*Space, error) {
	sp, _ := st.space(in.ID)
	isNew := in.ID == "" || sp == nil
	var tpl *Template
	if in.Template != "" || in.Examples {
		if !isNew {
			return nil, fmt.Errorf("模板只在新建空间时可用")
		}
		var err error
		if tpl, err = findTemplate(cmp.Or(in.Template, "blank")); err != nil {
			return nil, err
		}
	}
	if isNew {
		if len(st.Spaces) >= maxSpaces {
			return nil, fmt.Errorf("最多 %d 个空间", maxSpaces)
		}
		id := in.ID
		if id == "" {
			id = st.newID("s")
		} else if !idPattern.MatchString(id) {
			return nil, fmt.Errorf("空间 id %q 只能用小写字母、数字、- 和 _", id)
		}
		sp = &Space{ID: id, Icon: "folder", Types: []TypeDef{}}
		if tpl != nil {
			types, err := tpl.types()
			if err != nil {
				return nil, err
			}
			sp.Name, sp.Icon, sp.Description, sp.Types = tpl.Name, tpl.Icon, tpl.Description, types
		} else if in.Name == nil {
			return nil, fmt.Errorf("新空间需要名字")
		}
	}
	given := func(s *string) bool { return s != nil && (tpl == nil || strings.TrimSpace(*s) != "") }
	next := *sp
	if given(in.Name) {
		next.Name = strings.TrimSpace(*in.Name)
	}
	if given(in.Icon) {
		next.Icon = cmp.Or(strings.TrimSpace(*in.Icon), "folder")
	}
	if given(in.Description) {
		next.Description = strings.TrimSpace(*in.Description)
	}
	switch {
	case next.Name == "" || utf8.RuneCountInString(next.Name) > maxLabel:
		return nil, fmt.Errorf("空间名要有 1 到 %d 个字", maxLabel)
	case !slices.Contains(wire.NavIcons, next.Icon):
		return nil, fmt.Errorf("图标 %q 不在可选范围内：%s", next.Icon, strings.Join(wire.NavIcons, "、"))
	case utf8.RuneCountInString(next.Description) > 500:
		return nil, fmt.Errorf("空间说明最多 500 字")
	}
	*sp = next
	if !isNew {
		return sp, nil
	}
	st.Spaces = append(st.Spaces, sp)
	if tpl != nil && in.Examples {
		// a failed change is not saved, so the space goes with its examples
		if err := tpl.seed(st, sp.ID, now); err != nil {
			return nil, err
		}
	}
	return sp, nil
}

func (st *State) deleteSpace(id string) error {
	sp, err := st.space(id)
	if err != nil {
		return err
	}
	st.Spaces = slices.DeleteFunc(st.Spaces, func(s *Space) bool { return s == sp })
	gone := map[string]bool{}
	st.Entities = slices.DeleteFunc(st.Entities, func(e *Entity) bool {
		gone[e.ID] = e.Space == sp.ID
		return gone[e.ID]
	})
	st.Relations = slices.DeleteFunc(st.Relations, func(r Relation) bool { return gone[r.From] || gone[r.To] })
	return nil
}

// ---- Types ----

// upsertType defines a type or replaces its definition. The entities it has must fit the new one: values of a
// field left out go with it, the rest are checked again, and a change any entity does not fit is refused.
func (st *State) upsertType(spaceID string, t TypeDef) (*TypeDef, error) {
	sp, err := st.space(spaceID)
	if err != nil {
		return nil, err
	}
	if err := t.check(); err != nil {
		return nil, err
	}
	old := sp.typeDef(t.Key)
	if old == nil {
		if len(sp.Types) >= maxTypes {
			return nil, fmt.Errorf("一个空间最多 %d 种类型", maxTypes)
		}
		sp.Types = append(sp.Types, t)
		return &sp.Types[len(sp.Types)-1], nil
	}
	// A status the type no longer has is refused; one it newly has starts at its first.
	ents := st.entities(sp.ID, old)
	next := make([]Entity, len(ents))
	for i, e := range ents {
		n := Entity{Status: e.Status, Values: map[string]any{}}
		if len(t.Statuses) == 0 {
			n.Status = ""
		} else if n.Status == "" {
			n.Status = t.Statuses[0]
		}
		for k, v := range e.Values {
			if f := t.field(k); f != nil {
				if n.Values[k], err = f.normalize(plain(v)); err != nil {
					return nil, fmt.Errorf("「%s」不符合新结构：%v", e.Title, err)
				}
			}
		}
		if err := t.complete(n.Status, n.Values); err != nil {
			return nil, fmt.Errorf("「%s」不符合新结构：%v", e.Title, err)
		}
		next[i] = n
	}
	for i, e := range ents {
		e.Status, e.Values = next[i].Status, next[i].Values
	}
	*old = t
	return old, nil
}

// deleteType removes a type that has no entities.
func (st *State) deleteType(spaceID, key string) error {
	sp, err := st.space(spaceID)
	if err != nil {
		return err
	}
	t := sp.typeDef(key)
	if t == nil {
		return fmt.Errorf("没有类型 %q", key)
	}
	if n := len(st.entities(sp.ID, t)); n > 0 {
		return fmt.Errorf("「%s」还有 %d 条记录，先删掉它们", t.Label, n)
	}
	sp.Types = slices.DeleteFunc(sp.Types, func(d TypeDef) bool { return d.Key == key })
	return nil
}

// check holds a definition to the rules and fills its defaults (label from key, view list).
func (t *TypeDef) check() error {
	if !keyPattern.MatchString(t.Key) {
		return fmt.Errorf("类型 key %q 要用小写字母开头的 snake_case", t.Key)
	}
	t.Label, t.View = cmp.Or(strings.TrimSpace(t.Label), t.Key), cmp.Or(t.View, "list")
	if utf8.RuneCountInString(t.Label) > maxLabel {
		return fmt.Errorf("类型名最多 %d 字", maxLabel)
	}
	if !slices.Contains(viewKinds, t.View) {
		return fmt.Errorf("视图 %q 不是 %s 之一", t.View, strings.Join(viewKinds, "、"))
	}
	if t.Fields == nil {
		t.Fields = []FieldDef{}
	}
	if len(t.Fields) > maxFields {
		return fmt.Errorf("一种类型最多 %d 个字段", maxFields)
	}
	var err error
	if t.Statuses, err = names("状态", t.Statuses, maxStatuses); err != nil {
		return err
	}
	seen := map[string]bool{}
	numbers := 0
	for i := range t.Fields {
		f := &t.Fields[i]
		f.Label = cmp.Or(strings.TrimSpace(f.Label), f.Key)
		switch {
		case !keyPattern.MatchString(f.Key):
			return fmt.Errorf("字段 key %q 要用小写字母开头的 snake_case", f.Key)
		case seen[f.Key]:
			return fmt.Errorf("字段 %q 出现了两次", f.Key)
		case utf8.RuneCountInString(f.Label) > maxLabel:
			return fmt.Errorf("字段名最多 %d 字", maxLabel)
		case !slices.Contains(fieldKinds, f.Kind):
			return fmt.Errorf("字段 %s 的类型 %q 不是 %s 之一", f.Key, f.Kind, strings.Join(fieldKinds, "、"))
		case f.Kind == "enum" && len(f.Options) == 0:
			return fmt.Errorf("enum 字段 %s 需要选项", f.Key)
		case f.Kind != "enum" && len(f.Options) > 0:
			return fmt.Errorf("只有 enum 字段有选项（%s 是 %s）", f.Key, f.Kind)
		}
		if f.Options, err = names("选项", f.Options, maxOptions); err != nil {
			return err
		}
		seen[f.Key] = true
		if f.Kind == "number" {
			numbers++
		}
	}
	switch {
	case utf8.RuneCountInString(formatFields(t.Fields)) > maxText:
		return fmt.Errorf("字段定义太长，页面的表单放不下：请精简字段或选项")
	case t.View == "board" && len(t.Statuses) == 0:
		return fmt.Errorf("看板视图按状态分列，需要 statuses")
	case t.View == "progress" && numbers == 0:
		return fmt.Errorf("进度视图需要一个 number 字段（第一个是当前值，第二个是目标值）")
	}
	return nil
}

// names trims a list of names and holds it to unique, short and at most n.
func names(what string, in []string, n int) ([]string, error) {
	if len(in) > n {
		return nil, fmt.Errorf("%s最多 %d 个", what, n)
	}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		switch {
		case s == "" || utf8.RuneCountInString(s) > maxLabel:
			return nil, fmt.Errorf("%s要有 1 到 %d 个字", what, maxLabel)
		case slices.Contains(out, s):
			return nil, fmt.Errorf("%s「%s」重复了", what, s)
		}
		out = append(out, s)
	}
	return out, nil
}

// complete checks what a whole entity holds besides its values' kinds: the status and the required fields.
func (t *TypeDef) complete(status string, vals map[string]any) error {
	switch {
	case len(t.Statuses) == 0 && status != "":
		return fmt.Errorf("「%s」没有状态", t.Label)
	case len(t.Statuses) > 0 && !slices.Contains(t.Statuses, status):
		return fmt.Errorf("状态 %q 不是 %s 之一", status, strings.Join(t.Statuses, "、"))
	}
	for _, f := range t.Fields {
		if _, ok := vals[f.Key]; f.Required && !ok {
			return fmt.Errorf("缺少必填字段 %s（%s）", f.Key, f.Label)
		}
	}
	return nil
}

// normalize is a value as the field's kind holds it; a string is read by the kind (a form sends only strings), any
// other JSON type must already be the kind's. A text field keeps "007" as it is.
func (f *FieldDef) normalize(v any) (any, error) {
	bad := func() (any, error) {
		raw, _ := json.Marshal(v)
		return nil, fmt.Errorf("字段 %s（%s）要 %s，收到 %s", f.Key, f.Label, kindNames[f.Kind], clip(string(raw), 60))
	}
	s, isString := v.(string)
	s = strings.TrimSpace(s)
	switch f.Kind {
	case "text":
		if !isString {
			return bad()
		}
		if utf8.RuneCountInString(s) > maxText {
			return nil, fmt.Errorf("字段 %s 最多 %d 字", f.Key, maxText)
		}
		return s, nil
	case "number":
		n, ok := v.(float64)
		if isString {
			var err error
			n, err = strconv.ParseFloat(s, 64)
			ok = err == nil
		}
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return bad()
		}
		return n, nil
	case "boolean":
		b, ok := v.(bool)
		if isString {
			var err error
			b, err = strconv.ParseBool(s)
			ok = err == nil
		}
		if !ok {
			return bad()
		}
		return b, nil
	case "date":
		if _, err := time.Parse(time.DateOnly, s); !isString || err != nil {
			return bad()
		}
		return s, nil
	case "enum":
		if !isString || !slices.Contains(f.Options, s) {
			return nil, fmt.Errorf("字段 %s（%s）只能是 %s", f.Key, f.Label, strings.Join(f.Options, "、"))
		}
		return s, nil
	case "url":
		u, err := url.Parse(s)
		if !isString || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(s) > maxText {
			return bad()
		}
		return s, nil
	}
	return bad()
}

var kindNames = map[string]string{"text": "文本", "number": "数字", "boolean": "是或否", "date": "YYYY-MM-DD 日期",
	"enum": "选项", "url": "http(s) 链接"}

// ---- Entities ----

// EntityInput creates an entity (no id) or changes one. Values merge into what it holds: null or "" removes one.
// Links, when given, are all of its outgoing edges.
type EntityInput struct {
	Space  string         `json:"space_id"`
	ID     string         `json:"id"`
	Type   string         `json:"type"`
	Title  *string        `json:"title"`
	Status string         `json:"status"`
	Values map[string]any `json:"values"`
	Links  []Link         `json:"relations"`
}

func (st *State) upsertEntity(in EntityInput, now time.Time) (*Entity, error) {
	e := &Entity{}
	if in.ID != "" {
		old := st.entity(in.ID)
		switch {
		case old == nil:
			return nil, fmt.Errorf("没有记录 %q", in.ID)
		case in.Space != "" && in.Space != old.Space, in.Type != "" && in.Type != old.Type:
			return nil, fmt.Errorf("记录 %s 属于 %s/%s，不能换空间或类型", old.ID, old.Space, old.Type)
		}
		*e = *old
		if e.Values = maps.Clone(old.Values); e.Values == nil {
			e.Values = map[string]any{}
		}
	} else {
		sp, err := st.space(in.Space)
		if err != nil {
			return nil, err
		}
		if len(st.Entities) >= maxEntities {
			return nil, fmt.Errorf("最多 %d 条记录", maxEntities)
		}
		e.Space, e.Type, e.Created, e.Values = sp.ID, in.Type, now, map[string]any{}
		if in.Title == nil {
			return nil, fmt.Errorf("新记录需要标题")
		}
	}
	sp, _ := st.space(e.Space)
	t := sp.typeDef(e.Type)
	if t == nil {
		keys := make([]string, len(sp.Types))
		for i, d := range sp.Types {
			keys[i] = d.Key
		}
		return nil, fmt.Errorf("空间「%s」没有类型 %q（现有：%s）", sp.Name, e.Type, strings.Join(keys, "、"))
	}
	if in.Title != nil {
		e.Title = strings.TrimSpace(*in.Title)
	}
	if e.Title == "" || utf8.RuneCountInString(e.Title) > maxTitle {
		return nil, fmt.Errorf("标题要有 1 到 %d 个字", maxTitle)
	}
	if in.Status != "" {
		e.Status = in.Status
	} else if e.Status == "" && len(t.Statuses) > 0 {
		e.Status = t.Statuses[0]
	}
	for k, v := range in.Values {
		f := t.field(k)
		if f == nil {
			keys := make([]string, len(t.Fields))
			for i, d := range t.Fields {
				keys[i] = d.Key
			}
			return nil, fmt.Errorf("「%s」没有字段 %q（现有：%s）", t.Label, k, strings.Join(keys, "、"))
		}
		if s, ok := v.(string); v == nil || ok && strings.TrimSpace(s) == "" {
			delete(e.Values, k)
			continue
		}
		var err error
		if e.Values[k], err = f.normalize(v); err != nil {
			return nil, err
		}
	}
	if err := t.complete(e.Status, e.Values); err != nil {
		return nil, err
	}
	var edges []Relation
	if in.Links != nil {
		if in.ID == "" {
			e.ID = st.newID("e")
		}
		for _, l := range in.Links {
			r := Relation{From: e.ID, Type: strings.TrimSpace(l.Type), To: l.To}
			other := st.entity(r.To)
			switch {
			case r.Type == "" || utf8.RuneCountInString(r.Type) > maxLabel:
				return nil, fmt.Errorf("关系名要有 1 到 %d 个字", maxLabel)
			case other == nil || other.Space != e.Space:
				return nil, fmt.Errorf("关系的另一端 %q 不是这个空间里的记录", r.To)
			case r.To == e.ID:
				return nil, fmt.Errorf("记录不能和自己建立关系")
			}
			if !slices.Contains(edges, r) {
				edges = append(edges, r)
			}
		}
		if kept := len(st.Relations) - len(st.outgoing(e.ID)); kept+len(edges) > maxRelations {
			return nil, fmt.Errorf("最多 %d 条关系", maxRelations)
		}
	}
	e.Updated = now
	if in.ID == "" {
		if e.ID == "" {
			e.ID = st.newID("e")
		}
		st.Entities = append(st.Entities, e)
	} else {
		*st.entity(in.ID) = *e
		e = st.entity(in.ID)
	}
	if in.Links != nil {
		st.Relations = append(slices.DeleteFunc(st.Relations, func(r Relation) bool { return r.From == e.ID }), edges...)
	}
	return e, nil
}

func (st *State) outgoing(id string) []Relation {
	out, _ := st.links(id)
	return out
}

// deleteEntity removes an entity and every edge it has; it says how many edges went.
func (st *State) deleteEntity(id string) (int, error) {
	if st.entity(id) == nil {
		return 0, fmt.Errorf("没有记录 %q", id)
	}
	st.Entities = slices.DeleteFunc(st.Entities, func(e *Entity) bool { return e.ID == id })
	n := len(st.Relations)
	st.Relations = slices.DeleteFunc(st.Relations, func(r Relation) bool { return r.From == id || r.To == id })
	return n - len(st.Relations), nil
}

// ---- Queries ----

// Query selects the entities of a space: of a type, with a status, holding the given values exactly, or with text
// that contains the query.
type Query struct {
	Space  string         `json:"space_id"`
	Type   string         `json:"type"`
	ID     string         `json:"id"`
	Text   string         `json:"query"`
	Status string         `json:"status"`
	Where  map[string]any `json:"where"`
	Limit  int            `json:"limit"`
}

// query answers the entities that match, at most limit (default 50), and how many matched.
func (st *State) query(q Query) ([]*Entity, int, error) {
	if q.ID != "" {
		e := st.entity(q.ID)
		if e == nil {
			return nil, 0, fmt.Errorf("没有记录 %q", q.ID)
		}
		return []*Entity{e}, 1, nil
	}
	sp, err := st.space(q.Space)
	if err != nil {
		return nil, 0, err
	}
	types := sp.Types
	if q.Type != "" {
		t := sp.typeDef(q.Type)
		if t == nil {
			return nil, 0, fmt.Errorf("空间「%s」没有类型 %q", sp.Name, q.Type)
		}
		types = []TypeDef{*t}
	}
	where := map[string]any{}
	if len(q.Where) > 0 && q.Type == "" {
		return nil, 0, fmt.Errorf("where 按字段筛选，需要同时给 type")
	}
	for k, v := range q.Where {
		f := types[0].field(k)
		if f == nil {
			return nil, 0, fmt.Errorf("「%s」没有字段 %q", types[0].Label, k)
		}
		if where[k], err = f.normalize(v); err != nil {
			return nil, 0, err
		}
	}
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []*Entity
	total := 0
	for i := range types {
		for _, e := range st.entities(sp.ID, &types[i]) {
			if (q.Status != "" && e.Status != q.Status) || !e.contains(q.Text) || !e.holds(where) {
				continue
			}
			if total++; len(out) < limit {
				out = append(out, e)
			}
		}
	}
	return out, total, nil
}

func (e *Entity) contains(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" || strings.Contains(strings.ToLower(e.Title), text) {
		return true
	}
	for _, v := range e.Values {
		if s, ok := v.(string); ok && strings.Contains(strings.ToLower(s), text) {
			return true
		}
	}
	return false
}

func (e *Entity) holds(where map[string]any) bool {
	for k, v := range where {
		if e.Values[k] != v {
			return false
		}
	}
	return true
}

// ---- Helpers ----

// formatFields writes fields the way a form edits them: key:名称:kind, :选项|选项 for an enum, a trailing ! for a
// required field, separated by ; (parseFields reads them back).
func formatFields(fields []FieldDef) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		s := f.Key + ":" + f.Label + ":" + f.Kind
		if len(f.Options) > 0 {
			s += ":" + strings.Join(f.Options, "|")
		}
		if f.Required {
			s += "!"
		}
		parts[i] = s
	}
	return strings.Join(parts, "; ")
}

func parseFields(spec string) ([]FieldDef, error) {
	out := []FieldDef{}
	for _, item := range split(spec, ";；\n") {
		f := FieldDef{}
		if strings.HasSuffix(item, "!") {
			f.Required, item = true, strings.TrimSpace(strings.TrimSuffix(item, "!"))
		}
		parts := strings.Split(strings.ReplaceAll(item, "：", ":"), ":")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		switch len(parts) {
		case 2:
			f.Key, f.Kind = parts[0], parts[1]
		case 3, 4:
			f.Key, f.Label, f.Kind = parts[0], parts[1], parts[2]
			if len(parts) == 4 {
				f.Options = split(parts[3], "|｜")
			}
		default:
			return nil, fmt.Errorf("「%s」要写成 key:名称:类型", item)
		}
		out = append(out, f)
	}
	return out, nil
}

// split cuts s at any of seps and drops the empty pieces.
func split(s, seps string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return strings.ContainsRune(seps, r) }) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (st *State) newID(prefix string) string {
	for {
		var b [4]byte
		_, _ = rand.Read(b[:])
		id := prefix + "_" + hex.EncodeToString(b[:])
		if _, err := st.space(id); st.entity(id) == nil && err != nil {
			return id
		}
	}
}

// plain is a value as text: what a form shows, and what a changed field reads again by its new kind.
func plain(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return fmt.Sprint(v)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
