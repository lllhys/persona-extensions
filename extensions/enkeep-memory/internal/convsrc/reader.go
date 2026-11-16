package convsrc

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Conversation is one of the user's conversations with the assistant, as a reader lists it.
type Conversation struct {
	ID      string // the reader's own id; the chat is Prefix + ID
	Title   string
	Updated time.Time // its last activity
}

// Reader is where the conversations come from: the hub (assistant.read, nwverse/persona#1292) or, offline, a copy of
// the assistant's transcripts. Both give only words: the user's messages and the assistant's replies, in time order.
type Reader interface {
	// Conversations are those active in [since, until) (until zero: up to now).
	Conversations(ctx context.Context, since, until time.Time) ([]Conversation, error)
	// Messages are one conversation's turns in [since, until).
	Messages(ctx context.Context, id string, since, until time.Time) ([]Turn, error)
}

// ErrNoAccess: the hub does not give this extension the assistant's conversations (an older hub without
// assistant.read, or the user did not grant it). The curator then goes on without them.
var ErrNoAccess = errors.New("no access to the assistant's conversations")

// ErrAway: the assistant (its manager or its computer) is not connected now; try again on the next run.
var ErrAway = errors.New("the assistant is away")

// Origins of a conversation the user opened: in the app, or with a channel's /new. Conversations the hub opened by
// itself (a task, a spawned relay, the inbox's AI twin, an extension, a plugin's /agent) hold what Persona typed in
// for the model as role=user: they are never read. Agents' own sessions are not read at all: what Persona sends them
// (task_plan, agent_start, agent_send, supervision, interaction_answer) is role=user in their transcripts with no
// mark, so nothing in them can be confirmed as the user's.
var userOrigins = []string{"user", "channel"}

// UserOrigin says whether a conversation of this origin was opened by the user.
func UserOrigin(origin string) bool { return slices.Contains(userOrigins, origin) }

// Hub reads the conversations with assistant.conversations and assistant.messages (scope assistant.read).
type Hub struct{ H *ext.Hub }

func (h Hub) Conversations(ctx context.Context, since, until time.Time) ([]Conversation, error) {
	p := wire.AssistantConversationsParams{Since: since.UTC().Format(time.RFC3339), Limit: 200}
	if !until.IsZero() {
		p.Until = until.UTC().Format(time.RFC3339)
	}
	var out []Conversation
	for {
		r, err := h.H.AssistantConversations(ctx, p)
		if err != nil {
			return out, mapErr(err)
		}
		for _, c := range r.Items {
			if slices.Contains(userOrigins, c.Origin) && !c.Updated.Before(since) {
				out = append(out, Conversation{ID: c.ID, Title: c.Title, Updated: c.Updated})
			}
		}
		if r.NextOffset == nil || *r.NextOffset <= p.Offset {
			return out, nil
		}
		p.Offset = *r.NextOffset
	}
}

// maxPages bounds one conversation-day's reading: a page can be empty after a long stretch of tool work, so the
// loop follows next_seq, but never forever.
const maxPages = 200

func (h Hub) Messages(ctx context.Context, id string, since, until time.Time) ([]Turn, error) {
	p := wire.AssistantMessagesParams{ID: id, Since: since.UTC().Format(time.RFC3339), Limit: 500}
	if !until.IsZero() {
		p.Until = until.UTC().Format(time.RFC3339)
	}
	var msgs []wire.AssistantMessage
	for i := 0; i < maxPages; i++ {
		r, err := h.H.AssistantMessages(ctx, p)
		if err != nil {
			return nil, mapErr(err)
		}
		msgs = append(msgs, r.Messages...)
		if r.NextSeq == nil || (p.AfterSeq != nil && *r.NextSeq <= *p.AfterSeq) {
			break
		}
		next := *r.NextSeq
		p.AfterSeq = &next
	}
	return fromMessages(msgs), nil
}

