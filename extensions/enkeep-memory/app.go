package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/convsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/sources"
	"log"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

type app struct {
	mu        sync.Mutex
	c         *curate.Curator
	hub       *ext.Hub
	running   string // the run in progress, if any
	lastErr   string
	paused    []string     // a.tried keys of passes that found the model unavailable: tried again after the pause
	tried     []string     // days a daily run started since this process did
	member    bool         // a member hub of a group: the curator runs only on the centre
	changed   int          // kb.changed events since start
	dataDir   string       // the extension's data directory (trials leave their report there)
	lastTrial string       // the last trial's summary (dryrun_hub.go)
	source    kbsrc.Source // tests can inject a source before init; production uses the hub
	hubAPI    hubapi.Hub   // the hub beyond the knowledge base: the assistant's runtime, the weekly report's notify
	hubLoc    *time.Location
	rt        string // the assistant's runtime, cached (page_v4.go)
	rtAt      time.Time
	exitDir   string // where the exit files are when not in the store's directory (the page subcommand)
}

func newApp() *app { return &app{} }

func (a *app) init(ctx context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
	cfg, err := curate.ParseConfig(in.InitializeParams.Config)
	if err != nil {
		return nil, err
	}
	info := in.Info()
	dir := info.DataDir
	if dir == "" { // a hub always gives one; without it (tests, a remote run) the state lives in a fresh temp dir
		if dir, err = os.MkdirTemp("", "enkeep-memory-"); err != nil {
			return nil, err
		}
	}
	st, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	tz := cfg.Timezone
	if tz == "" {
		tz = in.Hub.Timezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" {
		loc = time.Local
	}
	a.hub = hub
	a.dataDir = info.DataDir
	a.hubLoc = loc // the weekly report goes by the hub's own time zone
	if hl, err := time.LoadLocation(in.Hub.Timezone); err == nil && in.Hub.Timezone != "" {
		a.hubLoc = hl
	}
	a.member = in.Hub.Role == "member"
	// The knowledge base, and the user's Persona conversations (assistant.read; without the grant they are left out).
	// The hub's other sources (agent sessions, cards, to-dos, "about me", memory) over its HTTP API with the
	// extension's token; without one, none of them, each noted on the sources card.
	var collect curate.Collector
	var src kbsrc.Source = a.source
	if src == nil {
		withConversations := convsrc.With(kbsrc.Hub{H: hub, Ext: info.ID}, convsrc.Hub{H: hub}, loc)
		withConversations.Logf = func(f string, args ...any) { log.Printf("enkeep-memory: "+f, args...) }
		var h hubapi.Hub = hubapi.None{}
		if hh := hubapi.FromEnv(); hh != nil {
			hh.Call = hub.Call // assistant.memory (H2) is an RPC, not a route
			h = hh
		}
		a.hubAPI = h
		more := sources.With(withConversations, h, loc)
		more.Logf = withConversations.Logf
		src, collect = more, more
	}
	a.c = &curate.Curator{Src: src, Model: model.Hub{H: hub, Extension: info.ID}, St: st, Cfg: cfg, Loc: loc,
		Lang: in.Hub.Locale, Extension: info.ID, Logf: func(f string, args ...any) { log.Printf(f, args...) }, Live: &curate.Live{},
		Collect: collect, Hub: a.hubAPI, Cards: curate.PeopleCards{}}
	st.Lock() // nothing runs yet: the health page's first snapshot
	a.c.Publish()
	st.Unlock()
	go a.recover(ctx)
	go a.migrate(ctx)
	if !a.member {
		go a.schedule(ctx)
	}
	return nil, nil
}

