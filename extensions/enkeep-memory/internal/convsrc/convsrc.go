// Package convsrc reads the user's conversations with the Persona assistant as a source of the knowledge base.
//
// They are what the user said to an AI, in the user's own words, and the most direct evidence of how the user judges
// and works. With wraps any kbsrc.Source and adds every conversation as a chat "persona-<id>", whose days hold the
// user's turns ("我") and the assistant's ("助手"), cleaned of what the hub and the agent injected. The words come
// from a Reader: on the hub, assistant.conversations and assistant.messages (scope assistant.read); offline, a copy
// of the assistant's Claude Code transcripts, which is only ever read.
package convsrc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Prefix starts the id of every conversation chat.
const Prefix = "persona-"

// Limits on what a turn keeps.
const (
	pasteOver   = 2000 // a user message longer than this (runes) is likely pasted material
	pasteKeep   = 600  // … and keeps this much
	replyHead   = 300  // the assistant's reply keeps its head and tail: the model only needs what the user answers
	replyTail   = 200
	nameRunes   = 20
	maxLineSize = 64 << 20
)

// Turn is one message of a conversation.
type Turn struct {
	At   time.Time
	User bool // the user's; otherwise the assistant's
	Text string
}

// Session is one conversation.
type Session struct {
	ID    string // the chat id: Prefix + the first 8 characters of the session id
	Name  string
	Turns []Turn // in time order
}

// Source is a kbsrc.Source with the conversations added: each is a chat Prefix + its id, of source "persona" and
// kind "assistant", whose days hold the user's turns ("我") and the assistant's ("助手").
type Source struct {
	kbsrc.Source
	R   Reader
	Loc *time.Location
	// Logf reports a reader that could not be read (no access, the assistant away); the run goes on without it.
	Logf func(format string, args ...any)

	mu    sync.Mutex
	known map[string]Conversation // by chat id, as the last Recent listed them
}

// With adds the conversations a reader gives to src. Days are local days in loc.
func With(src kbsrc.Source, r Reader, loc *time.Location) *Source {
	if loc == nil {
		loc = time.UTC
	}
	return &Source{Source: src, R: r, Loc: loc, known: map[string]Conversation{}}
}

// FromDir adds the conversations of a copy of the assistant's transcripts (offline reruns): only those origins names
// as opened by the user (session id → origin, as assistant.conversations gives it).
func FromDir(src kbsrc.Source, dir string, origins map[string]string, loc *time.Location) *Source {
	return With(src, &Transcripts{Dir: dir, Origins: origins}, loc)
}

func (s *Source) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

func (s *Source) entry(c Conversation) wire.KBEntry {
	return wire.KBEntry{Kind: "chats", ID: Prefix + c.ID, Name: s.name(c), Updated: c.Updated, Source: "persona", Extra: "assistant"}
}

func (s *Source) name(c Conversation) string {
	if strings.HasPrefix(c.Title, "Persona 对话") {
		return c.Title
	}
	return "Persona 对话 · " + runes(strings.Join(strings.Fields(c.Title), " "), nameRunes)
}

// Get answers for a conversation chat from what Recent listed (the name; the rest is fixed).
func (s *Source) Get(ctx context.Context, kind, id string) (wire.KBDetail, error) {
	if kind != "chats" || !strings.HasPrefix(id, Prefix) {
		return s.Source.Get(ctx, kind, id)
	}
	s.mu.Lock()
	c, ok := s.known[id]
	s.mu.Unlock()
	if !ok {
		c = Conversation{ID: strings.TrimPrefix(id, Prefix)}
	}
	meta, _ := json.Marshal(map[string]any{"id": id, "source": "persona", "kind": "assistant", "name": s.name(c), "mine": true})
	return wire.KBDetail{Kind: kind, ID: id, Meta: meta}, nil
}

