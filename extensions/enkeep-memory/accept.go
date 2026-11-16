package main

// The accept subcommand works out, from one state directory, what the acceptance checks of design §9 read:
//
//   - the readings R1…R8, R4.<fork type>, F1 and U1…U8 (internal/readings), stored in State.Readings with -save;
//   - the empty-talk check (9.4): 30 entries of exits A and B (≥4 per facet that has them), each put the fixed
//     question "delete this line: at which moment would the assistant act differently?", and 10 abstract entries
//     checked against the abstract gate (5.3);
//   - the F1 fact sample (9.5): 30 facts (⑤, and the fact part of the ⑤′ cards) judged against the lines they cite;
//   - the speaker check (U8): up to 30 attributed lines per source, "is this line by X?".
//
// The model only pre-judges (right / wrong / unsupported, yes / no / unsure); the user reviews on the check page,
// and the pre-judgements go to prejudge.json beside the state, never into an entry's verdicts. Every sample points
// back to an entry id or a unit, day and line. The model is a command-line model on a gateway (pi); Anthropic
// models are refused here, as in the regression smoke.
//
//	enkeep-memory accept -state DIR -out DIR [-day YYYY-MM-DD] [-kb DIR] [-judge pi|none] [-model M] [-save]

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	exits "github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/export"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/readings"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// cardsFile: the replay's cards kept beside the state (offline writes it) for R4.
const cardsFile = "cards.json"

// prejudgeFile: the model's pre-judgements beside the state, for the check page to show next to each line.
const prejudgeFile = "prejudge.json"

func acceptCmd(args []string) error {
	fs := flag.NewFlagSet("accept", flag.ContinueOnError)
	stateDir := fs.String("state", "", "state directory")
	out := fs.String("out", "", "directory for accept.json and accept.md")
	day := fs.String("day", "", "last day of the reading period (default: the last active day)")
	tz := fs.String("tz", "UTC", "the time zone days are cut in")
	kb := fs.String("kb", "", "a copy of the knowledge base the state was made from (speaker lines and members, fact context)")
	cards := fs.String("cards", "", "the cards of the period (default: <state>/"+cardsFile+")")
	agentsDir := fs.String("agents-dir", "", "read the period's cards from these transcripts when there is no cards file")
	assistantDirs := fs.String("assistant-dirs", "", "comma-separated names under -agents-dir holding the assistant's sessions")
	judgeWith := fs.String("judge", "pi", "who pre-judges the samples: pi | none")
	mdl := fs.String("model", "", "the model pi asks (required with -judge pi)")
	provider := fs.String("provider", "", "the provider pi asks (empty: pi's own default)")
	cache := fs.String("cache", "", "answer cache (default <out>/cache)")
	seed := fs.Int64("seed", 20261112, "seed of the samples")
	save := fs.Bool("save", false, "store the readings in the state (State.Readings) and the pre-judgements beside it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *stateDir == "" || *out == "" {
		return fmt.Errorf("-state and -out are required")
	}
	if *judgeWith == "pi" && *mdl == "" {
		return fmt.Errorf("-model is required with -judge pi")
	}
	if err := refuseAnthropic(*mdl); err != nil {
		return err
	}
	loc, err := time.LoadLocation(*tz)
	if err != nil {
		return err
	}
	st, err := store.Open(*stateDir)
	if err != nil {
		return err
	}
	var src kbsrc.Source
	if *kb != "" {
		if src, err = kbsrc.LoadFixture(*kb); err != nil {
			return err
		}
	}
	var m model.Model
	if *judgeWith == "pi" {
		if *cache == "" {
			*cache = filepath.Join(*out, "cache")
		}
		m = model.Pi{Provider: *provider, Models: map[model.Tier]string{model.Small: *mdl}, CacheDir: *cache}
	}
	in := acceptInput{St: st.S, Dir: *stateDir, Day: *day, Loc: loc, Src: src, Model: m, Rand: rand.New(rand.NewSource(*seed))}
	if in.Day == "" {
		in.Day = lastDay(st.S)
	}
	path := *cards
	if path == "" {
		path = filepath.Join(*stateDir, cardsFile)
	}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &in.Cards); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	} else if *agentsDir != "" {
		// A state made before replays kept their cards: read the period's cards from the transcripts.
		days := readings.Days(in.Day)
		since, _ := time.ParseInLocation("2006-01-02", days[0], loc)
		until, _ := time.ParseInLocation("2006-01-02", in.Day, loc)
		off := &hubapi.Offline{AgentsDir: *agentsDir, Since: since}
		if *assistantDirs != "" {
			off.AssistantDirs = strings.Split(*assistantDirs, ",")
		}
		in.Cards, err = off.Interactions(context.Background(), hubapi.InteractionsQuery{Since: since, Before: until.AddDate(0, 0, 1)})
		if err != nil && len(in.Cards) == 0 {
			in.CardsMissing = err.Error()
		}
	} else {
		in.CardsMissing = "no " + filepath.Base(path) + " beside the state"
	}
	if *agentsDir != "" {
		off := &hubapi.Offline{AgentsDir: *agentsDir}
		if ss, err := off.Sessions(context.Background(), time.Time{}); err == nil {
			in.Sessions = map[string]string{}
			for _, se := range ss {
				in.Sessions["agents/"+se.Machine+"/"+se.ID] = strings.TrimSpace(strings.Join([]string{se.Title, se.Project}, " "))
			}
		}
	}
	rep, err := runAccept(context.Background(), in)
	if err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(*out, "accept.json"), rep); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*out, "accept.md"), []byte(rep.markdown()), 0o600); err != nil {
		return err
	}
	if *save {
		if err := writeJSON(filepath.Join(*stateDir, prejudgeFile), rep.prejudge()); err != nil {
			return err
		}
		if err := st.Save(); err != nil {
			return err
		}
	}
	fmt.Print(rep.summary())
	return nil
}

