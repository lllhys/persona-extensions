package pluginkit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// PackagedManifest projects an embedded source manifest onto the runtime protocol.
// Only tags, replaces and icon are source-owned. All other fields, including unknown
// ones at every depth, remain available for strict validation by the copied SDK.
// Invalid JSON, duplicate top-level keys and non-object input panic so a malformed embedded
// manifest fails at program initialization rather than being served to the hub.
func PackagedManifest(raw []byte) []byte {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		panic("invalid embedded extension manifest: expected JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			panic(fmt.Errorf("invalid embedded extension manifest: %w", err))
		}
		key, ok := token.(string)
		if !ok {
			panic("invalid embedded extension manifest: expected field name")
		}
		if _, exists := fields[key]; exists {
			panic(fmt.Errorf("invalid embedded extension manifest: duplicate field %q", key))
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			panic(fmt.Errorf("invalid embedded extension manifest: %w", err))
		}
		fields[key] = value
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		panic("invalid embedded extension manifest: expected object end")
	}
	if _, err := decoder.Token(); err != io.EOF {
		panic("invalid embedded extension manifest: trailing JSON data")
	}
	delete(fields, "tags")
	delete(fields, "replaces")
	delete(fields, "icon")
	out, err := json.Marshal(fields)
	if err != nil {
		panic(fmt.Errorf("cannot encode embedded extension manifest: %w", err))
	}
	return out
}
