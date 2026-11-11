// Package mcp serves the spaces tools over the Model Context Protocol: line-delimited JSON-RPC 2.0 on a reader and a
// writer. The data is reached through kernel.Storage only; the kernel does the work and this package turns its answers
// into JSON and its errors into tool results.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/nwverse/persona-extensions/extensions/spaces/internal/templates"
	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// The protocol revision this server speaks, and how it identifies itself.
const (
	protocolVersion = "2025-06-18"
	serverName      = "spaces"
	serverVersion   = "0.1.0"
)

// JSON-RPC 2.0 error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

// Tool is one tool of the server. Read marks a tool that never changes the data (readOnlyHint). Input builds its JSON
// Schema from the kernel's generators. Run does the work on the base and returns the answer, which is marshalled to
// JSON as both the text and the structured content of the result.
type Tool struct {
	Name, About string
	Read        bool
	Input       func() map[string]any
	Run         func(b *kernel.Base, now time.Time, raw json.RawMessage) (any, error)
}

// destructive names the tools whose effect cannot be undone or that overwrite stored data (destructiveHint). Only
// entity_derive, which adds a record and changes nothing else, is a write that is not destructive.
var destructive = map[string]bool{"space_write": true, "type_write": true, "entity_write": true, "delete": true}

// table is the one list of tools. The manifest allows exactly the read-only ones.
var table = []Tool{
	{Name: "schema", Read: true, Input: schemaInput, Run: runSchema,
		About: "读取空间结构：类型、属性、生命周期和视图。不填空间时（有多个空间或还没有空间）列出所有空间、模板和可用词汇。"},
	{Name: "query", Read: true, Input: queryInput, Run: runQuery,
		About: "查询记录：按类型、文字、条件、排序、分组和汇总读取。带记录编号时只读这一条记录。"},
	{Name: "space_write", Input: spaceWriteInput, Run: runSpaceWrite,
		About: "新建或修改空间。可以从模板新建（可带示例记录），也可以修改名称、图标和简介。"},
	{Name: "type_write", Input: typeWriteInput, Run: runTypeWrite,
		About: "新增或整体替换一个类型的结构（生命周期、属性、卡片和视图）。替换时已有记录迁移到新结构，有记录不符合时整体拒绝。"},
	{Name: "entity_write", Input: entityWriteInput, Run: runEntityWrite,
		About: "新建或修改记录。修改时属性合并写入，值为 null 时清除该属性；状态只能按生命周期走一步。"},
	{Name: "entity_derive", Input: deriveInput, Run: runDerive,
		About: "从一条记录衍生出另一类型的新记录，结构兼容的属性会带过去；源记录不变。"},
	{Name: "delete", Input: deleteInput, Run: runDelete,
		About: "删除一条记录、一个类型（连同它的记录）或一个空间（连同它的全部内容）。不能撤销。"},
}

// Tools returns the tools in the order the server lists them.
func Tools() []Tool { return slices.Clone(table) }

