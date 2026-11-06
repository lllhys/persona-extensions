package curate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/graph"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/model"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/prompts"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/store"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/triage"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/verify"
)

// RunDaily curates one day: every entity with material that day goes through the same triage, the admitted ones
// are digested with citations, the answers are checked, and what passes becomes items and graph assertions. It is
// idempotent: a unit whose admitted lines and prompt did not change since it was last digested is skipped.
func (c *Curator) RunDaily(ctx context.Context, day string, dry bool) (*store.Run, error) {
	c.St.Lock()
	defer c.St.Unlock()
	run := &store.Run{ID: fmt.Sprintf("daily-%s-%d", day, c.now().Unix()), Kind: "daily", Period: day,
		Started: c.now().UTC().Format(time.RFC3339), DryRun: dry, Counts: map[string]int{}}
	owner := c.ownerNames(ctx)
	units, err := c.units(ctx, day)
	if err != nil {
		return nil, err
	}
	var m model.Model
	var metered *model.Metered
	if c.Model != nil {
		metered = &model.Metered{Model: c.Model, Usage: c.St, Day: day}
		m = metered
	}
	// 1. Triage: one set of rules for every source.
	cfg := c.Cfg.triage()
	decisions := make([]triage.Decision, len(units))
	byRef := map[string]*Unit{}
	for i := range units {
		u := &units[i]
		byRef[u.Ref] = u
		decisions[i] = triage.Decide(triage.Chat{Entity: u.Entity, Meta: u.Meta, Lines: u.Lines, OwnerRecently: u.OwnerRecently, OwnerNames: owner}, cfg)
		decisions[i].Chat = u.Ref
	}
	if triage.SmallDay(decisions, cfg) {
		run.Notes = append(run.Notes, "当天数据量小，除屏蔽、群发邮件外全部读")
		for i := range decisions {
			d := &decisions[i]
			if d.Action != triage.Include && d.Rule != triage.RBlock && d.Rule != triage.MBulk && d.Rule != triage.RPrivate {
				d.Action, d.Rule = triage.Include, triage.RSmall
				for _, l := range units[i].Lines {
					d.Shown[l.N] = true
				}
			}
		}
	}
	funnel := store.Funnel{Chats: map[string]int{}, Tokens: map[string]int{}, Sources: map[string]int{}}
	var admitted []triage.Decision
	modelDown := false
	for i := range decisions {
		d := &decisions[i]
		u := byRef[d.Chat]
		if d.Action == triage.AI {
			c.aiTriage(ctx, m, u, d, owner, run, &modelDown)
		}
		funnel.Chats[d.Rule+"→"+d.Action]++
		funnel.Tokens[d.Action] += d.Tokens
		funnel.Sources[sourceOf(u)]++
		if d.Action == triage.Include || d.Action == triage.Window {
			admitted = append(admitted, *d)
		}
		run.Counts["triage_"+d.Action]++
	}
	funnel.Admitted = len(admitted)
	c.St.S.Funnel[day] = funnel
	triage.Order(admitted)

	// 2. Digest every admitted unit, most valuable first.
	b := c.builder(day, owner)
	known := c.knownEntities(ctx)
	for _, d := range admitted {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		u := byRef[d.Chat]
		if m == nil || modelDown {
			run.Counts["digest_skipped_no_model"]++
			continue
		}
		if err := c.digest(ctx, m, b, u, d, owner, known, run); err != nil {
			if errors.Is(err, model.ErrUnavailable) {
				modelDown = true
				run.Notes = append(run.Notes, "模型不可用（hub 还没有 model.complete）：只做了分拣和确定性检查")
				continue
			}
			run.Notes = append(run.Notes, fmt.Sprintf("%s：%v", u.Meta.Name, err))
			run.Counts["digest_errors"]++
			continue
		}
		if !dry {
			if err := c.St.Save(); err != nil { // checkpoint after every unit
				return nil, err
			}
		}
	}
	if modelDown && !slices.ContainsFunc(run.Notes, func(n string) bool { return strings.Contains(n, "模型不可用") }) {
		run.Notes = append(run.Notes, "模型不可用（hub 还没有 model.complete）：只做了分拣和确定性检查")
	}
	// 3. Proposals that a day can already make, and the queue's upkeep.
	c.expire()
	if metered != nil {
		run.Counts["model_calls"] = metered.Calls
	}
	for _, u := range c.St.S.Usage {
		if u.Day == day {
			run.Tokens += u.In + u.Out
		}
	}
	run.Finished = c.now().UTC().Format(time.RFC3339)
	run.Report = c.dailyReport(day, run, funnel)
	c.St.S.Runs = append(c.St.S.Runs, run)
	if dry {
		return run, nil
	}
	return run, c.St.Save()
}

