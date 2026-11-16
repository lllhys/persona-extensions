package curate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/prompts"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
	"github.com/nwverse/persona/sdk/go/wire"
)

// PinsPerWeek bounds the new "record this" proposals a week makes: the limit is the user's attention, not money.
const PinsPerWeek = 30

// RunWeekly consolidates an ISO week: it promotes and ages items, merges what says the same thing, surfaces
// contradictions, ages the graph and finds merge candidates, runs the health checks, proposes what is ready to be
// recorded in the knowledge base, picks the spot-check sample, and applies what the user lets it apply by itself.
func (c *Curator) RunWeekly(ctx context.Context, week string, dry bool) (*store.Run, error) {
	days, err := WeekDays(week)
	if err != nil {
		return nil, err
	}
	live := c.Live
	if dry {
		live = nil
	}
	live.begin("weekly", week, c.now())
	live.progress(func(p *Progress) { p.Stage = "周级" })
	defer live.end()
	c.St.Lock()
	defer c.St.Unlock()
	run := &store.Run{ID: fmt.Sprintf("weekly-%s-%d", week, c.now().Unix()), Kind: "weekly", Period: week,
		Started: c.now().UTC().Format(time.RFC3339), DryRun: dry, Counts: map[string]int{}}
	// The week is processed as of its own last day, whenever it runs: a late run of last week must not work on this
	// week's material (a W44 run that only touched W45 items was the bug).
	today := days[6]
	owner := c.ownerNames(ctx)

	// 1. Promotion and ageing. Knowledge does not age with time: it is replaced by newer knowledge or a decision.
	// Promotion looks at the items seen this week. Ageing looks at every item that existed by the week's end (an
	// item not seen for weeks is exactly what ages); an item first seen after the week is not touched at all.
	for _, it := range c.St.S.Items {
		if it.FirstSeen > today {
			continue
		}
		before := it.Status
		if weekItem(it, days) {
			it.Sources = sources(it.Evidence) // counted again under the current rule (distinct chats, mirrors once)
			promote(it)
		}
		switch it.Class {
		case store.Practice:
			if (it.Status == "recurring" || it.Status == "established") && it.LastSeen < addDays(today, -56) && !it.Pinned {
				it.Status = "dormant"
			}
		case store.Fact:
			if it.Status == "active" && !it.Pinned && it.LastSeen < addDays(today, -c.Cfg.StaleDays) {
				it.Status = "stale"
			}
		}
		if it.Status != before {
			run.Counts["status_"+it.Status]++
		}
	}

	var m model.Model
	var metered *model.Metered
	if c.Model != nil {
		metered = &model.Metered{Model: c.Model, Usage: c.St, Day: today}
		m = metered
	}
	// The entries of every facet (design 5.0, 5.3, 5.4): filing caught up, the abstract gate, the impact review of
	// what exits A and B carry, the relations cleaned, the statuses as of the week's last day.
	c.weeklyEntries(ctx, m, today, run)

	// 2. Consolidation of the items: clusters of alike items, judged by the model (or merged only when near-identical).
	for _, class := range []string{store.Fact, store.Practice, store.Knowledge} {
		for _, cl := range c.clusters(class, days) {
			c.consolidate(ctx, m, week, class, cl, run)
		}
	}

	// 3. Graph: relations nobody mentioned for stale_days go stale; same-name nodes become merge candidates.
	b := c.builder(today, owner)
	run.Counts["graph_staled"] = b.Age(today, c.Cfg.StaleDays, addDays)
	for _, pair := range b.SameNameCandidates() {
		from, into := c.St.S.Graph.Nodes[pair[0]], c.St.S.Graph.Nodes[pair[1]]
		c.propose("graph_merge", fmt.Sprintf("「%s」和「%s」可能是同一个%s", from.Name, into.Name, from.Type),
			"名字相同；合并只影响图谱，能撤销", "graph/"+from.ID, Change{From: from.ID, Into: into.ID}, nil, nil, "")
		run.Counts["proposal_graph_merge"]++
	}

	// 4. Reconcile: what was derived from entities the knowledge base no longer has (a source forgotten while the
	// curator was not subscribed to kb.forgotten, or not running) is removed.
	run.Counts["reconciled"] = c.reconcile(ctx)

	// 5. Health checks against the knowledge base (zero tokens).
	findings := c.health(ctx, owner, today, run)

	// 6. Ready to record: active facts and knowledge, established practices.
	c.proposePins(ctx, days, run)
	c.proposeWorld(ctx, days, run)    // v4 ⑤ entries, single source marked (5.4)
	c.weeklyPeople(ctx, m, days, run) // ⑤′ cards: rhythm, the card pass, person pages (P9)

	// 7. Spot check: ten items of the week, picked the same way every time for the same week.
	c.sample(week, days)

	run.Counts["auto_applied"] = c.autoApply(ctx, run.ID, !dry)
	run.Counts["expired"] = c.expire()
	c.afterWeekly(week, days, run, dry) // the correction ledger's spot check (curate/exits.go)
	if metered != nil {
		run.Counts["model_calls"] = metered.Calls
	}
	for _, u := range c.St.S.Usage {
		if u.Day >= days[0] && u.Day <= today {
			run.Tokens += u.In + u.Out
		}
	}
	run.Finished = c.now().UTC().Format(time.RFC3339)
	run.Report = c.weeklyReport(week, days, run, findings)
	c.St.S.Runs = append(c.St.S.Runs, run)
	if dry {
		return run, nil
	}
	err = c.St.Save()
	c.Publish() // the weekly pass forgets what the knowledge base no longer has
	return run, err
}

