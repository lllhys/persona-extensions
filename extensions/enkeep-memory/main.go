// enkeep-memory: the user's memory, and the Persona knowledge base curated daily and weekly, in the manner of a
// provenance-checked pipeline.
// Every source goes through one triage; admitted material is digested into facts, practices and knowledge with
// citations a program checks; a typed entity graph is built alongside; changes to the knowledge base are proposals
// that a person (or, once proven, the curator itself) applies, each one logged and undoable.
//
//	enkeep-memory serve                       the extension program (the hub starts it)
//	enkeep-memory dryrun -fixture DIR -from YYYY-MM-DD -to YYYY-MM-DD [-week] [-out DIR] [-state DIR]
//	                                       run the pipeline over a synthetic knowledge base with the stub model;
//	                                       nothing is written anywhere but -out and -state
//	enkeep-memory clean-sources -state DIR -kb DIR [-dry] [-keep 3] [-reject-others]
//	                                       drop from an existing state what does not rest on the user's own words
//	                                       (backs state.json up first); with the extension stopped;
//	                                       -reject-others also marks what rests only on other people's words rejected
//	enkeep-memory migrate -state DIR       move the old judgement layer (judgements, patterns, traits, rules) into
//	                                       entries, with the extension stopped (backs state.json up first; idempotent)
//	enkeep-memory offline -kb DIR [-conversations DIR] -from YYYY-MM-DD [-to YYYY-MM-DD] -state DIR -out DIR [-week]
//	                                       rerun the curation on a read-only copy of a knowledge base with a real
//	                                       model through a local command line, for evaluation (README)
//	enkeep-memory accept -state DIR -out DIR [-day YYYY-MM-DD] [-kb DIR] [-judge pi|none] [-model M] [-save]
//	                                       the acceptance readings and samples of one state (design §9): R1…R8,
//	                                       R4.<type>, F1, U1…U8, the empty-talk and abstract checks, the F1 and
//	                                       speaker samples, pre-judged by a non-Anthropic model
//	enkeep-memory smoke -agents-dir DIR -from D -to D -state DIR -out DIR
//	                                       the regression smoke (9.5): questions drawn by position from the user's
//	                                       answered cards, predicted without and with the curated results
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"

	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var authorManifest []byte

var manifest = packagedManifest(authorManifest)

// packagedManifest is the runtime manifest: without the source-only catalog metadata strict older hubs refuse.
func packagedManifest(raw []byte) []byte {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		panic(err)
	}
	delete(fields, "icon")
	delete(fields, "tags")
	delete(fields, "replaces")
	data, err := json.Marshal(fields)
	if err != nil {
		panic(err)
	}
	return data
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "page" {
		if err := pageCmd(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "enkeep-memory page:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "dryrun" {
		if err := dryrun(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "enkeep-memory dryrun:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "offline" {
		if err := offline(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "enkeep-memory offline:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "review" {
		if err := review(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "enkeep-memory review:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "accept" {
		if err := acceptCmd(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "enkeep-memory accept:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "smoke" {
		if err := smokeCmd(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "enkeep-memory smoke:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "clean-sources" {
		if err := cleanSources(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "enkeep-memory clean-sources:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err := migrate(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "enkeep-memory migrate:", err)
			os.Exit(1)
		}
		return
	}
	ext.Main(program(newApp()))
}

func program(a *app) ext.Program {
	var m struct{ ID, Version string }
	_ = json.Unmarshal(manifest, &m)
	return ext.Program{
		ID: m.ID, Version: m.Version, Manifest: manifest,
		Init:       a.init,
		Namespaces: map[string]ext.Handler{"event": a.event},
		UI: map[string]ext.Page{
			"enkeep-memory":        {Render: a.home, Action: a.homeAction},
			"enkeep-memory-today":  {Render: a.today},
			"enkeep-memory-health": {Render: a.health, Action: a.healthAction},
		},
	}
}
