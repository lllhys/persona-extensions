package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// `spaces mcp` serves the ontology to the assistant: the Model Context Protocol on stdio (JSON-RPC 2.0, a message a
// line). Three tools read and the manifest allows them; five write and go by the conversation's permission policy.
// It shares spaces_state.json with the extension the hub runs, whose pages draw again when the file changes.

const mcpProtocol = "2025-06-18"

type mcpServer struct{ store *store }

type tool struct {
	name, description string
	readOnly          bool
	props             map[string]any
	required          []string
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

var (
	spaceArg = str("空间 id（见 space_list）；只有一个空间时可以不填")
	fieldDef = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"key", "kind"}, "properties": map[string]any{
		"key": str("snake_case，小写字母开头"), "label": str("显示名"),
		"kind":     map[string]any{"type": "string", "enum": fieldKinds},
		"options":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "enum 的选项"},
		"required": map[string]any{"type": "boolean"},
	}}
	link = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"type", "to"}, "properties": map[string]any{
		"type": str("关系名，例如 属于、依赖、参考"), "to": str("同一空间里另一条记录的 id"),
	}}
)

var tools = []tool{
	{name: "space_list", readOnly: true, props: map[string]any{},
		description: "列出所有空间：id、名称、说明，每个空间的类型（key、名称、视图、记录数）和关系数。不确定用户说的是哪个空间时先看这个。"},
	{name: "space_schema_get", readOnly: true, props: map[string]any{"space_id": spaceArg},
		description: "一个空间的完整结构：每种类型的字段（key、名称、kind、选项、是否必填）、状态和视图。写入记录前先看，值必须符合字段。"},
	{name: "space_query", readOnly: true,
		props: map[string]any{
			"space_id": spaceArg, "type": str("只查这种类型"), "id": str("只看这一条记录"),
			"query": str("标题或文本值包含的文字"), "status": str("状态"),
			"where": map[string]any{"type": "object", "description": "字段 key → 值，精确匹配（需要同时给 type）"},
			"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "description": "默认 50"},
		},
		description: "查记录：类型、状态、字段值、文字都能筛，最近改过的在前。每条带着它的关系：out 是它指向的，in 是指向它的。"},
	{name: "space_upsert",
		props: map[string]any{
			"id": str("要修改的空间；新建时可以指定一个小写 id，不填自动生成"), "name": str("名称"),
			"icon":        str("图标：folder、star、money、doc、calendar、chart、list、check、tag、link、person、clock、code 等"),
			"description": str("一句话说明这个空间管什么"),
			"template": map[string]any{"type": "string", "enum": templateKeys(),
				"description": "新建时从模板开始（见 space_list 的 templates）：project 项目与目标、reading 阅读与知识、inventory 清单与物品、blank 空白；名称、图标、说明不填就用模板的"},
			"examples": map[string]any{"type": "boolean", "description": "和 template 一起：顺带写入模板的几条示例记录，默认不写"},
		},
		description: "新建或修改一个空间（名称、图标、说明）。新建时可以给 template，一步建好模板的全部类型；否则空间建好后用 space_schema_upsert 定义它的类型。"},
	{name: "space_schema_upsert", required: []string{"key"},
		props: map[string]any{
			"space_id": spaceArg, "key": str("类型 key，snake_case"), "label": str("显示名"),
			"view": map[string]any{"type": "string", "enum": viewKinds,
				"description": "呈现方式：list 列表；board 按状态分列的看板（需要 statuses）；progress 进度条（第一个 number 字段是当前值，第二个是目标值，没有则按 100）；table 表格；feed 按时间倒序的动态流"},
			"statuses": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "依次流转的状态，新记录从第一个开始；可不填"},
			"fields":   map[string]any{"type": "array", "items": fieldDef, "description": "这种类型的全部字段；没列出的字段连同它的值会被删掉"},
		},
		description: "定义一种类型，或用完整的新定义替换它。已有记录必须符合新定义，否则整个修改被拒绝并说明是哪条记录。"},
	{name: "space_entity_upsert",
		props: map[string]any{
			"space_id": spaceArg, "id": str("要修改的记录；不填就是新建"), "type": str("新建时的类型 key"),
			"title": str("标题（新建必填）"), "status": str("状态，必须是类型的 statuses 之一"),
			"values":    map[string]any{"type": "object", "description": "字段 key → 值，按字段 kind 校验；修改时合并，null 删除一个值"},
			"relations": map[string]any{"type": "array", "items": link, "description": "它指向的全部关系；给了就整体替换，不给保持不变"},
		},
		description: "新建或修改一条记录。值按字段的 kind 严格校验：number 要数字，date 是 YYYY-MM-DD，enum 必须是选项之一，url 是 http(s) 链接；不认识的字段会被拒绝并列出可用字段。"},
	{name: "space_entity_derive", required: []string{"id", "type"},
		props: map[string]any{
			"id": str("从哪条记录衍生"), "type": str("新记录的类型 key（同一空间里的另一种类型）"),
			"title": str("新记录的标题，不填沿用原记录的"), "relation": str("新记录指向原记录的关系名，默认「源自」"),
		},
		description: "从一条记录衍生出另一种类型的新记录（例如把点子孵化成目标）：带上两种类型都有且符合的字段值，并建立一条从新记录指向原记录的关系。"},
	{name: "space_entity_delete", required: []string{"id"}, props: map[string]any{"id": str("记录 id")},
		description: "删除一条记录和它所有的关系。删除前先和用户确认是哪一条。"},
}

