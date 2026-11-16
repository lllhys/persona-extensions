package verify

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Conforms checks a model's answer against the JSON schema of its prompt: the subset the curator's schemas use
// (type, properties, required, additionalProperties, items, enum, minItems, maxItems, $ref to #/$defs). The hub
// checks answers itself and asks again when one does not conform; a model run any other way (offline, pi, a file)
// does not, so the curator checks too: the answer's items are still checked one by one, this says whether the
// answer as a whole kept to the contract. It returns at most max problems, as JSON pointers with what is wrong.
func Conforms(schemaJSON []byte, answer string, max int) []string {
	var root map[string]any
	if err := json.Unmarshal(schemaJSON, &root); err != nil {
		return []string{"schema: " + err.Error()}
	}
	var v any
	if err := json.Unmarshal([]byte(answer), &v); err != nil {
		return []string{"answer: " + err.Error()}
	}
	c := &conform{root: root, max: max}
	c.check(root, v, "")
	return c.errs
}

type conform struct {
	root map[string]any
	errs []string
	max  int
}

func (c *conform) fail(path, format string, args ...any) {
	if c.max > 0 && len(c.errs) >= c.max {
		return
	}
	if path == "" {
		path = "/"
	}
	c.errs = append(c.errs, path+": "+fmt.Sprintf(format, args...))
}

func (c *conform) resolve(s map[string]any) map[string]any {
	ref, ok := s["$ref"].(string)
	if !ok {
		return s
	}
	name, ok := strings.CutPrefix(ref, "#/$defs/")
	defs, _ := c.root["$defs"].(map[string]any)
	if d, _ := defs[name].(map[string]any); ok && d != nil {
		return d
	}
	return map[string]any{}
}

func (c *conform) check(s map[string]any, v any, path string) {
	s = c.resolve(s)
	if enum, ok := s["enum"].([]any); ok && !slices.Contains(enum, v) {
		c.fail(path, "%v is not one of %v", v, enum)
		return
	}
	switch s["type"] {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			c.fail(path, "not an object")
			return
		}
		props, _ := s["properties"].(map[string]any)
		req, _ := s["required"].([]any)
		for _, r := range req {
			if _, ok := obj[r.(string)]; !ok {
				c.fail(path, "missing %q", r)
			}
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if ps, ok := props[k].(map[string]any); ok {
				c.check(ps, obj[k], path+"/"+k)
				continue
			}
			switch ap := s["additionalProperties"].(type) {
			case bool:
				if !ap {
					c.fail(path, "unexpected %q", k)
				}
			case map[string]any:
				c.check(ap, obj[k], path+"/"+k)
			}
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			c.fail(path, "not an array")
			return
		}
		if m, ok := s["minItems"].(float64); ok && float64(len(arr)) < m {
			c.fail(path, "%d items, at least %v", len(arr), m)
		}
		if m, ok := s["maxItems"].(float64); ok && float64(len(arr)) > m {
			c.fail(path, "%d items, at most %v", len(arr), m)
		}
		if is, ok := s["items"].(map[string]any); ok {
			for i, x := range arr {
				c.check(is, x, fmt.Sprintf("%s/%d", path, i))
			}
		}
	case "string":
		if _, ok := v.(string); !ok {
			c.fail(path, "not a string")
		}
	case "integer":
		if f, ok := v.(float64); !ok || f != float64(int64(f)) {
			c.fail(path, "not an integer")
		}
	case "number":
		if _, ok := v.(float64); !ok {
			c.fail(path, "not a number")
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			c.fail(path, "not a boolean")
		}
	}
}
