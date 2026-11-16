// Package store is the curator's own state, all of it in its data directory: the ledger of what was processed,
// the curated items, the graph, the memory, the proposals, the log of every write to the knowledge base, and spending.
//
// It is one JSON document written atomically (temp file, fsync, rename) after every step, plus write-ahead records
// for in-flight proposal writes and one file per raw model answer under raw/. At the scale the curator works at (tens of thousands of items a year) that is a few
// megabytes; nothing in it is needed by anyone but the curator, and uninstalling removes it.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/people"
)

// Evidence anchors a claim to one message line.
type Evidence struct {
	Chat    string `json:"chat"`
	Day     string `json:"day"`
	N       int    `json:"n"`
	Speaker string `json:"speaker,omitempty"`
	// Role is who the line is by (voice.Role*): user, assistant, agent_draft, bot or other. Only the user's and other
	// people's lines are kept as what something rests on; empty in state written before roles were recorded.
	Role  string `json:"role,omitempty"`
	Quote string `json:"quote"`
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
	Why        string     `json:"why,omitempty"`    // the owner's reason, when they gave one
	Kind       string     `json:"kind,omitempty"`   // facts: decision | commitment | status | evaluation | dependency
	Impact     string     `json:"impact,omitempty"` // facts: what the assistant does differently for knowing it
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
	Sources     int      `json:"sources"` // independent sources: distinct chats, one quote in several chats counting once
	Pinned      bool     `json:"pinned,omitempty"`
	Verdict     string   `json:"verdict,omitempty"` // spot check: right | wrong
	Prompt      string   `json:"prompt"`
	Ruleset     string   `json:"ruleset"`
	Updated     string   `json:"updated"`
}

// Judgement is one situation and the owner's choice in it (daily/7), resting only on the owner's own lines.
type Judgement struct {
	ID         string     `json:"id"` // content address: sha1 of chat, day, the choice and the quotes
	Claim      string     `json:"claim"`
	Trigger    string     `json:"trigger"`
	Choice     string     `json:"choice"`
	Rejected   string     `json:"rejected,omitempty"`
	Why        string     `json:"why,omitempty"`
	Dimension  string     `json:"dimension"`
	Inferred   bool       `json:"inferred,omitempty"`
	Evidence   []Evidence `json:"evidence"`
	Confidence string     `json:"confidence"`
	Flags      []string   `json:"flags,omitempty"`
	Chat       string     `json:"chat"`
	Day        string     `json:"day"`
	Pattern    string     `json:"pattern,omitempty"` // the pattern the weekly synthesis filed it under
	Counter    string     `json:"counter,omitempty"` // the pattern it goes against: the owner chose the opposite
	Filed      string     `json:"filed,omitempty"`   // "none": looked at, no pattern to name
	Prompt     string     `json:"prompt"`
	Ruleset    string     `json:"ruleset"`
}

// Pattern is a named way the owner works, built from judgements. Status: seed (a starting name, no evidence here
// yet) → candidate → habitual (enough independent occurrences) → dormant.
type Pattern struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Dimension  string   `json:"dimension"`
	Definition string   `json:"definition"`
	Trigger    string   `json:"trigger,omitempty"`
	Choice     string   `json:"choice,omitempty"`
	NotWhen    string   `json:"not_when,omitempty"` // a kind of situation where the owner chose differently
	Members    []string `json:"members,omitempty"`  // judgement ids
	Days       []string `json:"days,omitempty"`
	Weeks      []string `json:"weeks,omitempty"`
	Chats      []string `json:"chats,omitempty"`
	Count      int      `json:"count"` // independent occurrences: distinct (chat, day) among the members
	Status     string   `json:"status"`
	Seed       bool     `json:"seed,omitempty"`
	Trait      string   `json:"trait,omitempty"`
	Counters   []string `json:"counters,omitempty"` // judgement ids that went against it
	// Inherited is how often an earlier profile saw it (seeds only), with its words in Prior: a pattern the earlier
	// profile found habitual (11 times or more) starts as a habit, and the judgements filed under it add to that.
	Inherited int        `json:"inherited,omitempty"`
	Prior     []Evidence `json:"prior,omitempty"`
	Pinned    bool       `json:"pinned,omitempty"`  // the user pinned it: it stays whatever the evidence does
	Deleted   bool       `json:"deleted,omitempty"` // the user deleted it: it is not made again
	FirstSeen string     `json:"first_seen,omitempty"`
	LastSeen  string     `json:"last_seen,omitempty"`
}

