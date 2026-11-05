package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/proto"
)

// Decisions of the write gate.
const (
	Allow   = "allow"   // run it
	Confirm = "confirm" // the user allows it on the phone first (the default)
	Deny    = "deny"    // refuse, and tell the model why
)

// Rule is one rule of the write gate (hub.json connector_gate): an action of a connector — both path.Match
// patterns, "*" for any — whose arguments' top-level fields equal Match when it is set (a string value is a pattern
// too) gets Decision. The first matching rule decides; when none matches, the user confirms.
type Rule struct {
	Connector string         `json:"connector"`
	Action    string         `json:"action"`
	Match     map[string]any `json:"match,omitempty"`
	Decision  string         `json:"decision"`
}

// Lark is the connector id whose rules are the assistant's Feishu writes (larkintegration's ol, lark-cli). Those run on
// the assistant's machine, where node/larkgate.go reads the command: they are not the Registry's actions, so a rule
// of connector "lark" goes there instead (LarkRules) and its action is "<METHOD> <path>" in lark_gate's terms —
// "POST /conversations/*/messages", "CLI /im/+messages-send", "*" for any call.
const Lark = "lark"

// ValidateRules reports a rule the gate could not use, so a typo in hub.json stops the start instead of quietly
// asking (or worse, quietly allowing) something else.
func ValidateRules(rules []Rule) error {
	for i, r := range rules {
		switch r.Decision {
		case Allow, Confirm, Deny:
		default:
			return fmt.Errorf("connector_gate[%d]: decision %q is not allow, confirm or deny", i, r.Decision)
		}
		for _, p := range []string{r.Connector, r.Action} {
			if _, err := path.Match(p, ""); err != nil || p == "" {
				return fmt.Errorf("connector_gate[%d]: bad pattern %q", i, p)
			}
		}
		if r.Connector == Lark {
			if _, ok := larkRule(r); !ok {
				return fmt.Errorf("connector_gate[%d]: a lark action is \"<METHOD> <path>\" or \"*\", not %q", i, r.Action)
			}
		}
	}
	return nil
}

// LarkRules are the rules the assistant's Feishu gate gets (proto.AssistantLark.Rules): connector_gate's rules of
// connector "lark" first, then hub.json's older lark_gate ones, which keep working as they are; the node puts its
// built-in rules after them.
func LarkRules(rules []Rule, larkGate []proto.LarkRule) []proto.LarkRule {
	var out []proto.LarkRule
	for _, r := range rules {
		if r.Connector != Lark {
			continue
		}
		if lr, ok := larkRule(r); ok {
			out = append(out, lr)
		}
	}
	return append(out, larkGate...)
}

// GateRules are connector_gate's rules of connector id written as HTTP calls ("<METHOD> <path>" or "*"), for a
// plugin's command gate whose calls go to that connector's service (the host's assistant command-gate contract): the user's say first.
func GateRules(rules []Rule, id string) []proto.LarkRule {
	var out []proto.LarkRule
	for _, r := range rules {
		if r.Connector != id {
			continue
		}
		if lr, ok := larkRule(r); ok {
			out = append(out, lr)
		}
	}
	return out
}

// larkRule is r in lark_gate's terms: the action's method and path, Match as the body's fields.
func larkRule(r Rule) (proto.LarkRule, bool) {
	lr := proto.LarkRule{Method: "*", Path: "**", Body: r.Match, Action: r.Decision}
	if r.Action == "*" {
		return lr, true
	}
	method, p, ok := strings.Cut(strings.TrimSpace(r.Action), " ")
	p = strings.TrimSpace(p)
	if !ok || method == "" || !strings.HasPrefix(p, "/") {
		return lr, false
	}
	lr.Method, lr.Path = strings.ToUpper(method), p
	return lr, true
}

// Decide is what the gate says about the action name of connector id (whose manifest is m) with args: the first
// matching rule's decision, Confirm when none matches. An action the manifest lists in ConfirmAlways is never
// Allow: a rule that allows it (a "*" one, say) still has the user confirm on the phone. Deny stays Deny.
func Decide(rules []Rule, m Manifest, id, name string, args json.RawMessage) string {
	var fields map[string]any
	_ = json.Unmarshal(args, &fields)
	for _, r := range rules {
		if matches(r.Connector, id) && matches(r.Action, name) && matchFields(r.Match, fields) {
			if r.Decision == Allow && m.AlwaysConfirms(name) {
				return Confirm
			}
			return r.Decision
		}
	}
	return Confirm
}

// cardMost is the most of Describe's words the card shows (a program's could be any length): more than a Telegram
// message or a long comment, and the card says when it was cut.
const cardMost = 16000

// card is Describe's words as the card shows them: at most cardMost runes, saying how much was left out.
func card(summary string) string {
	r := []rune(summary)
	if len(r) <= cardMost {
		return summary
	}
	return string(r[:cardMost]) + fmt.Sprintf("\n\n（后面还有 %d 字，卡片只显示前 %d 字）", len(r)-cardMost, cardMost)
}

// gate lets the action name of c through or answers instead of it: Deny refuses, Confirm asks the user with the
// card's words (Describe, and who will see it) and refuses unless they allow it, Allow says nothing. A nil result
// means run it, with the very args the card was made from.
func gate(ctx context.Context, rules []Rule, c Connector, a Actions, name string, args json.RawMessage, ask Ask) *proto.ToolResult {
	switch Decide(rules, c.Manifest(), c.ID(), name, args) {
	case Deny:
		res := proto.TextResult("The hub's rules (connector_gate) do not allow "+name+".", true)
		return &res
	case Confirm:
		act, summary, err := a.Describe(ctx, name, args)
		if err != nil {
			res := proto.TextResult(err.Error(), true)
			return &res
		}
		summary = card(summary)
		if act.Visible != "" {
			summary += "\n\n谁会看到：" + act.Visible
		}
		if ask == nil || !ask(ctx, summary, args) {
			res := proto.TextResult("The user declined "+name+".", true)
			return &res
		}
	}
	return nil
}

func matches(pattern, s string) bool {
	ok, err := path.Match(pattern, s)
	return err == nil && ok
}

func matchFields(want, got map[string]any) bool {
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			return false
		}
		if ws, isStr := w.(string); isStr {
			if gs, ok := g.(string); !ok || !matches(ws, gs) {
				return false
			}
		} else if !reflect.DeepEqual(w, g) {
			return false
		}
	}
	return true
}
