package memory

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// Status is an entry as of one day: its confidence, its state and the layer in force.
type Status struct {
	AsOf        string       `json:"as_of"`
	Score       float64      `json:"score"`
	State       State        `json:"state"`
	Layer       Layer        `json:"layer"`              // the layer in force: rule B, one-off and small-talk conversions applied
	Descriptive bool         `json:"descriptive"`        // in force as a descriptive attribute (small talk converted it, or it was one)
	Category    FactCategory `json:"category,omitempty"` // for facts in force: intent when only proposal questions back it
	Count       int          `json:"count"`              // independent occurrences
	Weeks       []string     `json:"weeks,omitempty"`    // ISO weeks with an occurrence
	FirstSeen   string       `json:"first_seen,omitempty"`
	LastSeen    string       `json:"last_seen,omitempty"`
	RuleB       bool         `json:"rule_b"`              // something in its evidence lets it be a guideline
	RuleBBy     string       `json:"rule_b_by,omitempty"` // sentence | weeks | confirm | follow_up | migration
	HighSource  bool         `json:"high_source"`
	ActiveSince string       `json:"active_since,omitempty"`
	Floor       bool         `json:"floor,omitempty"` // a "don't" or hard rule in force: decay stops at the active line
	Lost        bool         `json:"lost,omitempty"`  // lost a contradiction: not injected
	Held        bool         `json:"held,omitempty"`  // the newer side of a contradiction, not ahead yet
	Vetoed      bool         `json:"vetoed,omitempty"`
	VetoHits    int          `json:"veto_hits,omitempty"` // occurrences after a rejection, ignored and only counted
	FollowUps   int          `json:"follow_ups,omitempty"`
	VerbalOnly  int          `json:"verbal_only,omitempty"` // follow-throughs that were only words, ignored
	Awaiting    bool         `json:"awaiting,omitempty"`    // a suggestion waiting for a follow-through
	Retired     bool         `json:"retired,omitempty"`     // imported as obsolete: enkeep's own confirmations do not bring it back
}

const dayLayout = "2006-01-02"

func parseDay(s string) time.Time {
	t, _ := time.Parse(dayLayout, s)
	return t
}

func daysBetween(a, b string) float64 {
	return parseDay(b).Sub(parseDay(a)).Hours() / 24
}

func addDay(s string, n int) string { return parseDay(s).AddDate(0, 0, n).Format(dayLayout) }

func isoWeek(s string) string {
	y, w := parseDay(s).ISOWeek()
	return fmt.Sprintf("%d-W%02d", y, w)
}

// occurrence is one independent occurrence: every sentence of one (day, source).
type occurrence struct {
	day      string
	weight   float64
	band     Band
	ruleB    bool
	userB    bool
	oneOff   bool
	suggest  bool
	settings []string
	imported bool
	fallback bool // the low registration of an entry whose sentences all failed to support it
}

// mode is how the entry's sentences are weighed, from all its evidence up to the day (6.21 §3, "整条转换").
type mode struct {
	target   Target
	layer    Layer
	category FactCategory
	descr    bool
	chatLow  bool // a requirement on the AI said only in small talk: an experience, its sentences at the low band
	dropped  bool
}

func entryMode(e *Entry, ev []Evidence) mode {
	m := mode{target: TargetOf(e), layer: e.Layer, category: e.FactCategory, descr: e.Descriptive}
	if e.Subject == SubjectTeam || e.Subject == SubjectOther {
		// Team rules and other people's views are facts on the org, project or person (6.7).
		m.target, m.layer = TargetFact, Fact
		if m.category == "" {
			m.category = Opinion
		}
		return m
	}
	if m.target != TargetLayers {
		return m
	}
	user, describe, intent, descrValue := 0, 0, 0, false
	for _, x := range ev {
		if x.Speaker != SpeakerUser || x.Tags.Scene == "" {
			continue
		}
		user++
		switch GradeSentence(TargetLayers, SpeakerUser, x.Tags).Route {
		case RouteDescribe:
			describe++
			if x.Tags.DescriptiveValue != "" || x.Tags.Attitude != "" {
				descrValue = true
			}
		case RouteIntent:
			intent++
		}
	}
	switch {
	case user == 0:
	case describe == user && e.Subject == SubjectAI:
		m.layer, m.chatLow = Experience, true
	case describe == user && descrValue:
		m.target, m.layer, m.descr, m.category = TargetDescriptive, Fact, true, Attribute
	case describe == user:
		m.dropped = true
	case intent == user:
		m.target, m.layer, m.category = TargetFact, Fact, Intent
	}
	return m
}

