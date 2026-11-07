package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/memory"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// importEnkeep is the one-off migration from enkeep (design 6.18 §7): it reads an export (a JSON array of
// memory.EnkeepRecord, already classified) into a state directory's memory, with the import day as the start of
// every imported entry's grace. Run it with the extension stopped, on its data directory.
func importEnkeep(args []string) error {
	fs := flag.NewFlagSet("import-enkeep", flag.ContinueOnError)
	file := fs.String("file", "", "the enkeep export: a JSON array of records")
	stateDir := fs.String("state", "", "the extension's state directory")
	day := fs.String("day", "", "the import day, YYYY-MM-DD")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" || *stateDir == "" || *day == "" {
		return fmt.Errorf("-file, -state and -day are required")
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	var recs []memory.EnkeepRecord
	if err := json.Unmarshal(raw, &recs); err != nil {
		return fmt.Errorf("%s: %w", *file, err)
	}
	st, err := store.Open(*stateDir)
	if err != nil {
		return err
	}
	st.Lock()
	defer st.Unlock()
	n, joined, err := memory.Import(st.S.Memory, recs, *day)
	if err != nil {
		return err
	}
	st.S.Memory.Tick(*day, curate.Defaults().Memory)
	if err := st.Save(); err != nil {
		return err
	}
	levels := memory.Levels(memory.Inject(st.S.Memory, curate.Defaults().Memory))
	fmt.Printf("imported %d records (%d joined existing entries as side evidence); resident %d, recall %d, search %d, none %d\n",
		n, joined, levels[memory.Resident], levels[memory.Recall], levels[memory.Search], levels[memory.None])
	return nil
}