// Day reads one conversation's turns in the local day.
func (s *Source) Day(ctx context.Context, chat, day string) (wire.KBDayResult, error) {
	if !strings.HasPrefix(chat, Prefix) {
		return s.Source.Day(ctx, chat, day)
	}
	start, err := time.ParseInLocation("2006-01-02", day, s.Loc)
	if err != nil {
		return wire.KBDayResult{}, err
	}
	ts, err := s.R.Messages(ctx, strings.TrimPrefix(chat, Prefix), start, start.AddDate(0, 0, 1))
	if err != nil {
		return wire.KBDayResult{}, err
	}
	if len(ts) == 0 {
		return wire.KBDayResult{}, kbsrc.ErrNotFound
	}
	r := wire.KBDayResult{Day: day, Messages: []wire.KBDayMessage{}}
	for _, t := range ts {
		who := "我"
		if !t.User {
			who = voice.AI
		}
		r.Messages = append(r.Messages, wire.KBDayMessage{N: len(r.Messages) + 1, Text: t.At.In(s.Loc).Format("15:04") + " " + who + ": " + t.Text})
	}
	return r, nil
}

// Recent adds the conversations active in [since, until). A reader that cannot be read now is reported and left
// out: the other sources still go through.
func (s *Source) Recent(ctx context.Context, since, until time.Time) ([]wire.KBEntry, error) {
	out, err := s.Source.Recent(ctx, since, until)
	if err != nil {
		return out, err
	}
	cs, err := s.R.Conversations(ctx, since, until)
	if err != nil {
		s.logf("Persona conversations not read: %v", err)
		return out, nil
	}
	s.mu.Lock()
	for _, c := range cs {
		s.known[Prefix+c.ID] = c
		out = append(out, s.entry(c))
	}
	s.mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

func sortedIDs(ss map[string]*Session) []string {
	ids := make([]string, 0, len(ss))
	for id := range ss {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Load reads every transcript under dir. A subagent's transcript (under a "subagents" directory) is not a
// conversation with the user and is skipped; a file without a single turn is too.
func Load(dir string) (map[string]*Session, error) {
	out := map[string]*Session{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir {
				return err
			}
			return nil
		}
		if d.IsDir() {
			if d.Name() == "subagents" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		se, err := ReadFile(path)
		if err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		if se == nil {
			return nil
		}
		if cur := out[se.ID]; cur != nil { // two files of one session (or an id prefix shared): one conversation
			cur.Turns = append(cur.Turns, se.Turns...)
			sort.SliceStable(cur.Turns, func(i, j int) bool { return cur.Turns[i].At.Before(cur.Turns[j].At) })
			return nil
		}
		out[se.ID] = se
		return nil
	})
	return out, err
}

type record struct {
	Type        string    `json:"type"`
	Timestamp   time.Time `json:"timestamp"`
	SessionID   string    `json:"sessionId"`
	IsMeta      bool      `json:"isMeta"`
	IsSidechain bool      `json:"isSidechain"`
	Message     struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ReadFile reads one transcript. It returns nil when the file holds no turn.
func ReadFile(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), maxLineSize)
	var recs []record
	for sc.Scan() {
		var r record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue // a torn last line, or a record of a shape this does not read
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sid := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	for _, r := range recs {
		if r.SessionID != "" {
			sid = r.SessionID
			break
		}
	}
	ts := turns(recs)
	if len(ts) == 0 {
		return nil, nil
	}
	short := sid
	if len(short) > 8 {
		short = short[:8]
	}
	se := &Session{ID: Prefix + short, Turns: ts}
	first := ""
	for _, t := range ts {
		if t.User {
			first = strings.Join(strings.Fields(t.Text), " ")
			break
		}
	}
	se.Name = "Persona 对话 · " + runes(first, nameRunes)
	return se, nil
}

// turns makes the turns of a transcript's records: the user's cleaned messages, and between them the assistant's
// text joined into one reply.
func turns(recs []record) []Turn {
	var out []Turn
	var reply []string
	var replyAt time.Time
	flush := func() {
		if len(reply) > 0 {
			out = append(out, Turn{At: replyAt, Text: headTail(strings.Join(reply, "\n"))})
			reply = nil
		}
	}
	for _, r := range recs {
		if r.IsMeta || r.IsSidechain || r.Timestamp.IsZero() {
			continue
		}
		switch r.Type {
		case "user":
			text, ok := userText(r.Message.Content)
			if !ok {
				if len(texts(r.Message.Content)) > 0 {
					flush() // a notification or a command: what the assistant says next does not answer the user
				}
				continue // a tool result, a slash command, a notification: not a turn
			}
			flush()
			out = append(out, Turn{At: r.Timestamp, User: true, Text: text})
		case "assistant":
			for _, t := range texts(r.Message.Content) {
				if t = strings.TrimSpace(t); t != "" {
					if len(reply) == 0 {
						replyAt = r.Timestamp
					}
					reply = append(reply, t)
				}
			}
		}
	}
	flush()
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// texts are the text blocks of a message's content (a plain string is one).
func texts(raw json.RawMessage) []string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var bs []block
	if json.Unmarshal(raw, &bs) != nil {
		return nil
	}
	var out []string
	for _, b := range bs {
		if b.Type == "text" {
			out = append(out, b.Text)
		}
	}
	return out
}

var (
	// Blocks the hub or the agent puts into a user message: removed, the rest kept.
	injected = regexp.MustCompile(`(?s)<(jarvis-context|persona-context|system-reminder)\b[^>]*>.*?</(jarvis-context|persona-context|system-reminder)>`)
	// Wrappers that make the whole message not the user's words: slash commands and their output, task notices.
	wrapped     = regexp.MustCompile(`<(command-name|command-message|command-args|local-command-stdout|local-command-stderr|task-notification)\b`)
	interrupted = regexp.MustCompile(`\[Request interrupted by user[^\]]*\]`)
	// The marks of the messages the hub types into a conversation as if the user wrote them (the hub's
	// prompts.ParseInjected): task notices, supervision, inbox notes, wake-ups, the continue after a usage reset, a node
	// restart's lost tasks. The hub leaves them out of assistant.messages; they are dropped here too, for older hubs
	// and transcript copies.
	notices = []string{"这是 Persona 的任务通知", "这是 Persona 的督办消息", "这是 Persona 的收件箱通知", "不是用户发的消息",
		"not a message from the user", "<persona-supervision", "（Persona 叫醒你", "（Persona 叫你接着做）"}
	noticeHeads = []string{"🔔 任务", "⏰", "📋 督办：", "📥 收件箱事件「", "⚠️ 运行你的 node 刚重启过"}
)

// userText is the user's own words in a user record, or false when it holds none.
func userText(raw json.RawMessage) (string, bool) {
	ts := texts(raw)
	if len(ts) == 0 {
		return "", false
	}
	return cleanUser(strings.Join(ts, "\n"))
}

// cleanUser is a user message without what the hub or the agent put into it, or false when nothing of the user's
// is left (a slash command, a task notice, a scheduled follow-up). Pasted material is cut.
func cleanUser(text string) (string, bool) {
	if wrapped.MatchString(text) {
		return "", false
	}
	for _, n := range notices {
		if strings.Contains(text, n) {
			return "", false
		}
	}
	text = injected.ReplaceAllString(text, "")
	text = interrupted.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	for _, h := range noticeHeads {
		if strings.HasPrefix(text, h) {
			return "", false
		}
	}
	if n := utf8.RuneCountInString(text); n > pasteOver {
		text = string([]rune(text)[:pasteKeep]) + fmt.Sprintf("…[疑似粘贴材料，略去 %d 字]…", n-pasteKeep)
	}
	return text, true
}

func headTail(s string) string {
	r := []rune(s)
	if len(r) <= replyHead+replyTail {
		return s
	}
	return strings.TrimSpace(string(r[:replyHead])) + "……" + strings.TrimSpace(string(r[len(r)-replyTail:]))
}

func runes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