// Trait is an underlying trait several patterns share. Status: seed → candidate → stable, or shifting when the owner
// has lately chosen against it.
type Trait struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Definition string   `json:"definition"`
	Predicts   string   `json:"predicts,omitempty"`
	Applies    string   `json:"applies,omitempty"`     // the kind of situation it holds in
	NotApplies string   `json:"not_applies,omitempty"` // a kind of situation it does not
	Patterns   []string `json:"patterns,omitempty"`
	Inherited  bool     `json:"inherited,omitempty"` // a seed trait the earlier profile had validated: stable from the start
	Open       []string `json:"open,omitempty"`      // what the evidence leaves unsettled
	Pinned     bool     `json:"pinned,omitempty"`    // the user pinned it
	Deleted    bool     `json:"deleted,omitempty"`   // the user deleted it: it is not made again
	Status     string   `json:"status"`
	Seed       bool     `json:"seed,omitempty"`
	FirstSeen  string   `json:"first_seen,omitempty"`
	LastSeen   string   `json:"last_seen,omitempty"`
}

// Rule is one instruction the owner gave the AI they work with. Status: candidate (said once, not as a standing
// rule) → active (said to hold from now on, or said on two days) → dormant (not seen for a quarter).
type Rule struct {
	ID        string     `json:"id"`
	Text      string     `json:"text"`
	Scope     string     `json:"scope"`
	Hard      bool       `json:"hard,omitempty"`
	Durable   bool       `json:"durable,omitempty"`
	Why       string     `json:"why,omitempty"`
	Evidence  []Evidence `json:"evidence"`
	Days      []string   `json:"days"`
	Count     int        `json:"count"` // days it was said on
	Status    string     `json:"status"`
	Pinned    bool       `json:"pinned,omitempty"`  // the user pinned it: always in force, never dormant
	Edited    bool       `json:"edited,omitempty"`  // the user wrote the text: no later reading replaces it
	Deleted   bool       `json:"deleted,omitempty"` // the user deleted it: it is not made again
	FirstSeen string     `json:"first_seen"`
	LastSeen  string     `json:"last_seen"`
	Flags     []string   `json:"flags,omitempty"`
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
	// Part: "" for the unit's lines; "12-40" for a smaller batch of them, after the model declined the whole.
	Part string `json:"part,omitempty"`
	// Tier: the unit's tier when it was read (empty: tiers off), so a replay keeps to it.
	Tier string `json:"tier,omitempty"`
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

// ProposalWAL is the durable write-ahead record for one knowledge-base proposal. Before is captured before
// kb.update; NextApplied reserves the applied-log ID without advancing state.json until the write finishes.
// A record is removed only after state.json contains the completed Applied entry.
type ProposalWAL struct {
	Proposal      string    `json:"proposal"`
	ProposalState *Proposal `json:"proposal_state"` // present even if the weekly pass had not saved the proposal yet
	Status        string    `json:"status"`         // accepted | auto
	NextProp      int       `json:"next_proposal"`
	NextApplied   int       `json:"next_applied"`
	Applied       *Applied  `json:"applied"`
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
	Refusals []Refusal      `json:"refusals,omitempty"` // batches the model declined, and what became of them
	Report   string         `json:"report"`             // markdown
}

// Outcomes of a refused batch.
const (
	RefusalSplit   = "split"   // tried again as two smaller batches
	RefusalSkipped = "skipped" // too small to split (or split as far as it goes): its lines were not read
	RefusalWeekly  = "weekly"  // a triage call: the unit goes to the weekly pass only, as when the model is down
)

// Refusal is one batch of lines the model declined to answer.
type Refusal struct {
	Step    string `json:"step"` // triage | daily | memory
	Unit    string `json:"unit"` // "<kind>/<id>"
	Name    string `json:"name,omitempty"`
	Day     string `json:"day"`
	From    int    `json:"from"` // the first and last line number of the batch
	To      int    `json:"to"`
	Lines   int    `json:"lines"`
	Reason  string `json:"reason"` // as the hub said
	Outcome string `json:"outcome"`
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
	Version int `json:"version"`
	// SourceClean is what the one-off clean of a state written before source roles did (Version 1 → 2).
	SourceClean *SourceClean          `json:"source_clean,omitempty"`
	Installed   string                `json:"installed"`
	Done        map[string]*Done      `json:"done"`
	Items       map[string]*Item      `json:"items"`
	Rejections  []Rejection           `json:"rejections"`
	Digests     map[string]*Digest    `json:"digests"` // "<chat>@<day>", "<chat>@<day>#<part>" for a batch of it
	Triage      map[string]*TriageTag `json:"triage"`
	Proposals   []*Proposal           `json:"proposals"`
	Applied     []*Applied            `json:"applied"`
	Runs        []*Run                `json:"runs"`
	Usage       []*Usage              `json:"usage"`
	Graph       *Graph                `json:"graph"`
	Funnel      map[string]Funnel     `json:"funnel"`               // day → triage funnel
	Samples     map[string][]string   `json:"samples"`              // ISO week → items picked for the spot check
	Judgements  map[string]*Judgement `json:"judgements,omitempty"` // how the owner decides: the day's judgements (daily/7)
	Rules       map[string]*Rule      `json:"rules,omitempty"`      // what the owner asks of the AI (daily/7)
	Patterns    map[string]*Pattern   `json:"patterns,omitempty"`   // named from the judgements by the weekly synthesis
	Traits      map[string]*Trait     `json:"traits,omitempty"`     // the underlying traits the patterns share
	SeedsHash   string                `json:"seeds_hash,omitempty"` // the seed file last read
	NextPattern int                   `json:"next_pattern,omitempty"`
	NextTrait   int                   `json:"next_trait,omitempty"`
	// LegacyMemory is the memory layer of a state written before v4, kept as it was (the layer is gone): the one-off
	// clean reads it, curate.Migrate moves its entries into Entries, forgetting a source scrubs it.
	LegacyMemory json.RawMessage     `json:"memory,omitempty"`
	NextProp     int                 `json:"next_proposal"`
	NextApplied  int                 `json:"next_applied"`
	Reruns       []string            `json:"reruns,omitempty"`   // "<day>@<daily prompt>": days a daily run finished with that prompt (rerun_range)
	Admitted     map[string][]string `json:"admitted,omitempty"` // day → units a daily run (not dry) admitted: what coverage may expect a digest for

	// v4: every facet's entries in one structure (internal/entry), and what feeds and uses them. The event log is
	// not here: it is append-only in events.jsonl (AppendEvents).
	Entries       map[string]*entry.Entry         `json:"entries,omitempty"`
	Tombstones    []entry.Tombstone               `json:"tombstones,omitempty"`
	Types         map[entry.Facet][]entry.TypeDef `json:"types,omitempty"`
	Contacts      map[string]*entry.Contact       `json:"contacts,omitempty"` // by "person:<id>"
	PersonMerges  []entry.PersonMerge             `json:"person_merges,omitempty"`
	Rhythm        map[string]*people.Rhythm       `json:"rhythm,omitempty"` // by "person:<id>": direct-chat rhythm (P9)
	Corrections   []entry.Correction              `json:"corrections,omitempty"`
	Digs          []entry.Dig                     `json:"digs,omitempty"`
	Sources       map[string]*entry.SourceStatus  `json:"sources,omitempty"` // by source code + "/" + name
	SpeakerChecks []entry.SpeakerCheck            `json:"speaker_checks,omitempty"`
	Switches      map[string]entry.SwitchSetting  `json:"switches,omitempty"`
	Onboarding    *entry.Onboarding               `json:"onboarding,omitempty"`
	Exits         *entry.Exits                    `json:"exits,omitempty"`
	Readings      []entry.Reading                 `json:"readings,omitempty"`
	ActiveDays    []string                        `json:"active_days,omitempty"` // sorted days with any source data
	Cursors       map[string]string               `json:"cursors,omitempty"`     // per source: where incremental reading resumes
	Backfill      *Backfill                       `json:"backfill,omitempty"`
	// ModelPause: the model could not be called for a day's pass; the day is kept to run again and passes wait
	// until NextTry. Cleared by the next pass that reaches the model.
	ModelPause *ModelPause `json:"model_pause,omitempty"`
}

// ModelPause is why and since when the passes wait for the model, and when they try again.
type ModelPause struct {
	Since   string `json:"since"`    // RFC 3339 of the first failure in a row
	Reason  string `json:"reason"`   // the hub's message
	Day     string `json:"day"`      // the day whose pass could not run
	Fails   int    `json:"fails"`    // passes in a row that found the model unavailable
	NextTry string `json:"next_try"` // RFC 3339: no pass needing the model starts before it
	// What was away: "" the model, "assistant" the assistant's computer (its conversations could not be read).
	What string `json:"what,omitempty"`
}

// Backfill is the first-run replay of the user's history (4.1): the same daily and weekly passes over past days,
// oldest first, before anything is written to the assistant.
type Backfill struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Next     string `json:"next"` // the next day to run; "" when done
	Started  string `json:"started"`
	Finished string `json:"finished,omitempty"`
}

