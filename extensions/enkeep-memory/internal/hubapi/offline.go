package hubapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Offline answers like the hub from local copies, for replays: other agents' sessions from Claude Code transcripts
// (a directory of project directories of *.jsonl, only ever read), ask_user cards from the ask tools in them, the
// hand-written memory from a directory of Markdown files, "about me" from a file and to-dos from a JSON file. What
// has no copy returns ErrNotConnected, as a hub without the scope would leave it out.
//
// Items are made as the hub's node makes them: the user's words, the agent's text, tool calls and results,
// thoughts; with Fold, every run of tool calls, results and thoughts is one fold with its call count, the files
// changed, and (simulating H3, unless FoldUnknown) its error count and tool names.
type Offline struct {
	AgentsDir string // e.g. ~/.claude/projects
	// AssistantDirs are the project directories (base names) of the assistant's own sessions: listed with
	// Assistant set (their words are S1, read through convsrc; only a dug segment of their tools is S4).
	AssistantDirs []string
	MemoryDir     string
	AboutFile     string
	TodosFile     string // {"todos": [Todo…], "decisions": {"<id>": [Decision…]}}
	Machine       string // the machine id sessions are listed under (default "local")
	// Since: transcripts last written before it are not opened (a replay's first day).
	Since time.Time
	// FoldUnknown simulates a hub without H3: folds carry no error count or tool names.
	FoldUnknown bool
	// HubNow answers as today's hub does, before H2 and H3: no memory (waiting for H2), the cards only the newest 500
	// with a range asked answered partially (waiting for H3), folds without error counts or tool names.
	HubNow bool

	mu       sync.Mutex
	relayed  []string               // what the assistant sent agents (agent_start, agent_send, task_plan), as keys
	index    map[string]*offSession // session id → its file and what the list shows
	cards    []Interaction
	cached   string // the session whose items are held
	items    []Item
	notified []string
}

type offSession struct {
	Session
	path  string
	bytes int64
}

func (o *Offline) machine() string {
	if o.Machine == "" {
		return "local"
	}
	return o.Machine
}

// Notified is what Notify was asked to send (offline sends nothing).
func (o *Offline) Notified() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.notified...)
}

// TranscriptBytes is the size on disk of the transcripts of the sessions listed with activity in [since, until),
// the assistant's included or not: what reading every session whole would read.
func (o *Offline) TranscriptBytes(since, until time.Time, assistant bool) int64 {
	if o.load() != nil {
		return 0
	}
	var n int64
	for _, s := range o.index {
		if s.Assistant != assistant || s.Updated.Before(since) || (!until.IsZero() && !s.Started.Before(until)) {
			continue
		}
		n += s.bytes
	}
	return n
}

// load indexes the transcripts once: each session's times, title and directory, and the ask cards in it.
func (o *Offline) load() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.index != nil {
		return nil
	}
	if o.AgentsDir == "" {
		return ErrNotConnected
	}
	o.index = map[string]*offSession{}
	dirs, err := os.ReadDir(o.AgentsDir)
	if err != nil {
		return err
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		assistant := false
		for _, a := range o.AssistantDirs {
			if a == d.Name() {
				assistant = true
			}
		}
		files, _ := os.ReadDir(filepath.Join(o.AgentsDir, d.Name()))
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue // subagents' transcripts live in directories: their work shows in the parent's
			}
			info, err := f.Info()
			if err != nil || (!o.Since.IsZero() && info.ModTime().Before(o.Since)) {
				continue
			}
			path := filepath.Join(o.AgentsDir, d.Name(), f.Name())
			if assistant {
				o.relayed = append(o.relayed, relays(path)...)
			}
			s, cards, err := scanTranscript(path)
			if err != nil || s == nil {
				continue
			}
			s.Machine, s.Assistant, s.Project = o.machine(), assistant, d.Name()
			for i := range cards {
				cards[i].Machine = o.machine()
				cards[i].Title = s.Title
			}
			if cur := o.index[s.ID]; cur != nil { // one session in two files: keep the longer span
				if s.Started.After(cur.Started) {
					s.Started = cur.Started
				}
				if s.Updated.Before(cur.Updated) {
					s.Updated = cur.Updated
				}
			}
			o.index[s.ID] = &offSession{Session: *s, path: path, bytes: info.Size()}
			o.cards = append(o.cards, cards...)
		}
	}
	sort.SliceStable(o.cards, func(i, j int) bool { return o.cards[i].At.Before(o.cards[j].At) })
	return nil
}