// schedule runs the daily pass at daily_at for the day before, and the weekly pass on Mondays at weekly_at for the
// ISO week before. After a restart it catches up the most recent missed run of each, not every missed one.
func (a *app) schedule(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		a.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *app) tick(ctx context.Context) {
	c := a.c
	nowFn := c.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().In(c.Loc)
	at := func(hhmm string) time.Time {
		var h, m int
		fmt.Sscanf(hhmm, "%d:%d", &h, &m)
		return time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, c.Loc)
	}
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	a.releaseProfiles(ctx)
	// The model was unavailable to the last pass: nothing that needs it starts before the next try (State.ModelPause).
	// Then the day that could not run is tried again: by the replay from where it stopped, by rerun_range or the
	// daily pass as their own rules say.
	if _, wait := c.ModelWait(nowFn()); wait {
		return
	}
	a.retryAfterPause()
	// The first-run replay of the history (4.1) goes first, a day at a time, oldest first; a day that failed is
	// tried again on the next start.
	c.StartBackfill(now.Format("2006-01-02"))
	if next := a.backfillNext(); next != "" && !a.attempted("backfill@"+next) {
		a.start(ctx, "backfill", next)
		return
	}
	if !now.Before(at(c.Cfg.DailyAt)) && !a.ran("daily", yesterday) {
		a.start(ctx, "daily", yesterday)
		return
	}
	// rerun_range: only completed local days are rerun, one at a time with the current daily prompt. An early daily
	// must not suppress the formal pass after the day ends. Each day runs only once per prompt.
	days := c.Cfg.RerunDays()
	today := now.Format("2006-01-02")
	for i, day := range days {
		if day >= today {
			days = days[:i]
			break
		}
	}
	for _, day := range days {
		if !a.reran(day) && !a.attempted(day) {
			a.start(ctx, "daily", day)
			return
		}
	}
	monday := now.AddDate(0, 0, -int((now.Weekday()+6)%7))
	lastWeek := curate.ISOWeek(monday.AddDate(0, 0, -1).Format("2006-01-02"))
	weeklyDue := now.Weekday() != time.Monday || !now.Before(at(c.Cfg.WeeklyAt))
	if weeklyDue && !a.ran("weekly", lastWeek) {
		a.start(ctx, "weekly", lastWeek)
		return
	}
	// The weekly report to the user, once the week's pass has run (weekly_report.go).
	if err := a.weeklyTick(ctx, time.Now()); err != nil {
		log.Printf("enkeep-memory: weekly report: %v", err)
	}
	// After the days of rerun_range are digested with the current prompt, their judgements are filed once.
	if len(days) > 0 && a.reran(days[len(days)-1]) && !a.synthAfterDaily(days[len(days)-1]) && !a.attempted("synth@"+days[len(days)-1]) {
		a.start(ctx, "synth", days[len(days)-1])
	}
}

// backfillNext is the next day of the first-run replay, or "" when there is none to run.
func (a *app) backfillNext() string {
	a.c.St.Lock()
	defer a.c.St.Unlock()
	if b := a.c.St.S.Backfill; b != nil && b.Finished == "" {
		return b.Next
	}
	return ""
}

func (a *app) reran(day string) bool {
	a.c.St.Lock()
	marked := slices.Contains(a.c.St.S.Reruns, day+"@"+schema.DailyPrompt)
	a.c.St.Unlock()
	// A marker alone does not prove the day completed after its local day end.
	return marked && a.ran("daily", day)
}

// validDailyFinishLocked returns the finish time of a daily that really completed after its local day end.
// a.c.St must be locked by the caller.
func (a *app) validDailyFinishLocked(r *store.Run, day string) (time.Time, bool) {
	if r.Kind != "daily" || r.Period != day || r.Finished == "" || r.DryRun {
		return time.Time{}, false
	}
	started, startErr := time.Parse(time.RFC3339, r.Started)
	finished, finishErr := time.Parse(time.RFC3339, r.Finished)
	periodDay, dayErr := time.ParseInLocation("2006-01-02", day, a.c.Loc)
	if startErr != nil || finishErr != nil || dayErr != nil {
		return time.Time{}, false
	}
	if started.Before(periodDay.AddDate(0, 0, 1)) || finished.Before(started) {
		return time.Time{}, false
	}
	return finished, true
}

