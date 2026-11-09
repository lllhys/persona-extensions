package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (a *app) listCollections(ctx context.Context) ([]collection, error) {
	a.mu.RLock()
	cached, at := a.cols, a.colsAt
	a.mu.RUnlock()
	if !at.IsZero() && time.Since(at) <= 2*time.Minute {
		return cached, nil
	}
	items, err := a.read(ctx, func() ([]collection, error) { return a.api.collections(ctx) })
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.colsAt.IsZero() && time.Since(a.colsAt) <= 2*time.Minute {
		return a.cols, nil
	}
	a.cols, a.colsAt = items, time.Now()
	return items, nil
}

func (a *app) resolveCollection(ctx context.Context, name string, defaultID int64) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return defaultID, nil
	}
	if id, err := strconv.ParseInt(name, 10, 64); err == nil {
		if id < -1 {
			return 0, errors.New("collection ID must be -1, 0 or a positive collection ID")
		}
		return id, nil
	}
	items, err := a.listCollections(ctx)
	if err != nil {
		return 0, err
	}
	var found []int64
	for _, item := range items {
		if strings.EqualFold(item.Title, name) {
			found = append(found, item.ID)
		}
	}
	if len(found) != 1 {
		return 0, fmt.Errorf("collection %q must be an exact unique full path or numeric ID; call raindrop_collections", clipText(name, 100))
	}
	return found[0], nil
}

// resolveSaveCollection is memory-only. The assistant must first read collections and pass a numeric ID,
// or the exact full name returned in that same cached list. It then supplies both ID and name to the write gate.
func (a *app) resolveSaveCollection(in *saveArgs) error {
	if in.CollectionID == 0 {
		in.CollectionID = -1
	}
	name := strings.TrimSpace(in.Collection)
	if name == "" {
		return nil
	}
	a.mu.RLock()
	items := a.cols
	a.mu.RUnlock()
	if items == nil {
		return errors.New("read Raindrop collections before choosing one by name")
	}
	var found int64
	for _, item := range items {
		if strings.EqualFold(item.Title, name) {
			if found != 0 {
				return errors.New("collection name is ambiguous; use its numeric ID")
			}
			found = item.ID
		}
	}
	if found == 0 {
		return errors.New("collection not found; read Raindrop collections again")
	}
	if in.CollectionID > 0 && in.CollectionID != found {
		return errors.New("collection ID and name do not match")
	}
	in.CollectionID = found
	return nil
}

// actions.describe must not perform I/O: both the ID and the name are shown when available.
func (a *app) describeSave(raw json.RawMessage) (map[string]any, error) {
	var in saveArgs
	if err := decodeArgs(raw, &in); err != nil {
		return nil, err
	}
	if err := a.resolveSaveCollection(&in); err != nil {
		return nil, err
	}
	if err := normalizeSave(&in); err != nil {
		return nil, err
	}
	collection := "Unsorted"
	if in.CollectionID > 0 {
		collection = fmt.Sprintf("collection ID %d", in.CollectionID)
		if name := strings.TrimSpace(in.Collection); name != "" {
			collection = fmt.Sprintf("%s (ID %d)", name, in.CollectionID)
		}
	}
	summary := "Save " + in.URL + " to Raindrop / " + collection
	if len(in.Tags) > 0 {
		summary += "\nTags: " + strings.Join(in.Tags, ", ")
	}
	if in.Title != "" {
		summary += "\nTitle: " + in.Title
	}
	if in.Note != "" {
		summary += "\nNote: " + in.Note
	}
	return map[string]any{"action": map[string]any{"name": saveTool.Name, "visible": "owner"}, "summary": summary}, nil
}
