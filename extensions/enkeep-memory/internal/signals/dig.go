package signals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/hubapi"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/prompts"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
)

// Dig decisions (entry.Dig.Decision).
const (
	DecDig     = "dig"
	DecSkip    = "skip"
	DecWaiting = "waiting_hub"
)

const (
	contextItems  = 6     // words on each side of a hit shown to the model
	segmentBudget = 40000 // bytes of a segment's calls shown to the model
)

// DigID is the id of the dig of one hit.
func DigID(ref, signal string, seq int) string {
	return "d-" + hash(ref, signal, fmt.Sprint(seq))[:10]
}

// DigOf is the dig an entry came from: by its "dig:<id>" flag, else by evidence inside a dug stretch.
func DigOf(st *store.State, e *entry.Entry) *entry.Dig {
	for _, f := range e.Flags {
		if id, ok := strings.CutPrefix(f, "dig:"); ok {
			for i := range st.Digs {
				if st.Digs[i].ID == id {
					return &st.Digs[i]
				}
			}
		}
	}
	// An entry that was there before and seen again by a dig has no flag: the dig lists it.
	for i := range st.Digs {
		if slices.Contains(st.Digs[i].Entries, e.ID) {
			return &st.Digs[i]
		}
	}
	for _, ev := range e.Evidence {
		for i := range st.Digs {
			d := &st.Digs[i]
			if d.Decision == DecDig && d.Ref == ev.Ref && (ev.Seq == d.HitSeq || (ev.Seq >= d.FromSeq && ev.Seq <= d.ThruSeq)) {
				return d
			}
		}
	}
	return nil
}

// dig asks the model, per session, which hits deserve a dig and where; reads only those stretches; and keeps what
// they teach. Every hit becomes a Dig record, whatever was decided.
func dig(ctx context.Context, o *Options, st *store.State, g *gathered, hits []hit, res *Result) error {
	done := map[string]bool{}
	for _, d := range st.Digs {
		done[d.ID] = true
	}
	at := o.now().UTC().Format(time.RFC3339)
	bySession := map[string][]hit{}
	var order []string
	for _, h := range hits {
		id := DigID(h.Ref, h.Signal, h.Seq)
		if done[id] {
			continue
		}
		done[id] = true
		rec := entry.Dig{ID: id, Signal: h.Signal, Ref: h.Ref, Day: g.Day, At: at, HitSeq: h.Seq, Hit: h.What}
		switch {
		case h.sess == nil:
			rec.Decision, rec.Reason = DecSkip, "no session to read"
		case o.Model == nil:
			rec.Decision, rec.Reason = DecSkip, "no model"
		case len(folds(h.sess)) == 0:
			rec.Decision, rec.Reason = DecSkip, "no folded work near it"
		default:
			if _, ok := bySession[h.Ref]; !ok {
				order = append(order, h.Ref)
			}
			bySession[h.Ref] = append(bySession[h.Ref], h)
			continue
		}
		st.Digs = append(st.Digs, rec)
	}
	// The model's decisions, a session at a time (concurrently with Workers > 1).
	decisions := make([]map[int]decision, len(order))
	parallel(ctx, o.Workers, len(order), func(i int) {
		hs := bySession[order[i]]
		d, err := decide(ctx, o, hs[0].sess, hs)
		if err != nil {
			o.logf("signals: dig decision %s: %v", order[i], err)
		}
		decisions[i] = d
	})
	type job struct {
		rec *entry.Dig
		h   hit
		got minedAnswer
	}
	var recs []*entry.Dig
	var jobs []*job
	for i, ref := range order {
		for j, h := range bySession[ref] {
			rec := &entry.Dig{ID: DigID(h.Ref, h.Signal, h.Seq), Signal: h.Signal, Ref: h.Ref, Day: g.Day, At: at, HitSeq: h.Seq, Hit: h.What}
			d, ok := decisions[i][j]
			switch {
			case !ok:
				rec.Decision, rec.Reason = DecSkip, "no decision"
			case !d.Dig:
				rec.Decision, rec.Reason = DecSkip, cut(d.Reason, 200)
			default:
				rec.Decision, rec.Reason = DecDig, cut(d.Reason, 200)
				rec.FromSeq, rec.ThruSeq = clamp(h.sess, h.Seq, d.From, d.Through)
				jobs = append(jobs, &job{rec: rec, h: h})
			}
			recs = append(recs, rec)
		}
	}
	// The stretches read and asked about (concurrently), then kept in order (Observe is single-threaded).
	parallel(ctx, o.Workers, len(jobs), func(i int) { jobs[i].got = ask(ctx, o, jobs[i].h.sess, jobs[i].h, jobs[i].rec) })
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, j := range jobs {
		rec, got := j.rec, j.got
		if got.err != nil {
			rec.Decision = DecWaiting
			if !isWaiting(got.err) {
				rec.Decision, rec.Reason = DecSkip, cut(verify.Redact(got.err.Error()), 120)
			}
		}
		rec.Bytes = got.bytes
		rec.Entries = keepMined(o, st, j.h.sess, rec, got)
		res.SegmentBytes += got.bytes
		if got.bytes > 0 {
			res.Digs++
		}
		res.Mined = append(res.Mined, rec.Entries...)
	}
	for _, rec := range recs {
		st.Digs = append(st.Digs, *rec)
	}
	// The sources card's S4 row: what the digs read (they read through the hub directly, not through the sources'
	// Segment, which would count it).
	if res.Digs > 0 {
		if st.Sources == nil {
			st.Sources = map[string]*entry.SourceStatus{}
		}
		key := entry.SrcTools + "/segments"
		s := st.Sources[key]
		if s == nil {
			s = &entry.SourceStatus{Code: entry.SrcTools, Name: "segments", LearnPeople: true}
			st.Sources[key] = s
		}
		s.Available, s.Missing, s.LastRead = true, "", g.Day
		for _, j := range jobs {
			if j.got.bytes > 0 {
				s.Units++
				s.Lines += len(j.got.shown)
				s.Bytes += j.got.bytes
			}
		}
	}
	return nil
}

