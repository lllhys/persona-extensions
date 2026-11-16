// Package model is how the curator asks a language model for a judgement, and what it may spend doing so.
//
// Every call is one turn with a JSON answer: no agents, no tools, no files. The model never sees more than the
// lines the triage admitted, and what it answers is checked by the verify package before anything is kept.
package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Tier is the class of model a step asks for; the hub maps it to the user's own model settings.
type Tier string

const (
	Small  Tier = "small"  // Haiku class: daily digests, triage tags
	Medium Tier = "medium" // Sonnet class: weekly consolidation
)

// Request is one call.
type Request struct {
	Step      string          // triage | daily | weekly: also the usage purpose
	Tier      Tier            //
	System    string          //
	User      string          // the input, usually JSON
	MaxTokens int             //
	Schema    json.RawMessage // the JSON schema of the answer
}

// Response is a call's answer and what it cost.
type Response struct {
	Text      string
	Model     string
	InTokens  int
	OutTokens int
	// Nonconforming is the hub's account of how Text fails the request's schema, when it returned the reply anyway
	// (invalid_output): the caller's own checks of each item decide what is kept.
	Nonconforming string
}

// Model answers requests.
type Model interface {
	Complete(ctx context.Context, r Request) (Response, error)
}

// ErrUnavailable: the hub cannot run a model for the extension now: a hub without model.complete (older than
// nwverse/persona#6102), the permission not granted, nothing set up for the extension's purpose, or the connection
// held for quota or load. The curator then runs only its deterministic steps; the wrapped message says which.
var ErrUnavailable = errors.New("model unavailable to extensions")

// ErrRefused: the model declined this input (the Messages API's stop_reason "refusal", a safety classifier). The
// hub sends that as model_failed with its message "the model declined to answer", and the same input is declined
// again: the curator does not resend it as it is but splits it into smaller batches (curate's inBatches).
var ErrRefused = errors.New("model declined to answer")

// refused says whether a model_failed error is a refusal. The hub has no code of its own for it, only the message
// of its ErrRefused ("ai: the model declined to answer"); the other words are other upstreams' refusals. Not
// "refused": "connection refused" is a network failure, worth retrying as it is.
func refused(msg string) bool {
	m := strings.ToLower(msg)
	// "rejected as invalid": an OpenAI-style upstream turning a request down for what it contains, with a 400 and no
	// reply (seen with a GPT model on a group chat); like a refusal, a smaller part may go through.
	for _, w := range []string{"declined to answer", "refusal", "content_filter", "rejected as invalid"} {
		if strings.Contains(m, w) {
			return true
		}
	}
	return false
}

// Hub calls model.complete on the hub (nwverse/persona#6102, permission "models"). The hub runs it on the
// extension's own purpose extension.<id> in Settings › Model configuration, which follows the knowledge base's model
// until the user picks another; the extension never holds a key and does not pick the connection. There is no
// budget and no cap: each call's usage comes back and is recorded per day and step (Metered).
type Hub struct {
	H         *ext.Hub
	Extension string
}

// callTimeout bounds one call, the hub's second try for a reply that does not match the schema included.
func callTimeout(t Tier) int {
	if t == Medium {
		return 600
	}
	return 300
}

func (h Hub) Complete(ctx context.Context, r Request) (Response, error) {
	out, err := h.H.ModelComplete(ctx, wire.ModelCompleteParams{
		System: r.System, Messages: []wire.ModelMessage{{Role: wire.ModelRoleUser, Content: r.User}},
		MaxTokens: r.MaxTokens, JSONSchema: r.Schema, TimeoutSeconds: callTimeout(r.Tier), Label: r.Step,
	})
	var e *ext.Error
	if errors.As(err, &e) {
		switch e.Code {
		case wire.ErrorCodeUnknownMethod, wire.ErrorCodeUnsupported, wire.ErrorCodePermissionDenied,
			wire.ModelErrorCodeModelUnavailable, wire.ModelErrorCodeModelPaused, wire.ModelErrorCodeAssistantOffline:
			return Response{}, fmt.Errorf("%w: %s", ErrUnavailable, e.Message)
		case wire.ModelErrorCodeModelRefused: // a hub that names a refusal with its own code
			return Response{}, fmt.Errorf("%w: %s", ErrRefused, e.Message)
		case wire.ModelErrorCodeModelFailed:
			if refused(e.Message) {
				return Response{}, fmt.Errorf("%w: %s", ErrRefused, e.Message)
			}
		case wire.ModelErrorCodeInvalidOutput:
			// The reply did not match the schema after the hub's second try (a model that leaves a field out, say).
			// The curator checks every item of an answer on its own: take the reply and let those checks keep
			// what holds, rather than lose the whole unit.
			if r, ok := fromInvalidOutput(e.Data); ok {
				return r, nil
			}
		}
	}
	if err != nil {
		return Response{}, err
	}
	if out.StopReason == "refusal" { // a hub that passes the upstream's stop reason through
		return Response{}, fmt.Errorf("%w: stop_reason refusal", ErrRefused)
	}
	text := out.Text
	if len(r.Schema) > 0 && len(out.JSON) > 0 { // the hub checked it against the schema: take the decoded value
		text = string(out.JSON)
	}
	u := out.Usage
	return Response{Text: text, Model: out.Model, InTokens: u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
		OutTokens: u.OutputTokens}, nil
}

// Usage is what the calls cost in tokens, recorded per day and step for the report (there is no cap: the user
// asked for none; the hub's own usage records are what is billed).
type Usage interface {
	Record(day, step string, in, out int)
}

// Metered wraps a model and records its usage.
type Metered struct {
	Model Model
	Usage Usage
	Day   string
	Calls int
	mu    sync.Mutex
}

func (m *Metered) Complete(ctx context.Context, r Request) (Response, error) {
	resp, err := m.Model.Complete(ctx, r)
	if err != nil {
		return resp, err
	}
	m.mu.Lock()
	m.Calls++
	m.mu.Unlock()
	m.Usage.Record(m.Day, r.Step, resp.InTokens, resp.OutTokens)
	return resp, nil
}

// fromInvalidOutput is the reply an invalid_output error carries (wire.ModelInvalidOutput), when it has one.
func fromInvalidOutput(data json.RawMessage) (Response, bool) {
	var bad wire.ModelInvalidOutput
	if json.Unmarshal(data, &bad) != nil || strings.TrimSpace(bad.Text) == "" {
		return Response{}, false
	}
	u := bad.Usage
	return Response{Text: bad.Text, InTokens: u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
		OutTokens: u.OutputTokens, Nonconforming: bad.Problem}, true
}
