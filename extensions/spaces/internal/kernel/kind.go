package kernel

import (
	"cmp"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Input is the abstract form control of a kind; ui/form.go maps it one to one to a field kind.
type Input string

const (
	InLine   Input = "line"
	InNumber Input = "number"
	InToggle Input = "toggle"
	InChoice Input = "choice" // choices = Enum(p)
	InPick   Input = "pick"   // choices = entities of p.Target
	InNone   Input = ""       // computed: no input
)

// The names of the property kinds, the values stored in Prop.Kind.
const (
	KindText     = "text"
	KindNumber   = "number"
	KindBoolean  = "boolean"
	KindDate     = "date"
	KindEnum     = "enum"
	KindURL      = "url"
	KindRelation = "relation"
	KindRollup   = "rollup"
)

// Kind is one row of the descriptor table. Consumers read the row; nothing outside this file switches on a name.
type Kind struct {
	Name  string // stored in Prop.Kind
	Label string // "文本": UI and tool descriptions
	Hint  string // placeholder and the format note in tool descriptions
	Icon  string // an icon name for facts and schema rows
	Input Input
	Opts  []string // the Prop option fields this kind reads: unit options target many back via op of

	Check  func(p *Prop, t *Type, s *Space) error                   // are the options coherent
	Parse  func(p *Prop, raw any) (any, error)                      // any JSON value or form string -> canonical; ""/nil -> nil
	Eval   func(w *World, e *Entity, p *Prop) any                   // nil = stored (read Values[p.Key]); else computed
	Text   func(p *Prop, v any, name func(id string) string) string // canonical -> display text
	Cmp    func(a, b any) int                                       // order of two canonical values
	Num    func(v any) (float64, bool)                              // numeric reading; nil = not numeric
	Refs   func(v any) []string                                     // entity ids inside the value; nil = none
	Enum   func(p *Prop) []string                                   // enumerable values in order; nil = open
	Schema func(p *Prop) map[string]any                             // JSON Schema of one value
}

// Kinds is the descriptor table: eight rows, one per property kind. The rollup row is the one that is computed.
var Kinds = []*Kind{
	{
		Name: KindText, Label: "文本", Icon: "doc", Input: InLine,
		Check: noCheck, Parse: parseText, Text: textText, Cmp: cmpAs[string],
		Schema: func(*Prop) map[string]any {
			return map[string]any{"type": "string", "maxLength": MaxText}
		},
	},
	{
		Name: KindNumber, Label: "数字", Icon: "gauge", Input: InNumber, Opts: []string{"unit"},
		Check: checkNumber, Parse: parseNumber, Text: numberKindText, Num: numberNum, Cmp: cmpAs[float64],
		Schema: func(*Prop) map[string]any { return map[string]any{"type": "number"} },
	},
	{
		Name: KindBoolean, Label: "是否", Icon: "check", Input: InToggle,
		Check: noCheck, Parse: parseBoolean, Text: booleanText, Num: booleanNum, Cmp: cmpBool,
		Schema: func(*Prop) map[string]any { return map[string]any{"type": "boolean"} },
	},
	{
		Name: KindDate, Label: "日期", Hint: "YYYY-MM-DD", Icon: "calendar", Input: InLine,
		Check: noCheck, Parse: parseDate, Text: dateText, Cmp: cmpAs[string],
		Schema: func(*Prop) map[string]any {
			return map[string]any{"type": "string", "format": "date"}
		},
	},
	{
		Name: KindEnum, Label: "选项", Icon: "tag", Input: InChoice, Opts: []string{"options"},
		Check: checkEnum, Parse: parseEnum, Text: textText, Enum: enumValues, Cmp: cmpAs[string],
		Schema: func(p *Prop) map[string]any {
			return map[string]any{"type": "string", "enum": append([]string{}, p.Options...)}
		},
	},
	{
		Name: KindURL, Label: "链接", Hint: "https://", Icon: "link", Input: InLine,
		Check: noCheck, Parse: parseURL, Text: urlText, Cmp: cmpAs[string],
		Schema: func(*Prop) map[string]any {
			return map[string]any{"type": "string", "format": "uri"}
		},
	},
	{
		Name: KindRelation, Label: "关联", Icon: "branch", Input: InPick, Opts: []string{"target", "many", "back"},
		Check: checkRelation, Parse: parseRelation, Text: relationText, Num: relationNum, Refs: relationRefs,
		Cmp: relationCmp,
		Schema: func(p *Prop) map[string]any {
			maxItems := 1
			if p.Many {
				maxItems = MaxTargets
			}
			return map[string]any{
				"type":     "array",
				"items":    map[string]any{"type": "string"},
				"maxItems": maxItems,
			}
		},
	},
	{
		Name: KindRollup, Label: "汇总", Icon: "chart", Input: InNone,
		Opts:  []string{"via", "op", "of", "unit"},
		Check: checkRollup, Parse: parseNumber, Text: rollupText, Num: numberNum, Cmp: cmpAs[float64],
		Eval: rollupEval,
		Schema: func(*Prop) map[string]any {
			return map[string]any{"type": "number", "readOnly": true}
		},
	},
}

// kindIndex maps Kind.Name to its row. It is filled by init, after Kinds is initialized, so that the descriptor
// row functions may call KindOf without an initialization cycle.
var kindIndex map[string]*Kind

func init() {
	kindIndex = make(map[string]*Kind, len(Kinds))
	for _, k := range Kinds {
		kindIndex[k.Name] = k
	}
}

// KindOf returns the descriptor row of a property kind name.
func KindOf(name string) (*Kind, bool) {
	k, ok := kindIndex[name]
	return k, ok
}

// isTextProp reports whether a property is of the text kind (the default lead of a card).
func isTextProp(p *Prop) bool { return p.Kind == KindText }

// isRelationProp reports whether a property is a relation.
func isRelationProp(p *Prop) bool { return p.Kind == KindRelation }

// isPercentRollup reports whether a property is the percent_done rollup (the default progress of a card).
func isPercentRollup(p *Prop) bool { return p.Kind == KindRollup && p.Op == "percent_done" }

// scalar turns a string, a finite number or a bool into text; every Parse is defined on every scalar JSON value.
func scalar(raw any) (string, bool) {
	switch v := raw.(type) {
	case string:
		return v, true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "", false
		}
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case int:
		return strconv.Itoa(v), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case bool:
		return strconv.FormatBool(v), true
	}
	return "", false
}

