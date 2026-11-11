// ATS is an agent task queue for Persona, in one binary with two roles:
//
//	ats hub        the hub program: the queue and its dispatcher, the queue page, the HTTP API the assistant
//	               calls, and the write-back of a task's outcome to its to-do
//	ats node       the program on a computer: git worktrees and acceptance commands
//	ats manifest   prints the packaged extension manifest
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

func packagedManifest(raw []byte) []byte {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		panic("invalid embedded extension manifest")
	}
	delete(fields, "tags")
	delete(fields, "replaces")
	out, err := json.Marshal(fields)
	if err != nil {
		panic("cannot encode embedded extension manifest")
	}
	return out
}

func main() {
	var m struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	_ = json.Unmarshal(manifest, &m)

	role := ""
	if len(os.Args) > 1 {
		role = os.Args[1]
	}
	switch role {
	case "node":
		ext.Main(nodeProgram(m.ID, m.Version))
	case "hub", "manifest":
		ext.Main(newATSApp(nil).program(m.ID, m.Version))
	default:
		fmt.Fprintln(os.Stderr, "usage: ats hub | node | manifest   (started by the Persona hub, not by hand)")
		os.Exit(2)
	}
}
