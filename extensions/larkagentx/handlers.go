package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/pluginkit"
	"github.com/nwverse/persona/sdk/go/ext"
)

type sendArgs struct {
	Chat   string `json:"chat"`
	Text   string `json:"text"`
	Anchor string `json:"anchor,omitempty"`
}

func (c *connector) validateSend(a sendArgs) error {
	if !identifier.MatchString(a.Chat) || !slices.Contains(c.chats, a.Chat) || (a.Anchor != "" && !identifier.MatchString(a.Anchor)) || strings.TrimSpace(a.Text) == "" || !utf8.ValidString(a.Text) || utf8.RuneCountInString(a.Text) > 4096 {
		return failure("invalid_params")
	}
	return nil
}

var actionTool = map[string]any{
	"name":        sendAction,
	"description": "Send one Feishu text message to an explicitly authorized conversation. Persona must confirm the complete text and destination every time. Never retry an unknown outcome with a new request.",
	"input_schema": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"chat", "text"}, "properties": map[string]any{
		"chat":   map[string]any{"type": "string", "description": "An exact conversation ID in the configured allowlist."},
		"text":   map[string]any{"type": "string", "maxLength": 4096, "description": "The complete text the user will confirm."},
		"anchor": map[string]any{"type": "string", "description": "Optional topic anchor in that conversation."},
	}},
}

