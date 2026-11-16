// Package hubapi is how the curator reads the hub beyond the knowledge base (design §3, §12): other agents'
// sessions and ask_user questions (agents.read), to-dos and their decisions (todos.read), "about me"
// (assistant.ask), the assistant's hand-written memory (assistant.memory.read, hub change H2) and the phone alert
// (notify). HTTP uses PERSONA_HUB_URL and PERSONA_HUB_TOKEN (Bearer); Offline answers from local copies (replays).
//
// A source the hub cannot give yet returns ErrWaitingHub (with the hub change it waits for); a scope the user did
// not grant returns ErrNoScope; a source with no copy (offline) returns ErrNotConnected. None is a failure: the
// source is left out and the sources card says why.
//
// The extension's token is filtered by manager only: the hub hands it health-space sessions and private cards like
// any other (hub server/internal/hub/ownership.go extensionReach; nothing in the sessions, items or interactions
// routes looks at the space or Interaction.private). HTTP drops both here, before anything else sees them.
//
// # Manifest changes waiting for the hub
//
// The hub's manifest check knows a closed list of scopes (hub extension/perm.go): listing one it does not know fails
// the install. These go into persona-extension.json when the hub ships them, not before:
//
//   - H2: "assistant.memory.read" in permissions.hub (read the assistant's hand-written memory; Memories here then
//     calls the extension RPC assistant.memory with {include_content}).
//
//   - H1, H4, H5: the data contributions, under contributes:
//
//     "assistant": [{"id": "enkeep-memory", "source": "data", "instructions": "assistant/instructions.md",
//     "skills": "assistant/skills"}],
//     "agent_context": [{"id": "enkeep-memory", "source": "data", "dir": "agent/projects"}],
//     "inbox_context": [{"id": "enkeep-memory", "source": "data", "context": "inbox/context.md",
//     "never_quote": "inbox/never_quote.txt"}]
//
//     (the exact keys follow the hub's H1 manifest format when it lands; the paths are internal/export/layout.go's).
//     Unknown contribution points are skipped by today's hub, but an unknown key inside a known point fails the
//     strict decode, so they wait too.
//
// Already in the manifest (the hub knows them today): agents.read, todos.read, notify, assistant.ask.
package hubapi

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrNoScope: the extension was not granted the scope (403 with a scope).
var ErrNoScope = errors.New("scope not granted")

// ErrNotConnected: there is no copy of this source to read (offline replays without the file or directory).
var ErrNotConnected = errors.New("source not connected")

// WaitingHub is the error for a route or field the hub does not have yet.
type WaitingHub struct{ Change string } // H1…H5

func (w *WaitingHub) Error() string { return fmt.Sprintf("waiting for hub change %s", w.Change) }

// ErrWaitingHub matches any *WaitingHub with errors.Is.
var ErrWaitingHub = &WaitingHub{}

// Is makes errors.Is(err, ErrWaitingHub) true for every WaitingHub.
func (w *WaitingHub) Is(target error) bool { _, ok := target.(*WaitingHub); return ok }

// Session is an agent session as the sessions list gives it.
type Session struct {
	Machine   string    `json:"machine_id"`
	ID        string    `json:"session_id"`
	Title     string    `json:"title,omitempty"`
	Agent     string    `json:"agent,omitempty"` // the launcher or runtime (claude, codex…)
	Project   string    `json:"project,omitempty"`
	Started   time.Time `json:"started"`
	Updated   time.Time `json:"updated"`
	Ended     string    `json:"ended,omitempty"`     // normal | interrupted | timeout | "" (running)
	Assistant bool      `json:"assistant,omitempty"` // the main assistant's own session (S4)
	Health    bool      `json:"health,omitempty"`    // a health space session: never read
	Cwd       string    `json:"cwd,omitempty"`
	Status    string    `json:"status,omitempty"` // the hub's: idle | working | blocked | done | interrupted…
	// ByPersona: Persona started it (a task, a relay): its first message is Persona's, not the user's.
	ByPersona bool `json:"by_persona,omitempty"`
}

// ItemsQuery reads a session's items. Daily reading asks Fold (work folded to counts and file names) and After
// (incremental); a dig asks From..Through unfolded.
type ItemsQuery struct {
	Fold    bool
	After   int // after_seq
	From    int // from_seq
	Through int // through_seq
	Limit   int
}

