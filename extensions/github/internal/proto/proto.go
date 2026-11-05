// Package proto is the part of Persona's wire types (server/internal/proto) the connector protocol carries: tools and
// their results. Copied as they are:
// the hub reads them in the same shapes.
package proto

import "encoding/json"

// Tool is an MCP tool definition.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Content is an MCP tool result content block.
type Content struct {
	Type     string `json:"type"` // text | image
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
}

type ToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"is_error"`
}

func TextResult(text string, isErr bool) ToolResult {
	return ToolResult{Content: []Content{{Type: "text", Text: text}}, IsError: isErr}
}