func refuseAnthropic(name string) error {
	n := strings.ToLower(name)
	if strings.Contains(n, "claude") || strings.Contains(n, "anthropic") || strings.Contains(n, "opus") || strings.Contains(n, "sonnet") || strings.Contains(n, "haiku") || strings.Contains(n, "fable") {
		return fmt.Errorf("model %q: acceptance checks and the smoke do not use Anthropic models", name)
	}
	return nil
}

// lastDay is the last day with source data, else the last daily run's day, else today.
func lastDay(s *store.State) string {
	if n := len(s.ActiveDays); n > 0 {
		return s.ActiveDays[n-1]
	}
	d := ""
	for _, r := range s.Runs {
		if r.Kind == "daily" && r.Period > d {
			d = r.Period
		}
	}
	if d == "" {
		d = time.Now().Format("2006-01-02")
	}
	return d
}

type acceptInput struct {
	St           *store.State
	Dir          string
	Day          string
	Loc          *time.Location
	Cards        []hubapi.Interaction
	CardsMissing string
	Src          kbsrc.Source // optional: speaker lines, members, fact context
	// Sessions are the agent sessions' titles by unit ref ("agents/<machine>/<id>"), for the fact judge (optional).
	Sessions map[string]string
	Model    model.Model // nil: samples drawn, not pre-judged
	Rand     *rand.Rand
}

type acceptReport struct {
	State    string          `json:"state"`
	Day      string          `json:"day"`
	Readings []entry.Reading `json:"readings"`
	Exits    string          `json:"exits_source"` // where the exit A/B entries came from
	Empty    []emptyItem     `json:"empty_talk"`
	Abstract []abstractItem  `json:"abstract"`
	Facts    []factItem      `json:"f1"`
	Speakers []speakerItem   `json:"speakers"`
	Judge    string          `json:"judge"`
	Errors   []string        `json:"errors,omitempty"`
}

type emptyItem struct {
	ID         string      `json:"id"`
	Facet      entry.Facet `json:"facet"`
	Grain      entry.Grain `json:"grain"`
	Exit       string      `json:"exit"`
	Text       string      `json:"text"`
	Answerable *bool       `json:"answerable,omitempty"`
	Moment     string      `json:"moment,omitempty"`
	Difference string      `json:"difference,omitempty"`
}

type abstractItem struct {
	ID         string      `json:"id"`
	Facet      entry.Facet `json:"facet"`
	Text       string      `json:"text"`
	Default    string      `json:"default,omitempty"`
	Situations []string    `json:"situations,omitempty"`
	Members    int         `json:"members"`
	Days       int         `json:"active_days"`
	Structural bool        `json:"structural_pass"` // ≥3 members, ≥2 situations, ≥2 active days, a default given
	Generic    *bool       `json:"generic,omitempty"`
	Reason     string      `json:"reason,omitempty"`
	Pass       *bool       `json:"pass,omitempty"`
}

type factItem struct {
	ID       string           `json:"id"`
	Facet    entry.Facet      `json:"facet"`
	Person   string           `json:"person,omitempty"`
	Text     string           `json:"text"`
	Evidence []entry.Evidence `json:"evidence"`
	Context  []string         `json:"context,omitempty"`
	Sources  []string         `json:"sources,omitempty"` // the names of the chats and sessions it cites (a group's name is evidence too)
	Verdict  string           `json:"verdict,omitempty"` // right | wrong | unsupported
	Reason   string           `json:"reason,omitempty"`
}