// Item kinds as the curator sees them. The hub's items have role and kind (text, thinking, tool_use, tool_result,
// notice, image, recap, fold); a user text the hub marks injected, from another session or from a Persona source
// (supervision, follow-up, task notice) is a notice here, never the user's words. Cards are not items: they come
// from Interactions, joined by session.
const (
	KindUser       = "user"
	KindAgent      = "agent"
	KindToolUse    = "tool_use"
	KindToolResult = "tool_result"
	KindThinking   = "thinking"
	KindNotice     = "notice"
	KindFold       = "fold"
)

// Item is one session item.
type Item struct {
	ID   string    `json:"id,omitempty"`
	Seq  int       `json:"seq"`
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
	Text string    `json:"text,omitempty"` // user and agent text; a tool call's input; a result
	Tool string    `json:"tool,omitempty"`
	Err  bool      `json:"error,omitempty"` // a tool_result that is an error
	Fold *Fold     `json:"fold,omitempty"`
	// Notice: what made a user-role text not the user's (injected kind, source kind, "paste", "persona"…).
	Notice string `json:"notice,omitempty"`
	Cut    bool   `json:"cut,omitempty"` // the text was cut by the hub (full_size): the rest is not read
	// Call pairs a tool call with its result (the tool_use id on both).
	Call string `json:"call,omitempty"`
}

// Fold is a folded stretch of work. Errors and Tools come with hub change H3; an older hub leaves them empty and
// Known false, which the screen treats as "not known" (not as "no errors").
type Fold struct {
	From   int      `json:"from_seq"`
	To     int      `json:"through_seq"`
	Calls  int      `json:"calls"`
	Files  []string `json:"files,omitempty"`
	Errors int      `json:"errors,omitempty"`
	Tools  []string `json:"tool_names,omitempty"`
	Known  bool     `json:"-"` // Errors and Tools were given
}

// InteractionsQuery pages the ask_user history. Since and Before need H3: until then the hub gives only the newest
// ≤500, which HTTP filters to the range and returns together with a *WaitingHub{"H3"} (partial, not a failure).
type InteractionsQuery struct {
	Since    time.Time
	Before   time.Time
	BeforeID string // with Before: the cursor of the next older page (next_before_id)
	Kind     string
	Machine  string
	Session  string
	Limit    int
}

// Interaction is one card: the question, its options, the suggested one, and the answer.
type Interaction struct {
	ID       string    `json:"id"`
	Machine  string    `json:"machine_id,omitempty"`
	Session  string    `json:"session_id,omitempty"`
	Kind     string    `json:"kind"` // ask_user, permission, plan…
	At       time.Time `json:"created_at"`
	Question string    `json:"question"`
	Options  []string  `json:"options,omitempty"`
	// Recommend is the recommended option's label, as the card kept it (questions[].recommend, or the delegate's
	// recommendation): "" is unknown, not "none recommended" (cards from before hub #5876 do not keep it).
	Recommend string   `json:"recommend,omitempty"`
	Answers   []string `json:"answers,omitempty"`
	// AnsweredBy: user (resolution.answered_by absent: the user in an app, or on the to-do an expired card became),
	// assistant (answered for the user), default (nobody answered; the recommendation was taken at the deadline),
	// "" (not answered). Only "user" is the user's own choice.
	AnsweredBy string `json:"answered_by,omitempty"`
	// Late: answered after the card expired (Status stays expired): it still counts as answered.
	Late bool `json:"late,omitempty"`
	// Tool is the tool that raised the card (ask_user for the assistant's own questions); "" when not given.
	Tool string `json:"tool,omitempty"`
	// Deadline: when an unanswered question takes its recommendation (or is only reminded). Zero: none or unknown.
	Deadline time.Time `json:"deadline,omitempty"`
	// Reversible: whether going ahead can be undone, as the assistant said; nil when it did not say (unknown — never
	// read as false, and never as "may act on it").
	Reversible *bool  `json:"reversible,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Private    bool   `json:"private,omitempty"`
	Status     string `json:"status,omitempty"` // resolved | dismissed | cancelled | expired | pending
	Title      string `json:"session_title,omitempty"`
}

// Todo is a to-do with its state (S5).
type Todo struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Done     bool      `json:"done"`
	Project  string    `json:"project,omitempty"`
	Updated  time.Time `json:"updated"`
	Reopened int       `json:"reopened,omitempty"`
	Created  time.Time `json:"created,omitempty"`
	Closed   time.Time `json:"closed,omitempty"` // done or cancelled
	// Outcome: done | cancelled | "" (open); Reason the done or cancel reason; By who closed it.
	Outcome string `json:"outcome,omitempty"`
	Reason  string `json:"reason,omitempty"`
	By      string `json:"by,omitempty"`
	Kind    string `json:"kind,omitempty"`
}

// Decision is one decision written on a to-do: the question, the choice and why, and where it came from (manual,
// card, outcome, agent, thread, reported).
type Decision struct {
	At       time.Time `json:"at"`
	By       string    `json:"by,omitempty"`
	Text     string    `json:"text"` // question → choice (reason), as one line
	Question string    `json:"question,omitempty"`
	Choice   string    `json:"choice,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Replaced bool      `json:"replaced,omitempty"` // superseded by a later decision
}

