package x

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"text/template"
)

// The tools' descriptions and input schemas (one file a tool, as the GitHub extension has them: the description, an
// empty line, then the input schema in a ```json block).
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

// loadTools reads the tool files (prompts/<tool>.md for each of toolNames); the grok_*.md files are Grok's prompts
// (grokPrompts), not tools.
func loadTools() map[string]mcpTool {
	out := map[string]mcpTool{}
	for _, name := range toolNames {
		p := path.Join("prompts", name+".md")
		raw, err := promptFiles.ReadFile(p)
		if err != nil {
			panic("x prompts: " + err.Error())
		}
		desc, rest, ok := strings.Cut(string(raw), schemaFence)
		schema, ok2 := strings.CutSuffix(rest, "\n```\n")
		if !ok || !ok2 || desc == "" {
			panic(fmt.Sprintf("x prompts: %s: want the description, an empty line and a ```json block with the input schema", p))
		}
		var b bytes.Buffer
		if err := json.Compact(&b, []byte(schema)); err != nil {
			panic(fmt.Sprintf("x prompts: %s: input schema: %v", p, err))
		}
		out[name] = mcpTool{Name: name, Description: desc, InputSchema: b.Bytes()}
	}
	return out
}

// grokPrompts are the prompts grok runs with (text/template; Go only fills in the data).
var grokPrompts = template.Must(template.New("grok").Option("missingkey=error").ParseFS(promptFiles, "prompts/grok_*.md"))

// grokPrompt is the prompt name ("grok_digest", "grok_search") filled with data.
func grokPrompt(name string, data any) (string, error) {
	var b strings.Builder
	if err := grokPrompts.ExecuteTemplate(&b, name+".md", data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// toolPrompt is the tool named name; an unknown name is a bug and panics.
func toolPrompt(name string) mcpTool {
	t, ok := mcpTools[name]
	if !ok {
		panic("x prompts: no tool " + name)
	}
	return t
}
