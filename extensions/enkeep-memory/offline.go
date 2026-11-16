package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/convsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/sources"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// offline reruns the curation on a read-only copy of a hub's knowledge base (and, optionally, of the assistant's
// conversation transcripts) with a real model through a local command line, for evaluation. Nothing is written to
// -kb or -conversations: knowledge-base writes are recorded by a dry-run wrapper and dropped; the state goes to
// -state (a scratch directory, kept between runs so unchanged units are not asked again) and the reports and an
// export of everything produced to -out.
func offline(args []string) error {
	fs := flag.NewFlagSet("offline", flag.ContinueOnError)
	kb := fs.String("kb", "", "a copy of the hub's knowledge-base directory (read only)")
	convs := fs.String("conversations", "", "a copy of the assistant's conversation transcripts (read only, optional)")
	origins := fs.String("conversation-origins", "", "JSON {session id: origin} as assistant.conversations lists them; only user and channel conversations are read (none without it)")
	from := fs.String("from", "", "first day, YYYY-MM-DD")
	to := fs.String("to", "", "last day, YYYY-MM-DD (default: from)")
	tz := fs.String("tz", "UTC", "the time zone days are cut in")
	stateDir := fs.String("state", "", "scratch state directory")
	out := fs.String("out", "", "directory for the reports and export.json")
	cmd := fs.String("model-cmd", "claude", "the model command line")
	daily := fs.String("model", "", "model for the daily steps (required unless -model-dir answers)")
	weekly := fs.String("weekly-model", "", "model for the weekly step (default: -model)")
	effort := fs.String("effort", "", "effort level passed to the command line (optional)")
	usePi := fs.Bool("pi", false, "run the model through the pi coding agent with the provider it is signed in to (-model and -weekly-model name the models)")
	provider := fs.String("provider", "", "the provider -pi asks (empty: pi's own default)")
	modelDir := fs.String("model-dir", "", "answer requests from files in this directory instead of a command line: requests/<key>.json are written for whoever answers, answers/<key>.json are read")
	synth := fs.Bool("synth", false, "consolidate the entries after the days (catch up the filing, settle the statuses)")
	consolidateOnly := fs.Bool("consolidate-only", false, "do not read the days again: on a state that has the entries already, run the consolidation day by day, and the weekly pass at every week's end and after the last day")
	peopleOnly := fs.Bool("people-only", false, "do not read the days again: on a state that has the entries already, count the intersection list and the direct chats' rhythm over the days, run the card pass and propose the person pages (readout in people-cards.json)")
	workers := fs.Int("workers", 6, "model calls at once in the daily pass")
	cache := fs.String("cache", "", "answer cache directory (default: <state>/model-cache)")
	week := fs.Bool("week", false, "run the weekly pass for the week of the last day afterwards")
	mode := fs.String("mode", "review", "mode: shadow | review")
	tiers := fs.Bool("tiers", true, "tiers by source (false: the rules before tiers, for comparison)")
	language := fs.String("language", "", "the target language entries are written in (BCP 47, e.g. zh-Hans, en; default: the configuration's)")
	agentsDir := fs.String("agents-dir", "", "a directory of agents' transcript directories (Claude Code's projects, read only): S3 agent sessions and S2 cards")
	assistantDirs := fs.String("assistant-dirs", "", "comma-separated names under -agents-dir that hold the assistant's own sessions (S1, not read as agents)")
	memoryDir := fs.String("memory-dir", "", "the assistant's hand-written memory directory (S6, read only)")
	aboutFile := fs.String("about-file", "", "a copy of \"about me\" (S7, read only)")
	todosFile := fs.String("todos-file", "", "a copy of the to-dos and decisions as JSON {todos, decisions} (S5, read only)")
	foldUnknown := fs.Bool("fold-unknown", false, "simulate a hub without H3: folded work without error counts and tool names")
	hubNow := fs.Bool("hub-now", false, "answer as today's hub does, before H2 and H3: no memory, cards only the newest 500, folds without error counts")
	cold := fs.Bool("cold", false, "run the days as the first-run replay (4.1): oldest first, weekly passes at the end of each ISO week, progress in the state's backfill")
	onboarding := fs.String("onboarding", "", "first-run answers to save before the replay: JSON {\"use\": work|life|both, \"musts\": [...], \"focus\": [...]}")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *kb == "" || *from == "" || *stateDir == "" || *out == "" {
		return fmt.Errorf("-kb, -from, -state and -out are required")
	}
	if *daily == "" && *modelDir == "" {
		return fmt.Errorf("-model is required (or -model-dir)")
	}
	if *to == "" {
		*to = *from
	}
	if *to < *from {
		return fmt.Errorf("-to %s is before -from %s", *to, *from)
	}
	if *weekly == "" {
		*weekly = *daily
	}
	if *cache == "" {
		*cache = filepath.Join(*stateDir, "model-cache")
	}
	loc, err := time.LoadLocation(*tz)
	if err != nil {
		return err
	}
	fx, err := kbsrc.LoadFixture(*kb)
	if err != nil {
		return err
	}
	var src kbsrc.Source = fx
	if *convs != "" {
		var org map[string]string
		if *origins != "" {
			raw, err := os.ReadFile(*origins)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &org); err != nil {
				return fmt.Errorf("%s: %w", *origins, err)
			}
		} else {
			fmt.Fprintln(os.Stderr, "no -conversation-origins: no conversation can be confirmed as opened by the user, none is read")
		}
		src = convsrc.FromDir(src, *convs, org, loc)
	}
	start, err := time.ParseInLocation("2006-01-02", *from, loc)
	if err != nil {
		return err
	}
	off := &hubapi.Offline{AgentsDir: *agentsDir, MemoryDir: *memoryDir, AboutFile: *aboutFile, TodosFile: *todosFile,
		Since: start, FoldUnknown: *foldUnknown, HubNow: *hubNow}
	if *assistantDirs != "" {
		off.AssistantDirs = strings.Split(*assistantDirs, ",")
	}
	more := sources.With(src, off, loc)
	more.Logf = func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	if err := os.MkdirAll(*out, 0o700); err != nil {
		return err
	}
	dump, err := os.OpenFile(filepath.Join(*out, "source-units.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer dump.Close()
	dumped := map[string]bool{}
	more.Dump = func(ref, day string, lines []string) {
		if dumped[ref+"@"+day] {
			return // read again for the triage export
		}
		dumped[ref+"@"+day] = true
		b, _ := json.Marshal(map[string]any{"ref": ref, "day": day, "lines": lines})
		_, _ = dump.Write(append(b, '\n'))
	}
	src = more
	dry := &kbsrc.DryRun{Source: src} // whatever the curator writes is recorded here, never applied
	st, err := store.Open(*stateDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	cfg := curate.Defaults()
	cfg.Mode = *mode
	cfg.Tiers = *tiers
	if *language != "" {
		cfg.Language = *language
	}
	var m model.Model = model.CLI{Command: *cmd, Models: map[model.Tier]string{model.Small: *daily, model.Medium: *weekly}, Effort: *effort,
		CacheDir: *cache}
	if *usePi {
		m = model.Pi{Provider: *provider, Models: map[model.Tier]string{model.Small: *daily, model.Medium: *weekly}, CacheDir: *cache}
	}
	if *modelDir != "" {
		m = model.File{Dir: *modelDir}
	}
	// The clock of a run is the morning after the day it curates, as the scheduled pass sees it.
	var now time.Time
	// Hub: the same offline hub the sources read, so the correction ledger, the friction digs and the readings (which
	// read sessions, folded work and cards through it) run in a replay as they do in the extension.
	c := &curate.Curator{Src: dry, Model: m, St: st, Cfg: cfg, Loc: loc, Workers: *workers, Collect: more, Hub: off, Cards: curate.PeopleCards{},
		Now:  func() time.Time { return now },
		Logf: func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }}
	ctx := context.Background()
	if *onboarding != "" {
		// The first-run answers, as the page saves them, before anything runs (4.3).
		if err := applyOnboarding(ctx, c, *onboarding); err != nil {
			return err
		}
	}

	var runs []*store.Run
	if *peopleOnly {
		now = morningAfter(*to, loc)
		r, err := peopleReplay(ctx, c, dry, *from, *to, *out)
		if err != nil {
			return fmt.Errorf("people: %w", err)
		}
		runs = append(runs, r)
		*from, *week, *synth, *consolidateOnly = next(*to), false, false, false // nothing below reads days again
	}
	if *consolidateOnly {
		for d := *from; d <= *to; d = next(d) {
			now = morningAfter(d, loc)
			started := time.Now()
			r, err := c.RunSynth(ctx, d, false)
			if err != nil {
				return fmt.Errorf("consolidate %s: %w", d, err)
			}
			fmt.Fprintf(os.Stderr, "consolidate %s: %d pending, %d filed, %d new classes, %d model calls, %s\n", d, r.Counts["consolidate_pending"],
				r.Counts["consolidate_filed"], r.Counts["consolidate_new_class"], r.Counts["model_calls"], time.Since(started).Round(time.Second))
			runs = append(runs, r)
			if days, _ := curate.WeekDays(curate.ISOWeek(d)); len(days) == 7 && (d == days[6] || d == *to) {
				r, err := c.RunWeekly(ctx, curate.ISOWeek(d), false)
				if err != nil {
					return fmt.Errorf("week %s: %w", curate.ISOWeek(d), err)
				}
				fmt.Fprintf(os.Stderr, "weekly %s: %d abstract proposed, %d blocked, %d impact none, %d model calls\n", curate.ISOWeek(d),
					r.Counts["abstract_proposed"], r.Counts["abstract_blocked"], r.Counts["impact_none"], r.Counts["model_calls"])
				runs = append(runs, r)
			}
		}
		*from, *week, *synth = next(*to), false, false // nothing below reads days again
	}
	if *cold && *from <= *to {
		// The first-run replay: the same passes as the scheduled ones, a day at a time, resumable from the state.
		now = morningAfter(*from, loc).Add(-24 * time.Hour) // installed the morning of the first day
		st.Lock()
		c.PlanBackfill(*from, *to)
		st.Unlock()
		for {
			st.Lock()
			b := *st.S.Backfill
			st.Unlock()
			if b.Finished != "" {
				break
			}
			now = morningAfter(b.Next, loc)
			started := time.Now()
			if _, err := c.BackfillStep(ctx); err != nil {
				return fmt.Errorf("backfill %s: %w", b.Next, err)
			}
			fmt.Fprintf(os.Stderr, "backfill %s done in %s\n", b.Next, time.Since(started).Round(time.Second))
		}
		for _, r := range st.S.Runs {
			if r.Kind == "daily" && r.Period >= *from && r.Period <= *to || r.Kind == "weekly" {
				runs = append(runs, r)
			}
		}
	} else {
		for d := *from; d <= *to; d = next(d) {
			now = morningAfter(d, loc)
			started := time.Now()
			r, err := c.RunDaily(ctx, d, false)
			if err != nil {
				return fmt.Errorf("%s: %w", d, err)
			}
			fmt.Fprintf(os.Stderr, "daily %s: %d model calls, %d tokens, %s\n", d, r.Counts["model_calls"], r.Tokens, time.Since(started).Round(time.Second))
			runs = append(runs, r)
		}
	}
	if *week {
		now = morningAfter(*to, loc)
		r, err := c.RunWeekly(ctx, curate.ISOWeek(*to), false)
		if err != nil {
			return fmt.Errorf("week %s: %w", curate.ISOWeek(*to), err)
		}
		runs = append(runs, r)
	}
	if *synth {
		now = morningAfter(*to, loc)
		r, err := c.RunSynth(ctx, *to, false)
		if err != nil {
			return fmt.Errorf("synthesis: %w", err)
		}
		runs = append(runs, r)
	}
	for _, r := range runs {
		if err := os.WriteFile(filepath.Join(*out, r.Kind+"-"+r.Period+".md"), []byte(r.Report), 0o644); err != nil {
			return err
		}
	}
	x := export(st, len(dry.Writes))
	x.Rules, x.Judgements, x.Patterns, x.Traits = st.S.Rules, st.S.Judgements, st.S.Patterns, st.S.Traits
	x.Units = units(ctx, src, st)
	for _, id := range sortedIDs(st.S.Graph.Assertions) {
		a := st.S.Graph.Assertions[id]
		name := func(n string) string {
			if nd := st.S.Graph.Nodes[n]; nd != nil {
				return nd.Type + ":" + nd.Name
			}
			return n
		}
		x.Relations = append(x.Relations, exportRelation{ID: a.ID, Subject: name(a.S), Predicate: a.P, Object: name(a.O),
			Status: a.Status, Confidence: a.Confidence, Evidence: a.Evidence})
	}
	x.Triage = map[string]map[string]exportDecision{}
	for d := *from; d <= *to; d = next(d) {
		ds, err := c.Triage(ctx, d)
		if err != nil {
			return err
		}
		x.Triage[d] = map[string]exportDecision{}
		for _, dc := range ds {
			x.Triage[d][dc.Chat] = exportDecision{Rule: dc.Rule, Action: dc.Action, Tier: dc.Tier, Tokens: dc.Tokens, Reason: dc.Reason}
		}
	}
	b, err := json.MarshalIndent(x, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "export.json"), b, 0o644); err != nil {
		return err
	}
	if err := writeSources(*out, st, off, start, morningAfter(*to, loc).Add(-4*time.Hour)); err != nil {
		return err
	}
	// The cards of the range beside the state, so the acceptance readings (R4) can be worked out from the state
	// directory alone (accept -state).
	if cards, err := off.Interactions(ctx, hubapi.InteractionsQuery{Since: start, Before: morningAfter(*to, loc).Add(-4 * time.Hour)}); len(cards) > 0 || err == nil {
		if err := writeJSON(filepath.Join(*stateDir, cardsFile), cards); err != nil {
			return err
		}
	}
	fmt.Printf("state: %s\nout: %s\nitems: %d, entries: %d, digests: %d, proposals: %d, rejections: %d, kb writes recorded (not made): %d\n",
		*stateDir, *out, len(st.S.Items), len(st.S.Entries), len(st.S.Digests), len(st.S.Proposals), len(st.S.Rejections), len(dry.Writes))
	return nil
}

