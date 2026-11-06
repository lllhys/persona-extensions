// Package curate runs the curator: the daily pass (triage, digest with citations, deterministic checks, items and
// graph), the weekly pass (consolidation, promotion and ageing, conflicts, health checks, spot checks), and the
// proposal queue with its writes to the knowledge base and their undo.
package curate

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/graph"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/model"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/store"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Curator holds what a run needs.
type Curator struct {
	Src   kbsrc.Source
	Model model.Model // nil: no model; only the deterministic steps run
	St    *store.Store
	Cfg   Config
	Loc   *time.Location
	Lang  string
	Now   func() time.Time
	Logf  func(format string, args ...any)
	// Extension is the extension id, for the source written into timelines.
	Extension string
}

// Kinds of knowledge-base entities the daily pass reads. Every source goes through one pipeline: chats of any
// source (Lark, mail, WeChat, GitHub…), and documents, meetings, events and recordings.
var unitKinds = []string{"chats", "docs", "meetings", "events", "recordings"}

// Unit is one entity's material for one day.
type Unit struct {
	Ref    string // "<kind>/<id>"
	Entity string
	Meta   kbsrc.Meta
	Day    string
	Lines  []norm.Line
	// OwnerRecently: chats only; the owner wrote in it within activity_days before Day.
	OwnerRecently bool
}

func (c *Curator) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

func (c *Curator) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Curator) language() string {
	if c.Cfg.Language != "" {
		return c.Cfg.Language
	}
	if c.Lang != "" {
		return c.Lang
	}
	return "zh-Hans"
}

// ownerNames: the hub's owner (name, aliases) plus the user's additions.
func (c *Curator) ownerNames(ctx context.Context) []string {
	var out []string
	o, err := c.Src.Owner(ctx)
	if err == nil {
		out = append(out, o.Name)
		out = append(out, o.Aliases...)
		for _, h := range o.Handles {
			if h.Kind == "email" {
				if local, _, ok := strings.Cut(h.Value, "@"); ok && len(local) >= 3 {
					out = append(out, local)
				}
			}
		}
	}
	out = append(out, c.Cfg.OwnerAliases...)
	var clean []string
	for _, n := range out {
		n = strings.TrimSpace(n)
		if n != "" && !slices.ContainsFunc(clean, func(x string) bool { return norm.Q(x) == norm.Q(n) }) {
			clean = append(clean, n)
		}
	}
	return clean
}

// units lists every entity with material on day, from every source at once (kb.recent). A chat's entry carries
// the time of its newest message, so a chat that also had messages after the day is listed too: the window runs
// from the start of the day to now, and kb.day says whether the chat had messages that day. Other kinds count only
// when they changed within the day.
func (c *Curator) units(ctx context.Context, day string) ([]Unit, error) {
	start, err := time.ParseInLocation("2006-01-02", day, c.Loc)
	if err != nil {
		return nil, err
	}
	end := start.Add(24 * time.Hour)
	entries, err := c.Src.Recent(ctx, start, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("kb.recent: %w", err)
	}
	var out []Unit
	for _, e := range entries {
		kind := e.Kind
		if kind == "" || !slices.Contains(unitKinds, kind) || (kind != "chats" && !e.Updated.Before(end)) {
			continue
		}
		u, ok, err := c.unit(ctx, kind, e, day)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, u)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out, nil
}

func (c *Curator) unit(ctx context.Context, kind string, e wire.KBEntry, day string) (Unit, bool, error) {
	d, err := c.Src.Get(ctx, kind, e.ID)
	if errors.Is(err, kbsrc.ErrNotFound) {
		return Unit{}, false, nil
	}
	if err != nil {
		return Unit{}, false, err
	}
	u := Unit{Ref: kind + "/" + e.ID, Entity: kind, Meta: kbsrc.DecodeMeta(d), Day: day}
	if u.Meta.Name == "" {
		u.Meta.Name = e.Name
	}
	if u.Meta.Relevance == 0 {
		u.Meta.Relevance = e.Relevance
	}
	if u.Meta.Importance == "" {
		u.Meta.Importance = e.Importance
	}
	if u.Meta.Source == "" {
		u.Meta.Source = e.Source
	}
	if kind != "chats" {
		// A document, meeting, event or recording: its text, a line at a time, numbered as the knowledge base shows
		// it (summary first). Anchors are "<kind>/<id>" plus that number.
		text := strings.TrimSpace(d.Summary)
		if d.Recent != "" {
			text += "\n" + d.Recent
		}
		for i, l := range strings.Split(text, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				u.Lines = append(u.Lines, norm.Line{N: i + 1, Body: l})
			}
		}
		return u, len(u.Lines) > 0, nil
	}
	r, err := c.Src.Day(ctx, e.ID, day)
	if errors.Is(err, kbsrc.ErrNotFound) {
		return Unit{}, false, nil
	}
	if err != nil {
		return Unit{}, false, err
	}
	for _, m := range r.Messages {
		u.Lines = append(u.Lines, norm.Parse(m.N, m.Text))
	}
	if len(u.Lines) == 0 {
		return Unit{}, false, nil
	}
	if u.Meta.Kind == "group" && !slices.ContainsFunc(u.Lines, func(l norm.Line) bool { return l.Speaker == norm.Me }) {
		u.OwnerRecently = c.ownerRecently(ctx, e.ID, r.Prev, day)
	}
	return u, true, nil
}

// ownerRecently walks back through the chat's earlier days within activity_days.
func (c *Curator) ownerRecently(ctx context.Context, chat, prev, day string) bool {
	limit := addDays(day, -c.Cfg.ActivityDays)
	for i := 0; prev != "" && prev >= limit && i < c.Cfg.ActivityDays; i++ {
		r, err := c.Src.Day(ctx, chat, prev)
		if err != nil {
			return false
		}
		for _, m := range r.Messages {
			if norm.Parse(m.N, m.Text).Speaker == norm.Me {
				return true
			}
		}
		prev = r.Prev
	}
	return false
}

func (c *Curator) builder(today string, owner []string) *graph.Builder {
	reg, problems := graph.NewRegistry(c.Cfg.CustomTypes, c.Cfg.CustomPredicates)
	for _, p := range problems {
		c.logf("graph registry: %s", p)
	}
	b := &graph.Builder{G: c.St.S.Graph, Reg: reg, Owner: owner, Today: today}
	b.EnsureMe()
	return b
}

func hash(parts ...string) string {
	h := sha1.Sum([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(h[:])
}

func addDays(day string, n int) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

// ISOWeek is "2006-W01" for a day.
func ISOWeek(day string) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return ""
	}
	y, w := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", y, w)
}

// WeekDays are the seven days (Monday first) of an ISO week "2006-W01".
func WeekDays(week string) ([]string, error) {
	var y, w int
	if _, err := fmt.Sscanf(week, "%d-W%d", &y, &w); err != nil {
		return nil, fmt.Errorf("week %q: %w", week, err)
	}
	t := time.Date(y, 1, 4, 0, 0, 0, 0, time.UTC) // 4 January is always in week 1
	t = t.AddDate(0, 0, -int((t.Weekday()+6)%7)+(w-1)*7)
	out := make([]string, 7)
	for i := range out {
		out[i] = t.AddDate(0, 0, i).Format("2006-01-02")
	}
	return out, nil
}

func (c *Curator) today() string { return c.now().In(c.Loc).Format("2006-01-02") }
