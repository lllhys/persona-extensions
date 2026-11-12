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

// `search-as-code mcp` speaks the Model Context Protocol over stdio (JSON-RPC 2.0, one JSON message per line).
// Read-only tools (`sac_execute`, `sac_session_search`, `sac_recipes_list`) are allowed by the manifest;
// write tools (`sac_recipe_upsert`, `sac_recipe_delete`, `sac_session_restore`) follow the conversation's
// permission policy.

const mcpProtocol = "2025-06-18"

type mcpServer struct{ store *store }

type tool struct {
	name, description string
	readOnly          bool
	props             map[string]any
	required          []string
}

func str(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

var tools = []tool{
	{
		name:     "sac_execute",
		readOnly: true,
		props: map[string]any{
			"query":   str("自然语言复杂查询，或直接传入 |> 串联的 Search-as-Code 代码"),
			"code":    str("显式的 Search-as-Code 流水线代码，例如 scan_sessions(query=\"quota\", scope=\"conversation\") |> exclude_tools() |> correlate(by=\"file\") |> group_by(field=\"project\") |> rank(by=\"relevance_freshness\") |> limit(n=20)"),
			"recipe":  str("要执行的搜索配方 ID（见 sac_recipes_list，如 error-triage、cross-session-context、model-harness-audit）"),
			"scope":   map[string]any{"type": "string", "enum": scopes, "description": "检索范围：conversation 纯对话不含工具噪音（默认）、all 全部、user 仅用户提问、assistant 仅助手回复、errors 报错与异常"},
			"project": str("按项目名称或工作目录过滤"),
			"harness": str("按 Agent Harness 过滤（如 claude-code、codex）"),
			"model":   str("按模型过滤（如 claude-sonnet-5-5、claude-opus-5-5、gpt-5-codex）"),
			"since":   str("时间窗口（如 7d、24h 或 YYYY-MM-DD）"),
			"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "返回最大条数，默认 25"},
		},
		description: "执行一段 Search-as-Code 搜索流水线、预置配方或自然语言复杂查询。返回编译生成的 Search-as-Code 代码（code_listing）、每一步算子的耗时与命中轨迹（step_traces）、聚合分组（groups）以及结构化证据卡片（hits）。",
	},
	{
		name:     "sac_session_search",
		readOnly: true,
		props: map[string]any{
			"query":         str("检索关键词或短语"),
			"scope":         map[string]any{"type": "string", "enum": scopes, "description": "CASS 风格范围过滤：conversation 纯对话（自动排除工具调用噪音）、all 全部、user 仅用户、assistant 仅助手、errors 仅报错与异常"},
			"project":       str("只看指定项目"),
			"harness":       str("只看指定 Harness（claude-code / codex）"),
			"model":         str("只看指定模型"),
			"since":         str("起始时间（如 7d、48h 或 YYYY-MM-DD）"),
			"exclude_tools": map[string]any{"type": "boolean", "description": "是否剥离 tool_use / tool_result 噪音（默认在 conversation 范围下为 true）"},
			"limit":         map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "默认 25"},
		},
		description: "CASS 风格跨本机 Coding Agent 历史会话深度检索：支持按角色范围（All / Conversation without tools / User / Assistant / Errors）、项目、Harness、模型与时间窗检索，并自动关联同一文件或项目的上下文会话。",
	},
	{
		name:        "sac_recipes_list",
		readOnly:    true,
		props:       map[string]any{},
		description: "列出所有预置与自定义的 Search-as-Code 搜索配方（包含 ID、名称、说明、默认范围与完整流水线代码），以及当前本机已扫描的 Agent 会话源统计。",
	},
	{
		name:     "sac_recipe_upsert",
		required: []string{"name", "code"},
		props: map[string]any{
			"id":          str("配方 ID（小写字母开头，如 quota-triage）；新建时不填会自动按名称生成"),
			"name":        str("配方名称（必填）"),
			"description": str("配方用途说明"),
			"icon":        str("图标（如 code、search、warning、branch、chart、doc、folder、star）"),
			"scope":       map[string]any{"type": "string", "enum": scopes, "description": "默认检索范围"},
			"code":        str("Search-as-Code 流水线代码（必填，支持 scan_sessions、scan_files、where、exclude_tools、match_regex、correlate、dedup、group_by、rank、limit）"),
		},
		description: "新建或更新一个可复用的 Search-as-Code 搜索配方。代码会在保存前经过语法与算子校验。",
	},
	{
		name:     "sac_recipe_delete",
		required: []string{"id"},
		props: map[string]any{
			"id": str("要删除的自定义配方 ID"),
		},
		description: "删除一个自定义的 Search-as-Code 搜索配方。删除前先向用户确认配方名称。",
	},
	{
		name:     "sac_session_restore",
		required: []string{"entry_id"},
		props: map[string]any{
			"entry_id": str("已上云会话的 entry_id（来自检索结果中 location 为「已上云」的命中）"),
		},
		description: "把一个已被 disklessly 移到云端的会话（location 为「已上云」）从云端恢复到本机原路径（调用本机 disklessly 命令行，最长约 5 分钟）。返回恢复后的路径、会话 ID 与继续会话的命令（如 claude --resume <id>）。恢复前先向用户确认原路径；location 为「未确认上云」的会话上传未完成，云端可能没有副本。同一会话已在恢复时返回「正在恢复中」。",
	},
}

