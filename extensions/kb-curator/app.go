package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/model"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/store"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

type app struct {
	mu        sync.Mutex
	c         *curate.Curator
	hub       *ext.Hub
	running   string // the run in progress, if any
	lastErr   string
	member    bool   // a member hub of a group: the curator runs only on the centre
	changed   int    // kb.changed events since start
	dataDir   string // the extension's data directory (trials leave their report there)
	lastTrial string // the last trial's summary (dryrun_hub.go)
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
		if dir, err = os.MkdirTemp("", "kb-curator-"); err != nil {
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
	a.member = in.Hub.Role == "member"
	a.c = &curate.Curator{Src: kbsrc.Hub{H: hub}, Model: model.Hub{H: hub, Extension: info.ID}, St: st, Cfg: cfg, Loc: loc,
		Lang: in.Hub.Locale, Extension: info.ID, Logf: func(f string, args ...any) { log.Printf(f, args...) }}
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
	now := time.Now().In(c.Loc)
	at := func(hhmm string) time.Time {
		var h, m int
		fmt.Sscanf(hhmm, "%d:%d", &h, &m)
		return time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, c.Loc)
	}
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	if !now.Before(at(c.Cfg.DailyAt)) && !a.ran("daily", yesterday) {
		a.start(ctx, "daily", yesterday)
		return
	}
	monday := now.AddDate(0, 0, -int((now.Weekday()+6)%7))
	lastWeek := curate.ISOWeek(monday.AddDate(0, 0, -1).Format("2006-01-02"))
	weeklyDue := now.Weekday() != time.Monday || !now.Before(at(c.Cfg.WeeklyAt))
	if weeklyDue && !a.ran("weekly", lastWeek) {
		a.start(ctx, "weekly", lastWeek)
	}
}

func (a *app) ran(kind, period string) bool {
	a.c.St.Lock()
	defer a.c.St.Unlock()
	for _, r := range a.c.St.S.Runs {
		if r.Kind == kind && r.Period == period && r.Finished != "" && !r.DryRun {
			return true
		}
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
	a.mu.Unlock()
	go func() {
		var err error
		if kind == "daily" {
			_, err = a.c.RunDaily(ctx, period, false)
		} else {
			_, err = a.c.RunWeekly(ctx, period, false)
		}
		a.mu.Lock()
		a.running = ""
		a.lastErr = ""
		if err != nil {
			a.lastErr = kind + " " + period + "：" + err.Error()
			log.Printf("kb-curator %s %s: %v", kind, period, err)
		}
		a.mu.Unlock()
		a.badge()
		if a.hub != nil {
			_ = a.hub.Invalidate("kb-curator")
			_ = a.hub.Invalidate("kb-curator-today")
		}
	}()
	return true
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
	_ = a.hub.Badge("kb-curator", text, "")
}

func (a *app) pending() []*store.Proposal {
	a.c.St.Lock()
	defer a.c.St.Unlock()
	var out []*store.Proposal
	for _, p := range a.c.St.S.Proposals {
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
		log.Printf("kb-curator: source %q forgotten, %d derived records removed", e.Source, n)
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
