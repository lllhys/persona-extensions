package signals

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/prompts"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
)

// The correction ledger (design 5.1): the user's messages in S1, S2 and S3, screened by word lists, judged by the
// model (a correction of the AI? about which known entry, in any facet, or which new issue?). A correction later
// than its entry's Effective time by more than RecurAfter is a recurrence. New issues of one kind seen ≥2 times in
// ≥2 sessions become a ① entry; with K8 on it is stable at once and goes into exit A at the next compile. With no
// hand-written memory and no entries the ledger works the same: every correction is a new issue.

// RecurAfter: a correction within this time after the entry took effect is the same turn, not a recurrence.
const RecurAfter = 10 * time.Minute

const (
	judgeBatch   = 8
	catalogueMax = 220
	memoryPrefix = "memory:"
)

type ledger struct {
	o     *Options
	st    *store.State
	sd    *side
	day   string
	found []entry.Correction // the day's corrections, kept before or now: the screen's correction signals
	fresh map[string]bool    // corrections kept by this run
	mems  map[string]time.Time
}

func corrID(m msg) string {
	return "c-" + hash(m.Session, fmt.Sprint(m.Seq), m.At.UTC().Format(time.RFC3339), m.Text)[:10]
}

func (l *ledger) run(ctx context.Context, g *gathered, res *Result) error {
	msgs := g.userMsgs()
	res.UserMsgs = len(msgs)
	now := l.o.now().UTC().Format(time.RFC3339)
	l.mems = map[string]time.Time{}
	for _, m := range g.Memories {
		if _, ok := l.sd.MemorySeen[m.Name]; !ok {
			l.sd.MemorySeen[m.Name] = now
		}
		l.mems[m.Name] = m.Created
	}
	kept := map[string]int{}
	for i, c := range l.st.Corrections {
		kept[c.ID] = i
	}
	var cand []msg
	for _, m := range msgs {
		if i, ok := kept[corrID(m)]; ok {
			l.found = append(l.found, l.st.Corrections[i])
			continue
		}
		if match(entry.SigCorrection, m.Text, l.sd.Words) != "" {
			cand = append(cand, m)
		}
	}
	res.Screened = len(cand)
	if l.o.Model == nil || len(cand) == 0 {
		return nil
	}
	cat := l.catalogue(g)
	var batches [][]msg
	for i := 0; i < len(cand); i += judgeBatch {
		batches = append(batches, cand[i:min(i+judgeBatch, len(cand))])
	}
	// With Workers > 1 the batches are judged at once (each sees the open issues as the day began); kept in order.
	answers := make([][]verdict, len(batches))
	errs := make([]error, len(batches))
	if l.o.Workers > 1 {
		parallel(ctx, l.o.Workers, len(batches), func(i int) { answers[i], errs[i] = l.judge(ctx, cat, batches[i]) })
	}
	for bi, batch := range batches {
		if err := ctx.Err(); err != nil {
			return err
		}
		verdicts, err := answers[bi], errs[bi]
		if l.o.Workers <= 1 {
			verdicts, err = l.judge(ctx, cat, batch)
		}
		if err != nil {
			l.o.logf("signals: correction judge: %v", err)
			continue
		}
		for _, v := range verdicts {
			if v.I < 0 || v.I >= len(batch) || !v.Correction {
				continue
			}
			c := l.keep(batch[v.I], v, cat)
			res.Corrections++
			if c.Recurrence {
				res.Recurrences++
			}
		}
	}
	if res.Corrections > 0 {
		if err := l.groupIssues(ctx); err != nil {
			l.o.logf("signals: issue grouping: %v", err)
		}
	}
	res.NewEntries = append(res.NewEntries, l.fileIssues()...)
	return nil
}

const groupMax = 200 // issues shown to the grouping call: the filed ones and the newest open ones

