// Package store is the curator's own state, all of it in its data directory: the ledger of what was processed,
// the curated items, the graph, the memory, the proposals, the log of every write to the knowledge base, and spending.
//
// It is one JSON document written atomically (temp file, fsync, rename) after every step, plus one file per raw
// model answer under raw/. At the scale the curator works at (tens of thousands of items a year) that is a few
// megabytes; nothing in it is needed by anyone but the curator, and uninstalling removes it.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/memory"
)

// Evidence anchors a claim to one message line.
type Evidence struct {
	Chat    string `json:"chat"`
	Day     string `json:"day"`
	N       int    `json:"n"`
	Speaker string `json:"speaker,omitempty"`
	Quote   string `json:"quote"`
}

// Classes of items: the three kinds the curator organizes.
const (
	Fact      = "fact"      // 事实: dated, checkable statements about the world
	Practice  = "practice"  // 操作方式: how things are done
	Knowledge = "knowledge" // 知识: things that stay true and explain something
)

// Item is one curated fact, practice or piece of knowledge.
type Item struct {
	ID         string     `json:"id"` // content address: sha1 of class, scope, normalized quotes and claim
	Class      string     `json:"class"`
	Claim      string     `json:"claim"`
	About      string     `json:"about,omitempty"` // node id in the graph, or the name as written
	Scope      string     `json:"scope,omitempty"`
	When       string     `json:"when,omitempty"`
	Evidence   []Evidence `json:"evidence"`
	Confidence string     `json:"confidence"`
	Flags      []string   `json:"flags,omitempty"`
	// Status: facts candidate → active → stale | superseded; practices observed → recurring → established →
	// dormant; knowledge candidate → active → superseded; any → rejected (checks or the user), merged (into another).
	Status      string   `json:"status"`
	MergedInto  string   `json:"merged_into,omitempty"`
	DuplicateOf string   `json:"duplicate_of,omitempty"`
	FirstSeen   string   `json:"first_seen"`
	LastSeen    string   `json:"last_seen"`
	Days        []string `json:"days"`
	Weeks       []string `json:"weeks"`
	Sources     int      `json:"sources"` // independent sources: distinct (chat, speaker) pairs
	Pinned      bool     `json:"pinned,omitempty"`
	Verdict     string   `json:"verdict,omitempty"` // spot check: right | wrong
	Prompt      string   `json:"prompt"`
	Ruleset     string   `json:"ruleset"`
	Updated     string   `json:"updated"`
}

// Rejection is an answer item the checks threw away, kept with the reason so a rule change can be judged.
type Rejection struct {
	Key   string `json:"key"` // ledger key of the call
	Class string `json:"class"`
	Code  string `json:"code"`
	Claim string `json:"claim"`
	Day   string `json:"day"`
}

// Digest is the summary of one chat-day, every point with citations.
type Digest struct {
	Chat   string   `json:"chat"`
	Day    string   `json:"day"`
	Points []Point  `json:"points"`
	Items  []string `json:"items"`
	Key    string   `json:"key"`
}

// Point is a line of a digest.
type Point struct {
	Text     string     `json:"text"`
	Evidence []Evidence `json:"evidence"`
}

// Done is a ledger entry: one model call for one unit of work, keyed by content.
type Done struct {
	Key     string `json:"key"` // sha1(step, chat, day, window hash, prompt version)
	Step    string `json:"step"`
	Chat    string `json:"chat,omitempty"`
	Day     string `json:"day"`
	At      string `json:"at"`
	Raw     string `json:"raw"`             // file under raw/
	Shown   []int  `json:"shown,omitempty"` // the line numbers the model saw, for replaying the checks
	Passed  int    `json:"passed"`
	Flagged int    `json:"flagged"`
	Reject  int    `json:"rejected"`
	Prompt  string `json:"prompt"`
	Ruleset string `json:"ruleset"`
}