type speakerItem struct {
	Key     string   `json:"key"` // "<ref>@<day>#<n>"
	Source  string   `json:"source"`
	Ref     string   `json:"ref"`
	Day     string   `json:"day"`
	N       int      `json:"n"`
	Who     string   `json:"who"`
	Name    string   `json:"name"`
	Quote   string   `json:"quote"`
	Line    string   `json:"line,omitempty"` // the line as the source shows it, label included
	Members []string `json:"members,omitempty"`
	// MembersTotal: how many members the chat has (Members shows at most 40, the attributed person first).
	MembersTotal int    `json:"members_total,omitempty"`
	From         string `json:"from"` // speaker_check (the speaker package's sample) | evidence
	Verdict      string `json:"verdict,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

func runAccept(ctx context.Context, in acceptInput) (*acceptReport, error) {
	rep := &acceptReport{State: in.Dir, Day: in.Day, Judge: "none"}
	if in.Model != nil {
		rep.Judge = "model"
	}
	rep.Empty, rep.Exits = sampleEmpty(in)
	rep.Abstract = sampleAbstract(in)
	rep.Facts = sampleFacts(ctx, in)
	rep.Speakers = sampleSpeakers(ctx, in)
	if in.Model != nil {
		j := &judger{m: in.Model}
		j.empty(ctx, rep.Empty)
		j.abstract(ctx, rep.Abstract)
		j.facts(ctx, rep.Facts)
		j.speakers(ctx, rep.Speakers)
		rep.Errors = j.errs
	}
	var f1 map[string]string
	for _, f := range rep.Facts {
		if f.Verdict != "" {
			if f1 == nil {
				f1 = map[string]string{}
			}
			f1[f.ID] = f.Verdict
		}
	}
	rs, err := readings.Compute(readings.Input{St: in.St, Dir: in.Dir, Day: in.Day, Loc: in.Loc, Cards: in.Cards,
		CardsMissing: in.CardsMissing, F1Model: f1})
	if err != nil {
		return nil, err
	}
	rep.Readings = rs
	return rep, nil
}

// ---- samples ------------------------------------------------------------------------------------------------------

// exitEntries are the entries exits A and B carry, from the exits manifest the compiler keeps beside the state.
func exitEntries(dir string) (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, exits.ManifestPath))
	if err != nil {
		return nil, err
	}
	var man struct {
		Exits map[string]struct {
			Entries []string `json:"entries"`
		} `json:"exits"`
	}
	if err := json.Unmarshal(raw, &man); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, x := range []string{"A", "B"} {
		for _, id := range man.Exits[x].Entries {
			if out[id] == "" {
				out[id] = x
			}
		}
	}
	return out, nil
}

func exitText(e *entry.Entry) string {
	if e.Export != nil {
		if e.Export.User != "" {
			return e.Export.User
		}
		if e.Export.Text != "" {
			return e.Export.Text
		}
	}
	return e.Shown()
}

// sampleEmpty draws 30 entries of exits A and B, at least 4 per facet that has them (all when fewer).
func sampleEmpty(in acceptInput) ([]emptyItem, string) {
	ids, err := exitEntries(in.Dir)
	from := "exits.json (A, B)"
	byFacet := map[entry.Facet][]*entry.Entry{}
	if err != nil {
		// No exits compiled yet: what would be eligible, so the check still says something; the report says so.
		from = "no exits manifest: live non-action entries of ①②③④⑤⑥ (" + err.Error() + ")"
		for _, e := range in.St.Entries {
			if e.Live() && e.Facet != entry.People && (e.Grain != entry.Action || e.Facet == entry.Tools || e.Origin != entry.Behavior) {
				byFacet[e.Facet] = append(byFacet[e.Facet], e)
			}
		}
	} else {
		for id := range ids {
			if e := in.St.Entries[id]; e != nil {
				byFacet[e.Facet] = append(byFacet[e.Facet], e)
			}
		}
	}
	var pool, picked []*entry.Entry
	for _, f := range entry.Facets {
		got := drawEntries(byFacet[f], 4, in.Rand)
		picked = append(picked, got...)
		for _, e := range byFacet[f] {
			if !slices.Contains(got, e) {
				pool = append(pool, e)
			}
		}
	}
	picked = append(picked, drawEntries(pool, max(0, 30-len(picked)), in.Rand)...)
	var out []emptyItem
	for _, e := range picked {
		out = append(out, emptyItem{ID: e.ID, Facet: e.Facet, Grain: e.Grain, Exit: ids[e.ID], Text: exitText(e)})
	}
	return out, from
}

func sampleAbstract(in acceptInput) []abstractItem {
	var abs []*entry.Entry
	for _, e := range in.St.Entries {
		if e.Live() && e.Grain == entry.Abstract {
			abs = append(abs, e)
		}
	}
	var out []abstractItem
	for _, e := range drawEntries(abs, 10, in.Rand) {
		days := map[string]bool{}
		for _, id := range e.Members {
			if m := in.St.Entries[id]; m != nil {
				for _, d := range m.ActiveDays {
					days[d] = true
				}
			}
		}
		for _, d := range e.ActiveDays {
			days[d] = true
		}
		it := abstractItem{ID: e.ID, Facet: e.Facet, Text: e.Shown(), Default: e.Default, Situations: e.Situations,
			Members: len(e.Members), Days: len(days)}
		it.Structural = it.Members >= 3 && len(it.Situations) >= 2 && it.Days >= 2 && strings.TrimSpace(it.Default) != ""
		out = append(out, it)
	}
	return out
}

func sampleFacts(ctx context.Context, in acceptInput) []factItem {
	// The same thirty the check page shows for the period (pickFacts), so the user reviews what the model judged.
	var out []factItem
	for _, e := range pickFacts(in.St, in.Day) {
		it := factItem{ID: e.ID, Facet: e.Facet, Person: e.Person, Text: e.Shown(), Evidence: e.Evidence}
		if len(it.Evidence) > 5 {
			it.Evidence = it.Evidence[:5]
		}
		for _, ev := range it.Evidence {
			it.Context = append(it.Context, around(ctx, in.Src, ev.Ref, ev.Day, ev.N, 3)...)
			n := sourceName(ctx, in.Src, ev.Ref)
			if n == "" {
				n = in.Sessions[ev.Ref]
			}
			if n != "" && !slices.Contains(it.Sources, n) {
				it.Sources = append(it.Sources, n)
			}
		}
		out = append(out, it)
	}
	return out
}

// sourceName is the name a cited unit goes by in the knowledge base ("" when it has none to give).
func sourceName(ctx context.Context, src kbsrc.Source, ref string) string {
	kind, id, ok := strings.Cut(ref, "/")
	if src == nil || !ok || kind != "chats" {
		return ""
	}
	d, err := src.Get(ctx, kind, id)
	if err != nil {
		return ""
	}
	var m struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(d.Meta, &m)
	return m.Name
}

// around is the lines n-k…n+k of a chat's day as the knowledge base shows them (nothing for other units).
func around(ctx context.Context, src kbsrc.Source, ref, day string, n, k int) []string {
	kind, id, ok := strings.Cut(ref, "/")
	if src == nil || !ok || kind != "chats" || n <= 0 {
		return nil
	}
	r, err := src.Day(ctx, id, day)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range r.Messages {
		if m.N >= n-k && m.N <= n+k {
			out = append(out, fmt.Sprintf("%s@%s#%d %s", ref, day, m.N, m.Text))
		}
	}
	return out
}

// connector is the source a chat unit comes from ("lark", "wechat", "mail"…), "" for units that are not chats.
func connector(ref string) string {
	kind, id, ok := strings.Cut(ref, "/")
	if !ok || kind != "chats" || strings.HasPrefix(id, "persona-") {
		return ""
	}
	c, _, _ := strings.Cut(id, "-")
	return c
}

// sampleSpeakers takes, per source, the speaker package's sampled lines first, then attributed lines from the
// entries' evidence (lines resolved to the user or a person), to 30 per source.
func sampleSpeakers(ctx context.Context, in acceptInput) []speakerItem {
	by := map[string][]speakerItem{}
	seen := map[string]bool{}
	add := func(it speakerItem) {
		it.Key = fmt.Sprintf("%s@%s#%d", it.Ref, it.Day, it.N)
		if seen[it.Key] || it.Who == "" || it.Source == "" {
			return
		}
		seen[it.Key] = true
		by[it.Source] = append(by[it.Source], it)
	}
	for _, sc := range in.St.SpeakerChecks {
		add(speakerItem{Source: sc.Source, Ref: sc.Ref, Day: sc.Day, N: sc.N, Who: sc.Who, Name: sc.Name, Quote: sc.Quote, From: "speaker_check"})
	}
	extra := map[string][]speakerItem{}
	for _, id := range sortedIDs(in.St.Entries) {
		e := in.St.Entries[id]
		if e.Sensitive != "" {
			continue
		}
		for _, ev := range e.Evidence {
			src := connector(ev.Ref)
			if src == "" || ev.Who == "" || ev.Reported || ev.N <= 0 {
				continue
			}
			extra[src] = append(extra[src], speakerItem{Source: src, Ref: ev.Ref, Day: ev.Day, N: ev.N, Who: ev.Who, Name: ev.Speaker, Quote: ev.Quote, From: "evidence"})
		}
	}
	for _, src := range sortedIDs(extra) {
		xs := extra[src]
		in.Rand.Shuffle(len(xs), func(i, j int) { xs[i], xs[j] = xs[j], xs[i] })
		for _, it := range xs {
			if len(by[src]) >= 30 {
				break
			}
			add(it)
		}
	}
	var out []speakerItem
	members := map[string][]string{}
	for _, src := range sortedIDs(by) {
		for i, it := range by[src] {
			if i >= 30 {
				break
			}
			if l := around(ctx, in.Src, it.Ref, it.Day, it.N, 0); len(l) > 0 {
				_, it.Line, _ = strings.Cut(l[0], " ")
			}
			ms, ok := members[it.Ref]
			if !ok {
				ms = chatMembers(ctx, in.Src, it.Ref)
				members[it.Ref] = ms
			}
			it.Members, it.MembersTotal = membersFor(ms, it.Name)
			out = append(out, it)
		}
	}
	return out
}

// membersFor is what the judge sees of a chat's members for one line: the attributed person first when they are a
// member, then others, at most 40, and the total.
func membersFor(all []string, name string) ([]string, int) {
	var out []string
	if name != "" && slices.Contains(all, name) {
		out = append(out, name)
	}
	for _, m := range all {
		if len(out) >= 40 {
			break
		}
		if m != name {
			out = append(out, m)
		}
	}
	return out, len(all)
}

// chatMembers are the names of a chat's members as the knowledge base lists them (at most 500).
func chatMembers(ctx context.Context, src kbsrc.Source, ref string) []string {
	kind, id, ok := strings.Cut(ref, "/")
	if src == nil || !ok || kind != "chats" {
		return nil
	}
	d, err := src.Get(ctx, "chats", id)
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range kbsrc.DecodeMeta(d).People {
		name := p
		if pd, err := src.Get(ctx, "people", strings.TrimPrefix(p, "person:")); err == nil {
			if n := kbsrc.DecodeMeta(pd).Name; n != "" {
				name = n
			}
		}
		out = append(out, name)
		if len(out) >= 500 {
			break
		}
	}
	return out
}

// ---- the model's pre-judgement ------------------------------------------------------------------------------------

type judger struct {
	m    model.Model
	errs []string
}

const judgeBatch = 10

// ask sends items in batches of ten and decodes {"items": [...]} answers into got (keyed by "id").
func (j *judger) ask(ctx context.Context, step, system string, schema string, items []map[string]any, got func(id string, raw json.RawMessage)) {
	for i := 0; i < len(items); i += judgeBatch {
		batch := items[i:min(i+judgeBatch, len(items))]
		user, _ := json.Marshal(map[string]any{"items": batch})
		resp, err := j.m.Complete(ctx, model.Request{Step: "accept_" + step, Tier: model.Small, System: system, User: string(user),
			MaxTokens: 4000, Schema: json.RawMessage(schema)})
		if err != nil {
			if errors.Is(err, model.ErrRefused) {
				j.errs = append(j.errs, step+": the model declined a batch (stopped there, not resent)")
				return
			}
			j.errs = append(j.errs, fmt.Sprintf("%s batch %d: %v", step, i/judgeBatch, err))
			continue
		}
		var ans struct {
			Items []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal([]byte(resp.Text), &ans); err != nil {
			j.errs = append(j.errs, fmt.Sprintf("%s batch %d: %v", step, i/judgeBatch, err))
			continue
		}
		for _, raw := range ans.Items {
			var head struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &head) == nil && head.ID != "" {
				got(head.ID, raw)
			}
		}
	}
}

const schemaEmpty = `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"answerable":{"type":"boolean"},"moment":{"type":"string"},"difference":{"type":"string"}},"required":["id","answerable","moment","difference"]}}},"required":["items"]}`

const promptEmpty = `You check lines that are written into an AI assistant's standing instructions and skills about one particular user.
For each item answer the fixed question: "If this line were deleted, at which moment would the assistant act differently, and how?"
Name a concrete moment (for example: when asking the user to choose between options, when touching a named machine or tool, when starting a named kind of task, when replying to a named person) and the concrete difference in what the assistant does.
Set answerable to false when no such moment can be named: the line would hold for any user, states no behaviour, or is too vague to change anything the assistant does.
An item of facet "persona" is there so the assistant understands who the user is, not to tell it what to do: for it, answerable means it is a specific fact about this user (background, role or responsibilities, what they work on or aim at over time, their circumstances, a view or habit with something specific to it), false when it would hold for almost anyone ("values efficiency") or is too vague to tell this user from another; moment is what it says about the user, difference empty.
Judge only from the line itself. Write moment and difference in the line's language, one short sentence each (empty when not answerable).
Answer with JSON {"items":[{"id","answerable","moment","difference"}]}, one per item, ids as given.`

func (j *judger) empty(ctx context.Context, xs []emptyItem) {
	var items []map[string]any
	for _, x := range xs {
		items = append(items, map[string]any{"id": x.ID, "facet": x.Facet, "line": x.Text})
	}
	idx := map[string]*emptyItem{}
	for i := range xs {
		idx[xs[i].ID] = &xs[i]
	}
	j.ask(ctx, "empty", promptEmpty, schemaEmpty, items, func(id string, raw json.RawMessage) {
		var a struct {
			Answerable bool   `json:"answerable"`
			Moment     string `json:"moment"`
			Difference string `json:"difference"`
		}
		if x := idx[id]; x != nil && json.Unmarshal(raw, &a) == nil {
			x.Answerable, x.Moment, x.Difference = &a.Answerable, a.Moment, a.Difference
		}
	})
}

const schemaAbstract = `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"generic":{"type":"boolean"},"reason":{"type":"string"}},"required":["id","generic","reason"]}}},"required":["items"]}`

const promptAbstract = `Each item claims an overall tendency of one particular user, with what a capable person who does not know the user would do by default ("default") and the kinds of situations it governs.
Ask the fixed question: "Replace this user with any capable person: does the line still hold?"
Set generic to true when it does (it says what any competent person does, or does not differ from the stated default in a way that would change what an assistant does), false when it describes something specific to this user that differs from the default.
Judge only from the item. Give a one-sentence reason in the item's language.
Answer with JSON {"items":[{"id","generic","reason"}]}, one per item, ids as given.`

func (j *judger) abstract(ctx context.Context, xs []abstractItem) {
	var items []map[string]any
	idx := map[string]*abstractItem{}
	for i := range xs {
		idx[xs[i].ID] = &xs[i]
		items = append(items, map[string]any{"id": xs[i].ID, "line": xs[i].Text, "default": xs[i].Default, "situations": xs[i].Situations})
	}
	j.ask(ctx, "abstract", promptAbstract, schemaAbstract, items, func(id string, raw json.RawMessage) {
		var a struct {
			Generic bool   `json:"generic"`
			Reason  string `json:"reason"`
		}
		if x := idx[id]; x != nil && json.Unmarshal(raw, &a) == nil {
			pass := x.Structural && !a.Generic
			x.Generic, x.Reason, x.Pass = &a.Generic, a.Reason, &pass
		}
	})
}

const schemaFacts = `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"verdict":{"type":"string","enum":["right","wrong","unsupported"]},"reason":{"type":"string"}},"required":["id","verdict","reason"]}}},"required":["items"]}`

const promptFacts = `Each item is a fact a curator wrote into one user's knowledge base, with the source lines it cites (original words, "quotes"), where available the lines around them ("context") and the names of the chats or agent sessions they come from ("sources": a group's or a session's name says what it is about).
Judge each fact against those lines only, without outside knowledge. An item without "context" is judged on its quotes alone; the quotes are the evidence.
- right: the lines state it or clearly imply it;
- wrong: the lines contradict it (wrong person, wrong thing, wrong time, reversed relation…);
- unsupported: the lines do not say it.
Give a one-sentence reason in the fact's language.
Answer with JSON {"items":[{"id","verdict","reason"}]}, one per item, ids as given.`

func (j *judger) facts(ctx context.Context, xs []factItem) {
	var items []map[string]any
	idx := map[string]*factItem{}
	for i := range xs {
		idx[xs[i].ID] = &xs[i]
		var quotes []string
		for _, ev := range xs[i].Evidence {
			s := ev.Quote
			if ev.Speaker != "" {
				s = ev.Speaker + ": " + s
			}
			quotes = append(quotes, s)
		}
		item := map[string]any{"id": xs[i].ID, "fact": xs[i].Text, "quotes": quotes}
		if len(xs[i].Sources) > 0 {
			item["sources"] = xs[i].Sources
		}
		if len(xs[i].Context) > 0 {
			// Left out when there is none: a "context": null read as "no evidence at all" and judged a supported
			// fact unsupported.
			item["context"] = xs[i].Context
		}
		items = append(items, item)
	}
	j.ask(ctx, "f1", promptFacts, schemaFacts, items, func(id string, raw json.RawMessage) {
		var a struct {
			Verdict string `json:"verdict"`
			Reason  string `json:"reason"`
		}
		if x := idx[id]; x != nil && json.Unmarshal(raw, &a) == nil {
			if a.Verdict != "right" && a.Verdict != "wrong" {
				a.Verdict = "unsupported"
			}
			x.Verdict, x.Reason = a.Verdict, a.Reason
		}
	})
}

const schemaSpeakers = `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"verdict":{"type":"string","enum":["yes","no","unsure"]},"reason":{"type":"string"}},"required":["id","verdict","reason"]}}},"required":["items"]}`

const promptSpeakers = `Each item is one line from a chat: the line as the source shows it (with its speaker label, when available), the text, the chat's members (at most 40 listed; "members_total" is the full count, so someone missing from a long list may still be a member), and the person a curator attributed the line to ("attributed_to"; "self" means the owner of the knowledge base, named in "owner_label" when known).
Answer whether the line is by that person: yes, no, or unsure (the label and members do not settle it).
Use only the line, its label and the member list. Give a one-sentence reason.
Answer with JSON {"items":[{"id","verdict","reason"}]}, one per item, ids as given.`

func (j *judger) speakers(ctx context.Context, xs []speakerItem) {
	var items []map[string]any
	idx := map[string]*speakerItem{}
	for i := range xs {
		idx[xs[i].Key] = &xs[i]
		who := xs[i].Name
		if xs[i].Who == entry.WhoSelf {
			who = "self"
		}
		items = append(items, map[string]any{"id": xs[i].Key, "line": xs[i].Line, "text": xs[i].Quote, "members": xs[i].Members,
			"members_total": xs[i].MembersTotal, "attributed_to": who, "owner_label": ownerLabel(xs[i])})
	}
	j.ask(ctx, "speakers", promptSpeakers, schemaSpeakers, items, func(id string, raw json.RawMessage) {
		var a struct {
			Verdict string `json:"verdict"`
			Reason  string `json:"reason"`
		}
		if x := idx[id]; x != nil && json.Unmarshal(raw, &a) == nil {
			if a.Verdict != "yes" && a.Verdict != "no" {
				a.Verdict = "unsure"
			}
			x.Verdict, x.Reason = a.Verdict, a.Reason
		}
	})
}

func ownerLabel(it speakerItem) string {
	if it.Who == entry.WhoSelf {
		return it.Name
	}
	return ""
}

// ---- output -------------------------------------------------------------------------------------------------------

// prejudge is what the check page shows next to each sampled line: the model's call and its reason.
func (r *acceptReport) prejudge() map[string]any {
	facts := map[string]map[string]string{}
	for _, f := range r.Facts {
		if f.Verdict != "" {
			facts[f.ID] = map[string]string{"verdict": f.Verdict, "reason": f.Reason}
		}
	}
	spk := map[string]map[string]string{}
	for _, s := range r.Speakers {
		if s.Verdict != "" {
			spk[s.Key] = map[string]string{"verdict": s.Verdict, "reason": s.Reason}
		}
	}
	return map[string]any{"day": r.Day, "facts": facts, "speakers": spk}
}

// acceptCounts are the headline numbers of a run.
type acceptCounts struct {
	EmptyN, EmptyNo, EmptyJudged int
	EmptyByFacet                 map[entry.Facet][2]int // judged, not answerable
	AbsN, AbsStruct, AbsPass     int
	FactRight, FactWrong, FactUn int
	SpeakerBy                    map[string][3]int // yes, no, unsure
}

func (r *acceptReport) counts() acceptCounts {
	c := acceptCounts{EmptyByFacet: map[entry.Facet][2]int{}, SpeakerBy: map[string][3]int{}}
	c.EmptyN = len(r.Empty)
	for _, e := range r.Empty {
		if e.Answerable != nil {
			c.EmptyJudged++
			x := c.EmptyByFacet[e.Facet]
			x[0]++
			if !*e.Answerable {
				c.EmptyNo++
				x[1]++
			}
			c.EmptyByFacet[e.Facet] = x
		}
	}
	c.AbsN = len(r.Abstract)
	for _, a := range r.Abstract {
		if a.Structural {
			c.AbsStruct++
		}
		if a.Pass != nil && *a.Pass {
			c.AbsPass++
		}
	}
	for _, f := range r.Facts {
		switch f.Verdict {
		case "right":
			c.FactRight++
		case "wrong":
			c.FactWrong++
		case "unsupported":
			c.FactUn++
		}
	}
	for _, s := range r.Speakers {
		x := c.SpeakerBy[s.Source]
		switch s.Verdict {
		case "yes":
			x[0]++
		case "no":
			x[1]++
		default:
			x[2]++
		}
		c.SpeakerBy[s.Source] = x
	}
	return c
}

func (r *acceptReport) summary() string {
	var b strings.Builder
	c := r.counts()
	fmt.Fprintf(&b, "state %s, period ending %s, judge %s\n", r.State, r.Day, r.Judge)
	for _, x := range r.Readings {
		v := "量不出"
		if x.Measure {
			v = fmt.Sprintf("%.3f", x.Value)
		}
		fmt.Fprintf(&b, "%-14s %4d/%-5d %s  %s\n", x.Code, x.Num, x.Den, v, x.Note)
	}
	fmt.Fprintf(&b, "empty talk: %d sampled (%s), %d judged, %d not answerable", c.EmptyN, r.Exits, c.EmptyJudged, c.EmptyNo)
	if c.EmptyJudged > 0 {
		fmt.Fprintf(&b, " (%.0f%%)", 100*float64(c.EmptyNo)/float64(c.EmptyJudged))
	}
	b.WriteString("\n  by facet:")
	for _, f := range entry.Facets {
		if x, ok := c.EmptyByFacet[f]; ok {
			fmt.Fprintf(&b, " %s %d/%d", f, x[1], x[0])
		}
	}
	fmt.Fprintf(&b, "\nabstract: %d sampled, %d pass the structure, %d pass the gate\n", c.AbsN, c.AbsStruct, c.AbsPass)
	fmt.Fprintf(&b, "F1 (model first): %d sampled: right %d, wrong %d, unsupported %d\n", len(r.Facts), c.FactRight, c.FactWrong, c.FactUn)
	for _, src := range sortedIDs(c.SpeakerBy) {
		x := c.SpeakerBy[src]
		fmt.Fprintf(&b, "speaker %-8s %d lines: yes %d, no %d, unsure %d\n", src, x[0]+x[1]+x[2], x[0], x[1], x[2])
	}
	for _, e := range r.Errors {
		fmt.Fprintf(&b, "error: %s\n", e)
	}
	return b.String()
}

func (r *acceptReport) markdown() string {
	var b strings.Builder
	b.WriteString("# Acceptance checks\n\n```\n" + r.summary() + "```\n")
	b.WriteString("\n## Empty talk (9.4)\n\n")
	for _, e := range r.Empty {
		mark := "?"
		if e.Answerable != nil {
			mark = map[bool]string{true: "ok", false: "EMPTY"}[*e.Answerable]
		}
		fmt.Fprintf(&b, "- [%s] %s %s/%s exit %s: %s\n  - %s / %s\n", mark, e.ID, e.Facet, e.Grain, e.Exit, e.Text, e.Moment, e.Difference)
	}
	b.WriteString("\n## Abstract gate (5.3)\n\n")
	for _, a := range r.Abstract {
		pass := "?"
		if a.Pass != nil {
			pass = fmt.Sprint(*a.Pass)
		}
		fmt.Fprintf(&b, "- [pass %s, structure %v: members %d, situations %d, days %d] %s %s\n  - default: %s\n  - %s\n", pass, a.Structural,
			a.Members, len(a.Situations), a.Days, a.ID, a.Text, a.Default, a.Reason)
	}
	b.WriteString("\n## F1 facts (9.5)\n\n")
	for _, f := range r.Facts {
		fmt.Fprintf(&b, "- [%s] %s %s\n  - %s\n", f.Verdict, f.ID, f.Text, f.Reason)
		for _, ev := range f.Evidence {
			fmt.Fprintf(&b, "  - %s@%s#%d %s: %s\n", ev.Ref, ev.Day, ev.N, ev.Speaker, ev.Quote)
		}
	}
	b.WriteString("\n## Speakers (U8)\n\n")
	for _, s := range r.Speakers {
		fmt.Fprintf(&b, "- [%s] %s %s → %s (%s): %s\n  - %s\n", s.Verdict, s.Source, s.Key, s.Name, s.Who, s.Quote, s.Reason)
	}
	return b.String()
}

// applyOnboarding saves first-run answers from a file ({"use", "musts", "focus"}) as the page does.
func applyOnboarding(ctx context.Context, c *curate.Curator, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var ans struct {
		Use   string   `json:"use"`
		Musts []string `json:"musts"`
		Focus []string `json:"focus"`
	}
	if err := json.Unmarshal(raw, &ans); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	a := &app{c: c}
	c.St.Lock()
	defer c.St.Unlock()
	if err := a.saveOnboarding(ctx, ans.Use, ans.Musts, ans.Focus); err != nil {
		return err
	}
	return c.St.Save()
}
