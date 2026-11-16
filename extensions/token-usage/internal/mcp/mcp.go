// Package mcp serves the token-usage tools over the Model Context Protocol: line-delimited JSON-RPC 2.0 on a reader
// and a writer. The reads go through the store and never change it; the two budget tools are the only writes, and they
// touch only the budgets file.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/budget"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/store"
)

const (
	protocolVersion = "2025-06-18"
	serverName      = "token-usage"
	serverVersion   = "0.1.0"

	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

// Tool is one tool of the server. Read marks a tool that never changes anything.
type Tool struct {
	Name, About string
	Read        bool
	Schema      map[string]any
}

// Tools returns the tools in the order the server lists them.
func Tools() []Tool {
	return []Tool{
		{Name: "usage_summary", Read: true, About: "汇总一段时间内的 token 用量：总量、与上一段相比的变化、按天的趋势，以及按模型、机器、启动器的分布。",
			Schema: object(map[string]any{
				"range":    enumProp("时间范围", []string{"today", "7d", "30d"}),
				"group_by": enumProp("可选，只返回其中一个维度的分布", []string{"model", "machine", "launcher", "day"}),
			}, "range")},
		{Name: "usage_top", Read: true, About: "某段时间内用量最高的几项。",
			Schema: object(map[string]any{
				"range": enumProp("时间范围", []string{"today", "7d", "30d"}),
				"dim":   enumProp("按什么排名", []string{"model", "machine", "launcher"}),
				"n":     map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "返回几项，默认 8"},
			}, "range", "dim")},
		{Name: "budget_status", Read: true, About: "每个预算目前用了多少、占上限的比例。",
			Schema: object(map[string]any{})},
		{Name: "budget_set", About: "设置一个预算：某个模型或全部模型的每日或每周上限。已有同一范围和周期的预算会被替换。",
			Schema: object(map[string]any{
				"model": map[string]any{"type": "string", "description": "模型名；省略表示全部模型"},
				"span":  enumProp("周期", []string{"day", "week"}),
				"limit": map[string]any{"type": "integer", "minimum": 1, "description": "上限，单位是 token"},
			}, "span", "limit")},
		{Name: "budget_delete", About: "删除一个预算。",
			Schema: object(map[string]any{
				"model": map[string]any{"type": "string", "description": "模型名；省略表示全部模型的那个"},
				"span":  enumProp("周期", []string{"day", "week"}),
			}, "span")},
	}
}

func object(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func enumProp(desc string, values []string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

// Serve reads requests from in and writes one answer per request to out, until in ends or ctx is done.
func Serve(ctx context.Context, in io.Reader, out io.Writer, st *store.Store, bf *budget.File, loc *time.Location, now func() time.Time) error {
	r := bufio.NewReader(in)
	s := &server{st: st, bf: bf, loc: loc, now: now}
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

type server struct {
	st  *store.Store
	bf  *budget.File
	loc *time.Location
	now func() time.Time
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  *string         `json:"method"`
	Params  json.RawMessage `json:"params"`
}

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

func (s *server) handle(line []byte) []byte {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return reply(nil, nil, &rpcError{Code: codeParse, Message: "Parse error"})
	}
	if req.JSONRPC != "2.0" || req.Method == nil || !validID(req.ID) {
		return reply(nil, nil, &rpcError{Code: codeInvalidRequest, Message: "Invalid Request"})
	}
	if len(req.ID) == 0 {
		return nil
	}
	result, rerr := s.dispatch(*req.Method, req.Params)
	return reply(req.ID, result, rerr)
}

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

func reply(id json.RawMessage, result any, err *rpcError) []byte {
	if id == nil {
		id = json.RawMessage("null")
	}
	r := response{JSONRPC: "2.0", ID: id, Error: err}
	if err == nil {
		b, merr := json.Marshal(result)
		if merr != nil {
			r.Error = &rpcError{Code: codeInternal, Message: merr.Error()}
		} else {
			r.Result = b
		}
	}
	out, _ := json.Marshal(r)
	return out
}

func (s *server) dispatch(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": serverName, "version": serverVersion},
		}, nil
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return s.list(), nil
	case "tools/call":
		return s.call(params)
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: "Method not found"}
	}
}

func (s *server) list() any {
	type listed struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"inputSchema"`
	}
	out := make([]listed, 0, 5)
	for _, t := range Tools() {
		out = append(out, listed{Name: t.Name, Description: t.About, InputSchema: t.Schema})
	}
	return map[string]any{"tools": out}
}

func (s *server) call(params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "参数不是合法的 JSON"}
	}
	res, err := s.run(p.Name, p.Arguments)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return toolResult(res), nil
}

// run does one tool's work.
func (s *server) run(name string, args json.RawMessage) (any, error) {
	switch name {
	case "usage_summary":
		return s.summary(args)
	case "usage_top":
		return s.top(args)
	case "budget_status":
		return s.status()
	case "budget_set":
		return s.set(args)
	case "budget_delete":
		return s.del(args)
	default:
		return nil, fmt.Errorf("没有叫 %q 的工具", name)
	}
}

func toolResult(v any) any {
	b, _ := json.Marshal(v)
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(b)}},
		"structuredContent": v,
	}
}

func toolError(msg string) any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": msg}},
		"isError": true,
	}
}
