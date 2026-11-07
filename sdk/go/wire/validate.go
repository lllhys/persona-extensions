package wire

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// The schemas (schema/extension, copied here by the generator) are the one truth of what a view, a manifest or a
// handshake may hold: Validate holds a value to one of their definitions. It knows the subset the generator does
// (schema/extension/README.md "Subset"), the same as scripts/test/test_extension_schema.py's validator, and nothing more.

//go:embed schema/*.json
var schemaFS embed.FS

var (
	loadOnce sync.Once
	files    map[string]map[string]any // file name → its JSON
	loadErr  error
	patterns sync.Map // pattern → *regexp.Regexp
	// refs holds what resolve answers for every $ref the schemas carry, keyed by
	// the ref and the file it stands in. Filled once with files, read-only after,
	// so no lock: a view visits the same handful of refs once per component.
	refs map[refSite]refTarget
)

type refSite struct{ ref, here string }

type refTarget struct {
	schema map[string]any
	file   string
}

func loadSchemas() {
	files = map[string]map[string]any{}
	entries, err := schemaFS.ReadDir("schema")
	if err != nil {
		loadErr = err
		return
	}
	for _, e := range entries {
		raw, err := schemaFS.ReadFile(path.Join("schema", e.Name()))
		if err != nil {
			loadErr = err
			return
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			loadErr = fmt.Errorf("%s: %v", e.Name(), err)
			return
		}
		files[e.Name()] = doc
	}
	refs = map[refSite]refTarget{}
	for name, doc := range files {
		collectRefs(doc, name)
	}
}

// collectRefs resolves every $ref of node, which stands in the file here, so
// that resolve walks a JSON pointer once per (ref, file) instead of once per
// visit. A $ref that is not a schema reference costs one unused entry.
func collectRefs(node any, here string) {
	switch value := node.(type) {
	case map[string]any:
		for key, member := range value {
			if key == "$ref" {
				if ref, ok := member.(string); ok {
					site := refSite{ref: ref, here: here}
					if _, seen := refs[site]; !seen {
						schema, file := walkRef(ref, here)
						refs[site] = refTarget{schema: schema, file: file}
					}
				}
				continue
			}
			collectRefs(member, here)
		}
	case []any:
		for _, member := range value {
			collectRefs(member, here)
		}
	}
}

// ValidationError is what a value does not hold to, each "<where>: <what>" ($ is the value itself).
type ValidationError struct {
	Def    string
	Errors []string
}

func (e *ValidationError) Error() string {
	if len(e.Errors) == 1 {
		return e.Errors[0]
	}
	return fmt.Sprintf("%s (and %d more)", e.Errors[0], len(e.Errors)-1)
}

// errorsMost is how many errors a validation collects before it stops looking.
const errorsMost = 20

// Validate holds v to the definition def (View, Manifest, InitializeResult…): v is decoded JSON (any of
// map[string]any, []any, string, float64, bool, nil) or anything encoding/json writes, which is written and read back
// first. It returns a *ValidationError, or nil when v holds.
func Validate(def string, v any) error {
	switch v.(type) {
	case map[string]any, []any, string, float64, json.Number, bool, nil:
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return &ValidationError{Def: def, Errors: []string{"$: " + err.Error()}}
		}
		return ValidateJSON(def, raw)
	}
	loadOnce.Do(loadSchemas)
	if loadErr != nil {
		return loadErr
	}
	for name, doc := range files {
		if s, ok := defs(doc)[def].(map[string]any); ok {
			c := checker{}
			c.check(s, v, name, "$")
			if len(c.errs) > 0 {
				return &ValidationError{Def: def, Errors: c.errs}
			}
			return nil
		}
	}
	return fmt.Errorf("no definition %s in schema/extension", def)
}

// ValidateJSON is Validate for raw JSON.
func ValidateJSON(def string, raw []byte) error {
	var v any
	if strings.HasPrefix(def, "UIStream") {
		// Stream bounds and versions must be checked before float64 rounding. Keep legacy decoding unchanged.
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&v); err != nil {
			return &ValidationError{Def: def, Errors: []string{"$: " + err.Error()}}
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return &ValidationError{Def: def, Errors: []string{"$: trailing JSON data"}}
		}
	} else if err := json.Unmarshal(raw, &v); err != nil {
		return &ValidationError{Def: def, Errors: []string{"$: " + err.Error()}}
	}
	return Validate(def, v)
}

