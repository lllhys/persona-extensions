// Bypass auto-allow (docs/extensions/claude-bypass-allow.md): Claude Code in bypassPermissions mode still stops on some
// Bash calls (its bypass-immune safety checks, mostly an rm whose target it cannot resolve), and an unattended agent
// waits there for a person. On the computers you pick, this extension gives Claude Code a PermissionRequest hook that
// allows those calls, and only those: Bash, in a bypassPermissions session, not about one of your own ask or deny rules.
//
// One program, three roles:
//
//	claude-bypass-allow hub     the hub's: the extension's page (hub.go)
//	claude-bypass-allow node    each computer's: installs and removes the hook, keeps its heartbeat (node.go)
//	claude-bypass-allow hook …  what Claude Code runs on PermissionRequest (hook.go)
package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"os"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

//go:embed persona-extension.json
var manifest []byte

func main() {
	var m struct{ ID, Version string }
	_ = json.Unmarshal(manifest, &m)
	role := ""
	if len(os.Args) > 1 {
		role = os.Args[1]
	}
	switch role {
	case "hook":
		fs := flag.NewFlagSet("hook", flag.ContinueOnError)
		var a hookArgs
		fs.StringVar(&a.ConfigDir, "config-dir", "", "the Claude config directory the entry is in")
		fs.StringVar(&a.Data, "data", "", "the node program's data directory")
		fs.String("persona-extension", "", "the entry's tag")
		fs.SetOutput(os.Stderr)
		if fs.Parse(os.Args[2:]) != nil {
			return // a hook that cannot read its flags stays silent: Claude Code asks as usual
		}
		runHook(a, os.Stdin, os.Stdout, time.Now())
	case "node":
		n := &node{}
		ext.Main(n.program(m.ID, m.Version))
		n.cleanup()
	default:
		ext.Main((&hubApp{}).program(m.ID, m.Version))
	}
}
