// Package sources adds the curator's sources beyond the knowledge base to the one pipeline every unit goes through
// (design §3): other agents' sessions (S3, agents/<machine>/<session>), ask_user cards (S2, asks/<day>), to-dos and
// their decisions (S5, todos/<id>), "about me" (S7, about/me) and the assistant's hand-written memory (S6,
// memory/<name>). It wraps a kbsrc.Source as convsrc does: each of them is an entity of its own kind whose day reads
// like a chat's ("HH:MM speaker: text"), so triage, extraction and the checks need nothing of their own.
//
// Reading stays light (3.1): an agent session is read as the hub's folded page (fold_work=1), incrementally after
// the last seq read (after_seq): the user's and the agent's words, the cards and one line per stretch of work (calls,
// files, errors, tool names). Tool calls and results are only read for a segment a friction signal points at
// (Segment), never in bulk.
//
// Every source that is not there (no scope, the hub without the change, no copy, nothing in it) is left out and
// noted: the day runs on. The ledger of what was read goes to State.Sources, State.Cursors and State.ActiveDays at
// the end of each daily pass (Begin, End).
package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/convsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Kinds of the units this package adds. Each is read a day at a time with Day("<kind>/<id>", day).
const (
	KindAgents = "agents" // S3: id "<machine>/<session>"
	KindAsks   = "asks"   // S2: id "<day>"
	KindTodos  = "todos"  // S5: id the to-do's
	KindAbout  = "about"  // S7: id "me"
	KindMemory = "memory" // S6: id the file's name
)

// Kinds lists them.
var Kinds = []string{KindAgents, KindAsks, KindTodos, KindAbout, KindMemory}

// DayKind says whether a unit kind is one of this package's (read with Day("<kind>/<id>", day)).
func DayKind(kind string) bool { return slices.Contains(Kinds, kind) }

// Speakers written in the lines.
const (
	SpeakerUser    = "我"     // the user's own words (norm.Me)
	SpeakerAgent   = "Agent" // an agent's text
	SpeakerPersona = "Persona"
	SpeakerPaste   = "粘贴材料"
	SpeakerOther   = "其他会话"
	SpeakerWork    = "工作"
	SpeakerCard    = "卡片"
	SpeakerEnd     = "结局"
	SpeakerTodo    = "待办"
	SpeakerDecide  = "决定"
	SpeakerMemory  = "助手记忆"
)

// Limits on what a line keeps.
const (
	agentHead = 300
	agentTail = 200
	noticeCut = 200
)

// Source is a kbsrc.Source with the hub's other sources added.
type Source struct {
	kbsrc.Source
	Hub  hubapi.Hub
	Loc  *time.Location
	Logf func(format string, args ...any)
	// Machine names the machine an agent session runs on in its unit's name (machine id → name); optional.
	Machine map[string]string
	// Dump, when set, is given every unit day this package makes (replays keep them beside their reports).
	Dump func(ref, day string, lines []string)

	mu       sync.Mutex
	day      string // the day the pass reads (Begin)
	switches map[string]entry.SwitchSetting
	cursors  map[string]string // committed (State.Cursors)
	pending  map[string]string // advanced by this pass, committed at End
	status   map[string]*entry.SourceStatus
	counted  map[string]bool // "<code>|<ref>@<day>": a unit counted once per pass
	sessions map[string]hubapi.Session
	folds    map[string]*foldPage
	cards    map[string][]hubapi.Interaction // day → the cards of the day
	cardErr  map[string]error
	todos    map[string]hubapi.Todo
	decided  map[string][]hubapi.Decision
	about    string
	memories map[string]hubapi.MemoryFile
	private  map[string]bool // recordings marked private
	srcOf    map[string]string
	days     map[string]wire.KBDayResult
}

type foldPage struct {
	base  int // the after_seq it was read from
	items []hubapi.Item
}

// With adds the hub's other sources to src.
func With(src kbsrc.Source, hub hubapi.Hub, loc *time.Location) *Source {
	if loc == nil {
		loc = time.UTC
	}
	s := &Source{Source: src, Hub: hub, Loc: loc}
	s.reset()
	return s
}