func normQuote(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), "")
}

// occurrences groups the evidence into independent occurrences: one per (day, source), and one sentence copied
// word for word into another session the same day is the same occurrence (6.18 §2).
func occurrences(e *Entry, m mode, ev []Evidence, p Params) []occurrence {
	type key struct{ day, src string }
	idx := map[key]int{}
	quoteAt := map[string]int{}
	var out []occurrence
	var unsupportedDay string
	for _, x := range ev {
		g := gradeEvidence(m, x)
		if g.Unsupported && (unsupportedDay == "" || x.Day < unsupportedDay) {
			unsupportedDay = x.Day
		}
		w := p.Weight(g.Band)
		if m.chatLow && x.Speaker == SpeakerUser && w == 0 && g.Route == RouteDescribe {
			g.Band, w = BandLow, p.Low
		}
		if w == 0 {
			continue
		}
		k := key{x.Day, x.Source}
		i, ok := idx[k]
		if q := normQuote(x.Quote); !ok && q != "" {
			if j, dup := quoteAt[x.Day+"\x00"+q]; dup {
				i, ok = j, true
			}
		}
		if !ok {
			out = append(out, occurrence{day: x.Day, imported: x.Imported})
			i = len(out) - 1
			idx[k] = i
		}
		if q := normQuote(x.Quote); q != "" {
			quoteAt[x.Day+"\x00"+q] = i
		}
		o := &out[i]
		if w > o.weight {
			o.weight, o.band = w, g.Band
		}
		o.ruleB = o.ruleB || g.RuleB
		o.userB = o.userB || (g.RuleB && x.Speaker == SpeakerUser)
		o.suggest = o.suggest || g.Suggestion
		o.oneOff = o.oneOff || g.OneOff
		if s := x.Tags.ThingType; s != "" {
			o.settings = append(o.settings, s)
		} else if x.Tags.Scene != "" {
			o.settings = append(o.settings, string(x.Tags.Scene))
		}
	}
	if len(out) == 0 && unsupportedDay != "" {
		// Everything it rests on fails to support it: it exists, registered once as an AI summary (6.21 §3).
		b := BandLow
		if m.target == TargetFact {
			b = BandFactAI
		}
		out = append(out, occurrence{day: unsupportedDay, weight: p.Weight(b), band: b, fallback: true})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].day < out[j].day })
	return out
}

// gradeEvidence weighs a sentence; evidence without tags (not judged: an imported quote, an enkeep violation
// without a quote) keeps the band of its kind.
func gradeEvidence(m mode, x Evidence) Grade {
	if x.Tags.Scene == "" {
		switch {
		case x.Violation:
			if m.target == TargetFact {
				return Grade{Band: BandFactQuote}
			}
			return Grade{Band: BandHigh}
		case x.Speaker == SpeakerUser:
			if m.target == TargetFact {
				return Grade{Band: BandFactQuote}
			}
			return Grade{Band: BandMid}
		}
	}
	return GradeSentence(m.target, x.Speaker, x.Tags)
}

func halfLife(weeks int, p Params) float64 {
	return math.Min(p.HalfLifeDays*float64(1+weeks), p.MaxHalfLifeDays)
}

func add(s, w, k float64) float64 { return s + w*k*(1-s/100) }