func (t tool) schema() map[string]any {
	s := map[string]any{"type": "object", "properties": t.props, "additionalProperties": false}
	if len(t.required) > 0 {
		s["required"] = t.required
	}
	return map[string]any{"name": t.name, "description": t.description, "inputSchema": s,
		"annotations": map[string]any{"readOnlyHint": t.readOnly, "destructiveHint": t.name == "space_entity_delete"}}
}

// ---- JSON-RPC ----

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// serve answers the requests read from in on out until in ends.
func (m *mcpServer) serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() && ctx.Err() == nil {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		resp := map[string]any{"jsonrpc": "2.0"}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			resp["id"], resp["error"] = nil, rpcError{-32700, "parse error"}
		} else if len(msg.ID) == 0 || string(msg.ID) == "null" {
			continue // a notification
		} else {
			res, rerr := m.handle(msg.Method, msg.Params)
			resp["id"] = msg.ID
			if rerr != nil {
				resp["error"] = rerr
			} else {
				resp["result"] = res
			}
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (m *mcpServer) handle(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &p)
		return map[string]any{
			"protocolVersion": cmp.Or(p.ProtocolVersion, mcpProtocol),
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "spaces", "version": about.Version},
			"instructions": "用户的空间：每个空间由类型（字段、状态、视图）和记录、关系组成，页面按这些结构呈现。" +
				"先 space_list，再 space_schema_get 看字段，再读写；用户想管理一件新的事情时，先看 space_list 的 templates 里有没有合适的常见模板（space_upsert 带 template 一步建好），" +
				"没有就和用户商量好类型和字段，再用 space_upsert 和 space_schema_upsert 搭好。",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		list := make([]map[string]any, len(tools))
		for i, t := range tools {
			list[i] = t.schema()
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		out, err := m.call(p.Name, p.Arguments)
		if errors.Is(err, errNoTool) {
			return nil, &rpcError{-32602, err.Error()}
		}
		if err != nil {
			return map[string]any{"content": []map[string]any{{"type": "text", "text": err.Error()}}, "isError": true}, nil
		}
		text, _ := json.MarshalIndent(out, "", "  ")
		return map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}, "structuredContent": out}, nil
	}
	return nil, &rpcError{-32601, "method not found: " + method}
}

var errNoTool = errors.New("unknown tool")

// ---- The tools ----