// bad is the error of a value that does not fit the property.
func bad(p *Prop, want string) error {
	return fmt.Errorf("「%s」应为%s", p.Name, want)
}

// cmpAs orders two canonical values of one Go type; an unset value (nil) sorts first.
func cmpAs[T cmp.Ordered](a, b any) int {
	x, xok := a.(T)
	y, yok := b.(T)
	switch {
	case !xok && !yok:
		return 0
	case !xok:
		return -1
	case !yok:
		return 1
	}
	return cmp.Compare(x, y)
}

func cmpBool(a, b any) int {
	x, xok := a.(bool)
	y, yok := b.(bool)
	switch {
	case !xok && !yok:
		return 0
	case !xok:
		return -1
	case !yok:
		return 1
	case x == y:
		return 0
	case !x:
		return -1
	}
	return 1
}

func noCheck(*Prop, *Type, *Space) error { return nil }

// numberText is the shortest decimal followed by the unit.
func numberText(p *Prop, f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if p.Unit != "" {
		s += " " + p.Unit
	}
	return s
}

// percentText shows a ratio in 0..1 as a whole percentage.
func percentText(f float64) string {
	return strconv.FormatFloat(math.Round(f*100), 'f', 0, 64) + "%"
}

// checkUnit refuses a unit longer than a short name.
func checkUnit(p *Prop) error {
	if runeLen(p.Unit) > MaxShort {
		return fmt.Errorf("「%s」的单位不能超过 %d 个字", p.Name, MaxShort)
	}
	return nil
}

// --- text ---

func parseText(p *Prop, raw any) (any, error) {
	if raw == nil {
		return nil, nil
	}
	s, ok := scalar(raw)
	if !ok {
		return nil, bad(p, "文本")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if runeLen(s) > MaxText {
		return nil, fmt.Errorf("「%s」不能超过 %d 个字", p.Name, MaxText)
	}
	return s, nil
}

// textText shows a text, an enum value or a status as it is.
func textText(_ *Prop, v any, _ func(string) string) string {
	s, _ := v.(string)
	return s
}

// --- number and rollup ---

func parseNumber(p *Prop, raw any) (any, error) {
	var f float64
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case float64:
		f = v
	case int:
		f = float64(v)
	case int64:
		f = float64(v)
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil, nil
		}
		x, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, bad(p, "数字")
		}
		f = x
	default:
		return nil, bad(p, "数字")
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, bad(p, "有限的数字")
	}
	if f == 0 {
		f = 0 // drop the sign of zero so that the value is canonical
	}
	return f, nil
}

