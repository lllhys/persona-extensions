package curate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/convsrc"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/graph"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/prompts"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/speaker"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

// RunDaily curates one day: every entity with material that day goes through the same triage, the admitted ones
// are digested with citations, the answers are checked, and what passes becomes items and graph assertions. It is
// idempotent: a unit whose admitted lines and prompt did not change since it was last digested is skipped.
func (c *Curator) RunDaily(ctx context.Context, day string, dry bool) (*store.Run, error) {
	live := c.Live
	if dry {
		live = nil // a trial on the curator's copy is not the curator's run
	}
	live.begin("daily", day, c.now())
	defer live.end()
	c.St.Lock()
	defer c.St.Unlock()
	run := &store.Run{ID: fmt.Sprintf("daily-%s-%d", day, c.now().Unix()), Kind: "daily", Period: day,
		Started: c.now().UTC().Format(time.RFC3339), DryRun: dry, Counts: map[string]int{}}
	owner := c.ownerNames(ctx)
	if !dry {
		c.collectBegin(day)
	}
	units, err := c.units(ctx, day)
	if errors.Is(err, convsrc.ErrAway) {
		// The assistant's computer is not connected: the day cannot be read whole now. Not done; tried again after
		// the same wait as when the model is unavailable, instead of only at the next start.
		perr := c.notePauseOf(day, err, "assistant")
		if !dry {
			if serr := c.St.Save(); serr != nil {
				return nil, serr
			}
			c.Publish()
		}
		return nil, perr
	}
	if err != nil {
		return nil, err
	}
	var m model.Model
	var metered *model.Metered
	if c.Model != nil {
		metered = &model.Metered{Model: c.Model, Usage: c.St, Day: day}
		m = metered
	}
	// 0. Who is who (3.2): the intersection list counts the day's dealings, the attribution checks sample it.
	known := c.knownEntities(ctx)
	if !dry {
		c.observeContacts(ctx, day, units)
		c.checkSpeakers(day, units)
		c.weakOwnerNote(units)
	}
	contacts, topics := c.contactSet(), c.topics(known)
	// 1. Triage: one set of rules for every source.
	cfg := c.Cfg.triage()
	decisions := make([]triage.Decision, len(units))
	byRef := map[string]*Unit{}
	for i := range units {
		u := &units[i]
		byRef[u.Ref] = u
		decisions[i] = triage.Decide(triage.Chat{Entity: u.Entity, Meta: u.Meta, Lines: u.Lines, OwnerRecently: u.OwnerRecently, OwnerLast: u.OwnerLast, OwnerNames: owner,
			Who: u.Who, Contacts: contacts, Topics: topics}, cfg)
		decisions[i].Chat = u.Ref
	}
	if triage.SmallDay(decisions, cfg) {
		run.Notes = append(run.Notes, "当天数据量小，除屏蔽、群发邮件外全部读（其他邮件仍由模型分类）")
		for i := range decisions {
			d := &decisions[i]
			// Mail the rules could not place still goes to the model: marketing is dropped for everyone.
			if d.Action != triage.Include && d.Rule != triage.RBlock && d.Rule != triage.MBulk && d.Rule != triage.RPrivate &&
				d.Rule != triage.MOther && d.Rule != triage.RNoise && d.Rule != triage.RNoIntersect && d.Rule != triage.RIntersect {
				d.Action, d.Rule = triage.Include, triage.RSmall
				if cfg.Tiers && d.Tier == "" {
					d.Tier = triage.TierBackground // read because the day is small, not because it is the owner's
				}
				for _, l := range units[i].Lines {
					d.Shown[l.N] = true
				}
			}
		}
	}
	live.progress(func(p *Progress) { p.Stage, p.Total = "分拣", len(decisions) })
	funnel := store.Funnel{Chats: map[string]int{}, Tokens: map[string]int{}, Sources: map[string]int{}}
	if cfg.Tiers {
		funnel.Tiers, funnel.TierTokens = map[string]int{}, map[string]int{}
	}
	var admitted []triage.Decision
	modelDown := false
	var downErr error // the hub's answer when the model was unavailable
	for i := range decisions {
		d := &decisions[i]
		u := byRef[d.Chat]
		if d.Action == triage.AI {
			live.progress(func(p *Progress) { p.Done, p.Unit = i+1, u.Meta.Name })
			c.aiTriage(ctx, m, u, d, owner, run, &modelDown, &downErr)
		}
		funnel.Chats[d.Rule+"→"+d.Action]++
		funnel.Tokens[d.Action] += d.Tokens
		funnel.Sources[sourceOf(u)]++
		if d.Action == triage.Include || d.Action == triage.Window {
			admitted = append(admitted, *d)
			if cfg.Tiers {
				funnel.Tiers[d.Tier]++
				funnel.TierTokens[d.Tier] += d.Tokens
			}
		} else if cfg.Tiers && d.Rule == triage.RNoise {
			funnel.Tiers["noise"]++
			funnel.TierTokens["noise"] += tokensOf(u)
		}
		run.Counts["triage_"+d.Action]++
	}
	funnel.Admitted = len(admitted)
	c.St.S.Funnel[day] = funnel
	c.recordAdmitted(day, admitted, dry)
	triage.Order(admitted)
	if n := c.focusFirst(ctx, admitted, byRef); n > 0 {
		run.Counts["focus_first"] = n // first-run replay: units about the user's focus (first-run question 3) first
	}
	live.progress(func(p *Progress) {
		p.Stage, p.Done, p.Total, p.Unit, p.Refused = "摘要", 0, len(admitted), "", len(run.Refusals)
	})

	// 2. Digest every admitted unit, most valuable first. With Workers > 1 the calls are made concurrently first;
	// the answers are then taken in this same order, so everything the answers change stays single-threaded.
	b := c.builder(day, owner)
	if m != nil && !modelDown && c.Workers > 1 {
		m = c.prefetch(ctx, metered, admitted, byRef, b, owner, known)
	}
	for _, d := range admitted {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		u := byRef[d.Chat]
		var err error
		if m == nil || modelDown {
			run.Counts["digest_skipped_no_model"]++
			continue
		}
		live.progress(func(p *Progress) { p.Done, p.Unit = p.Done+1, u.Meta.Name })
		// A unit over the batch size is read in parts (dailyParts); a part the model refuses is split further.
		for _, p := range dailyParts(u, d) {
			err = c.batch("daily", u, p.d, p.part, 0, run, func(d triage.Decision, part string) error {
				return c.digest(ctx, m, b, u, d, part, owner, known, run)
			})
			if err != nil {
				break
			}
		}
		if err != nil {
			if errors.Is(err, model.ErrUnavailable) {
				modelDown, downErr = true, err
				run.Notes = append(run.Notes, "模型不可用（"+modelReason(err)+"）：只做了分拣和确定性检查")
				continue
			}
			run.Notes = append(run.Notes, fmt.Sprintf("%s：%v", u.Meta.Name, err))
			run.Counts["digest_errors"]++
			continue
		}
		live.progress(func(p *Progress) { p.Refused = len(run.Refusals) })
		if !dry {
			if err := c.St.Save(); err != nil { // checkpoint after every unit
				return nil, err
			}
		}
	}
	if m != nil && modelDown {
		// The model went away before every admitted unit was read (or before the rules' open cases were triaged): the
		// day is not done. What was read is kept; the day runs again when the model is back, from where it stopped.
		if downErr == nil {
			downErr = model.ErrUnavailable
		}
		err := c.notePause(day, downErr)
		if !dry {
			if serr := c.St.Save(); serr != nil {
				return nil, serr
			}
			c.Publish()
		}
		return nil, err
	}
	if m != nil && !dry {
		c.clearPause()
	}
	// 3. Consolidation (design 5.0): the day's new entries recalled, judged and filed, counted, statuses settled.
	if len(units) > 0 {
		c.MarkActive(day)
	}
	if !dry {
		if _, err := c.Migrate(); err != nil {
			run.Notes = append(run.Notes, "迁移旧判断层："+err.Error())
		}
		var cm model.Model
		if m != nil && !modelDown {
			cm = m
		}
		if err := c.Consolidate(ctx, cm, day, run); err != nil {
			return nil, err
		}
	}
	// 4. Proposals that a day can already make, and the queue's upkeep; commitments past due are flagged (5.4).
	c.expire()
	c.markCommitments(day, run)
	c.afterDaily(ctx, m, day, run, dry) // signals and the ledger, then the exits (curate/exits.go)
	if !dry {
		// Commit source cursors and ActiveDays only after the whole day completed. In particular, the model-down return
		// above leaves them untouched so a retry rereads everything the model could not summarize.
		c.collectEnd(day, len(units))
	}
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
	err = c.St.Save()
	c.Publish()
	return run, err
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
func (c *Curator) aiTriage(ctx context.Context, m model.Model, u *Unit, d *triage.Decision, owner []string, run *store.Run, down *bool, why *error) {
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
		User: schema.Must(in), MaxTokens: 4000, Schema: prompts.TriageSchema})
	if err != nil {
		if errors.Is(err, model.ErrUnavailable) {
			*down, *why = true, err
		}
		if errors.Is(err, model.ErrRefused) { // only a decision about the unit: no need to split it, the week reads it
			r := c.refusal("triage", u, lineNumbers(head), err)
			r.Outcome = store.RefusalWeekly
			run.Refusals = append(run.Refusals, r)
			run.Counts["triage_refused"]++
			d.Action, d.Reason = triage.DigestOnly, d.Reason+"；模型拒答，先只进周级"
			return
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
	w := &verify.Window{Chat: u.Ref, Day: u.Day, Kind: u.Meta.Kind, All: map[int]norm.Line{}, Shown: shown, Owner: owner,
		Names: append([]string{u.Meta.Name, u.Ref}, u.Meta.Aliases...)}
	for _, l := range u.Lines {
		w.All[l.N] = l
	}
	return w
}

// knownEntities are the knowledge base's people and projects, for naming in prompts and seeding graph nodes, and
// what the release rules (5.4) need: the user's projects' names and aliases, whether kb.owner is too thin to tell
// the user's lines in groups and mail, and the K7 switches.
type known struct {
	people   map[string]string // id → name
	projects map[string]string
	byName   map[string][]string // canonical name or alias → refs
	// topics are the user's projects' names and aliases that are distinctive enough to look for in a line.
	topics []string
	// weak: kb.owner has a name at most; groups and mail teach nothing about the user (③④⑥, 5.0).
	weak bool
	// k7: facts about others are released at all (K7); k7group: a group's line naming a project of the user's is
	// released (K7.group).
	k7, k7group bool
	// contacts are the person ids on the intersection list: where a person called by the given name only is looked for
	// when the unit's members do not have them.
	contacts []string
}

func (c *Curator) knownEntities(ctx context.Context) *known {
	k := &known{people: map[string]string{}, projects: map[string]string{}, byName: map[string][]string{},
		k7: entry.On(c.St.S.Switches, entry.K7WorldFacts), k7group: entry.On(c.St.S.Switches, entry.K7GroupByProject)}
	for _, ct := range c.St.S.Contacts {
		if ct.Manual != "removed" {
			k.contacts = append(k.contacts, strings.TrimPrefix(ct.Person, "person:"))
		}
	}
	slices.Sort(k.contacts)
	index := func(name, ref string) {
		q := norm.Q(name)
		if q != "" && !slices.Contains(k.byName[q], ref) {
			k.byName[q] = append(k.byName[q], ref)
		}
	}
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
			index(e.Name, kind+"/"+e.ID)
		}
	}
	// Aliases: every project's (a few dozen reads), and the people's the speaker resolution already read (members of
	// the units seen, the intersection list among them): never a read for each of ten thousand people.
	for _, id := range sortedKeys(k.projects) {
		names := []string{k.projects[id]}
		if d, err := c.Src.Get(ctx, "projects", id); err == nil {
			for _, a := range kbsrc.DecodeMeta(d).Aliases {
				if !graphGeneric(a) {
					index(a, "projects/"+id)
					names = append(names, a)
				}
			}
		}
		for _, n := range names {
			if distinctiveName(n) && !slices.Contains(k.topics, n) {
				k.topics = append(k.topics, n)
			}
		}
	}
	w := c.whoC()
	w.mu.Lock()
	for id, p := range w.details {
		if k.people[id] == "" {
			continue
		}
		for _, a := range p.Aliases {
			index(a, "people/"+id)
		}
	}
	w.mu.Unlock()
	k.weak = c.speakerOwner(ctx).Weak
	return k
}