// parallel runs fn(0..n-1) on up to workers goroutines (one when workers ≤ 1), and waits.
func parallel(ctx context.Context, workers, n int, fn func(i int)) {
	if workers <= 1 {
		for i := 0; i < n && ctx.Err() == nil; i++ {
			fn(i)
		}
		return
	}
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < min(workers, n); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				fn(i)
			}
		}()
	}
	for i := 0; i < n && ctx.Err() == nil; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
}

func isWaiting(err error) bool { return errors.Is(err, hubapi.ErrWaitingHub) }

// folds lists a session's folded stretches.
func folds(s *session) []hubapi.Fold {
	var out []hubapi.Fold
	for _, it := range s.Items {
		if it.Kind == kFold && it.Fold != nil && it.Fold.To >= it.Fold.From {
			out = append(out, *it.Fold)
		}
	}
	return out
}

// reach is the most a hit may read: the stretch it is in, or the stretch just before it through the one just after
// it (one stretch on each side of the signal, K17).
func reach(s *session, seq int) (from, through int, ok bool) {
	fs := folds(s)
	var prev, next, in *hubapi.Fold
	for i := range fs {
		f := &fs[i]
		switch {
		case seq >= f.From && seq <= f.To:
			in = f
		case f.To < seq:
			prev = f
		case f.From > seq && next == nil:
			next = f
		}
	}
	if in != nil {
		return in.From, in.To, true
	}
	switch {
	case prev != nil && next != nil:
		return prev.From, next.To, true
	case prev != nil:
		return prev.From, prev.To, true
	case next != nil:
		return next.From, next.To, true
	}
	return 0, 0, false
}

// clamp keeps the model's stretch inside the hit's reach; a stretch outside it falls back to the nearest one.
func clamp(s *session, seq, from, through int) (int, int) {
	lo, hi, ok := reach(s, seq)
	if !ok {
		return 0, 0
	}
	if from < lo || from > hi {
		from = lo
	}
	if through > hi || through < from {
		through = hi
	}
	return from, through
}

type decision struct {
	H       int    `json:"h"`
	Dig     bool   `json:"dig"`
	Reason  string `json:"reason"`
	From    int    `json:"from_seq"`
	Through int    `json:"through_seq"`
}

