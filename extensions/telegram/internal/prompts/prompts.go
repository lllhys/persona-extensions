// Package prompts is the Telegram connector's tool descriptions (mcp/<tool>.md: the description, an empty line, the
// input schema in a ```json block; as in Persona's server/internal/prompts) and its inbox kinds' descriptions.
package prompts

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

//go:embed mcp/*.md
var files embed.FS

// MCPTool is one MCP tool as the model sees it: what it is for and its arguments (with their descriptions).
type MCPTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage // compact
}

var mcpTools = loadMCP()

const schemaFence = "\n\n```json\n"

func loadMCP() map[string]MCPTool {
	out := map[string]MCPTool{}
	err := fs.WalkDir(files, "mcp", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		desc, rest, ok := strings.Cut(string(raw), schemaFence)
		schema, ok2 := strings.CutSuffix(rest, "\n```\n")
		if !ok || !ok2 || desc == "" {
			return fmt.Errorf("%s: want the description, an empty line and a ```json block with the input schema", p)
		}
		var b bytes.Buffer
		if err := json.Compact(&b, []byte(schema)); err != nil {
			return fmt.Errorf("%s: input schema: %w", p, err)
		}
		name := strings.TrimSuffix(path.Base(p), ".md")
		out[name] = MCPTool{Name: name, Description: desc, InputSchema: b.Bytes()}
		return nil
	})
	if err != nil {
		panic("prompts: " + err.Error())
	}
	return out
}

// Tool is the MCP tool named name; an unknown name is a bug and panics.
func Tool(name string) MCPTool {
	t, ok := mcpTools[name]
	if !ok {
		panic("prompts: no MCP tool " + name)
	}
	return t
}

// ToolNames lists every tool described here (for tests).
func ToolNames() []string {
	out := make([]string, 0, len(mcpTools))
	for n := range mcpTools {
		out = append(out, n)
	}
	return out
}

// KindDescriptions is what each kind of the connector's inbox things means, for the models (the manifest's
// kind_descriptions): the hub writes 「类型：」 before it, as Persona's prompts/inbox/kinds/telegram.md read.
var KindDescriptions = map[string]string{
	"dm":      "Telegram 里有人私聊用户，还没读",
	"mention": "Telegram 群里 @ 了用户，或回复了用户的消息",
}