func morningAfter(day string, loc *time.Location) time.Time {
	t, _ := time.ParseInLocation("2006-01-02", day, loc)
	return t.AddDate(0, 0, 1).Add(4 * time.Hour)
}

// The export: everything a run produced, for scoring.
type (
	exportFile struct {
		Items      []*store.Item           `json:"items"`
		Entries    []*entry.Entry          `json:"entries"`
		Digests    []*store.Digest         `json:"digests"`
		Proposals  []exportProposal        `json:"proposals"`
		Rejections map[string]int          `json:"rejections"`          // code → count
		ByClass    map[string]int          `json:"rejections_by_class"` // "<class>/<code>" → count
		Funnel     map[string]store.Funnel `json:"funnel"`
		Writes     int                     `json:"kb_writes_recorded"`
		// Units are the lines each model call was shown, by "<chat>@<day>@<step>": the context a reader needs
		// to judge what was extracted.
		Units map[string][]exportLine `json:"units"`
		// Triage is what the rules said of every unit, by day and unit (before the model's tags).
		Triage map[string]map[string]exportDecision `json:"triage"`
		// Relations are the graph's assertions, with the names of both ends (the background tier gives only these).
		Relations []exportRelation `json:"relations"`
		// The layers of daily/7 and the synthesis.
		Rules      map[string]*store.Rule      `json:"rules,omitempty"`
		Judgements map[string]*store.Judgement `json:"judgements,omitempty"`
		Patterns   map[string]*store.Pattern   `json:"patterns,omitempty"`
		Traits     map[string]*store.Trait     `json:"traits,omitempty"`
	}
	exportRelation struct {
		ID         string           `json:"id"`
		Subject    string           `json:"subject"`
		Predicate  string           `json:"predicate"`
		Object     string           `json:"object"`
		Status     string           `json:"status"`
		Confidence string           `json:"confidence"`
		Evidence   []store.Evidence `json:"evidence"`
	}
	exportDecision struct {
		Rule   string `json:"rule"`
		Action string `json:"action"`
		Tier   string `json:"tier,omitempty"`
		Tokens int    `json:"tokens"`
		Reason string `json:"reason"`
	}
	exportLine struct {
		N       int    `json:"n"`
		Time    string `json:"time,omitempty"`
		Speaker string `json:"speaker"`
		Text    string `json:"text"`
	}
	exportProposal struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Status string `json:"status"`
		Title  string `json:"title"`
		Reason string `json:"reason,omitempty"`
		Target string `json:"target,omitempty"`
	}
)

