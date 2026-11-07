// Package verify is the deterministic check of every model answer: each citation must point at a line that was
// shown, the quote must be in that line, the line must be the right person's, and dates must come from the lines.
// An item that fails a rejection check is thrown away (kept in the rejection log with its code); one that only
// raises flags is kept but never applied without a person looking at it.
package verify

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
)

// Rejection codes: the item is not kept.
const (
	Schema              = "SCHEMA"                 // required field missing, unknown type or relation, bad value
	NoEvidence          = "NO_EVIDENCE"            // no citation at all
	AnchorNotFound      = "ANCHOR_NOT_FOUND"       // the cited line number is not a line of that chat-day
	OutOfScope          = "OUT_OF_SCOPE"           // the line exists but was not shown (outside the admitted window, or private)
	QuoteNotFound       = "QUOTE_NOT_FOUND"        // the quote is not in the cited line
	QuoteWrongLine      = "QUOTE_WRONG_LINE"       // the quote is in another line of the window, not the cited one
	QuoteWrongSpeaker   = "QUOTE_WRONG_SPEAKER"    // the citation names a speaker the line is not by
	SpeakerMismatch     = "SPEAKER_MISMATCH"       // someone decided / promised something on another person's words
	EntityNotInEvidence = "ENTITY_NOT_IN_EVIDENCE" // neither end of a relation is named in the lines it cites
)

// Flag codes: the item is kept, marked, and never applied automatically.
const (
	DateMismatch  = "DATE_MISMATCH"  // a date in the claim is neither the day of the evidence nor written in it
	SelfAmbiguous = "SELF_AMBIGUOUS" // the claim is about the owner, who neither spoke nor was named that day
	Partial       = "PARTIAL_QUOTES" // a quote skips words ("……")
	LongQuote     = "LONG_QUOTE"     // a quote over MaxQuote characters (kept cut to it)
	FromBot       = "QUOTE_FROM_BOT" // evidence is an automated message
	LowConfidence = "LOW_CONFIDENCE" // the model said low
	SingleSource  = "SINGLE_SOURCE"  // one citation only, for a change to a high-impact field (set by proposals)
	ConflictsPin  = "CONFLICTS_PINNED"
)

// RejectCodes and FlagCodes, in order, for reports.
var (
	RejectCodes = []string{Schema, NoEvidence, AnchorNotFound, OutOfScope, QuoteNotFound, QuoteWrongLine, QuoteWrongSpeaker, SpeakerMismatch, EntityNotInEvidence}
	FlagCodes   = []string{DateMismatch, SelfAmbiguous, Partial, LongQuote, FromBot, LowConfidence, SingleSource, ConflictsPin}
)

// MaxQuote is the longest quote kept: evidence is a pointer, not a copy of the conversation.
const MaxQuote = 200

// Window is one chat-day as the model saw it.
type Window struct {
	Chat     string
	Day      string
	Kind     string // dm | group | bot | mail
	All      map[int]norm.Line
	Shown    map[int]bool
	Owner    []string // the owner's names (from kb.owner and the configuration)
	BotNames []string // speakers known to be automated
}

// OwnerSpoke says whether the owner wrote in the window.
func (w *Window) OwnerSpoke() bool {
	for n := range w.Shown {
		if w.All[n].Speaker == norm.Me {
			return true
		}
	}
	return false
}

// OwnerNamed says whether one of the owner's names appears in a shown line.
func (w *Window) OwnerNamed() bool {
	for n := range w.Shown {
		for _, o := range w.Owner {
			if norm.Contains(w.All[n].Body, o) {
				return true
			}
		}
	}
	return false
}

// IsOwner says whether a name is the owner.
func (w *Window) IsOwner(name string) bool {
	if name == norm.Me || strings.EqualFold(name, "me") || name == "我自己" {
		return true
	}
	q := norm.Q(name)
	for _, o := range w.Owner {
		if q != "" && norm.Q(o) == q {
			return true
		}
	}
	return false
}

