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

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/graph"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	srcs "github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/sources"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Curator holds what a run needs.
type Curator struct {
	// Cards, when set, places the people facet's card lines by the people package's own dimensions (P9);
	// otherwise the consolidation's model judges them like every other facet.
	Cards CardFiler
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
	// Live is what pages read without the store's lock: the run's progress and the last snapshot (nil: none).
	Live *Live
	// Workers > 1: the daily pass makes up to this many model calls at once (curate/prefetch.go); 0 or 1: one at a time.
	Workers int
	// Hub is the hub beyond the knowledge base (agents, cards, to-dos, memory, the exits' readback); nil: none.
	Hub hubapi.Hub
	// who caches what speaker resolution reads from the knowledge base (curate/speakers.go).
	who *whoCache
	// Collect is the sources' ledger (State.Sources, Cursors, ActiveDays); nil: none kept.
	Collect Collector
}

// Kinds of entities the daily pass reads. Every source goes through one pipeline: chats of any source (Lark, mail,
// WeChat, GitHub…), documents, meetings, events and recordings, and the hub's other sources (internal/sources: agent
// sessions, ask_user cards, to-dos, "about me", the hand-written memory), which read a day at a time like a chat.
var unitKinds = append([]string{"chats", "docs", "meetings", "events", "recordings"}, srcs.Kinds...)

// Collector is a source that keeps a ledger of what it read (internal/sources): the daily pass hands it the state
// before reading the day's units and after, holding the store's lock.
type Collector interface {
	Begin(st *store.State, day string)
	End(st *store.State, day string, units int)
}