// Serve reads requests from in, one JSON object per line, and writes one answer per request to out, until in ends or
// ctx is done. A message without an id is a notification and is never answered. Each tool call runs on st: a read
// through View, a write through Update, so a failed write saves nothing. now gives the time of a read.
func Serve(ctx context.Context, in io.Reader, out io.Writer, st kernel.Storage, now func() time.Time) error {
	r := bufio.NewReader(in)
	s := &server{st: st, now: now}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			if reply := s.handle(line); reply != nil {
				if _, werr := out.Write(append(reply, '\n')); werr != nil {
					return werr
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// server holds what every request needs.
type server struct {
	st  kernel.Storage
	now func() time.Time
}

// request is one incoming message. ID is nil for a notification; a null id keeps the bytes "null".
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  *string         `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// response is one answer. Exactly one of Result and Error is set.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// handle answers one line. It returns nil when the line gets no answer.
func (s *server) handle(line []byte) []byte {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return reply(nil, nil, &rpcError{Code: codeParse, Message: "Parse error"})
		}
		return reply(answerID(req.ID), nil, &rpcError{Code: codeInvalidRequest, Message: "Invalid Request"})
	}
	// An id that is not a string, a number or null is refused before anything runs; the answer carries id null.
	if !validID(req.ID) {
		return reply(nil, nil, &rpcError{Code: codeInvalidRequest, Message: "Invalid Request"})
	}
	if req.JSONRPC != "2.0" || req.Method == nil {
		return reply(req.ID, nil, &rpcError{Code: codeInvalidRequest, Message: "Invalid Request"})
	}
	if len(req.ID) == 0 {
		return nil
	}
	result, rerr := s.dispatch(*req.Method, req.Params)
	return reply(req.ID, result, rerr)
}

// validID reports whether a raw id is a string, a number or null, the only kinds JSON-RPC allows. An absent id (empty
// bytes) is valid here: it marks a notification. The raw bytes come from a decoder, so they are valid JSON.
func validID(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return true
	}
	switch c := t[0]; {
	case c == '"', c == '-', c >= '0' && c <= '9', c == 'n':
		return true
	}
	return false
}

// answerID is the id a refusal echoes: the id as it was sent when it is valid, else nil (null).
func answerID(raw json.RawMessage) json.RawMessage {
	if validID(raw) {
		return raw
	}
	return nil
}

// dispatch runs one method and returns its result, or the JSON-RPC error.
func (s *server) dispatch(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return initResult(), nil
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return toolList(), nil
	case "tools/call":
		return s.call(params)
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "Method not found: " + method}
}

// reply encodes an answer. The encoding of a valid result cannot fail; one that does (a number that is not finite) is
// answered with an internal error instead.
func reply(id json.RawMessage, result any, rerr *rpcError) []byte {
	resp := response{JSONRPC: "2.0", ID: id, Error: rerr}
	if rerr == nil {
		data, err := json.Marshal(result)
		if err != nil {
			resp.Error = &rpcError{Code: codeInternal, Message: "Internal error: " + err.Error()}
		} else {
			resp.Result = data
		}
	}
	out, _ := json.Marshal(resp) // every part is valid JSON, so this cannot fail
	return out
}

func initResult() map[string]any {
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
	}
}

// toolInfo is one entry of tools/list.
type toolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
}

func toolList() map[string]any {
	infos := []toolInfo{}
	for _, t := range Tools() {
		infos = append(infos, toolInfo{
			Name:        t.Name,
			Description: t.About,
			InputSchema: t.Input(),
			Annotations: map[string]any{"readOnlyHint": t.Read, "destructiveHint": destructive[t.Name]},
		})
	}
	return map[string]any{"tools": infos}
}

// callParams is the params of tools/call.
type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type textItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// callResult is the result of tools/call. A failed tool is a result with IsError, not a JSON-RPC error.
type callResult struct {
	Content           []textItem      `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError"`
}

// call runs a tool. An unknown tool or malformed params are protocol errors; everything the tool itself reports is an
// error result carrying the kernel's sentence.
func (s *server) call(params json.RawMessage) (any, *rpcError) {
	var p callParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "Invalid params: " + err.Error()}
	}
	t, ok := find(p.Name)
	if !ok {
		return nil, &rpcError{Code: codeInvalidParams, Message: "Unknown tool: " + p.Name}
	}
	data, err := s.run(t, p.Arguments)
	if err != nil {
		return callResult{Content: []textItem{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}
	return callResult{Content: []textItem{{Type: "text", Text: string(data)}}, StructuredContent: data}, nil
}

func find(name string) (Tool, bool) {
	i := slices.IndexFunc(table, func(t Tool) bool { return t.Name == name })
	if i < 0 {
		return Tool{}, false
	}
	return table[i], true
}

// run executes a tool on the store and returns its answer as JSON. The answer is marshalled inside the store call, so
// it never refers to the base after the call has returned.
func (s *server) run(t Tool, raw json.RawMessage) ([]byte, error) {
	var out []byte
	do := func(b *kernel.Base, at time.Time) error {
		v, err := t.Run(b, at, raw)
		if err != nil {
			return err
		}
		out, err = json.Marshal(v)
		return err
	}
	var err error
	if t.Read {
		err = s.st.View(func(b *kernel.Base) error { return do(b, s.now()) })
	} else {
		err = s.st.Update(do)
	}
	return out, err
}

// decode reads the arguments of a tool call. A property the schema does not list, or a value of the wrong type, is
// refused. Missing arguments decode to nothing.
func decode(raw json.RawMessage, v any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(trimmed))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("参数不对：%w", err)
	}
	return nil
}