func (w *Window) isBot(l norm.Line) bool {
	if l.Speaker == norm.Me {
		return false
	}
	if w.Kind == "bot" {
		return true
	}
	for _, b := range w.BotNames {
		if norm.Q(b) == norm.Q(l.Speaker) {
			return true
		}
	}
	s := strings.ToLower(l.Speaker)
	return strings.Contains(s, "bot") || strings.Contains(s, "机器人") || strings.Contains(s, "系统消息") || strings.Contains(s, "Contributor") || strings.Contains(s, "no-reply")
}

// Evidence checks citations and returns them anchored, or the code that rejects them, and any flags.
func (w *Window) Evidence(evs []schema.Evidence) (out []store.Evidence, reject string, flags []string) {
	if len(evs) == 0 {
		return nil, NoEvidence, nil
	}
	for _, e := range evs {
		l, ok := w.All[e.N]
		if !ok {
			return nil, AnchorNotFound, nil
		}
		if !w.Shown[e.N] {
			return nil, OutOfScope, nil
		}
		quote := strings.TrimSpace(e.Quote)
		hit, partial := norm.Match(quote, l.Body)
		if !hit {
			for n := range w.Shown {
				if n != e.N {
					if ok, _ := norm.Match(quote, w.All[n].Body); ok {
						return nil, QuoteWrongLine, nil
					}
				}
			}
			return nil, QuoteNotFound, nil
		}
		if e.Speaker != "" && norm.Q(e.Speaker) != norm.Q(l.Speaker) && !(w.IsOwner(e.Speaker) && l.Speaker == norm.Me) {
			return nil, QuoteWrongSpeaker, nil
		}
		if partial {
			flags = addFlag(flags, Partial)
		}
		if utf8.RuneCountInString(quote) > MaxQuote {
			flags = addFlag(flags, LongQuote)
			quote = string([]rune(quote)[:MaxQuote])
		}
		if w.isBot(l) {
			flags = addFlag(flags, FromBot)
		}
		out = append(out, store.Evidence{Chat: w.Chat, Day: w.Day, N: e.N, Speaker: l.Speaker, Quote: quote})
	}
	return out, "", flags
}

// Item checks a fact, practice or piece of knowledge.
func (w *Window) Item(it schema.Item) (ev []store.Evidence, reject string, flags []string) {
	claim := strings.TrimSpace(it.Claim)
	if claim == "" || utf8.RuneCountInString(claim) > 160 || !slices.Contains([]string{"high", "medium", "low"}, it.Confidence) {
		return nil, Schema, nil
	}
	if it.When != "" && !isoDay.MatchString(it.When) {
		return nil, Schema, nil
	}
	ev, reject, flags = w.Evidence(it.Evidence)
	if reject != "" {
		return nil, reject, nil
	}
	if it.Confidence == "low" {
		flags = addFlag(flags, LowConfidence)
	}
	if w.datesOff(claim, it.When, ev) {
		flags = addFlag(flags, DateMismatch)
	}
	if w.aboutOwner(claim, it.About) && !w.OwnerSpoke() && !w.OwnerNamed() {
		flags = addFlag(flags, SelfAmbiguous)
	}
	return ev, "", flags
}

// SpeakerBound are the relations whose subject must be the person whose words are cited: nobody decides, promises
// or assigns on someone else's line.
var SpeakerBound = []string{"decided", "committed", "assigned"}

