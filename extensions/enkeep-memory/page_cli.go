package main

// The page subcommand draws the v4 pages and the weekly report from a state on disk, without a hub and without
// changing the state: for screenshots (the views are the JSON the hub's apps draw) and for checking a replay's state
// (how many entries, cards, speaker lines and facts the check page lists).
//
//	enkeep-memory page -state DIR [-data DIR] -out DIR [-today YYYY-MM-DD] [-tz ZONE]

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

func pageCmd(args []string) error {
	fs := flag.NewFlagSet("page", flag.ContinueOnError)
	stateDir := fs.String("state", "", "the state directory (state.json)")
	dataDir := fs.String("data", "", "where the exit files are (default: the state directory)")
	out := fs.String("out", "", "where the views go")
	today := fs.String("today", "", "the day the weekly report is made on (default today)")
	tz := fs.String("tz", "UTC", "the hub's time zone")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *stateDir == "" || *out == "" {
		return fmt.Errorf("page: -state and -out are needed")
	}
	if _, err := os.Stat(filepath.Join(*stateDir, "state.json")); err != nil {
		return fmt.Errorf("page: no state.json in %s", *stateDir)
	}
	loc, err := time.LoadLocation(*tz)
	if err != nil {
		return err
	}
	st, err := store.Open(*stateDir)
	if err != nil {
		return err
	}
	a := &app{c: &curate.Curator{St: st, Cfg: curate.Defaults(), Loc: loc, Live: &curate.Live{}}, exitDir: *dataDir, hubLoc: loc}
	day := *today
	if day == "" {
		day = time.Now().In(loc).Format("2006-01-02")
	}
	views, counts := drawAll(a, day)
	for name, v := range views {
		if err := writeJSON(filepath.Join(*out, name+".json"), v); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(*out, "counts.json"), counts); err != nil {
		return err
	}
	fmt.Printf("%d views in %s\n", len(views), *out)
	b, _ := json.MarshalIndent(counts, "", " ")
	fmt.Println(string(b))
	return nil
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// drawAll draws every v4 page in both languages (name "<lang>/<page>"), checks each against the hub's view check,
// and counts what the check page lists and what the weekly report holds.
func drawAll(a *app, today string) (map[string]*wire.View, map[string]any) {
	ctx := context.Background()
	st, done := a.read()
	var firstCard, firstSource, firstEntry string
	if cards := pickCards(st); len(cards) > 0 {
		firstCard = cards[0][0]
	}
	names, by := speakerSources(st)
	if len(names) > 0 {
		firstSource = names[0]
	}
	for _, f := range facetsShown {
		if es := pickFacet(st, f); len(es) > 0 && firstEntry == "" {
			firstEntry = es[0].ID
		}
	}
	counts := map[string]any{}
	parts := map[string]int{}
	for k, f := range uFacets {
		es := pickFacet(st, f)
		abs := 0
		for _, e := range es {
			if e.Grain == entry.Abstract {
				abs++
			}
		}
		parts[k] = len(es)
		parts[k+"_abstract"] = abs
	}
	cards := pickCards(st)
	parts["u7_cards"] = len(cards)
	lines := 0
	for _, c := range cards {
		lines += len(cardLines(st, c))
	}
	parts["u7_lines"] = lines
	for _, n := range names {
		parts["u8_"+n] = len(by[n])
	}
	parts["f1"] = len(pickFacts(st, today))
	counts["check"] = parts
	lang := a.c.Cfg.Language
	weekly := map[string]any{}
	hubLoc := a.hubLoc
	if hubLoc == nil {
		hubLoc = a.c.Loc
	}
	for _, l := range []string{"zh-Hans", "en"} {
		if lang != "" && l != lang {
			continue
		}
		t, _ := time.ParseInLocation("2006-01-02", today, hubLoc)
		monday := t.AddDate(0, 0, -int((t.Weekday()+6)%7))
		week := curate.ISOWeek(monday.AddDate(0, 0, -1).Format("2006-01-02"))
		days, _ := curate.WeekDays(week)
		rep := buildWeekly(st, week, days, today, l, hubLoc, nil)
		weekly[l] = rep
	}
	counts["weekly"] = weekly
	done()

	pages := map[string]page{
		"start": {Tab: tabStart}, "people": {Tab: tabPeople}, "switches": {Tab: tabSwitches}, "onboarding": {Tab: tabStart, Open: "onboarding"},
		"check-u7": {Tab: tabCheck, Check: "u7"}, "check-u8": {Tab: tabCheck, Check: "u8"}, "check-f1": {Tab: tabCheck, Check: "f1"},
		"types-judgement": {Tab: tabFacets, Open: "types:judgement"}, "k2": {Tab: tabSwitches, Open: "k2"},
	}
	for _, f := range facetsShown {
		pages["facet-"+string(f)] = page{Tab: tabFacets, Facet: string(f)}
	}
	for k := range uFacets {
		pages["check-"+k] = page{Tab: tabCheck, Check: k}
	}
	if firstCard != "" {
		pages["card"] = page{Tab: tabPeople, Open: "card:" + firstCard}
		pages["check-u7-card"] = page{Tab: tabCheck, Check: "u7", Sub: firstCard}
	}
	if firstSource != "" {
		pages["check-u8-source"] = page{Tab: tabCheck, Check: "u8", Sub: firstSource}
	}
	if firstEntry != "" {
		pages["entry"] = page{Tab: tabFacets, Open: "e:" + firstEntry}
	}
	views := map[string]*wire.View{}
	var bad []string
	for _, locale := range []string{"zh-Hans", "en"} {
		for name, p := range pages {
			r := &ext.UIRequest{UIParams: wire.UIParams{State: ext.EncodeState(p), Locale: locale, UIVersion: 6}}
			v, err := a.home(ctx, r)
			if err != nil {
				bad = append(bad, name+": "+err.Error())
				continue
			}
			if err := wire.CheckView(v); err != nil {
				bad = append(bad, locale+"/"+name+": "+err.Error())
			}
			views[locale+"/"+name] = v
		}
	}
	counts["view_check_failures"] = bad
	return views, counts
}