func export(st *store.Store, writes int) exportFile {
	s := st.S
	x := exportFile{Rejections: map[string]int{}, ByClass: map[string]int{}, Funnel: s.Funnel, Writes: writes}
	for _, it := range s.Items {
		x.Items = append(x.Items, it)
	}
	sort.Slice(x.Items, func(i, j int) bool { return x.Items[i].ID < x.Items[j].ID })
	for _, id := range sortedIDs(s.Entries) {
		x.Entries = append(x.Entries, s.Entries[id])
	}
	for _, d := range s.Digests {
		x.Digests = append(x.Digests, d)
	}
	sort.Slice(x.Digests, func(i, j int) bool {
		if x.Digests[i].Day != x.Digests[j].Day {
			return x.Digests[i].Day < x.Digests[j].Day
		}
		return x.Digests[i].Chat < x.Digests[j].Chat
	})
	for _, pr := range s.Proposals {
		x.Proposals = append(x.Proposals, exportProposal{ID: pr.ID, Kind: pr.Kind, Status: pr.Status, Title: pr.Title,
			Reason: pr.Detail, Target: pr.Target})
	}
	for _, r := range s.Rejections {
		x.Rejections[r.Code]++
		x.ByClass[r.Class+"/"+r.Code]++
	}
	return x
}

