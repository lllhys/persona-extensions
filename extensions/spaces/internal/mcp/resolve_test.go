package mcp

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/spaces/internal/templates"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/store"
)

// memStore is a kernel.Storage held in memory that gives every write the time t0, so that answers are byte for byte
// the same on every run. Each call works on a fresh decoded and checked copy, as the file store does; a write replaces
// the stored bytes only when it and the check succeed.
type memStore struct{ data []byte }

func (m *memStore) base() *kernel.Base {
	b := &kernel.Base{Format: 1}
	if m.data != nil {
		_ = json.Unmarshal(m.data, b)
		_ = b.Check() // normalizes the decoded values, as a load does
	}
	return b
}

func (m *memStore) View(fn func(b *kernel.Base) error) error { return fn(m.base()) }

func (m *memStore) Update(fn func(b *kernel.Base, now time.Time) error) error {
	b := m.base()
	if err := fn(b, t0); err != nil {
		return err
	}
	if err := b.Check(); err != nil {
		return err
	}
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	m.data = data
	return nil
}

func (m *memStore) Changed() bool { return false }

// projectSpace starts a session on st with a space made from the project template with its samples.
func projectSpace(t *testing.T, st kernel.Storage) (*session, string) {
	t.Helper()
	s := start(t, st)
	return s, asStr(t, get(t, s.done("space_write", `{"template":"project","samples":true}`), "id"))
}

// idByTitle is the id of the record with the title among the records of a type, found through query.
func (s *session) idByTitle(space, typ, title string) string {
	s.t.Helper()
	for _, e := range asList(s.t, get(s.t, s.done("query", fmt.Sprintf(`{"space":%q,"type":%q,"limit":200}`, space, typ)), "entities")) {
		if asMap(s.t, e)["title"] == title {
			return asStr(s.t, asMap(s.t, e)["id"])
		}
	}
	s.t.Fatalf("no %s titled %q", typ, title)
	return ""
}

// projectTypes are the types of the project template as the refusals list them: 「key（名称）」.
func projectTypes(t *testing.T) string {
	t.Helper()
	p, ok := templates.Get("project")
	if !ok {
		t.Fatalf("no project template")
	}
	var parts []string
	for _, ty := range p.Types {
		parts = append(parts, ty.Key+"（"+ty.Name+"）")
	}
	return strings.Join(parts, "、")
}

