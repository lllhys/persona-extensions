// Self-contained Go native ATS task scheduler extension for Persona:
//
//	ats hub                 Persona extension protocol server (panel UI, quota sync, outcome decision write-back)
//	ats mcp [--enqueue]     stdio JSON-RPC 2.0 MCP server (list_tasks, get_task, get_evidence, and opt-in enqueue_task)
//	ats --mcp [--enqueue]   alias for `ats mcp`
//	ats manifest            prints the packaged extension manifest
package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

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

	if len(os.Args) > 1 {
		arg1 := strings.TrimSpace(os.Args[1])
		switch arg1 {
		case "-h", "--help", "help":
			printUsage(os.Stdout)
			return
		case "mcp", "--mcp":
			runMCPCommand(m.Version, os.Args[2:])
			return
		}
	}

	app := newATSApp(nil)
	ext.Main(app.program(m.ID, m.Version))
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, "Usage: ats <command> [options]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  hub                 Run the Persona extension runtime server")
	fmt.Fprintln(w, "  mcp [--enqueue]     Run the stdio MCP server (read-only by default; --enqueue enables enqueue_task)")
	fmt.Fprintln(w, "  manifest            Print the packaged extension manifest")
}

func runMCPCommand(version string, args []string) {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	var allowEnqueue bool
	var dataDir string
	fs.BoolVar(&allowEnqueue, "enqueue", false, "enable the enqueue_task write tool")
	fs.StringVar(&dataDir, "data-dir", "", "directory storing persistent ATS queue state")
	fs.SetOutput(os.Stderr)
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(os.Stdout, "Usage: ats mcp [--enqueue] [--data-dir <dir>]")
		return
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if strings.TrimSpace(dataDir) == "" {
		dataDir = defaultDataDir()
	}
	q, err := NewQueue(dataDir, DefaultQueueConfig())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ats mcp: %v\n", err)
		os.Exit(1)
	}
	if err := ServeMCP(q, os.Stdin, os.Stdout, MCPOptions{
		AllowEnqueue: allowEnqueue,
		Version:      version,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "ats mcp: %v\n", err)
		os.Exit(1)
	}
}