// Funnel is how a day's chats were sorted, for the report and the health page.
type Funnel struct {
	Chats    map[string]int `json:"chats"`  // decision → chat-days
	Tokens   map[string]int `json:"tokens"` // decision → estimated tokens
	Admitted int            `json:"admitted"`
	Sources  map[string]int `json:"sources"` // source → chat-days seen, for the report
	// With tiers: admitted units and their tokens by tier, and the units skipped as noise.
	Tiers      map[string]int `json:"tiers,omitempty"`
	TierTokens map[string]int `json:"tier_tokens,omitempty"`
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
		st.S = &State{Version: Version}
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
	if s.Judgements == nil {
		s.Judgements = map[string]*Judgement{}
	}
	if s.Rules == nil {
		s.Rules = map[string]*Rule{}
	}
	if s.Patterns == nil {
		s.Patterns = map[string]*Pattern{}
	}
	if s.Traits == nil {
		s.Traits = map[string]*Trait{}
	}
	if s.Samples == nil {
		s.Samples = map[string][]string{}
	}
	if s.Admitted == nil {
		s.Admitted = map[string][]string{}
	}
	if s.Funnel == nil {
		s.Funnel = map[string]Funnel{}
	}
	if s.Graph == nil {
		s.Graph = &Graph{}
	}
	if s.Entries == nil {
		s.Entries = map[string]*entry.Entry{}
	}
	if s.Types == nil {
		s.Types = map[entry.Facet][]entry.TypeDef{}
	}
	if s.Contacts == nil {
		s.Contacts = map[string]*entry.Contact{}
	}
	if s.Rhythm == nil {
		s.Rhythm = map[string]*people.Rhythm{}
	}
	if s.Sources == nil {
		s.Sources = map[string]*entry.SourceStatus{}
	}
	if s.Switches == nil {
		s.Switches = map[string]entry.SwitchSetting{}
	}
	if s.Cursors == nil {
		s.Cursors = map[string]string{}
	}
	if s.Exits == nil {
		s.Exits = &entry.Exits{}
	}
	if s.Exits.Files == nil {
		s.Exits.Files = map[string]*entry.ExitFile{}
	}
	s.Graph.init()
}

