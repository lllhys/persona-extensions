package pluginkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Inbox events (the host's event-composition contract, nwverse/persona#1680): a connector that declares the capability
// "group" hands a conversation's new messages over as a batch (InboxThing's Group, a ThingGroup) instead of one thing
// per conversation, and the hub's model relates each message to an event of that conversation. The connector's side is
// GroupHandlers, each optional:
//
//   - Tool: the model's reads of the conversation (messages_before, messages_around, search).
//   - Fallback: grouping by structure when the model can't.
//   - Append: writing each event's meta after it is composed (the connector's own record of the event, stored by the
//     hub and handed back, never read by it). Only a connector that declares "meta" (CapMeta) is asked, and it must
//     say what its meta is in the manifest's meta_description. Without "meta" the hub writes the events itself, with
//     no meta.
//
// Declare "meta" and meta_description only when Append is registered: CheckGroupManifest holds a manifest to that.

// Connector capabilities of inbox events.
const (
	CapGroup = "group" // hands batches to relate
	CapMeta  = "meta"  // writes each event's meta (Append); needs meta_description
)

// MetaDescriptionMost is the longest meta_description, in characters (the hub's bound).
const MetaDescriptionMost = 4000

// The methods the hub calls, in the connector namespace.
const (
	MGroupTool     = "connector.group.tool"
	MGroupAppend   = "connector.group.append"
	MGroupFallback = "connector.group.fallback"
)

// GroupToolCall is one of the connector's tools called for the model, in a conversation (Thread): Name is
// messages_before, messages_around or search, Args as the model wrote them.
type GroupToolCall struct {
	Thread string          `json:"thread"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
}

// GroupAppend adds messages to one event: an existing one (Event, with the Meta the hub has) or a new one (Event "",
// Meta null), which may continue a done one. The answer is the event as an inbox thing, with its id and meta.
type GroupAppend struct {
	Thread    string          `json:"thread"`
	Event     string          `json:"event"`
	Meta      json.RawMessage `json:"meta"`
	Messages  []string        `json:"messages"`
	Continues string          `json:"continues"`
}

// GroupEvent is an event of the conversation as the hub hands it to Fallback (its meta as stored; null without).
type GroupEvent struct {
	ID   string          `json:"id"`
	Meta json.RawMessage `json:"meta"`
	Done bool            `json:"done"`
}

// GroupFallback asks for a batch to be grouped by structure, with the conversation's events.
type GroupFallback struct {
	Thread string       `json:"thread"`
	Batch  string       `json:"batch"`
	Events []GroupEvent `json:"events"`
}

// GroupFallbackResult puts each message of the batch in an open event (Assign) or a new one (New: a key of its own,
// a rule tier and a title), each exactly once.
type GroupFallbackResult struct {
	Assign []GroupAssign `json:"assign"`
	New    []GroupNew    `json:"new"`
}

type GroupAssign struct {
	Message string `json:"message"`
	Event   string `json:"event"`
}

type GroupNew struct {
	Key      string   `json:"key"`
	Messages []string `json:"messages"`
	RuleTier string   `json:"rule_tier"`
	Title    string   `json:"title"`
}

// GroupHandlers are a connector's answers to the hub's inbox-event calls; a nil one is not registered (the hub's call
// then gets unknown_method).
type GroupHandlers struct {
	Tool     func(ctx context.Context, call GroupToolCall) (any, error)
	Append   func(ctx context.Context, a GroupAppend) (any, error)
	Fallback func(ctx context.Context, f GroupFallback) (GroupFallbackResult, error)
}

// Register adds the handlers set to h for the connector contribution: a call naming another contribution answers
// no_contribution.
func (g GroupHandlers) Register(h map[string]Handler, contribution string) {
	add := func(method string, run func(ctx context.Context, params json.RawMessage) (any, error)) {
		h[method] = func(ctx context.Context, _ *Hub, params json.RawMessage) (any, error) {
			if c := Contribution(params); c != "" && c != contribution {
				return nil, Errorf(CodeNoContribution, "no connector %s here", c)
			}
			return run(ctx, params)
		}
	}
	if g.Tool != nil {
		add(MGroupTool, func(ctx context.Context, params json.RawMessage) (any, error) {
			var call GroupToolCall
			if err := json.Unmarshal(params, &call); err != nil {
				return nil, err
			}
			return g.Tool(ctx, call)
		})
	}
	if g.Append != nil {
		add(MGroupAppend, func(ctx context.Context, params json.RawMessage) (any, error) {
			var a GroupAppend
			if err := json.Unmarshal(params, &a); err != nil {
				return nil, err
			}
			return g.Append(ctx, a)
		})
	}
	if g.Fallback != nil {
		add(MGroupFallback, func(ctx context.Context, params json.RawMessage) (any, error) {
			var f GroupFallback
			if err := json.Unmarshal(params, &f); err != nil {
				return nil, err
			}
			return g.Fallback(ctx, f)
		})
	}
}

// Capabilities are the connector capabilities the handlers go with: group, and meta when Append is set (declare
// meta_description with it).
func (g GroupHandlers) Capabilities() []string {
	caps := []string{CapGroup}
	if g.Append != nil {
		caps = append(caps, CapMeta)
	}
	return caps
}

// CheckGroupManifest holds connector's declarations in a manifest to the handlers the plugin registers: group with an
// inbox, and with any handler; meta exactly when Append is registered, with group and a meta_description of at most
// MetaDescriptionMost characters; meta_description only with meta.
func CheckGroupManifest(manifest []byte, connector string, g GroupHandlers) error {
	type item struct {
		ID              string   `json:"id"`
		Capabilities    []string `json:"capabilities"`
		MetaDescription *string  `json:"meta_description"`
	}
	var m struct {
		Contributes struct {
			Connectors []item `json:"connectors"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	i := slices.IndexFunc(m.Contributes.Connectors, func(c item) bool { return c.ID == connector })
	if i < 0 {
		return fmt.Errorf("no connector %s in the manifest", connector)
	}
	c := m.Contributes.Connectors[i]
	has := func(cp string) bool { return slices.Contains(c.Capabilities, cp) }
	var errs []error
	handlers := g.Tool != nil || g.Append != nil || g.Fallback != nil
	if has(CapGroup) && !has("inbox") {
		errs = append(errs, fmt.Errorf("connector %s: %s without inbox", connector, CapGroup))
	}
	if handlers && !has(CapGroup) {
		errs = append(errs, fmt.Errorf("connector %s: group handlers registered, %s not declared", connector, CapGroup))
	}
	switch {
	case g.Append != nil && !has(CapMeta):
		errs = append(errs, fmt.Errorf("connector %s: an Append handler writes meta: declare %s (and meta_description)", connector, CapMeta))
	case g.Append == nil && has(CapMeta):
		errs = append(errs, fmt.Errorf("connector %s: %s declared without an Append handler: the hub would ask for meta nobody writes", connector, CapMeta))
	}
	if has(CapMeta) && !has(CapGroup) {
		errs = append(errs, fmt.Errorf("connector %s: %s without %s", connector, CapMeta, CapGroup))
	}
	desc := ""
	if c.MetaDescription != nil {
		desc = strings.TrimSpace(*c.MetaDescription)
	}
	switch {
	case has(CapMeta) && desc == "":
		errs = append(errs, fmt.Errorf("connector %s: %s needs meta_description (what the meta is, for the model)", connector, CapMeta))
	case !has(CapMeta) && c.MetaDescription != nil:
		errs = append(errs, fmt.Errorf("connector %s: meta_description without %s", connector, CapMeta))
	case utf8.RuneCountInString(desc) > MetaDescriptionMost:
		errs = append(errs, fmt.Errorf("connector %s: meta_description longer than %d characters", connector, MetaDescriptionMost))
	}
	return errors.Join(errs...)
}