// groupIssues asks the model once which issues ask for the same change, and merges them: the judge names issues one
// batch at a time and gives the same kind of problem different keys. A group with a filed issue keeps that one (and
// its entry's text); otherwise the group's text replaces the members'.
func (l *ledger) groupIssues(ctx context.Context) error {
	var keys []string
	for k, is := range l.sd.Issues {
		if is.Text != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) < 2 {
		return nil
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := l.sd.Issues[keys[i]], l.sd.Issues[keys[j]]
		if (a.Entry != "") != (b.Entry != "") {
			return a.Entry != ""
		}
		if a.First != b.First {
			return a.First > b.First
		}
		return keys[i] < keys[j]
	})
	if len(keys) > groupMax {
		keys = keys[:groupMax]
	}
	type inIssue struct {
		Key   string `json:"key"`
		Text  string `json:"text"`
		Count int    `json:"count"`
	}
	in := struct {
		Language string    `json:"language"`
		UserWord string    `json:"user_word"`
		Issues   []inIssue `json:"issues"`
	}{Language: l.o.Lang, UserWord: norm.UserWord(l.o.Lang)}
	for _, k := range keys {
		in.Issues = append(in.Issues, inIssue{Key: k, Text: l.sd.Issues[k].Text, Count: len(l.sd.Issues[k].Corrections)})
	}
	user, _ := json.Marshal(in)
	resp, err := l.o.Model.Complete(ctx, model.Request{Step: "signals_issues", Tier: l.o.Tier, System: prompts.Issues,
		User: string(user), MaxTokens: 4000, Schema: prompts.IssuesSchema})
	if err != nil {
		return err
	}
	var out struct {
		Groups []struct {
			Key     string   `json:"key"`
			Members []string `json:"members"`
			Text    string   `json:"text"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &out); err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	for _, g := range out.Groups {
		l.merge(g.Key, g.Members, strings.TrimSpace(g.Text))
	}
	return nil
}

// merge folds a group of issues into one.
func (l *ledger) merge(key string, members []string, text string) {
	var group []string
	for _, m := range append([]string{key}, members...) {
		if l.sd.Issues[m] != nil && !slices.Contains(group, m) {
			group = append(group, m)
		}
	}
	if len(group) < 2 {
		return
	}
	keep := group[0]
	filed := 0
	for _, m := range group {
		if l.sd.Issues[m].Entry != "" {
			filed++
			if filed == 1 {
				keep = m
			}
		}
	}
	if filed > 1 {
		return // two filed issues are two entries: merging them is the curator's (P3), not the ledger's
	}
	into := l.sd.Issues[keep]
	if into.Entry == "" && text != "" {
		into.Text = text
	}
	moved := map[string]bool{}
	for _, m := range group {
		if m == keep {
			continue
		}
		is := l.sd.Issues[m]
		for _, c := range is.Corrections {
			if !slices.Contains(into.Corrections, c) {
				into.Corrections = append(into.Corrections, c)
			}
			moved[c] = true
		}
		for _, s := range is.Sessions {
			if !slices.Contains(into.Sessions, s) {
				into.Sessions = append(into.Sessions, s)
			}
		}
		if is.First < into.First {
			into.First = is.First
		}
		delete(l.sd.Issues, m)
	}
	for i := range l.st.Corrections {
		c := &l.st.Corrections[i]
		if moved[c.ID] {
			c.Issue = keep
			if into.Entry != "" && c.Entry == "" {
				c.Entry = into.Entry
			}
		}
	}
}

type catItem struct {
	ID    string `json:"id"`
	Facet string `json:"facet"`
	Text  string `json:"text"`
}

// catalogue is what a correction may be about: the user's ① entries first, then the other facets' entries by
// weight, then the hand-written memory (when the hub gives it). It may be empty.
func (l *ledger) catalogue(g *gathered) []catItem {
	var out []catItem
	add := func(e *entry.Entry) {
		if len(out) < catalogueMax {
			out = append(out, catItem{ID: e.ID, Facet: string(e.Facet), Text: cut(e.Shown(), 100)})
		}
	}
	var rest []*entry.Entry
	for _, e := range l.st.Entries {
		if !e.Live() || e.Sensitive != "" {
			continue
		}
		rest = append(rest, e)
	}
	sort.Slice(rest, func(i, j int) bool {
		wi, wj := weight(rest[i]), weight(rest[j])
		if (rest[i].Facet == entry.Interaction) != (rest[j].Facet == entry.Interaction) {
			return rest[i].Facet == entry.Interaction
		}
		if wi != wj {
			return wi > wj
		}
		return rest[i].ID < rest[j].ID
	})
	for _, e := range rest {
		add(e)
	}
	for _, m := range g.Memories {
		text := m.Description
		if text == "" {
			text = m.Name
		}
		out = append(out, catItem{ID: memoryPrefix + m.Name, Facet: "memory", Text: cut(text, 120)})
	}
	return out
}

func weight(e *entry.Entry) int { return e.Count * max(1, len(e.Kinds)) }

type verdict struct {
	I          int    `json:"i"`
	Correction bool   `json:"correction"`
	Target     string `json:"target"`
	IssueKey   string `json:"issue_key"`
	IssueText  string `json:"issue_text"`
	Sensitive  string `json:"sensitive"`
}

func (l *ledger) judge(ctx context.Context, cat []catItem, batch []msg) ([]verdict, error) {
	type inMsg struct {
		I      int    `json:"i"`
		Source string `json:"source"`
		Prior  string `json:"prior"`
		Text   string `json:"text"`
	}
	type issue struct {
		Key  string `json:"key"`
		Text string `json:"text"`
	}
	in := struct {
		Language   string    `json:"language"`
		UserWord   string    `json:"user_word"`
		Catalogue  []catItem `json:"catalogue"`
		OpenIssues []issue   `json:"open_issues"`
		Messages   []inMsg   `json:"messages"`
	}{Language: l.o.Lang, UserWord: norm.UserWord(l.o.Lang), Catalogue: cat}
	keys := make([]string, 0, len(l.sd.Issues))
	for k := range l.sd.Issues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		in.OpenIssues = append(in.OpenIssues, issue{Key: k, Text: l.sd.Issues[k].Text})
	}
	for i, m := range batch {
		src := "chat"
		if m.Card {
			src = "card"
		}
		in.Messages = append(in.Messages, inMsg{I: i, Source: src, Prior: verify.Redact(m.Prior), Text: verify.Redact(cut(m.Text, 600))})
	}
	user, _ := json.Marshal(in)
	resp, err := l.o.Model.Complete(ctx, model.Request{Step: "signals_correct", Tier: l.o.Tier, System: prompts.Correct,
		User: string(user), MaxTokens: 3000, Schema: prompts.CorrectSchema})
	if err != nil {
		return nil, err
	}
	var out struct {
		Verdicts []verdict `json:"verdicts"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &out); err != nil {
		return nil, fmt.Errorf("answer: %w", err)
	}
	return out.Verdicts, nil
}