func sourceOf(u *Unit) string {
	if u.Entity != "chats" {
		return u.Entity
	}
	s := u.Meta.Source
	if s == "" {
		s, _, _ = strings.Cut(u.Meta.ID, "-")
	}
	return s + "/" + u.Meta.Kind
}

// aiTriage asks the model about a unit the rules could not place, unless a tag for the same signals is less than a
// week old. Without a model (or when its answer fails the checks) the unit is kept for the week, not dropped.
func (c *Curator) aiTriage(ctx context.Context, m model.Model, u *Unit, d *triage.Decision, owner []string, run *store.Run, down *bool) {
	fp := triage.Fingerprint(triage.Chat{Meta: u.Meta}, *d)
	if t := c.St.S.Triage[u.Ref]; t != nil && t.Fingerprint == fp && t.At >= addDays(u.Day, -7) {
		c.applyTag(u, d, t.Admit, t.Category, t.Reason+"（缓存）")
		run.Counts["triage_cached"]++
		return
	}
	if m == nil || *down {
		d.Action, d.Reason = triage.DigestOnly, d.Reason+"；模型不可用，先只进周级"
		return
	}
	head, skipped := triage.Head(u.Lines, d.Shown)
	in := schema.TriageInput{Chat: u.Meta.Name, Kind: u.Meta.Kind, Source: sourceOf(u), Members: len(u.Meta.People),
		Owner: owner, Skipped: skipped, Language: c.language()}
	for _, l := range head {
		in.Lines = append(in.Lines, schema.MsgLine{N: l.N, Time: l.Time, Speaker: l.Speaker, Text: l.Body})
	}
	resp, err := m.Complete(ctx, model.Request{Step: "triage", Tier: tier(c.Cfg.DailyTier), System: prompts.Triage,
		User: schema.Must(in), MaxTokens: 400, Schema: prompts.TriageSchema})
	if err != nil {
		if errors.Is(err, model.ErrUnavailable) {
			*down = true
		}
		d.Action, d.Reason = triage.DigestOnly, d.Reason+"；模型没答上，先只进周级"
		return
	}
	var out schema.TriageOutput
	w := c.window(u, d.Shown, owner)
	if json.Unmarshal([]byte(resp.Text), &out) != nil || !slices.Contains([]string{"include", "digest_only", "drop"}, out.Admit) {
		d.Action, d.Reason = triage.DigestOnly, d.Reason+"；模型答案格式不对，先只进周级"
		run.Counts["triage_rejected"]++
		return
	}
	if _, rej, _ := w.Evidence(out.Evidence); rej != "" && out.Admit == "drop" {
		// A drop must be justified by the lines; one that is not is only a downgrade.
		out.Admit, out.Reason = "digest_only", out.Reason+"（理由核验不过："+rej+"）"
		run.Counts["triage_rejected"]++
	}
	c.St.S.Triage[u.Ref] = &store.TriageTag{Chat: u.Ref, Fingerprint: fp, Admit: out.Admit, Category: out.Category,
		Relevance: out.Relevance, Reason: out.Reason, At: u.Day}
	c.applyTag(u, d, out.Admit, out.Category, out.Reason)
	if u.Meta.Kind == "mail" && (out.Category == "marketing" || out.Category == "newsletter") {
		c.proposeMailDowngrade(u, out.Category)
	}
}

func (c *Curator) applyTag(u *Unit, d *triage.Decision, admit, category, reason string) {
	d.Reason = "模型：" + reason
	if category != "" {
		d.Reason += "（" + category + "）"
	}
	switch admit {
	case "include":
		d.Action, d.Score = triage.Include, 40+float64(u.Meta.Relevance)*0.3
	case "drop":
		d.Action = triage.Drop
	default:
		d.Action = triage.DigestOnly
	}
}

func (c *Curator) window(u *Unit, shown map[int]bool, owner []string) *verify.Window {
	w := &verify.Window{Chat: u.Ref, Day: u.Day, Kind: u.Meta.Kind, All: map[int]norm.Line{}, Shown: shown, Owner: owner}
	for _, l := range u.Lines {
		w.All[l.N] = l
	}
	return w
}

