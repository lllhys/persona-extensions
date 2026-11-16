package main

import (
	"flag"
	"fmt"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// migrate moves the old judgement layer of a state into entries (curate.Migrate) and saves it. It backs the state up
// before the first migration and does nothing the second time.
func migrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	dir := fs.String("state", "", "the state directory (the extension stopped)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return fmt.Errorf("-state is required")
	}
	st, err := store.Open(*dir)
	if err != nil {
		return err
	}
	c := &curate.Curator{St: st, Cfg: curate.Defaults(), Loc: time.UTC}
	st.Lock()
	defer st.Unlock()
	before := len(st.S.Entries)
	rep, err := c.Migrate()
	if err != nil {
		return err
	}
	c.Recount()
	if err := st.Save(); err != nil {
		return err
	}
	fmt.Printf("judgements %d, patterns %d, traits %d, rules %d → entries from them %d (new this run %d); entries %d → %d; backup %q\n",
		rep.Judgements, rep.Patterns, rep.Traits, rep.Rules, rep.Entries, rep.New, before, len(st.S.Entries), rep.Backup)
	return nil
}