func defs(doc map[string]any) map[string]any {
	d, _ := doc["$defs"].(map[string]any)
	return d
}

type checker struct{ errs []string }

func (c *checker) add(format string, a ...any) { c.errs = append(c.errs, fmt.Sprintf(format, a...)) }

func (c *checker) full() bool { return len(c.errs) >= errorsMost }

// resolve finds a $ref (#/$defs/X or <file>.json#/$defs/X) from the file here,
// from what collectRefs already walked; a ref the schemas do not carry is
// walked here, as before.
func resolve(ref, here string) (map[string]any, string) {
	if target, ok := refs[refSite{ref: ref, here: here}]; ok {
		return target.schema, target.file
	}
	return walkRef(ref, here)
}

// walkRef is resolve's own pointer walk: the file the ref names, else here,
// then one map step per segment of its JSON pointer.
func walkRef(ref, here string) (map[string]any, string) {
	name, ptr, _ := strings.Cut(ref, "#")
	if name == "" {
		name = here
	}
	var node any = files[name]
	for _, part := range strings.Split(strings.Trim(ptr, "/"), "/") {
		if part == "" {
			continue
		}
		m, _ := node.(map[string]any)
		node = m[part]
	}
	s, _ := node.(map[string]any)
	return s, name
}

func (c *checker) check(s map[string]any, v any, here, at string) {
	if c.full() || s == nil {
		return
	}
	if ref, ok := s["$ref"].(string); ok {
		target, f := resolve(ref, here)
		if target == nil {
			c.add("%s: the schema has no %s", at, ref)
			return
		}
		c.check(target, v, f, at)
		rest := map[string]any{}
		for k, x := range s {
			if k != "$ref" && k != "description" {
				rest[k] = x
			}
		}
		if len(rest) > 0 {
			c.check(rest, v, here, at)
		}
		return
	}
	if all, ok := s["allOf"].([]any); ok {
		for _, option := range all {
			c.check(option.(map[string]any), v, here, at)
		}
	}
	if anyOf, ok := s["anyOf"].([]any); ok {
		passing := false
		for _, option := range anyOf {
			sub := checker{}
			sub.check(option.(map[string]any), v, here, at)
			if len(sub.errs) == 0 {
				passing = true
				break
			}
		}
		if !passing {
			c.add("%s: no matching anyOf branch", at)
		}
	}
	if forbidden, ok := s["not"].(map[string]any); ok {
		sub := checker{}
		sub.check(forbidden, v, here, at)
		if len(sub.errs) == 0 {
			c.add("%s: matches forbidden schema", at)
		}
	}
	if one, ok := s["oneOf"].([]any); ok {
		if disc, ok := s["x-discriminator"].(string); ok {
			obj, _ := v.(map[string]any)
			tag := obj[disc]
			matched := false
			for _, o := range one {
				option := o.(map[string]any)
				ref, _ := option["$ref"].(string)
				target, _ := resolve(ref, here)
				props, _ := target["properties"].(map[string]any)
				d, _ := props[disc].(map[string]any)
				if tag != nil && d["const"] == tag {
					// Check the complete branch, including overlays next to its $ref.
					c.check(option, v, here, at)
					matched = true
					break
				}
			}
			if !matched {
				c.add("%s: no variant with %s %s", at, disc, show(tag))
				return
			}
		} else {
			passing := 0
			for _, o := range one {
				sub := checker{}
				sub.check(o.(map[string]any), v, here, at)
				if len(sub.errs) == 0 {
					passing++
				}
			}
			if passing != 1 {
				c.add("%s: matches %d of oneOf, not one", at, passing)
			}
		}
		// All oneOf forms also retain their sibling type/properties constraints.
	}
	if t, ok := s["type"].(string); ok && !isType(t, v) {
		c.add("%s: not of type %s", at, t)
		return
	}
	if want, ok := s["const"]; ok && !equal(v, want) {
		c.add("%s: not %s", at, show(want))
	}
	if enum, ok := s["enum"].([]any); ok {
		in := false
		for _, e := range enum {
			if equal(v, e) {
				in = true
			}
		}
		if !in {
			c.add("%s: %s is not one of %s", at, show(v), show(enum))
		}
	}
	switch x := v.(type) {
	case string:
		n := utf8.RuneCountInString(x)
		if m, ok := num(s["minLength"]); ok && float64(n) < m {
			c.add("%s: shorter than %v", at, m)
		}
		if m, ok := num(s["maxLength"]); ok && float64(n) > m {
			c.add("%s: longer than %v", at, m)
		}
		if p, ok := s["pattern"].(string); ok {
			if re := pattern(p); re == nil || !re.MatchString(x) {
				c.add("%s: %s does not match %s", at, show(x), p)
			}
		}
	case float64:
		if m, ok := num(s["minimum"]); ok && x < m {
			c.add("%s: below %v", at, m)
		}
		if m, ok := num(s["maximum"]); ok && x > m {
			c.add("%s: above %v", at, m)
		}
	case json.Number:
		if n, ok := preciseNumber(x); ok {
			if m, ok := preciseNumber(s["minimum"]); ok && n.Cmp(m) < 0 {
				c.add("%s: below %s", at, show(s["minimum"]))
			}
			if m, ok := preciseNumber(s["maximum"]); ok && n.Cmp(m) > 0 {
				c.add("%s: above %s", at, show(s["maximum"]))
			}
		}
	case []any:
		if m, ok := num(s["minItems"]); ok && float64(len(x)) < m {
			c.add("%s: fewer than %v items", at, m)
		}
		if m, ok := num(s["maxItems"]); ok && float64(len(x)) > m {
			c.add("%s: more than %v items", at, m)
		}
		if s["uniqueItems"] == true {
			seen := map[string]bool{}
			for i, e := range x {
				if k := show(e); seen[k] {
					c.add("%s[%d]: %s twice", at, i, k)
				} else {
					seen[k] = true
				}
			}
		}
		items, _ := s["items"].(map[string]any)
		for i, e := range x {
			c.check(items, e, here, fmt.Sprintf("%s[%d]", at, i))
		}
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, k := range req {
				if _, has := x[k.(string)]; !has {
					c.add("%s: no %s", at, k)
				}
			}
		}
		if m, ok := num(s["minProperties"]); ok && float64(len(x)) < m {
			c.add("%s: fewer than %v properties", at, m)
		}
		if m, ok := num(s["maxProperties"]); ok && float64(len(x)) > m {
			c.add("%s: more than %v properties", at, m)
		}
		extra := s["additionalProperties"]
		for _, k := range sortedKeys(x) {
			if names, ok := s["propertyNames"].(map[string]any); ok {
				c.check(names, k, here, at+".<key>")
			}
			if p, ok := props[k].(map[string]any); ok {
				c.check(p, x[k], here, at+"."+k)
			} else if extra == false {
				c.add("%s: unknown field %s", at, k)
			} else if e, ok := extra.(map[string]any); ok {
				c.check(e, x[k], here, at+"."+k)
			}
		}
	}
}

