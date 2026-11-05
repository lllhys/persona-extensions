package github

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// The tools' descriptions and input schemas, as the hub had them in its prompts/mcp/github/ (one file a tool: the
// description, an empty line, then the input schema in a ```json block). Kept word for word: the model sees the
// same tools whether the hub's own connector or this extension serves them.
//
//go:embed prompts/*.md
var promptFiles embed.FS

// mcpTool is one tool as the model sees it.
type mcpTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage // compact
}

const schemaFence = "\n\n```json\n"

var mcpTools = loadTools()

func loadTools() map[string]mcpTool {
	out := map[string]mcpTool{}
	err := fs.WalkDir(promptFiles, "prompts", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := promptFiles.ReadFile(p)
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
		out[name] = mcpTool{Name: name, Description: desc, InputSchema: b.Bytes()}
		return nil
	})
	if err != nil {
		panic("github prompts: " + err.Error())
	}
	return out
}

// toolPrompt is the tool named name; an unknown name is a bug and panics.
func toolPrompt(name string) mcpTool {
	t, ok := mcpTools[name]
	if !ok {
		panic("github prompts: no tool " + name)
	}
	return t
}
