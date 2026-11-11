package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/spaces/internal/templates"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/store"
)

// t0 is the time the server is given in these tests.
var t0 = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

func clock() func() time.Time { return func() time.Time { return t0 } }

// wantTools is the order the server lists its tools in.
var wantTools = []string{"schema", "query", "space_write", "type_write", "entity_write", "entity_derive", "delete"}

// ---- JSON helpers ----

// norm is the JSON form of a Go value, decoded again: maps, lists and scalars only.
func norm(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// get walks a decoded JSON value: a string selects an object member, an int a list element.
func get(t *testing.T, v any, path ...any) any {
	t.Helper()
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("at %q: not an object: %v", k, v)
			}
			var found bool
			if v, found = m[k]; !found {
				t.Fatalf("missing %q", k)
			}
		case int:
			a, ok := v.([]any)
			if !ok || k >= len(a) {
				t.Fatalf("at %d: not a list of that length: %v", k, v)
			}
			v = a[k]
		}
	}
	return v
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("want an object, got %T", v)
	}
	return m
}

func asList(t *testing.T, v any) []any {
	t.Helper()
	l, ok := v.([]any)
	if !ok {
		t.Fatalf("want a list, got %T", v)
	}
	return l
}

func asStr(t *testing.T, v any) string {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("want a string, got %T", v)
	}
	return s
}

// ---- session: Serve over pipes, one request at a time ----

// session drives Serve over two pipes. Each request is written and its answer is read before the next one, so every
// answer can be matched with its request.
type session struct {
	t        *testing.T
	in       *io.PipeWriter
	out      *bufio.Reader
	finished chan error
	nextID   int
}

func start(t *testing.T, st kernel.Storage) *session {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &session{t: t, in: inW, out: bufio.NewReader(outR), finished: make(chan error, 1)}
	go func() {
		err := Serve(context.Background(), inR, outW, st, clock())
		outW.Close()
		s.finished <- err
	}()
	return s
}

// finish ends the input and checks that Serve returned without error.
func (s *session) finish() {
	s.t.Helper()
	s.in.Close()
	if err := <-s.finished; err != nil {
		s.t.Fatalf("Serve: %v", err)
	}
}

// write sends one line that gets no answer, or the next answer is read by the caller.
func (s *session) write(line string) {
	s.t.Helper()
	if _, err := io.WriteString(s.in, line+"\n"); err != nil {
		s.t.Fatalf("write: %v", err)
	}
}

// call sends one line and reads the answer to it.
func (s *session) call(line string) map[string]any {
	s.t.Helper()
	s.write(line)
	raw, err := s.out.ReadBytes('\n')
	if err != nil {
		s.t.Fatalf("no answer to %s: %v", line, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		s.t.Fatalf("answer %s: %v", raw, err)
	}
	return m
}

// rpc sends a request with the next id and checks that the answer carries that id.
func (s *session) rpc(method, params string) map[string]any {
	s.t.Helper()
	s.nextID++
	m := s.call(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, s.nextID, method, params))
	if got := get(s.t, m, "id"); got != float64(s.nextID) {
		s.t.Fatalf("answer id = %v, want %d", got, s.nextID)
	}
	return m
}

// tool calls a tool and returns the whole answer.
func (s *session) tool(name, args string) map[string]any {
	s.t.Helper()
	return s.rpc("tools/call", fmt.Sprintf(`{"name":%q,"arguments":%s}`, name, args))
}

// done runs a tool that must succeed and returns its structured content. The text of the result must be the same JSON.
func (s *session) done(name, args string) any {
	s.t.Helper()
	res := get(s.t, s.tool(name, args), "result")
	if get(s.t, res, "isError") != false {
		s.t.Fatalf("%s: tool error: %v", name, get(s.t, res, "content", 0, "text"))
	}
	sc := get(s.t, res, "structuredContent")
	var fromText any
	if err := json.Unmarshal([]byte(asStr(s.t, get(s.t, res, "content", 0, "text"))), &fromText); err != nil {
		s.t.Fatalf("%s: text is not JSON: %v", name, err)
	}
	if !reflect.DeepEqual(sc, fromText) {
		s.t.Fatalf("%s: text and structured content differ:\n%v\n%v", name, fromText, sc)
	}
	return sc
}

// failed runs a tool that must report an error and returns the text of the error.
func (s *session) failed(name, args string) string {
	s.t.Helper()
	res := get(s.t, s.tool(name, args), "result")
	if get(s.t, res, "isError") != true {
		s.t.Fatalf("%s: want a tool error, got %v", name, res)
	}
	return asStr(s.t, get(s.t, res, "content", 0, "text"))
}

// ---- tests ----

