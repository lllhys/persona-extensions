// Package kbsrc is the curator's only way to the knowledge base: the six kb.* methods of the extension protocol
// (docs/extensions.md "Knowledge base RPC"). Hub calls them over the extension's connection; Fixture answers them
// from a directory of synthetic data, for tests and dry runs; DryRun wraps either and keeps writes to itself.
package kbsrc

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Source is what the curator reads and writes.
type Source interface {
	List(ctx context.Context, kind string) ([]wire.KBEntry, error)
	Get(ctx context.Context, kind, id string) (wire.KBDetail, error)
	Day(ctx context.Context, chat, day string) (wire.KBDayResult, error)
	// Recent is what came in from every source in [since, until) (until zero: now), across kinds (kb.recent).
	Recent(ctx context.Context, since, until time.Time) ([]wire.KBEntry, error)
	// Update writes and returns the previous value of each field written (kb.update's before).
	Update(ctx context.Context, p wire.KBUpdateParams) (map[string]json.RawMessage, error)
	Owner(ctx context.Context) (wire.KBOwner, error)
}

// ErrNotFound is an entity or day that is not there.
var ErrNotFound = errors.New("not found")

// Conflict: the write touched a field the user or another extension holds; nothing was written. It is a question
// for the user, never retried.
type Conflict struct{ Holders map[string]string }

func (c *Conflict) Error() string {
	var parts []string
	for f, h := range c.Holders {
		who := "你"
		if h != "user" {
			who = "扩展 " + strings.TrimPrefix(h, "ext:")
		}
		parts = append(parts, f+"（"+who+"）")
	}
	sort.Strings(parts)
	return "这些字段被锁着，没有写：" + strings.Join(parts, "、")
}

// Meta is the part of an entity's metadata the curator uses; the rest of meta.json is kept as raw JSON.
type Meta struct {
	ID         string    `json:"id"`
	Slug       string    `json:"slug"`
	Source     string    `json:"source"`
	Kind       string    `json:"kind"` // chats: dm | group | bot | mail; projects: short | long
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	People     []string  `json:"people"`
	Chats      []string  `json:"chats"`
	Projects   []string  `json:"projects"`
	Aliases    []string  `json:"aliases"`
	Mine       bool      `json:"mine"`
	Note       string    `json:"note"`
	Importance string    `json:"importance"`
	Relevance  int       `json:"relevance"`
	Tags       []string  `json:"tags"`
	Pinned     string    `json:"pinned"`
	Locked     []string  `json:"locked_fields"`
	LastAt     time.Time `json:"last_at"`
	MergedInto string    `json:"merged_into"`
}

// DecodeMeta reads an entity's metadata.
func DecodeMeta(d wire.KBDetail) Meta {
	var m Meta
	_ = json.Unmarshal(d.Meta, &m)
	if m.ID == "" {
		m.ID = d.ID
	}
	return m
}

// Hub is the knowledge base over the extension's connection to the hub.
type Hub struct{ H *ext.Hub }

func (h Hub) List(ctx context.Context, kind string) ([]wire.KBEntry, error) {
	var out []wire.KBEntry
	offset := 0
	for {
		r, err := h.H.KBList(ctx, wire.KBListParams{Kind: kind, Limit: 200, Offset: offset})
		if err != nil {
			return out, mapErr(err)
		}
		out = append(out, r.Items...)
		if r.NextOffset == nil || *r.NextOffset <= offset {
			return dedupe(out), nil
		}
		offset = *r.NextOffset
	}
}

func (h Hub) Get(ctx context.Context, kind, id string) (wire.KBDetail, error) {
	zero := 0
	r, err := h.H.KBGet(ctx, wire.KBGetParams{Kind: kind, ID: id, RecentChars: &zero})
	return r.Detail, mapErr(err)
}

func (h Hub) Recent(ctx context.Context, since, until time.Time) ([]wire.KBEntry, error) {
	p := wire.KBRecentParams{Since: since.UTC().Format(time.RFC3339), Limit: 200}
	if !until.IsZero() {
		p.Until = until.UTC().Format(time.RFC3339)
	}
	var out []wire.KBEntry
	for {
		r, err := h.H.KBRecent(ctx, p)
		if err != nil {
			return out, mapErr(err)
		}
		out = append(out, r.Items...)
		if r.NextOffset == nil || *r.NextOffset <= p.Offset {
			return dedupe(out), nil
		}
		p.Offset = *r.NextOffset
	}
}

func (h Hub) Day(ctx context.Context, chat, day string) (wire.KBDayResult, error) {
	r, err := h.H.KBDay(ctx, wire.KBDayParams{ID: chat, Day: day})
	return r, mapErr(err)
}

func (h Hub) Update(ctx context.Context, p wire.KBUpdateParams) (map[string]json.RawMessage, error) {
	r, err := h.H.KBUpdate(ctx, p)
	return r.Before, mapErr(err)
}

func (h Hub) Owner(ctx context.Context) (wire.KBOwner, error) {
	o, err := h.H.KBOwner(ctx)
	return o, mapErr(err)
}

func mapErr(err error) error {
	var e *ext.Error
	if errors.As(err, &e) {
		switch e.Code {
		case wire.KBErrorCodeNotFound:
			return ErrNotFound
		case wire.KBErrorCodeConflict:
			c := &Conflict{Holders: map[string]string{}}
			var data wire.KBConflict
			if json.Unmarshal(e.Data, &data) == nil && data.Owners != nil {
				c.Holders = data.Owners
			}
			return c
		}
	}
	return err
}

// dedupe drops entries listed twice: paging is not a snapshot, so an entity can move between pages.
func dedupe(in []wire.KBEntry) []wire.KBEntry {
	seen := map[string]bool{}
	out := in[:0]
	for _, e := range in {
		if !seen[e.Kind+"/"+e.ID] {
			seen[e.Kind+"/"+e.ID] = true
			out = append(out, e)
		}
	}
	return out
}

// DryRun reads through to a source and records writes instead of making them.
type DryRun struct {
	Source
	Writes []wire.KBUpdateParams
}

func (d *DryRun) Update(_ context.Context, p wire.KBUpdateParams) (map[string]json.RawMessage, error) {
	d.Writes = append(d.Writes, p)
	return map[string]json.RawMessage{}, nil
}
