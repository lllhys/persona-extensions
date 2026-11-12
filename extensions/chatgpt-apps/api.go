package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// maxAnswer is the most of one answer read from ChatGPT: it bounds the memory one call takes. It is not a cut: an
// app's result goes on whole (a cut list is broken JSON a subscription cannot read), and a longer answer is an error.
const maxAnswer = 32 << 20

// backend calls chatgpt.com's web backend as the signed-in user and decodes the JSON answer into out (nil: not
// decoded). A refused access token is refreshed once. Logs say the method, the path without its query, the status
// and the time; never a token, a query or a body.
func (a *App) backend(ctx context.Context, method, path string, body any, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	raw, ctype, err := a.send(ctx, method, a.ep.API+path, payload)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if strings.Contains(ctype, "text/event-stream") {
		msg, err := lastEvent(raw)
		if err != nil {
			return err
		}
		raw = msg
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("ChatGPT answered %s %s with something that does not decode; its API may have changed", method, pathOnly(path))
	}
	return nil
}

func (a *App) send(ctx context.Context, method, target string, payload []byte) ([]byte, string, error) {
	tok, err := a.access(ctx)
	if err != nil {
		return nil, "", err
	}
	for attempt := 1; ; attempt++ {
		start := a.now()
		req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		if id := tok.accountID(); id != "" {
			req.Header.Set("chatgpt-account-id", id)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("originator", originator)
		req.Header.Set("User-Agent", userAgent)
		resp, err := a.http.Do(req)
		if err != nil {
			log.Printf("%s %s: %v", method, pathOnly(target), errors.Unwrap(err))
			return nil, "", fmt.Errorf("could not reach ChatGPT (%s %s); try again later", method, pathOnly(target))
		}
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, a.answerMost+1))
		resp.Body.Close()
		log.Printf("%s %s %d %s", method, pathOnly(target), resp.StatusCode, a.now().Sub(start).Round(time.Millisecond))
		if resp.StatusCode == http.StatusUnauthorized && attempt == 1 {
			if tok, err = a.refreshAfter401(ctx, tok.AccessToken); err != nil {
				return nil, "", err
			}
			continue
		}
		if resp.StatusCode/100 != 2 {
			return nil, "", &httpError{Status: resp.StatusCode, Body: short(string(raw), 300)}
		}
		if rerr != nil {
			return nil, "", fmt.Errorf("reading ChatGPT's answer: %w", rerr)
		}
		if int64(len(raw)) > a.answerMost {
			return nil, "", fmt.Errorf("ChatGPT's answer to %s %s is over %s; ask for less at a time (fewer items, a narrower filter or time range)",
				method, pathOnly(target), sizeOf(a.answerMost))
		}
		return raw, resp.Header.Get("Content-Type"), nil
	}
}

// sizeOf says a byte count for a message: whole MB, or KB below that.
func sizeOf(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%d MB", n>>20)
	}
	return fmt.Sprintf("%d KB", n>>10)
}

// pathOnly is a URL or path without its query and host, for logs and messages.
func pathOnly(target string) string {
	if i := strings.Index(target, "?"); i >= 0 {
		target = target[:i]
	}
	if i := strings.Index(target, "/backend-api"); i >= 0 {
		target = target[i+len("/backend-api"):]
	}
	return target
}

// lastEvent is the JSON-RPC answer in an event stream: the last data line that carries an id.
func lastEvent(raw []byte) ([]byte, error) {
	var last []byte
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[len("data:"):])
		var probe struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal([]byte(data), &probe) == nil && len(probe.ID) > 0 {
			last = []byte(data)
		}
	}
	if last == nil {
		return nil, errors.New("ChatGPT's MCP answered an empty event stream")
	}
	return last, nil
}

// rpcError is an MCP error answer.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("ChatGPT MCP error %d: %s", e.Code, e.Message) }

// rpc is one JSON-RPC call to the MCP endpoint that serves every app connected to the account.
func (a *App) rpc(ctx context.Context, method string, params any, out any) error {
	var msg struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
	if err := a.backend(ctx, http.MethodPost, "/ps/mcp", body, &msg); err != nil {
		return err
	}
	if msg.Error != nil {
		return msg.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(msg.Result, out)
}
