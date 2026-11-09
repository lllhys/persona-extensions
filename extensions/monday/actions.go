package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

type createArgs struct {
	BoardID int64  `json:"board_id"`
	Name    string `json:"name"`
}

type statusArgs struct {
	BoardID  int64  `json:"board_id"`
	ItemID   int64  `json:"item_id"`
	ColumnID string `json:"column_id"`
	Label    string `json:"label"`
}

func validText(value string, limit int) error {
	if strings.TrimSpace(value) == "" || len([]rune(value)) > limit {
		return errors.New("text must be nonempty and within the extension limit")
	}
	for _, r := range value {
		if r == 0 || unicode.IsControl(r) {
			return errors.New("text contains unsupported control characters")
		}
	}
	return nil
}

func (in createArgs) validate() error {
	if in.BoardID <= 0 {
		return errors.New("board ID must be positive")
	}
	return validText(in.Name, 500)
}

func (in statusArgs) validate() error {
	if in.BoardID <= 0 || in.ItemID <= 0 {
		return errors.New("board and item IDs must be positive")
	}
	if err := validText(in.ColumnID, 100); err != nil {
		return err
	}
	return validText(in.Label, 100)
}

func describeAction(name string, raw json.RawMessage) (map[string]any, error) {
	var summary string
	switch name {
	case "monday_create_item":
		var in createArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		summary = fmt.Sprintf("Create monday item in board %d\nTitle: %s", in.BoardID, in.Name)
	case "monday_update_status":
		var in statusArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		summary = fmt.Sprintf("Update monday item %d on board %d\nColumn %s → %s", in.ItemID, in.BoardID, in.ColumnID, in.Label)
	default:
		return nil, fmt.Errorf("unknown action %q", name)
	}
	return map[string]any{"action": map[string]any{"name": name, "visible": "owner"}, "summary": summary}, nil
}

func (a *app) doAction(ctx context.Context, requestID, name string, raw json.RawMessage) (map[string]any, error) {
	var fn func() (string, error)
	switch name {
	case "monday_create_item":
		var in createArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		if _, err := a.client.board(ctx, in.BoardID); err != nil {
			return nil, err
		}
		fn = func() (string, error) {
			item, err := a.client.createItem(ctx, in.BoardID, in.Name)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Created monday item #%d: %s", item.ID, item.Name), nil
		}
	case "monday_update_status":
		var in statusArgs
		if err := decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.validate(); err != nil {
			return nil, err
		}
		board, err := a.client.board(ctx, in.BoardID)
		if err != nil {
			return nil, err
		}
		if !statusLabelExists(board, in.ColumnID, in.Label) {
			return nil, errors.New("label must already exist on the selected status column; no label was created")
		}
		fn = func() (string, error) {
			if _, err := a.client.updateStatusColumn(ctx, in.BoardID, in.ItemID, in.ColumnID, in.Label); err != nil {
				return "", err
			}
			return fmt.Sprintf("Updated monday item #%d to %s", in.ItemID, in.Label), nil
		}
	default:
		return nil, fmt.Errorf("unknown action %q", name)
	}
	keyData, _ := json.Marshal(raw)
	text, err := a.receipts.commit(requestID, string(keyData), fn)
	if err != nil {
		return nil, err
	}
	a.record(nil)
	if a.hub != nil {
		_ = a.hub.Invalidate(pageUI)
	}
	return map[string]any{"text": text}, nil
}

func statusLabelExists(board board, columnID, label string) bool {
	for _, column := range board.Columns {
		if column.ID != columnID || column.Type != "status" {
			continue
		}
		var settings struct {
			Labels          map[string]string `json:"labels"`
			LabelsPositions map[string]int    `json:"labels_positions"`
		}
		if json.Unmarshal([]byte(column.SettingsStr), &settings) != nil {
			return false
		}
		for _, name := range settings.Labels {
			if name == label {
				return true
			}
		}
	}
	return false
}