// keysOf are the sorted member names of a decoded JSON object.
func keysOf(t *testing.T, v any) []string {
	t.Helper()
	var keys []string
	for k := range asMap(t, v) {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// wantError checks that a tool call fails with exactly the message.
func (s *session) wantError(name, args, msg string) {
	s.t.Helper()
	if got := s.failed(name, args); got != msg {
		s.t.Errorf("%s %s:\n got %q\nwant %q", name, args, got, msg)
	}
}

func TestSpaceByIDOrName(t *testing.T) {
	s := start(t, store.Open(""))
	defer s.finish()

	// No space yet: the refusals point to space_write.
	s.wantError("query", `{}`, "还没有空间；先用 space_write 新建一个")
	s.wantError("query", `{"space":"读书"}`, "没有空间「读书」；还没有空间，先用 space_write 新建一个")

	project := asStr(t, get(t, s.done("space_write", `{"template":"project","samples":true}`), "id"))

	// The name works wherever the id does.
	if id := get(t, s.done("schema", `{"space":"项目与目标"}`), "id"); id != project {
		t.Errorf("schema by name: id = %v, want %v", id, project)
	}
	if n := get(t, s.done("query", `{"space":"项目与目标","type":"goal"}`), "total"); n != float64(2) {
		t.Errorf("query by name: total = %v", n)
	}
	s.done("type_write", `{"space":"项目与目标","type":{"key":"memo","name":"备忘"}}`)
	memo := asStr(t, get(t, s.done("entity_write", `{"space":"项目与目标","type":"memo","title":"按名称写入"}`), "id"))
	if got := get(t, s.done("query", fmt.Sprintf(`{"space":"项目与目标","id":%q}`, memo)), "total"); got != float64(1) {
		t.Errorf("query of one record with the space by name: total = %v", got)
	}

	// With several spaces, an omitted or unknown space lists them; a name two spaces share lists those two.
	other := asStr(t, get(t, s.done("space_write", `{"name":"另一个"}`), "id"))
	same := asStr(t, get(t, s.done("space_write", `{"name":"另一个"}`), "id"))
	all := fmt.Sprintf("项目与目标（%s）、另一个（%s）、另一个（%s）", project, other, same)
	s.wantError("query", `{}`, "有多个空间，请用 space 指定（id 或名称）。现有空间："+all)
	s.wantError("entity_write", `{"type":"memo","title":"x"}`, "有多个空间，请用 space 指定（id 或名称）。现有空间："+all)
	s.wantError("schema", `{"space":"读书"}`, "没有空间「读书」。现有空间："+all)
	s.wantError("type_write", `{"space":"读书","type":{"key":"x","name":"x"}}`, "没有空间「读书」。现有空间："+all)
	s.wantError("query", `{"space":"另一个"}`, fmt.Sprintf("有 2 个空间叫「另一个」，请用 id 指定：另一个（%s）、另一个（%s）", other, same))
	s.wantError("delete", `{"space":"另一个"}`, fmt.Sprintf("有 2 个空间叫「另一个」，请用 id 指定：另一个（%s）、另一个（%s）", other, same))

	// The refused delete removed nothing; a unique name deletes its space.
	if n := len(asList(t, get(t, s.done("schema", `{}`), "spaces"))); n != 3 {
		t.Errorf("spaces after a refused delete = %d", n)
	}
	if d := asMap(t, s.done("delete", `{"space":"项目与目标"}`)); d["deleted"] != "space" || d["id"] != project {
		t.Errorf("delete by name = %v", d)
	}
	// An id is taken before a name: the space with id other is meant even if another space were named so.
	if d := asMap(t, s.done("delete", fmt.Sprintf(`{"space":%q}`, other))); d["id"] != other {
		t.Errorf("delete by id = %v", d)
	}
}

func TestTypeByKeyOrName(t *testing.T) {
	s, space := projectSpace(t, store.Open(""))
	defer s.finish()
	types := projectTypes(t)

	// entity_write, query, entity_derive and delete take a type by its name.
	task := asMap(t, s.done("entity_write", fmt.Sprintf(`{"space":%q,"type":"任务","title":"按名称"}`, space)))
	if task["type"] != "task" {
		t.Errorf("type by name = %v", task["type"])
	}
	byKey := get(t, s.done("query", fmt.Sprintf(`{"space":%q,"type":"task"}`, space)), "total")
	if byName := get(t, s.done("query", fmt.Sprintf(`{"space":%q,"type":"任务"}`, space)), "total"); byName != byKey {
		t.Errorf("query by name: total = %v, by key %v", byName, byKey)
	}
	derived := get(t, s.done("entity_derive", fmt.Sprintf(`{"id":%q,"type":"记录"}`, task["id"])), "entity")
	if asMap(t, derived)["type"] != "note" {
		t.Errorf("derive by name: type = %v", asMap(t, derived)["type"])
	}
	// A change may name the record's own type by its name; another type is still refused by the kernel.
	s.done("entity_write", fmt.Sprintf(`{"id":%q,"type":"任务","title":"改名"}`, task["id"]))
	s.wantError("entity_write", fmt.Sprintf(`{"id":%q,"type":"目标"}`, task["id"]), "记录的类型在创建时指定，之后不能改")

	// An unknown type lists the space's types; a create without a type asks for one.
	s.wantError("entity_write", fmt.Sprintf(`{"space":%q,"type":"nope","title":"x"}`, space), "没有类型「nope」。可选："+types)
	s.wantError("entity_write", fmt.Sprintf(`{"space":%q,"title":"x"}`, space), "新建记录需要 type。可选："+types)
	s.wantError("query", fmt.Sprintf(`{"space":%q,"type":"nope"}`, space), "没有类型「nope」。可选："+types)
	s.wantError("entity_derive", fmt.Sprintf(`{"id":%q,"type":"nope"}`, task["id"]), "没有类型「nope」。可选："+types)
	s.wantError("delete", fmt.Sprintf(`{"space":%q,"type":"nope"}`, space), "没有类型「nope」。可选："+types)

	// Delete a type by its name: the answer names its key.
	if d := asMap(t, s.done("delete", fmt.Sprintf(`{"space":%q,"type":"记录"}`, space))); d["type"] != "note" {
		t.Errorf("delete type by name = %v", d)
	}

	// A space with one type creates without a type; a space without types says how to get one.
	one := asStr(t, get(t, s.done("space_write", `{"name":"单类型"}`), "id"))
	s.wantError("entity_write", fmt.Sprintf(`{"space":%q,"title":"x"}`, one), "空间「单类型」还没有类型，先用 type_write 定义一个")
	s.wantError("query", fmt.Sprintf(`{"space":%q,"type":"x"}`, one), "没有类型「x」；空间「单类型」还没有类型，先用 type_write 定义一个")
	s.done("type_write", fmt.Sprintf(`{"space":%q,"type":{"key":"memo","name":"备忘"}}`, one))
	if typ := get(t, s.done("entity_write", fmt.Sprintf(`{"space":%q,"title":"默认类型"}`, one)), "type"); typ != "memo" {
		t.Errorf("default type = %v", typ)
	}

	// Two types with one name: the name alone is refused, with their keys.
	s.done("type_write", fmt.Sprintf(`{"space":%q,"type":{"key":"memo2","name":"备忘"}}`, one))
	s.wantError("entity_write", fmt.Sprintf(`{"space":%q,"type":"备忘","title":"x"}`, one), "有 2 个类型叫「备忘」，请用 key 指定：memo（备忘）、memo2（备忘）")
}

func TestRelationValuesByTitle(t *testing.T) {
	s, space := projectSpace(t, store.Open(""))
	defer s.finish()
	run := s.idByTitle(space, "goal", "完成一次半程跑")
	second := s.idByTitle(space, "task", "写第 2 篇")
	first := s.idByTitle(space, "task", "写第 1 篇")

	// A one-target relation by title, a many relation by a list mixing a title and an id, and by a single title.
	e := asMap(t, s.done("entity_write", fmt.Sprintf(`{"space":%q,"type":"task","title":"配速跑",`+
		`"values":{"goal":"完成一次半程跑","blocked_by":["写第 2 篇",%q]}}`, space, first)))
	values := asMap(t, e["values"])
	if !reflect.DeepEqual(values["goal"], []any{run}) || !reflect.DeepEqual(values["blocked_by"], []any{second, first}) {
		t.Errorf("values = %v", values)
	}
	e = asMap(t, s.done("entity_write", fmt.Sprintf(`{"id":%q,"values":{"blocked_by":"写第 2 篇"}}`, e["id"])))
	if got := asMap(t, e["values"])["blocked_by"]; !reflect.DeepEqual(got, []any{second}) {
		t.Errorf("one title for a many relation = %v", got)
	}
	// Ids keep working, and null still clears.
	e = asMap(t, s.done("entity_write", fmt.Sprintf(`{"id":%q,"values":{"goal":[%q],"blocked_by":null}}`, e["id"], run)))
	if _, ok := asMap(t, e["values"])["blocked_by"]; ok {
		t.Errorf("null did not clear: %v", e["values"])
	}

	// No record, or a record of another type, with the title; several records with it.
	s.wantError("entity_write", fmt.Sprintf(`{"space":%q,"type":"task","title":"x","values":{"goal":"跑完半马"}}`, space),
		"属性「目标」：没有标题为「跑完半马」的目标记录")
	s.wantError("entity_write", fmt.Sprintf(`{"space":%q,"type":"task","title":"x","values":{"goal":"写第 1 篇"}}`, space),
		"属性「目标」：没有标题为「写第 1 篇」的目标记录")
	a := asStr(t, get(t, s.done("entity_write", fmt.Sprintf(`{"space":%q,"type":"goal","title":"重复"}`, space)), "id"))
	b := asStr(t, get(t, s.done("entity_write", fmt.Sprintf(`{"space":%q,"type":"goal","title":"重复"}`, space)), "id"))
	s.wantError("entity_write", fmt.Sprintf(`{"space":%q,"type":"task","title":"x","values":{"goal":"重复"}}`, space),
		fmt.Sprintf("属性「目标」：有 2 条目标记录的标题是「重复」，请用编号：%s、%s", a, b))
	// An id of a record of the wrong type is kept as an id, and the kernel refuses it as before.
	if msg := s.failed("entity_write", fmt.Sprintf(`{"space":%q,"type":"task","title":"x","values":{"goal":%q}}`, space, first)); !strings.Contains(msg, "关联的记录不存在或类型不对") {
		t.Errorf("wrong type id = %q", msg)
	}
}

func TestChoiceErrorsNameTheOptions(t *testing.T) {
	s, space := projectSpace(t, store.Open(""))
	defer s.finish()

	s.wantError("entity_write", fmt.Sprintf(`{"space":%q,"type":"task","title":"x","values":{"priority":"紧急"}}`, space),
		"属性「优先级」只能是：高、中、低")
	s.wantError("entity_write", fmt.Sprintf(`{"space":%q,"type":"task","title":"x","status":"完蛋"}`, space),
		"「完蛋」不是「任务」的阶段；阶段有：待办、进行中、完成、取消")

	task := asStr(t, get(t, s.done("entity_write", fmt.Sprintf(`{"space":%q,"type":"task","title":"x","values":{"priority":"高"}}`, space)), "id"))
	s.wantError("entity_write", fmt.Sprintf(`{"id":%q,"values":{"priority":1}}`, task), "属性「优先级」只能是：高、中、低")
	s.wantError("entity_write", fmt.Sprintf(`{"id":%q,"status":"完蛋"}`, task), "「完蛋」不是「任务」的阶段；阶段有：待办、进行中、完成、取消")
	// A stage that is not one move away keeps the kernel's sentence, which names the allowed moves.
	s.wantError("entity_write", fmt.Sprintf(`{"id":%q,"status":"完成"}`, task), "「待办」不能直接变为「完成」；可以变为：进行中、取消")
	// A valid option, and null, still work.
	e := asMap(t, s.done("entity_write", fmt.Sprintf(`{"id":%q,"status":"进行中","values":{"priority":" 低 "}}`, task)))
	if asMap(t, e["values"])["priority"] != "低" || e["status"] != "进行中" {
		t.Errorf("valid write = %v", e)
	}
	e = asMap(t, s.done("entity_write", fmt.Sprintf(`{"id":%q,"values":{"priority":null}}`, task)))
	if _, ok := asMap(t, e["values"])["priority"]; ok {
		t.Errorf("null did not clear: %v", e["values"])
	}
}

func TestBatchWrite(t *testing.T) {
	s, space := projectSpace(t, store.Open(t.TempDir()))
	defer s.finish()
	before := get(t, s.done("query", fmt.Sprintf(`{"space":%q}`, space)), "total")

	// Three records in order; the third relates by title to the first, made in the same batch.
	out := asList(t, get(t, s.done("entity_write", fmt.Sprintf(`{"space":"项目与目标","entities":[`+
		`{"type":"目标","title":"学会做饭"},`+
		`{"type":"note","title":"想法","values":{"kind":"进展"}},`+
		`{"type":"task","title":"买菜","status":"进行中","values":{"goal":"学会做饭","priority":"中"}}]}`)), "entities"))
	if len(out) != 3 {
		t.Fatalf("batch answered %d records", len(out))
	}
	// The answer is read after the whole batch: the goal's rollups already count the task written after it.
	goal := asMap(t, out[0])
	if !slices.Equal(keysOf(t, goal), []string{"id", "status", "title", "type", "values"}) || goal["status"] != "规划中" ||
		asMap(t, goal["values"])["tasks"] != float64(1) {
		t.Errorf("brief goal = %v", goal)
	}
	if note := asMap(t, out[1]); !slices.Equal(keysOf(t, note), []string{"id", "title", "type", "values"}) {
		t.Errorf("brief note without a flow = %v", note)
	}
	task := asMap(t, out[2])
	if got := asMap(t, task["values"])["goal"]; !reflect.DeepEqual(got, []any{goal["id"]}) || task["status"] != "进行中" {
		t.Errorf("task = %v", task)
	}
	// A batch may change records too; without a space each record is found by its id.
	changed := asList(t, get(t, s.done("entity_write", fmt.Sprintf(`{"entities":[{"id":%q,"status":"完成"}]}`, task["id"])), "entities"))
	if asMap(t, changed[0])["status"] != "完成" {
		t.Errorf("changed = %v", changed)
	}

	// A failing item saves nothing, not even the items before it, and names its position and title.
	after := get(t, s.done("query", fmt.Sprintf(`{"space":%q}`, space)), "total")
	s.wantError("entity_write", fmt.Sprintf(`{"space":%q,"entities":[{"type":"task","title":"甲"},{"type":"task","title":"乙"},`+
		`{"type":"task","title":"丙","values":{"priority":"紧急"}}]}`, space), "第 3 条「丙」：属性「优先级」只能是：高、中、低")
	s.wantError("entity_write", fmt.Sprintf(`{"entities":[{"type":"task","title":"甲","space":%q}]}`, space),
		`参数不对：json: unknown field "space"`)
	// An item that changes a record without a title is named by the record's title.
	s.wantError("entity_write", fmt.Sprintf(`{"space":%q,"entities":[{"type":"task","title":"甲"},{"id":%q,"status":"已达成"}]}`, space, goal["id"]),
		"第 2 条「学会做饭」：「规划中」不能直接变为「已达成」；可以变为：进行中、已放弃")
	s.wantError("entity_write", fmt.Sprintf(`{"space":%q,"entities":[{"title":"无类型"}]}`, space), "第 1 条「无类型」：新建记录需要 type。可选："+projectTypes(t))
	s.wantError("entity_write", `{"entities":[{"id":"nope","title":" "}]}`, "第 1 条（nope）：没有记录「nope」")
	if got := get(t, s.done("query", fmt.Sprintf(`{"space":%q}`, space)), "total"); got != after {
		t.Errorf("total after refused batches = %v, want %v", got, after)
	}
	if got := get(t, s.done("query", fmt.Sprintf(`{"space":%q,"text":"甲"}`, space)), "total"); got != float64(0) {
		t.Errorf("an item before the failing one was saved")
	}
	if before.(float64)+3 != after.(float64) {
		t.Errorf("totals: before %v, after %v", before, after)
	}

	// The shape of the arguments.
	s.wantError("entity_write", fmt.Sprintf(`{"space":%q,"title":"x","entities":[{"type":"task","title":"y"}]}`, space),
		"entities 不能和顶层的 id、type、title、status、values 一起用；每条记录的字段写在 entities 的各项里")
	s.wantError("entity_write", `{"id":"","entities":[{"type":"task","title":"y"}]}`,
		"entities 不能和顶层的 id、type、title、status、values 一起用；每条记录的字段写在 entities 的各项里")
	s.wantError("entity_write", fmt.Sprintf(`{"space":%q,"entities":[]}`, space), "entities 需要 1 到 50 条记录")
	many := strings.TrimSuffix(strings.Repeat(`{"type":"note","title":"n"},`, maxBatch+1), ",")
	s.wantError("entity_write", fmt.Sprintf(`{"space":%q,"entities":[%s]}`, space, many), "entities 需要 1 到 50 条记录")
	full := strings.TrimSuffix(strings.Repeat(`{"type":"note","title":"n"},`, maxBatch), ",")
	if n := len(asList(t, get(t, s.done("entity_write", fmt.Sprintf(`{"space":%q,"entities":[%s]}`, space, full)), "entities"))); n != maxBatch {
		t.Errorf("a full batch wrote %d", n)
	}
}

func TestBriefQuery(t *testing.T) {
	s, space := projectSpace(t, store.Open(""))
	defer s.finish()

	q := fmt.Sprintf(`{"space":%q,"type":"task","group":"status","agg":[{"op":"sum","of":"effort"}]`, space)
	full := asMap(t, s.done("query", q+`}`))
	brief := asMap(t, s.done("query", q+`,"brief":true}`))
	for _, k := range []string{"total", "aggs", "groups"} {
		if !reflect.DeepEqual(full[k], brief[k]) {
			t.Errorf("brief %s = %v, want %v", k, brief[k], full[k])
		}
	}
	fe, be := asList(t, full["entities"]), asList(t, brief["entities"])
	if len(fe) != len(be) {
		t.Fatalf("brief entities = %d, want %d", len(be), len(fe))
	}
	for i := range fe {
		f, b := asMap(t, fe[i]), asMap(t, be[i])
		want := map[string]any{"id": f["id"], "type": f["type"], "title": f["title"], "status": f["status"]}
		if v := asMap(t, f["values"]); len(v) > 0 {
			want["values"] = v
		}
		if !reflect.DeepEqual(b, want) {
			t.Errorf("brief record %d = %v, want %v", i, b, want)
		}
	}
	// Status and values are left out when empty: a note has no flow, and this one has no values.
	s.done("entity_write", fmt.Sprintf(`{"space":%q,"type":"note","title":"空的"}`, space))
	for _, e := range asList(t, get(t, s.done("query", fmt.Sprintf(`{"space":%q,"type":"note","brief":true,"text":"空的"}`, space)), "entities")) {
		if got := keysOf(t, e); !slices.Equal(got, []string{"id", "title", "type"}) {
			t.Errorf("brief empty note keys = %v", got)
		}
	}
	// One record by its id, briefly.
	one := asMap(t, s.done("query", fmt.Sprintf(`{"id":%q,"brief":true}`, asMap(t, fe[0])["id"])))
	if got := keysOf(t, get(t, one, "entities", 0)); slices.Contains(got, "moves") || !slices.Contains(got, "title") {
		t.Errorf("brief one keys = %v", got)
	}
}

// TestDefaultAnswersUnchanged pins the exact bytes of a single entity_write and of a query without brief.
func TestDefaultAnswersUnchanged(t *testing.T) {
	s := start(t, &memStore{})
	defer s.finish()
	s.done("space_write", `{"name":"固定"}`)
	s.done("type_write", `{"type":{"key":"task","name":"任务","flow":[{"name":"待办","phase":"todo"},{"name":"完成","phase":"done"}],`+
		`"props":[{"key":"n","name":"数","kind":"number"},{"key":"up","name":"上级","kind":"relation","target":"task"}]}}`)
	s.done("entity_write", `{"type":"task","title":"甲"}`)
	want := `{"id":"e3","type":"task","title":"乙","status":"待办","phase":"todo","moves":["完成"],"values":{"n":3,"up":["e2"]},` +
		`"out":[{"prop":"up","id":"e2","title":"甲"}],"in":[],"created":"2026-11-10T09:00:00Z","updated":"2026-11-10T09:00:00Z"}`
	res := get(t, s.tool("entity_write", `{"type":"task","title":"乙","values":{"n":3,"up":"甲"}}`), "result")
	if got := asStr(t, get(t, res, "content", 0, "text")); got != want {
		t.Errorf("entity_write answer:\n got %s\nwant %s", got, want)
	}
	wantQuery := `{"total":1,"groups":[{"key":"","name":"","count":1}],"entities":[{"id":"e2","type":"task","title":"甲","status":"待办",` +
		`"phase":"todo","moves":["完成"],"values":{},"out":[],"in":[{"type":"task","prop":"up","id":"e3","title":"乙"}],` +
		`"created":"2026-11-10T09:00:00Z","updated":"2026-11-10T09:00:00Z"}]}`
	res = get(t, s.tool("query", `{"type":"task","where":[{"field":"up","op":"empty"}]}`), "result")
	if got := asStr(t, get(t, res, "content", 0, "text")); got != wantQuery {
		t.Errorf("query answer:\n got %s\nwant %s", got, wantQuery)
	}
}

func TestRelationConditionsByTitle(t *testing.T) {
	s, space := projectSpace(t, store.Open(""))
	defer s.finish()
	review := s.idByTitle(space, "goal", "连续写满 12 篇周复盘")
	all := get(t, s.done("query", fmt.Sprintf(`{"space":%q,"type":"task"}`, space)), "total").(float64)

	count := func(where string) float64 {
		t.Helper()
		return get(t, s.done("query", fmt.Sprintf(`{"space":%q,"type":"任务","where":[%s]}`, space, where)), "total").(float64)
	}
	byID := count(fmt.Sprintf(`{"field":"goal","op":"eq","value":%q}`, review))
	if byID != 4 {
		t.Fatalf("tasks of the review goal by id = %v", byID)
	}
	for _, where := range []string{
		`{"field":"goal","op":"eq","value":"连续写满 12 篇周复盘"}`,
		`{"field":"goal","op":"eq","value":["连续写满 12 篇周复盘"]}`,
		`{"field":"goal","op":"has","value":["连续写满 12 篇周复盘"]}`,
		`{"field":"goal","op":"has","value":"连续写满 12 篇周复盘"}`,
	} {
		if got := count(where); got != byID {
			t.Errorf("%s: total = %v, want %v", where, got, byID)
		}
	}
	if got := count(`{"field":"goal","op":"ne","value":"连续写满 12 篇周复盘"}`); got != all-byID {
		t.Errorf("ne by title: total = %v, want %v", got, all-byID)
	}
	if got := count(`{"field":"blocked_by","op":"has","value":["写第 2 篇"]}`); got != 1 {
		t.Errorf("blocked by a title: total = %v", got)
	}
	if got := count(`{"field":"goal","op":"empty"}`); got != 0 {
		t.Errorf("empty: total = %v", got)
	}
	s.wantError("query", fmt.Sprintf(`{"space":%q,"type":"task","where":[{"field":"goal","op":"eq","value":"跑完半马"}]}`, space),
		"筛选条件：属性「目标」：没有标题为「跑完半马」的目标记录")
	s.wantError("query", fmt.Sprintf(`{"space":%q,"type":"task","where":[{"field":"blocked_by","op":"has","value":["写第 1 篇","没有这篇"]}]}`, space),
		"筛选条件：属性「被阻塞」：没有标题为「没有这篇」的目标记录")
}

func TestOriginConditionsByTitle(t *testing.T) {
	s := start(t, &memStore{})
	defer s.finish()
	space := asStr(t, get(t, s.done("space_write", `{"name":"Synthetic"}`), "id"))
	s.done("type_write", fmt.Sprintf(`{"space":%q,"type":{"key":"source","name":"Source"}}`, space))
	s.done("type_write", fmt.Sprintf(`{"space":%q,"type":{"key":"copy","name":"Copy"}}`, space))
	s.done("type_write", fmt.Sprintf(`{"space":%q,"type":{"key":"other","name":"Other"}}`, space))
	sourceID := asStr(t, get(t, s.done("entity_write", fmt.Sprintf(`{"space":%q,"type":"source","title":"Source Alpha"}`, space)), "id"))
	s.done("entity_derive", fmt.Sprintf(`{"id":%q,"type":"copy","title":"Copy Alpha"}`, sourceID))
	derivedID := s.idByTitle(space, "copy", "Copy Alpha")

	count := func(typ, where string) float64 {
		t.Helper()
		args := fmt.Sprintf(`{"space":%q,"where":[%s]}`, space, where)
		if typ != "" {
			args = fmt.Sprintf(`{"space":%q,"type":%q,"where":[%s]}`, space, typ, where)
		}
		return get(t, s.done("query", args), "total").(float64)
	}
	if got := count("copy", fmt.Sprintf(`{"field":"origin","op":"eq","value":%q}`, sourceID)); got != 1 {
		t.Fatalf("origin by ID = %v, want 1", got)
	}
	for _, tc := range []struct {
		typ   string
		where string
		want  float64
	}{
		{typ: "copy", where: `{"field":"origin","op":"eq","value":"Source Alpha"}`, want: 1},
		{typ: "", where: `{"field":"origin","op":"eq","value":"Source Alpha"}`, want: 1},
		{typ: "copy", where: `{"field":"origin","op":"has","value":"Source Alpha"}`, want: 1},
		{typ: "", where: `{"field":"origin","op":"has","value":["Source Alpha"]}`, want: 1},
		{typ: "copy", where: `{"field":"origin","op":"ne","value":"Source Alpha"}`, want: 0},
		{typ: "", where: `{"field":"origin","op":"ne","value":"Source Alpha"}`, want: 1},
	} {
		if got := count(tc.typ, tc.where); got != tc.want {
			t.Errorf("type=%q %s: total=%v, want %v", tc.typ, tc.where, got, tc.want)
		}
	}
	result := s.done("query", fmt.Sprintf(`{"space":%q,"type":"copy","where":[{"field":"origin","op":"eq","value":"Source Alpha"}]}`, space))
	if got := asStr(t, get(t, result, "entities", 0, "id")); got != derivedID {
		t.Errorf("origin title returned %q, want derived record %q", got, derivedID)
	}
	s.wantError("query", fmt.Sprintf(`{"space":%q,"type":"copy","where":[{"field":"origin","op":"eq","value":"Missing"}]}`, space),
		"筛选条件：属性「源自」：没有标题为「Missing」的目标记录")

	otherID := asStr(t, get(t, s.done("entity_write", fmt.Sprintf(`{"space":%q,"type":"other","title":"Source Alpha"}`, space)), "id"))
	s.wantError("query", fmt.Sprintf(`{"space":%q,"type":"copy","where":[{"field":"origin","op":"eq","value":"Source Alpha"}]}`, space),
		fmt.Sprintf("筛选条件：属性「源自」：有 2 条目标记录的标题是「Source Alpha」，请用编号：%s、%s", sourceID, otherID))
}

func TestTypeWriteMerge(t *testing.T) {
	s, space := projectSpace(t, store.Open(""))
	defer s.finish()
	typeOfKey := func(key string) map[string]any {
		t.Helper()
		for _, ty := range asList(t, get(t, s.done("schema", fmt.Sprintf(`{"space":%q}`, space)), "types")) {
			if asMap(t, ty)["key"] == key {
				return asMap(t, ty)
			}
		}
		t.Fatalf("no type %s", key)
		return nil
	}
	propKeys := func(ty map[string]any) []string {
		var keys []string
		for _, p := range asList(t, ty["props"]) {
			keys = append(keys, asStr(t, asMap(t, p)["key"]))
		}
		return keys
	}
	old := typeOfKey("task")

	// One new prop: appended; everything else is kept. The space is named by its name.
	got := asMap(t, s.done("type_write", `{"space":"项目与目标","merge":true,"type":{"key":"task","props":[{"key":"place","name":"地点","kind":"text"}]}}`))
	if want := append(propKeys(old), "place"); !slices.Equal(propKeys(got), want) {
		t.Errorf("props = %v, want %v", propKeys(got), want)
	}
	for _, k := range []string{"name", "flow", "card", "views"} {
		if !reflect.DeepEqual(got[k], old[k]) {
			t.Errorf("merge changed %s: %v", k, got[k])
		}
	}
	if got["count"] != old["count"] {
		t.Errorf("count = %v, want %v", got["count"], old["count"])
	}

	// A prop with a known key is replaced in place; a name alone is replaced; an empty list clears the views.
	got = asMap(t, s.done("type_write", fmt.Sprintf(`{"space":%q,"merge":true,"type":{"key":"task","name":"待办事项","views":[],`+
		`"props":[{"key":"priority","name":"优先级","kind":"enum","options":["高","中","低","紧急"]}]}}`, space)))
	if !slices.Equal(propKeys(got), append(propKeys(old), "place")) {
		t.Errorf("props after replacing one = %v", propKeys(got))
	}
	i := slices.Index(propKeys(got), "priority")
	if opts := asList(t, asMap(t, asList(t, got["props"])[i])["options"]); len(opts) != 4 {
		t.Errorf("priority options = %v", opts)
	}
	if got["name"] != "待办事项" || got["views"] != nil || !reflect.DeepEqual(got["flow"], old["flow"]) || !reflect.DeepEqual(got["card"], old["card"]) {
		t.Errorf("after merge: name %v views %v flow %v card %v", got["name"], got["views"], got["flow"], got["card"])
	}
	s.done("entity_write", fmt.Sprintf(`{"space":%q,"type":"task","title":"急事","values":{"priority":"紧急","place":"家"}}`, space))

	// The kernel's migration rules still apply: a merge that breaks a record is refused whole.
	if msg := s.failed("type_write", fmt.Sprintf(`{"space":%q,"merge":true,"type":{"key":"task","name":"x",`+
		`"props":[{"key":"priority","name":"优先级","kind":"number"}]}}`, space)); !strings.Contains(msg, "不符合新结构") {
		t.Errorf("breaking merge = %q", msg)
	}
	if typeOfKey("task")["name"] != "待办事项" {
		t.Errorf("a refused merge changed the type")
	}

	// merge on a new key creates the type; a new type still needs a name.
	if got := asMap(t, s.done("type_write", fmt.Sprintf(`{"space":%q,"merge":true,"type":{"key":"memo","name":"备忘"}}`, space))); got["name"] != "备忘" {
		t.Errorf("merge create = %v", got)
	}
	if msg := s.failed("type_write", fmt.Sprintf(`{"space":%q,"merge":true,"type":{"key":"memo2"}}`, space)); !strings.Contains(msg, "名称") {
		t.Errorf("new type without a name = %q", msg)
	}

	// Without merge the type is replaced whole: the props not given are gone.
	got = asMap(t, s.done("type_write", fmt.Sprintf(`{"space":%q,"type":{"key":"memo","name":"备忘","props":[{"key":"a","name":"甲","kind":"text"}]}}`, space)))
	got = asMap(t, s.done("type_write", fmt.Sprintf(`{"space":%q,"type":{"key":"memo","name":"备忘"}}`, space)))
	if _, ok := got["props"]; ok {
		t.Errorf("replacement kept props: %v", got["props"])
	}
}

// TestMergeFieldsCoverType checks that a merge handles every field of kernel.Type: the key, the props, or a field of
// mergeFields. A field added to the kernel fails here until the merge knows it.
func TestMergeFieldsCoverType(t *testing.T) {
	known := map[string]bool{"key": true, "props": true}
	for name := range mergeFields {
		known[name] = true
	}
	rt := reflect.TypeFor[kernel.Type]()
	var names []string
	for i := range rt.NumField() {
		name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		names = append(names, name)
		if !known[name] {
			t.Errorf("kernel.Type field %q is not merged", name)
		}
	}
	if len(names) != len(known) {
		t.Errorf("fields = %v, merge knows %d", names, len(known))
	}
}

// TestToolListSchemasArePlain reads tools/list as a client does: every input schema is a plain object that refuses
// unknown properties, with no combinator at its root, and the new arguments are there to be found.
func TestToolListSchemasArePlain(t *testing.T) {
	s := start(t, store.Open(""))
	defer s.finish()
	byName := map[string]map[string]any{}
	for _, it := range asList(t, get(t, s.rpc("tools/list", `{}`), "result", "tools")) {
		schema := asMap(t, get(t, it, "inputSchema"))
		name := asStr(t, get(t, it, "name"))
		byName[name] = schema
		if schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Errorf("%s: root = %v", name, schema)
		}
		for _, k := range []string{"oneOf", "anyOf", "allOf"} {
			if _, ok := schema[k]; ok {
				t.Errorf("%s: root has %s", name, k)
			}
		}
		checkSchema(t, name, schema)
	}
	for tool, prop := range map[string]string{"query": "brief", "type_write": "merge", "entity_write": "entities"} {
		if _, ok := asMap(t, byName[tool]["properties"])[prop]; !ok {
			t.Errorf("%s has no %s", tool, prop)
		}
	}
	item := asMap(t, get(t, byName["entity_write"], "properties", "entities", "items"))
	if got := keysOf(t, item["properties"]); !slices.Equal(got, []string{"id", "status", "title", "type", "values"}) || item["additionalProperties"] != false {
		t.Errorf("batch item = %v", item)
	}
	if d := asStr(t, get(t, byName["schema"], "properties", "space", "description")); !strings.Contains(d, "名称") {
		t.Errorf("space description = %q", d)
	}
}

// Only eq, ne and has with a non-empty string or a list of non-empty strings are read as titles. Anything else on a
// relation reaches the kernel as it was sent: null means unset, and the other operators and operands get the
// kernel's own answer, not a title lookup.
func TestRelationConditionsLeaveOtherOperandsToTheKernel(t *testing.T) {
	s, space := projectSpace(t, store.Open(""))
	defer s.finish()
	all := get(t, s.done("query", fmt.Sprintf(`{"space":%q,"type":"task"}`, space)), "total").(float64)
	unset := get(t, s.done("query", fmt.Sprintf(`{"space":%q,"type":"task","where":[{"field":"goal","op":"empty"}]}`, space)), "total").(float64)

	text := func(where string) string {
		t.Helper()
		res := get(t, s.tool("query", fmt.Sprintf(`{"space":%q,"type":"task","where":[%s]}`, space, where)), "result")
		return asStr(t, get(t, res, "content", 0, "text"))
	}
	count := func(where string) float64 {
		t.Helper()
		return get(t, s.done("query", fmt.Sprintf(`{"space":%q,"type":"task","where":[%s]}`, space, where)), "total").(float64)
	}
	if got := count(`{"field":"goal","op":"eq","value":null}`); got != unset {
		t.Errorf("eq null: total = %v, want %v", got, unset)
	}
	if got := count(`{"field":"goal","op":"eq"}`); got != unset {
		t.Errorf("eq without a value: total = %v, want %v", got, unset)
	}
	if got := count(`{"field":"goal","op":"ne","value":null}`); got != all-unset {
		t.Errorf("ne null: total = %v, want %v", got, all-unset)
	}
	if got := count(`{"field":"goal","op":"eq","value":""}`); got != unset {
		t.Errorf("eq empty string: total = %v, want %v", got, unset)
	}
	// A string that is no record's title is not looked up outside eq, ne and has, or inside a mixed list.
	for _, where := range []string{
		`{"field":"goal","op":"gt","value":"没有这条"}`,
		`{"field":"goal","op":"le","value":"没有这条"}`,
		`{"field":"goal","op":"empty","value":"没有这条"}`,
		`{"field":"goal","op":"eq","value":["没有这条",7]}`,
		`{"field":"goal","op":"has","value":["没有这条",""]}`,
		`{"field":"goal","op":"has","value":[]}`,
		`{"field":"goal","op":"eq","value":7}`,
	} {
		if got := text(where); strings.Contains(got, "没有标题为") {
			t.Errorf("%s: read as a title: %s", where, got)
		}
	}
}

// A batch writes to one space. Without a space it is the space of the records the batch changes, and the items that
// create land there, also when the base holds other spaces; records of several spaces, or a record outside the
// given space, are refused and nothing is saved.
func TestBatchWritesToOneSpace(t *testing.T) {
	s, project := projectSpace(t, store.Open(""))
	defer s.finish()
	reading := asStr(t, get(t, s.done("space_write", `{"template":"reading","samples":true}`), "id"))
	task := s.idByTitle(project, "task", "写第 2 篇")
	book := asStr(t, asMap(t, asList(t, get(t, s.done("query", fmt.Sprintf(`{"space":%q,"type":"book","limit":1}`, reading)), "entities"))[0])["id"])
	total := func(space string) float64 {
		t.Helper()
		return get(t, s.done("query", fmt.Sprintf(`{"space":%q}`, space)), "total").(float64)
	}
	inProject, inReading := total(project), total(reading)

	// An update and a create, no space: the create goes beside the record that is changed, in either order.
	for i, items := range []string{
		fmt.Sprintf(`{"id":%q,"values":{"priority":"高"}},{"type":"task","title":"跟着改的任务 1"}`, task),
		fmt.Sprintf(`{"type":"task","title":"跟着改的任务 2"},{"id":%q,"values":{"priority":"低"}}`, task),
	} {
		out := asList(t, get(t, s.done("entity_write", `{"entities":[`+items+`]}`), "entities"))
		if len(out) != 2 {
			t.Fatalf("batch %d answered %d records", i, len(out))
		}
		if got := total(project); got != inProject+float64(i+1) {
			t.Errorf("batch %d: project total = %v, want %v", i, got, inProject+float64(i+1))
		}
		if got := total(reading); got != inReading {
			t.Errorf("batch %d: reading total = %v, want %v", i, got, inReading)
		}
	}
	inProject = total(project)

	// Only creates and no space: refused as a single create is, naming the spaces.
	res := get(t, s.tool("entity_write", `{"entities":[{"type":"task","title":"没有空间"}]}`), "result")
	if got := asStr(t, get(t, res, "content", 0, "text")); !strings.Contains(got, "有多个空间，请用 space 指定") {
		t.Errorf("creates without a space: %s", got)
	}
	// Records of two spaces in one batch.
	res = get(t, s.tool("entity_write", fmt.Sprintf(`{"entities":[{"id":%q,"values":{"priority":"中"}},{"id":%q,"title":"改名"},{"type":"task","title":"夹在中间"}]}`, task, book)), "result")
	if got := asStr(t, get(t, res, "content", 0, "text")); !strings.Contains(got, "分属 2 个空间") || !strings.Contains(got, "一次批量只写一个空间") {
		t.Errorf("two spaces: %s", got)
	}
	// A record outside the given space.
	res = get(t, s.tool("entity_write", fmt.Sprintf(`{"space":%q,"entities":[{"type":"book","title":"新书"},{"id":%q,"values":{"priority":"中"}}]}`, reading, task)), "result")
	if got := asStr(t, get(t, res, "content", 0, "text")); !strings.Contains(got, "第 2 条「写第 2 篇」：记录在空间「项目与目标」，不在「阅读与知识」") {
		t.Errorf("record outside the given space: %s", got)
	}
	if total(project) != inProject || total(reading) != inReading {
		t.Errorf("a refused batch saved something: project %v (was %v), reading %v (was %v)", total(project), inProject, total(reading), inReading)
	}
	// With the space given, updates and creates of that space pass.
	out := asList(t, get(t, s.done("entity_write", fmt.Sprintf(`{"space":"阅读与知识","entities":[{"id":%q,"title":"改名的书"},{"type":"book","title":"新书"}]}`, book)), "entities"))
	if len(out) != 2 || total(reading) != inReading+1 || total(project) != inProject {
		t.Errorf("batch in the given space: %v", out)
	}
}