type pageLine struct {
	Seq    int      `json:"seq"`
	Kind   string   `json:"kind"`
	Text   string   `json:"text,omitempty"`
	Calls  int      `json:"calls,omitempty"`
	Errors *int     `json:"errors,omitempty"` // absent when the hub did not say
	Tools  []string `json:"tools,omitempty"`
	Files  []string `json:"files,omitempty"`
	Thru   int      `json:"through_seq,omitempty"`
}

// page is the part of a session's folded page around the hits, as the model sees it.
func page(s *session, seqs []int) []pageLine {
	keep := map[int]bool{}
	for _, seq := range seqs {
		pos := 0
		for i, it := range s.Items {
			if it.Seq <= seq {
				pos = i
			}
		}
		for i := max(0, pos-contextItems); i <= min(len(s.Items)-1, pos+contextItems); i++ {
			keep[i] = true
		}
	}
	var out []pageLine
	for i, it := range s.Items {
		if !keep[i] {
			continue
		}
		switch it.Kind {
		case kUser, kAgent, kNotice:
			out = append(out, pageLine{Seq: it.Seq, Kind: it.Kind, Text: verify.Redact(cut(it.Text, 400))})
		case kFold:
			if f := it.Fold; f != nil {
				l := pageLine{Seq: f.From, Kind: kFold, Calls: f.Calls, Tools: f.Tools, Files: f.Files, Thru: f.To}
				if f.Known {
					n := f.Errors
					l.Errors = &n
				}
				out = append(out, l)
			}
		}
	}
	return out
}