// synthAfterDaily reports whether a synth started after the latest valid daily for day finished.
func (a *app) synthAfterDaily(day string) bool {
	a.c.St.Lock()
	defer a.c.St.Unlock()
	var latest time.Time
	for _, r := range a.c.St.S.Runs {
		finished, ok := a.validDailyFinishLocked(r, day)
		if ok && finished.After(latest) {
			latest = finished
		}
	}
	if latest.IsZero() {
		return false
	}
	for _, r := range a.c.St.S.Runs {
		if r.Kind != "synth" || r.Period != day || r.Finished == "" || r.DryRun {
			continue
		}
		started, startErr := time.Parse(time.RFC3339, r.Started)
		finished, finishErr := time.Parse(time.RFC3339, r.Finished)
		if startErr == nil && finishErr == nil && !finished.Before(started) && !started.Before(latest) {
			return true
		}
	}
	return false
}

// triedKey is the a.tried key of a pass.
func triedKey(kind, period string) string {
	switch kind {
	case "daily":
		return period
	case "synth", "backfill":
		return kind + "@" + period
	}
	return kind + " " + period
}

// retryAfterPause forgets, once the pause is over, that the passes the model was unavailable to were tried: the
// tick starts them again.
func (a *app) retryAfterPause() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.paused) == 0 {
		return
	}
	a.tried = slices.DeleteFunc(a.tried, func(k string) bool { return slices.Contains(a.paused, k) })
	a.paused = nil
}

// attempted says whether a daily run of the day already started since this process did: a rerun that failed is
// not retried every minute, the next start (or a change of settings) tries again.
func (a *app) attempted(day string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Contains(a.tried, day)
}

func (a *app) ran(kind, period string) bool {
	a.c.St.Lock()
	defer a.c.St.Unlock()
	for _, r := range a.c.St.S.Runs {
		if r.Kind != kind || r.Period != period || r.Finished == "" || r.DryRun {
			continue
		}
		if kind == "daily" {
			if _, ok := a.validDailyFinishLocked(r, period); !ok {
				continue
			}
			return true
		}
		if kind == "synth" {
			started, startErr := time.Parse(time.RFC3339, r.Started)
			finished, finishErr := time.Parse(time.RFC3339, r.Finished)
			day, dayErr := time.ParseInLocation("2006-01-02", period, a.c.Loc)
			if startErr != nil || finishErr != nil || dayErr != nil {
				continue // an unknown time does not prove the synth completed
			}
			if finished.Before(started) || started.Before(day.AddDate(0, 0, 1)) {
				continue // an unfinished or inverted run is not the formal completion for this period
			}
		}
		return true
	}
	return false
}

// start runs a pass in the background; one at a time.
func (a *app) start(ctx context.Context, kind, period string) bool {
	a.mu.Lock()
	if a.running != "" {
		a.mu.Unlock()
		return false
	}
	a.running = kind + " " + period
	if kind == "daily" {
		a.tried = append(a.tried, period)
	}
	if kind == "synth" {
		a.tried = append(a.tried, "synth@"+period)
	}
	if kind == "backfill" {
		a.tried = append(a.tried, "backfill@"+period)
	}
	a.mu.Unlock()
	prompt := map[string]string{"daily": schema.DailyPrompt, "backfill": schema.DailyPrompt, "weekly": schema.WeeklyPrompt, "synth": schema.SynthPrompt}[kind]
	log.Printf("enkeep-memory: %s %s starts (prompt=%s ruleset=%s)", kind, period, prompt, schema.Ruleset)
	go func() {
		var err error
		if kind == "backfill" {
			_, err = a.c.BackfillStep(ctx)
		} else if kind == "daily" {
			_, err = a.c.RunDaily(ctx, period, false)
			if err == nil {
				a.c.St.Lock()
				if key := period + "@" + schema.DailyPrompt; !slices.Contains(a.c.St.S.Reruns, key) {
					a.c.St.S.Reruns = append(a.c.St.S.Reruns, key)
				}
				err = a.c.St.Save()
				a.c.St.Unlock()
			}
		} else if kind == "synth" {
			_, err = a.c.RunSynth(ctx, period, false)
		} else {
			_, err = a.c.RunWeekly(ctx, period, false)
			if days, werr := curate.WeekDays(period); err == nil && werr == nil {
				_, err = a.c.RunSynth(ctx, days[6], false) // the week's judgements, filed under patterns
			}
		}
		a.mu.Lock()
		a.running = ""
		a.lastErr = ""
		if errors.Is(err, curate.ErrModelDown) {
			a.paused = append(a.paused, triedKey(kind, period)) // tried again after the pause, not only at the next start
		}
		if err != nil {
			a.lastErr = kind + " " + period + "：" + err.Error()
			log.Printf("enkeep-memory %s %s: %v", kind, period, err)
		}
		a.mu.Unlock()
		a.badge()
		if a.hub != nil {
			_ = a.hub.Invalidate("enkeep-memory")
			_ = a.hub.Invalidate("enkeep-memory-today")
		}
	}()
	return true
}

