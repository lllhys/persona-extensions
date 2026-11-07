package curate

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/memory"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/prompts"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
)

// maxKnown is how many known entries the memory pass shows with one unit: recall first, then judge.
const maxKnown = 40

// remember is the daily memory call for one admitted unit (design chapter 6): the model extracts conclusions and
// facts and tags every sentence; the memory package scores them. Like the digest, it is keyed by content and done
// once per unchanged unit.
func (c *Curator) remember(ctx context.Context, m model.Model, u *Unit, d triage.Decision, owner []string, run *store.Run) error {
	in := schema.MemoryInput{Day: u.Day, Chat: u.Ref, ChatName: u.Meta.Name, Kind: u.Meta.Kind, Members: len(u.Meta.People),
		Owner: owner, ThingTypes: memory.ThingTypes, LifeAreas: memory.LifeAreas, SelfSubnodes: memory.SelfSubnodes, Language: c.language()}
	if in.Kind == "" {
		in.Kind = strings.TrimSuffix(u.Entity, "s")
	}
	var sig, all strings.Builder
	for _, l := range u.Lines {
		if d.Shown[l.N] {
			in.Lines = append(in.Lines, schema.MsgLine{N: l.N, Time: l.Time, Speaker: l.Speaker, Text: l.Body})
			fmt.Fprintf(&sig, "%d\x00%s\x00%s\n", l.N, l.Speaker, l.Body)
			all.WriteString(l.Body + "\n")
		}
	}
	if len(in.Lines) == 0 {
		return nil
	}
	in.Known = c.knownMemory(u.Ref, all.String())
	for _, e := range c.St.S.Memory.Awaiting() {
		k := schema.MemoryKnown{ID: e.ID, Text: e.Text, When: e.Status.LastSeen}
		for _, x := range e.Evidence {
			if x.Day == e.Status.LastSeen {
				k.Quote = x.Quote
			}
		}
		in.Waiting = append(in.Waiting, k)
	}
	key := hash("memory", u.Ref, u.Day, sig.String(), schema.MemoryPrompt)[:20]
	if c.St.S.Done[key] != nil {
		run.Counts["memory_unchanged"]++
		return nil
	}
	resp, err := m.Complete(ctx, model.Request{Step: "memory", Tier: tier(c.Cfg.DailyTier), System: prompts.Memory,
		User: schema.Must(in), MaxTokens: 4000, Schema: prompts.MemorySchema})
	if err != nil {
		return err
	}
	raw, err := c.St.PutRaw(key, []byte(resp.Text))
	if err != nil {
		return err
	}
	done := &store.Done{Key: key, Step: "memory", Chat: u.Ref, Day: u.Day, At: c.now().UTC().Format(time.RFC3339), Raw: raw,
		Prompt: schema.MemoryPrompt, Ruleset: schema.Ruleset}
	for n := range d.Shown {
		done.Shown = append(done.Shown, n)
	}
	sort.Ints(done.Shown)
	c.ingestMemory(u, d, owner, resp.Text, done, run)
	c.St.S.Done[key] = done
	run.Counts["memory_units"]++
	return nil
}

// knownMemory picks the entries a unit is most likely to be about: those with evidence from the same source, then
// the closest by wording.
func (c *Curator) knownMemory(ref, text string) []schema.MemoryKnown {
	type scored struct {
		e *memory.Entry
		s float64
	}
	var xs []scored
	for _, e := range c.St.S.Memory.Entries {
		if e.Status.State == memory.Obsolete || e.Status.State == memory.Dropped {
			continue
		}
		s := norm.Jaccard(e.Text, text)
		if slices.ContainsFunc(e.Evidence, func(x memory.Evidence) bool { return x.Source == ref }) {
			s += 1
		}
		if s > 0.02 {
			xs = append(xs, scored{e, s})
		}
	}
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].s != xs[j].s {
			return xs[i].s > xs[j].s
		}
		return xs[i].e.ID < xs[j].e.ID
	})
	var out []schema.MemoryKnown
	for i := 0; i < len(xs) && i < maxKnown; i++ {
		e := xs[i].e
		out = append(out, schema.MemoryKnown{ID: e.ID, Text: e.Text, Layer: string(e.Layer), Subject: string(e.Subject)})
	}
	return out
}

var (
	subjects   = []string{string(memory.SubjectSelf), string(memory.SubjectAI), string(memory.SubjectTeam), string(memory.SubjectOther)}
	layers     = []string{string(memory.Principle), string(memory.Guideline), string(memory.Experience), string(memory.Fact)}
	scenes     = []string{"review", "work", "assign", "correct", "chat"}
	exprs      = []string{"claim", "suggest", "question", "vent", "joke", "aside"}
	stances    = []string{"requirement", "position", "none"}
	supports   = []string{"direct", "partial", "none"}
	categories = []string{"", "attribute", "decision", "event", "progress", "todo", "knowledge", "opinion", "intent"}
)