func decide(ctx context.Context, o *Options, s *session, hs []hit) (map[int]decision, error) {
	type inHit struct {
		H      int    `json:"h"`
		Signal string `json:"signal"`
		Seq    int    `json:"seq"`
		What   string `json:"what"`
	}
	in := struct {
		Title string     `json:"session_title"`
		Page  []pageLine `json:"page"`
		Hits  []inHit    `json:"hits"`
	}{Title: s.Title}
	var seqs []int
	for i, h := range hs {
		in.Hits = append(in.Hits, inHit{H: i, Signal: h.Signal, Seq: h.Seq, What: h.What})
		seqs = append(seqs, h.Seq)
	}
	in.Page = page(s, seqs)
	user, _ := json.Marshal(in)
	resp, err := o.Model.Complete(ctx, model.Request{Step: "signals_dig", Tier: o.Tier, System: prompts.Dig, User: string(user),
		MaxTokens: 4000, Schema: prompts.DigSchema})
	if err != nil {
		return nil, err
	}
	var out struct {
		Decisions []decision `json:"decisions"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &out); err != nil {
		return nil, fmt.Errorf("answer: %w", err)
	}
	m := map[int]decision{}
	for _, d := range out.Decisions {
		if d.H >= 0 && d.H < len(hs) {
			m[d.H] = d
		}
	}
	return m, nil
}

type call struct {
	Seq       int    `json:"seq"` // the call's
	Tool      string `json:"tool"`
	Input     string `json:"input"`
	ResultSeq int    `json:"result_seq,omitempty"` // the result's: quote a result with this seq
	Result    string `json:"result"`
	Error     bool   `json:"error"`
}

// calls pairs a segment's tool calls with their results by Call ID when present, in order, cut short and with
// credentials redacted. A result without a known Call ID keeps the old hub's most-recent-open pairing.
func calls(items []hubapi.Item) []call {
	var out []call
	byCall := map[string]int{}
	open := -1
	for _, it := range items {
		switch it.Kind {
		case kToolUse:
			out = append(out, call{Seq: it.Seq, Tool: it.Tool, Input: verify.Redact(cut(it.Text, 600))})
			open = len(out) - 1
			if it.Call != "" {
				byCall[it.Call] = open
			}
		case kToolResult:
			n := 500
			if it.Err {
				n = 1500
			}
			i := open
			if it.Call != "" {
				if matched, ok := byCall[it.Call]; ok {
					i = matched
				}
			}
			if i >= 0 && out[i].Result == "" {
				out[i].Result, out[i].Error, out[i].ResultSeq = verify.Redact(cut(it.Text, n)), it.Err, it.Seq
				if open == i {
					open = -1
				}
			} else {
				out = append(out, call{Seq: it.Seq, ResultSeq: it.Seq, Tool: it.Tool, Result: verify.Redact(cut(it.Text, n)), Error: it.Err})
			}
		}
	}
	size := 0
	for i, c := range out {
		size += len(c.Input) + len(c.Result) + 40
		if size > segmentBudget {
			return out[:i]
		}
	}
	return out
}

type mined struct {
	Facet     string       `json:"facet"`
	Kind      string       `json:"kind"`
	Object    string       `json:"object"`
	Type      string       `json:"type"`
	Text      string       `json:"text"`
	Situation string       `json:"situation"`
	Choice    string       `json:"choice"`
	Why       string       `json:"why"`
	Pitfall   string       `json:"pitfall"`
	Fix       string       `json:"fix"`
	Seq       int          `json:"seq"`
	Quote     string       `json:"quote"`
	Impact    entry.Impact `json:"impact"`
}

// minedAnswer is what a dig's stretch was and what the model said it teaches.
type minedAnswer struct {
	entries []mined
	shown   map[int]hubapi.Item // the items the model saw, by seq: what a quote must be in
	bytes   int
	err     error // reading or mining the stretch failed
}

// ask reads the dig's stretch (the only unfolded read) and asks what it teaches. Safe to run concurrently.
func ask(ctx context.Context, o *Options, s *session, h hit, rec *entry.Dig) minedAnswer {
	items, err := o.Hub.Items(ctx, s.Machine, s.ID, hubapi.ItemsQuery{From: rec.FromSeq, Through: rec.ThruSeq})
	if err != nil {
		return minedAnswer{err: fmt.Errorf("read failed: %w", err)}
	}
	var seg []hubapi.Item
	for _, it := range items {
		if it.Seq >= rec.FromSeq && it.Seq <= rec.ThruSeq {
			seg = append(seg, it)
		}
	}
	raw, _ := json.Marshal(seg)
	got := minedAnswer{bytes: len(raw), shown: map[int]hubapi.Item{}}
	// The words: the folded page's around the hit, and any inside the stretch.
	var ctxLines []pageLine
	seen := map[int]bool{}
	for _, it := range append(append([]hubapi.Item(nil), s.Items...), seg...) {
		if (it.Kind == kUser || it.Kind == kAgent) && !seen[it.Seq] &&
			(abs(it.Seq-h.Seq) <= 3*contextItems || (it.Seq >= rec.FromSeq && it.Seq <= rec.ThruSeq)) {
			seen[it.Seq] = true
			ctxLines = append(ctxLines, pageLine{Seq: it.Seq, Kind: it.Kind, Text: verify.Redact(cut(it.Text, 500))})
			got.shown[it.Seq] = it
		}
	}
	for _, it := range seg {
		got.shown[it.Seq] = it
	}
	cs := calls(seg)
	if len(cs) == 0 && len(ctxLines) == 0 {
		return got
	}
	in := struct {
		Language string     `json:"language"`
		UserWord string     `json:"user_word"`
		Signal   string     `json:"signal"`
		Hit      string     `json:"hit"`
		Title    string     `json:"session_title"`
		Words    []pageLine `json:"words"`
		Calls    []call     `json:"calls"`
	}{Language: o.Lang, UserWord: norm.UserWord(o.Lang), Signal: h.Signal, Hit: h.What, Title: s.Title, Words: ctxLines, Calls: cs}
	user, _ := json.Marshal(in)
	resp, err := o.Model.Complete(ctx, model.Request{Step: "signals_mine", Tier: o.Tier, System: prompts.Mine, User: string(user),
		MaxTokens: 3000, Schema: prompts.MineSchema})
	if err != nil {
		o.logf("signals: mine %s: %v", rec.ID, err)
		got.err = fmt.Errorf("mine failed: %w", err)
		return got
	}
	var out struct {
		Entries []mined `json:"entries"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &out); err != nil {
		o.logf("signals: mine %s: answer: %v", rec.ID, err)
		got.err = fmt.Errorf("mine answer: %w", err)
		return got
	}
	got.entries = out.Entries
	return got
}