// object is a JSON Schema object that refuses every property it does not list.
func object(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "additionalProperties": false, "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

// str is a JSON Schema string with a description.
func str(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// spaceProp is the space parameter that most tools take.
func spaceProp() map[string]any {
	return str("空间 id；只有一个空间时可以不填")
}

// templateKeys are the keys of the templates, in their order.
func templateKeys() []string {
	all := templates.All()
	keys := make([]string, 0, len(all))
	for _, t := range all {
		keys = append(keys, t.Key)
	}
	return keys
}

// spaceView is a space as the tools answer it: its name and counts, not its records.
type spaceView struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Icon     string     `json:"icon,omitempty"`
	About    string     `json:"about,omitempty"`
	Entities int        `json:"entities"`
	Types    []typeInfo `json:"types"`
}

// typeInfo is a type with its record count.
type typeInfo struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// typeView is a complete type with its record count and the JSON Schema of its values.
type typeView struct {
	kernel.Type
	Count        int            `json:"count"`
	ValuesSchema map[string]any `json:"values_schema"`
}

// templateInfo is a template as the schema tool lists it.
type templateInfo struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Icon  string `json:"icon"`
	About string `json:"about"`
}

// countOf is how many records of the type the space holds.
func countOf(sp *kernel.Space, key string) int {
	n := 0
	for _, e := range sp.Entities {
		if e.Type == key {
			n++
		}
	}
	return n
}

// viewOf is the summary of a space.
func viewOf(sp *kernel.Space) spaceView {
	out := spaceView{ID: sp.ID, Name: sp.Name, Icon: sp.Icon, About: sp.About, Entities: len(sp.Entities), Types: []typeInfo{}}
	for _, t := range sp.Types {
		out.Types = append(out.Types, typeInfo{Key: t.Key, Name: t.Name, Count: countOf(sp, t.Key)})
	}
	return out
}

// typeOf is a complete type of the space with its count and values schema.
func typeOf(sp *kernel.Space, t *kernel.Type) typeView {
	return typeView{Type: *t, Count: countOf(sp, t.Key), ValuesSchema: kernel.ValuesSchema(t)}
}

// typeByKey finds a type of the space, nil when there is none.
func typeByKey(sp *kernel.Space, key string) *kernel.Type {
	i := slices.IndexFunc(sp.Types, func(t *kernel.Type) bool { return t.Key == key })
	if i < 0 {
		return nil
	}
	return sp.Types[i]
}

// vocabulary is the kernel's vocabulary with the template keys added.
func vocabulary() map[string]any {
	v := kernel.Vocabulary()
	v["templates"] = templateKeys()
	return v
}

func templateList() []templateInfo {
	out := []templateInfo{}
	for _, t := range templates.All() {
		out = append(out, templateInfo{Key: t.Key, Name: t.Name, Icon: t.Icon, About: t.About})
	}
	return out
}

// ---- schema ----

type schemaArgs struct {
	Space string `json:"space"`
}

// schemaOverview answers the schema tool when no single space is meant: every space, the templates and the vocabulary.
type schemaOverview struct {
	Spaces     []spaceView    `json:"spaces"`
	Templates  []templateInfo `json:"templates"`
	Vocabulary map[string]any `json:"vocabulary"`
}

// schemaSpace answers the schema tool for one space: its complete types.
type schemaSpace struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Icon  string     `json:"icon,omitempty"`
	About string     `json:"about,omitempty"`
	Types []typeView `json:"types"`
}

func schemaInput() map[string]any {
	return object(map[string]any{"space": spaceProp()})
}

func runSchema(b *kernel.Base, _ time.Time, raw json.RawMessage) (any, error) {
	var a schemaArgs
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if a.Space == "" && len(b.Spaces) != 1 {
		out := schemaOverview{Spaces: []spaceView{}, Templates: templateList(), Vocabulary: vocabulary()}
		for _, sp := range b.Spaces {
			out.Spaces = append(out.Spaces, viewOf(sp))
		}
		return out, nil
	}
	sp, err := b.Space(a.Space)
	if err != nil {
		return nil, err
	}
	out := schemaSpace{ID: sp.ID, Name: sp.Name, Icon: sp.Icon, About: sp.About, Types: []typeView{}}
	for _, t := range sp.Types {
		out.Types = append(out.Types, typeOf(sp, t))
	}
	return out, nil
}

// ---- query ----

// queryArgs are the arguments of query: a space, a record id, or the query parts.
type queryArgs struct {
	Space string `json:"space"`
	ID    string `json:"id"`
	kernel.Query
}