func checkNumber(p *Prop, _ *Type, _ *Space) error { return checkUnit(p) }

func numberKindText(p *Prop, v any, _ func(string) string) string {
	f, _ := v.(float64)
	return numberText(p, f)
}

func numberNum(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

func rollupText(p *Prop, v any, _ func(string) string) string {
	f, ok := v.(float64)
	if !ok {
		return ""
	}
	if isPercentRollup(p) {
		return percentText(f)
	}
	return numberText(p, f)
}

// rollupEval runs the rollup's aggregate over the entities its Via names; an undefined result is nil.
func rollupEval(w *World, e *Entity, p *Prop) any {
	agg, ok := aggOf(p.Op)
	if !ok {
		return nil
	}
	f, ok := agg.Fn(w, w.Related(e, p.Via), p.Of)
	if !ok {
		return nil
	}
	return f
}

// checkRollup verifies the source (Via), the aggregate and its operand. A rollup may not read a rollup.
func checkRollup(p *Prop, t *Type, s *Space) error {
	if err := checkUnit(p); err != nil {
		return err
	}
	agg, ok := aggOf(p.Op)
	if !ok {
		return fmt.Errorf("汇总「%s」的方式「%s」不存在", p.Name, p.Op)
	}
	related, err := rollupSource(p, t, s)
	if err != nil {
		return err
	}
	if !agg.Operand {
		if p.Of != "" {
			return fmt.Errorf("汇总「%s」的「%s」不需要属性", p.Name, agg.Label)
		}
		return nil
	}
	if p.Of == "" {
		return fmt.Errorf("汇总「%s」需要一个数字属性", p.Name)
	}
	op, ok := s.Field(related, p.Of)
	if !ok {
		return fmt.Errorf("类型「%s」没有属性「%s」", related.Name, p.Of)
	}
	k, ok := KindOf(op.Kind)
	switch {
	case !ok || k.Num == nil:
		return fmt.Errorf("属性「%s」不是数字", op.Name)
	case k.Eval != nil:
		return fmt.Errorf("汇总不能读取另一个汇总「%s」", op.Name)
	}
	return nil
}

// rollupSource returns the type whose entities the rollup reads: the target of its own relation for "prop", the
// source type of the backlinks for "type.prop".
func rollupSource(p *Prop, t *Type, s *Space) (*Type, error) {
	tk, pk, dotted := strings.Cut(p.Via, ".")
	if !dotted {
		vp, ok := s.Field(t, p.Via)
		if !ok || !isRelationProp(&vp) {
			return nil, fmt.Errorf("汇总「%s」的来源「%s」不是关联", p.Name, p.Via)
		}
		target, ok := s.typeByKey(vp.Target)
		if !ok {
			return nil, fmt.Errorf("汇总「%s」的来源「%s」没有固定的目标类型", p.Name, p.Via)
		}
		return target, nil
	}
	src, ok := s.typeByKey(tk)
	if !ok {
		return nil, fmt.Errorf("汇总「%s」的来源类型「%s」不存在", p.Name, tk)
	}
	bp, ok := s.Field(src, pk)
	if !ok || !isRelationProp(&bp) || bp.Target != t.Key {
		return nil, fmt.Errorf("汇总「%s」的来源「%s」必须是指向「%s」的关联", p.Name, p.Via, t.Name)
	}
	return src, nil
}

// --- boolean ---

func parseBoolean(p *Prop, raw any) (any, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case bool:
		return v, nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil, nil
		}
		b, err := strconv.ParseBool(s)
		if err != nil {
			return nil, bad(p, "是或否")
		}
		return b, nil
	}
	return nil, bad(p, "是或否")
}

func booleanText(_ *Prop, v any, _ func(string) string) string {
	if b, _ := v.(bool); b {
		return "是"
	}
	return "否"
}

func booleanNum(v any) (float64, bool) {
	b, ok := v.(bool)
	if !ok {
		return 0, false
	}
	if b {
		return 1, true
	}
	return 0, true
}

// --- date ---

func parseDate(p *Prop, raw any) (any, error) {
	if raw == nil {
		return nil, nil
	}
	s, ok := raw.(string)
	if !ok {
		return nil, bad(p, "日期")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return nil, bad(p, "YYYY-MM-DD 格式的日期")
	}
	return t.Format(time.DateOnly), nil
}