// ingestMemory checks a memory answer and hands what passes to the book. Every citation goes through the same
// checks as the digest's; a conclusion with a bad field or a citation that fails is thrown away whole.
func (c *Curator) ingestMemory(u *Unit, d triage.Decision, owner []string, text string, done *store.Done, run *store.Run) {
	var out schema.MemoryOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		c.reject(done, "memory", verify.Schema, "", u.Day)
		run.Counts["memory_rejected"]++
		return
	}
	w := c.window(u, d.Shown, owner)
	book := c.St.S.Memory
	for _, mc := range out.Conclusions {
		e, rej := c.memoryEntry(w, u, mc)
		if rej != "" {
			c.reject(done, "memory", rej, mc.Text, u.Day)
			run.Counts["memory_rejected"]++
			continue
		}
		e = book.Put(e)
		if mc.Contradicts != "" && mc.Contradicts != e.ID && book.Entries[mc.Contradicts] != nil {
			book.Contradict(mc.Contradicts, e.ID)
			run.Counts["memory_contradictions"]++
		}
		run.Counts["memory_evidence"] += len(mc.Evidence)
	}
	vote := func(v schema.MemoryVote) (string, bool) {
		ev, rej, _ := w.Evidence([]schema.Evidence{{N: v.N, Quote: v.Quote}})
		if rej != "" || book.Entries[v.Entry] == nil || ev[0].Speaker != norm.Me {
			return "", false // only the user's own words confirm or reject
		}
		return fmt.Sprintf("%s#%d", u.Ref, v.N), true
	}
	for _, v := range out.Confirms {
		if a, ok := vote(v); ok && book.Confirm(v.Entry, u.Day, a) {
			run.Counts["memory_confirmed"]++
		}
	}
	for _, v := range out.Rejects {
		if a, ok := vote(v); ok && book.Reject(v.Entry, u.Day, a) {
			run.Counts["memory_rejected_by_user"]++
		}
	}
	for _, f := range out.FollowUps {
		ev, rej, _ := w.Evidence([]schema.Evidence{{N: f.N, Quote: f.Quote}})
		kind := memory.FollowKind(f.Kind)
		if rej != "" || book.Entries[f.Entry] == nil || !slices.Contains([]memory.FollowKind{memory.FollowStated, memory.FollowArtifact, memory.FollowVerbal}, kind) {
			continue
		}
		if book.FollowUp(f.Entry, u.Day, kind, fmt.Sprintf("%s#%d", u.Ref, f.N), ev[0].Quote) {
			run.Counts["memory_follow_ups"]++
		} else {
			run.Counts["memory_follow_ups_verbal"]++
		}
	}
}

// memoryEntry builds an entry from one conclusion of the answer, or says why it is rejected.
func (c *Curator) memoryEntry(w *verify.Window, u *Unit, mc schema.MemoryConclusion) (*memory.Entry, string) {
	if strings.TrimSpace(mc.Text) == "" || !slices.Contains(subjects, mc.Subject) || !slices.Contains(layers, mc.Layer) ||
		!slices.Contains(categories, mc.FactCategory) || len(mc.Evidence) == 0 {
		return nil, verify.Schema
	}
	e := &memory.Entry{Text: mc.Text, Sentences: mc.Sentences, Layer: memory.Layer(mc.Layer), Subject: memory.Subject(mc.Subject),
		Descriptive: mc.Descriptive, FactCategory: memory.FactCategory(mc.FactCategory), Owner: mc.Owner, Global: mc.Global,
		Hard: mc.Hard, Prohibition: mc.Prohibition, OneOff: mc.OneOff}
	if slices.Contains(memory.ThingTypes, mc.ThingType) {
		e.ThingType = mc.ThingType
	}
	for _, a := range mc.LifeAreas {
		if slices.Contains(memory.LifeAreas, a) && len(e.LifeAreas) < 2 {
			e.LifeAreas = append(e.LifeAreas, a)
		}
	}
	if slices.Contains(memory.SelfSubnodes, mc.SelfSubnode) {
		e.SelfSubnode = mc.SelfSubnode
	}
	if e.Descriptive {
		e.Layer, e.FactCategory = memory.Fact, memory.Attribute
	}
	if e.Layer == memory.Fact && e.FactCategory == "" {
		e.FactCategory = memory.Attribute
	}
	e.Use = useOf(e)
	n := len(e.Sentences)
	if n == 0 {
		n = 1
	}
	for _, p := range mc.Private {
		if p.I >= 0 && p.I < n {
			e.PrivateSentences = append(e.PrivateSentences, memory.PrivateSentence{I: p.I, Category: p.Category})
		}
	}
	for _, x := range mc.Evidence {
		if !slices.Contains(scenes, x.Scene) || !slices.Contains(exprs, x.Expression) || !slices.Contains(stances, x.Stance) || !slices.Contains(supports, x.Support) {
			return nil, verify.Schema
		}
		ev, rej, flags := w.Evidence([]schema.Evidence{{N: x.N, Quote: x.Quote, Speaker: x.Speaker}})
		if rej != "" {
			return nil, rej
		}
		sp := memory.SpeakerOther
		switch {
		case ev[0].Speaker == norm.Me:
			sp = memory.SpeakerUser
		case slices.Contains(flags, verify.FromBot):
			sp = memory.SpeakerAI
		}
		e.Evidence = append(e.Evidence, memory.Evidence{Day: u.Day, Source: u.Ref, Anchor: fmt.Sprintf("%s#%d", u.Ref, x.N),
			Quote: ev[0].Quote, Speaker: sp, Tags: memory.Tags{Scene: memory.Scene(x.Scene), Expression: memory.Expression(x.Expression),
				HoldsStance: x.HoldsStance, Stance: memory.Stance(x.Stance), ProposalQuestion: x.ProposalQuestion,
				DescriptiveValue: x.DescriptiveValue, Attitude: x.Attitude, Support: memory.Support(x.Support), LongTerm: x.LongTerm,
				ThingType: e.ThingType}})
	}
	if mc.Match != "" && c.St.S.Memory.Entries[mc.Match] != nil {
		e.ID = mc.Match // a known entry seen again: its evidence grows, its classification stays
		return e, ""
	}
	e.ID = "m-" + hash(string(e.Subject), norm.Q(e.Text))[:12]
	return e, ""
}