// MemoryFile is one hand-written memory (H2): name, frontmatter description and type, times, body (optional).
type MemoryFile struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Type        string    `json:"type,omitempty"`
	Created     time.Time `json:"created,omitempty"` // zero when the system cannot tell
	Modified    time.Time `json:"modified"`
	Size        int       `json:"size"`
	Content     string    `json:"content,omitempty"`
	Truncated   bool      `json:"truncated,omitempty"` // content cut at 16 KB, or left out past 512 KB a call
}

// ErrOffline: the assistant's computer is not connected or not set up (assistant.memory's machine_offline,
// assistant_offline). The source is missing for now; it is not an empty memory.
var ErrOffline = errors.New("assistant offline")

// Adoption is the hub's readback of a data contribution (H1, H4, H5): the hash it uses and when.
type Adoption struct {
	Exit      string    `json:"exit"` // instructions | skills | project_context | inbox_context
	Hash      string    `json:"hash"`
	At        time.Time `json:"at"`
	Truncated bool      `json:"truncated,omitempty"`
	// Reason and Earliest: why the current version was not taken, and the earliest time it will be (optional; the
	// readback's fields are a stub until the hub's H1/H4/H5 land).
	Reason   string    `json:"reason,omitempty"`
	Earliest time.Time `json:"earliest,omitempty"`
	// State is the hub's: adopted (in use), pending (a newer version waits for its window), kept (the older text stays:
	// the new one was refused), none. Current: the hub uses the file as it is now.
	State   string `json:"state,omitempty"`
	Current bool   `json:"current,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Hub is everything the curator asks the hub beyond kb.* and assistant.read. Every method may return ErrNoScope,
// ErrWaitingHub or ErrNotConnected; callers treat them as "this source is not there" and record why. Interactions
// alone may return items with a *WaitingHub: what it could read, short of what was asked.
type Hub interface {
	Sessions(ctx context.Context, since time.Time) ([]Session, error)
	Items(ctx context.Context, machine, session string, q ItemsQuery) ([]Item, error)
	Interactions(ctx context.Context, q InteractionsQuery) ([]Interaction, error)
	Todos(ctx context.Context, since time.Time) ([]Todo, error)
	TodoDecisions(ctx context.Context, id string) ([]Decision, error)
	AboutMe(ctx context.Context) (string, error)
	Memories(ctx context.Context, content bool) ([]MemoryFile, error) // H2
	Adoptions(ctx context.Context) ([]Adoption, error)                // H1/H4/H5 readback
	Notify(ctx context.Context, title, body string) error
}

// None is a hub that has nothing: every source missing. Tests and a hub without any of the scopes use it.
type None struct{}

func (None) Sessions(context.Context, time.Time) ([]Session, error) { return nil, ErrNoScope }
func (None) Items(context.Context, string, string, ItemsQuery) ([]Item, error) {
	return nil, ErrNoScope
}
func (None) Interactions(context.Context, InteractionsQuery) ([]Interaction, error) {
	return nil, ErrNoScope
}
func (None) Todos(context.Context, time.Time) ([]Todo, error)          { return nil, ErrNoScope }
func (None) TodoDecisions(context.Context, string) ([]Decision, error) { return nil, ErrNoScope }
func (None) AboutMe(context.Context) (string, error)                   { return "", ErrNoScope }
func (None) Memories(context.Context, bool) ([]MemoryFile, error)      { return nil, &WaitingHub{"H2"} }
func (None) Adoptions(context.Context) ([]Adoption, error)             { return nil, &WaitingHub{"H1"} }
func (None) Notify(context.Context, string, string) error              { return ErrNoScope }
