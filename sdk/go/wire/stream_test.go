package wire

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamBounds(t *testing.T) {
	base := UIStreamDelta{Protocol: 1, Session: "session-a", Epoch: "epoch-a", BaseVersion: 1, Version: 1, Operations: []UIStreamOperation{}}
	for _, version := range []int64{1, 9007199254740991, 0, -1, 9007199254740992} {
		base.Version = version
		err := Validate("UIStreamDelta", base)
		valid := version >= 1 && version <= 9007199254740991
		if (err == nil) != valid {
			t.Fatalf("version %d: %v", version, err)
		}
	}
	base.Version = 1
	for _, n := range []int{256, 257} {
		base.Operations = make([]UIStreamOperation, n)
		for i := range base.Operations {
			base.Operations[i] = UIStreamOperation{Op: "remove", Key: "node"}
		}
		if err := Validate("UIStreamDelta", base); (err == nil) != (n == 256) {
			t.Fatalf("%d operations: %v", n, err)
		}
	}
	base.Operations = []UIStreamOperation{}
	for _, n := range []int{0, 128, 129} {
		base.Session = strings.Repeat("a", n)
		if err := Validate("UIStreamDelta", base); (err == nil) != (n == 128) {
			t.Fatalf("%d id characters: %v", n, err)
		}
	}
	for _, raw := range []string{`{"op":"replace","key":"node","item":null}`, `{"op":"replace","key":"node","item":{"type":"text","key":"node"}}`, `{"op":"replace","key":"node","item":{"type":"text","key":"node","text":"Demo","html":"code"}}`, `{"op":"replace","key":"node","view":{"sections":[]},"execute":"code"}`} {
		if err := ValidateJSON("UIStreamOperation", []byte(raw)); err == nil {
			t.Fatalf("accepted malformed payload: %s", raw)
		}
	}
	for _, raw := range []string{
		`{"type":"divider"}`,
		`{"type":"divider","key":""}`,
		`{"type":"divider","key":"node","execute":"code"}`,
	} {
		if err := ValidateJSON("UIStreamItem", []byte(raw)); err == nil {
			t.Fatalf("accepted malformed strict component: %s", raw)
		}
	}
}

func TestStreamCallbackReference(t *testing.T) {
	base := UIStreamEvent{Protocol: 1, Session: "session-a", Epoch: "epoch-a", Version: 1, EventID: "event-a", Key: "node", Action: "cb.example", Event: "press", Value: nil}
	if err := Validate("UIStreamEvent", base); err != nil {
		t.Fatalf("registered callback reference: %v", err)
	}
	for _, action := range []string{"", strings.Repeat("a", 129)} {
		base.Action = action
		if err := Validate("UIStreamEvent", base); err == nil {
			t.Fatal("accepted missing or unbounded callback reference")
		}
	}
	if err := ValidateJSON("UIStreamEvent", []byte(`{"protocol":1,"session":"session-a","epoch":"epoch-a","version":1,"event_id":"event-a","key":"node","event":"press","value":null}`)); err == nil {
		t.Fatal("accepted interaction without its callback reference")
	}
}

func TestStreamRawNumberPrecision(t *testing.T) {
	for _, raw := range []string{"9007199254740991.1", "-9007199254740991.1"} {
		if err := ValidateJSON("UIStreamScalar", []byte(raw)); err == nil {
			t.Fatalf("accepted raw scalar outside safe range: %s", raw)
		}
	}
	for _, raw := range []string{"9007199254740990.5", "0.99999999999999999", "1e-20000"} {
		if err := ValidateJSON("UIStreamVersion", []byte(raw)); err == nil {
			t.Fatalf("accepted raw fractional version: %s", raw)
		}
	}
	for _, raw := range []string{"1", "1e0", "9007199254740991", "9007199254740991.0"} {
		if err := ValidateJSON("UIStreamVersion", []byte(raw)); err != nil {
			t.Fatalf("rejected integral version: %s: %v", raw, err)
		}
	}
	for _, raw := range []string{"0.5", "1e-20000", "-9007199254740991"} {
		if err := ValidateJSON("UIStreamScalar", []byte(raw)); err != nil {
			t.Fatalf("rejected bounded scalar: %s: %v", raw, err)
		}
	}
	if err := ValidateJSON("UIStreamCapability", []byte(`{"protocol":1.0,"components":[]}`)); err != nil {
		t.Fatalf("numeric const comparison changed: %v", err)
	}
	for _, raw := range []string{"1 2", "1e999999999", "1e-999999999"} {
		if err := ValidateJSON("UIStreamVersion", []byte(raw)); err == nil {
			t.Fatalf("accepted invalid/unbounded version: %s", raw)
		}
	}
}