// clusters groups a class's live items of the week (seen on one of its days) that read alike (bigram Jaccard ≥ 0.6,
// or ≥ 0.4 about the same entity).
func (c *Curator) clusters(class string, days []string) [][]*store.Item {
	items := c.St.S.ItemsBy(class, func(it *store.Item) bool {
		return it.Status != "merged" && it.Status != "rejected" && it.Status != "superseded" && weekItem(it, days)
	})
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if parent[x] == "" || parent[x] == x {
			return x
		}
		parent[x] = find(parent[x])
		return parent[x]
	}
	for i, a := range items {
		for _, bb := range items[i+1:] {
			j := norm.Jaccard(a.Claim, bb.Claim)
			if j >= 0.6 || (j >= 0.4 && a.About != "" && norm.Q(a.About) == norm.Q(bb.About)) {
				parent[find(a.ID)] = find(bb.ID)
			}
		}
	}
	groups := map[string][]*store.Item{}
	for _, it := range items {
		if _, ok := parent[it.ID]; ok || slices.ContainsFunc(items, func(o *store.Item) bool { return parent[o.ID] == it.ID }) {
			groups[find(it.ID)] = append(groups[find(it.ID)], it)
		}
	}
	var out [][]*store.Item
	for _, g := range groups {
		if len(g) >= 2 {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0].ID < out[j][0].ID })
	return out
}

