// Package kbsrc is the curator's only way to the knowledge base: the six kb.* methods of the extension protocol
// (docs/extensions.md "Knowledge base RPC"). Hub calls them over the extension's connection; Fixture answers them
// from a directory of synthetic data, for tests and dry runs; DryRun wraps either and keeps writes to itself.
package kbsrc

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Source is what the curator reads and writes.
type Source interface {
	List(ctx context.Context, kind string) ([]wire.KBEntry, error)
	Get(ctx context.Context, kind, id string) (wire.KBDetail, error)
	Day(ctx context.Context, chat, day string) (wire.KBDayResult, error)
	Update(ctx context.Context, p wire.KBUpdateParams) error
	Owner(ctx context.Context) (wire.KBOwner, error)
}

// ErrNotFound is an entity or day that is not there.
var ErrNotFound = errors.New("not found")

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

func (h Hub) Day(ctx context.Context, chat, day string) (wire.KBDayResult, error) {
	r, err := h.H.KBDay(ctx, wire.KBDayParams{ID: chat, Day: day})
	return r, mapErr(err)
}

func (h Hub) Update(ctx context.Context, p wire.KBUpdateParams) error {
	_, err := h.H.KBUpdate(ctx, p)
	return mapErr(err)
}

func (h Hub) Owner(ctx context.Context) (wire.KBOwner, error) {
	o, err := h.H.KBOwner(ctx)
	return o, mapErr(err)
}

func mapErr(err error) error {
	var e *ext.Error
	if errors.As(err, &e) && e.Code == wire.KBErrorCodeNotFound {
		return ErrNotFound
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

func (d *DryRun) Update(_ context.Context, p wire.KBUpdateParams) error {
	d.Writes = append(d.Writes, p)
	return nil
}