// Unit is one entity's material for one day.
type Unit struct {
	Ref    string // "<kind>/<id>"
	Entity string
	Meta   kbsrc.Meta
	Day    string
	Lines  []norm.Line
	// OwnerRecently: chats only; the owner wrote in it within activity_days before Day.
	OwnerRecently bool
	// Who is each line's speaker after resolution (design 3.2, internal/speaker): line number → entry.WhoSelf,
	// "person:<kb id>", or "" (unresolved: never attributed to anyone, never used for ③④⑥ or ⑤′). Nil until resolved.
	Who map[int]string
	// WhoWhy says how each line got its Who (speaker.Why*), for the attribution table and checks.
	WhoWhy map[int]string
	// SelfOff, when set, is why the user's own lines here may not teach ③④⑥ (a weak kb.owner in a group or mail,
	// design 5.0); the text is the page's hint. The assistant's and agents' conversations never have it.
	SelfOff string
	// OwnerLast: groups only; how many days before Day the owner last wrote in it, within the lookback (activity_days,
	// or engaged_days with tiers); 0: not within it.
	OwnerLast int
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

// ownerNames: the hub's owner (name, aliases) plus the user's additions, without the generic words ("用户", "我",
// "owner"…) that the hub uses as a default name or that a configuration may list: they are in half the lines of
// any group, and every check downstream (triage's mentions, the window's "owner named", the graph's owner node)
// would take them for the owner. Lines by the owner are recognised by their speaker (norm.Me), not by name.
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
	return voice.Names(clean)
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
	// The next local midnight, not start+24h: a day is 23 or 25 hours long when daylight saving time changes.
	end := start.AddDate(0, 0, 1)
	entries, err := c.Src.Recent(ctx, start, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("kb.recent: %w", err)
	}
	var out []Unit
	for _, e := range entries {
		kind := e.Kind
		byDay := kind == "chats" || srcs.DayKind(kind) // read with Day: listed whenever active since the day began
		if kind == "" || !slices.Contains(unitKinds, kind) || (!byDay && !e.Updated.Before(end)) {
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

func (c *Curator) unit(ctx context.Context, kind string, e wire.KBEntry, day string) (u Unit, ok bool, err error) {
	defer func() {
		if ok && err == nil {
			c.attribute(ctx, &u) // who each line is by (design 3.2)
		}
	}()
	d, err := c.Src.Get(ctx, kind, e.ID)
	if errors.Is(err, kbsrc.ErrNotFound) {
		return Unit{}, false, nil
	}
	if err != nil {
		return Unit{}, false, err
	}
	u = Unit{Ref: kind + "/" + e.ID, Entity: kind, Meta: kbsrc.DecodeMeta(d), Day: day}
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
	if kind != "chats" && !srcs.DayKind(kind) {
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
	chat := e.ID
	if srcs.DayKind(kind) {
		chat = kind + "/" + e.ID // the sources' units are told apart by their kind
	}
	r, err := c.Src.Day(ctx, chat, day)
	if errors.Is(err, kbsrc.ErrNotFound) {
		return Unit{}, false, nil
	}
	if err != nil {
		return Unit{}, false, err
	}
	for _, m := range r.Messages {
		// A line under the user's name that an agent wrote is not the user's: it is relabelled here, once, so the
		// triage, the checks and the prompts all see it as someone else's.
		u.Lines = append(u.Lines, voice.Relabel(norm.Parse(m.N, m.Text)))
	}
	if isCode(u.Meta) {
		u.Lines = voice.AgentThread(u.Lines)
	}
	if len(u.Lines) == 0 {
		return Unit{}, false, nil
	}
	if u.Meta.Kind == "group" && !slices.ContainsFunc(u.Lines, func(l norm.Line) bool { return l.Speaker == norm.Me }) {
		back := c.Cfg.ActivityDays
		if c.Cfg.Tiers && c.Cfg.EngagedDays > back {
			back = c.Cfg.EngagedDays
		}
		u.OwnerLast = c.ownerLast(ctx, e.ID, r.Prev, day, back)
		u.OwnerRecently = u.OwnerLast > 0 && u.OwnerLast <= c.Cfg.ActivityDays
	}
	return u, true, nil
}

// ownerLast walks back through the chat's earlier days within back days and says how many days before day the owner
// last wrote in it (0: not within them). Only days with messages are read.
func (c *Curator) ownerLast(ctx context.Context, chat, prev, day string, back int) int {
	limit := addDays(day, -back)
	for i := 0; prev != "" && prev >= limit && i < back; i++ {
		r, err := c.Src.Day(ctx, chat, prev)
		if err != nil {
			return 0
		}
		for _, m := range r.Messages {
			if voice.Relabel(norm.Parse(m.N, m.Text)).Speaker == norm.Me {
				return daysBetween(prev, day)
			}
		}
		prev = r.Prev
	}
	return 0
}

func daysBetween(a, b string) int {
	ta, err1 := time.Parse("2006-01-02", a)
	tb, err2 := time.Parse("2006-01-02", b)
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(tb.Sub(ta).Hours()/24 + 0.5)
}

func (c *Curator) builder(today string, owner []string) *graph.Builder {
	reg, problems := graph.NewRegistry(c.Cfg.CustomTypes, c.Cfg.CustomPredicates)
	for _, p := range problems {
		c.logf("graph registry: %s", p)
	}
	// The open relation types (5.4): the packs the first-run answer starts (both when unanswered), plus what the
	// user's table learned. The table starts with the packs' types.
	use := ""
	if ob := c.St.S.Onboarding; ob != nil {
		use = ob.Use
	}
	reg = reg.WithPacks(graph.Packs(use), c.St.S.Types[entry.World])
	if len(c.St.S.Types[entry.World]) == 0 {
		c.St.S.Types[entry.World] = reg.Seeds()
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

// isCode says whether a chat is a code review or issue thread (the triage's G1 sources).
func isCode(m kbsrc.Meta) bool {
	return m.Source == "github" || strings.HasPrefix(m.ID, "github-") || strings.HasPrefix(m.ID, "gh-")
}

// collectBegin and collectEnd bracket a daily pass's reading for the sources' ledger (the caller holds the lock).
func (c *Curator) collectBegin(day string) {
	if c.Collect != nil {
		c.Collect.Begin(c.St.S, day)
	}
}

func (c *Curator) collectEnd(day string, units int) {
	if c.Collect != nil {
		c.Collect.End(c.St.S, day, units)
	}
}