type linkOut struct {
	Prop  string `json:"prop"`
	ID    string `json:"id"`
	Title string `json:"title"`
}

type backOut struct {
	Type  string `json:"type"`
	Prop  string `json:"prop"`
	ID    string `json:"id"`
	Title string `json:"title"`
}

// entityOut is one record as the tools answer it.
type entityOut struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Title   string         `json:"title"`
	Status  string         `json:"status"`
	Phase   string         `json:"phase"`
	Moves   []string       `json:"moves"`
	Values  map[string]any `json:"values"`
	Out     []linkOut      `json:"out"`
	In      []backOut      `json:"in"`
	Created time.Time      `json:"created"`
	Updated time.Time      `json:"updated"`
}

type groupOut struct {
	Key   string        `json:"key"`
	Name  string        `json:"name"`
	Count int           `json:"count"`
	Aggs  []kernel.Cell `json:"aggs,omitempty"`
}

// queryOut is the answer to query. Entities are the page's records, each once, in group order.
type queryOut struct {
	Total    int           `json:"total"`
	Aggs     []kernel.Cell `json:"aggs,omitempty"`
	Groups   []groupOut    `json:"groups"`
	Entities []entityOut   `json:"entities"`
}

func queryInput() map[string]any {
	s := kernel.QuerySchema()
	props := s["properties"].(map[string]any)
	props["space"] = spaceProp()
	props["id"] = str("只看这一条记录（不需要 type）")
	return s
}

func runQuery(b *kernel.Base, _ time.Time, raw json.RawMessage) (any, error) {
	var a queryArgs
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if a.ID != "" {
		return queryOne(b, a)
	}
	sp, err := b.Space(a.Space)
	if err != nil {
		return nil, err
	}
	w := kernel.Open(sp)
	res, err := w.Run(a.Query)
	if err != nil {
		return nil, err
	}
	out := queryOut{Total: res.Total, Aggs: res.Aggs, Groups: []groupOut{}, Entities: []entityOut{}}
	seen := map[string]bool{}
	for _, g := range res.Groups {
		out.Groups = append(out.Groups, groupOut{Key: g.Key, Name: g.Name, Count: g.Count, Aggs: g.Aggs})
		for _, e := range g.Items {
			if !seen[e.ID] {
				seen[e.ID] = true
				out.Entities = append(out.Entities, entityOf(sp, w, e))
			}
		}
	}
	return out, nil
}

// queryOne answers a query for one record. The other query parts are not used.
func queryOne(b *kernel.Base, a queryArgs) (any, error) {
	sp, e, err := b.Find(a.ID)
	if err != nil {
		return nil, err
	}
	if a.Space != "" {
		want, err := b.Space(a.Space)
		if err != nil {
			return nil, err
		}
		if want != sp {
			return nil, fmt.Errorf("记录「%s」不在空间「%s」里", a.ID, want.ID)
		}
	}
	return queryOut{
		Total:    1,
		Groups:   []groupOut{{Count: 1}},
		Entities: []entityOut{entityOf(sp, kernel.Open(sp), e)},
	}, nil
}

// entityOf is one record with its phase, the moves it may make, its values (rollups included) and its links.
func entityOf(sp *kernel.Space, w *kernel.World, e *kernel.Entity) entityOut {
	out := entityOut{
		ID: e.ID, Type: e.Type, Title: e.Title, Status: e.Status,
		Moves: []string{}, Values: map[string]any{}, Out: []linkOut{}, In: []backOut{},
		Created: e.Created, Updated: e.Updated,
	}
	if t := typeByKey(sp, e.Type); t != nil {
		if st, ok := t.Stage(e.Status); ok {
			out.Phase = string(st.Phase)
		}
		out.Moves = append(out.Moves, t.Moves(e.Status)...)
		for _, p := range t.Props {
			if v := w.Value(e, p.Key); v != nil {
				out.Values[p.Key] = v
			}
		}
	}
	for _, l := range w.Out(e) {
		title := ""
		if t := w.Entity(l.To); t != nil {
			title = t.Title
		}
		out.Out = append(out.Out, linkOut{Prop: l.Prop, ID: l.To, Title: title})
	}
	for _, l := range w.In(e) {
		src := w.Entity(l.From)
		out.In = append(out.In, backOut{Type: src.Type, Prop: l.Prop, ID: l.From, Title: src.Title})
	}
	return out
}