// knownEntities are the knowledge base's people and projects, for naming in prompts and seeding graph nodes.
type known struct {
	people   map[string]string // id → name
	projects map[string]string
	byName   map[string][]string // canonical name → refs
}

func (c *Curator) knownEntities(ctx context.Context) *known {
	k := &known{people: map[string]string{}, projects: map[string]string{}, byName: map[string][]string{}}
	for _, kind := range []string{"people", "projects"} {
		es, err := c.Src.List(ctx, kind)
		if err != nil {
			continue
		}
		for _, e := range es {
			if kind == "people" {
				k.people[e.ID] = e.Name
			} else {
				k.projects[e.ID] = e.Name
			}
			k.byName[norm.Q(e.Name)] = append(k.byName[norm.Q(e.Name)], kind+"/"+e.ID)
		}
	}
	return k
}

// digest is the daily call for one unit.
func (c *Curator) digest(ctx context.Context, m model.Model, b *graph.Builder, u *Unit, d triage.Decision, owner []string, k *known, run *store.Run) error {
	in := schema.DailyInput{Day: u.Day, Chat: u.Ref, ChatName: u.Meta.Name, Kind: u.Meta.Kind, Owner: owner,
		Types: b.Reg.Types, Predicates: b.Reg.List(), Language: c.language(), Graph: c.Cfg.Graph}
	if in.Kind == "" {
		in.Kind = strings.TrimSuffix(u.Entity, "s")
	}
	var sig strings.Builder
	speakers := map[string]bool{}
	for _, l := range u.Lines {
		if d.Shown[l.N] {
			in.Lines = append(in.Lines, schema.MsgLine{N: l.N, Time: l.Time, Speaker: l.Speaker, Text: l.Body})
			fmt.Fprintf(&sig, "%d\x00%s\x00%s\n", l.N, l.Speaker, l.Body)
			speakers[norm.Q(l.Speaker)] = true
		}
	}
	// Known entities: the people who spoke, the projects of this chat, the chat itself.
	for _, pid := range u.Meta.People {
		if name := k.people[pid]; name != "" && speakers[norm.Q(name)] {
			in.Known = append(in.Known, schema.Entity{ID: "people/" + pid, Type: "person", Name: name})
		}
	}
	for _, pid := range u.Meta.Projects {
		if name := k.projects[pid]; name != "" {
			in.Known = append(in.Known, schema.Entity{ID: "projects/" + pid, Type: "project", Name: name})
		}
	}
	if u.Entity == "chats" {
		in.Known = append(in.Known, schema.Entity{ID: u.Ref, Type: "chat", Name: u.Meta.Name})
	}
	if u.Meta.Pinned != "" {
		for _, l := range strings.Split(u.Meta.Pinned, "\n") {
			if l = strings.TrimSpace(l); l != "" {
				in.Pinned = append(in.Pinned, l)
			}
		}
	}
	key := hash("daily", u.Ref, u.Day, sig.String(), schema.DailyPrompt)[:20]
	if c.St.S.Done[key] != nil {
		run.Counts["digest_unchanged"]++
		return nil
	}
	resp, err := m.Complete(ctx, model.Request{Step: "daily", Tier: tier(c.Cfg.DailyTier), System: prompts.Daily,
		User: schema.Must(in), MaxTokens: 4000, Schema: prompts.DailySchema})
	if err != nil {
		return err
	}
	raw, err := c.St.PutRaw(key, []byte(resp.Text))
	if err != nil {
		return err
	}
	done := &store.Done{Key: key, Step: "daily", Chat: u.Ref, Day: u.Day, At: c.now().UTC().Format(time.RFC3339), Raw: raw,
		Prompt: schema.DailyPrompt, Ruleset: schema.Ruleset}
	for n := range d.Shown {
		done.Shown = append(done.Shown, n)
	}
	sort.Ints(done.Shown)
	c.ingest(b, u, d, owner, k, resp.Text, done, run)
	c.St.S.Done[key] = done
	run.Counts["digested"]++
	return nil
}

