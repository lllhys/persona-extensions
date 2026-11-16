package main

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/mcp"
)

type manifestDoc struct {
	Contributes struct {
		MCP []struct {
			ID       string   `json:"id"`
			Requires []string `json:"requires"`
			Allow    []string `json:"allow"`
		} `json:"mcp_servers"`
	} `json:"contributes"`
}

func TestConformance(t *testing.T) {
	// exttest gives Init a temporary data directory, so the loop pulls against a hub it cannot reach and stores
	// nothing; both contributions must still draw.
	exttest.Check(t, manifest, exttest.Serve(t, program()))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
}

func TestReadOnlyToolsAreAllowed(t *testing.T) {
	var d manifestDoc
	if err := json.Unmarshal(manifest, &d); err != nil {
		t.Fatal(err)
	}
	var read []string
	for _, tool := range mcp.Tools() {
		if tool.Read {
			read = append(read, tool.Name)
		}
	}
	allow := slices.Clone(d.Contributes.MCP[0].Allow)
	slices.Sort(read)
	slices.Sort(allow)
	if !slices.Equal(read, allow) {
		t.Fatalf("read-only tools %v, manifest allow %v", read, allow)
	}
}

func TestUsageNamesEveryRequirement(t *testing.T) {
	var d manifestDoc
	if err := json.Unmarshal(manifest, &d); err != nil {
		t.Fatal(err)
	}
	for _, s := range d.Contributes.MCP {
		for _, word := range s.Requires {
			if !strings.Contains(usage, word) {
				t.Errorf("the help text does not contain %q", word)
			}
		}
	}
}

func TestDispatch(t *testing.T) {
	var out bytes.Buffer
	handled, err := dispatch([]string{"help"}, strings.NewReader(""), &out)
	if err != nil || !handled || !strings.Contains(out.String(), "mcp") {
		t.Fatalf("help: handled %v err %v out %q", handled, err, out.String())
	}
	handled, err = dispatch(nil, strings.NewReader(""), &out)
	if err != nil || handled {
		t.Fatalf("no command should belong to ext.Main, handled %v err %v", handled, err)
	}
}