// TriageTag is the cached AI tag of a chat: reused while its signals do not change, for a week.
type TriageTag struct {
	Chat        string `json:"chat"`
	Fingerprint string `json:"fingerprint"`
	Admit       string `json:"admit"`
	Category    string `json:"category"`
	Relevance   int    `json:"relevance"`
	Reason      string `json:"reason"`
	At          string `json:"at"`
}

// Proposal is a change the curator wants to make, waiting for the user (or applied by itself, in which case
// it is listed as done without being asked: the "unacknowledged" list).
type Proposal struct {
	ID       string          `json:"id"`   // K-0001…
	Kind     string          `json:"kind"` // see curate.ProposalKinds
	Risk     string          `json:"risk"` // low | medium | high
	Title    string          `json:"title"`
	Detail   string          `json:"detail,omitempty"`
	Target   string          `json:"target,omitempty"` // "<kind>/<id>" in the knowledge base, or "graph/<node>"
	Change   json.RawMessage `json:"change"`           // the kb.update params, or a graph operation
	Items    []string        `json:"items,omitempty"`
	Evidence []Evidence      `json:"evidence,omitempty"`
	Key      string          `json:"key"` // content address: the same proposal is never made twice
	// Status: pending → accepted (applied) | rejected | expired | later (back to pending when shown again) |
	// auto (applied without asking) | blocked (needs a hub method that does not exist yet) | held (the user or
	// another extension holds a field it writes: kb.update said conflict) | undone
	Status   string `json:"status"`
	Verdict  string `json:"verdict,omitempty"` // shadow mode: right | wrong
	Created  string `json:"created"`
	Shown    string `json:"shown,omitempty"` // first shown: the expiry clock starts here, not at creation
	Decided  string `json:"decided,omitempty"`
	Acked    bool   `json:"acked,omitempty"`
	Shadow   bool   `json:"shadow,omitempty"`
	Needs    string `json:"needs,omitempty"` // the hub method it waits for (status blocked)
	ApplyLog string `json:"apply_log,omitempty"`
}

// Applied is one write to the knowledge base, with what was there before.
type Applied struct {
	ID       string          `json:"id"` // A-0001…
	Proposal string          `json:"proposal"`
	Run      string          `json:"run"`
	At       string          `json:"at"`
	Kind     string          `json:"kind"`
	Entity   string          `json:"entity"`
	Before   json.RawMessage `json:"before"`         // the fields the write touched, as they were
	After    json.RawMessage `json:"after"`          // as the curator wrote them
	Update   json.RawMessage `json:"update"`         // the kb.update params sent
	Free     json.RawMessage `json:"free,omitempty"` // field → nobody held it before: undo unlocks it
	Undone   string          `json:"undone,omitempty"`
	StateSum string          `json:"state_sum"` // hash of the touched fields after the write, checked before undo
}

// Run is one daily or weekly run.
type Run struct {
	ID       string         `json:"id"`
	Kind     string         `json:"kind"` // daily | weekly | health
	Period   string         `json:"period"`
	Started  string         `json:"started"`
	Finished string         `json:"finished,omitempty"`
	DryRun   bool           `json:"dry_run,omitempty"`
	Counts   map[string]int `json:"counts"`
	Tokens   int            `json:"tokens"`
	Notes    []string       `json:"notes,omitempty"`
	Report   string         `json:"report"` // markdown
}

// Usage is tokens by day and step.
type Usage struct {
	Day   string `json:"day"`
	Step  string `json:"step"`
	In    int    `json:"in"`
	Out   int    `json:"out"`
	Calls int    `json:"calls"`
}

// State is everything.
type State struct {
	Version     int                   `json:"version"`
	Installed   string                `json:"installed"`
	Done        map[string]*Done      `json:"done"`
	Items       map[string]*Item      `json:"items"`
	Rejections  []Rejection           `json:"rejections"`
	Digests     map[string]*Digest    `json:"digests"` // "<chat>/<day>"
	Triage      map[string]*TriageTag `json:"triage"`
	Proposals   []*Proposal           `json:"proposals"`
	Applied     []*Applied            `json:"applied"`
	Runs        []*Run                `json:"runs"`
	Usage       []*Usage              `json:"usage"`
	Graph       *Graph                `json:"graph"`
	Funnel      map[string]Funnel     `json:"funnel"`  // day → triage funnel
	Samples     map[string][]string   `json:"samples"` // ISO week → items picked for the spot check
	Memory      *memory.Book          `json:"memory"`  // the user's memory (design chapter 6): conclusions and facts with their evidence
	NextProp    int                   `json:"next_proposal"`
	NextApplied int                   `json:"next_applied"`
}