func (c *Curator) consolidate(ctx context.Context, m model.Model, week, class string, cl []*store.Item, run *store.Run) {
	in := schema.WeeklyInput{Week: week, Class: class, Language: c.language()}
	byID := map[string]*store.Item{}
	for _, it := range cl {
		byID[it.ID] = it
		e := schema.WeeklyEntry{ID: it.ID, Claim: it.Claim, About: it.About, Days: it.Days, Sources: it.Sources, Status: it.Status, Pinned: it.Pinned, Valid: it.When}
		for _, ev := range it.Evidence {
			if len(e.Quotes) < 4 {
				e.Quotes = append(e.Quotes, ev.Quote)
			}
		}
		in.Items = append(in.Items, e)
	}
	var out schema.WeeklyOutput
	key := hash("weekly", week, schema.Must(in), schema.WeeklyPrompt)[:20]
	if m != nil && c.St.S.Done[key] == nil {
		resp, err := m.Complete(ctx, model.Request{Step: "weekly", Tier: tier(c.Cfg.WeeklyTier), System: prompts.Weekly,
			User: schema.Must(in), MaxTokens: 4000, Schema: prompts.WeeklySchema})
		if err == nil && json.Unmarshal([]byte(resp.Text), &out) == nil {
			raw, _ := c.St.PutRaw(key, []byte(resp.Text))
			c.St.S.Done[key] = &store.Done{Key: key, Step: "weekly", Day: week, At: c.now().UTC().Format(time.RFC3339), Raw: raw, Prompt: schema.WeeklyPrompt, Ruleset: schema.Ruleset}
		} else if err != nil && !errors.Is(err, model.ErrUnavailable) {
			run.Notes = append(run.Notes, "周级合并：模型出错 "+err.Error())
		}
		if err != nil {
			out = deterministicMerges(cl)
		}
	} else if m == nil {
		out = deterministicMerges(cl)
	}
	for _, mg := range out.Merges {
		keep := byID[mg.Keep]
		if keep == nil {
			continue
		}
		for _, d := range mg.Drop {
			drop := byID[d]
			if drop == nil || drop == keep || drop.Pinned {
				continue // never invented, never a pinned item dropped
			}
			drop.Status, drop.MergedInto = "merged", keep.ID
			for _, e := range drop.Evidence {
				if !slices.ContainsFunc(keep.Evidence, func(x store.Evidence) bool { return x.Chat == e.Chat && x.Day == e.Day && x.N == e.N }) {
					keep.Evidence = append(keep.Evidence, e)
				}
			}
			for _, dd := range drop.Days {
				if !slices.Contains(keep.Days, dd) {
					keep.Days = append(keep.Days, dd)
				}
			}
			for _, w := range drop.Weeks {
				if !slices.Contains(keep.Weeks, w) {
					keep.Weeks = append(keep.Weeks, w)
				}
			}
			sort.Strings(keep.Days)
			sort.Strings(keep.Weeks)
			run.Counts["merged"]++
		}
		keep.Sources = sources(keep.Evidence)
		// A rewording is accepted only when it stays close to one of the merged claims.
		if mg.Claim != "" && !keep.Pinned && norm.Jaccard(mg.Claim, keep.Claim) >= 0.5 {
			keep.Claim = mg.Claim
		}
		promote(keep)
	}
	for _, cf := range out.Conflicts {
		var ids []string
		var ev []store.Evidence
		for _, id := range cf.IDs {
			if it := byID[id]; it != nil && it.Status != "merged" {
				ids = append(ids, id)
				ev = append(ev, it.Evidence...)
			}
		}
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		c.propose("item_conflict", "两条说法对不上："+byID[ids[0]].Claim, cf.Reason, "items/"+ids[0], Change{IDs: ids}, ids, ev, "")
		run.Counts["proposal_item_conflict"]++
	}
}

// deterministicMerges: without a model, only near-identical wording (Jaccard ≥ 0.85) is merged.
func deterministicMerges(cl []*store.Item) schema.WeeklyOutput {
	var out schema.WeeklyOutput
	sort.Slice(cl, func(i, j int) bool {
		if cl[i].Sources != cl[j].Sources {
			return cl[i].Sources > cl[j].Sources
		}
		return cl[i].ID < cl[j].ID
	})
	keep := cl[0]
	mg := schema.Merge{Keep: keep.ID}
	for _, it := range cl[1:] {
		if norm.Jaccard(it.Claim, keep.Claim) >= 0.85 {
			mg.Drop = append(mg.Drop, it.ID)
		}
	}
	if len(mg.Drop) > 0 {
		out.Merges = append(out.Merges, mg)
	}
	return out
}

var classLabel = map[string]string{store.Fact: "事实", store.Practice: "做事方式", store.Knowledge: "知识"}

// weekItem says whether an item was seen on one of the week's days.
func weekItem(it *store.Item, days []string) bool {
	return slices.ContainsFunc(it.Days, func(d string) bool { return d >= days[0] && d <= days[6] })
}