func (c *connector) handle(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var p struct {
		Contribution string            `json:"contribution"`
		Step         string            `json:"step"`
		Values       map[string]string `json:"values"`
		Name         string            `json:"name"`
		Args         json.RawMessage   `json:"args"`
		RequestID    string            `json:"request_id"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return nil, failure("invalid_params")
	}
	if p.Contribution != id {
		return nil, ext.NoContribution("unavailable")
	}
	switch method {
	case "manifest":
		var m struct {
			Contributes struct {
				Connectors []map[string]any `json:"connectors"`
			} `json:"contributes"`
		}
		_ = json.Unmarshal(manifest, &m)
		result := m.Contributes.Connectors[0]
		result["protocol"] = 2
		return result, nil
	case "status":
		c.mu.Lock()
		state := c.state
		c.mu.Unlock()
		out := map[string]any{"state": state, "via": "User-installed LarkAgentX companion (SSH stdio)"}
		if state != "ok" {
			out["guide"] = []string{"Configure both exact allowlists and the SSH companion command.", "Install the pinned upstream yourself on the companion, then sign in or import a session in Persona."}
		}
		return out, nil
	case "inbox.collect":
		c.mu.Lock()
		things := slices.Clone(c.data.Inbox)
		c.mu.Unlock()
		if things == nil {
			things = []thing{}
		}
		things = slices.DeleteFunc(things, func(t thing) bool {
			chat, _, _ := strings.Cut(t.Thread, ":")
			return !slices.Contains(c.chats, chat) || !slices.Contains(c.senders, t.Sender)
		})
		pluginkit.StampRules(things, c.rules)
		return map[string]any{"things": things}, nil
	case "actions.list":
		return map[string]any{"tools": []any{actionTool}}, nil
	case "actions.describe", "actions.do":
		if p.Name != sendAction {
			return nil, ext.UnknownMethod("connector.action")
		}
		var a sendArgs
		if json.Unmarshal(p.Args, &a) != nil {
			return nil, failure("invalid_params")
		}
		if err := c.validateSend(a); err != nil {
			return nil, err
		}
		if method == "actions.describe" {
			where := a.Chat
			if a.Anchor != "" {
				where += " · topic " + a.Anchor
			}
			return map[string]any{"action": map[string]string{"name": sendAction, "visible": "Conversation participants"}, "summary": "Send to Feishu conversation " + where + " (visible to its participants):\n" + a.Text}, nil
		}
		if p.RequestID == "" || len(p.RequestID) > 256 {
			return nil, failure("invalid_params")
		}
		c.op.Lock()
		defer c.op.Unlock()
		return c.send(ctx, p.RequestID, a)
	case "login.step":
		c.op.Lock()
		defer c.op.Unlock()
		if len(c.argv) == 0 || len(c.chats) == 0 || len(c.senders) == 0 {
			return nil, failure("not_configured")
		}
		return c.login(ctx, p.Step, p.Values)
	case "logout":
		c.op.Lock()
		defer c.op.Unlock()
		c.detach()
		c.mu.Lock()
		c.data = stored{}
		c.state = "not_signed_in"
		c.mu.Unlock()
		c.changed()
		if err := c.secret(ctx, "delete", nil, nil); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	default:
		return nil, ext.UnknownMethod("connector.unsupported")
	}
}

func (c *connector) login(ctx context.Context, step string, values map[string]string) (any, error) {
	if step == "" && c.loginMethod == "import" {
		step = "import"
	}
	if step == "import" {
		if values["credentials"] == "" {
			return map[string]any{"step": "import", "hint": "Paste the upstream credentials JSON privately. It is stored only in Persona's encrypted connector secret.", "fields": []any{map[string]any{"name": "credentials", "label": "Credentials JSON", "secret": true}}}, nil
		}
		if err := c.adopt(ctx, json.RawMessage(values["credentials"])); err != nil {
			return nil, err
		}
		return map[string]any{"done": true}, nil
	}
	if step != "" && step != "qr" {
		return nil, failure("invalid_params")
	}
	c.mu.Lock()
	b := c.b
	c.mu.Unlock()
	method := "login.poll"
	if step == "" || b == nil {
		c.detach()
		var err error
		b, err = c.create()
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.state = "not_signed_in"
		c.mu.Unlock()
		method = "login.begin"
	}
	var result struct {
		State       string          `json:"state"`
		QR          string          `json:"qr"`
		Credentials json.RawMessage `json:"credentials"`
	}
	params := map[string]any{"region": "feishu", "allowed_chats": c.chats, "allowed_senders": c.senders}
	if err := b.Call(ctx, method, params, &result); err != nil {
		c.detach()
		return nil, err
	}
	if result.State == "ready" {
		if err := c.adopt(ctx, result.Credentials); err != nil {
			return nil, err
		}
		return map[string]any{"done": true}, nil
	}
	if result.State == "expired" {
		c.detach()
		return map[string]any{"step": "import", "error": "The QR login expired. Start sign-in again for a new QR code, or import a session.", "fields": []any{map[string]any{"name": "credentials", "label": "Credentials JSON", "secret": true}}}, nil
	}
	if result.State != "pending" {
		c.detach()
		return nil, failure("unavailable")
	}
	if result.QR != "" {
		if len(result.QR) > 4096 {
			c.detach()
			return nil, failure("unavailable")
		}
		c.mu.Lock()
		c.qr = result.QR
		c.mu.Unlock()
	}
	c.mu.Lock()
	qr := c.qr
	c.mu.Unlock()
	if qr == "" {
		return nil, failure("unavailable")
	}
	return map[string]any{"step": "qr", "kind": "qr", "url": qr, "interval": 2, "hint": "Scan with the explicitly authorized Feishu test account. QR state stays in memory; no agent or read receipt is enabled."}, nil
}

func (c *connector) send(ctx context.Context, requestID string, a sendArgs) (any, error) {
	key := digest(requestID)
	c.mu.Lock()
	payload, _ := json.Marshal(a)
	fingerprint := digest(string(c.data.Session) + "\x00" + string(payload))
	if r, ok := c.data.Writes[key]; ok {
		c.mu.Unlock()
		if r.Digest != fingerprint {
			return nil, failure("invalid_params")
		}
		if r.Result == "" {
			return nil, failure("unknown_result")
		}
		return map[string]string{"text": "Sent Feishu message " + r.Result}, nil
	}
	b := c.b
	if c.state != "ok" || b == nil {
		c.mu.Unlock()
		return nil, failure("auth_expired")
	}
	if len(c.data.Writes) >= 1024 {
		c.mu.Unlock()
		return nil, failure("journal_full")
	}
	if c.data.Writes == nil {
		c.data.Writes = map[string]receipt{}
	}
	c.data.Writes[key] = receipt{Digest: fingerprint}
	c.mu.Unlock()
	if err := c.save(ctx); err != nil {
		c.setError(err)
		c.detach()
		return nil, err
	}
	var result struct {
		MessageID string `json:"message_id"`
	}
	params := map[string]any{"chat": a.Chat, "text": a.Text, "request_id": requestID}
	if a.Anchor != "" {
		params["anchor"] = a.Anchor
	}
	if err := b.Call(ctx, "session.send", params, &result); err != nil {
		c.setError(err)
		c.detach()
		return nil, failure("unknown_result")
	}
	if !identifier.MatchString(result.MessageID) {
		return nil, failure("unknown_result")
	}
	c.mu.Lock()
	c.data.Writes[key] = receipt{Digest: fingerprint, Result: result.MessageID}
	c.mu.Unlock()
	if err := c.save(ctx); err != nil {
		c.mu.Lock()
		c.data.Writes[key] = receipt{Digest: fingerprint}
		c.mu.Unlock()
		c.setError(err)
		c.detach()
		return nil, failure("unknown_result")
	}
	return map[string]string{"text": "Sent Feishu message " + result.MessageID}, nil
}
