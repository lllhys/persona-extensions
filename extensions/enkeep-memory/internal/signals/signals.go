// Package signals keeps the correction ledger of facet ① (design 5.1) and learns from friction (3.1): it reads the
// day's words and folded work from the hub, screens them without a model (word lists, folded error counts, cards,
// to-dos, how sessions ended), lets a model decide whether and where to dig, reads only that stretch of tool calls
// and results, and turns what it shows into entries (② mostly, ① and ④ from the user's own words).
//
// It never reads a whole session's tool calls: daily reading asks the hub for folded pages only (fold_work=1), and
// the one unfolded read is the from_seq..through_seq of a dig, at most one folded stretch next to the signal (K17).
// Every dig is kept (State.Digs) with the signal that triggered it, the decision, the bytes read and the entries it
// made; those entries carry the flag "dig:<id>" and evidence inside the stretch, so they point back to it.
//
// Entries are written only through the curator's Observe (Options.Observe), never directly.
package signals

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// Options is what a run needs. Hub and Model may be nil: the ledger and the screen then do what they can without
// them (nothing is judged without a model; nothing is read without a hub).
type Options struct {
	Hub   hubapi.Hub
	Model model.Model
	Tier  model.Tier
	Lang  string // the target language of entries
	Loc   *time.Location
	Now   func() time.Time
	Dir   string // the state directory: signals.json is kept there
	// Observe keeps an entry (curate.(*Curator).Observe); it returns the id, or "" when the user deleted the same
	// content before.
	Observe func(e *entry.Entry, run string) string
	Run     string
	Logf    func(format string, args ...any)
	// Workers > 1: up to this many model calls (and segment reads) at once; what they answer is kept in order.
	Workers int
}

func (o *Options) logf(format string, args ...any) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

func (o *Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o *Options) loc() *time.Location {
	if o.Loc != nil {
		return o.Loc
	}
	return time.UTC
}

// Result counts what a day's run did.
type Result struct {
	UserMsgs     int            `json:"user_msgs"`
	Screened     int            `json:"screened"`    // user messages the correction screen passed to the model
	Corrections  int            `json:"corrections"` // new corrections kept
	Recurrences  int            `json:"recurrences"` // of them, after the entry was effective
	NewEntries   []string       `json:"new_entries"` // ① entries made from new issues
	Hits         map[string]int `json:"hits"`        // signal → screen hits
	Digs         int            `json:"digs"`        // segments read
	SegmentBytes int            `json:"segment_bytes"`
	FoldBytes    int            `json:"fold_bytes"`
	Mined        []string       `json:"mined"` // entries the digs made or saw again
	Missing      []string       `json:"missing,omitempty"`
}

// Run does a day: gather, ledger, screen, dig. Call it with the store's lock held.
func Run(ctx context.Context, st *store.State, day string, o Options) (res *Result, retErr error) {
	sd, err := loadSide(o.Dir)
	if err != nil {
		return nil, err
	}
	res = &Result{Hits: map[string]int{}}
	g, err := gather(ctx, o, day, sd)
	if err != nil {
		return nil, err
	}
	// Once gather succeeds, every later exit must checkpoint the side ledger before the caller can save state.json.
	defer func() {
		saveErr := sd.save(o.Dir)
		if retErr == nil {
			retErr = saveErr
		} else if saveErr != nil {
			retErr = errors.Join(retErr, saveErr)
		}
	}()
	res.Missing = g.Missing
	res.FoldBytes = g.FoldBytes
	stats := sd.day(day)
	stats.FoldBytes, stats.Sessions, stats.Cards, stats.FoldsUnknown = g.FoldBytes, len(g.Sessions), len(g.Cards), g.foldsUnknown()
	stats.Asks = g.asks()

	led := &ledger{o: &o, st: st, sd: sd, day: day}
	if err := led.run(ctx, g, res); err != nil {
		return nil, err
	}
	stats.UserMsgs, stats.Screened = res.UserMsgs, res.Screened

	hits := screen(g, st, sd.Words, led.found)
	for _, h := range hits {
		res.Hits[h.Signal]++
	}
	stats.AskInfra = res.Hits[entry.SigAskInfra]
	stats.Repeats = res.Hits[entry.SigRepeat]
	if err := dig(ctx, &o, st, g, hits, res); err != nil {
		return nil, err
	}
	stats.Segments, stats.SegmentBytes = res.Digs, res.SegmentBytes
	return res, nil
}