// proposePins proposes recording ready items of the week as confirmed facts (pinned) on the person or project they
// are about. Ready is an active fact or piece of knowledge, or an established practice, from at least two
// independent sources: one chat's word is never pinned. An item that is about no known person or project is not
// pinned anywhere: a chat is a source, not what the item is about. The hub keeps pinned text through its own
// rewrites and shows it to the assistant.
func (c *Curator) proposePins(ctx context.Context, days []string, run *store.Run) {
	ready := c.St.S.ItemsBy("", func(it *store.Item) bool {
		if it.Pinned || len(it.Flags) > 0 || !weekItem(it, days) || (sources(it.Evidence) < 2 && !ownKept(it)) {
			return false
		}
		return (it.Class != store.Practice && it.Status == "active") || (it.Class == store.Practice && it.Status == "established")
	})
	sort.SliceStable(ready, func(i, j int) bool { return ready[i].Sources > ready[j].Sources })
	var k *known
	n := 0
	for _, it := range ready {
		if n >= PinsPerWeek {
			break
		}
		if k == nil {
			k = c.knownEntities(ctx)
		}
		target := pinTarget(it, k)
		if target == "" {
			continue
		}
		kind, id, _ := strings.Cut(target, "/")
		e := it.Evidence[0]
		line := fmt.Sprintf("- %s：%s（%s %s #%d）", classLabel[it.Class], it.Claim, refName(e.Chat), e.Day, e.N)
		p := c.propose("pin_"+it.Class, classLabel[it.Class]+"："+it.Claim, fmt.Sprintf("写进「%s」的已确认事实；%d 个独立来源", target, it.Sources),
			target, Change{Update: &wire.KBUpdateParams{Kind: kind, ID: id}, PinLine: line}, []string{it.ID}, it.Evidence, "")
		if p.Created == c.today() {
			n++
			run.Counts["proposal_pin_"+it.Class]++
		}
	}
}

func refName(ref string) string {
	_, id, _ := strings.Cut(ref, "/")
	return id
}

// pinTarget: the person or project an item is about, when the knowledge base has it: named by id
// ("people/<id>", "projects/<id>"), or by a name exactly one known person or project goes by. Anything else (a
// chat, a document, a name nobody or several have) is no target: there is no fallback to the chat the evidence is
// from.
func pinTarget(it *store.Item, k *known) string {
	about := strings.TrimSpace(it.About)
	if kind, id, ok := strings.Cut(about, "/"); ok {
		switch {
		case kind == "people" && k.people[id] != "", kind == "projects" && k.projects[id] != "":
			return about
		}
		return ""
	}
	if refs := k.byName[norm.Q(about)]; about != "" && len(refs) == 1 {
		return refs[0]
	}
	return ""
}

func (c *Curator) sample(week string, days []string) {
	if len(c.St.S.Samples[week]) > 0 {
		return
	}
	pool := c.St.S.ItemsBy("", func(it *store.Item) bool {
		return weekItem(it, days) && it.Status != "merged"
	})
	seed := int64(0)
	for _, r := range week {
		seed = seed*31 + int64(r)
	}
	rnd := rand.New(rand.NewSource(seed))
	rnd.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	var ids []string
	for i := 0; i < len(pool) && i < 10; i++ {
		ids = append(ids, pool[i].ID)
	}
	c.St.S.Samples[week] = ids
}

// Judge records a spot-check verdict on an item; "wrong" also takes it out of use.
func (c *Curator) Judge(id, verdict string) error {
	c.St.Lock()
	defer c.St.Unlock()
	it := c.St.S.Items[id]
	if it == nil {
		return fmt.Errorf("没有 %s", id)
	}
	it.Verdict = verdict
	if verdict == "wrong" && !it.Pinned {
		it.Status = "rejected"
	}
	return c.St.Save()
}

// Revalidate checks stored raw answers again under the current rules, without calling the model: after a change
// to the checks, items are rebuilt from what the model said then. Lines are read again from the knowledge base.
func (c *Curator) Revalidate(ctx context.Context) (replayed int, err error) {
	c.St.Lock()
	defer c.St.Unlock()
	owner := c.ownerNames(ctx)
	k := c.knownEntities(ctx)
	run := &store.Run{ID: fmt.Sprintf("revalidate-%d", c.now().Unix()), Kind: "revalidate", Counts: map[string]int{}}
	for _, d := range c.St.S.Done {
		if d.Step != "daily" || d.Ruleset == schema.Ruleset {
			continue
		}
		raw, err := c.St.Raw(d.Raw)
		if err != nil {
			continue
		}
		kind, id, _ := strings.Cut(d.Chat, "/")
		u, ok, err := c.unit(ctx, kind, wire.KBEntry{ID: id}, d.Day)
		if err != nil || !ok {
			continue
		}
		dec := triage.Decision{Chat: d.Chat, Shown: map[int]bool{}, Tier: d.Tier}
		for _, n := range d.Shown {
			dec.Shown[n] = true
		}
		c.ingest(c.builder(d.Day, owner), &u, dec, owner, k, string(raw), d, run, c.storedRewrite(d))
		d.Ruleset = schema.Ruleset
		replayed++
	}
	return replayed, c.St.Save()
}