func TestStreamValueBounds(t *testing.T) {
	for _, v := range []any{nil, false, float64(0), "", []any{}, map[string]any{}, []any{nil, false, float64(0)}} {
		if err := Validate("UIStreamValue", v); err != nil {
			t.Fatalf("valid %v: %v", v, err)
		}
	}
	for _, v := range []any{math.NaN(), math.Inf(1), []any{[]any{}}, map[string]any{"nested": map[string]any{}}, map[string]any{"": nil}, map[string]any{strings.Repeat("a", 129): nil}, strings.Repeat("a", 4097), make([]any, 65)} {
		if err := Validate("UIStreamValue", v); err == nil {
			t.Fatalf("accepted out-of-bounds value: %#v", v)
		}
	}
}

func TestStreamReplacementClearingAndEmptyRoundTrip(t *testing.T) {
	path := filepath.Join("..", "..", "..", "schema", "extension", "examples", "UIStreamDelta.operations.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var delta UIStreamDelta
	if err := json.Unmarshal(raw, &delta); err != nil {
		t.Fatal(err)
	}
	// This checks payload preservation, not reducer application. Full replacement clears omitted metadata.
	if delta.Operations[0].View.Title != "" || delta.Operations[0].View.Search != nil {
		t.Fatal("root replacement unexpectedly kept metadata")
	}
	last := delta.Operations[len(delta.Operations)-1]
	if last.Section == nil || last.Section.Items == nil || len(last.Section.Items) != 0 {
		t.Fatal("section replacement lost its explicit empty children")
	}
	if delta.Operations[1].Item.Ratio == nil || *delta.Operations[1].Item.Ratio != 0 {
		t.Fatal("replacement lost explicit zero")
	}
	for _, v := range []any{delta, UIStreamDelta{Protocol: 1, Session: "session-a", Epoch: "epoch-a", BaseVersion: 1, Version: 1, Operations: []UIStreamOperation{}}, UIStreamRequest{Request: "open", Protocol: 1, RequestID: "request-a", Extension: "demo", Contribution: "page", Context: &UIContext{Stream: &UIStreamCapability{Protocol: 1, Components: []string{}}}, State: ""}} {
		out, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		def := "UIStreamDelta"
		if _, ok := v.(UIStreamRequest); ok {
			def = "UIStreamRequest"
			if !strings.Contains(string(out), `"state":""`) || !strings.Contains(string(out), `"components":[]`) {
				t.Fatalf("lost required empty fields: %s", out)
			}
		}
		if err := ValidateJSON(def, out); err != nil {
			t.Fatalf("round trip: %v (%s)", err, out)
		}
	}
}

func TestStreamNativeRequiredEmptyFields(t *testing.T) {
	path := filepath.Join("..", "..", "..", "schema", "extension", "examples", "UIStreamSnapshot.required-empty.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateJSON("UIStreamSnapshot", raw); err != nil {
		t.Fatalf("fixture is not schema-valid: %v", err)
	}
	var snapshot UIStreamSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	section := snapshot.View.Sections[0]
	operations := []UIStreamOperation{
		{Op: "replace", Key: "page", View: &snapshot.View},
		{Op: "replace", Key: "main", Section: &section},
		{Op: "insert", Parent: "page", Section: &section},
	}
	for _, item := range section.Items {
		operations = append(operations,
			UIStreamOperation{Op: "replace", Key: item.Key, Item: &item},
			UIStreamOperation{Op: "insert", Parent: "main", Item: &item})
	}
	code := Component{Type: "code", Key: "code", Text: ""}
	// Empty code text is schema-valid, though CheckView rejects it; this test only asserts wire preservation.
	operations = append(operations, UIStreamOperation{Op: "replace", Key: "code", Item: &code})
	for _, operation := range operations {
		out, err := json.Marshal(operation)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateJSON("UIStreamOperation", out); err != nil {
			t.Fatalf("lost required fields: %v (%s)", err, out)
		}
	}
	// Stream-only encoders must not silently change existing snapshot Component serialization.
	legacy, err := json.Marshal(code)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), `"text"`) {
		t.Fatalf("legacy Component encoding changed: %s", legacy)
	}
}

func TestStreamSchemaDoesNotClaimReducerSemantics(t *testing.T) {
	// A shape-valid frame can still have backwards versions, missing nodes and a cycle. The later reducer must reject it.
	raw := []byte(`{"protocol":1,"session":"session-a","epoch":"epoch-a","base_version":5,"version":2,"operations":[{"op":"move","key":"node","parent":"node"}]}`)
	if err := ValidateJSON("UIStreamDelta", raw); err != nil {
		t.Fatalf("semantic check accidentally represented as schema-only: %v", err)
	}
}