// Sessions lists the sessions with activity since since.
func (o *Offline) Sessions(_ context.Context, since time.Time) ([]Session, error) {
	if err := o.load(); err != nil {
		return nil, err
	}
	var out []Session
	for _, s := range o.index {
		if !s.Updated.Before(since) {
			out = append(out, s.Session)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Updated.Equal(out[j].Updated) {
			return out[i].Updated.Before(out[j].Updated)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Items answers as the hub's items route: folded and after a seq, or a segment unfolded.
func (o *Offline) Items(_ context.Context, machine, session string, q ItemsQuery) ([]Item, error) {
	if err := o.load(); err != nil {
		return nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.index[session]
	if s == nil || machine != o.machine() {
		return nil, nil
	}
	if o.cached != session {
		items, err := readItems(s.path, o.relayed)
		if err != nil {
			return nil, err
		}
		o.cached, o.items = session, items
	}
	var sel []Item
	for _, it := range o.items {
		if q.Through > 0 {
			if it.Seq >= q.From && it.Seq <= q.Through {
				sel = append(sel, it)
			}
		} else if it.Seq > q.After {
			sel = append(sel, it)
		}
	}
	if q.Fold && q.Through == 0 {
		sel = FoldWork(sel, !o.FoldUnknown && !o.HubNow)
	}
	if q.Limit > 0 && len(sel) > q.Limit {
		sel = sel[:q.Limit]
	}
	return sel, nil
}

// Interactions are the ask tools' questions and answers in the transcripts.
func (o *Offline) Interactions(_ context.Context, q InteractionsQuery) ([]Interaction, error) {
	if err := o.load(); err != nil {
		return nil, err
	}
	cards := o.cards
	if o.HubNow && len(cards) > 500 {
		cards = cards[len(cards)-500:] // the newest 500, as the history route gives without paging
	}
	var out []Interaction
	for _, c := range cards {
		if (!q.Since.IsZero() && c.At.Before(q.Since)) || (!q.Before.IsZero() && !c.At.Before(q.Before)) ||
			(q.Kind != "" && c.Kind != q.Kind) || (q.Session != "" && c.Session != q.Session) || c.Private {
			continue
		}
		out = append(out, c)
	}
	if o.HubNow && (!q.Since.IsZero() || !q.Before.IsZero()) {
		return out, &WaitingHub{Change: "H3"}
	}
	return out, nil
}

type todosFile struct {
	Todos     []Todo                `json:"todos"`
	Decisions map[string][]Decision `json:"decisions"`
}

func (o *Offline) todos() (*todosFile, error) {
	if o.TodosFile == "" {
		return nil, ErrNotConnected
	}
	raw, err := os.ReadFile(o.TodosFile)
	if err != nil {
		return nil, err
	}
	var f todosFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", o.TodosFile, err)
	}
	return &f, nil
}

func (o *Offline) Todos(_ context.Context, since time.Time) ([]Todo, error) {
	f, err := o.todos()
	if err != nil {
		return nil, err
	}
	var out []Todo
	for _, t := range f.Todos {
		if !t.Updated.Before(since) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (o *Offline) TodoDecisions(_ context.Context, id string) ([]Decision, error) {
	f, err := o.todos()
	if err != nil {
		return nil, err
	}
	return f.Decisions[id], nil
}

func (o *Offline) AboutMe(context.Context) (string, error) {
	if o.AboutFile == "" {
		return "", ErrNotConnected
	}
	raw, err := os.ReadFile(o.AboutFile)
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(raw), err
}

// AboutMeTime is when the "about me" copy was last written (zero without one): offline, the day it is read on.
func (o *Offline) AboutMeTime() time.Time {
	if o.AboutFile == "" {
		return time.Time{}
	}
	info, err := os.Stat(o.AboutFile)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// Memories reads the hand-written memory directory as H2 will give it: name, the frontmatter's description and type,
// created (the file's birth time where the system has one) and modified times, size, and the body when asked. The
// index file MEMORY.md is a list of pointers, not a memory, and is left out. No directory: an empty list.
func (o *Offline) Memories(_ context.Context, content bool) ([]MemoryFile, error) {
	if o.HubNow {
		return nil, &WaitingHub{Change: "H2"}
	}
	if o.MemoryDir == "" {
		return nil, ErrNotConnected
	}
	des, err := os.ReadDir(o.MemoryDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []MemoryFile
	for _, d := range des {
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") || strings.EqualFold(d.Name(), "MEMORY.md") {
			continue
		}
		path := filepath.Join(o.MemoryDir, d.Name())
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		m := ParseMemory(d.Name(), raw)
		m.Created, m.Modified, m.Size = birthTime(info), info.ModTime(), int(info.Size())
		if !content {
			m.Content = ""
		}
		out = append(out, m)
	}
	return out, nil
}

// ParseMemory reads a memory file: its frontmatter's description and type (metadata.type or type) and the body.
// Without frontmatter both are empty and the whole file is the body.
func ParseMemory(name string, raw []byte) MemoryFile {
	m := MemoryFile{Name: name}
	text := string(raw)
	body := text
	if strings.HasPrefix(text, "---\n") {
		if end := strings.Index(text[4:], "\n---"); end >= 0 {
			front := text[4 : 4+end]
			body = strings.TrimLeft(text[4+end+4:], "\n")
			for _, l := range strings.Split(front, "\n") {
				k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
				if !ok {
					continue
				}
				v = strings.Trim(strings.TrimSpace(v), `"'`)
				switch strings.TrimSpace(k) {
				case "description":
					m.Description = v
				case "type":
					m.Type = v
				}
			}
		}
	}
	m.Content = body
	return m
}

func (o *Offline) Adoptions(context.Context) ([]Adoption, error) {
	return nil, &WaitingHub{Change: "H1"}
}

func (o *Offline) Notify(_ context.Context, title, body string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.notified = append(o.notified, title+"\n"+body)
	return nil
}

var _ Hub = (*Offline)(nil)

// FoldWork folds every run of tool calls, results and thoughts into one fold item, as the hub's fold_work=1 does:
// the fold's seq is the run's first, through_seq its last; Calls counts the tool calls, Files the files an edit or
// write touched; with known, Errors counts the failed results and Tools lists the tool names (≤10, in order).
func FoldWork(items []Item, known bool) []Item {
	var out []Item
	var run []Item
	flush := func() {
		if len(run) == 0 {
			return
		}
		f := &Fold{From: run[0].Seq, To: run[len(run)-1].Seq, Known: known}
		seenFile, seenTool := map[string]bool{}, map[string]bool{}
		for _, it := range run {
			switch it.Kind {
			case KindToolUse:
				f.Calls++
				if p := editedFile(it.Tool, it.Text); p != "" && !seenFile[p] {
					seenFile[p] = true
					f.Files = append(f.Files, p)
				}
				if known && it.Tool != "" && !seenTool[it.Tool] && len(f.Tools) < 10 {
					seenTool[it.Tool] = true
					f.Tools = append(f.Tools, it.Tool)
				}
			case KindToolResult:
				if known && it.Err {
					f.Errors++
				}
			}
		}
		out = append(out, Item{ID: "fold:" + run[0].ID, Seq: run[0].Seq, At: run[0].At, Kind: KindFold, Fold: f})
		run = nil
	}
	for _, it := range items {
		switch it.Kind {
		case KindToolUse, KindToolResult, KindThinking:
			run = append(run, it)
		default:
			flush()
			out = append(out, it)
		}
	}
	flush()
	return out
}

// editedFile is the file a tool call writes (Edit, Write, MultiEdit, NotebookEdit and the like), or "".
func editedFile(tool, input string) string {
	t := strings.ToLower(tool)
	if !strings.Contains(t, "edit") && !strings.Contains(t, "write") {
		return ""
	}
	var in struct {
		Path     string `json:"file_path"`
		Notebook string `json:"notebook_path"`
	}
	if json.Unmarshal([]byte(input), &in) != nil {
		return ""
	}
	if in.Path != "" {
		return in.Path
	}
	return in.Notebook
}

// The transcript records read here.
type tRecord struct {
	Type        string    `json:"type"`
	Timestamp   time.Time `json:"timestamp"`
	SessionID   string    `json:"sessionId"`
	IsMeta      bool      `json:"isMeta"`
	IsSidechain bool      `json:"isSidechain"`
	AITitle     string    `json:"aiTitle"`
	Cwd         string    `json:"cwd"`
	Origin      *struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

type tBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

const maxRecord = 64 << 20

func eachRecord(path string, fn func(tRecord)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), maxRecord)
	for sc.Scan() {
		line := sc.Bytes()
		// Only messages and titles are read: bookkeeping records without them are skipped undecoded.
		if !bytes.Contains(line, []byte(`"type":"user"`)) && !bytes.Contains(line, []byte(`"type":"assistant"`)) &&
			!bytes.Contains(line, []byte(`"type":"ai-title"`)) {
			continue
		}
		var r tRecord
		if json.Unmarshal(line, &r) != nil {
			continue
		}
		fn(r)
	}
	return sc.Err()
}

func blocks(raw json.RawMessage) []tBlock {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []tBlock{{Type: "text", Text: s}}
	}
	var bs []tBlock
	_ = json.Unmarshal(raw, &bs)
	return bs
}

// resultText is a tool result's text (a string, or its text blocks).
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []string
	for _, b := range blocks(raw) {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// scanTranscript reads a transcript's list entry and its ask cards.
func scanTranscript(path string) (*Session, []Interaction, error) {
	s := &Session{ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"), Agent: "claude"}
	askQs := map[string][]Interaction{} // ask tool_use id → its questions, until the result answers them
	var cards []Interaction
	firstUser := true
	err := eachRecord(path, func(r tRecord) {
		if r.Type == "ai-title" && r.AITitle != "" {
			s.Title = r.AITitle
			return
		}
		if r.IsSidechain || r.Timestamp.IsZero() || r.Message == nil {
			return
		}
		if r.SessionID != "" {
			s.ID = r.SessionID
		}
		if s.Started.IsZero() || r.Timestamp.Before(s.Started) {
			s.Started = r.Timestamp
		}
		if r.Timestamp.After(s.Updated) {
			s.Updated = r.Timestamp
		}
		if s.Cwd == "" {
			s.Cwd = r.Cwd
		}
		for _, b := range blocks(r.Message.Content) {
			switch {
			case r.Type == "user" && b.Type == "text" && firstUser && !r.IsMeta:
				firstUser = false
				if _, notice := UserWords(b.Text, originKind(r)); notice == "persona" {
					s.ByPersona = true
				}
			case r.Type == "assistant" && b.Type == "tool_use" && IsAskTool(b.Name):
				qs := askQuestions(b.Input)
				for i := range qs {
					qs[i].ID = fmt.Sprintf("%s#%d", b.ID, i)
					qs[i].Session, qs[i].At = s.ID, r.Timestamp
				}
				askQs[b.ID] = qs
			case r.Type == "user" && b.Type == "tool_result" && askQs[b.ToolUseID] != nil:
				qs := askQs[b.ToolUseID]
				delete(askQs, b.ToolUseID)
				answerAsk(qs, b, r.ToolUseResult)
				cards = append(cards, qs...)
			}
		}
	})
	if err != nil {
		return nil, nil, err
	}
	if s.Updated.IsZero() {
		return nil, nil, nil
	}
	if s.Title == "" {
		s.Title = filepath.Base(s.Cwd)
	}
	return s, cards, nil
}

func originKind(r tRecord) string {
	if r.Origin == nil {
		return ""
	}
	return r.Origin.Kind
}

// IsAskTool says whether a tool asks the user a question with options (Claude Code's AskUserQuestion, Persona's
// ask_user MCP tool under any server name).
func IsAskTool(name string) bool {
	return name == "AskUserQuestion" || strings.HasSuffix(name, "__ask_user") || name == "ask_user"
}

func askQuestions(input json.RawMessage) []Interaction {
	// The tool's own arguments carry what the hub keeps on the card (hub #5876): the deadline, whether it can be
	// undone, and each question's recommended option. Absent stays unknown.
	var in struct {
		Deadline   string `json:"deadline"`
		Reversible *bool  `json:"reversible"`
		Questions  []struct {
			Question  string `json:"question"`
			Recommend string `json:"recommend"`
			Options   []struct {
				Label string `json:"label"`
			} `json:"options"`
		} `json:"questions"`
	}
	if json.Unmarshal(input, &in) != nil {
		return nil
	}
	var out []Interaction
	for _, q := range in.Questions {
		x := Interaction{Kind: "question", Tool: "ask_user", Question: q.Question, Reversible: in.Reversible}
		if t, err := time.Parse(time.RFC3339, in.Deadline); err == nil {
			x.Deadline = t
		}
		for _, o := range q.Options {
			x.Options = append(x.Options, o.Label)
			if q.Recommend != "" && o.Label == q.Recommend {
				x.Recommend = q.Recommend // only an option's label counts, as the hub keeps it
			}
			if x.Recommend == "" && strings.HasSuffix(o.Label, " (Recommended)") {
				// Claude Code's AskUserQuestion marks its recommended option this way (the tool's own convention).
				x.Recommend = o.Label
			}
		}
		out = append(out, x)
	}
	return out
}

// answeredLine is one answer in Persona's ask_user result: "- <question> → <answer>".
var answeredLine = regexp.MustCompile(`(?m)^- (.*) → (.*)$`)

// answerAsk fills the answers of an ask tool's questions from its result: AskUserQuestion's toolUseResult.answers
// (question → answer), or ask_user's "The user answered:" lines. A dismissed or timed-out card has no answer.
func answerAsk(qs []Interaction, b tBlock, tur json.RawMessage) {
	var r struct {
		Answers map[string]string `json:"answers"`
	}
	text := resultText(b.Content)
	_ = json.Unmarshal(tur, &r)
	lines := answeredLine.FindAllStringSubmatch(text, -1)
	for i := range qs {
		q := &qs[i]
		switch {
		case b.IsError:
			q.Status = "cancelled"
		case r.Answers[q.Question] != "":
			q.Answers, q.AnsweredBy, q.Status = []string{r.Answers[q.Question]}, "user", "resolved"
		default:
			for _, l := range lines {
				if strings.TrimSpace(l[1]) == strings.TrimSpace(q.Question) || (len(lines) == len(qs) && lines[i][1] == l[1]) {
					q.Answers, q.AnsweredBy, q.Status = []string{strings.TrimSpace(l[2])}, "user", "resolved"
					break
				}
			}
			if q.Status == "" {
				q.Status = "dismissed"
				if strings.Contains(text, "did not answer") {
					q.Reason = "dismissed or timed out"
				}
			}
		}
	}
}

// relayKeyRunes is how much of a relayed text identifies it.
const relayKeyRunes = 60

func relayKey(text string) string {
	r := []rune(strings.Join(strings.Fields(text), " "))
	if len(r) > relayKeyRunes {
		r = r[:relayKeyRunes]
	}
	return string(r)
}

// relays are the texts the assistant sent agents in a transcript (its agent_start prompts, agent_send texts and
// task_plan tasks), as keys: an agent's transcript shows them as the user's messages, without a mark. The hub marks
// what it injects (Item.injected, source); offline, a user message that carries one of these is Persona's, not
// the user's.
func relays(path string) []string {
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			if len([]rune(x)) >= 40 {
				out = append(out, relayKey(x))
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for k, e := range x {
				if k != "cwd" && k != "machine" && k != "session_id" && k != "worktree" && k != "launcher" {
					walk(e)
				}
			}
		}
	}
	_ = eachRecord(path, func(r tRecord) {
		if r.Type != "assistant" || r.Message == nil {
			return
		}
		for _, b := range blocks(r.Message.Content) {
			if b.Type != "tool_use" || !(strings.HasSuffix(b.Name, "agent_start") || strings.HasSuffix(b.Name, "agent_send") ||
				strings.HasSuffix(b.Name, "task_plan")) {
				continue
			}
			var in any
			if json.Unmarshal(b.Input, &in) == nil {
				walk(in)
			}
		}
	})
	return out
}

// isRelayed says whether a user message carries a text the assistant sent.
func isRelayed(text string, relayed []string) bool {
	if len(relayed) == 0 {
		return false
	}
	head := []rune(strings.Join(strings.Fields(text), " "))
	if len(head) > 2000 {
		head = head[:2000]
	}
	h := string(head)
	for _, k := range relayed {
		if strings.Contains(h, k) {
			return true
		}
	}
	return false
}

// readItems makes a transcript's items, numbered in order. A user message the assistant relayed is a notice.
func readItems(path string, relayed []string) ([]Item, error) {
	var out []Item
	tools := map[string]string{} // tool_use id → name
	seq := 0
	add := func(it Item) {
		seq++
		it.Seq = seq
		if it.ID == "" {
			it.ID = fmt.Sprintf("i%d", seq)
		}
		out = append(out, it)
	}
	err := eachRecord(path, func(r tRecord) {
		if r.IsSidechain || r.Timestamp.IsZero() || r.Message == nil || (r.Type != "user" && r.Type != "assistant") {
			return
		}
		for _, b := range blocks(r.Message.Content) {
			switch {
			case r.Type == "user" && b.Type == "text":
				if r.IsMeta {
					continue
				}
				text, notice := UserWords(b.Text, originKind(r))
				if text == "" {
					continue
				}
				if notice == "" && isRelayed(text, relayed) {
					notice, text = "persona", headTail(text, 600, 200)
				}
				kind := KindUser
				if notice != "" {
					kind = KindNotice
				}
				add(Item{At: r.Timestamp, Kind: kind, Text: text, Notice: notice})
			case r.Type == "user" && b.Type == "tool_result":
				add(Item{ID: "r-" + b.ToolUseID, At: r.Timestamp, Kind: KindToolResult, Tool: tools[b.ToolUseID],
					Text: resultText(b.Content), Err: b.IsError, Call: b.ToolUseID})
			case r.Type == "assistant" && b.Type == "text":
				if t := strings.TrimSpace(b.Text); t != "" {
					add(Item{At: r.Timestamp, Kind: KindAgent, Text: t})
				}
			case r.Type == "assistant" && b.Type == "thinking":
				add(Item{At: r.Timestamp, Kind: KindThinking, Text: b.Thinking})
			case r.Type == "assistant" && b.Type == "tool_use":
				tools[b.ID] = b.Name
				add(Item{ID: b.ID, At: r.Timestamp, Kind: KindToolUse, Tool: b.Name, Text: string(b.Input), Call: b.ID})
			}
		}
	})
	return out, err
}

var (
	// Blocks the hub or the agent put into a user message: removed, the rest kept.
	injectedBlock = regexp.MustCompile(`(?s)<(jarvis-context|persona-context|system-reminder)\b[^>]*>.*?</(jarvis-context|persona-context|system-reminder)>`)
	pasteBlock    = regexp.MustCompile(`(?s)<pasted_content\b[^>]*>(.*?)</pasted_content[^>]*>`)
	wrappers      = regexp.MustCompile(`<(command-name|command-message|command-args|local-command-stdout|local-command-stderr|task-notification|persona-supervision)\b`)
	interruptMark = regexp.MustCompile(`\[Request interrupted by user[^\]]*\]`)
	personaMarks  = []string{"<persona-context", "(Persona: this task was handed", "这是 Persona 的任务通知", "这是 Persona 的督办消息",
		"这是 Persona 的收件箱通知", "不是用户发的消息", "not a message from the user", "（Persona 叫醒你", "（Persona 叫你接着做）",
		// How Persona passes a card's answer back to the agent (prompts agent_answer): its wording, not the user's.
		"关于你上面的问题，"}
)

// UserWords is what a user-role text of a transcript says, and whose it is: notice "" when it is the user's own words;
// "persona" when Persona wrote it (a task handed over, supervision, a notice; it has a Persona mark); "paste" when
// all of it is pasted material; "command" for a slash command or its output; "notification" for a task
// notification. Context blocks are removed; pasted material is replaced by a mark with its size.
func UserWords(text, origin string) (string, string) {
	if origin == "task-notification" || strings.Contains(text, "<task-notification") {
		return headTail(text, 200, 0), "notification"
	}
	if origin == "peer" {
		return headTail(text, 300, 0), "peer"
	}
	for _, m := range personaMarks {
		if strings.Contains(text, m) {
			return headTail(stripTags(text), 600, 200), "persona"
		}
	}
	if wrappers.MatchString(text) {
		return "", "command"
	}
	text = injectedBlock.ReplaceAllString(text, "")
	text = interruptMark.ReplaceAllString(text, "")
	pasted := 0
	text = pasteBlock.ReplaceAllStringFunc(text, func(m string) string {
		n := len([]rune(pasteBlock.FindStringSubmatch(m)[1]))
		pasted += n
		return fmt.Sprintf("[粘贴材料 %d 字]", n)
	})
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "command"
	}
	if pasted > 0 && strings.TrimSpace(pasteMark.ReplaceAllString(text, "")) == "" {
		return text, "paste"
	}
	if n := len([]rune(text)); n > 2000 {
		text = string([]rune(text)[:600]) + fmt.Sprintf("…[疑似粘贴材料，略去 %d 字]…", n-600)
	}
	return text, ""
}

var pasteMark = regexp.MustCompile(`\[粘贴材料 \d+ 字\]`)

func stripTags(s string) string {
	return strings.TrimSpace(regexp.MustCompile(`</?[a-z_-]+\b[^>]*>`).ReplaceAllString(s, ""))
}

func headTail(s string, head, tail int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= head+tail {
		return string(r)
	}
	if tail == 0 {
		return string(r[:head]) + "……"
	}
	return string(r[:head]) + "……" + string(r[len(r)-tail:])
}

// Hash is a short content hash (cursors of catalog sources: "about me", memory files).
func Hash(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:8])
}
