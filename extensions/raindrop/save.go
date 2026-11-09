package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// saveOnce persists the request before contacting Raindrop. A timeout is an unknown result, not permission to retry.
func (a *app) saveOnce(ctx context.Context, requestID string, raw json.RawMessage) (raindrop, error) {
	var args saveArgs
	if err := decodeArgs(raw, &args); err != nil {
		return raindrop{}, err
	}
	if err := a.resolveSaveCollection(&args); err != nil {
		return raindrop{}, err
	}
	if err := normalizeSave(&args); err != nil {
		return raindrop{}, err
	}
	key, err := json.Marshal(args)
	if err != nil {
		return raindrop{}, err
	}
	item, err := a.receipts.commit(requestID, string(key), func() (raindrop, error) { return a.api.save(ctx, args) })
	if err != nil {
		return raindrop{}, err
	}
	a.mu.Lock()
	a.lastOK, a.lastErr = time.Now(), nil
	if a.hub != nil {
		_ = a.hub.Invalidate(pageUI)
	}
	a.mu.Unlock()
	return item, nil
}

func (a *app) saveFromPage(ctx context.Context, r *ext.UIRequest, st pageState) (*wire.ActionResult, error) {
	args := st.Save
	if err := normalizeSave(&args); err != nil {
		return nil, err
	}
	if st.DraftID == "" {
		return nil, errors.New("the save form has expired; reopen it before saving")
	}
	raw, _ := json.Marshal(args)
	item, err := a.saveOnce(ctx, "ui:"+st.DraftID, raw)
	if err != nil {
		return nil, err
	}
	return &wire.ActionResult{Toast: fmt.Sprintf(textsFor(r.Locale).savedHere, collectionLabel(item, textsFor(r.Locale))), Pop: true}, nil
}

func splitTags(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == '\n' })
}