// keepMined checks what a dig taught and keeps the entries that pass, through Observe. It returns the entries made
// or seen again.
func keepMined(o *Options, st *store.State, s *session, rec *entry.Dig, got minedAnswer) []string {
	var ids []string
	for _, m := range got.entries {
		e, why := toEntry(o, st, s, rec, m, got.shown)
		if e == nil {
			o.logf("signals: mine %s: dropped (%s)", rec.ID, why)
			continue
		}
		if o.Observe == nil {
			continue
		}
		if id := o.Observe(e, o.Run); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// toEntry checks a mined entry and makes it: anchored to an item the model was shown (the quote must be in it), the
// user's own words for ① and ④, a named object for ②, in the target language, no credential anywhere.
func toEntry(o *Options, st *store.State, s *session, rec *entry.Dig, m mined, shown map[int]hubapi.Item) (*entry.Entry, string) {
	f := entry.Facet(m.Facet)
	if f != entry.Tools && f != entry.Interaction && f != entry.Workflow {
		return nil, "facet"
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return nil, "empty"
	}
	for _, x := range []string{text, m.Pitfall, m.Fix, m.Choice, m.Object, m.Situation, m.Why} {
		if verify.Secret(x) {
			return nil, "secret"
		}
	}
	if !norm.InLanguage(text, o.Lang) {
		return nil, "language"
	}
	it, ok := shown[m.Seq]
	if !ok || strings.TrimSpace(m.Quote) == "" || !strings.Contains(squash(verify.Redact(it.Text)), squash(m.Quote)) {
		return nil, "quote"
	}
	if (f == entry.Interaction || f == entry.Workflow) && it.Kind != kUser {
		return nil, "not the user's words"
	}
	if f == entry.Tools && strings.TrimSpace(m.Object) == "" {
		return nil, "no object"
	}
	if f == entry.Tools && m.Pitfall == "" && m.Fix == "" && m.Choice == "" && !slices.Contains([]string{"approach", "boundary", "ask"}, m.Kind) {
		return nil, "a fact, not a way of working"
	}
	src, role, who := entry.SrcTools, "tool", ""
	switch it.Kind {
	case kUser:
		src, role, who = s.Source, "user", entry.WhoSelf
	case kAgent:
		role = "agent"
		if it.Seq < rec.FromSeq || it.Seq > rec.ThruSeq {
			src = s.Source
		}
	}
	imp := m.Impact
	if !slices.Contains(entry.Moments, imp.Moment) {
		imp.Moment = map[entry.Facet]string{entry.Tools: "use_object", entry.Workflow: "task_start", entry.Interaction: "every_turn"}[f]
	}
	if imp.Action == "" {
		imp.Action = text
	}
	if imp.Object == "" {
		imp.Object = m.Object
	}
	day := rec.Day
	if !it.At.IsZero() {
		day = it.At.In(o.loc()).Format("2006-01-02")
	}
	e := &entry.Entry{Facet: f, Grain: entry.Action, Type: m.Type, Text: text, Situation: m.Situation, Choice: m.Choice,
		Why: m.Why, Object: strings.TrimSpace(m.Object), Pitfall: m.Pitfall, Fix: m.Fix, Impact: imp,
		Evidence: []entry.Evidence{{Source: src, Ref: s.Ref, Day: day, Seq: it.Seq, Who: who, Role: role, Quote: cut(verify.Redact(m.Quote), 200)}},
		Flags:    []string{"dig:" + rec.ID, "signal:" + rec.Signal}}
	if f == entry.Tools {
		e.Type = firstNonEmpty(m.Type, m.Kind)
		e.Scopes = []entry.Scope{{Kind: entry.ScopeObject, Name: e.Object}}
		if entry.On(st.Switches, entry.K8AutoEffective) {
			// Shown by the tool results themselves: one is enough (5.5), and K8 makes it effective at once.
			e.Status = entry.Stable
		}
	}
	return e, ""
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func squash(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }
