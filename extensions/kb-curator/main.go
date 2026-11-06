// kb-curator: curates the Persona knowledge base daily and weekly, in the manner of a provenance-checked pipeline.
// Every source goes through one triage; admitted material is digested into facts, practices and knowledge with
// citations a program checks; a typed entity graph is built alongside; changes to the knowledge base are proposals
// that a person (or, once proven, the curator itself) applies, each one logged and undoable.
//
//	kb-curator serve                       the extension program (the hub starts it)
//	kb-curator dryrun -fixture DIR -from YYYY-MM-DD -to YYYY-MM-DD [-week] [-out DIR] [-state DIR]
//	                                       run the pipeline over a synthetic knowledge base with the stub model;
//	                                       nothing is written anywhere but -out and -state
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"

	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var manifest []byte

func main() {
	if len(os.Args) > 1 && os.Args[1] == "dryrun" {
		if err := dryrun(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "kb-curator dryrun:", err)
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
			"kb-curator":        {Render: a.home, Action: a.homeAction},
			"kb-curator-today":  {Render: a.today},
			"kb-curator-health": {Render: a.health, Action: a.healthAction},
		},
	}
}