// dateText shows the day of a date, or of a built-in timestamp in RFC 3339 form.
func dateText(_ *Prop, v any, _ func(string) string) string {
	s, _ := v.(string)
	if len(s) > len(time.DateOnly) {
		return s[:len(time.DateOnly)]
	}
	return s
}

// --- enum ---

func parseEnum(p *Prop, raw any) (any, error) {
	if raw == nil {
		return nil, nil
	}
	s, ok := scalar(raw)
	if !ok {
		return nil, bad(p, "选项")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if !slices.Contains(p.Options, s) {
		return nil, fmt.Errorf("「%s」没有选项「%s」", p.Name, s)
	}
	return s, nil
}

func checkEnum(p *Prop, _ *Type, _ *Space) error {
	if len(p.Options) == 0 || len(p.Options) > MaxOptions {
		return fmt.Errorf("「%s」需要 1 到 %d 个选项", p.Name, MaxOptions)
	}
	seen := map[string]bool{}
	for _, o := range p.Options {
		if o == "" || o != strings.TrimSpace(o) || runeLen(o) > MaxName {
			return fmt.Errorf("「%s」的选项「%s」应为 1 到 %d 个字，且没有首尾空白", p.Name, o, MaxName)
		}
		if seen[o] {
			return fmt.Errorf("「%s」的选项「%s」重复", p.Name, o)
		}
		seen[o] = true
	}
	return nil
}

func enumValues(p *Prop) []string { return append([]string(nil), p.Options...) }

// --- url ---

func parseURL(p *Prop, raw any) (any, error) {
	if raw == nil {
		return nil, nil
	}
	s, ok := scalar(raw)
	if !ok {
		return nil, bad(p, "链接")
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, bad(p, "以 http 或 https 开头的完整链接")
	}
	if runeLen(s) > MaxText {
		return nil, fmt.Errorf("「%s」不能超过 %d 个字", p.Name, MaxText)
	}
	return s, nil
}

// urlText shows the host and the path of a link.
func urlText(_ *Prop, v any, _ func(string) string) string {
	s, _ := v.(string)
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	path := u.Path
	if path == "/" {
		path = ""
	}
	return u.Host + path
}

// --- relation ---

func parseRelation(p *Prop, raw any) (any, error) {
	var items []string
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		items = []string{v}
	case []string:
		items = v
	case []any:
		for _, x := range v {
			s, ok := scalar(x)
			if !ok {
				return nil, bad(p, "记录编号")
			}
			items = append(items, s)
		}
	default:
		return nil, bad(p, "记录编号")
	}
	var out []string
	for _, s := range items {
		s = strings.TrimSpace(s)
		if s == "" || slices.Contains(out, s) {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, nil
	}
	if !p.Many && len(out) > 1 {
		return nil, fmt.Errorf("「%s」只能关联一条记录", p.Name)
	}
	if len(out) > MaxTargets {
		return nil, fmt.Errorf("「%s」最多关联 %d 条记录", p.Name, MaxTargets)
	}
	return out, nil
}

func checkRelation(p *Prop, _ *Type, s *Space) error {
	if p.Target == "" {
		return fmt.Errorf("「%s」需要一个目标类型", p.Name)
	}
	if _, ok := s.typeByKey(p.Target); !ok {
		return fmt.Errorf("「%s」指向的类型「%s」不存在", p.Name, p.Target)
	}
	if runeLen(p.Back) > MaxShort {
		return fmt.Errorf("「%s」的反向名称不能超过 %d 个字", p.Name, MaxShort)
	}
	return nil
}

// relationText joins the names of the targets; a target that cannot be named is left out.
func relationText(_ *Prop, v any, name func(id string) string) string {
	ids, _ := v.([]string)
	var names []string
	for _, id := range ids {
		if n := name(id); n != "" {
			names = append(names, n)
		}
	}
	return strings.Join(names, "、")
}

// relationNum is the number of targets.
func relationNum(v any) (float64, bool) {
	ids, ok := v.([]string)
	if !ok {
		return 0, false
	}
	return float64(len(ids)), true
}

func relationRefs(v any) []string {
	ids, _ := v.([]string)
	return ids
}

func relationCmp(a, b any) int {
	x, _ := a.([]string)
	y, _ := b.([]string)
	return slices.Compare(x, y)
}