func (s *Source) reset() {
	s.pending = map[string]string{}
	s.counted = map[string]bool{}
	s.sessions = map[string]hubapi.Session{}
	s.folds = map[string]*foldPage{}
	s.cards = map[string][]hubapi.Interaction{}
	s.cardErr = map[string]error{}
	s.todos = map[string]hubapi.Todo{}
	s.decided = map[string][]hubapi.Decision{}
	s.memories = map[string]hubapi.MemoryFile{}
	s.private = map[string]bool{}
	s.srcOf = map[string]string{}
	s.days = map[string]wire.KBDayResult{}
	if s.status == nil {
		s.status = map[string]*entry.SourceStatus{}
	}
	if s.cursors == nil {
		s.cursors = map[string]string{}
	}
}

func (s *Source) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// Begin starts a daily pass of day: what was read before (cursors), the switches, the sources card as it stands.
// The caller holds the store's lock.
func (s *Source) Begin(st *store.State, day string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reset()
	s.day = day
	s.switches = st.Switches
	s.cursors = map[string]string{}
	for k, v := range st.Cursors {
		s.cursors[k] = v
	}
	s.status = map[string]*entry.SourceStatus{}
	for k, v := range st.Sources {
		c := *v
		c.Units, c.Lines, c.Bytes = 0, 0, 0 // this pass's reading; End adds it to the totals
		s.status[k] = &c
	}
}

// End writes the pass's reading into the state: the sources card (availability, why not, volumes added), the
// cursors advanced, and day as an active day when any unit was read. The caller holds the store's lock.
func (s *Source) End(st *store.State, day string, units int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st.Sources == nil {
		st.Sources = map[string]*entry.SourceStatus{}
	}
	for k, v := range s.status {
		cur := st.Sources[k]
		if cur == nil {
			cur = &entry.SourceStatus{Code: v.Code, Name: v.Name, LearnPeople: true}
			st.Sources[k] = cur
		}
		units, lines, bytes := cur.Units+v.Units, cur.Lines+v.Lines, cur.Bytes+v.Bytes
		learn, sampled, right, unresolved := cur.LearnPeople, cur.Sampled, cur.Right, cur.Unresolved // P4's
		*cur = *v
		cur.Units, cur.Lines, cur.Bytes = units, lines, bytes
		cur.LearnPeople, cur.Sampled, cur.Right, cur.Unresolved = learn, sampled, right, unresolved
	}
	// Every source has a row, read or not: one never touched (no recordings, no to-dos yet) says it is empty.
	for _, b := range baseRows {
		if st.Sources[b[0]+"/"+b[1]] == nil {
			st.Sources[b[0]+"/"+b[1]] = &entry.SourceStatus{Code: b[0], Name: b[1], Missing: "empty", LearnPeople: true}
		}
	}
	if st.Cursors == nil {
		st.Cursors = map[string]string{}
	}
	for k, v := range s.pending {
		if later(v, st.Cursors[k]) {
			st.Cursors[k] = v
		}
	}
	if units > 0 && !slices.Contains(st.ActiveDays, day) {
		st.ActiveDays = append(st.ActiveDays, day)
		sort.Strings(st.ActiveDays)
	}
	s.cursors = map[string]string{}
	for k, v := range st.Cursors {
		s.cursors[k] = v
	}
	s.pending = map[string]string{}
}

// baseRows are the sources card's rows that are always there (S8 has a row per connected source as it is read).
var baseRows = [][2]string{
	{entry.SrcAssistant, "persona"}, {entry.SrcAsk, KindAsks}, {entry.SrcAgent, KindAgents}, {entry.SrcTools, "segments"},
	{entry.SrcTodo, KindTodos}, {entry.SrcMemory, KindMemory}, {entry.SrcAbout, KindAbout}, {entry.SrcRecording, "recordings"},
}

// Status is a copy of the ledger as it stands in this pass (for tests and the replay report).
func (s *Source) Status() map[string]entry.SourceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]entry.SourceStatus{}
	for k, v := range s.status {
		out[k] = *v
	}
	return out
}