func TestScriptedSession(t *testing.T) {
	s := start(t, store.Open(""))
	defer s.finish()

	initAnswer := s.rpc("initialize", `{}`)
	if v := get(t, initAnswer, "result", "protocolVersion"); v != "2025-06-18" {
		t.Errorf("protocolVersion = %v", v)
	}
	if v := get(t, initAnswer, "result", "serverInfo", "name"); v != "spaces" {
		t.Errorf("serverInfo.name = %v", v)
	}

	s.write(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if got := get(t, s.rpc("ping", `{}`), "result"); !reflect.DeepEqual(got, map[string]any{}) {
		t.Errorf("ping result = %v", got)
	}

	var names []string
	for _, it := range asList(t, get(t, s.rpc("tools/list", `{}`), "result", "tools")) {
		names = append(names, asStr(t, get(t, it, "name")))
	}
	if !slices.Equal(names, wantTools) {
		t.Fatalf("tools = %v, want %v", names, wantTools)
	}

	// Create a space from a template, with its samples; the stored space is the one that answers.
	space := asStr(t, get(t, s.done("space_write", `{"name":"写作","template":"project","samples":true}`), "id"))

	// Define two types in the space.
	s.done("type_write", fmt.Sprintf(`{"space":%q,"type":{"key":"chapter","name":"章节",`+
		`"flow":[{"name":"待写","phase":"todo"},{"name":"在写","phase":"doing"},{"name":"定稿","phase":"done"}],`+
		`"props":[{"key":"words","name":"字数","kind":"number","unit":"字"}]}}`, space))
	s.done("type_write", fmt.Sprintf(`{"space":%q,"type":{"key":"draft","name":"草稿",`+
		`"props":[{"key":"words","name":"字数","kind":"number","unit":"字"}]}}`, space))

	// Write records: one in progress, one not started.
	chap1 := get(t, s.done("entity_write", fmt.Sprintf(
		`{"space":%q,"type":"chapter","title":"第一章","status":"在写","values":{"words":1200}}`, space)), "id")
	chap1Answer := s.done("entity_write", fmt.Sprintf(`{"space":%q,"type":"chapter","title":"第二章","status":"待写"}`, space))
	chap2 := get(t, chap1Answer, "id")
	if v := get(t, chap1Answer, "phase"); v != "todo" {
		t.Errorf("phase of a not started chapter = %v", v)
	}
	if v := get(t, chap1Answer, "moves", 0); v != "在写" {
		t.Errorf("first move of 待写 = %v", v)
	}
	if msg := s.failed("entity_write", fmt.Sprintf(`{"id":%q,"status":"定稿"}`, chap2)); !strings.Contains(msg, "不能直接变为") {
		t.Errorf("illegal move message = %q", msg)
	}

	// Derive a draft from the first chapter: the word count is carried over, and the origin is a link.
	derived := asMap(t, s.done("entity_derive", fmt.Sprintf(`{"id":%q,"type":"draft"}`, chap1)))
	if got := asList(t, derived["inherited"]); !reflect.DeepEqual(got, []any{"words"}) {
		t.Errorf("inherited = %v", got)
	}
	draft := asMap(t, derived["entity"])
	if draft["title"] != "第一章" {
		t.Errorf("derived title = %v", draft["title"])
	}
	if out := asList(t, draft["out"]); len(out) != 1 || asMap(t, out[0])["prop"] != "origin" {
		t.Errorf("derived out = %v", out)
	}

	// Advance the first chapter without naming its space: the record is found by its id.
	if v := get(t, s.done("entity_write", fmt.Sprintf(`{"id":%q,"status":"定稿"}`, chap1)), "phase"); v != "done" {
		t.Errorf("phase after advancing = %v", v)
	}

	// Query with a filter, then grouped by status with aggregates.
	narrow := asMap(t, s.done("query", fmt.Sprintf(`{"space":%q,"type":"chapter","text":"第一",`+
		`"where":[{"field":"words","op":"gt","value":1000}]}`, space)))
	if narrow["total"] != float64(1) {
		t.Errorf("filtered total = %v", narrow["total"])
	}
	grouped := asMap(t, s.done("query", fmt.Sprintf(`{"space":%q,"type":"chapter","group":"status",`+
		`"agg":[{"op":"sum","of":"words"},{"op":"count"}]}`, space)))
	if grouped["total"] != float64(2) {
		t.Errorf("total = %v", grouped["total"])
	}
	aggs := asList(t, grouped["aggs"])
	if asMap(t, aggs[0])["value"] != float64(1200) || asMap(t, aggs[1])["value"] != float64(2) {
		t.Errorf("aggs = %v", aggs)
	}
	groups := asList(t, grouped["groups"])
	if len(groups) != 2 || asMap(t, groups[0])["key"] != "待写" || asMap(t, groups[1])["key"] != "定稿" {
		t.Errorf("groups = %v (want the flow order)", groups)
	}
	if n := len(asList(t, grouped["entities"])); n != 2 {
		t.Errorf("entities = %d, want 2", n)
	}

	// A record by its id shows its origin among its links.
	one := asMap(t, s.done("query", fmt.Sprintf(`{"id":%q}`, get(t, derived["entity"], "id"))))
	if one["total"] != float64(1) || len(asList(t, one["entities"])) != 1 {
		t.Errorf("query by id = %v", one)
	}

	// The schema of the space lists its types with their value schemas.
	sch := asMap(t, s.done("schema", fmt.Sprintf(`{"space":%q}`, space)))
	types := asList(t, sch["types"])
	if len(types) != 6 {
		t.Errorf("types = %d, want 6", len(types))
	}
	if vs := asMap(t, asMap(t, types[len(types)-2])["values_schema"]); vs["type"] != "object" {
		t.Errorf("values_schema = %v", vs)
	}

	// A second space: a record id in the wrong space is refused.
	other := asStr(t, get(t, s.done("space_write", `{"name":"另一个"}`), "id"))
	if msg := s.failed("query", fmt.Sprintf(`{"id":%q,"space":%q}`, chap1, other)); !strings.Contains(msg, "不在空间") {
		t.Errorf("wrong space message = %q", msg)
	}

	// Deletes: a record, a type, then the two spaces.
	if d := asMap(t, s.done("delete", fmt.Sprintf(`{"entity":%q}`, chap2))); d["deleted"] != "entity" || d["unlinked"] != nil {
		t.Errorf("delete entity = %v", d)
	}
	if d := asMap(t, s.done("delete", fmt.Sprintf(`{"space":%q,"type":"draft"}`, space))); d["deleted"] != "type" || d["entities"] != float64(1) {
		t.Errorf("delete type = %v", d)
	}
	if msg := s.failed("delete", fmt.Sprintf(`{"space":%q,"type":"nope"}`, space)); !strings.Contains(msg, "没有类型") {
		t.Errorf("delete missing type message = %q", msg)
	}
	s.done("delete", fmt.Sprintf(`{"space":%q}`, other))
	s.done("delete", fmt.Sprintf(`{"space":%q}`, space))

	// With no space left, the schema tool lists the templates and the vocabulary.
	empty := asMap(t, s.done("schema", `{}`))
	if len(asList(t, empty["spaces"])) != 0 || len(asList(t, empty["templates"])) != 4 {
		t.Errorf("empty overview = %v", empty)
	}
	if _, ok := asMap(t, empty["vocabulary"])["templates"]; !ok {
		t.Errorf("vocabulary without templates")
	}
}

func TestTemplateRelations(t *testing.T) {
	s := start(t, store.Open(""))
	defer s.finish()

	made := asMap(t, s.done("space_write", `{"template":"project","samples":true,"icon":"star","about":"示例"}`))
	if made["name"] != "项目与目标" || made["icon"] != "star" || made["about"] != "示例" {
		t.Errorf("template space = %v", made)
	}
	space := asStr(t, made["id"])

	// A goal derived from the first idea inherits its area.
	ideas := asList(t, get(t, s.done("query", fmt.Sprintf(`{"space":%q,"type":"idea"}`, space)), "entities"))
	var idea string
	for _, e := range ideas {
		if asMap(t, e)["title"] == "每周写一篇复盘" {
			idea = asStr(t, asMap(t, e)["id"])
		}
	}
	if idea == "" {
		t.Fatalf("sample idea not found")
	}
	inherited := asList(t, get(t, s.done("entity_derive", fmt.Sprintf(`{"id":%q,"type":"goal"}`, idea)), "inherited"))
	if !slices.Contains(inherited, any("area")) {
		t.Errorf("inherited = %v, want area", inherited)
	}

	// Rollups are values of the record: 2 of 4 tasks are done.
	goals := asList(t, get(t, s.done("query", fmt.Sprintf(`{"space":%q,"type":"goal"}`, space)), "entities"))
	var goal1 string
	for _, g := range goals {
		if asMap(t, g)["title"] == "连续写满 12 篇周复盘" {
			goal1 = asStr(t, asMap(t, g)["id"])
			values := asMap(t, asMap(t, g)["values"])
			if values["tasks"] != float64(4) || values["progress"] != 0.5 {
				t.Errorf("rollups = %v", values)
			}
		}
	}
	if goal1 == "" {
		t.Fatalf("sample goal not found")
	}

	// A task blocked by two tasks is one record in both groups: the page lists it once.
	tasks := asList(t, get(t, s.done("query", fmt.Sprintf(`{"space":%q,"type":"task"}`, space)), "entities"))
	if len(tasks) < 3 {
		t.Fatalf("tasks = %d", len(tasks))
	}
	ids := []string{asStr(t, asMap(t, tasks[0])["id"]), asStr(t, asMap(t, tasks[1])["id"]), asStr(t, asMap(t, tasks[2])["id"])}
	s.done("entity_write", fmt.Sprintf(`{"space":%q,"id":%q,"values":{"blocked_by":[%q,%q]}}`, space, ids[2], ids[0], ids[1]))
	byBlocker := asMap(t, s.done("query", fmt.Sprintf(`{"space":%q,"type":"task","group":"blocked_by"}`, space)))
	if n := len(asList(t, byBlocker["entities"])); n != int(byBlocker["total"].(float64)) {
		t.Errorf("entities = %d, total = %v: a record was listed twice", n, byBlocker["total"])
	}

	// Deleting a goal unlinks the tasks and notes that pointed at it.
	del := asMap(t, s.done("delete", fmt.Sprintf(`{"entity":%q}`, goal1)))
	if n, ok := del["unlinked"].(float64); !ok || n < 1 {
		t.Errorf("unlinked = %v", del["unlinked"])
	}
}

func TestSpaceWriteVariants(t *testing.T) {
	s := start(t, store.Open(""))
	defer s.finish()

	// Without a name, a template names the space after itself.
	blank, _ := templates.Get("blank")
	made := asMap(t, s.done("space_write", `{"template":"blank"}`))
	if made["name"] != blank.Name {
		t.Errorf("name = %v, want %v", made["name"], blank.Name)
	}
	// Changing a space keeps what is not given.
	changed := asMap(t, s.done("space_write", fmt.Sprintf(`{"id":%q,"name":"改名","about":"新的简介"}`, made["id"])))
	if changed["name"] != "改名" || changed["about"] != "新的简介" || changed["id"] != made["id"] {
		t.Errorf("changed = %v", changed)
	}
	// A new space with a given name and no template.
	if v := asMap(t, s.done("space_write", `{"name":"手动","icon":"box"}`)); v["icon"] != "box" {
		t.Errorf("icon = %v", v["icon"])
	}
}

func TestErrorAnswers(t *testing.T) {
	s := start(t, store.Open(""))
	defer s.finish()
	space := asStr(t, get(t, s.done("space_write", `{"name":"测试"}`), "id"))

	cases := []struct {
		name    string
		line    string
		code    int    // a JSON-RPC error code, or 0 for a result
		isError bool   // a tool error result
		id      any    // the id of the answer; nil is null
		msg     string // a part of the error message or of the tool error text
	}{
		{name: "ping with a null id", line: `{"jsonrpc":"2.0","id":null,"method":"ping"}`, id: nil},
		{name: "parse error", line: `{"jsonrpc":`, code: -32700, msg: "Parse error"},
		{name: "not an object", line: `[1]`, code: -32600, msg: "Invalid Request"},
		{name: "wrong version", line: `{"jsonrpc":"1.0","id":3,"method":"ping"}`, code: -32600, id: float64(3), msg: "Invalid Request"},
		{name: "no method", line: `{"jsonrpc":"2.0","id":4}`, code: -32600, id: float64(4), msg: "Invalid Request"},
		{name: "unknown method", line: `{"jsonrpc":"2.0","id":5,"method":"resources/list"}`, code: -32601, id: float64(5), msg: "Method not found"},
		{name: "call without params", line: `{"jsonrpc":"2.0","id":6,"method":"tools/call"}`, code: -32602, id: float64(6), msg: "Invalid params"},
		{name: "call with string params", line: `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":"x"}`, code: -32602, id: float64(7), msg: "Invalid params"},
		{name: "unknown tool", line: `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"nope"}}`, code: -32602, id: float64(8), msg: "Unknown tool"},
		{name: "no tool name", line: `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{}}`, code: -32602, id: float64(9), msg: "Unknown tool"},
		{name: "unknown argument", line: `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"query","arguments":{"bogus":1}}}`, id: float64(10), isError: true, msg: "参数不对"},
		{name: "argument of the wrong type", line: `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"schema","arguments":{"space":5}}}`, id: float64(11), isError: true, msg: "参数不对"},
		{name: "arguments that are a list", line: `{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"schema","arguments":[1]}}`, id: float64(12), isError: true, msg: "参数不对"},
		{name: "schema of a missing space", line: `{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"schema","arguments":{"space":"nope"}}}`, id: float64(13), isError: true, msg: "没有空间"},
		{name: "query of a missing space", line: `{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"query","arguments":{"space":"nope"}}}`, id: float64(14), isError: true, msg: "没有空间"},
		{name: "query of a missing type", line: `{"jsonrpc":"2.0","id":15,"method":"tools/call","params":{"name":"query","arguments":{"type":"nope"}}}`, id: float64(15), isError: true},
		{name: "query of a missing record", line: `{"jsonrpc":"2.0","id":16,"method":"tools/call","params":{"name":"query","arguments":{"id":"nope"}}}`, id: float64(16), isError: true, msg: "没有记录"},
		{name: "type without a structure", line: `{"jsonrpc":"2.0","id":17,"method":"tools/call","params":{"name":"type_write","arguments":{"space":"SPACE"}}}`, id: float64(17), isError: true, msg: "需要一个类型的结构"},
		{name: "type with a bad key", line: `{"jsonrpc":"2.0","id":18,"method":"tools/call","params":{"name":"type_write","arguments":{"space":"SPACE","type":{"key":"Bad Key","name":"x"}}}}`, id: float64(18), isError: true},
		{name: "record of a missing type", line: `{"jsonrpc":"2.0","id":19,"method":"tools/call","params":{"name":"entity_write","arguments":{"space":"SPACE","type":"nope","title":"x"}}}`, id: float64(19), isError: true, msg: "没有类型"},
		{name: "change of a missing record", line: `{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"entity_write","arguments":{"id":"nope","title":"x"}}}`, id: float64(20), isError: true, msg: "没有记录"},
		{name: "derive of a missing record", line: `{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"entity_derive","arguments":{"id":"nope","type":"x"}}}`, id: float64(21), isError: true, msg: "没有记录"},
		{name: "delete of nothing", line: `{"jsonrpc":"2.0","id":22,"method":"tools/call","params":{"name":"delete","arguments":{}}}`, id: float64(22), isError: true, msg: "需要指定"},
		{name: "delete of a record and a space", line: `{"jsonrpc":"2.0","id":23,"method":"tools/call","params":{"name":"delete","arguments":{"entity":"nope","space":"SPACE"}}}`, id: float64(23), isError: true, msg: "不能和"},
		{name: "delete of a missing record", line: `{"jsonrpc":"2.0","id":24,"method":"tools/call","params":{"name":"delete","arguments":{"entity":"nope"}}}`, id: float64(24), isError: true, msg: "没有记录"},
		{name: "delete of a missing space", line: `{"jsonrpc":"2.0","id":25,"method":"tools/call","params":{"name":"delete","arguments":{"space":"nope"}}}`, id: float64(25), isError: true, msg: "没有空间"},
		{name: "delete of a type that is not there", line: `{"jsonrpc":"2.0","id":26,"method":"tools/call","params":{"name":"delete","arguments":{"space":"SPACE","type":"nope"}}}`, id: float64(26), isError: true, msg: "没有类型"},
		{name: "samples without a template", line: `{"jsonrpc":"2.0","id":27,"method":"tools/call","params":{"name":"space_write","arguments":{"name":"x","samples":true}}}`, id: float64(27), isError: true, msg: "示例记录只能随模板新建"},
		{name: "template with an id", line: `{"jsonrpc":"2.0","id":28,"method":"tools/call","params":{"name":"space_write","arguments":{"id":"SPACE","template":"blank"}}}`, id: float64(28), isError: true, msg: "不能指定编号"},
		{name: "unknown template", line: `{"jsonrpc":"2.0","id":29,"method":"tools/call","params":{"name":"space_write","arguments":{"template":"nope"}}}`, id: float64(29), isError: true, msg: "没有模板"},
		{name: "space without a name", line: `{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"space_write","arguments":{}}}`, id: float64(30), isError: true, msg: "需要一个名称"},
		{name: "object id", line: `{"jsonrpc":"2.0","id":{"x":1},"method":"ping"}`, code: -32600, id: nil, msg: "Invalid Request"},
		{name: "array id", line: `{"jsonrpc":"2.0","id":[1],"method":"ping"}`, code: -32600, id: nil, msg: "Invalid Request"},
		{name: "boolean id", line: `{"jsonrpc":"2.0","id":true,"method":"ping"}`, code: -32600, id: nil, msg: "Invalid Request"},
		{name: "object id with a bad method", line: `{"jsonrpc":"2.0","id":{"x":1},"method":5}`, code: -32600, id: nil, msg: "Invalid Request"},
		{name: "string id", line: `{"jsonrpc":"2.0","id":"a","method":"ping"}`, id: "a"},
		{name: "fractional id", line: `{"jsonrpc":"2.0","id":1.5,"method":"ping"}`, id: 1.5},
	}
	for _, c := range cases {
		m := s.call(strings.ReplaceAll(c.line, "SPACE", space))
		if !reflect.DeepEqual(m["id"], c.id) {
			t.Errorf("%s: id = %v, want %v", c.name, m["id"], c.id)
		}
		if c.code != 0 {
			e, ok := m["error"].(map[string]any)
			if !ok {
				t.Errorf("%s: no error in %v", c.name, m)
				continue
			}
			if e["code"] != float64(c.code) {
				t.Errorf("%s: code = %v, want %d", c.name, e["code"], c.code)
			}
			if msg, _ := e["message"].(string); !strings.Contains(msg, c.msg) {
				t.Errorf("%s: message = %q", c.name, msg)
			}
			continue
		}
		res, ok := m["result"].(map[string]any)
		if !ok {
			t.Errorf("%s: no result in %v", c.name, m)
			continue
		}
		got, _ := res["isError"].(bool)
		if got != c.isError {
			t.Errorf("%s: isError = %v, want %v (%v)", c.name, got, c.isError, res)
			continue
		}
		if c.isError {
			if text := asStr(t, get(t, res, "content", 0, "text")); !strings.Contains(text, c.msg) {
				t.Errorf("%s: text = %q", c.name, text)
			}
		}
	}
}

func TestNotificationsAreNotAnswered(t *testing.T) {
	s := start(t, store.Open(""))
	defer s.finish()
	// None of these lines gets an answer, and the write is never applied.
	s.write(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"space_write","arguments":{"name":"不应创建"}}}`)
	s.write(`{"jsonrpc":"2.0","method":"ping"}`)
	s.write("   ")
	// The next answer is the one to this request.
	m := s.rpc("tools/call", `{"name":"schema","arguments":{}}`)
	if spaces := get(t, m, "result", "structuredContent", "spaces"); !reflect.DeepEqual(spaces, []any{}) {
		t.Errorf("spaces = %v: a notification was applied", spaces)
	}
}

func TestServeLineHandling(t *testing.T) {
	st := store.Open("")
	ping := `{"jsonrpc":"2.0","id":2,"method":"ping"}`

	// A last line without a newline is answered.
	var buf bytes.Buffer
	if err := Serve(context.Background(), strings.NewReader(ping), &buf, st, clock()); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if got := buf.String(); got != `{"jsonrpc":"2.0","id":2,"result":{}}`+"\n" {
		t.Errorf("answer = %q", got)
	}

	// A canceled context stops before reading.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Serve(ctx, strings.NewReader(ping+"\n"), io.Discard, st, clock()); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Serve = %v", err)
	}

	// A read error ends the loop with that error, after the lines before it are answered.
	buf.Reset()
	in := io.MultiReader(strings.NewReader(ping+"\n"), errReader{})
	if err := Serve(context.Background(), in, &buf, st, clock()); err == nil || err.Error() != "read failed" {
		t.Errorf("read error = %v", err)
	}
	if !strings.Contains(buf.String(), `"id":2`) {
		t.Errorf("answer before the read error is missing: %q", buf.String())
	}

	// A write error ends the loop with that error.
	if err := Serve(context.Background(), strings.NewReader(ping+"\n"), failWriter{}, st, clock()); err == nil || err.Error() != "write failed" {
		t.Errorf("write error = %v", err)
	}
}

// errReader fails on the first read.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// failWriter fails on every write.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

// fakeStore is a kernel.Storage whose reads or writes can fail, and which otherwise holds an empty base.
type fakeStore struct {
	viewErr, updErr error
}

func (f fakeStore) View(fn func(b *kernel.Base) error) error {
	if f.viewErr != nil {
		return f.viewErr
	}
	return fn(&kernel.Base{Format: 1})
}

func (f fakeStore) Update(fn func(b *kernel.Base, now time.Time) error) error {
	if f.updErr != nil {
		return f.updErr
	}
	return fn(&kernel.Base{Format: 1}, t0)
}

func (f fakeStore) Changed() bool { return false }

func TestStorageFailures(t *testing.T) {
	reads := start(t, fakeStore{viewErr: errors.New("disk gone")})
	if msg := reads.failed("schema", `{}`); msg != "disk gone" {
		t.Errorf("read failure = %q", msg)
	}
	reads.finish()

	writes := start(t, fakeStore{updErr: errors.New("locked")})
	if msg := writes.failed("space_write", `{"name":"x"}`); msg != "locked" {
		t.Errorf("write failure = %q", msg)
	}
	writes.finish()
}

func TestRunAndReplyFailures(t *testing.T) {
	srv := &server{st: fakeStore{}, now: clock()}
	inf := Tool{Name: "inf", Run: func(*kernel.Base, time.Time, json.RawMessage) (any, error) { return math.Inf(1), nil }}
	if _, err := srv.run(inf, nil); err == nil {
		t.Errorf("read of a value that cannot be encoded succeeded")
	}
	inf.Read = true
	if _, err := srv.run(inf, nil); err == nil {
		t.Errorf("write of a value that cannot be encoded succeeded")
	}
	if got := string(reply(nil, math.Inf(1), nil)); !strings.Contains(got, "-32603") {
		t.Errorf("reply = %s, want an internal error", got)
	}
}

func TestDecode(t *testing.T) {
	cases := []struct {
		raw string
		ok  bool
	}{
		{"", true},
		{"null", true},
		{" \n null ", true},
		{`{"space":"a"}`, true},
		{`{"x":1}`, false},
		{`{"space":5}`, false},
		{`5`, false},
	}
	for _, c := range cases {
		var a schemaArgs
		err := decode(json.RawMessage(c.raw), &a)
		if (err == nil) != c.ok {
			t.Errorf("decode(%q) = %v, want ok %v", c.raw, err, c.ok)
		}
	}
}

func TestToolTable(t *testing.T) {
	var names, read []string
	for _, tl := range Tools() {
		names = append(names, tl.Name)
		if tl.Read {
			read = append(read, tl.Name)
		}
		if tl.About == "" || tl.Input == nil || tl.Run == nil {
			t.Errorf("%s: incomplete table row", tl.Name)
		}
		checkSchema(t, tl.Name, norm(t, tl.Input()))
	}
	if !slices.Equal(names, wantTools) {
		t.Errorf("tools = %v, want %v", names, wantTools)
	}
	// The manifest allows exactly these two tools, so only they may be read-only.
	if want := []string{"schema", "query"}; !slices.Equal(read, want) {
		t.Errorf("read-only tools = %v, want %v", read, want)
	}

	// Tools returns a copy of the table.
	ts := Tools()
	ts[0].Name = "changed"
	if Tools()[0].Name != "schema" {
		t.Errorf("Tools shares the table")
	}

	// Annotations: readOnlyHint for the read-only tools; destructiveHint for every write except entity_derive, which only
	// adds a record.
	wantDestructive := []string{"space_write", "type_write", "entity_write", "delete"}
	for _, it := range asList(t, get(t, norm(t, toolList()), "tools")) {
		name := asStr(t, get(t, it, "name"))
		ann := asMap(t, get(t, it, "annotations"))
		if ann["readOnlyHint"] != slices.Contains(read, name) {
			t.Errorf("%s: readOnlyHint = %v", name, ann["readOnlyHint"])
		}
		if ann["destructiveHint"] != slices.Contains(wantDestructive, name) {
			t.Errorf("%s: destructiveHint = %v", name, ann["destructiveHint"])
		}
	}
}

// TestToolSchemasAreFlat checks that no tool input schema has a combinator or a count rule at its root: some clients
// refuse such a root, and the rules of a tool are stated in its description instead.
func TestToolSchemasAreFlat(t *testing.T) {
	for _, tl := range Tools() {
		root := asMap(t, norm(t, tl.Input()))
		for _, k := range []string{"oneOf", "anyOf", "allOf", "dependentRequired", "minProperties"} {
			if _, ok := root[k]; ok {
				t.Errorf("%s: root schema has %s", tl.Name, k)
			}
		}
		if root["type"] != "object" {
			t.Errorf("%s: root schema type = %v, want object", tl.Name, root["type"])
		}
	}
}

func TestQuerySchemaUsesKernelGenerators(t *testing.T) {
	props := asMap(t, get(t, norm(t, queryInput()), "properties"))
	for _, k := range []string{"space", "id"} {
		if _, ok := props[k]; !ok {
			t.Errorf("query schema without %q", k)
		}
	}
	kernelProps := asMap(t, get(t, norm(t, kernel.QuerySchema()), "properties"))
	for k, v := range kernelProps {
		if !reflect.DeepEqual(props[k], v) {
			t.Errorf("query schema property %q differs from the kernel", k)
		}
	}
	if got, want := norm(t, typeWriteInput()), norm(t, map[string]any{"type": "object", "additionalProperties": false,
		"required":   []string{"type"},
		"properties": map[string]any{"space": spaceProp(), "type": kernel.TypeSchema()}}); !reflect.DeepEqual(got, want) {
		t.Errorf("type_write schema differs from the kernel generator")
	}
}

// checkSchema checks a decoded JSON Schema of a tool input: every object refuses unknown properties, lists only known
// properties as required, and every property schema is well formed.
func checkSchema(t *testing.T, path string, v any) {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Errorf("%s: schema is not an object", path)
		return
	}
	typ, ok := m["type"]
	if !ok {
		return // a schema without a type: an empty value or a combination of others
	}
	switch typ {
	case "object":
		props, ok := m["properties"].(map[string]any)
		if !ok {
			// a free-form object, such as the values of entity_write: nothing more to check here
			return
		}
		if m["additionalProperties"] != false {
			t.Errorf("%s: additionalProperties is not false", path)
		}
		req, _ := m["required"].([]any)
		for _, r := range req {
			if _, ok := props[asStr(t, r)]; !ok {
				t.Errorf("%s: required %v is not a property", path, r)
			}
		}
		for k, p := range props {
			checkSchema(t, path+"."+k, p)
		}
	case "array":
		if _, ok := m["items"]; !ok {
			t.Errorf("%s: array without items", path)
		}
		checkSchema(t, path+"[]", m["items"])
	case "string", "integer", "number", "boolean":
		if e, ok := m["enum"]; ok {
			if list, ok := e.([]any); !ok || len(list) == 0 {
				t.Errorf("%s: enum is empty or not a list", path)
			}
		}
	default:
		t.Errorf("%s: unknown type %v", path, typ)
	}
}

func TestSchemaOfSpaceTypes(t *testing.T) {
	// The type schema of an existing space's type is the kernel's values schema, which must be well formed.
	s := start(t, store.Open(""))
	defer s.finish()
	space := asStr(t, get(t, s.done("space_write", `{"template":"inventory","samples":true}`), "id"))
	for _, ty := range asList(t, get(t, s.done("schema", fmt.Sprintf(`{"space":%q}`, space)), "types")) {
		checkSchema(t, asStr(t, get(t, ty, "key")), get(t, ty, "values_schema"))
	}
}

// TestInvalidIDIsNotExecuted checks that a request whose id is not a string, a number or null is answered with an
// Invalid Request error and id null, and that the tool it names is not run.
func TestInvalidIDIsNotExecuted(t *testing.T) {
	s := start(t, store.Open(""))
	defer s.finish()
	space := asStr(t, get(t, s.done("space_write", `{"name":"保留"}`), "id"))

	m := s.call(fmt.Sprintf(`{"jsonrpc":"2.0","id":{"x":1},"method":"tools/call","params":{"name":"delete","arguments":{"space":%q}}}`, space))
	if got := get(t, m, "id"); got != nil {
		t.Errorf("id = %v, want null", got)
	}
	if code := get(t, m, "error", "code"); code != float64(-32600) {
		t.Errorf("code = %v, want -32600", code)
	}
	if _, ok := m["result"]; ok {
		t.Errorf("answer has a result: %v", m)
	}

	// The space is still there: the schema of it answers.
	if name := get(t, s.done("schema", fmt.Sprintf(`{"space":%q}`, space)), "name"); name != "保留" {
		t.Errorf("space after a refused delete = %v", name)
	}
}

// TestDeleteArguments checks that delete runs only the three shapes its description allows: one record alone, a space,
// or a type with its space. Every other combination, and every present-but-empty string, is refused and removes nothing.
func TestDeleteArguments(t *testing.T) {
	s := start(t, store.Open(""))
	defer s.finish()
	space := asStr(t, get(t, s.done("space_write", `{"name":"删除测试"}`), "id"))
	s.done("type_write", fmt.Sprintf(`{"space":%q,"type":{"key":"note","name":"笔记"}}`, space))
	rec := get(t, s.done("entity_write", fmt.Sprintf(`{"space":%q,"type":"note","title":"记"}`, space)), "id")

	refused := []string{
		`{}`,
		`{"type":"note"}`,
		`{"space":""}`,
		`{"space":"","type":"note"}`,
		`{"space":` + fmt.Sprintf("%q", space) + `,"type":""}`,
		`{"entity":""}`,
		`{"entity":"","space":` + fmt.Sprintf("%q", space) + `}`,
		`{"entity":` + fmt.Sprintf("%q", rec) + `,"type":"note"}`,
		`{"entity":` + fmt.Sprintf("%q", rec) + `,"space":` + fmt.Sprintf("%q", space) + `}`,
	}
	for _, args := range refused {
		if msg := s.failed("delete", args); msg == "" {
			t.Errorf("delete %s: empty refusal", args)
		}
	}

	// Nothing was removed: the record and its type are still there.
	sch := asMap(t, s.done("schema", fmt.Sprintf(`{"space":%q}`, space)))
	if types := asList(t, sch["types"]); len(types) != 1 {
		t.Errorf("types after refused deletes = %v", types)
	}
	if n := get(t, s.done("query", fmt.Sprintf(`{"id":%q}`, rec)), "total"); n != float64(1) {
		t.Errorf("record after refused deletes: total = %v", n)
	}

	// The three allowed shapes still work: a type with its space, then a record alone, then the space.
	if d := asMap(t, s.done("delete", fmt.Sprintf(`{"space":%q,"type":"note"}`, space))); d["deleted"] != "type" {
		t.Errorf("delete type = %v", d)
	}
	s.done("type_write", fmt.Sprintf(`{"space":%q,"type":{"key":"note","name":"笔记"}}`, space))
	rec = get(t, s.done("entity_write", fmt.Sprintf(`{"space":%q,"type":"note","title":"记"}`, space)), "id")
	if d := asMap(t, s.done("delete", fmt.Sprintf(`{"entity":%q}`, rec))); d["deleted"] != "entity" {
		t.Errorf("delete entity = %v", d)
	}
	if d := asMap(t, s.done("delete", fmt.Sprintf(`{"space":%q}`, space))); d["deleted"] != "space" {
		t.Errorf("delete space = %v", d)
	}
}
