package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The plugin-side schemas' examples (examples/plugin: connector, kb) read into their types and write back the same,
// hold to their definitions, and those under invalid/ do not.
func TestPluginExamples(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "schema", "extension", "examples", "plugin")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("not in the main repository:", err) // an extension repository's copy of the SDK has no schema/
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) == 0 {
		t.Fatal("no plugin examples")
	}
	for _, f := range files {
		def := strings.SplitN(filepath.Base(f), ".", 2)[0]
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateJSON(def, raw); err != nil {
			t.Errorf("%s: %v", filepath.Base(f), err)
		}
		mk, ok := newDef[def]
		if !ok {
			t.Errorf("%s: no type %s", filepath.Base(f), def)
			continue
		}
		v := mk()
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatalf("%s: %v", filepath.Base(f), err)
		}
		out, _ := json.Marshal(v)
		var want, got any
		_ = json.Unmarshal(raw, &want)
		_ = json.Unmarshal(out, &got)
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s: round trip differs\nwant %s\n got %s", filepath.Base(f), raw, out)
		}
	}
	invalid, _ := filepath.Glob(filepath.Join(dir, "invalid", "*.json"))
	if len(invalid) == 0 {
		t.Fatal("no invalid plugin examples")
	}
	for _, f := range invalid {
		raw, _ := os.ReadFile(f)
		if ValidateJSON(strings.SplitN(filepath.Base(f), ".", 2)[0], raw) == nil {
			t.Errorf("%s: holds, but should not", filepath.Base(f))
		}
	}
}

func TestCallsNameTheirDefinitions(t *testing.T) {
	for name, c := range Calls {
		if c.Name != name || (c.From != "hub" && c.From != "program") {
			t.Errorf("%s: %+v", name, c)
		}
		for _, def := range []string{c.Params, c.Result} {
			if _, ok := newDef[def]; def != "" && !ok {
				t.Errorf("%s: no definition %s", name, def)
			}
		}
	}
}

func TestValidateCalls(t *testing.T) {
	// A null is absent: Go writes a nil list as null.
	if err := ValidateResult("connector.thread.page", json.RawMessage(`{"chat":{"id":"c","name":"C","kind":"dm","labels":null},"messages":[]}`)); err != nil {
		t.Errorf("null list: %v", err)
	}
	if err := ValidateParams("connector.status", json.RawMessage(`{}`)); err == nil {
		t.Error("params without the contribution hold")
	}
	if err := ValidateParams("connector.status", json.RawMessage(`{"contribution":"example"}`)); err != nil {
		t.Error(err)
	}
	if err := ValidateResult("connector.logout", json.RawMessage(`{"extra":1}`)); err == nil {
		t.Error("an empty answer with a field holds")
	}
	if err := ValidateResult("connector.group.tool", json.RawMessage(`[1,2]`)); err != nil {
		t.Errorf("group.tool answers any JSON: %v", err)
	}
	if err := ValidateResult("connector.changed", json.RawMessage(`{}`)); err == nil {
		t.Error("an event's answer")
	}
	if err := ValidateParams("connector.nonexistent", json.RawMessage(`{}`)); err == nil {
		t.Error("an unknown call")
	}
}

// What the hub sends and reads holds, nulls included: Go writes a nil list, map or pointer as null, and the hub
// reads null as empty (a list, an object) or absent; a null answer is {}.
func TestValidateReadsNullsAsTheHub(t *testing.T) {
	holds := []struct{ name, raw string }{
		// a new event: meta null, the hub's own call
		{"connector.group.append", `{"contribution":"x","thread":"t","event":"","meta":null,"messages":["m"]}`},
		{"connector.group.fallback", `{"contribution":"x","thread":"t","batch":"b","events":[{"id":"e","meta":null,"done":false}]}`},
	}
	for _, c := range holds[:2] {
		if err := ValidateParams(c.name, json.RawMessage(c.raw)); err != nil {
			t.Errorf("%s %s: %v", c.name, c.raw, err)
		}
	}
	answers := []struct{ name, raw string }{
		{"connector.thread.page", `{"chat":{"id":"c","name":"C","kind":"dm"},"messages":null}`},
		{"connector.manifest", `{"protocol":2,"id":"x","name":"X","capabilities":null}`},
		{"kb.list", `{"items":null,"tags":null,"total":0}`},
		{"connector.group.fallback", `{"assign":null,"new":null}`},
		{"channel.typing", `null`},
		{"kb.owner", `{"name":"","aliases":null,"handles":null}`},
		{"connector.secrets.delete", `null`},
		// a nil pointer to an optional object: absent, even when the object has required fields
		{"connector.thread.page", `{"chat":{"id":"c","name":"C","kind":"pr","author":null,"branch":null},"messages":[{"id":"1","at":"2026-11-04T08:00:00Z","sender":{"name":"A"},"reply_to":null,"code":null}],"before":null}`},
		{"connector.login.step", `{"step":"scan","kind":"qr","qr":null}`},
		{"connector.snapshot.get", `{"chats":null,"meetings":null}`},
		// a nil map is an empty one
		{"connector.inbox.settle", `{"settled":null}`},
	}
	for _, c := range answers {
		if err := ValidateResult(c.name, json.RawMessage(c.raw)); err != nil {
			t.Errorf("%s %s: %v", c.name, c.raw, err)
		}
	}
	// A null where a string or a struct with required fields is wanted is still wrong.
	for _, c := range []struct{ name, raw string }{
		{"connector.status", `{"state":null}`},
		{"connector.status", `null`},
		{"connector.thread.page", `{"chat":null,"messages":[]}`},
	} {
		if err := ValidateResult(c.name, json.RawMessage(c.raw)); err == nil {
			t.Errorf("%s %s holds", c.name, c.raw)
		}
	}
}