// ingest checks an answer and keeps what passes. It is also what Revalidate replays over stored raw answers.
func (c *Curator) ingest(b *graph.Builder, u *Unit, d triage.Decision, owner []string, k *known, text string, done *store.Done, run *store.Run) {
	var out schema.DailyOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		c.reject(done, "answer", verify.Schema, "", u.Day)
		run.Counts["rejected"]++
		return
	}
	w := c.window(u, d.Shown, owner)
	dg := &store.Digest{Chat: u.Ref, Day: u.Day, Key: done.Key}
	for _, p := range out.Summary {
		ev, rej, _ := w.Evidence(p.Evidence)
		if rej != "" {
			c.reject(done, "summary", rej, p.Text, u.Day)
			continue
		}
		dg.Points = append(dg.Points, store.Point{Text: p.Text, Evidence: ev})
	}
	for class, items := range map[string][]schema.Item{store.Fact: out.Facts, store.Practice: out.Practices, store.Knowledge: out.Knowledge} {
		for _, it := range items {
			ev, rej, flags := w.Item(it)
			if rej != "" {
				c.reject(done, class, rej, it.Claim, u.Day)
				run.Counts["rejected"]++
				done.Reject++
				continue
			}
			id := c.keepItem(class, it, ev, flags, u.Day)
			dg.Items = append(dg.Items, id)
			if len(flags) > 0 {
				done.Flagged++
				run.Counts["flagged"]++
			} else {
				done.Passed++
				run.Counts["passed"]++
			}
			run.Counts[class]++
		}
	}
	if c.Cfg.Graph {
		c.ingestGraph(b, w, u, out, k, done, run)
	}
	c.St.S.Digests[u.Ref+"@"+u.Day] = dg
}

func (c *Curator) reject(done *store.Done, class, code, claim, day string) {
	c.St.S.Rejections = append(c.St.S.Rejections, store.Rejection{Key: done.Key, Class: class, Code: code, Claim: claim, Day: day})
}

// keepItem adds an item or adds evidence to the same claim seen before. Identity is the class, what it is about
// and the claim in canonical form, so restating a fact on another day strengthens it instead of copying it.
func (c *Curator) keepItem(class string, it schema.Item, ev []store.Evidence, flags []string, day string) string {
	id := "i-" + hash(class, norm.Q(it.About), norm.Q(it.Claim))[:12]
	week := ISOWeek(day)
	cur := c.St.S.Items[id]
	if cur == nil {
		// Same quotes within 7 days as an existing item of the class: the same observation worded differently.
		if dup := c.sameQuotes(class, ev, day); dup != nil {
			cur, id = dup, dup.ID
		}
	}
	if cur == nil {
		cur = &store.Item{ID: id, Class: class, Claim: it.Claim, About: it.About, Scope: it.Scope, When: it.When,
			Confidence: it.Confidence, Flags: flags, FirstSeen: day, Prompt: schema.DailyPrompt, Ruleset: schema.Ruleset}
		switch class {
		case store.Practice:
			cur.Status = "observed"
		default:
			cur.Status = "candidate"
		}
		c.St.S.Items[id] = cur
	} else if len(flags) == 0 && len(cur.Flags) > 0 {
		cur.Flags = nil // clean evidence for the same claim outweighs a flagged one
	}
	for _, e := range ev {
		if !slices.ContainsFunc(cur.Evidence, func(x store.Evidence) bool { return x.Chat == e.Chat && x.Day == e.Day && x.N == e.N }) {
			cur.Evidence = append(cur.Evidence, e)
		}
	}
	if !slices.Contains(cur.Days, day) {
		cur.Days = append(cur.Days, day)
		sort.Strings(cur.Days)
	}
	if !slices.Contains(cur.Weeks, week) {
		cur.Weeks = append(cur.Weeks, week)
		sort.Strings(cur.Weeks)
	}
	if day > cur.LastSeen {
		cur.LastSeen = day
	}
	if cur.Confidence != "high" && it.Confidence == "high" {
		cur.Confidence = "high"
	}
	cur.Sources = sources(cur.Evidence)
	cur.Updated = day
	promote(cur)
	return id
}

func (c *Curator) sameQuotes(class string, ev []store.Evidence, day string) *store.Item {
	qk := quoteKey(ev)
	for _, it := range c.St.S.Items {
		if it.Class == class && it.Status != "merged" && it.LastSeen >= addDays(day, -7) && quoteKey(it.Evidence) == qk {
			return it
		}
	}
	return nil
}

func quoteKey(ev []store.Evidence) string {
	var qs []string
	for _, e := range ev {
		qs = append(qs, norm.Q(e.Quote))
	}
	sort.Strings(qs)
	return strings.Join(qs, "|")
}