// units reads again, from the source, the lines every daily and memory call was shown.
func units(ctx context.Context, src kbsrc.Source, st *store.Store) map[string][]exportLine {
	out := map[string][]exportLine{}
	for _, d := range st.S.Done {
		if d.Step != "daily" && d.Step != "memory" {
			continue
		}
		kind, id, ok := strings.Cut(d.Chat, "/")
		if !ok {
			continue
		}
		if sources.DayKind(kind) {
			id = d.Chat // the sources' units are read by "<kind>/<id>"
		}
		r, err := src.Day(ctx, id, d.Day)
		if err != nil {
			continue
		}
		shown := map[int]bool{}
		for _, n := range d.Shown {
			shown[n] = true
		}
		var all []norm.Line
		for _, m := range r.Messages {
			all = append(all, voice.Relabel(norm.Parse(m.N, m.Text)))
		}
		if strings.HasPrefix(id, "github-") || strings.HasPrefix(id, "gh-") {
			all = voice.AgentThread(all) // as the curator read it
		}
		var ls []exportLine
		for _, l := range all {
			if shown[l.N] {
				ls = append(ls, exportLine{N: l.N, Time: l.Time, Speaker: l.Speaker, Text: l.Body})
			}
		}
		out[d.Chat+"@"+d.Day+"@"+d.Step] = ls
	}
	return out
}