// digestRequest builds the daily call for one unit and its ledger key. It is the one place the request is made:
// the sequential pass and the concurrent prefetch (Workers > 1) both use it, so they ask the same thing.
func (c *Curator) digestRequest(b *graph.Builder, u *Unit, d triage.Decision, owner []string, k *known) (model.Request, string) {
	lang := c.language()
	in := schema.DailyInput{Day: u.Day, Chat: u.Ref, ChatName: u.Meta.Name, Kind: u.Meta.Kind, Owner: owner, Tier: d.Tier,
		Types: b.Reg.Types, Predicates: b.Reg.List(), Language: lang, Graph: c.Cfg.Graph,
		Source: unitCode(u), Origin: string(unitOrigin(u.Ref)), UserWord: norm.UserWord(lang)}
	if in.Kind == "" {
		in.Kind = strings.TrimSuffix(u.Entity, "s")
	}
	kind := u.Meta.Kind
	if kind == "" {
		kind = in.Kind
	}
	var sig strings.Builder
	speakers := map[string]bool{}
	var bodies strings.Builder
	for _, l := range u.Lines {
		if d.Shown[l.N] {
			who := whoOf(u, k, l, kind)
			in.Lines = append(in.Lines, schema.MsgLine{N: l.N, Time: l.Time, Speaker: l.Speaker, Text: l.Body, Who: who})
			fmt.Fprintf(&sig, "%d\x00%s\x00%s\x00%s\n", l.N, l.Speaker, l.Body, who)
			speakers[norm.Q(l.Speaker)] = true
			bodies.WriteString(l.Body + "\n")
		}
	}
	// Known entities: the people who spoke or are named in the lines (a card takes what others say of someone too),
	// the projects of this chat, the chat itself.
	text := bodies.String()
	for _, pid := range u.Meta.People {
		if name := k.people[pid]; name != "" && (speakers[norm.Q(name)] || (utf8.RuneCountInString(name) >= 2 && norm.Contains(text, name))) {
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
	key := hash("daily", u.Ref, u.Day, sig.String(), schema.DailyPrompt, lang)[:20]
	return model.Request{Step: "daily", Tier: tier(c.Cfg.DailyTier), System: prompts.Daily,
		User: schema.Must(in), MaxTokens: dailyMaxTokens, Schema: prompts.DailySchema}, key
}

// digest is the daily call for one unit.
// digest is the daily call for one unit (or one part of a unit the model refused whole).
func (c *Curator) digest(ctx context.Context, m model.Model, b *graph.Builder, u *Unit, d triage.Decision, part string, owner []string, k *known, run *store.Run) error {
	req, key := c.digestRequest(b, u, d, owner, k)
	if c.St.S.Done[key] != nil {
		run.Counts["digest_unchanged"]++
		return nil
	}
	resp, err := m.Complete(ctx, req)
	if err != nil {
		return err
	}
	raw, err := c.St.PutRaw(key, []byte(resp.Text))
	if err != nil {
		return err
	}
	done := &store.Done{Key: key, Step: "daily", Chat: u.Ref, Day: u.Day, At: c.now().UTC().Format(time.RFC3339), Raw: raw,
		Prompt: schema.DailyPrompt, Ruleset: schema.Ruleset, Part: part, Tier: d.Tier}
	for n := range d.Shown {
		done.Shown = append(done.Shown, n)
	}
	sort.Ints(done.Shown)
	fix := func(in schema.RewriteInput) (schema.RewriteOutput, bool) {
		run.Counts["rewrite_calls"]++
		r, err := m.Complete(ctx, model.Request{Step: "rewrite", Tier: tier(c.Cfg.DailyTier), System: prompts.Rewrite,
			User: schema.Must(in), MaxTokens: rewriteMaxTokens, Schema: prompts.RewriteSchema})
		var out schema.RewriteOutput
		if err != nil || json.Unmarshal([]byte(r.Text), &out) != nil {
			run.Counts["rewrite_failed"]++
			return out, false
		}
		if _, err := c.St.PutRaw(rewriteKey(key), []byte(r.Text)); err != nil {
			c.logf("rewrite of %s not kept: %v", key, err)
		}
		return out, true
	}
	c.ingest(b, u, d, owner, k, resp.Text, done, run, fix)
	c.St.S.Done[key] = done
	run.Counts["digested"]++
	return nil
}

// ingest checks an answer and keeps what passes. It is also what Revalidate replays over stored raw answers.
func (c *Curator) ingest(b *graph.Builder, u *Unit, d triage.Decision, owner []string, k *known, text string, done *store.Done, run *store.Run, fix rewriter) {
	var out schema.DailyOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		c.reject(done, "answer", verify.Schema, "", u.Day)
		run.Counts["rejected"]++
		run.Counts["answer_unparsed"]++
		return
	}
	current := schema.DailyAtLeast(done.Prompt, 8) // daily/8: observations, impact, language and K13 checks
	var rewritten map[string]bool
	if current {
		// The answer as a whole against its schema: counted (the hub asks again for one that does not conform; a
		// model run any other way does not), then every item is checked on its own all the same.
		if errs := verify.Conforms(prompts.DailySchema, text, 3); len(errs) > 0 {
			run.Counts["answer_nonconforming"]++
			c.reject(done, "answer_shape", verify.Schema, strings.Join(errs, "; "), u.Day)
		} else {
			run.Counts["answer_conforming"]++
		}
		rewritten = c.fixWording(&out, owner, fix, run)
	}
	w := c.window(u, d.Shown, owner)
	if k != nil {
		for _, p := range u.Meta.People {
			w.Names = append(w.Names, k.people[p])
		}
	}
	dg := &store.Digest{Chat: u.Ref, Day: u.Day, Key: done.Key}
	tier := d.Tier
	if tier == "" {
		tier = done.Tier
	}
	if tier == triage.TierBackground {
		// A group the owner keeps quiet in gives its entities and how they relate, nothing else: what happened
		// there and what was decided is not the owner's record.
		out.Summary = nil
		out.Facts = tierOut(c, done, u, out.Facts, store.Fact, run)
		out.Practices = tierOut(c, done, u, out.Practices, store.Practice, run)
		out.Knowledge = tierOut(c, done, u, out.Knowledge, store.Knowledge, run)
		out.Assertions = structural(c, done, u, out.Assertions, structuralPredicates, run)
	}
	if tier == triage.TierCode {
		// Code review gives no relations: what a pull request references, fixes or depends on is the forge's own
		// links, and what it says about owners is mostly an agent's write-up.
		out.Assertions = structural(c, done, u, out.Assertions, nil, run)
	}
	for _, p := range out.Summary {
		ev, rej, _ := w.Evidence(p.Evidence)
		if rej != "" {
			c.reject(done, "summary", rej, p.Text, u.Day)
			continue
		}
		for i := range ev {
			ev[i].Quote = verify.Redact(ev[i].Quote)
		}
		dg.Points = append(dg.Points, store.Point{Text: verify.Redact(p.Text), Evidence: ev})
	}
	// In a fixed order, so the rejection log and the digest's items come out the same on every run.
	for _, ci := range []struct {
		class string
		items []schema.Item
	}{{store.Fact, out.Facts}, {store.Practice, out.Practices}, {store.Knowledge, out.Knowledge}} {
		class, items := ci.class, ci.items
		for fi, it := range items {
			ev, rej, flags := w.Item(it)
			sens := ""
			if rej == "" && current && class == store.Fact {
				rej, sens = c.factGate(it, ev, owner)
				if rewritten[fmt.Sprintf("f%d", fi)] {
					flags = append(flags, flagRewritten)
				}
			}
			if rej == "" {
				if class == store.Fact && done.Prompt == schema.DailyPrompt && slices.Contains(schema.WorldKinds, it.Kind) &&
					(tier == "" || tier == triage.TierOwn || tier == triage.TierEngaged) && aboutKnown(k, it.About) {
					rej = worldGate(w, it, ev, k) // a fact about the user's world (5.4)
				} else {
					rej = itemGate(w, class, it, ev, k)
				}
			}
			if rej == "" && (tier == triage.TierEngaged || tier == triage.TierCode) && class != store.Fact && !ownLine(w, ev) {
				// In a group the owner takes part in, a way of working or a piece of knowledge is the owner's only
				// when the owner said it: the group's consensus is not the owner's practice. So in code review.
				rej = verify.TierNotAllowed
			}
			if rej == "" && class == store.Fact && current {
				rej = factCheck(it) // a fact that changes nothing the assistant does is not kept
			}
			if rej == "" {
				ev, rej = w.Supply(it.Claim, ev) // its names, numbers and times are in the lines it cites (F1, 9.5)
			}
			if rej == "" && tier == triage.TierCode && class == store.Fact {
				// A pull request's progress (opened, approved, merged) is the forge's own record and soon over.
				rej = verify.TierNotAllowed
			}
			if rej != "" {
				claim := it.Claim
				if rej == verify.DetailNotInEvidence {
					claim = withMissing(claim, w.Missing)
				}
				c.reject(done, class, rej, claim, u.Day)
				run.Counts["rejected"]++
				if rej == verify.TierNotAllowed {
					run.Counts["rejected_tier"]++
				}
				done.Reject++
				continue
			}
			for i := range ev {
				ev[i].Quote = verify.Redact(ev[i].Quote)
			}
			id := c.keepItem(class, it, sourced(ev), flags, u.Day)
			dg.Items = append(dg.Items, id)
			if current && class == store.Fact {
				if eid := c.Observe(c.worldEntry(u, k, w, it, sourced(ev), flags, sens), run.ID); eid != "" {
					run.Counts["obs:world"]++
				}
			}
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
	// Answers to daily/7 and earlier carry judgements and rules; daily/8 gives them as observations.
	c.ingestJudgements(w, u, tier, out.Judgements, done, run)
	c.ingestRules(w, u, tier, out.Rules, done, run)
	if current {
		c.ingestObservations(w, u, k, tier, out.Observations, owner, rewritten, done, run)
	}
	if c.Cfg.Graph {
		c.ingestGraph(b, w, u, out, k, done, run)
	}
	at := u.Ref + "@" + u.Day
	if done.Part != "" {
		at += "#" + done.Part
	}
	c.St.S.Digests[at] = dg
}

// selfUnverified: the user's own line in a group or mail while kb.owner is too thin to tell the user's lines and
// mentions there (5.0): ③④⑥ are not taken from it until the user adds their accounts and aliases.
const selfUnverified = "SELF_UNVERIFIED"

// itemGate is what an item may be about, once its citations check out. A money notice (a bank, payroll or broker
// message, or a claim about someone's money) is never an item, whoever wrote it. Otherwise an item is kept when at
// least one of its lines is
//   - the user's own words (speaker norm.Me after relabelling: ghost-written lines are not), saying something of
//     their own; a practice (④) not from a group or mail while kb.owner is weak (5.0);
//   - another person naming the user (an @-mention or the real name; generic words never count);
//   - for a fact or a piece of knowledge about the user's world (5.4, K7): the other side of a direct chat (said to
//     the user), or a line of a group, mail or thread whose content names one of the user's projects or its alias
//     (K7.group).
//
// Being in the knowledge base is not a reason: with many contacts everyone in a group is there. A line of an older
// mail quoted inside a message is nobody's here: it is not the outer sender's words, nor the user's.
func itemGate(w *verify.Window, class string, it schema.Item, ev []store.Evidence, k *known) string {
	if voice.Finance(it.Claim) {
		return verify.PrivateFinance
	}
	for _, e := range ev {
		if voice.Finance(w.All[e.N].Body) || voice.Finance(e.Quote) {
			return verify.PrivateFinance
		}
	}
	// What it rests on: the user's own words, saying something of their own (not only "好" to what was said), or a
	// person addressing the user by name. The assistant, an agent's draft or a bot is never the source, even when it
	// names the user or the user agreed to it.
	assistant, weakSelf := false, false
	for _, e := range ev {
		l := w.All[e.N]
		if w.Kind == "mail" && speaker.Quoted(l.Body, e.Quote) {
			continue
		}
		switch role := voice.Role(l, w.Kind); {
		case role == voice.RoleUser && !voice.Agreement(e.Quote):
			if class == store.Practice && k.selfOff(w) {
				weakSelf = true
				continue
			}
			return ""
		case role == voice.RoleOther && voice.Mentions(l.Body, w.Owner):
			return ""
		case role == voice.RoleOther && class != store.Practice && k.release(w, it, l, e):
			return ""
		case !voice.Source(role) || role == voice.RoleUser:
			assistant = true
		}
	}
	switch {
	case weakSelf:
		return selfUnverified
	case assistant:
		return verify.AssistantSource
	}
	return verify.NotAboutOwner
}

// selfOff: the user's lines in this window may not teach ③④⑥ (a weak kb.owner, a group or mail).
func (k *known) selfOff(w *verify.Window) bool {
	return k != nil && k.weak && (w.Kind == "group" || w.Kind == "mail")
}

// release says whether another person's line supports a fact about the user's world (5.4): the other side of a
// direct chat speaks to the user; in a group, a mail or a thread, only a line whose content (or the claim it
// supports) names one of the user's projects or aliases. Not "the person is in the knowledge base", not everything.
func (k *known) release(w *verify.Window, it schema.Item, l norm.Line, e store.Evidence) bool {
	if k == nil || !k.k7 {
		return false
	}
	if w.Kind == "dm" {
		return true
	}
	if !k.k7group || w.Kind == "bot" {
		return false
	}
	body := l.Body
	if w.Kind == "mail" {
		body = speaker.OwnText(body)
	}
	text := it.Claim + "\n" + body // the content: the line and the claim it supports; not the model's About
	for _, t := range k.topics {
		if voice.Mentions(text, []string{t}) {
			return true
		}
	}
	return false
}

// aboutKnown says whether a fact is about a person or project the knowledge base has: by id, or by a name or alias
// some known entity goes by. It only picks the gate; being known releases nothing by itself (worldGate).
func aboutKnown(k *known, about string) bool {
	about = strings.TrimSpace(about)
	if about == "" || k == nil {
		return false
	}
	if kind, id, ok := strings.Cut(about, "/"); ok {
		return (kind == "people" && k.people[id] != "") || (kind == "projects" && k.projects[id] != "")
	}
	return len(k.byName[norm.Q(about)]) > 0
}

// worldGate is the gate for a fact about the user's world (5.4). It used to take anyone's word when the fact was
// about someone in the knowledge base; with many contacts that let a group's noise back in (verification 2: 17
// released, 3 noise). It now releases by the same rules as itemGate: the user's words, a line naming the user, the
// other side of a direct chat, or a group line naming one of the user's projects.
func worldGate(w *verify.Window, it schema.Item, ev []store.Evidence, k *known) string {
	return itemGate(w, store.Fact, it, ev, k)
}

// sourced keeps the evidence an entry rests on: the user's and other people's lines. The assistant's, agents' and
// bots' lines it cited were context.
func sourced(ev []store.Evidence) []store.Evidence {
	var out []store.Evidence
	for _, e := range ev {
		if voice.Source(e.Role) {
			out = append(out, e)
		}
	}
	return out
}

func (c *Curator) reject(done *store.Done, class, code, claim, day string) {
	// The log keeps what was rejected, never a credential in it (SECRET rejections included).
	c.St.S.Rejections = append(c.St.S.Rejections, store.Rejection{Key: done.Key, Class: class, Code: code, Claim: verify.Redact(claim), Day: day})
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
		cur = &store.Item{ID: id, Class: class, Claim: it.Claim, About: it.About, Scope: it.Scope, Why: strings.TrimSpace(it.Why), Kind: it.Kind, Impact: strings.TrimSpace(it.Impact), When: it.When,
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
	if cur.Why == "" {
		cur.Why = strings.TrimSpace(it.Why)
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

// sources counts independent sources: distinct chats (documents, meetings…), so anything said in one chat counts
// once however many people repeat it there. Chats that carry the same words (the canonical quote text, norm.Q) are
// one source too: a mirrored repository, a message cross-posted to several groups, a mail forwarded to a chat.
func sources(ev []store.Evidence) int {
	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if parent[x] == x {
			return x
		}
		parent[x] = find(parent[x])
		return parent[x]
	}
	byQuote := map[string]string{} // canonical quote → the first chat it was seen in
	for _, e := range ev {
		if _, ok := parent[e.Chat]; !ok {
			parent[e.Chat] = e.Chat
		}
		q := norm.Q(e.Quote)
		if q == "" {
			continue
		}
		if first, ok := byQuote[q]; ok {
			parent[find(e.Chat)] = find(first)
		} else {
			byQuote[q] = e.Chat
		}
	}
	n := 0
	for ch := range parent {
		if find(ch) == ch {
			n++
		}
	}
	return n
}

// ownKept says whether a fact is one the owner's own words settle alone: a decision or a commitment in their own
// line, stated plainly. Another person's word, or a guess, still needs a second source.
func ownKept(it *store.Item) bool {
	if it.Class != store.Fact || it.Confidence != "high" || (it.Kind != "decision" && it.Kind != "commitment") {
		return false
	}
	return slices.ContainsFunc(it.Evidence, func(e store.Evidence) bool { return e.Role == voice.RoleUser && e.Speaker == norm.Me })
}

// promote moves an item up as its evidence grows. Practices: observed → recurring (seen on two days or from two
// independent sources) → established (seen in two different weeks, or from three sources). Facts and knowledge:
// candidate → active only with two independent sources and no flags, however confident the model was: one chat's
// word is a candidate, not something the assistant relies on.
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
		if it.Status == "candidate" && len(it.Flags) == 0 && (it.Sources >= 2 || ownKept(it)) {
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
		// A conversation or session is never an entity: its id stands for the project it belongs to, when it has
		// exactly one; otherwise the relation is not written.
		if kind, _, _ := strings.Cut(name, "/"); kind == "chats" || kind == "agents" || name == u.Ref || name == u.Meta.ID || graph.SessionLike(name) {
			if len(u.Meta.Projects) != 1 {
				return "", []string{name}
			}
			name = "projects/" + u.Meta.Projects[0]
			if k.projects[u.Meta.Projects[0]] == "" {
				return "", []string{name}
			}
		}
		if kind, id, ok := strings.Cut(name, "/"); ok && slices.Contains([]string{"people", "projects", "docs", "meetings", "events"}, kind) {
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
	proposed := map[string]bool{}
	learn := LearnPeople(c.St.S, unitSource(u))
	for _, a := range out.Assertions {
		pr, ok := b.Reg.Predicates[a.P]
		if !ok && len(a.Evidence) > 0 {
			// A type the user's table does not have yet (5.4): uninformative ones never join; another joins once it
			// came up twice (one unit-day counts once), and this relation is taken from then on.
			name := graph.TypeName(a.P)
			if graph.Uninformative(name) {
				c.reject(done, "assertion", relationEmpty, a.S+" "+a.P+" "+a.O, u.Day)
				run.Counts["rejected"]++
				continue
			}
			var now bool
			c.St.S.Types[entry.World], now = graph.Propose(c.St.S.Types[entry.World], name, proposed[name])
			proposed[name] = true
			if !now {
				c.reject(done, "assertion", typePending, a.S+" "+a.P+" "+a.O, u.Day)
				run.Counts["rejected"]++
				run.Counts["graph_type_proposed"]++
				continue
			}
			pr, ok = graph.Learned(name), true
			b.Reg.Predicates[name] = pr
			a.P = name
			run.Counts["graph_type_learned"]++
		}
		if !ok || len(a.Evidence) == 0 {
			c.reject(done, "assertion", verify.Schema, a.S+" "+a.P+" "+a.O, u.Day)
			run.Counts["rejected"]++
			continue
		}
		if a.S == a.O || (a.P == "collaborates_with" && strings.TrimSpace(a.Attrs["on"]) == "") {
			// "works with" without what they work on says nothing the chat's membership does not.
			c.reject(done, "assertion", relationEmpty, a.S+" "+a.P+" "+a.O, u.Day)
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
		if rej == "" && (b.TypeOf(sID) == "person" || b.TypeOf(oID) == "person") {
			rej = c.peopleRelationGate(u, a, learn, b.TypeOf(sID) == "person" && b.TypeOf(oID) == "person")
		}
		if rej != "" {
			c.reject(done, "assertion", rej, a.S+" "+a.P+" "+a.O, u.Day)
			run.Counts["rejected"]++
			continue
		}
		attrs := a.Attrs
		for i := range ev {
			ev[i].Quote = verify.Redact(ev[i].Quote)
		}
		id, outcome := b.Add(sID, a.P, oID, attrs, a.When, ev, a.Confidence, flags, schema.DailyPrompt)
		run.Counts["graph_"+outcome]++
		if done.Prompt == schema.DailyPrompt && outcome != "conflict" {
			// ⑤ relations are entries too (the graph keeps the knowledge base's side of them).
			if src := sourced(ev); len(src) > 0 {
				if eid := c.Observe(c.relationEntry(u, k, w, a.S, a.P, a.O, sNames[0], oNames[0], pr.Meaning, src), run.ID); eid != "" {
					run.Counts["obs:relation"]++
				}
			}
		}
		if outcome == "conflict" {
			c.proposeGraphConflict(b, id)
		}
	}
}

// Codes of relations not written.
const (
	typePending    = "TYPE_PENDING"        // a new relation type seen once: it joins the user's table the second time
	speakerUnres   = "SPEAKER_UNRESOLVED"  // a relation between people resting on a line whose speaker is not resolved
	peopleLearnOff = "PEOPLE_LEARNING_OFF" // the source's attribution failed its check (K16): no people relations
)

// peopleRelationGate: a relation involving a person is taken only when its source may teach people (K16), and, when
// it is between two people, only from lines whose speaker is resolved (3.2).
func (c *Curator) peopleRelationGate(u *Unit, a schema.Assertion, learn, between bool) string {
	if !learn {
		return peopleLearnOff
	}
	if u.Who == nil || !between {
		return ""
	}
	for _, e := range a.Evidence {
		why := u.WhoWhy[e.N]
		if u.Who[e.N] == "" && (why == speaker.WhyAmbiguous || why == speaker.WhyNone || why == speaker.WhyUnsupported || why == speaker.WhyQuoted) &&
			(u.Entity == "chats" || u.Entity == "recordings") {
			return speakerUnres
		}
	}
	return ""
}

// modelReason is why the model could not be called, as the hub said (model.ErrUnavailable's wrapped message).
func modelReason(err error) string {
	msg := strings.TrimPrefix(err.Error(), model.ErrUnavailable.Error())
	msg = strings.TrimSpace(strings.TrimPrefix(msg, ":"))
	if msg == "" {
		return "hub 没有给扩展用的模型"
	}
	return msg
}

// tokensOf is the estimated size of a unit's whole day, as the triage counts it (for a unit it did not admit).
func tokensOf(u *Unit) int {
	t := 0
	for _, l := range u.Lines {
		t += norm.Tokens(l.Speaker+": "+l.Body) + 4
	}
	return t
}

// tierOut rejects every item of a class a background unit gave: it gives none (TIER_NOT_ALLOWED), and returns nil.
func tierOut(c *Curator, done *store.Done, u *Unit, items []schema.Item, class string, run *store.Run) []schema.Item {
	for _, it := range items {
		c.reject(done, class, verify.TierNotAllowed, it.Claim, u.Day)
		run.Counts["rejected"]++
		run.Counts["rejected_tier"]++
	}
	return nil
}

// structuralPredicates are the relations a background unit may give: who is in what, who owns or reports to whom,
// what depends on or points at what. Decisions, commitments, assignments and blockers are not among them.
var structuralPredicates = []string{"owns", "member_of", "reports_to", "depends_on", "decides", "collaborates_with"}

// structural keeps the relations whose predicate is allowed and rejects the rest (TIER_NOT_ALLOWED).
func structural(c *Curator, done *store.Done, u *Unit, as []schema.Assertion, allowed []string, run *store.Run) []schema.Assertion {
	var out []schema.Assertion
	for _, a := range as {
		if slices.Contains(allowed, a.P) {
			out = append(out, a)
			continue
		}
		c.reject(done, "assertion", verify.TierNotAllowed, a.S+" "+a.P+" "+a.O, u.Day)
		run.Counts["rejected"]++
		run.Counts["rejected_tier"]++
	}
	return out
}

// ownLine says whether one of the cited lines is the owner's own words.
func ownLine(w *verify.Window, ev []store.Evidence) bool {
	for _, e := range ev {
		if voice.Own(w.All[e.N]) {
			return true
		}
	}
	return false
}

// Triage is what the rules say of every unit of a day (no model, nothing kept): for reports and offline reruns
// that compare the tiers. Units the rules leave to the model are given as triage.AI.
func (c *Curator) Triage(ctx context.Context, day string) ([]triage.Decision, error) {
	owner := c.ownerNames(ctx)
	units, err := c.units(ctx, day)
	if err != nil {
		return nil, err
	}
	cfg := c.Cfg.triage()
	contacts, topics := c.contactSet(), c.topics(c.knownEntities(ctx))
	out := make([]triage.Decision, len(units))
	for i := range units {
		u := &units[i]
		out[i] = triage.Decide(triage.Chat{Entity: u.Entity, Meta: u.Meta, Lines: u.Lines, OwnerRecently: u.OwnerRecently,
			OwnerLast: u.OwnerLast, OwnerNames: owner, Who: u.Who, Contacts: contacts, Topics: topics}, cfg)
		out[i].Chat = u.Ref
		if out[i].Action == triage.Drop {
			out[i].Tokens = tokensOf(u)
		}
	}
	return out, nil
}