// sources counts independent sources: distinct (chat, speaker) pairs, so one person repeating themselves in one
// chat counts once.
func sources(ev []store.Evidence) int {
	seen := map[string]bool{}
	for _, e := range ev {
		seen[e.Chat+"\x00"+e.Speaker] = true
	}
	return len(seen)
}

// promote moves an item up as its evidence grows. Practices: observed → recurring (seen on two days or from two
// sources) → established (seen in two different weeks, or from three sources). Facts and knowledge: candidate →
// active (plainly stated without flags, or two independent sources).
func promote(it *store.Item) {
	switch it.Class {
	case store.Practice:
		if it.Status == "observed" && (len(it.Days) >= 2 || it.Sources >= 2) {
			it.Status = "recurring"
		}
		if (it.Status == "recurring" || it.Status == "dormant") && (len(it.Weeks) >= 2 || it.Sources >= 3) && len(it.Flags) == 0 {
			it.Status = "established"
		}
	default:
		if it.Status == "candidate" && len(it.Flags) == 0 && (it.Confidence == "high" || it.Sources >= 2) {
			it.Status = "active"
		}
		if it.Status == "stale" {
			it.Status = "active"
		}
	}
}

// ingestGraph turns verified assertions into graph relations.
func (c *Curator) ingestGraph(b *graph.Builder, w *verify.Window, u *Unit, out schema.DailyOutput, k *known, done *store.Done, run *store.Run) {
	mentionType := map[string]string{}
	for _, mn := range out.Mentions {
		mentionType[norm.Q(mn.Surface)] = mn.Type
	}
	resolve := func(name string, allowed []string, ev *store.Evidence) (string, []string) {
		if kind, id, ok := strings.Cut(name, "/"); ok && slices.Contains([]string{"people", "projects", "chats", "docs", "meetings", "events"}, kind) {
			typ := map[string]string{"people": "person", "projects": "project", "chats": "chat", "docs": "doc", "meetings": "meeting", "events": "meeting"}[kind]
			disp := k.people[id]
			if kind == "projects" {
				disp = k.projects[id]
			}
			if name == u.Ref {
				disp = u.Meta.Name
			}
			if disp == "" {
				disp = id
			}
			nid := b.Seed(typ, name, disp, nil)
			return nid, append([]string{disp}, b.Names(nid)...)
		}
		typ := mentionType[norm.Q(name)]
		if typ == "" && len(allowed) > 0 {
			typ = allowed[0]
		}
		// A name the knowledge base has: seed from it, so nodes point at entities.
		if refs := k.byName[norm.Q(name)]; len(refs) == 1 {
			kind, _, _ := strings.Cut(refs[0], "/")
			if (kind == "people" && typ == "person") || (kind == "projects" && typ == "project") {
				nid := b.Seed(typ, refs[0], name, nil)
				return nid, b.Names(nid)
			}
		}
		nid, ok := b.Resolve(name, typ, "", ev)
		if !ok {
			return "", []string{name}
		}
		return nid, append([]string{name}, b.Names(nid)...)
	}
	for _, a := range out.Assertions {
		pr, ok := b.Reg.Predicates[a.P]
		if !ok || len(a.Evidence) == 0 {
			c.reject(done, "assertion", verify.Schema, a.S+" "+a.P+" "+a.O, u.Day)
			run.Counts["rejected"]++
			continue
		}
		first := &store.Evidence{Chat: u.Ref, Day: u.Day, N: a.Evidence[0].N, Quote: a.Evidence[0].Quote}
		sID, sNames := resolve(a.S, pr.Subject, first)
		oID, oNames := resolve(a.O, pr.Object, first)
		if sID == "" || oID == "" || !slices.Contains(pr.Subject, b.TypeOf(sID)) || !slices.Contains(pr.Object, b.TypeOf(oID)) {
			c.reject(done, "assertion", verify.Schema, a.S+" "+a.P+" "+a.O, u.Day)
			run.Counts["rejected"]++
			continue
		}
		ev, rej, flags := w.Assertion(a, sNames, oNames, b.TypeOf(sID) == "person")
		if rej != "" {
			c.reject(done, "assertion", rej, a.S+" "+a.P+" "+a.O, u.Day)
			run.Counts["rejected"]++
			continue
		}
		attrs := a.Attrs
		id, outcome := b.Add(sID, a.P, oID, attrs, a.When, ev, a.Confidence, flags, schema.DailyPrompt)
		run.Counts["graph_"+outcome]++
		if outcome == "conflict" {
			c.proposeGraphConflict(b, id)
		}
	}
}
