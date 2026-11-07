package wire

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The calls and events of the extension points' protocols (Calls, from the schemas' x-methods and x-events): what
// a program and the hub hold each other's messages to.

// ValidateParams holds the params of a call or an event, as sent, to its definition (Calls). A null is read as the
// hub's Go decoding reads it: a null list or object is an empty one, any other null is absent (Go writes a nil
// slice, map or pointer as null). A name Calls does not describe is an error.
func ValidateParams(name string, raw json.RawMessage) error {
	c, ok := Calls[name]
	if !ok {
		return fmt.Errorf("%s: not a call the schemas describe", name)
	}
	return validateWire(c.Params, raw)
}

// ValidateResult holds a call's answer to its definition (Calls); one whose answer is any JSON holds whatever it is.
func ValidateResult(name string, raw json.RawMessage) error {
	c, ok := Calls[name]
	if !ok {
		return fmt.Errorf("%s: not a call the schemas describe", name)
	}
	if c.Event {
		return fmt.Errorf("%s: an event has no answer", name)
	}
	if c.Result == "" {
		return nil
	}
	return validateWire(c.Result, raw)
}

func validateWire(def string, raw json.RawMessage) error {
	// An answer of nothing, or null (the Go SDK's nil result), is {}: the hub reads it so.
	if t := strings.TrimSpace(string(raw)); t == "" || t == "null" {
		raw = json.RawMessage("{}")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return &ValidationError{Def: def, Errors: []string{"$: " + err.Error()}}
	}
	loadOnce.Do(loadSchemas)
	if loadErr != nil {
		return loadErr
	}
	for name, doc := range files {
		if s, ok := defs(doc)[def].(map[string]any); ok {
			return Validate(def, readNulls(s, v, name))
		}
	}
	return fmt.Errorf("no definition %s in schema/extension", def)
}

// readNulls reads the nulls of v as the hub's Go decoding does, by what the schema s says is there (Go writes a nil
// slice, map or pointer as null): a null list or map is an empty one, a null optional object (a nil pointer) or
// scalar is absent, a null required object is {} (held to its fields), and a null of any JSON stays null.
func readNulls(s map[string]any, v any, here string) any {
	for s != nil {
		ref, ok := s["$ref"].(string)
		if !ok {
			break
		}
		s, here = resolve(ref, here)
	}
	switch x := v.(type) {
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		extra, _ := s["additionalProperties"].(map[string]any)
		required := map[string]bool{}
		for _, r := range asList(s["required"]) {
			if name, ok := r.(string); ok {
				required[name] = true
			}
		}
		for k, e := range x {
			sub, _ := props[k].(map[string]any)
			if sub == nil {
				sub = extra
			}
			if sub == nil { // a field the schema does not have: left for Validate to judge
				continue
			}
			if e != nil {
				x[k] = readNulls(sub, e, here)
				continue
			}
			switch t, isMap := typeOf(sub, here); {
			case t == "":
				// any JSON: null is a value
			case t == "array":
				x[k] = []any{}
			case t == "object" && (isMap || required[k]):
				// a nil map is an empty one; a required object that is null holds to its fields as {}
				x[k] = map[string]any{}
			default:
				// a nil pointer to an optional object, or a scalar: absent
				delete(x, k)
			}
		}
	case []any:
		items, _ := s["items"].(map[string]any)
		for i, e := range x {
			x[i] = readNulls(items, e, here)
		}
	}
	return v
}

// typeOf is the JSON type s says (its $ref's), "" for any JSON, and whether it is a map (an object of
// additionalProperties without properties of its own).
func typeOf(s map[string]any, here string) (string, bool) {
	for s != nil {
		ref, ok := s["$ref"].(string)
		if !ok {
			break
		}
		s, here = resolve(ref, here)
	}
	t, _ := s["type"].(string)
	_, props := s["properties"]
	_, extra := s["additionalProperties"].(map[string]any)
	return t, t == "object" && !props && extra
}

func asList(v any) []any { l, _ := v.([]any); return l }