func isType(t string, v any) bool {
	switch t {
	case "null":
		return v == nil
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "number", "integer":
		if n, ok := v.(json.Number); ok {
			f, valid := preciseNumber(n)
			return valid && (t == "number" || f.IsInt())
		}
		f, ok := v.(float64)
		return ok && !math.IsNaN(f) && !math.IsInf(f, 0) && (t == "number" || f == math.Trunc(f))
	}
	return true
}

func num(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

// preciseNumber retains enough mantissa bits to distinguish the input's decimal digits from integer bounds.
// big.Float stores an exponent separately: hostile exponents never allocate an enormous integer/denominator.
func preciseNumber(v any) (*big.Float, bool) {
	switch n := v.(type) {
	case json.Number:
		f, _, err := big.ParseFloat(string(n), 10, uint(len(n))*4+64, big.ToNearestEven)
		return f, err == nil && !f.IsInf()
	case float64:
		if !math.IsNaN(n) && !math.IsInf(n, 0) {
			return big.NewFloat(n), true
		}
	}
	return nil, false
}

func equal(a, b any) bool {
	_, aNumber := a.(json.Number)
	_, bNumber := b.(json.Number)
	if aNumber || bNumber {
		if left, ok := preciseNumber(a); ok {
			if right, ok := preciseNumber(b); ok {
				return left.Cmp(right) == 0
			}
		}
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func show(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func pattern(p string) *regexp.Regexp {
	if re, ok := patterns.Load(p); ok {
		return re.(*regexp.Regexp)
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return nil
	}
	patterns.Store(p, re)
	return re
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