// publish takes a new snapshot for the pages after the user changed something. A run that took the lock meanwhile
// takes its own when it ends: no waiting for it here.
func (a *app) publish() {
	if !a.c.St.TryLock() {
		return
	}
	a.c.Publish()
	a.c.St.Unlock()
}

func (a *app) badge() {
	if a.hub == nil {
		return
	}
	n := len(a.pending())
	text := ""
	if n > 0 {
		text = fmt.Sprint(n)
	}
	_ = a.hub.Badge("enkeep-memory", text, "")
}

func (a *app) pending() []*store.Proposal {
	st, done := a.read()
	defer done()
	var out []*store.Proposal
	for _, p := range st.Proposals {
		if p.Status == "pending" || p.Status == "later" {
			out = append(out, p)
		}
	}
	return out
}

// event: kb.forgotten removes everything derived from the forgotten source; kb.changed is only counted (the next
// daily pass reads what changed).
func (a *app) event(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "kb.forgotten":
		var e wire.KBForgottenEvent
		if err := json.Unmarshal(params, &e); err != nil {
			return nil, err
		}
		n, err := a.c.Forget(e.Source)
		log.Printf("enkeep-memory: source %q forgotten, %d derived records removed", e.Source, n)
		return nil, err
	case "kb.changed":
		a.mu.Lock()
		a.changed++
		a.mu.Unlock()
	}
	return nil, nil
}

func short(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return string(r)
}

// recover replays interrupted proposal writes without holding up initialization. Curator.Recover holds the store's
// lock; individual failures are logged and left for the next recovery or proposal retry.
func (a *app) recover(ctx context.Context) {
	if err := a.c.Recover(ctx); err != nil {
		log.Printf("enkeep-memory: proposal recovery will be tried again: %v", err)
	}
}

// migrate cleans a state written before source roles, once, holding the store's lock (migrateSources). A failure is
// logged and leaves the state as it was: the next start tries again.
func (a *app) migrate(ctx context.Context) {
	st := a.c.St
	st.Lock()
	defer st.Unlock()
	sc, err := migrateSources(ctx, st, a.c.Src, time.Now())
	switch {
	case err != nil:
		log.Printf("enkeep-memory: clean of the old state not done, tried again at the next start: %v", err)
	case sc != nil:
		log.Printf("enkeep-memory: cleaned the old state: %s", cleanSummary(sc))
		a.c.Publish()
	}
}

// releaseProfiles asks the hub whether it takes person profiles (at most once a day, only while a card waits;
// curate.CheckProfileCapability writes nothing to anyone) and lets the waiting cards through when it does.
func (a *app) releaseProfiles(ctx context.Context) {
	a.c.CheckProfileCapability(ctx)
	if !a.c.St.TryLock() {
		return // a run holds the state: the next tick
	}
	defer a.c.St.Unlock()
	if a.c.ReleaseProfiles() > 0 {
		if err := a.c.St.Save(); err != nil {
			log.Printf("enkeep-memory: release person profiles: %v", err)
		}
	}
}