// Assertion checks a relation. names maps each end to the names it goes by (the surface written, plus aliases of
// the entity it resolved to); isPerson says whether the subject is a person.
func (w *Window) Assertion(a schema.Assertion, sNames, oNames []string, sIsPerson bool) (ev []store.Evidence, reject string, flags []string) {
	if strings.TrimSpace(a.S) == "" || strings.TrimSpace(a.O) == "" || !slices.Contains([]string{"high", "medium", "low"}, a.Confidence) {
		return nil, Schema, nil
	}
	ev, reject, flags = w.Evidence(a.Evidence)
	if reject != "" {
		return nil, reject, nil
	}
	named := func(names []string) bool {
		for _, e := range ev {
			l := w.All[e.N]
			for _, n := range names {
				if w.IsOwner(n) && (l.Speaker == norm.Me || w.ownerIn(l.Body)) {
					return true
				}
				if norm.Contains(l.Body, n) || norm.Q(l.Speaker) == norm.Q(n) {
					return true
				}
			}
		}
		return false
	}
	if !named(sNames) && !named(oNames) {
		return nil, EntityNotInEvidence, nil
	}
	if slices.Contains(SpeakerBound, a.P) && sIsPerson {
		bySubject := false
		for _, e := range ev {
			l := w.All[e.N]
			for _, n := range sNames {
				if norm.Q(l.Speaker) == norm.Q(n) || (w.IsOwner(n) && l.Speaker == norm.Me) {
					bySubject = true
				}
			}
		}
		if !bySubject {
			return nil, SpeakerMismatch, nil
		}
	}
	if a.Confidence == "low" {
		flags = addFlag(flags, LowConfidence)
	}
	if w.datesOff("", a.When, ev) {
		flags = addFlag(flags, DateMismatch)
	}
	return ev, "", flags
}

func (w *Window) ownerIn(s string) bool {
	for _, o := range w.Owner {
		if norm.Contains(s, o) {
			return true
		}
	}
	return false
}

func (w *Window) aboutOwner(claim, about string) bool {
	if about != "" && w.IsOwner(about) {
		return true
	}
	if strings.Contains(claim, norm.Me) {
		return true
	}
	return w.ownerIn(claim)
}

var (
	isoDay   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	anyDate  = regexp.MustCompile(`\d{4}-\d{1,2}-\d{1,2}|\d{1,2}月\d{1,2}[日号]|\d{1,2}/\d{1,2}`)
	relative = regexp.MustCompile(`(?i)今天|明天|后天|昨天|前天|下周|下下周|本周|这周|周[一二三四五六日天]|星期|月底|月初|下个月|tomorrow|today|yesterday|tonight|next (week|month|monday|tuesday|wednesday|thursday|friday)|this (week|friday|monday)|monday|tuesday|wednesday|thursday|friday|end of (the )?(week|month)`)
)

// datesOff: a date in the claim, or the item's own date, must be the evidence day, or be written (as a date or a
// relative day) in the cited lines. Processing time must never become event time.
func (w *Window) datesOff(claim, when string, ev []store.Evidence) bool {
	var text strings.Builder
	for _, e := range ev {
		text.WriteString(w.All[e.N].Body)
		text.WriteString("\n")
	}
	cited := text.String()
	if when != "" && when != w.Day {
		if !anyDate.MatchString(cited) && !relative.MatchString(cited) {
			return true
		}
	}
	for _, d := range anyDate.FindAllString(claim, -1) {
		if d == w.Day || strings.Contains(norm.Q(cited), norm.Q(d)) {
			continue
		}
		// "next Wednesday" can become a date, but only one near the day it was said.
		if relative.MatchString(cited) && near(d, w.Day, 14) {
			continue
		}
		return true
	}
	if when != "" && when != w.Day && !anyDate.MatchString(cited) && !near(when, w.Day, 14) {
		return true
	}
	return false
}

// near: an ISO date within days of day (dates in other forms count as near: they cannot be placed).
func near(date, day string, days int) bool {
	a, err1 := time.Parse("2006-01-02", date)
	b, err2 := time.Parse("2006-01-02", day)
	if err1 != nil || err2 != nil {
		return true
	}
	d := a.Sub(b)
	if d < 0 {
		d = -d
	}
	return d <= time.Duration(days)*24*time.Hour
}

func addFlag(flags []string, f string) []string {
	if slices.Contains(flags, f) {
		return flags
	}
	return append(flags, f)
}