func sortedIDs[V any](m map[string]V) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// sourcesReport is what the replay read, by source: the sources card as the state has it, and for the agents'
// sessions what the daily reading took against reading every transcript whole.
type sourcesReport struct {
	Sources map[string]*entry.SourceStatus `json:"sources"`
	Cursors int                            `json:"cursors"`
	Active  []string                       `json:"active_days"`
	Agents  struct {
		ReadBytes       int     `json:"read_bytes"`       // folded pages (fold_work=1, after_seq)
		TranscriptBytes int64   `json:"transcript_bytes"` // the transcripts of the sessions active in the range, whole
		Ratio           float64 `json:"ratio"`            // read / transcripts
	} `json:"agents"`
	Backfill *store.Backfill `json:"backfill,omitempty"`
}

func writeSources(out string, st *store.Store, off *hubapi.Offline, since, until time.Time) error {
	r := sourcesReport{Sources: st.S.Sources, Cursors: len(st.S.Cursors), Active: st.S.ActiveDays, Backfill: st.S.Backfill}
	if s := st.S.Sources[entry.SrcAgent+"/"+sources.KindAgents]; s != nil {
		r.Agents.ReadBytes = s.Bytes
	}
	r.Agents.TranscriptBytes = off.TranscriptBytes(since, until, false)
	if r.Agents.TranscriptBytes > 0 {
		r.Agents.Ratio = float64(r.Agents.ReadBytes) / float64(r.Agents.TranscriptBytes)
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	var keys []string
	for k := range r.Sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := r.Sources[k]
		fmt.Fprintf(os.Stderr, "source %-20s available=%-5v units=%-5d lines=%-6d bytes=%-9d missing=%s waiting=%s\n", k, s.Available, s.Units, s.Lines, s.Bytes, s.Missing, s.Waiting)
	}
	fmt.Fprintf(os.Stderr, "agents: read %d bytes of %d transcript bytes (%.2f%%)\n", r.Agents.ReadBytes, r.Agents.TranscriptBytes, 100*r.Agents.Ratio)
	return os.WriteFile(filepath.Join(out, "sources.json"), b, 0o644)
}