// Evaluate replays an entry's evidence and events day by day up to asOf and returns its status then.
// held is whether a contradiction currently keeps it from becoming active (the book decides that).
func Evaluate(e *Entry, asOf string, p Params) Status {
	var ev []Evidence
	for _, x := range e.Evidence {
		if x.Day <= asOf {
			ev = append(ev, x)
		}
	}
	m := entryMode(e, ev)
	occ := occurrences(e, m, ev, p)
	var events []EntryEvent
	for _, x := range e.Events {
		if x.Day <= asOf {
			events = append(events, x)
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].Day < events[j].Day })

	st := Status{AsOf: asOf, State: Candidate, Layer: m.layer, Descriptive: m.descr, Category: m.category}
	if m.dropped {
		st.State = Dropped
		return st
	}
	decays := !(m.layer == Fact && !m.descr)
	var (
		score       float64 // the score on day `anchor`
		anchor      string
		graceEnd    string
		lostDay     string
		weeks       = map[string]bool{}
		settings    = map[string]bool{}
		suggestWks  = map[string]bool{}
		maxBand     float64
		userB       bool
		exempt      bool // enkeep's confirmed rule: rule B by migration (6.18 §7)
		confirmed   bool
		everActive  bool
		obsolete    bool
		lastSuggest string
	)
	value := func(day string) float64 {
		if !decays || anchor == "" || score == 0 || (e.ImportDay != "" && day <= e.ImportDay) {
			return score // history before the import day accumulates without decay (6.18 §7)
		}
		start := anchor
		if graceEnd > start {
			start = graceEnd
		}
		hl := halfLife(len(weeks), p)
		if lostDay != "" {
			hl *= p.LoserHalfLife
		}
		if e.ImportDay > start {
			start = e.ImportDay
		}
		d := daysBetween(start, day)
		if d <= 0 {
			return score
		}
		v := score * math.Pow(0.5, d/hl)
		if st.Floor && lostDay == "" && v < p.ActiveLine {
			v = math.Min(score, p.ActiveLine)
		}
		return v
	}
	// set moves the score to v at day.
	set := func(day string, v float64) { score, anchor = v, day }
	line := func() float64 {
		if st.Layer == Principle {
			return p.PrincipleLine
		}
		return p.ActiveLine
	}
	layerNow := func(day string) {
		l := m.layer
		oneOff := e.OneOff || (len(occ) > 0 && occ[0].oneOff)
		// A one-off instruction becomes a guideline when the same meaning comes again on another occasion (6.3).
		again := oneOff && st.Count >= 2
		ruleB := userB || exempt || confirmed || again || st.RuleBBy == "follow_up" || len(suggestWks) >= p.SuggestionBWeeks
		switch {
		case userB:
			st.RuleBBy = "sentence"
		case again:
			st.RuleBBy = "again"
		case exempt:
			st.RuleBBy = "migration"
		case confirmed:
			st.RuleBBy = "confirm"
		case st.RuleBBy == "follow_up":
		case len(suggestWks) >= p.SuggestionBWeeks:
			st.RuleBBy = "weeks"
		}
		st.RuleB = ruleB
		if l == Principle || l == Guideline {
			if !ruleB || (oneOff && st.Count < 2 && !confirmed && !exempt) {
				l = Experience
			}
		}
		if l == Principle && !confirmed {
			if !(value(day) >= p.PrincipleLine && len(weeks) >= p.PrincipleWeeks && len(settings) >= p.PrincipleSettings && userB) {
				l = Guideline
			}
		}
		st.Layer = l
		st.Floor = everActive && e.Use == UseFollow && (e.Hard || e.Prohibition) && (l == Guideline || l == Principle)
	}
	canActivate := func(v float64) bool {
		if st.Held {
			return false
		}
		switch {
		case st.Layer == Principle:
			return v >= p.PrincipleLine
		case m.descr:
			return v >= p.ActiveLine && len(weeks) >= p.DescriptiveWeeks
		case st.Layer == Fact:
			return v >= p.ActiveLine
		}
		return v >= p.ActiveLine && (st.HighSource || len(weeks) >= p.MidActivationWeeks)
	}
	settle := func(day string) {
		if obsolete {
			st.State = Obsolete
			return
		}
		v := value(day)
		switch {
		case lostDay != "" && v < p.DormantLine:
			st.State, obsolete = Obsolete, true
		case st.State == Active && v < p.DormantLine:
			st.State = Dormant
		case st.State == Active:
		case st.Count > 0 && v < p.DormantLine:
			st.State = Dormant
		case canActivate(v):
			st.State = Active
			if !everActive {
				everActive = true
				st.ActiveSince = day
			}
		}
	}

	// The timeline: occurrences and events, day by day. Within a day: the import, evidence, follow-throughs,
	// confirmations, then rejections, losses and holds.
	type step struct {
		day string
		ord int
		occ *occurrence
		ev  *EntryEvent
	}
	var steps []step
	for i := range occ {
		steps = append(steps, step{day: occ[i].day, ord: 1, occ: &occ[i]})
	}
	order := map[EventKind]int{EvImport: 0, EvFollowUp: 2, EvConfirm: 3, EvReject: 4, EvSuperseded: 4, EvLost: 5, EvHeld: 6, EvReleased: 6}
	for i := range events {
		steps = append(steps, step{day: events[i].Day, ord: order[events[i].Kind], ev: &events[i]})
	}
	sort.SliceStable(steps, func(i, j int) bool {
		if steps[i].day != steps[j].day {
			return steps[i].day < steps[j].day
		}
		return steps[i].ord < steps[j].ord
	})
	for i, s := range steps {
		day := s.day
		if s.occ != nil {
			o := s.occ
			if st.Vetoed {
				st.VetoHits++
				continue
			}
			if obsolete {
				// rejected, superseded, retired at import, or lost and gone: new evidence does not bring it back
				continue
			}
			v := value(day)
			if st.State == Active && v < p.DormantLine || st.State == Dormant {
				st.State = Candidate // dormant, and new evidence: back to candidate, from the current score
			}
			k := 1.0
			w := isoWeek(day)
			if weeks[w] {
				k = p.RepeatWeekFactor
			}
			set(day, add(v, o.weight, k))
			if !o.imported {
				graceEnd = addDay(day, p.GraceDays)
			}
			weeks[w] = true
			for _, x := range o.settings {
				settings[x] = true
			}
			st.Count++
			if st.FirstSeen == "" {
				st.FirstSeen = day
			}
			st.LastSeen = day
			st.HighSource = st.HighSource || o.band == BandHigh
			userB = userB || o.userB
			if o.suggest {
				suggestWks[w] = true
				lastSuggest = day
			}
			if o.weight > maxBand {
				maxBand = o.weight
			}
		} else {
			x := s.ev
			switch x.Kind {
			case EvImport:
				graceEnd = addDay(day, p.GraceDays)
				switch x.Status {
				case EnkeepConfirmed:
					exempt = e.Layer != Fact
					layerNow(day)
					set(day, math.Max(value(day), line()))
					st.State, everActive = Active, true
					if st.ActiveSince == "" {
						st.ActiveSince = day
					}
				case EnkeepDormant:
					set(day, math.Min(value(day), p.ImportDormantScore))
					st.State = Dormant
				case EnkeepRetired:
					st.Retired, obsolete = true, true
				case EnkeepRejected:
					set(day, 0)
					st.Vetoed, obsolete = true, true
				}
			case EvFollowUp:
				if x.Follow != FollowStated && x.Follow != FollowArtifact {
					st.VerbalOnly++
					break
				}
				if lastSuggest == "" || lastSuggest > day || maxBand >= p.High || obsolete {
					break
				}
				// Backfill: what the serious setting would have given, less what it got; no occurrence, no new week.
				set(day, add(value(day), p.High-maxBand, 1))
				maxBand = p.High
				st.FollowUps++
				if !(e.OneOff || (len(occ) > 0 && occ[0].oneOff)) {
					st.RuleBBy = "follow_up"
				}
			case EvConfirm:
				if st.Retired && x.Imported {
					break // enkeep's own confirmations do not bring back what it had retired
				}
				st.Vetoed, obsolete, st.Retired = false, false, false
				lostDay = ""
				confirmed = true
				w := isoWeek(day)
				k := 1.0
				if weeks[w] {
					k = p.RepeatWeekFactor
				}
				v := add(value(day), p.High, k)
				weeks[w] = true
				st.Count++
				st.HighSource = true
				st.LastSeen = maxDay(st.LastSeen, day)
				if st.FirstSeen == "" {
					st.FirstSeen = day
				}
				layerNow(day)
				set(day, math.Max(v, line()))
				graceEnd = addDay(day, p.GraceDays)
				st.State, everActive = Active, true
				if st.ActiveSince == "" {
					st.ActiveSince = day
				}
			case EvReject:
				set(day, 0)
				st.Vetoed, obsolete = true, true
				st.State = Obsolete
			case EvSuperseded:
				obsolete = true
				st.State = Obsolete
			case EvLost:
				if lostDay == "" && !obsolete {
					set(day, value(day))
					lostDay, graceEnd = day, day
					if m.layer == Fact && !m.descr {
						obsolete = true // a fact that lost is replaced at once
					}
				}
			case EvHeld:
				st.Held = true
			case EvReleased:
				st.Held = false
			}
		}
		if i+1 == len(steps) || steps[i+1].day != day {
			layerNow(day)
			settle(day)
		}
	}
	layerNow(asOf)
	settle(asOf)
	st.Score = math.Round(value(asOf)*10) / 10
	st.Lost = lostDay != ""
	for w := range weeks {
		st.Weeks = append(st.Weeks, w)
	}
	slices.Sort(st.Weeks)
	st.Awaiting = lastSuggest != "" && maxBand < p.High && !obsolete && daysBetween(lastSuggest, asOf) <= float64(p.FollowUpWindowDays)
	return st
}

func maxDay(a, b string) string {
	if a > b {
		return a
	}
	return b
}
