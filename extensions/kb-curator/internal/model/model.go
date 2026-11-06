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
}

// Model answers requests.
type Model interface {
	Complete(ctx context.Context, r Request) (Response, error)
}

// ErrUnavailable: the hub cannot run models for extensions (no model.complete yet, or no model set up). The
// curator then runs only its deterministic steps.
var ErrUnavailable = errors.New("model unavailable to extensions")

// Hub calls model.complete on the hub (docs/interface-needs.md N1: not in the hub yet). The hub resolves the tier
// to the model the user picked for this extension under Settings › Models, so the extension never holds a key.
type Hub struct {
	H         *ext.Hub
	Extension string
}

type completeParams struct {
	Purpose    string          `json:"purpose"`
	Tier       Tier            `json:"tier"`
	System     string          `json:"system"`
	Messages   []message       `json:"messages"`
	MaxTokens  int             `json:"max_tokens"`
	JSONSchema json.RawMessage `json:"json_schema,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completeResult struct {
	Text  string `json:"text"`
	Model string `json:"model"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (h Hub) Complete(ctx context.Context, r Request) (Response, error) {
	var out completeResult
	err := h.H.Call(ctx, "model.complete", completeParams{
		Purpose: "plugin." + h.Extension + "." + r.Step, Tier: r.Tier, System: r.System,
		Messages: []message{{Role: "user", Content: r.User}}, MaxTokens: r.MaxTokens, JSONSchema: r.Schema,
	}, &out)
	var e *ext.Error
	if errors.As(err, &e) && (e.Code == wire.ErrorCodeUnknownMethod || e.Code == "not_configured" || e.Code == wire.ErrorCodeUnsupported) {
		return Response{}, fmt.Errorf("%w: %s", ErrUnavailable, e.Message)
	}
	if err != nil {
		return Response{}, err
	}
	return Response{Text: out.Text, Model: out.Model, InTokens: out.Usage.InputTokens, OutTokens: out.Usage.OutputTokens}, nil
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