// ---- space_write ----

// spaceWriteArgs are the arguments of space_write: a kernel space input, a template and whether to add its samples.
type spaceWriteArgs struct {
	kernel.SpaceInput
	Template string `json:"template"`
	Samples  bool   `json:"samples"`
}

func spaceWriteInput() map[string]any {
	return object(map[string]any{
		"id":       str("空间 id：不填则新建一个空间；填已有的 id 则修改它"),
		"name":     map[string]any{"type": "string", "maxLength": kernel.MaxName, "description": "名称"},
		"icon":     str("图标名"),
		"about":    map[string]any{"type": "string", "maxLength": kernel.MaxAbout, "description": "简介"},
		"template": map[string]any{"type": "string", "enum": templateKeys(), "description": "模板标识（见 schema 的 templates）；只用于新建"},
		"samples":  map[string]any{"type": "boolean", "description": "是否带示例记录；只随模板新建时有效，默认不带"},
	})
}

func runSpaceWrite(b *kernel.Base, now time.Time, raw json.RawMessage) (any, error) {
	var a spaceWriteArgs
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if a.Template == "" {
		if a.Samples {
			return nil, errors.New("示例记录只能随模板新建")
		}
		sp, err := b.PutSpace(a.SpaceInput)
		if err != nil {
			return nil, err
		}
		return viewOf(sp), nil
	}
	if a.ID != "" {
		return nil, errors.New("用模板新建空间时不能指定编号")
	}
	name := ""
	if a.Name != nil {
		name = *a.Name
	}
	sp, err := templates.Apply(b, a.Template, name, a.Samples, now)
	if err != nil {
		return nil, err
	}
	if a.Icon != nil || a.About != nil {
		sp, err = b.PutSpace(kernel.SpaceInput{ID: sp.ID, Icon: a.Icon, About: a.About})
		if err != nil {
			return nil, err
		}
	}
	return viewOf(sp), nil
}

// ---- type_write ----

type typeWriteArgs struct {
	Space string       `json:"space"`
	Type  *kernel.Type `json:"type"`
}

func typeWriteInput() map[string]any {
	return object(map[string]any{
		"space": spaceProp(),
		"type":  kernel.TypeSchema(),
	}, "type")
}

func runTypeWrite(b *kernel.Base, now time.Time, raw json.RawMessage) (any, error) {
	var a typeWriteArgs
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	sp, err := b.Space(a.Space)
	if err != nil {
		return nil, err
	}
	if a.Type == nil {
		return nil, errors.New("需要一个类型的结构")
	}
	if err := sp.PutType(*a.Type, now); err != nil {
		return nil, err
	}
	return typeOf(sp, typeByKey(sp, a.Type.Key)), nil
}

// ---- entity_write ----

// entityWriteArgs are the arguments of entity_write: a space and a kernel entity input.
type entityWriteArgs struct {
	Space string `json:"space"`
	kernel.EntityInput
}

func entityWriteInput() map[string]any {
	return object(map[string]any{
		"space":  spaceProp(),
		"id":     str("记录编号：填写则修改这条记录，不填则新建"),
		"type":   str("类型标识（只用于新建）"),
		"title":  map[string]any{"type": "string", "maxLength": kernel.MaxTitle, "description": "标题"},
		"status": str("状态名：生命周期中的一个阶段"),
		"values": map[string]any{"type": "object", "description": "属性 key → 值，合并写入，null 清除；格式见 schema 的 values_schema"},
	})
}

// spaceFor resolves the space of a record: the one that holds the record when only its id is given, else the named
// one (or the only one).
func spaceFor(b *kernel.Base, space, id string) (*kernel.Space, error) {
	if space == "" && id != "" {
		sp, _, err := b.Find(id)
		return sp, err
	}
	return b.Space(space)
}

func runEntityWrite(b *kernel.Base, now time.Time, raw json.RawMessage) (any, error) {
	var a entityWriteArgs
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	sp, err := spaceFor(b, a.Space, a.ID)
	if err != nil {
		return nil, err
	}
	e, err := sp.PutEntity(b, a.EntityInput, now)
	if err != nil {
		return nil, err
	}
	return entityOf(sp, kernel.Open(sp), e), nil
}

// ---- entity_derive ----

type deriveArgs struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