func (m *mcpServer) call(name string, raw json.RawMessage) (out any, err error) {
	args := func(v any) error {
		if len(raw) == 0 || string(raw) == "null" {
			return nil
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			return fmt.Errorf("参数有误：%v", err)
		}
		return nil
	}
	read := func(f func(st *State) (any, error)) (any, error) {
		rerr := m.store.read(func(st *State) { out, err = f(st) })
		return out, errors.Join(rerr, err)
	}
	write := func(f func(st *State, now time.Time) (any, error)) (any, error) {
		uerr := m.store.update(func(st *State, now time.Time) (err error) { out, err = f(st, now); return err })
		return out, uerr
	}
	switch name {
	case "space_list":
		if err := args(&struct{}{}); err != nil {
			return nil, err
		}
		return read(func(st *State) (any, error) {
			list := []map[string]any{}
			for _, sp := range st.Spaces {
				types := []map[string]any{}
				for i, t := range sp.Types {
					types = append(types, map[string]any{"key": t.Key, "label": t.Label, "view": t.View,
						"entities": len(st.entities(sp.ID, &sp.Types[i]))})
				}
				rels := 0
				for _, r := range st.Relations {
					if e := st.entity(r.From); e != nil && e.Space == sp.ID {
						rels++
					}
				}
				list = append(list, map[string]any{"id": sp.ID, "name": sp.Name, "icon": sp.Icon,
					"description": sp.Description, "types": types, "relations": rels})
			}
			return map[string]any{"spaces": list, "templates": templateList()}, nil
		})
	case "space_schema_get":
		var in struct {
			Space string `json:"space_id"`
		}
		if err := args(&in); err != nil {
			return nil, err
		}
		return read(func(st *State) (any, error) {
			sp, err := st.space(in.Space)
			if err != nil {
				return nil, err
			}
			return map[string]any{"space": sp, "field_kinds": fieldKinds, "views": viewKinds, "templates": templateList()}, nil
		})
	case "space_query":
		var q Query
		if err := args(&q); err != nil {
			return nil, err
		}
		return read(func(st *State) (any, error) {
			ents, total, err := st.query(q)
			if err != nil {
				return nil, err
			}
			list := make([]entityOut, len(ents))
			for i, e := range ents {
				list[i] = outOf(st, e)
			}
			return map[string]any{"total": total, "entities": list}, nil
		})
	case "space_upsert":
		var in SpaceInput
		if err := args(&in); err != nil {
			return nil, err
		}
		return write(func(st *State, now time.Time) (any, error) {
			sp, err := st.upsertSpace(in, now)
			return map[string]any{"space": sp}, err
		})
	case "space_schema_upsert":
		var in struct {
			Space string `json:"space_id"`
			TypeDef
		}
		if err := args(&in); err != nil {
			return nil, err
		}
		return write(func(st *State, _ time.Time) (any, error) {
			t, err := st.upsertType(in.Space, in.TypeDef)
			return map[string]any{"type": t}, err
		})
	case "space_entity_upsert":
		var in EntityInput
		if err := args(&in); err != nil {
			return nil, err
		}
		return write(func(st *State, now time.Time) (any, error) {
			e, err := st.upsertEntity(in, now)
			if err != nil {
				return nil, err
			}
			return map[string]any{"entity": outOf(st, e)}, nil
		})
	case "space_entity_derive":
		var in struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Title    string `json:"title"`
			Relation string `json:"relation"`
		}
		if err := args(&in); err != nil {
			return nil, err
		}
		return write(func(st *State, now time.Time) (any, error) {
			e, err := st.deriveEntity(in.ID, in.Type, in.Title, in.Relation, now)
			if err != nil {
				return nil, err
			}
			return map[string]any{"entity": outOf(st, e)}, nil
		})
	case "space_entity_delete":
		var in struct {
			ID string `json:"id"`
		}
		if err := args(&in); err != nil {
			return nil, err
		}
		return write(func(st *State, _ time.Time) (any, error) {
			n, err := st.deleteEntity(in.ID)
			return map[string]any{"deleted": in.ID, "relations_removed": n}, err
		})
	}
	return nil, fmt.Errorf("%w %q", errNoTool, name)
}

// templateList names the templates a new space can start from and the types each brings.
func templateList() []map[string]any {
	out := make([]map[string]any, len(templates))
	for i, t := range templates {
		types := make([]map[string]any, len(t.Types))
		for j, d := range t.Types {
			types[j] = map[string]any{"key": d.Key, "label": d.Label, "view": d.View}
		}
		out[i] = map[string]any{"key": t.Key, "name": t.Name, "icon": t.Icon, "description": t.Description, "types": types}
	}
	return out
}

// entityOut is an entity with its edges, each naming the other end.
type entityOut struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Title   string         `json:"title"`
	Status  string         `json:"status,omitempty"`
	Values  map[string]any `json:"values"`
	Created time.Time      `json:"created"`
	Updated time.Time      `json:"updated"`
	Out     []edge         `json:"out,omitempty"`
	In      []edge         `json:"in,omitempty"`
}

type edge struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Title string `json:"title"`
}

func outOf(st *State, e *Entity) entityOut {
	o := entityOut{ID: e.ID, Type: e.Type, Title: e.Title, Status: e.Status, Values: e.Values, Created: e.Created, Updated: e.Updated}
	if o.Values == nil {
		o.Values = map[string]any{}
	}
	out, in := st.links(e.ID)
	for _, r := range out {
		if other := st.entity(r.To); other != nil {
			o.Out = append(o.Out, edge{r.Type, r.To, other.Title})
		}
	}
	for _, r := range in {
		if other := st.entity(r.From); other != nil {
			o.In = append(o.In, edge{r.Type, r.From, other.Title})
		}
	}
	return o
}
