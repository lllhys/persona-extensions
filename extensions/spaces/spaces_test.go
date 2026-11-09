package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "spaces-locator-")
	if err != nil {
		panic(err)
	}
	locatorFile = func() string { return filepath.Join(dir, "data-dir") } // never the real config directory
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newApp(t.TempDir()))))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m struct {
		Contributes struct {
			MCPServers []struct {
				Allow []string `json:"allow"`
			} `json:"mcp_servers"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil || len(m.Contributes.MCPServers) != 1 {
		t.Fatalf("one mcp server: %v", err)
	}
	var readOnly []string
	for _, tl := range tools {
		if tl.readOnly {
			readOnly = append(readOnly, tl.name)
		}
	}
	if allow := m.Contributes.MCPServers[0].Allow; !slices.Equal(allow, readOnly) {
		t.Fatalf("allow %v, want exactly the read-only tools %v", allow, readOnly)
	}
	if !strings.Contains(usage, "mcp") {
		t.Fatal("--help names mcp, which the manifest requires")
	}
	if _, err := os.Stat(filepath.Join("skills", "spaces", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

var t0 = time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)

func ptr(s string) *string { return &s }

// library is a space with one type of every field kind.
func library(t *testing.T) *State {
	t.Helper()
	st := &State{}
	if _, err := st.upsertSpace(SpaceInput{ID: "reading", Name: ptr("读书"), Icon: ptr("doc")}, time.Now()); err != nil {
		t.Fatal(err)
	}
	_, err := st.upsertType("reading", TypeDef{Key: "book", Label: "书", View: "progress", Statuses: []string{"想读", "在读", "读完"},
		Fields: []FieldDef{
			{Key: "read", Label: "已读", Kind: "number"}, {Key: "pages", Label: "页数", Kind: "number"},
			{Key: "isbn", Label: "ISBN", Kind: "text"}, {Key: "owned", Label: "有纸书", Kind: "boolean"},
			{Key: "due", Label: "归还", Kind: "date"}, {Key: "shelf", Label: "书架", Kind: "enum", Options: []string{"客厅", "书房"}},
			{Key: "link", Label: "链接", Kind: "url"}, {Key: "author", Label: "作者", Kind: "text", Required: true},
		}})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestValuesFollowTheirKind(t *testing.T) {
	st := library(t)
	e, err := st.upsertEntity(EntityInput{Type: "book", Title: ptr("示例书"), Values: map[string]any{
		"author": "某人", "isbn": "007", "read": "12", "pages": 300.0, "owned": "true", "due": "2026-12-01", "shelf": "书房",
		"link": "https://example.com/book"}}, t0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"author": "某人", "isbn": "007", "read": 12.0, "pages": 300.0, "owned": true, "due": "2026-12-01",
		"shelf": "书房", "link": "https://example.com/book"}
	for k, v := range want {
		if e.Values[k] != v {
			t.Errorf("%s = %#v, want %#v", k, e.Values[k], v)
		}
	}
	if e.Status != "想读" || e.Space != "reading" {
		t.Fatalf("a new entity starts at the first status in the only space: %+v", e)
	}
	for _, bad := range []map[string]any{
		{"isbn": 7.0}, {"read": "很多"}, {"owned": "也许"}, {"due": "12/01/2026"}, {"shelf": "地下室"},
		{"link": "ftp://example.com"}, {"link": "example.com"}, {"color": "红"}, {"author": nil},
	} {
		if _, err := st.upsertEntity(EntityInput{ID: e.ID, Values: bad}, t0); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if _, err := st.upsertEntity(EntityInput{Type: "book", Title: ptr("缺作者")}, t0); err == nil || !strings.Contains(err.Error(), "author") {
		t.Fatalf("a required field: %v", err)
	}
	if _, err := st.upsertEntity(EntityInput{ID: e.ID, Status: "读了一半"}, t0); err == nil {
		t.Fatal("a status the type does not have")
	}
	if _, err := st.upsertEntity(EntityInput{ID: e.ID, Type: "other"}, t0); err == nil {
		t.Fatal("an entity changes its type")
	}
	// Values merge; null and "" remove one.
	e, err = st.upsertEntity(EntityInput{ID: e.ID, Status: "在读", Values: map[string]any{"due": nil, "link": "", "read": 40.0}}, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Values["due"]; ok || e.Values["link"] != nil || e.Values["read"] != 40.0 || e.Values["isbn"] != "007" || e.Status != "在读" {
		t.Fatalf("merged: %+v", e)
	}
	if !e.Updated.After(e.Created) {
		t.Fatal("updated moves")
	}
}

func TestRelationsAndDeletion(t *testing.T) {
	st := library(t)
	if _, err := st.upsertType("reading", TypeDef{Key: "note", Label: "笔记", View: "feed", Fields: []FieldDef{{Key: "body", Kind: "text"}}}); err != nil {
		t.Fatal(err)
	}
	book, _ := st.upsertEntity(EntityInput{Type: "book", Title: ptr("书一"), Values: map[string]any{"author": "甲"}}, t0)
	note, err := st.upsertEntity(EntityInput{Type: "note", Title: ptr("摘抄"), Links: []Link{{Type: "摘自", To: book.ID}, {Type: "摘自", To: book.ID}}}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := st.links(note.ID); len(out) != 1 || out[0] != (Relation{note.ID, "摘自", book.ID}) {
		t.Fatalf("one edge, duplicates folded: %v", out)
	}
	if _, in := st.links(book.ID); len(in) != 1 {
		t.Fatalf("the book sees the edge coming in: %v", in)
	}
	if _, err := st.upsertEntity(EntityInput{ID: note.ID, Links: []Link{{Type: "自指", To: note.ID}}}, t0); err == nil {
		t.Fatal("an edge to itself")
	}
	if _, err := st.upsertSpace(SpaceInput{ID: "other", Name: ptr("别处")}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.upsertType("other", TypeDef{Key: "book", Label: "另一种书"}); err != nil {
		t.Fatal("type keys belong to their space")
	}
	far, _ := st.upsertEntity(EntityInput{Space: "other", Type: "book", Title: ptr("远处")}, t0)
	if _, err := st.upsertEntity(EntityInput{ID: note.ID, Links: []Link{{Type: "跨", To: far.ID}}}, t0); err == nil {
		t.Fatal("an edge across spaces")
	}
	if _, err := st.upsertEntity(EntityInput{Type: "book", Title: ptr("x")}, t0); err == nil {
		t.Fatal("two spaces: the space must be named")
	}
	// Leaving relations out keeps them; an empty list clears them.
	st.upsertEntity(EntityInput{ID: note.ID, Title: ptr("摘抄二")}, t0)
	if out, _ := st.links(note.ID); len(out) != 1 {
		t.Fatal("relations kept when not given")
	}
	if n, err := st.deleteEntity(book.ID); err != nil || n != 1 || len(st.Relations) != 0 {
		t.Fatalf("delete takes the edges: %d %v %v", n, err, st.Relations)
	}
	if err := st.deleteSpace("other"); err != nil || st.entity(far.ID) != nil || len(st.Spaces) != 1 {
		t.Fatal("deleting a space deletes what it holds")
	}
}

func TestTypesEvolveOnlyWhenTheirEntitiesFit(t *testing.T) {
	st := library(t)
	e, _ := st.upsertEntity(EntityInput{Type: "book", Title: ptr("书"), Values: map[string]any{"author": "甲", "isbn": "12", "shelf": "客厅"}}, t0)
	def := *st.Spaces[0].typeDef("book")
	retype := func(mut func(d *TypeDef)) error {
		d := def
		d.Fields = slices.Clone(def.Fields)
		mut(&d)
		_, err := st.upsertType("reading", d)
		return err
	}
	if err := retype(func(d *TypeDef) { d.Fields[2].Kind = "number" }); err != nil || e.Values["isbn"] != 12.0 {
		t.Fatalf("text 12 becomes number 12: %v %#v", err, e.Values["isbn"])
	}
	def = *st.Spaces[0].typeDef("book")
	if err := retype(func(d *TypeDef) { d.Fields[5].Options = []string{"书房"} }); err == nil {
		t.Fatal("an option in use removed")
	}
	if err := retype(func(d *TypeDef) { d.Statuses = []string{"在读", "读完"} }); err == nil {
		t.Fatal("a status in use removed")
	}
	if err := retype(func(d *TypeDef) { d.Fields = append(d.Fields, FieldDef{Key: "lang", Kind: "text", Required: true}) }); err == nil {
		t.Fatal("a new required field the entities lack")
	}
	if err := retype(func(d *TypeDef) {
		d.Fields = slices.DeleteFunc(d.Fields, func(f FieldDef) bool { return f.Key == "shelf" })
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Values["shelf"]; ok {
		t.Fatal("a field left out takes its values")
	}
	def = *st.Spaces[0].typeDef("book")
	if err := retype(func(d *TypeDef) { d.Statuses, d.View = nil, "list" }); err != nil || e.Status != "" {
		t.Fatalf("no statuses: %v %q", err, e.Status)
	}
	def = *st.Spaces[0].typeDef("book")
	if err := retype(func(d *TypeDef) { d.Statuses = []string{"新", "旧"} }); err != nil || e.Status != "新" {
		t.Fatalf("new statuses start at the first: %v %q", err, e.Status)
	}
	for name, d := range map[string]TypeDef{
		"board without statuses": {Key: "x", View: "board"}, "progress without a number": {Key: "x", View: "progress"},
		"bad key": {Key: "X-1"}, "bad kind": {Key: "x", Fields: []FieldDef{{Key: "a", Kind: "money"}}},
		"enum without options": {Key: "x", Fields: []FieldDef{{Key: "a", Kind: "enum"}}},
		"options on text":      {Key: "x", Fields: []FieldDef{{Key: "a", Kind: "text", Options: []string{"1"}}}},
		"twice":                {Key: "x", Fields: []FieldDef{{Key: "a", Kind: "text"}, {Key: "a", Kind: "url"}}},
		"bad view":             {Key: "x", View: "gallery"},
	} {
		if _, err := st.upsertType("reading", d); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := st.deleteType("reading", "book"); err == nil {
		t.Fatal("a type with entities deleted")
	}
}

func TestStoreIsSharedBetweenProcesses(t *testing.T) {
	dir := t.TempDir()
	hub, assistant := newStore(dir), newStore(dir)
	hub.read(func(*State) {})
	if hub.changed() {
		t.Fatal("nothing changed yet")
	}
	err := assistant.update(func(st *State, _ time.Time) error {
		_, err := st.upsertSpace(SpaceInput{ID: "a", Name: ptr("甲")}, time.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hub.changed() || hub.changed() {
		t.Fatal("the other process's write is seen once")
	}
	var names []string
	hub.read(func(st *State) { names = append(names, st.Spaces[0].Name) })
	if !slices.Equal(names, []string{"甲"}) {
		t.Fatal(names)
	}
	// A failed change saves nothing.
	before, _ := os.ReadFile(filepath.Join(dir, stateFile))
	hub.update(func(st *State, _ time.Time) error {
		st.Spaces = nil
		_, err := st.upsertSpace(SpaceInput{}, time.Now())
		return err
	})
	if after, _ := os.ReadFile(filepath.Join(dir, stateFile)); string(after) != string(before) {
		t.Fatal("a failed change was saved")
	}
	// An unreadable file is reported and left alone.
	os.WriteFile(filepath.Join(dir, stateFile), []byte("{"), 0o600)
	if err := hub.update(func(*State, time.Time) error { return nil }); err == nil {
		t.Fatal("an unreadable file")
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, stateFile)); string(raw) != "{" {
		t.Fatal("an unreadable file overwritten")
	}
}

// ---- Pages ----

func ui(t *testing.T, a *app, p page) *wire.View {
	t.Helper()
	v, err := a.render(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(p), TZ: "Asia/Shanghai", UIVersion: 6}})
	if err != nil {
		t.Fatal(err)
	}
	checkView(t, v, false)
	return v
}

func act(t *testing.T, a *app, state string, action string, values map[string]string) *wire.ActionResult {
	t.Helper()
	res, err := a.action(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{State: state, Action: action, Values: values, UIVersion: 6}})
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	if res.View != nil {
		checkView(t, res.View, false)
	}
	return res
}

func checkView(t *testing.T, v *wire.View, compact bool) {
	t.Helper()
	raw, _ := json.Marshal(v)
	if err := exttest.CheckView(raw, compact); err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
}

// items are the view's components of a type.
func items(v *wire.View, typ string) []wire.Component {
	var out []wire.Component
	for _, s := range v.Sections {
		for _, c := range s.Items {
			if c.Type == typ {
				out = append(out, c)
			}
		}
	}
	return out
}

func TestPagesAreDrawnFromTheSchema(t *testing.T) {
	a := newApp(t.TempDir())
	a.now = func() time.Time { return t0.Add(2 * time.Hour) }
	a.db().now = func() time.Time { return t0 }

	v := ui(t, a, page{})
	if e := items(v, wire.ComponentEmpty); len(e) != 1 || e[0].Button == nil || v.Tabs != nil {
		t.Fatalf("no spaces: one empty state that creates one: %+v", v)
	}
	v = act(t, a, v.State, "new-space", nil).View
	v = act(t, a, v.State, "save-space", map[string]string{"name": "装修", "icon": "settings", "description": "新家的装修"}).View
	if v.Title != "装修" || v.Filter == nil || len(v.Filter.Choices) != 2 || items(v, wire.ComponentEmpty)[0].Button.Title != "新建类型" {
		t.Fatalf("a new space without types: %+v", v)
	}
	v = act(t, a, v.State, "new-type", nil).View
	v = act(t, a, v.State, "save-type", map[string]string{"key": "task", "label": "事项", "view": "board", "statuses": "待办，进行中, 完成",
		"fields": "room:房间:enum:厨房|卫生间; cost:花费:number\nurgent:加急:boolean"}).View
	if v.Tabs == nil || len(v.Tabs.Items) != 2 || v.Tabs.Value != "task" || items(v, wire.ComponentEmpty)[0].Title != "还没有事项" {
		t.Fatalf("the new type is a tab: %+v", v)
	}
	var p page
	json.Unmarshal([]byte(v.State), &p)

	// The entity form is drawn from the fields.
	form := ui(t, a, page{Space: p.Space, Type: "task", Mode: modeEdit})
	var names []string
	for _, f := range items(form, wire.ComponentField) {
		names = append(names, f.Name+":"+f.Kind)
	}
	if want := []string{"title:text", "status:choice", "f_room:choice", "f_cost:number", "f_urgent:toggle"}; !slices.Equal(names, want) {
		t.Fatalf("form fields %v, want %v", names, want)
	}
	res := act(t, a, form.State, "save-entity", map[string]string{"title": "换水龙头", "status": "待办", "f_room": "厨房", "f_cost": "260", "f_urgent": "true"})
	if !res.Pop || res.Toast == "" {
		t.Fatalf("saved and back: %+v", res)
	}
	if _, err := a.action(context.Background(), &ext.UIRequest{UIParams: wire.UIParams{State: form.State, Action: "save-entity",
		Values: map[string]string{"title": "坏的", "f_cost": "很多"}}}); err == nil || !strings.Contains(err.Error(), "cost") {
		t.Fatalf("a bad value is refused in words: %v", err)
	}
	act(t, a, form.State, "save-entity", map[string]string{"title": "刷墙", "status": "进行中", "f_room": "", "f_cost": "", "f_urgent": "false"})

	v = ui(t, a, p)
	if len(v.Sections) != 4 || v.Sections[1].Title != "待办 · 1" || v.Sections[2].Title != "进行中 · 1" {
		t.Fatalf("a board is a section per status: %+v", v.Sections)
	}
	row := v.Sections[1].Items[0]
	if row.Title != "换水龙头" || row.Subtitle != "房间 厨房 · 花费 260 · 加急" || row.Action.Push == nil {
		t.Fatalf("a row: %+v", row)
	}
	if v.Tabs.Items[0].Badge != "2" {
		t.Fatal("a tab counts its entities")
	}

	// The entity page: facts from the values, status advance, relations, delete.
	var ep page
	json.Unmarshal([]byte(row.Action.Push.State), &ep)
	ev := ui(t, a, ep)
	head := items(ev, wire.ComponentHeader)[0]
	if head.Title != "换水龙头" || head.Badges[0].Text != "待办" || len(head.Facts) != 3 || head.Facts[2].Value != "是" {
		t.Fatalf("header: %+v", head)
	}
	ev = act(t, a, ev.State, "advance", nil).View
	if items(ev, wire.ComponentHeader)[0].Badges[0].Text != "进行中" {
		t.Fatal("advanced to the next status")
	}
	lv := ui(t, a, page{Entity: ep.Entity, Mode: modeLinks})
	to := items(lv, wire.ComponentField)[1].Choices[0].Value
	lv = act(t, a, lv.State, "link", map[string]string{"rel": "先于", "to": to}).View
	if r := items(lv, wire.ComponentRow); len(r) != 1 || r[0].Title != "先于 → 刷墙" {
		t.Fatalf("linked: %+v", r)
	}
	other := ui(t, a, page{Entity: to, Mode: modeEntity})
	if r := items(other, wire.ComponentRow); len(r) != 1 || r[0].Subtitle != "← 先于 · 事项" {
		t.Fatalf("the other end shows it coming in: %+v", r)
	}
	lv = act(t, a, lv.State, "unlink", map[string]string{"i": "0"}).View
	if r := items(lv, wire.ComponentRow); len(r) != 0 {
		t.Fatal("unlinked")
	}

	// Every view kind draws the same entities in its own shape.
	for _, view := range viewKinds {
		statuses := "待办, 进行中, 完成"
		res := act(t, a, ext.EncodeState(page{Space: p.Space, Type: "task", Mode: modeType}), "save-type",
			map[string]string{"label": "事项", "view": view, "statuses": statuses, "fields": "room:房间:enum:厨房|卫生间; cost:花费:number; urgent:加急:boolean"})
		if !res.Pop {
			t.Fatal("an edited type pops back")
		}
		v := ui(t, a, p)
		switch view {
		case "table":
			tb := items(v, wire.ComponentTable)
			if len(tb) != 1 || len(tb[0].Columns) != 5 || len(tb[0].Rows) != 2 || tb[0].Rows[0][3] != "260" || tb[0].Rows[1][3] != "" {
				t.Fatalf("table: %+v", tb)
			}
			if s := ui(t, a, page{Space: p.Space, Type: "task", Query: "水龙头"}); len(items(s, wire.ComponentRow)) != 2 { // the row and settings
				t.Fatal("a search turns the table into rows")
			}
		case "progress":
			pr := items(v, wire.ComponentProgress)
			if len(pr) != 2 || items(v, wire.ComponentStatGrid)[0].Stats[0].Label != "平均进度" {
				t.Fatalf("progress: %+v", pr)
			}
		case "feed":
			if items(v, wire.ComponentRow)[0].Value != "2 小时前" {
				t.Fatalf("a feed says when: %+v", items(v, wire.ComponentRow)[0])
			}
		case "list":
			if sg := items(v, wire.ComponentStatGrid); len(sg) != 1 || len(sg[0].Stats) != 3 {
				t.Fatalf("a list counts its statuses: %+v", sg)
			}
		}
	}
	if s := ui(t, a, page{Space: p.Space, Type: "task", Query: "没有这个"}); items(s, wire.ComponentEmpty)[0].Title != "没有找到" {
		t.Fatal("an empty search")
	}

	// The space's settings list its types; the card lists the spaces.
	sv := ui(t, a, page{Space: p.Space, Mode: modeSpace})
	if r := items(sv, wire.ComponentRow); len(r) != 1 || r[0].Title != "事项" {
		t.Fatalf("settings: %+v", r)
	}
	card, err := a.card(context.Background(), &ext.UIRequest{})
	if err != nil {
		t.Fatal(err)
	}
	checkView(t, card, true)
	if r := items(card, wire.ComponentRow); len(r) != 1 || r[0].Subtitle != "1 种类型 · 2 条记录" {
		t.Fatalf("card: %+v", r)
	}
	if res := act(t, a, ev.State, "delete-entity", nil); !res.Pop {
		t.Fatal("deleted and back")
	}
	if v := ui(t, a, ep); items(v, wire.ComponentEmpty)[0].Title != "它已经不在了" {
		t.Fatal("a deleted entity's page")
	}
	act(t, a, sv.State, "delete-space", nil)
	if v := ui(t, a, page{}); v.Filter != nil {
		t.Fatal("no spaces left")
	}
}

func TestFieldsSpecRoundTrips(t *testing.T) {
	fields, err := parseFields("a:甲:text!；b:乙:enum:x|y\n c : number ")
	if err != nil {
		t.Fatal(err)
	}
	want := []FieldDef{{Key: "a", Label: "甲", Kind: "text", Required: true}, {Key: "b", Label: "乙", Kind: "enum", Options: []string{"x", "y"}}, {Key: "c", Kind: "number"}}
	got, _ := json.Marshal(fields)
	if w, _ := json.Marshal(want); string(got) != string(w) {
		t.Fatalf("%s, want %s", got, w)
	}
	if formatFields(want) != "a:甲:text!; b:乙:enum:x|y; c::number" {
		t.Fatal(formatFields(want))
	}
	if _, err := parseFields("just-a-word"); err == nil {
		t.Fatal("a field without a kind")
	}
}

// ---- The MCP server ----

func TestMCPToolsShareTheStoreWithThePages(t *testing.T) {
	dir := t.TempDir()
	toServer, in := io.Pipe()
	out, fromServer := io.Pipe()
	go (&mcpServer{store: newStore(dir)}).serve(context.Background(), toServer, fromServer)
	t.Cleanup(func() { in.Close(); out.Close() })
	lines := bufio.NewScanner(out)
	lines.Buffer(nil, 1<<20)
	id := 0
	rpc := func(method string, params any) map[string]any {
		t.Helper()
		id++
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		in.Write(append(b, '\n'))
		if !lines.Scan() {
			t.Fatal("no answer")
		}
		var resp map[string]any
		json.Unmarshal(lines.Bytes(), &resp)
		return resp
	}
	call := func(name string, args map[string]any) (map[string]any, bool) {
		t.Helper()
		res := rpc("tools/call", map[string]any{"name": name, "arguments": args})["result"].(map[string]any)
		if res["isError"] == true {
			return map[string]any{"error": res["content"].([]any)[0].(map[string]any)["text"]}, false
		}
		return res["structuredContent"].(map[string]any), true
	}
	in.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))
	if r := rpc("initialize", map[string]any{"protocolVersion": "2025-06-18"})["result"].(map[string]any); r["serverInfo"].(map[string]any)["name"] != "spaces" {
		t.Fatal(r)
	}
	list := rpc("tools/list", nil)["result"].(map[string]any)["tools"].([]any)
	if len(list) != 8 {
		t.Fatalf("%d tools", len(list))
	}

	if r, ok := call("space_query", nil); ok {
		t.Fatalf("no spaces yet: %v", r)
	}
	call("space_upsert", map[string]any{"id": "trips", "name": "旅行", "icon": "calendar"})
	if r, ok := call("space_schema_upsert", map[string]any{"key": "place", "label": "地点", "view": "list",
		"fields": []map[string]any{{"key": "city", "label": "城市", "kind": "text", "required": true}, {"key": "days", "kind": "number"}}}); !ok {
		t.Fatal(r)
	}
	call("space_schema_upsert", map[string]any{"key": "plan", "label": "行程", "view": "board", "statuses": []string{"计划", "已订", "去过"},
		"fields": []map[string]any{{"key": "start", "label": "出发", "kind": "date"}}})
	place, ok := call("space_entity_upsert", map[string]any{"type": "place", "title": "海边小城", "values": map[string]any{"city": "某市", "days": "3"}})
	if !ok {
		t.Fatal(place)
	}
	placeID := place["entity"].(map[string]any)["id"].(string)
	if days := place["entity"].(map[string]any)["values"].(map[string]any)["days"]; days != 3.0 {
		t.Fatalf("days %v", days)
	}
	plan, ok := call("space_entity_upsert", map[string]any{"type": "plan", "title": "十二月出发", "values": map[string]any{"start": "2026-12-20"},
		"relations": []map[string]any{{"type": "去往", "to": placeID}}})
	if !ok || plan["entity"].(map[string]any)["status"] != "计划" {
		t.Fatal(plan)
	}
	if r, ok := call("space_entity_upsert", map[string]any{"type": "plan", "title": "x", "values": map[string]any{"start": "下个月"}}); ok ||
		!strings.Contains(r["error"].(string), "YYYY-MM-DD") {
		t.Fatalf("a bad date is refused in words: %v", r)
	}
	if r, ok := call("space_entity_upsert", map[string]any{"type": "plan", "title": "x", "values": map[string]any{"budget": 1}}); ok ||
		!strings.Contains(r["error"].(string), "start") {
		t.Fatalf("an unknown field lists the fields: %v", r)
	}

	q, _ := call("space_query", map[string]any{"type": "place", "where": map[string]any{"days": 3}})
	ents := q["entities"].([]any)
	if q["total"] != 1.0 || len(ents) != 1 {
		t.Fatal(q)
	}
	if in := ents[0].(map[string]any)["in"].([]any); in[0].(map[string]any)["title"] != "十二月出发" {
		t.Fatalf("an entity names the other end of its edges: %v", in)
	}
	if q, _ := call("space_query", map[string]any{"query": "海边"}); q["total"] != 1.0 {
		t.Fatal(q)
	}
	l, _ := call("space_list", nil)
	sp := l["spaces"].([]any)[0].(map[string]any)
	if sp["relations"] != 1.0 || len(sp["types"].([]any)) != 2 {
		t.Fatal(sp)
	}
	sc, _ := call("space_schema_get", map[string]any{"space_id": "trips"})
	if types := sc["space"].(map[string]any)["types"].([]any); types[1].(map[string]any)["view"] != "board" {
		t.Fatal(sc)
	}

	// The pages read what the assistant wrote.
	a := newApp(dir)
	v := ui(t, a, page{Space: "trips", Type: "plan"})
	if v.Title != "旅行" || len(v.Tabs.Items) != 3 || v.Sections[1].Title != "计划 · 1" {
		t.Fatalf("%+v", v)
	}

	if d, ok := call("space_entity_delete", map[string]any{"id": placeID}); !ok || d["relations_removed"] != 1.0 {
		t.Fatal(d)
	}
	if r := rpc("tools/call", map[string]any{"name": "nope"}); r["error"] == nil {
		t.Fatal("an unknown tool is a protocol error")
	}
	if tl := l["templates"].([]any); len(tl) != len(templates) || tl[0].(map[string]any)["key"] != "project" {
		t.Fatalf("space_list names the templates: %v", tl)
	}
	tp, ok := call("space_upsert", map[string]any{"template": "inventory", "examples": true})
	if !ok || tp["space"].(map[string]any)["name"] != "清单与物品" {
		t.Fatal(tp)
	}
	invID := tp["space"].(map[string]any)["id"].(string)
	if r, ok := call("space_upsert", map[string]any{"id": "trips", "template": "reading"}); ok {
		t.Fatalf("a template does not change a space: %v", r)
	}
	wish, _ := call("space_query", map[string]any{"space_id": invID, "type": "wishes"})
	wishID := wish["entities"].([]any)[0].(map[string]any)["id"]
	d, ok := call("space_entity_derive", map[string]any{"id": wishID, "type": "items", "title": "台灯"})
	if !ok || d["entity"].(map[string]any)["out"].([]any)[0].(map[string]any)["type"] != "源自" {
		t.Fatal(d)
	}
	if r, ok := call("space_list", map[string]any{"surprise": true}); ok {
		t.Fatalf("unknown arguments: %v", r)
	}
}

func TestTemplatesAreValidSpaces(t *testing.T) {
	seen := map[string]bool{}
	for _, tpl := range templates {
		if seen[tpl.Key] || !slices.Contains(wire.NavIcons, tpl.Icon) || tpl.Name == "" {
			t.Fatalf("template %s: unique key, a nav icon and a name", tpl.Key)
		}
		seen[tpl.Key] = true
		st := &State{}
		sp, err := st.upsertSpace(SpaceInput{Template: tpl.Key, Examples: true}, t0)
		if err != nil {
			t.Fatalf("%s: %v", tpl.Key, err)
		}
		if sp.Name != tpl.Name || sp.Icon != tpl.Icon || len(sp.Types) != len(tpl.Types) || len(st.Entities) != len(tpl.examples) {
			t.Fatalf("%s: %+v, %d entities", tpl.Key, sp, len(st.Entities))
		}
		// the space's types are its own: changing them leaves the template as it is
		if len(sp.Types) > 0 {
			sp.Types[0].Fields[0].Label = "改过"
			if tpl.Types[0].Fields[0].Label == "改过" {
				t.Fatal("a space shares its template's fields")
			}
		}
	}
	if !slices.Equal(templateKeys(), []string{"project", "reading", "inventory", "blank"}) {
		t.Fatal(templateKeys())
	}
	st := &State{}
	if _, err := st.upsertSpace(SpaceInput{Template: "nope"}, t0); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("an unknown template names the ones there are: %v", err)
	}
	sp, err := st.upsertSpace(SpaceInput{Template: "project", Name: ptr("副业"), Icon: ptr(""), Description: ptr("")}, t0)
	if err != nil || sp.Name != "副业" || sp.Icon != "star" || sp.Description == "" || len(st.Entities) != 0 {
		t.Fatalf("a name given wins, empty ones are the template's, no examples unless asked: %+v %v", sp, err)
	}
	if _, err := st.upsertSpace(SpaceInput{ID: sp.ID, Template: "reading"}, t0); err == nil {
		t.Fatal("a template only makes a new space")
	}
}

func TestDeriveCarriesSharedFieldsAndPointsBack(t *testing.T) {
	st := &State{}
	sp, _ := st.upsertSpace(SpaceInput{Template: "project", Examples: true}, t0)
	idea, _, _ := st.query(Query{Space: sp.ID, Type: "ideas"})
	goal, err := st.deriveEntity(idea[0].ID, "goals", "", "", t0)
	if err != nil {
		t.Fatal(err)
	}
	if goal.Type != "goals" || goal.Title != idea[0].Title || goal.Status != "规划中" {
		t.Fatalf("%+v", goal)
	}
	if out := st.outgoing(goal.ID); len(out) != 1 || out[0] != (Relation{From: goal.ID, Type: "源自", To: idea[0].ID}) {
		t.Fatalf("points back: %v", out)
	}
	// a field both types have goes along (log and ideas both have summary)
	entry, err := st.deriveEntity(idea[0].ID, "log", "孵化了一个点子", "记录", t0)
	if err != nil || entry.Values["summary"] != idea[0].Values["summary"] || st.outgoing(entry.ID)[0].Type != "记录" {
		t.Fatalf("%+v %v", entry, err)
	}
	if _, err := st.deriveEntity(idea[0].ID, "nope", "", "", t0); err == nil {
		t.Fatal("an unknown type is refused")
	}
}

func TestTemplatesOnThePage(t *testing.T) {
	a := newApp(t.TempDir())
	a.now = func() time.Time { return t0.Add(time.Hour) }
	a.db().now = func() time.Time { return t0 }

	v := ui(t, a, page{})
	rows := v.Sections[1].Items
	if len(rows) != len(templates) || rows[0].Title != "项目与目标" || rows[3].Action.ID != "new-space" {
		t.Fatalf("the empty home offers the templates: %+v", v.Sections)
	}
	v = act(t, a, v.State, rows[0].Action.ID, rows[0].Action.Values).View
	if v.Title != "项目与目标" || len(v.Tabs.Items) != 5 || v.Tabs.Items[1].Title != "目标" {
		t.Fatalf("one tap makes the space with its types: %+v", v)
	}

	v = act(t, a, v.State, "space", map[string]string{"space": "+"}).View
	if f := items(v, wire.ComponentField); f[0].Name != "template" || len(f[0].Choices) != len(templates) || f[3].Choices[0].Value != "" {
		t.Fatalf("the form picks a template: %+v", f)
	}
	v = act(t, a, v.State, "save-space", map[string]string{"template": "reading", "examples": "true", "name": "", "icon": "", "description": ""}).View
	if v.Title != "阅读与知识" || v.Tabs.Value != "books" || len(items(v, wire.ComponentProgress)) != 1 {
		t.Fatalf("from the form, with its examples: %+v", v)
	}

	// An entity derives into another type of its space.
	var p page
	json.Unmarshal([]byte(v.State), &p)
	var bookID string
	a.db().read(func(st *State) {
		ents, _, _ := st.query(Query{Space: p.Space, Type: "books"})
		bookID = ents[0].ID
	})
	ev := ui(t, a, page{Entity: bookID, Mode: modeEntity})
	var derive *wire.Section
	for i := range ev.Sections {
		if ev.Sections[i].Title == "衍生" {
			derive = &ev.Sections[i]
		}
	}
	if derive == nil || derive.Items[0].Value != "notes" || len(derive.Items[0].Choices) != 2 {
		t.Fatalf("the entity page derives: %+v", ev.Sections)
	}
	res := act(t, a, ev.State, "derive", map[string]string{"derive_to": "clips"})
	if res.Push == nil || !strings.Contains(res.Toast, "摘录与链接") {
		t.Fatalf("a derived entity opens: %+v", res)
	}
	var np page
	json.Unmarshal([]byte(res.Push.State), &np)
	nv := ui(t, a, np)
	if rel := items(nv, wire.ComponentRow); len(rel) == 0 || !strings.Contains(rel[0].Subtitle, "源自") {
		t.Fatalf("the new entity points back: %+v", nv.Sections)
	}
}
