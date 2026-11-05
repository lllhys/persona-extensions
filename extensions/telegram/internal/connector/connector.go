// Package connector is how a source of the user's information (GitHub, iMessage, later Feishu and mail) plugs into
// the hub: one connector per source, and each pipe the hub has — the assistant's tools, writes behind a gate, the
// settings page's status, realtime kicks — picks up what the connector implements, so adding a source does not
// touch those pipes (the host's connector capability contract).
//
// A connector implements Connector, whose Manifest declares its capabilities; the hub finds each declared one's
// interface with As. Runner, Events and Machines are how it runs, found by type assertion.
package connector

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nwverse/persona-extensions/extensions/telegram/internal/kb"
	"github.com/nwverse/persona-extensions/extensions/telegram/internal/proto"
)

// Connector is what every connector provides: who it is.
type Connector interface {
	// ID names it everywhere: the key in hub.json, the prefix of its tools ("github_read"), InboxItem.Source.
	// Lowercase letters and digits only.
	ID() string
	// Name is for people: GitHub, Messages.
	Name() string
	// Manifest is what it declares (Check holds it to that).
	Manifest() Manifest
}

// States of a connector (Status.State).
const (
	StateOK            = "ok"
	StateNotConfigured = "not_configured"
	StateNotSignedIn   = "not_signed_in"
	StateNoPermission  = "no_permission"
	StateRateLimited   = "rate_limited"
	StateError         = "error"
	// StateDisabled: the user switched it off (Registry.SetEnabled): it does not run, offer tools or bring anything in.
	StateDisabled = "disabled"
)

// Status is how a connector is doing, for Settings › Connectors and the assistant: only an ok connector offers tools.
type Status struct {
	State string `json:"state"`
	// Account is the account it reads as, for display (a GitHub login, "Messages on this Mac").
	Account string `json:"account,omitempty"`
	// Via says where the sign-in comes from ("gh sign-in from <machine>").
	Via string `json:"via,omitempty"`
	// Error is one sentence for people when State is not ok ("The gh sign-in on <machine> has expired…"); never a secret.
	Error string `json:"error,omitempty"`
	// Guide is how to fix it, a step a sentence, when the user can ("Open System Settings › …"); shown under Error.
	Guide     []string  `json:"guide,omitempty"`
	LastSync  time.Time `json:"last_sync,omitempty"`
	LastEvent time.Time `json:"last_event,omitempty"`
}

// Accounts is a connector that knows how it is doing. Status answers from memory (it is called on every tool list
// and settings page): whatever finds out runs in the connector's own loop. A connector without it is always ok.
type Accounts interface {
	Status(ctx context.Context) Status
}

// ReadTools are tools that only read, offered to the assistant without asking the user (like lark_*). Every name
// starts with "<ID>_"; built-in connectors' descriptions and schemas live in the host's prompts/mcp/<id>/ directory.
type ReadTools interface {
	Tools() []proto.Tool
	Call(ctx context.Context, name string, args json.RawMessage) (string, error)
}

// Action is what the user is shown before a write: Visible says who will see it ("All participants in
// example-org/example-repo#12"), and the card always says so.
type Action struct {
	Name    string `json:"name,omitempty"`
	Visible string `json:"visible,omitempty"`
}

// Actions are tools that write (comment, send). They never run without the gate letting them through (Gate): by
// default the user allows each one on the phone.
type Actions interface {
	Actions() []proto.Tool
	// Describe turns the arguments into what the phone's card says (the conversation, the object, the full text to
	// send). It works offline: what it cannot look up it lists as given. An error refuses a call that could not run
	// before the user is asked.
	Describe(ctx context.Context, name string, args json.RawMessage) (Action, string, error)
	// Do runs it (the gate let it through), refusing arguments it cannot use as Describe does.
	Do(ctx context.Context, name string, args json.RawMessage) (string, error)
}

// Options is a connector with settings of its own the user changes in the app (GitHub's: which CI failures, muted
// repositories): JSON both ways, shown in GET /api/v1/connectors, set with PUT …/{id}/settings {"options": …}.
// SetOptions refuses what it cannot use; the hub keeps what it accepted and gives it back at the next start.
type Options interface {
	Options() json.RawMessage
	SetOptions(json.RawMessage) error
}

// Events are changes the connector hears about as they happen (a realtime socket, a file that changed). Watch runs
// until ctx ends, calling changed with what changed; the hub restarts it with a backoff when it returns an error.
type Events interface {
	Watch(ctx context.Context, changed func(Scope)) error
}

// Scope is what changed (Events).
type Scope string

// Scopes of a change: the knowledge base's chats or items, or the inbox.
const (
	ScopeChats Scope = "chats"
	ScopeItems Scope = "items"
	ScopeInbox Scope = "inbox"
)

// Known says whether s is one of the scopes: one from a program or a machine is checked before the hub acts on it.
func (s Scope) Known() bool { return s == ScopeChats || s == ScopeItems || s == ScopeInbox }

// Runner is a connector with work of its own (checking its sign-in, polling): the hub runs it until ctx ends.
type Runner interface {
	Run(ctx context.Context)
}

// Machines is a connector that depends on one of the user's computers (a token read there, data on its disk): the
// hub tells it when one connects.
type Machines interface {
	MachineOnline(id string)
}

// Chats is a connector whose conversations the knowledge base reads chat by chat, each with its own cursor
// (Feishu's chats, Moments posts): one kb.Source per stream, its Name the prefix of its chats' ids (which never
// changes). A source that can read further back also implements kb.Backfiller.
type Chats interface {
	ChatSources() []kb.Source
}

// Items is a connector with documents, meetings, events… for the knowledge base's items.
type Items interface {
	ItemSource() kb.ItemSource
}