// Funnel is how a day's chats were sorted, for the report and the health page.
type Funnel struct {
	Chats    map[string]int `json:"chats"`  // decision → chat-days
	Tokens   map[string]int `json:"tokens"` // decision → estimated tokens
	Admitted int            `json:"admitted"`
	Sources  map[string]int `json:"sources"` // source → chat-days seen, for the report
}

// Store is the state in a directory.
type Store struct {
	Dir string
	mu  sync.Mutex
	S   *State
	Now func() time.Time
}

// Open loads (or starts) the state in dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "raw"), 0o700); err != nil {
		return nil, err
	}
	st := &Store{Dir: dir, Now: time.Now}
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		st.S = &State{Version: 1}
	case err != nil:
		return nil, err
	default:
		st.S = &State{}
		if err := json.Unmarshal(raw, st.S); err != nil {
			return nil, err
		}
	}
	st.S.init(st.Now())
	return st, nil
}

func (s *State) init(now time.Time) {
	if s.Installed == "" {
		s.Installed = now.UTC().Format(time.RFC3339)
	}
	if s.Done == nil {
		s.Done = map[string]*Done{}
	}
	if s.Items == nil {
		s.Items = map[string]*Item{}
	}
	if s.Digests == nil {
		s.Digests = map[string]*Digest{}
	}
	if s.Triage == nil {
		s.Triage = map[string]*TriageTag{}
	}
	if s.Samples == nil {
		s.Samples = map[string][]string{}
	}
	if s.Funnel == nil {
		s.Funnel = map[string]Funnel{}
	}
	if s.Graph == nil {
		s.Graph = &Graph{}
	}
	if s.Memory == nil {
		s.Memory = memory.NewBook()
	}
	s.Memory.Init()
	s.Graph.init()
}

// Lock and Unlock guard the state: the scheduler, the UI and event handlers share it.
func (s *Store) Lock()   { s.mu.Lock() }
func (s *Store) Unlock() { s.mu.Unlock() }

// Save writes the state atomically. Call it with the lock held.
func (s *Store) Save() error {
	b, err := json.Marshal(s.S)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.Dir, "state.json"), b)
}

// PutRaw keeps a model's raw answer, for replay when the checks change.
func (s *Store) PutRaw(key string, b []byte) (string, error) {
	name := key + ".json"
	return name, writeAtomic(filepath.Join(s.Dir, "raw", name), b)
}

// Raw reads a kept answer.
func (s *Store) Raw(name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.Dir, "raw", filepath.Base(name)))
}

func writeAtomic(path string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Record keeps token usage by day and step (model.Usage). It takes no lock: runs hold the store's.
func (s *Store) Record(day, step string, in, out int) {
	for _, u := range s.S.Usage {
		if u.Day == day && u.Step == step {
			u.In += in
			u.Out += out
			u.Calls++
			return
		}
	}
	s.S.Usage = append(s.S.Usage, &Usage{Day: day, Step: step, In: in, Out: out, Calls: 1})
}

// ItemsBy returns items in a stable order (by last seen, newest first).
func (s *State) ItemsBy(class string, keep func(*Item) bool) []*Item {
	var out []*Item
	for _, it := range s.Items {
		if (class == "" || it.Class == class) && (keep == nil || keep(it)) {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastSeen != out[j].LastSeen {
			return out[i].LastSeen > out[j].LastSeen
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Proposal finds one by id.
func (s *State) Proposal(id string) *Proposal {
	for _, p := range s.Proposals {
		if p.ID == id {
			return p
		}
	}
	return nil
}