// Clone is a deep copy of the state, through the same JSON as state.json: what pages read while a run changes the
// state itself. Call it with the lock held.
func (s *State) Clone(now time.Time) (*State, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	out := &State{}
	if err := json.Unmarshal(b, out); err != nil {
		return nil, err
	}
	out.init(now)
	return out, nil
}

// Lock and Unlock guard the state: the scheduler, the UI and event handlers share it.
func (s *Store) Lock()   { s.mu.Lock() }
func (s *Store) Unlock() { s.mu.Unlock() }

// TryLock takes the lock only if nobody holds it.
func (s *Store) TryLock() bool { return s.mu.TryLock() }

// Save writes the state atomically. Call it with the lock held.
func (s *Store) Save() error {
	b, err := json.Marshal(s.S)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.Dir, "state.json"), b)
}

// EditBackups applies edit to the raw JSON of every automatic state backup and atomically writes changed results.
// Call it with the lock held. A byte-identical result is not written, preserving the backup's bytes and mtime.
func (s *Store) EditBackups(edit func([]byte) ([]byte, error)) error {
	paths, err := filepath.Glob(filepath.Join(s.Dir, "state.json.bak-*"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read state backup %s: %w", filepath.Base(path), err)
		}
		out, err := edit(raw)
		if err != nil {
			return fmt.Errorf("edit state backup %s: %w", filepath.Base(path), err)
		}
		if bytes.Equal(raw, out) {
			continue
		}
		if err := writeAtomic(path, out); err != nil {
			return fmt.Errorf("write state backup %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

func proposalWALPath(dir, id string) (string, error) {
	if id == "" || filepath.Base(id) != id {
		return "", errors.New("invalid proposal write-ahead record id")
	}
	return filepath.Join(dir, "proposal-wal", id+".json"), nil
}

// WriteProposalWAL durably records a proposal write before the knowledge base is changed. Call it with the lock held.
func (s *Store) WriteProposalWAL(w *ProposalWAL) error {
	if w == nil || w.Applied == nil || w.Proposal == "" || w.ProposalState == nil || w.Status == "" || w.NextProp < 1 || w.NextApplied < 1 {
		return errors.New("incomplete proposal write-ahead record")
	}
	path, err := proposalWALPath(s.Dir, w.Proposal)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(w)
	if err != nil {
		return err
	}
	return writeAtomic(path, b)
}

// ReadProposalWAL returns nil when the proposal has no incomplete write.
func (s *Store) ReadProposalWAL(id string) (*ProposalWAL, error) {
	path, err := proposalWALPath(s.Dir, id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	w := &ProposalWAL{}
	if err := json.Unmarshal(raw, w); err != nil {
		return nil, err
	}
	if w.Proposal != id || w.ProposalState == nil || w.Applied == nil || w.NextProp < 1 || w.NextApplied < 1 {
		return nil, errors.New("invalid proposal write-ahead record " + id)
	}
	return w, nil
}

// PendingProposalWALs returns all write-ahead records left by interrupted or failed applications.
func (s *Store) PendingProposalWALs() ([]*ProposalWAL, error) {
	entries, err := os.ReadDir(filepath.Join(s.Dir, "proposal-wal"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*ProposalWAL
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok {
			continue
		}
		w, err := s.ReadProposalWAL(id)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

// CompleteProposalWAL clears the write-ahead record after its Applied entry is saved in state.json.
func (s *Store) CompleteProposalWAL(id string) error {
	path, err := proposalWALPath(s.Dir, id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
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

// Version is the state's version: 2 since every cited line records its role and nothing rests on the assistant's,
// an agent's or a bot's words. A state of an older version is cleaned once when the extension starts (SourceClean).
const Version = 2

// SourceClean is the one-off clean of an older state: what it held before and after, what went, by layer and
// category ("memory:agent", "item:assistant"…), and where the state was backed up first.
type SourceClean struct {
	At               string         `json:"at"`
	MemoryBefore     int            `json:"memory_before"`
	MemoryAfter      int            `json:"memory_after"`
	ItemsBefore      int            `json:"items_before"`
	ItemsAfter       int            `json:"items_after"`
	Removed          map[string]int `json:"removed"`
	Candidates       int            `json:"candidates"` // items made candidates (待确认): other people's words, or a single source
	ProposalsExpired int            `json:"proposals_expired"`
	Backup           string         `json:"backup"`
}

// Replace puts a whole state in place of the current one (the one-off clean), the caller holding the lock.
func (s *Store) Replace(ns *State) {
	ns.init(s.Now())
	*s.S = *ns
}

// AppendEvents adds events to the append-only log events.jsonl (one JSON object a line). Call it with the lock held.
func (s *Store) AppendEvents(evs ...entry.Event) error {
	if len(evs) == 0 {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, e := range evs {
		if err := enc.Encode(e); err != nil {
			f.Close()
			return err
		}
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// RewriteEvents rewrites the event log in place (forgetting a source): keep changes an event or says it goes. The
// new log replaces the old atomically. Call it with the lock held.
func (s *Store) RewriteEvents(keep func(*entry.Event) bool) error {
	evs, err := s.Events()
	if err != nil {
		return err
	}
	if evs == nil {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for i := range evs {
		if !keep(&evs[i]) {
			continue
		}
		if err := enc.Encode(evs[i]); err != nil {
			return err
		}
	}
	return writeAtomic(filepath.Join(s.Dir, "events.jsonl"), buf.Bytes())
}

// Events reads the whole event log, oldest first (empty when there is none).
func (s *Store) Events() ([]entry.Event, error) {
	raw, err := os.ReadFile(filepath.Join(s.Dir, "events.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []entry.Event
	dec := json.NewDecoder(bytes.NewReader(raw))
	for dec.More() {
		var e entry.Event
		if err := dec.Decode(&e); err != nil {
			return out, err
		}
		out = append(out, e)
	}
	return out, nil
}
