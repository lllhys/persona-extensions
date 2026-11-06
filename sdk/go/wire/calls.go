package wire

import (
	"encoding/json"
	"fmt"
)

// The calls and events of the extension points' protocols (Calls, from the schemas' x-methods and x-events): what
// a program and the hub hold each other's messages to.

// ValidateParams holds the params of a call or an event, as sent, to its definition (Calls). A null is read as
// absent, as the hub reads it (Go writes a nil list as null). A name Calls does not describe is an error.
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
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return &ValidationError{Def: def, Errors: []string{"$: " + err.Error()}}
	}
	return Validate(def, dropNulls(v))
}

// dropNulls removes the null members of objects, at any depth: absent and null mean the same on the wire.
func dropNulls(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if e == nil {
				delete(x, k)
				continue
			}
			x[k] = dropNulls(e)
		}
	case []any:
		for i, e := range x {
			x[i] = dropNulls(e)
		}
	}
	return v
}
