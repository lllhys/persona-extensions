package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/household/internal/model"
	"github.com/nwverse/persona-extensions/extensions/household/internal/ops"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/store"
)

// shanghai is the hub's zone in these tests. The tools read the clock, so "today" is the day of the test run in it.
var shanghai = time.FixedZone("UTC+8", 8*3600)

func today() model.Day { return model.DayOf(time.Now(), shanghai) }

// wantTools is the order the server lists its tools in.
var wantTools = []string{
	"household_status", "household_list", "household_meal_suggest", "household_save", "household_use",
	"household_restock", "household_purchase", "household_chore_done", "household_remove",
}

// readTools are the tools that never change the data; the manifest allows exactly these.
var readTools = map[string]bool{"household_status": true, "household_list": true, "household_meal_suggest": true}

// destructiveTools are the tools that overwrite stored data or delete a record (destructiveHint). Restock only adds
// purchases, so it is not destructive.
var destructiveTools = map[string]bool{
	"household_save": true, "household_use": true, "household_purchase": true,
	"household_chore_done": true, "household_remove": true,
}

// wantRequired is the required arguments of each tool.
var wantRequired = map[string][]string{
	"household_status":       nil,
	"household_list":         {"type"},
	"household_meal_suggest": nil,
	"household_save":         nil,
	"household_use":          {"id", "amount"},
	"household_restock":      nil,
	"household_purchase":     {"ids"},
	"household_chore_done":   {"id"},
	"household_remove":       {"id"},
}

// ---- JSON helpers ----

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

// records is the list of records of a list answer.
func records(t *testing.T, v any) []any { return asList(t, get(t, v, "records")) }

// seeded is a store in a fresh directory; with samples it holds the sample set of the household.
func seeded(t *testing.T, samples bool) kernel.Storage {
	t.Helper()
	st := store.Open(t.TempDir())
	if samples {
		err := st.Update(func(b *kernel.Base, now time.Time) error {
			_, err := ops.LoadSamples(b, now, shanghai)
			return err
		})
		if err != nil {
			t.Fatalf("load samples: %v", err)
		}
	}
	return st
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
		err := Serve(context.Background(), inR, outW, st, shanghai)
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

// write sends one line that gets no answer.
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
		s.t.Fatalf("%s %s: want a tool error, got %v", name, args, res)
	}
	return asStr(s.t, get(s.t, res, "content", 0, "text"))
}

// rec returns the record of the type with the title, as household_list answers it.
func rec(t *testing.T, s *session, typ, title string) map[string]any {
	t.Helper()
	for _, r := range records(t, s.done("household_list", fmt.Sprintf(`{"type":%q}`, typ))) {
		if asStr(t, get(t, r, "title")) == title {
			return asMap(t, r)
		}
	}
	t.Fatalf("no %s titled %q", typ, title)
	return nil
}

// idOf is the id of the record of the type with the title.
func idOf(t *testing.T, s *session, typ, title string) string {
	t.Helper()
	return asStr(t, rec(t, s, typ, title)["id"])
}

// ---- protocol ----

func TestInitialize(t *testing.T) {
	s := start(t, seeded(t, false))
	defer s.finish()
	res := get(t, s.rpc("initialize", `{}`), "result")
	if got := get(t, res, "protocolVersion"); got != "2025-06-18" {
		t.Errorf("protocolVersion = %v", got)
	}
	if got := get(t, res, "serverInfo", "name"); got != "household" {
		t.Errorf("server name = %v", got)
	}
	if _, ok := asMap(t, get(t, res, "capabilities"))["tools"]; !ok {
		t.Error("no tools capability")
	}
}

func TestPing(t *testing.T) {
	s := start(t, seeded(t, false))
	defer s.finish()
	if res := get(t, s.rpc("ping", `{}`), "result"); !reflect.DeepEqual(res, map[string]any{}) {
		t.Errorf("ping result = %v", res)
	}
}

func TestToolTable(t *testing.T) {
	tools := Tools()
	if len(tools) != len(wantTools) {
		t.Fatalf("%d tools, want %d", len(tools), len(wantTools))
	}
	for i, tl := range tools {
		if tl.Name != wantTools[i] {
			t.Errorf("tool %d = %s, want %s", i, tl.Name, wantTools[i])
		}
		if tl.About == "" || tl.Input == nil || tl.Run == nil {
			t.Errorf("%s: incomplete table entry", tl.Name)
		}
		if tl.Read != readTools[tl.Name] {
			t.Errorf("%s: Read = %v", tl.Name, tl.Read)
		}
	}
	// Tools returns a copy: changing it does not change the table.
	tools[0].Name = "changed"
	if Tools()[0].Name != wantTools[0] {
		t.Error("Tools shares the table")
	}
}