// useOf is the use that goes with the subject and layer (design 6.16): what the user asks of the AI is to be
// followed; how the user is and the user's own principles and guidelines help the AI understand them; the rest is
// for reference.
func useOf(e *memory.Entry) memory.Use {
	switch {
	case e.Subject == memory.SubjectAI:
		return memory.UseFollow
	case e.Descriptive:
		return memory.UseUnderstand
	case e.Layer == memory.Principle || e.Layer == memory.Guideline:
		if e.Subject == memory.SubjectSelf {
			return memory.UseUnderstand
		}
	}
	return memory.UseReference
}

// settleMemory ticks the book for the day: contradictions compared, every entry's state as of today.
func (c *Curator) settleMemory(day string, run *store.Run) {
	for _, ch := range c.St.S.Memory.Tick(day, c.Cfg.Memory) {
		run.Counts["memory_now_"+string(ch.To.State)]++
		if ch.From.Layer != "" && ch.From.Layer != ch.To.Layer {
			run.Counts["memory_layer_changed"]++
		}
	}
}

// forgetMemory drops the evidence that came from forgotten sources, and the entries left with none.
func forgetMemory(b *memory.Book, from func(ref string) bool) (removed int) {
	for id, e := range b.Entries {
		n := len(e.Evidence)
		e.Evidence = slices.DeleteFunc(e.Evidence, func(x memory.Evidence) bool { return from(x.Source) })
		e.Events = slices.DeleteFunc(e.Events, func(x memory.EntryEvent) bool {
			ref, _, _ := strings.Cut(x.Anchor, "#")
			return x.Anchor != "" && from(ref)
		})
		removed += n - len(e.Evidence)
		if len(e.Evidence) == 0 && e.Imported == "" {
			delete(b.Entries, id)
		}
	}
	b.Conflicts = slices.DeleteFunc(b.Conflicts, func(x memory.Conflict) bool { return b.Entries[x.A] == nil || b.Entries[x.B] == nil })
	return removed
}

// Vote is the user confirming ("确认": speeds it up, active today) or rejecting ("否定": one vote voids it) a memory
// entry from the page.
func (c *Curator) Vote(id string, confirm bool) error {
	c.St.Lock()
	defer c.St.Unlock()
	day := c.today()
	ok := false
	if confirm {
		ok = c.St.S.Memory.Confirm(id, day, "ui")
	} else {
		ok = c.St.S.Memory.Reject(id, day, "ui")
	}
	if !ok {
		return fmt.Errorf("没有 %s", id)
	}
	c.St.S.Memory.Tick(day, c.Cfg.Memory)
	return c.St.Save()
}

// MemoryView is the memory as the page lists it: every entry with its injection level, best first.
func (c *Curator) MemoryView() []MemoryRow {
	book := c.St.S.Memory
	in := memory.Inject(book, c.Cfg.Memory)
	order := map[memory.Level]int{memory.Resident: 0, memory.Recall: 1, memory.Search: 2, memory.None: 3}
	sort.SliceStable(in, func(i, j int) bool {
		if order[in[i].Level] != order[in[j].Level] {
			return order[in[i].Level] < order[in[j].Level]
		}
		return in[i].Score > in[j].Score
	})
	var out []MemoryRow
	for _, x := range in {
		e := book.Entries[x.ID]
		layer := layerLabel[e.Status.Layer]
		if e.Status.Descriptive {
			layer = "描述属性"
		}
		text := x.Text
		if text == "" {
			text = "（隐私，不显示）"
		}
		out = append(out, MemoryRow{ID: x.ID, Text: text, Sub: fmt.Sprintf("%s · %s · %.0f 分 · %d 次", layer, stateLabel[e.Status.State], e.Status.Score, e.Status.Count),
			Level: levelLabel[x.Level], Entry: e})
	}
	return out
}

// MemoryRow is one line of the memory list.
type MemoryRow struct {
	ID, Text, Sub, Level string
	Entry                *memory.Entry
}
