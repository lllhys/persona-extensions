package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

var idSchema = map[string]any{"type": "integer", "minimum": 1}

var readTools = []tool{
	{Name: "monday_boards", Description: "List accessible monday boards with exact board IDs and URLs. No arbitrary GraphQL is accepted; only the documented boards query is used.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}},
	{Name: "monday_items", Description: "Read items in an exact monday board with items_page cursor pagination. Includes names, IDs, status/column values and timestamps.",
		InputSchema: map[string]any{"type": "object", "required": []string{"board_id"}, "properties": map[string]any{"board_id": idSchema, "cursor": map[string]any{"type": "string", "maxLength": 2048}}}},
	{Name: "monday_board", Description: "Read a monday board's column IDs, types and settings needed to select an existing status label before writing.",
		InputSchema: map[string]any{"type": "object", "required": []string{"board_id"}, "properties": map[string]any{"board_id": idSchema}}},
}

var actionTools = []tool{
	{Name: "monday_create_item", Description: "Create an item in an exact monday board after explicit confirmation. Only board_id and item_name are sent; no arbitrary column values or board changes.",
		InputSchema: map[string]any{"type": "object", "required": []string{"board_id", "name"}, "properties": map[string]any{"board_id": idSchema, "name": map[string]any{"type": "string", "minLength": 1, "maxLength": 500}}}},
	{Name: "monday_update_status", Description: "Update an existing status column to an existing label after explicit confirmation. Does not create labels or change arbitrary columns.",
		InputSchema: map[string]any{"type": "object", "required": []string{"board_id", "item_id", "column_id", "label"}, "properties": map[string]any{
			"board_id": idSchema, "item_id": idSchema, "column_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 100}, "label": map[string]any{"type": "string", "minLength": 1, "maxLength": 100},
		}}},
}

func decode(raw json.RawMessage, out any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	if dec.More() {
		return errors.New("invalid arguments: trailing JSON value")
	}
	return nil
}

func (a *app) callTool(ctx context.Context, name string, raw json.RawMessage) (string, error) {
	var in struct {
		BoardID int64  `json:"board_id"`
		Cursor  string `json:"cursor"`
	}
	if err := decode(raw, &in); err != nil {
		return "", err
	}
	switch name {
	case "monday_boards":
		items, err := a.client.boards(ctx)
		a.record(err)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, item := range items {
			fmt.Fprintf(&b, "- Board #%d: %s\n  URL: %s\n", item.ID, clipText(item.Name, 500), safeBoardLink(item.URL))
		}
		return clipText(b.String(), 90000), nil
	case "monday_items":
		page, err := a.client.items(ctx, in.BoardID, in.Cursor)
		a.record(err)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, item := range page.Items {
			b.WriteString(formatItem(item))
		}
		if len(page.Items) == 0 {
			b.WriteString("No items in this board.\n")
		}
		if page.Cursor != "" {
			fmt.Fprintf(&b, "Next cursor: %s\n", page.Cursor)
		}
		return clipText(b.String(), 90000), nil
	case "monday_board":
		item, err := a.client.board(ctx, in.BoardID)
		a.record(err)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Board #%d: %s\n", item.ID, item.Name)
		for _, column := range item.Columns {
			fmt.Fprintf(&b, "- Column %s: %s | type: %s | settings: %s\n", column.ID, clipText(column.Title, 500), column.Type, clipText(column.SettingsStr, 10000))
		}
		return clipText(b.String(), 90000), nil
	}
	return "", fmt.Errorf("no tool %s", name)
}

func formatItem(item item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- Item #%d: %s\n  Updated: %s\n", item.ID, clipText(item.Name, 500), item.UpdatedAt)
	for _, column := range item.ColumnValues {
		if column.Text != "" {
			fmt.Fprintf(&b, "  %s: %s\n", column.ID, clipText(column.Text, 1000))
		}
	}
	return b.String()
}