func mapErr(err error) error {
	var e *ext.Error
	if errors.As(err, &e) {
		switch e.Code {
		case wire.ErrorCodePermissionDenied, wire.ErrorCodeUnknownMethod, wire.ErrorCodeUnsupported:
			return fmt.Errorf("%w: %s", ErrNoAccess, e.Message)
		case wire.AssistantErrorCodeAssistantOffline, wire.AssistantErrorCodeMachineOffline:
			return fmt.Errorf("%w: %s", ErrAway, e.Message)
		}
	}
	return err
}

// fromMessages makes turns of the hub's messages, with the same shaping as a transcript's: the user's messages
// cleaned (the hub has already removed context blocks and Persona's own notices; what it may have missed is removed
// here too) and pasted material cut, the assistant's consecutive replies joined into one and kept head and tail.
func fromMessages(msgs []wire.AssistantMessage) []Turn {
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].Seq < msgs[j].Seq })
	var out []Turn
	var reply []string
	var replyAt time.Time
	flush := func() {
		if len(reply) > 0 {
			out = append(out, Turn{At: replyAt, Text: headTail(strings.Join(reply, "\n"))})
			reply = nil
		}
	}
	for _, m := range msgs {
		switch m.Role {
		case wire.AssistantRoleUser:
			flush()
			if text, ok := cleanUser(m.Text); ok {
				out = append(out, Turn{At: m.At, User: true, Text: text})
			}
		case wire.AssistantRoleAssistant:
			if t := strings.TrimSpace(m.Text); t != "" {
				if len(reply) == 0 {
					replyAt = m.At
				}
				reply = append(reply, t)
			}
		}
	}
	flush()
	return out
}

// Transcripts reads a copy of the assistant's transcripts (Claude Code JSONL files) in a directory, for offline
// reruns: the same words as assistant.messages gives, cleaned the same way. The directory is only ever read. A
// transcript does not say who opened its conversation, so only the sessions Origins names with an origin the user
// opens (UserOrigin) are read; with no Origins, none is: what cannot be confirmed as the user's is not taken.
type Transcripts struct {
	Dir string
	// Origins: session id (the file's name, or its first characters as in the chat id) → origin, as the hub lists
	// them (assistant.conversations' origin).
	Origins  map[string]string
	sessions map[string]*Session
	err      error
	loaded   bool
}

func (t *Transcripts) load() (map[string]*Session, error) {
	if !t.loaded {
		t.sessions, t.err = Load(t.Dir)
		t.loaded = true
		for id := range t.sessions {
			if !UserOrigin(t.origin(strings.TrimPrefix(id, Prefix))) {
				delete(t.sessions, id)
			}
		}
	}
	return t.sessions, t.err
}

// origin is a session's origin from Origins, by its full id or the short one ("" when not listed).
func (t *Transcripts) origin(short string) string {
	for id, o := range t.Origins {
		if id == short || strings.HasPrefix(id, short) {
			return o
		}
	}
	return ""
}

func (t *Transcripts) Conversations(_ context.Context, since, until time.Time) ([]Conversation, error) {
	ss, err := t.load()
	if err != nil {
		return nil, err
	}
	var out []Conversation
	for _, id := range sortedIDs(ss) {
		se := ss[id]
		var last time.Time
		for _, tu := range se.Turns {
			if !tu.At.Before(since) && (until.IsZero() || tu.At.Before(until)) {
				last = tu.At
			}
		}
		if !last.IsZero() {
			out = append(out, Conversation{ID: strings.TrimPrefix(id, Prefix), Title: se.Name, Updated: last})
		}
	}
	return out, nil
}

func (t *Transcripts) Messages(_ context.Context, id string, since, until time.Time) ([]Turn, error) {
	ss, err := t.load()
	if err != nil {
		return nil, err
	}
	se := ss[Prefix+id]
	if se == nil {
		return nil, nil
	}
	var out []Turn
	for _, tu := range se.Turns {
		if !tu.At.Before(since) && (until.IsZero() || tu.At.Before(until)) {
			out = append(out, tu)
		}
	}
	return out, nil
}