// deriveOut is the new record and the keys that were carried over from its source.
type deriveOut struct {
	Entity    entityOut `json:"entity"`
	Inherited []string  `json:"inherited"`
}

func deriveInput() map[string]any {
	return object(map[string]any{
		"id":    str("源记录的编号"),
		"type":  str("新记录的类型标识（同一空间）"),
		"title": map[string]any{"type": "string", "maxLength": kernel.MaxTitle, "description": "新记录的标题；不填则沿用源记录的标题"},
	}, "id", "type")
}

func runDerive(b *kernel.Base, now time.Time, raw json.RawMessage) (any, error) {
	var a deriveArgs
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	sp, _, err := b.Find(a.ID)
	if err != nil {
		return nil, err
	}
	e, err := sp.Derive(b, a.ID, a.Type, a.Title, now)
	if err != nil {
		return nil, err
	}
	inherited := []string{}
	for k := range e.Values {
		inherited = append(inherited, k)
	}
	slices.Sort(inherited)
	return deriveOut{Entity: entityOf(sp, kernel.Open(sp), e), Inherited: inherited}, nil
}

// ---- delete ----

// deleteArgs are the arguments of delete. A pointer is set only when the property is present, so a present empty
// string can be told from a missing one.
type deleteArgs struct {
	Entity *string `json:"entity"`
	Space  *string `json:"space"`
	Type   *string `json:"type"`
}

// deleteOut says what was removed. Unlinked counts the relations that lost a record; Entities counts the records
// removed with a type or a space.
type deleteOut struct {
	Deleted  string `json:"deleted"`
	ID       string `json:"id"`
	Type     string `json:"type,omitempty"`
	Unlinked int    `json:"unlinked,omitempty"`
	Entities int    `json:"entities,omitempty"`
}

// deleteRule is the rule of delete's arguments, in the words a model reads: exactly one of the three forms.
const deleteRule = "只能选一种：只填 entity 删除一条记录；只填 space 删除整个空间（连同它的全部内容）；" +
	"填 space 和 type 删除这个空间里的一个类型（连同它的记录）。不能同时填 entity 和 space 或 type，" +
	"也不能只填 type，字段值不能是空字符串。"

// deleteInput is a plain object: the rule above is stated in its description, not in a schema combinator, because some
// clients refuse a root schema with oneOf, anyOf or allOf.
func deleteInput() map[string]any {
	s := object(map[string]any{
		"entity": str("要删除的记录编号"),
		"space":  str("空间 id；与 type 一起使用时删除这个类型，单独使用时删除整个空间"),
		"type":   str("类型标识；与 space 一起使用"),
	})
	s["description"] = deleteRule
	return s
}

func runDelete(b *kernel.Base, _ time.Time, raw json.RawMessage) (any, error) {
	var a deleteArgs
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	switch {
	case a.Entity != nil && (a.Space != nil || a.Type != nil):
		return nil, errors.New("记录不能和空间或类型一起指定")
	case a.Entity != nil && *a.Entity == "":
		return nil, errors.New("记录编号不能是空字符串")
	case a.Entity != nil:
		sp, _, err := b.Find(*a.Entity)
		if err != nil {
			return nil, err
		}
		n, err := sp.Drop(*a.Entity)
		if err != nil {
			return nil, err
		}
		return deleteOut{Deleted: "entity", ID: *a.Entity, Unlinked: n}, nil
	case a.Space == nil && a.Type != nil:
		return nil, errors.New("删除类型时需要同时指定空间")
	case a.Space == nil:
		return nil, errors.New("需要指定记录、类型或空间")
	case *a.Space == "":
		return nil, errors.New("空间 id 不能是空字符串")
	case a.Type != nil && *a.Type == "":
		return nil, errors.New("类型标识不能是空字符串")
	case a.Type != nil:
		sp, err := b.Space(*a.Space)
		if err != nil {
			return nil, err
		}
		n := countOf(sp, *a.Type)
		if err := sp.DropType(*a.Type); err != nil {
			return nil, err
		}
		return deleteOut{Deleted: "type", ID: sp.ID, Type: *a.Type, Entities: n}, nil
	default:
		sp, err := b.Space(*a.Space)
		if err != nil {
			return nil, err
		}
		n := len(sp.Entities)
		if err := b.DropSpace(sp.ID); err != nil {
			return nil, err
		}
		return deleteOut{Deleted: "space", ID: sp.ID, Entities: n}, nil
	}
}