func TestToolsList(t *testing.T) {
	s := start(t, seeded(t, false))
	defer s.finish()
	list := asList(t, get(t, s.rpc("tools/list", `{}`), "result", "tools"))
	var names []string
	for _, item := range list {
		m := asMap(t, item)
		name := asStr(t, m["name"])
		names = append(names, name)
		if strings.TrimSpace(asStr(t, m["description"])) == "" {
			t.Errorf("%s: no description", name)
		}
		ann := asMap(t, m["annotations"])
		if ann["readOnlyHint"] != readTools[name] {
			t.Errorf("%s: readOnlyHint = %v", name, ann["readOnlyHint"])
		}
		if ann["destructiveHint"] != destructiveTools[name] {
			t.Errorf("%s: destructiveHint = %v", name, ann["destructiveHint"])
		}
		checkSchema(t, name, asMap(t, m["inputSchema"]))
	}
	if !reflect.DeepEqual(names, wantTools) {
		t.Errorf("tools = %v, want %v", names, wantTools)
	}
}

// checkSchema checks that a tool's input schema is a flat JSON Schema object: every property has a type or an enum and
// a description, the required list names listed properties only, and unknown properties are refused.
func checkSchema(t *testing.T, name string, s map[string]any) {
	t.Helper()
	if s["type"] != "object" {
		t.Errorf("%s: schema type = %v", name, s["type"])
	}
	if s["additionalProperties"] != false {
		t.Errorf("%s: additionalProperties = %v", name, s["additionalProperties"])
	}
	props := asMap(t, s["properties"])
	for key, v := range props {
		p := asMap(t, v)
		_, hasType := p["type"]
		_, hasEnum := p["enum"]
		if !hasType && !hasEnum {
			t.Errorf("%s.%s: no type", name, key)
		}
		if d, _ := p["description"].(string); d == "" {
			t.Errorf("%s.%s: no description", name, key)
		}
	}
	var required []string
	if r, ok := s["required"]; ok {
		for _, k := range asList(t, r) {
			key := asStr(t, k)
			if _, ok := props[key]; !ok {
				t.Errorf("%s: required %q is not a property", name, key)
			}
			required = append(required, key)
		}
	}
	if !slices.Equal(required, wantRequired[name]) {
		t.Errorf("%s: required = %v, want %v", name, required, wantRequired[name])
	}
	if _, err := json.Marshal(s); err != nil {
		t.Errorf("%s: schema does not marshal: %v", name, err)
	}
}

func TestMalformedAndUnknown(t *testing.T) {
	s := start(t, seeded(t, false))
	defer s.finish()
	cases := []struct {
		line string
		code float64
		id   any // the id the answer carries; nil is null
	}{
		{`{"jsonrpc":"2.0","id":1,"method":`, -32700, nil},
		{`{"jsonrpc":"2.0","id":5,"method":"nope"}`, -32601, float64(5)},
		{`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"household_nothing"}}`, -32602, float64(6)},
		{`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":"x"}`, -32602, float64(7)},
		{`{"jsonrpc":"2.0","id":{"a":1},"method":"ping"}`, -32600, nil},
		{`{"id":8,"method":"ping"}`, -32600, float64(8)},
	}
	for _, c := range cases {
		m := s.call(c.line)
		if got := get(t, m, "error", "code"); got != c.code {
			t.Errorf("%s: code = %v, want %v", c.line, got, c.code)
		}
		if got := get(t, m, "id"); got != c.id {
			t.Errorf("%s: id = %v, want %v", c.line, got, c.id)
		}
	}
}

