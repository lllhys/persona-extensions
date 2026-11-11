// Package mcp serves the household tools over the Model Context Protocol: line-delimited JSON-RPC 2.0 on a reader and a
// writer. The records are reached through kernel.Storage only: a read runs in View, a write in Update, so a failed write
// saves nothing. tools.go holds the tools and what they answer.
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

	"github.com/nwverse/persona-extensions/pluginkit/spaces/kernel"
)

// The protocol revision this server speaks, and how it identifies itself.
const (
	protocolVersion = "2025-06-18"
	serverName      = "household"
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

// Serve reads requests from in, one JSON object per line, and writes one answer per request to out, until in ends or
// ctx is done. A message without an id is a notification and is never answered. Each tool call runs on st. loc is the
// hub's time zone: "today" is the day in it.
func Serve(ctx context.Context, in io.Reader, out io.Writer, st kernel.Storage, loc *time.Location) error {
	r := bufio.NewReader(in)
	s := &server{st: st, loc: loc}
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
	loc *time.Location
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
// error result carrying its sentence.
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

// run executes a tool on the store and returns its answer as JSON. The answer is marshalled inside the store call, so
// it never refers to the base after the call has returned. A read gets the current time; a write gets the time the
// store gives it.
func (s *server) run(t Tool, raw json.RawMessage) ([]byte, error) {
	var out []byte
	do := func(e env) error {
		v, err := t.Run(e, raw)
		if err != nil {
			return err
		}
		out, err = json.Marshal(v)
		return err
	}
	var err error
	if t.Read {
		err = s.st.View(func(b *kernel.Base) error { return do(env{b: b, now: time.Now(), loc: s.loc}) })
	} else {
		err = s.st.Update(func(b *kernel.Base, now time.Time) error { return do(env{b: b, now: now, loc: s.loc}) })
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