// side is what the package keeps beside the state (signals.json in the state directory): the open issues of the
// ledger, when each hand-written memory was first seen, per-day counts for the readings, and the user's own words.
type side struct {
	Issues     map[string]*Issue    `json:"issues"`
	MemorySeen map[string]string    `json:"memory_seen"`
	Days       map[string]*DayStats `json:"days"`
	Words      map[string][]string  `json:"words,omitempty"` // signal → the user's own words for the screen
	// Read: per session, the last seq of a folded page read and the day it was read for; the next day reads on
	// from there (after_seq), keeping some items before it for context.
	Read map[string]Cursor `json:"read,omitempty"`
}

// Cursor is where a session's folded page was last read to.
type Cursor struct {
	Seq int    `json:"seq"`
	Day string `json:"day"`
}

// Issue is a kind of new problem the ledger saw: corrections no entry covered, grouped by the model's key. It
// becomes a ① entry once it was seen ≥2 times in ≥2 sessions.
type Issue struct {
	Key         string   `json:"key"`
	Text        string   `json:"text"` // what the user wants of the AI, in the target language
	Corrections []string `json:"corrections"`
	Sessions    []string `json:"sessions"`
	Entry       string   `json:"entry,omitempty"`
	First       string   `json:"first"`
	Sensitive   string   `json:"sensitive,omitempty"`
}

// DayStats are a day's counts, the denominators of the readings and the reading volume.
type DayStats struct {
	UserMsgs     int `json:"user_msgs"`
	Screened     int `json:"screened"`
	Asks         int `json:"asks"` // questions the AI put to the user: cards, and the AI's lines with a question before a user's
	Sessions     int `json:"sessions"`
	Cards        int `json:"cards"`
	AskInfra     int `json:"ask_infra"`
	Repeats      int `json:"repeats"`
	FoldBytes    int `json:"fold_bytes"`
	SegmentBytes int `json:"segment_bytes"`
	Segments     int `json:"segments"`
	FoldsUnknown int `json:"folds_unknown"` // folds without error counts (hub without H3): not read as "no errors"
}

const sideFile = "signals.json"

func loadSide(dir string) (*side, error) {
	sd := &side{}
	if dir != "" {
		b, err := os.ReadFile(filepath.Join(dir, sideFile))
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return nil, err
		default:
			if err := json.Unmarshal(b, sd); err != nil {
				return nil, fmt.Errorf("%s: %w", sideFile, err)
			}
		}
	}
	if sd.Issues == nil {
		sd.Issues = map[string]*Issue{}
	}
	if sd.MemorySeen == nil {
		sd.MemorySeen = map[string]string{}
	}
	if sd.Days == nil {
		sd.Days = map[string]*DayStats{}
	}
	return sd, nil
}

func (sd *side) save(dir string) error {
	if dir == "" {
		return nil
	}
	b, err := json.MarshalIndent(sd, "", " ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, sideFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, sideFile))
}

func (sd *side) day(day string) *DayStats {
	d := sd.Days[day]
	if d == nil {
		d = &DayStats{}
		sd.Days[day] = d
	}
	return d
}

// AddWord adds the user's own word for a signal's screen (the page calls it).
func AddWord(dir, signal, word string) error {
	sd, err := loadSide(dir)
	if err != nil {
		return err
	}
	if sd.Words == nil {
		sd.Words = map[string][]string{}
	}
	word = strings.TrimSpace(word)
	if word == "" {
		return nil
	}
	for _, w := range sd.Words[signal] {
		if w == word {
			return nil
		}
	}
	sd.Words[signal] = append(sd.Words[signal], word)
	return sd.save(dir)
}

// Stats reads the per-day counts (for the page and the replay).
func Stats(dir string) (map[string]*DayStats, error) {
	sd, err := loadSide(dir)
	if err != nil {
		return nil, err
	}
	return sd.Days, nil
}

// Issues reads the ledger's open and filed issues.
func Issues(dir string) (map[string]*Issue, error) {
	sd, err := loadSide(dir)
	if err != nil {
		return nil, err
	}
	return sd.Issues, nil
}

func hash(parts ...string) string {
	h := sha1.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func cut(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func uniq(xs []string) []string {
	sort.Strings(xs)
	out := xs[:0]
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}