var keyClean = regexp.MustCompile(`[^a-z0-9]+`)

func issueKey(s string) string {
	return strings.Trim(keyClean.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// keep records one correction: against an entry or a memory file (with its recurrence), or under an issue.
func (l *ledger) keep(m msg, v verdict, cat []catItem) entry.Correction {
	c := entry.Correction{ID: corrID(m), At: m.At.UTC().Format(time.RFC3339), Day: l.day, Source: m.Source, Ref: m.Ref,
		Seq: m.Seq, Quote: verify.Redact(cut(m.Text, 500)), Prior: verify.Redact(cut(m.Prior, 200))}
	sens := v.Sensitive
	if sens != "" && !slices.Contains(entry.Sensitives, sens) {
		sens = ""
	}
	if sens != "" && !entry.On(l.st.Switches, entry.K13Sensitive+"."+sens) {
		c.Quote, c.Prior = "", "" // counted, not kept: the category is off (K13)
	}
	target := v.Target
	if !slices.ContainsFunc(cat, func(x catItem) bool { return x.ID == target }) {
		target = ""
	}
	if target == "" {
		key := issueKey(v.IssueKey)
		if key == "" {
			key = "unnamed-" + c.ID[2:8] // its own issue until the grouping puts it with others
		}
		is := l.sd.Issues[key]
		if is == nil {
			is = &Issue{Key: key, Text: strings.TrimSpace(v.IssueText), First: l.day, Sensitive: sens}
			l.sd.Issues[key] = is
		}
		if is.Text == "" {
			is.Text = strings.TrimSpace(v.IssueText)
		}
		is.Corrections = append(is.Corrections, c.ID)
		// Occasions are a session on a day: the assistant's own conversation is one long session over many days, and
		// the same complaint on another day is not the same exchange.
		if occ := m.Session + "@" + m.At.In(l.o.loc()).Format("2006-01-02"); !slices.Contains(is.Sessions, occ) {
			is.Sessions = append(is.Sessions, occ)
		}
		c.Issue = key
		target = is.Entry // an issue already filed counts against its entry
	}
	c.Entry = target
	if eff, ok := l.effective(target); ok && m.At.Sub(eff) > RecurAfter {
		c.Recurrence = true
	}
	l.st.Corrections = append(l.st.Corrections, c)
	l.found = append(l.found, c)
	if l.fresh == nil {
		l.fresh = map[string]bool{}
	}
	l.fresh[c.ID] = true
	return c
}

// effective is when an entry or a memory file took effect: the entry's Effective (set by the exits when it first went
// out); a memory file's creation time from the hub (H2), else when the plugin first saw it.
func (l *ledger) effective(target string) (time.Time, bool) {
	if target == "" {
		return time.Time{}, false
	}
	if name, ok := strings.CutPrefix(target, memoryPrefix); ok {
		if t := l.mems[name]; !t.IsZero() {
			return t, true
		}
		t, err := time.Parse(time.RFC3339, l.sd.MemorySeen[name])
		return t, err == nil
	}
	e := l.st.Entries[target]
	if e == nil || e.Effective == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, e.Effective)
	return t, err == nil
}

// fileIssues makes a ① entry of every issue seen ≥2 times in ≥2 sessions, and counts later corrections of a filed
// issue on its entry. Returns the entries made.
func (l *ledger) fileIssues() []string {
	byID := map[string]*entry.Correction{}
	for i := range l.st.Corrections {
		byID[l.st.Corrections[i].ID] = &l.st.Corrections[i]
	}
	var made []string
	keys := make([]string, 0, len(l.sd.Issues))
	for k := range l.sd.Issues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		is := l.sd.Issues[k]
		if is.Text == "" || len(is.Corrections) < 2 || len(uniq(append([]string(nil), is.Sessions...))) < 2 {
			continue
		}
		if is.Sensitive != "" && !entry.On(l.st.Switches, entry.K13Sensitive+"."+is.Sensitive) {
			continue
		}
		if l.o.Observe == nil {
			continue
		}
		e := &entry.Entry{Facet: entry.Interaction, Grain: entry.Action, Type: "correction", Text: is.Text,
			Impact: entry.Impact{Moment: "every_turn", Action: is.Text}, Sensitive: is.Sensitive, Flags: []string{"ledger:" + k}}
		var sessions []string
		for _, id := range is.Corrections {
			c := byID[id]
			if c == nil || (is.Entry != "" && !l.fresh[id]) {
				continue // filed before: only this run's corrections are a new sighting
			}
			ev := entry.Evidence{Source: c.Source, Ref: c.Ref, Day: c.Day, Seq: c.Seq, Who: entry.WhoSelf, Role: "user", Quote: c.Quote}
			if ev.Quote == "" {
				continue // a quote not kept (K13) is no evidence
			}
			e.Evidence = append(e.Evidence, ev)
			sessions = append(sessions, c.Ref+"#"+c.Day)
		}
		if len(e.Evidence) == 0 {
			continue
		}
		if is.Entry == "" {
			e.Count, e.Sessions = len(e.Evidence), len(uniq(sessions))
			if entry.On(l.st.Switches, entry.K8AutoEffective) {
				e.Status = entry.Stable // K8: takes effect at once; the weekly report says so and the user can undo
			}
		}
		id := l.o.Observe(e, l.o.Run)
		if id == "" {
			continue
		}
		if is.Entry == "" {
			made = append(made, id)
		}
		is.Entry = id
		for _, cid := range is.Corrections {
			if c := byID[cid]; c != nil && c.Entry == "" {
				c.Entry = id
			}
		}
	}
	return made
}

// SampleWeek picks up to 30 of a week's corrections at random for the check page (5.1 step 4), under
// State.Samples["corrections/<week>"]. The pick is the same for the same week and ledger.
func SampleWeek(st *store.State, week string, days []string) []string {
	var ids []string
	for _, c := range st.Corrections {
		if slices.Contains(days, c.Day) {
			ids = append(ids, c.ID)
		}
	}
	sort.Strings(ids)
	seed := int64(0)
	for _, b := range []byte(hash("corrections", week)[:12]) {
		seed = seed*31 + int64(b)
	}
	r := rand.New(rand.NewSource(seed))
	r.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	if len(ids) > 30 {
		ids = ids[:30]
	}
	sort.Strings(ids)
	if st.Samples == nil {
		st.Samples = map[string][]string{}
	}
	st.Samples["corrections/"+week] = ids
	return ids
}