func (t tool) schema() map[string]any {
	s := map[string]any{"type": "object", "properties": t.props, "additionalProperties": false}
	if len(t.required) > 0 {
		s["required"] = t.required
	}
	return map[string]any{
		"name":        t.name,
		"description": t.description,
		"inputSchema": s,
		"annotations": map[string]any{
			"readOnlyHint":    t.readOnly,
			"destructiveHint": t.name == "sac_recipe_delete",
		},
	}
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (m *mcpServer) serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() && ctx.Err() == nil {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": rpcError{Code: -32700, Message: "parse error"}})
			continue
		}
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}
		res, rerr := m.handle(req.Method, req.Params)
		reply := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rerr != nil {
			reply["error"] = rerr
		} else {
			reply["result"] = res
		}
		if err := enc.Encode(reply); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (m *mcpServer) handle(method string, raw json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": mcpProtocol,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": about.ID, "version": about.Version},
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
		var call struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params"}
		}
		if !hasTool(call.Name) {
			return nil, &rpcError{Code: -32602, Message: fmt.Sprintf("unknown tool %q", call.Name)}
		}
		out, err := m.call(call.Name, call.Arguments)
		if err != nil {
			return map[string]any{
				"isError": true,
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
			}, nil
		}
		text, _ := json.MarshalIndent(out, "", "  ")
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(text)}},
		}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", method)}
	}
}

func hasTool(name string) bool {
	for _, t := range tools {
		if t.name == name {
			return true
		}
	}
	return false
}

func (m *mcpServer) call(name string, args map[string]any) (any, error) {
	strArg := func(k string) string { s, _ := args[k].(string); return strings.TrimSpace(s) }
	intArg := func(k string, def int) int {
		if n, ok := args[k].(float64); ok && n > 0 {
			return int(n)
		}
		return def
	}

	switch name {
	case "sac_recipes_list":
		var out any
		err := m.store.read(func(st *State) {
			_, stats := loadSessions(st)
			out = map[string]any{
				"recipes":      st.allRecipes(),
				"operators":    knownOps,
				"scopes":       scopes,
				"source_stats": stats,
			}
		})
		return out, err

	case "sac_execute":
		var out any
		err := m.store.update(func(st *State, now time.Time) error {
			var rec *Recipe
			if rid := strArg("recipe"); rid != "" {
				rec = st.recipe(rid)
				if rec == nil {
					return fmt.Errorf("找不到搜索配方 %q（可用 sac_recipes_list 查看）", rid)
				}
			}
			prog, err := CompileProgram(CompileInput{
				Query:   strArg("query"),
				Code:    strArg("code"),
				Recipe:  rec,
				Scope:   cmp.Or(strArg("scope"), st.DefaultScope),
				Project: strArg("project"),
				Harness: strArg("harness"),
				Model:   strArg("model"),
				Since:   strArg("since"),
				Limit:   intArg("limit", defaultLim),
			})
			if err != nil {
				return err
			}
			res := ExecuteProgram(st, prog, now)
			st.recordRun(res, now)
			out = res
			return nil
		})
		return out, err

	case "sac_session_search":
		var out any
		err := m.store.update(func(st *State, now time.Time) error {
			var excl *bool
			if b, ok := args["exclude_tools"].(bool); ok {
				excl = &b
			}
			prog, err := CompileProgram(CompileInput{
				Query:        strArg("query"),
				Scope:        cmp.Or(strArg("scope"), st.DefaultScope),
				Project:      strArg("project"),
				Harness:      strArg("harness"),
				Model:        strArg("model"),
				Since:        strArg("since"),
				ExcludeTools: excl,
				Limit:        intArg("limit", defaultLim),
			})
			if err != nil {
				return err
			}
			res := ExecuteProgram(st, prog, now)
			st.recordRun(res, now)
			out = res
			return nil
		})
		return out, err

	case "sac_recipe_upsert":
		var out any
		err := m.store.update(func(st *State, now time.Time) error {
			rec, err := st.upsertRecipe(RecipeInput{
				ID:          strArg("id"),
				Name:        strArg("name"),
				Icon:        strArg("icon"),
				Description: strArg("description"),
				Scope:       strArg("scope"),
				Code:        strArg("code"),
			}, now)
			if err != nil {
				return err
			}
			out = rec
			return nil
		})
		return out, err

	case "sac_recipe_delete":
		id := strArg("id")
		if id == "" {
			return nil, errors.New("缺少配方 id")
		}
		err := m.store.update(func(st *State, _ time.Time) error {
			return st.deleteRecipe(id)
		})
		return map[string]any{"deleted": id}, err

	case "sac_session_restore":
		var st *State
		if err := m.store.read(func(s *State) { st = s }); err != nil {
			return nil, err
		}
		return restoreArchived(context.Background(), st, strArg("entry_id"), m.store.dir)

	default:
		return nil, fmt.Errorf("unknown tool %q", name)
	}
}