// statusOf is the ledger row of a source (the caller holds mu).
func (s *Source) statusOf(code, name string) *entry.SourceStatus {
	k := code + "/" + name
	st := s.status[k]
	if st == nil {
		st = &entry.SourceStatus{Code: code, Name: name, LearnPeople: true}
		s.status[k] = st
	}
	return st
}

// note records whether a source could be read: err nil is there; otherwise why not.
func (s *Source) note(code, name string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.statusOf(code, name)
	st.LastRead = time.Now().UTC().Format(time.RFC3339)
	var w *hubapi.WaitingHub
	switch {
	case err == nil:
		st.Available, st.Missing, st.Waiting = true, "", ""
	case errors.As(err, &w):
		st.Waiting = w.Change
		if st.Units == 0 {
			st.Available, st.Missing = false, "waiting_hub"
		}
	case errors.Is(err, hubapi.ErrNoScope), errors.Is(err, convsrc.ErrNoAccess):
		st.Available, st.Missing, st.Waiting = false, "no_scope", ""
	case errors.Is(err, hubapi.ErrNotConnected):
		st.Available, st.Missing, st.Waiting = false, "not_connected", ""
	case errors.Is(err, hubapi.ErrOffline):
		st.Available, st.Missing, st.Waiting = false, "offline", "" // the assistant's computer is away: try again next run
	default:
		st.Available, st.Missing = false, "error"
		st.Note = err.Error()
	}
}

// partial records a source read short of what was asked (the cards without H3's paging): there, and waiting.
func (s *Source) partial(code, name, change, note string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.statusOf(code, name)
	st.Available, st.Missing, st.Waiting, st.Note = true, "", change, note
	st.LastRead = time.Now().UTC().Format(time.RFC3339)
}

// off records a source the user turned off.
func (s *Source) off(code, name, sw string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.statusOf(code, name)
	st.Available, st.Missing, st.Note = false, "off", "switch "+sw
}

// count adds a unit's reading to its source, once per unit and day in a pass.
func (s *Source) count(code, name, ref, day string, lines, bytes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := code + "|" + ref + "@" + day
	if s.counted[key] {
		return
	}
	s.counted[key] = true
	st := s.statusOf(code, name)
	st.Units++
	st.Lines += lines
	st.Bytes += bytes
	st.Available = true
	st.Missing = ""
}

// addBytes adds bytes read that belong to no unit yet (a fold page read for days to come).
func (s *Source) addBytes(code, name string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusOf(code, name).Bytes += n
}