func TestNotificationsAreNotAnswered(t *testing.T) {
	s := start(t, seeded(t, false))
	defer s.finish()
	// None of these lines gets an answer, and the write is never applied.
	s.write(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"household_save","arguments":{"type":"place","title":"不应创建"}}}`)
	s.write(`{"jsonrpc":"2.0","method":"ping"}`)
	s.write("   ")
	// The next answer is the one to this request.
	m := s.rpc("tools/call", `{"name":"household_list","arguments":{"type":"place"}}`)
	if recs := records(t, get(t, m, "result", "structuredContent")); len(recs) != 0 {
		t.Errorf("places = %v: a notification was applied", recs)
	}
}

func TestServeLineHandling(t *testing.T) {
	st := seeded(t, false)
	ping := `{"jsonrpc":"2.0","id":2,"method":"ping"}`

	// A last line without a newline is answered.
	var buf bytes.Buffer
	if err := Serve(context.Background(), strings.NewReader(ping), &buf, st, shanghai); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if got := buf.String(); got != `{"jsonrpc":"2.0","id":2,"result":{}}`+"\n" {
		t.Errorf("answer = %q", got)
	}

	// A canceled context stops before reading.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Serve(ctx, strings.NewReader(ping+"\n"), io.Discard, st, shanghai); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Serve = %v", err)
	}

	// A read error ends the loop with that error, after the lines before it are answered.
	buf.Reset()
	in := io.MultiReader(strings.NewReader(ping+"\n"), errReader{})
	if err := Serve(context.Background(), in, &buf, st, shanghai); err == nil || err.Error() != "read failed" {
		t.Errorf("read error = %v", err)
	}
	if !strings.Contains(buf.String(), `"id":2`) {
		t.Errorf("answer before the read error is missing: %q", buf.String())
	}

	// A write error ends the loop with that error.
	if err := Serve(context.Background(), strings.NewReader(ping+"\n"), failWriter{}, st, shanghai); err == nil || err.Error() != "write failed" {
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
	return fn(&kernel.Base{Format: 1}, time.Now())
}

func (f fakeStore) Changed() bool { return false }

func TestStorageFailures(t *testing.T) {
	reads := start(t, fakeStore{viewErr: errors.New("disk gone")})
	if msg := reads.failed("household_status", `{}`); msg != "disk gone" {
		t.Errorf("read failure = %q", msg)
	}
	reads.finish()

	writes := start(t, fakeStore{updErr: errors.New("locked")})
	if msg := writes.failed("household_save", `{"type":"place","title":"x"}`); msg != "locked" {
		t.Errorf("write failure = %q", msg)
	}
	writes.finish()
}

// ---- read tools ----

func TestReadToolsOnEmptyStore(t *testing.T) {
	s := start(t, seeded(t, false))
	defer s.finish()
	st := s.done("household_status", `{}`)
	if got := get(t, st, "summary", "alerts"); got != float64(0) {
		t.Errorf("alerts = %v", got)
	}
	for _, key := range []string{"findings", "purchases", "chores_due", "meals"} {
		if got := get(t, st, key); !reflect.DeepEqual(got, []any{}) {
			t.Errorf("%s = %v, want an empty list", key, got)
		}
	}
	for _, typ := range []string{model.TPlace, model.TItem, model.TBuy, model.TChore, model.TRecipe, model.TMeal} {
		if recs := records(t, s.done("household_list", fmt.Sprintf(`{"type":%q}`, typ))); len(recs) != 0 {
			t.Errorf("%s: %d records on an empty store", typ, len(recs))
		}
	}
	if ideas := asList(t, get(t, s.done("household_meal_suggest", `{}`), "ideas")); len(ideas) != 0 {
		t.Errorf("ideas = %v", ideas)
	}
}

func TestStatusWithSamples(t *testing.T) {
	s := start(t, seeded(t, true))
	defer s.finish()
	st := s.done("household_status", `{}`)
	if got := get(t, st, "today"); got != string(today()) {
		t.Errorf("today = %v", got)
	}

	want := map[string]string{
		"纯牛奶": "expired", "鸡蛋": "out", "酱油": "low", "纸尿裤": "runout", "酸奶": "expiring", "生菜": "expiring",
	}
	got := map[string]string{}
	for _, f := range asList(t, get(t, st, "findings")) {
		got[asStr(t, get(t, f, "title"))] = asStr(t, get(t, f, "finding"))
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings = %v, want %v", got, want)
	}

	sum := get(t, st, "summary")
	for key, n := range map[string]float64{
		"expired": 1, "out": 1, "low": 1, "runout": 1, "expiring": 2, "open_buys": 1, "chores_due": 2, "alerts": 6,
	} {
		if v := get(t, sum, key); v != n {
			t.Errorf("summary.%s = %v, want %v", key, v, n)
		}
	}
	if got := get(t, sum, "tone"); got != "danger" {
		t.Errorf("tone = %v", got)
	}

	purchases := asList(t, get(t, st, "purchases"))
	if len(purchases) != 1 || get(t, purchases[0], "title") != "洗衣液" || get(t, purchases[0], "status") != "待买" {
		t.Errorf("purchases = %v", purchases)
	}

	chores := asList(t, get(t, st, "chores_due"))
	if len(chores) != 2 || get(t, chores[0], "title") != "拖地" || get(t, chores[0], "overdue") != float64(2) ||
		get(t, chores[1], "title") != "整理冰箱" {
		t.Errorf("chores_due = %v", chores)
	}

	meals := asList(t, get(t, st, "meals"))
	if len(meals) != 1 || get(t, meals[0], "title") != "番茄炒蛋" || get(t, meals[0], "slot") != "晚餐" ||
		get(t, meals[0], "status") != "计划" {
		t.Errorf("meals = %v", meals)
	}
}

func TestListWithSamples(t *testing.T) {
	s := start(t, seeded(t, true))
	defer s.finish()
	counts := map[string]int{
		model.TPlace: 3, model.TItem: 10, model.TBuy: 1, model.TChore: 3, model.TRecipe: 3, model.TMeal: 2,
	}
	for typ, n := range counts {
		if got := len(records(t, s.done("household_list", fmt.Sprintf(`{"type":%q}`, typ)))); got != n {
			t.Errorf("%s: %d records, want %d", typ, got, n)
		}
	}

	// An item comes with its estimate, its finding and the name of its place.
	diaper := rec(t, s, model.TItem, "纸尿裤")
	if get(t, diaper, "finding") != "runout" || get(t, diaper, "label") != "用量预警" || get(t, diaper, "est") != float64(25) {
		t.Errorf("diaper = %v", diaper)
	}
	if get(t, diaper, "place") != "杂物柜" {
		t.Errorf("diaper place = %v", get(t, diaper, "place"))
	}

	// The text narrows the list to the titles that contain it.
	only := records(t, s.done("household_list", `{"type":"item","text":"纸"}`))
	if len(only) != 1 || get(t, only[0], "title") != "纸尿裤" {
		t.Errorf("text filter = %v", only)
	}
	if none := records(t, s.done("household_list", `{"type":"item","text":"不存在"}`)); len(none) != 0 {
		t.Errorf("no match = %v", none)
	}

	// The place of a record is counted with its items.
	if got := get(t, rec(t, s, model.TPlace, "厨房冰箱"), "items"); got != float64(5) {
		t.Errorf("items in 厨房冰箱 = %v", got)
	}

	// A recipe lists its needs, by id and title.
	needs := asList(t, get(t, rec(t, s, model.TRecipe, "蒜蓉生菜"), "needs"))
	if len(needs) != 2 || get(t, needs[0], "title") != "生菜" {
		t.Errorf("needs = %v", needs)
	}

	// A list needs a known type.
	if msg := s.failed("household_list", `{}`); msg == "" {
		t.Error("list without a type has no message")
	}
	if msg := s.failed("household_list", `{"type":"bogus"}`); msg == "" {
		t.Error("list of an unknown type has no message")
	}
}

func TestMealSuggest(t *testing.T) {
	s := start(t, seeded(t, true))
	defer s.finish()
	ideas := asList(t, get(t, s.done("household_meal_suggest", `{}`), "ideas"))
	if len(ideas) != 3 {
		t.Fatalf("%d ideas, want 3", len(ideas))
	}
	// Every ingredient at hand, and one of them expiring soon, comes first.
	if got := get(t, ideas[0], "recipe", "title"); got != "蒜蓉生菜" {
		t.Errorf("first idea = %v", got)
	}
	if got := get(t, ideas[0], "use_first"); !reflect.DeepEqual(got, []any{"生菜"}) {
		t.Errorf("use_first = %v", got)
	}
	// The egg is gone, so the tomato and egg dish lists it as missing.
	last := ideas[2]
	if got := get(t, last, "recipe", "title"); got != "番茄炒蛋" {
		t.Errorf("last idea = %v", got)
	}
	if !reflect.DeepEqual(get(t, last, "missing"), []any{"鸡蛋"}) || get(t, last, "have") != float64(1) ||
		get(t, last, "total") != float64(2) {
		t.Errorf("番茄炒蛋 = %v", last)
	}
	if got := asList(t, get(t, s.done("household_meal_suggest", `{"limit":1}`), "ideas")); len(got) != 1 {
		t.Errorf("limit 1 gave %d ideas", len(got))
	}
	if msg := s.failed("household_meal_suggest", `{"limit":"two"}`); msg == "" {
		t.Error("a limit that is not a number has no message")
	}
}

// ---- write tools ----

func TestSaveCreatesAndChanges(t *testing.T) {
	s := start(t, seeded(t, false))
	defer s.finish()

	place := s.done("household_save", `{"type":"place","title":"冷冻柜","values":{"note":"冰箱下层"}}`)
	if get(t, place, "type") != "place" || get(t, place, "record", "title") != "冷冻柜" {
		t.Fatalf("created place = %v", place)
	}
	placeID := asStr(t, get(t, place, "record", "id"))

	item := s.done("household_save", fmt.Sprintf(
		`{"type":"item","title":"冷冻饺子","values":{"category":"食材","place":%q,"qty":2,"unit":"袋","expires":%q}}`,
		placeID, today().Add(30)))
	itemID := asStr(t, get(t, item, "record", "id"))
	if get(t, item, "record", "place") != "冷冻柜" || get(t, item, "record", "counted") != string(today()) {
		t.Errorf("created item = %v", item)
	}
	if get(t, item, "record", "est") != float64(2) {
		t.Errorf("estimate = %v", get(t, item, "record", "est"))
	}

	// A change merges the values; null clears one, and a count is not needed to keep the others.
	changed := s.done("household_save", fmt.Sprintf(
		`{"id":%q,"title":"冷冻水饺","values":{"note":"冷冻保存","qty":3,"place":null}}`, itemID))
	if get(t, changed, "record", "title") != "冷冻水饺" || get(t, changed, "record", "qty") != float64(3) {
		t.Errorf("changed item = %v", changed)
	}
	if get(t, changed, "record", "note") != "冷冻保存" {
		t.Errorf("note = %v", get(t, changed, "record", "note"))
	}
	if _, ok := asMap(t, asMap(t, changed)["record"])["place"]; ok {
		t.Error("place was not cleared")
	}
	if got := get(t, changed, "record", "expires"); got != string(today().Add(30)) {
		t.Errorf("expires = %v: a change must keep the other values", got)
	}
}

func TestSaveErrors(t *testing.T) {
	s := start(t, seeded(t, true))
	defer s.finish()
	for name, args := range map[string]string{
		"no id and no type":       `{"title":"没有类型"}`,
		"unknown type":            `{"type":"nothing","title":"x"}`,
		"value of the wrong kind": fmt.Sprintf(`{"id":%q,"values":{"qty":"很多"}}`, idOf(t, s, model.TItem, "大米")),
		"unknown id":              `{"id":"e999","title":"x"}`,
		"unknown argument":        `{"type":"place","title":"x","colour":"red"}`,
	} {
		if msg := s.failed("household_save", args); msg == "" {
			t.Errorf("%s: no message", name)
		}
	}
	// Nothing was saved by the failed calls.
	if n := len(records(t, s.done("household_list", `{"type":"place"}`))); n != 3 {
		t.Errorf("places = %d after failed saves", n)
	}
}

func TestUse(t *testing.T) {
	s := start(t, seeded(t, true))
	defer s.finish()
	milk := idOf(t, s, model.TItem, "纯牛奶")

	used := s.done("household_use", fmt.Sprintf(`{"id":%q,"amount":1}`, milk))
	if get(t, used, "record", "qty") != float64(1) || get(t, used, "record", "counted") != string(today()) {
		t.Errorf("after use = %v", used)
	}
	// Using more than is there leaves nothing, never less than zero.
	if got := get(t, s.done("household_use", fmt.Sprintf(`{"id":%q,"amount":5}`, milk)), "record", "qty"); got != float64(0) {
		t.Errorf("qty after over-use = %v", got)
	}

	for name, args := range map[string]string{
		"no amount":        fmt.Sprintf(`{"id":%q}`, milk),
		"amount of zero":   fmt.Sprintf(`{"id":%q,"amount":0}`, milk),
		"item without qty": fmt.Sprintf(`{"id":%q,"amount":1}`, idOf(t, s, model.TItem, "创可贴")),
		"unknown id":       `{"id":"e999","amount":1}`,
		"place given":      fmt.Sprintf(`{"id":%q,"amount":1}`, idOf(t, s, model.TPlace, "厨房柜")),
		"no id":            `{"amount":1}`,
	} {
		if msg := s.failed("household_use", args); msg == "" {
			t.Errorf("%s: no message", name)
		}
	}
}

func TestRestockAndPurchase(t *testing.T) {
	s := start(t, seeded(t, true))
	defer s.finish()

	created := s.done("household_restock", `{}`)
	recs := records(t, created)
	titles := map[string]string{}
	amounts := map[string]float64{}
	for _, r := range recs {
		titles[asStr(t, get(t, r, "title"))] = asStr(t, get(t, r, "reason"))
		amounts[asStr(t, get(t, r, "title"))] = get(t, r, "amount").(float64)
	}
	want := map[string]string{"纯牛奶": "已过期", "鸡蛋": "已用完", "酱油": "低库存", "纸尿裤": "用量预警"}
	if !reflect.DeepEqual(titles, want) {
		t.Errorf("restock purchases = %v, want %v", titles, want)
	}
	if amounts["纸尿裤"] != 40 || amounts["鸡蛋"] != 1 {
		t.Errorf("amounts = %v", amounts)
	}
	if again := records(t, s.done("household_restock", `{}`)); len(again) != 0 {
		t.Errorf("second restock created %d purchases", len(again))
	}

	// Bought: the item is stocked with the amount, counted today.
	egg := s.done("household_purchase", fmt.Sprintf(`{"ids":[%q]}`, idOf(t, s, model.TBuy, "鸡蛋")))
	if get(t, egg, "records", 0, "status") != "已买" {
		t.Errorf("bought purchase = %v", egg)
	}
	if got := rec(t, s, model.TItem, "鸡蛋"); get(t, got, "qty") != float64(1) || get(t, got, "counted") != string(today()) {
		t.Errorf("egg after purchase = %v", got)
	}

	// Bought after its date passed: the old batch is thrown away, so the stock is the amount alone, and the old expiry is
	// cleared.
	s.done("household_purchase", fmt.Sprintf(`{"ids":[%q],"bought":true}`, idOf(t, s, model.TBuy, "纯牛奶")))
	milk := rec(t, s, model.TItem, "纯牛奶")
	if get(t, milk, "qty") != amounts["纯牛奶"] {
		t.Errorf("milk qty = %v, want the amount %v alone", get(t, milk, "qty"), amounts["纯牛奶"])
	}
	if _, ok := milk["expires"]; ok {
		t.Errorf("milk kept its past expiry: %v", milk["expires"])
	}

	// The diaper estimate is 40 less 5 a day for three days, then the refill of 40.
	s.done("household_purchase", fmt.Sprintf(`{"ids":[%q]}`, idOf(t, s, model.TBuy, "纸尿裤")))
	if got := get(t, rec(t, s, model.TItem, "纸尿裤"), "qty"); got != float64(65) {
		t.Errorf("diaper qty = %v, want 65", got)
	}

	// Not bought: the purchase is closed and the stock stays.
	skipped := s.done("household_purchase", fmt.Sprintf(`{"ids":[%q],"bought":false}`, idOf(t, s, model.TBuy, "酱油")))
	if get(t, skipped, "records", 0, "status") != "不买" {
		t.Errorf("skipped purchase = %v", skipped)
	}
	if got := get(t, rec(t, s, model.TItem, "酱油"), "qty"); got != float64(1) {
		t.Errorf("soy qty after 不买 = %v", got)
	}

	// A closed purchase cannot be closed again, and an empty or unknown list is refused.
	closed := idOf(t, s, model.TBuy, "鸡蛋")
	if msg := s.failed("household_purchase", fmt.Sprintf(`{"ids":[%q]}`, closed)); msg == "" {
		t.Error("closing a bought purchase again has no message")
	}
	if msg := s.failed("household_purchase", `{"ids":[]}`); msg == "" {
		t.Error("empty ids have no message")
	}
	if msg := s.failed("household_purchase", `{"ids":["e999"]}`); msg == "" {
		t.Error("unknown purchase has no message")
	}
}

func TestPurchaseIsAllOrNothing(t *testing.T) {
	s := start(t, seeded(t, true))
	defer s.finish()
	s.done("household_restock", `{}`)
	egg := idOf(t, s, model.TBuy, "鸡蛋")
	s.failed("household_purchase", fmt.Sprintf(`{"ids":[%q,"e999"]}`, egg))
	if got := rec(t, s, model.TBuy, "鸡蛋"); get(t, got, "status") != "待买" {
		t.Errorf("purchase closed by a failed call: %v", got)
	}
}

// A repeated id is processed once: the item is stocked once and the answer lists the purchase once.
func TestPurchaseRepeatedIDs(t *testing.T) {
	s := start(t, seeded(t, true))
	defer s.finish()
	s.done("household_restock", `{}`)
	egg := idOf(t, s, model.TBuy, "鸡蛋")
	out := s.done("household_purchase", fmt.Sprintf(`{"ids":[%q,%q]}`, egg, egg))
	if n := len(records(t, out)); n != 1 {
		t.Errorf("answer lists %d records, want 1", n)
	}
	if get(t, out, "records", 0, "status") != "已买" {
		t.Errorf("purchase = %v", out)
	}
	if got := rec(t, s, model.TItem, "鸡蛋"); get(t, got, "qty") != float64(1) {
		t.Errorf("egg qty after a repeated id = %v, want 1", get(t, got, "qty"))
	}
}

func TestChoreDone(t *testing.T) {
	s := start(t, seeded(t, true))
	defer s.finish()
	mop := idOf(t, s, model.TChore, "拖地")
	done := s.done("household_chore_done", fmt.Sprintf(`{"id":%q}`, mop))
	if get(t, done, "record", "last") != string(today()) || get(t, done, "record", "next") != string(today().Add(7)) {
		t.Errorf("after chore done = %v", done)
	}

	// A chore without a period is a one-off: its next date is cleared.
	s.done("household_save", `{"type":"chore","title":"换灯泡"}`)
	once := idOf(t, s, model.TChore, "换灯泡")
	if got := rec(t, s, model.TChore, "换灯泡"); get(t, got, "next") != string(today()) {
		t.Errorf("new chore next = %v, want today", get(t, got, "next"))
	}
	res := s.done("household_chore_done", fmt.Sprintf(`{"id":%q}`, once))
	if _, ok := asMap(t, res)["record"].(map[string]any)["next"]; ok {
		t.Errorf("one-off chore kept a next date: %v", res)
	}

	if msg := s.failed("household_chore_done", fmt.Sprintf(`{"id":%q}`, idOf(t, s, model.TItem, "纯牛奶"))); msg == "" {
		t.Error("chore done on an item has no message")
	}
	if msg := s.failed("household_chore_done", `{}`); msg == "" {
		t.Error("chore done without an id has no message")
	}
}

func TestRemove(t *testing.T) {
	s := start(t, seeded(t, true))
	defer s.finish()
	cupboard := idOf(t, s, model.TPlace, "厨房柜")
	res := s.done("household_remove", fmt.Sprintf(`{"id":%q}`, cupboard))
	if get(t, res, "removed") != cupboard || get(t, res, "title") != "厨房柜" {
		t.Errorf("removed = %v", res)
	}
	// The relations that pointed to the place lost it.
	if _, ok := rec(t, s, model.TItem, "食用油")["place"]; ok {
		t.Error("item still points to the removed place")
	}
	if msg := s.failed("household_remove", `{"id":"e999"}`); msg == "" {
		t.Error("removing an unknown record has no message")
	}
	if msg := s.failed("household_remove", `{}`); msg == "" {
		t.Error("removing without an id has no message")
	}
}

func TestPlanMealAndSuggestionsWithSave(t *testing.T) {
	s := start(t, seeded(t, true))
	defer s.finish()
	porridge := idOf(t, s, model.TRecipe, "白粥")
	s.done("household_save", fmt.Sprintf(
		`{"type":"meal","title":"白粥","values":{"date":%q,"slot":"晚餐","recipe":%q}}`, today(), porridge))
	// Tonight has the tomato and egg dish and this porridge, both planned.
	meals := asList(t, get(t, s.done("household_status", `{}`), "meals"))
	if len(meals) != 2 {
		t.Errorf("planned meals today = %d, want 2", len(meals))
	}
}

func TestToolArgumentsAreChecked(t *testing.T) {
	s := start(t, seeded(t, true))
	defer s.finish()
	if msg := s.failed("household_status", `{"today":"2026-01-01"}`); msg == "" {
		t.Error("an unknown argument has no message")
	}
	if msg := s.failed("household_remove", `{"id":["e1"]}`); msg == "" {
		t.Error("an id that is not a string has no message")
	}
}
