package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/store"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/stub"
)

// dryrun runs the daily pass over a range of days and then the weekly pass, against a fixture directory with the
// stub model. Writes change only the fixture's in-memory copy (never its files) and are printed; the state goes to -state (a
// temporary directory by default) and the reports to -out.
func dryrun(args []string) error {
	fs := flag.NewFlagSet("dryrun", flag.ContinueOnError)
	fixture := fs.String("fixture", "testdata/sample", "fixture directory (synthetic knowledge base)")
	from := fs.String("from", "", "first day, YYYY-MM-DD")
	to := fs.String("to", "", "last day, YYYY-MM-DD (default: from)")
	week := fs.Bool("week", true, "run the weekly pass for the week of the last day afterwards")
	out := fs.String("out", "", "directory for the reports (default: print them)")
	stateDir := fs.String("state", "", "state directory (default: a new temporary one)")
	mode := fs.String("mode", "review", "mode: shadow | review")
	small := fs.Int("admit-all-below", curate.Defaults().AdmitAllBelowTokens, "admit_all_below_tokens (0: always apply the rules)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return fmt.Errorf("-from is required")
	}
	if *to == "" {
		*to = *from
	}
	fx, err := kbsrc.LoadFixture(*fixture)
	if err != nil {
		return err
	}
	if *stateDir == "" {
		if *stateDir, err = os.MkdirTemp("", "kb-curator-dryrun-"); err != nil {
			return err
		}
	}
	st, err := store.Open(*stateDir)
	if err != nil {
		return err
	}
	cfg := curate.Defaults()
	cfg.Mode = *mode
	cfg.AdmitAllBelowTokens = *small
	m := &stub.Model{}
	c := &curate.Curator{Src: fx, Model: m, St: st, Cfg: cfg, Loc: time.UTC}
	ctx := context.Background()
	var reports []*store.Run
	for d := *from; d <= *to; d = next(d) {
		r, err := c.RunDaily(ctx, d, false)
		if err != nil {
			return fmt.Errorf("%s: %w", d, err)
		}
		reports = append(reports, r)
	}
	if *week {
		r, err := c.RunWeekly(ctx, curate.ISOWeek(*to), false)
		if err != nil {
			return err
		}
		reports = append(reports, r)
	}
	// Accept every pending knowledge-base proposal, to show the writes the curator would make (recorded only).
	accepted := 0
	for _, p := range st.S.Proposals {
		if p.Status == "pending" && curate.ProposalKinds[p.Kind].KB {
			if _, err := c.Decide(ctx, p.ID, "accept", ""); err == nil {
				accepted++
			}
		}
	}
	for _, r := range reports {
		if *out != "" {
			if err := os.MkdirAll(*out, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(*out, r.Kind+"-"+r.Period+".md"), []byte(r.Report), 0o644); err != nil {
				return err
			}
		} else {
			fmt.Println(r.Report)
		}
	}
	counts := map[string]int{}
	for _, p := range st.S.Proposals {
		counts[p.Kind+":"+p.Status]++
	}
	fmt.Printf("state: %s\nmodel calls: %v\nitems: %d, graph nodes: %d, assertions: %d, rejections: %d\nproposals: %v\n",
		*stateDir, m.Calls, len(st.S.Items), len(st.S.Graph.Nodes), len(st.S.Graph.Assertions), len(st.S.Rejections), counts)
	fmt.Printf("accepted %d proposals in the dry run; writes made to the in-memory copy (files untouched):\n", accepted)
	for _, w := range fx.Writes() {
		fmt.Printf("  kb.update %s/%s %s\n", w.Kind, w.ID, describe(w))
	}
	return nil
}

func next(day string) string {
	t, _ := time.Parse("2006-01-02", day)
	return t.AddDate(0, 0, 1).Format("2006-01-02")
}

func describe(w any) string {
	b, _ := json.Marshal(w)
	return string(b)
}