func (s *Source) bounds(day string) (time.Time, time.Time, error) {
	start, err := time.ParseInLocation("2006-01-02", day, s.Loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return start, start.AddDate(0, 0, 1), nil
}

// Recent adds, to what the knowledge base and the conversations list, the units of this package active in
// [since, until): agent sessions, the day's cards, to-dos, and "about me" and memory files new or changed since
// last read. Recordings follow K11 and their private mark. Nothing here fails the day.
func (s *Source) Recent(ctx context.Context, since, until time.Time) ([]wire.KBEntry, error) {
	in, err := s.Source.Recent(ctx, since, until)
	if err != nil {
		return in, err
	}
	day := since.In(s.Loc).Format("2006-01-02")
	_, end, _ := s.bounds(day)
	out := s.filterRecordings(ctx, in)
	out = append(out, s.agentEntries(ctx, since, end)...)
	out = append(out, s.askEntries(ctx, day)...)
	out = append(out, s.todoEntries(ctx, since, end)...)
	out = append(out, s.aboutEntries(ctx, since)...)
	out = append(out, s.memoryEntries(ctx, since, end)...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// filterRecordings drops recordings when K11 is off and those marked private (the hub's meeting-private mark, when
// it ships: "private": true, or a "private" / "私密" tag).
func (s *Source) filterRecordings(ctx context.Context, in []wire.KBEntry) []wire.KBEntry {
	s.mu.Lock()
	on := entry.On(s.switches, entry.K11Recordings)
	s.mu.Unlock()
	out := in[:0:0]
	off := false
	for _, e := range in {
		if e.Kind != "recordings" {
			out = append(out, e)
			continue
		}
		if !on {
			off = true
			continue
		}
		if s.isPrivate(ctx, e) {
			continue
		}
		out = append(out, e)
	}
	if off {
		s.off(entry.SrcRecording, "recordings", entry.K11Recordings)
	}
	return out
}

func (s *Source) isPrivate(ctx context.Context, e wire.KBEntry) bool {
	for _, t := range e.Tags {
		if strings.EqualFold(t, "private") || t == "私密" {
			s.markPrivate(e.ID)
			return true
		}
	}
	d, err := s.Source.Get(ctx, e.Kind, e.ID)
	if err != nil {
		return false
	}
	priv := privateMeta(d.Meta)
	if priv {
		s.markPrivate(e.ID)
	}
	return priv
}

func privateMeta(raw json.RawMessage) bool {
	var m struct {
		Private bool     `json:"private"`
		Tags    []string `json:"tags"`
	}
	_ = json.Unmarshal(raw, &m)
	return m.Private || slices.ContainsFunc(m.Tags, func(t string) bool { return strings.EqualFold(t, "private") || t == "私密" })
}

func (s *Source) markPrivate(id string) {
	s.mu.Lock()
	s.private[id] = true
	s.mu.Unlock()
}

// Get answers for this package's units (their meta: the source code and origin, the name) and passes the rest on.
// A private recording is not given.
func (s *Source) Get(ctx context.Context, kind, id string) (wire.KBDetail, error) {
	if !DayKind(kind) {
		s.mu.Lock()
		priv := kind == "recordings" && s.private[id]
		s.mu.Unlock()
		if priv {
			return wire.KBDetail{}, kbsrc.ErrNotFound
		}
		d, err := s.Source.Get(ctx, kind, id)
		if err == nil && kind == "recordings" && privateMeta(d.Meta) {
			s.markPrivate(id)
			return wire.KBDetail{}, kbsrc.ErrNotFound
		}
		if err == nil {
			s.countGet(kind, id, d)
		}
		return d, err
	}
	meta := map[string]any{"id": id, "source": "persona", "kind": kind, "mine": true, "origin": string(entry.Behavior)}
	switch kind {
	case KindAgents:
		meta["code"] = entry.SrcAgent
		s.mu.Lock()
		se, ok := s.sessions[id]
		s.mu.Unlock()
		meta["name"] = "Agent 会话"
		if ok {
			meta["name"] = sessionName(se, s.Machine)
		}
	case KindAsks:
		meta["code"], meta["name"] = entry.SrcAsk, "卡片问答 · "+id
	case KindTodos:
		meta["code"] = entry.SrcTodo
		s.mu.Lock()
		t := s.todos[id]
		s.mu.Unlock()
		meta["name"] = "待办 · " + clip(t.Title, 30)
		if t.Project != "" {
			meta["projects"] = []string{t.Project}
		}
	case KindAbout:
		meta["code"], meta["origin"], meta["name"] = entry.SrcAbout, string(entry.SelfReport), "关于我"
	case KindMemory:
		meta["code"], meta["origin"], meta["name"] = entry.SrcMemory, string(entry.Handwritten), "助手记忆 · "+id
		s.mu.Lock()
		if m, ok := s.memories[id]; ok && m.Description != "" {
			meta["note"] = m.Description
		}
		s.mu.Unlock()
	}
	raw, _ := json.Marshal(meta)
	return wire.KBDetail{Kind: kind, ID: id, Meta: raw}, nil
}

// countGet counts a document, meeting, event or recording read whole (S8, S11), and remembers each chat's source.
func (s *Source) countGet(kind, id string, d wire.KBDetail) {
	m := kbsrc.DecodeMeta(d)
	if kind == "chats" {
		s.mu.Lock()
		s.srcOf[id] = m.Source
		s.mu.Unlock()
		return
	}
	text := strings.TrimSpace(d.Summary + "\n" + d.Recent)
	if text == "" {
		return
	}
	code, name := entry.SrcChat, m.Source
	if kind == "recordings" {
		code, name = entry.SrcRecording, "recordings"
	}
	if name == "" {
		name = kind
	}
	s.mu.Lock()
	day := s.day
	s.mu.Unlock()
	s.count(code, name, kind+"/"+id, day, strings.Count(text, "\n")+1, len(text))
}

// Day reads a unit's day: this package's ("<kind>/<id>") or the knowledge base's and the conversations' (counted as
// S1 and S8). A unit with nothing that day is kbsrc.ErrNotFound.
func (s *Source) Day(ctx context.Context, chat, day string) (wire.KBDayResult, error) {
	kind, id, _ := strings.Cut(chat, "/")
	if !DayKind(kind) {
		r, err := s.Source.Day(ctx, chat, day)
		if err == nil {
			s.countDay(chat, day, r)
		}
		return r, err
	}
	s.mu.Lock()
	r, ok := s.days[chat+"@"+day]
	s.mu.Unlock()
	if ok {
		if len(r.Messages) == 0 {
			return wire.KBDayResult{}, kbsrc.ErrNotFound
		}
		return r, nil
	}
	var lines []string
	var err error
	switch kind {
	case KindAgents:
		lines, err = s.agentDay(ctx, id, day)
	case KindAsks:
		lines, err = s.askDay(ctx, id, day)
	case KindTodos:
		lines, err = s.todoDay(ctx, id, day)
	case KindAbout:
		lines = s.aboutDay(day)
	case KindMemory:
		lines = s.memoryDay(id, day)
	}
	if err != nil {
		return wire.KBDayResult{}, err
	}
	r = wire.KBDayResult{Day: day, Messages: []wire.KBDayMessage{}}
	for _, l := range lines {
		r.Messages = append(r.Messages, wire.KBDayMessage{N: len(r.Messages) + 1, Text: l})
	}
	s.mu.Lock()
	s.days[chat+"@"+day] = r
	s.mu.Unlock()
	if s.Dump != nil && len(lines) > 0 {
		s.Dump(chat, day, lines)
	}
	if len(r.Messages) == 0 {
		return wire.KBDayResult{}, kbsrc.ErrNotFound
	}
	return r, nil
}

// countDay counts a conversation's day (S1) or a chat's (S8, by its source).
func (s *Source) countDay(chat, day string, r wire.KBDayResult) {
	n := 0
	for _, m := range r.Messages {
		n += len(m.Text)
	}
	if strings.HasPrefix(chat, convsrc.Prefix) {
		s.count(entry.SrcAssistant, "persona", "chats/"+chat, day, len(r.Messages), n)
		return
	}
	s.mu.Lock()
	src := s.srcOf[chat]
	s.mu.Unlock()
	if src == "" {
		src = "chats"
	}
	s.count(entry.SrcChat, src, "chats/"+chat, day, len(r.Messages), n)
}

func sessionName(se hubapi.Session, machines map[string]string) string {
	title := clip(strings.Join(strings.Fields(se.Title), " "), 30)
	if title == "" {
		title = se.ID
		if len(title) > 8 {
			title = title[:8]
		}
	}
	name := "Agent 会话 · " + title
	if m := machines[se.Machine]; m != "" {
		name += " @" + m
	}
	return name
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func headTail(s string, head, tail int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= head+tail {
		return string(r)
	}
	return strings.TrimSpace(string(r[:head])) + "……" + strings.TrimSpace(string(r[len(r)-tail:]))
}

// oneLine makes a text one line (a day's line is "HH:MM speaker: text", the text may not break it).
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func hhmm(t time.Time, loc *time.Location) string { return t.In(loc).Format("15:04") }

// cursor values: "<seq>|<day>" for an agent session (the last seq read and the day it was read through), a content
// hash for "about me" and a memory file. later says whether a is past b (a day-stamped cursor by its day, then seq).
func later(a, b string) bool {
	if b == "" {
		return true
	}
	sa, da, okA := strings.Cut(a, "|")
	sb, db, okB := strings.Cut(b, "|")
	if !okA || !okB {
		return a != b
	}
	if da != db {
		return da > db
	}
	var na, nb int
	fmt.Sscan(sa, &na)
	fmt.Sscan(sb, &nb)
	return na > nb
}

func cursorOf(v string) (int, string) {
	seq, day, ok := strings.Cut(v, "|")
	if !ok {
		return 0, ""
	}
	var n int
	fmt.Sscan(seq, &n)
	return n, day
}
