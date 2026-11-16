package curate

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

// Rejection codes of the judgement gate.
const (
	// judgementGeneric: the choice says nothing that predicts what the owner would do ("pays attention to quality").
	judgementGeneric = "JUDGEMENT_GENERIC"
	// judgementUngrounded: the claim, the choice and the reason share almost nothing with the words they cite.
	judgementUngrounded = "JUDGEMENT_UNGROUNDED"
)

// vague are words that make a choice sound like a trait without saying what is chosen.
var vague = []string{"注重", "重视", "强调", "倾向", "善于", "偏好", "习惯", "喜欢", "比较", "更加", "非常", "认真", "严谨", "高效", "细致", "注意",
	"pays attention", "values", "prefers", "cares about", "is careful", "emphasizes"}

// minGrounded is the bigrams the claim, choice and reason must share with the quotes: a judgement is the owner's
// own words made into a sentence, not something the model thought of.
const minGrounded = 3

// lowGrounding flags a judgement or rule worded so differently from its quotes that the overlap cannot be measured
// (another language, a long paraphrase): it is kept, marked.
const lowGrounding = "LOW_GROUNDING"

const (
	groundedYes = iota
	groundedWeak
	groundedNo
)

// grounded says whether text stands on the quotes it cites: it shares at least min character bigrams with them. Two
// texts in different scripts share none whatever they say, and a long paraphrase may share few: those are weak, not
// no. Only text in the quotes' own script that shares nothing at all is no.
func grounded(text, quotes string, min int) int {
	n := norm.Shared(text, quotes)
	switch {
	case n >= min:
		return groundedYes
	case n == 0 && cjkShare(text) >= 0.5 && cjkShare(quotes) >= 0.5:
		return groundedNo
	}
	return groundedWeak
}

// cjkShare is the share of a text's letters that are Han.
func cjkShare(s string) float64 {
	han, all := 0, 0
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			han++
			all++
		case unicode.IsLetter(r):
			all++
		}
	}
	if all == 0 {
		return 0
	}
	return float64(han) / float64(all)
}

// minChoice is the least a choice may say once the vague words are taken out, in characters.
const minChoice = 6

// generic says whether a choice is only vague words.
func generic(choice string) bool {
	q := norm.Q(choice)
	for _, v := range vague {
		q = strings.ReplaceAll(q, norm.Q(v), "")
	}
	return utf8.RuneCountInString(q) < minChoice
}

// judgementTiers are the tiers whose own lines may give judgements: a background group gives entities only.
func judgementTier(tier string) bool { return tier != triage.TierBackground }

// ingestJudgements checks the judgements of a daily answer and keeps the ones that pass. A judgement rests on the
// owner's own lines only: other people's, the assistant's, an agent's and a bot's are context and are not kept, and
// one with no line of the owner's left is thrown away.
func (c *Curator) ingestJudgements(w *verify.Window, u *Unit, tier string, js []schema.Judgement, done *store.Done, run *store.Run) {
	for _, j := range js {
		if u.SelfOff != "" { // a weak kb.owner: a group's or a mail's lines teach nothing about the user (5.0)
			c.reject(done, "judgement", selfUnverified, j.Claim, u.Day)
			run.Counts["judgement_rejected"]++
			continue
		}
		if !judgementTier(tier) {
			c.reject(done, "judgement", verify.TierNotAllowed, j.Claim, u.Day)
			run.Counts["judgement_rejected"]++
			continue
		}
		ev, rej, flags := c.judgementCheck(w, j)
		if rej != "" {
			c.reject(done, "judgement", rej, j.Claim, u.Day)
			run.Counts["judgement_rejected"]++
			continue
		}
		id := "j-" + hash(u.Ref, u.Day, norm.Q(j.Choice))[:12]
		cur := c.St.S.Judgements[id]
		if cur == nil {
			cur = &store.Judgement{ID: id, Claim: strings.TrimSpace(j.Claim), Trigger: strings.TrimSpace(j.Trigger), Choice: strings.TrimSpace(j.Choice),
				Rejected: strings.TrimSpace(j.Rejected), Why: strings.TrimSpace(j.Why), Dimension: j.Dimension, Inferred: j.Inferred, Confidence: j.Confidence,
				Chat: u.Ref, Day: u.Day, Prompt: done.Prompt, Ruleset: schema.Ruleset}
			c.St.S.Judgements[id] = cur
			run.Counts["judgement_new"]++
		}
		cur.Flags = flags
		for _, e := range ev {
			if !slices.ContainsFunc(cur.Evidence, func(x store.Evidence) bool { return x.N == e.N && x.Quote == e.Quote }) {
				cur.Evidence = append(cur.Evidence, e)
			}
		}
		if len(flags) > 0 {
			run.Counts["judgement_flagged"]++
		} else {
			run.Counts["judgement_passed"]++
		}
		run.Counts["judgement"]++
	}
}

// judgementCheck is the gate: fields in bounds, citations that check out, the owner's own words, a choice that says
// something, and a claim that stands on the words it cites. It returns the owner's lines, nothing else.
func (c *Curator) judgementCheck(w *verify.Window, j schema.Judgement) ([]store.Evidence, string, []string) {
	n := func(s string) int { return utf8.RuneCountInString(strings.TrimSpace(s)) }
	if n(j.Claim) == 0 || n(j.Claim) > schema.MaxJudgementClaim || n(j.Trigger) == 0 || n(j.Trigger) > schema.MaxJudgementTrigger ||
		n(j.Choice) == 0 || n(j.Choice) > schema.MaxJudgementChoice || n(j.Rejected) > schema.MaxJudgementRejected || n(j.Why) > schema.MaxJudgementWhy ||
		!slices.Contains(schema.Dimensions, j.Dimension) || !slices.Contains([]string{"high", "medium", "low"}, j.Confidence) {
		return nil, verify.Schema, nil
	}
	all, rej, flags := w.Evidence(j.Evidence)
	if rej != "" {
		return nil, rej, nil
	}
	var own []store.Evidence
	for _, e := range all {
		if e.Role == voice.RoleUser && e.Speaker == norm.Me {
			own = append(own, e)
		}
	}
	if len(own) == 0 {
		return nil, verify.NotAboutOwner, nil
	}
	said := false // at least one of the owner's lines says something of its own
	var quotes []string
	for _, e := range own {
		quotes = append(quotes, e.Quote)
		if !voice.Agreement(e.Quote) {
			said = true
		}
	}
	if !said {
		return nil, verify.AssistantSource, nil
	}
	text := j.Claim + " " + j.Trigger + " " + j.Choice + " " + j.Rejected + " " + j.Why
	if voice.Finance(text) || slices.ContainsFunc(quotes, voice.Finance) {
		return nil, verify.PrivateFinance, nil
	}
	if generic(j.Choice) {
		return nil, judgementGeneric, nil
	}
	switch g := grounded(j.Claim+j.Trigger+j.Choice+j.Rejected+j.Why, strings.Join(quotes, ""), minGrounded); g {
	case groundedNo:
		return nil, judgementUngrounded, nil
	case groundedWeak:
		flags = append(flags, lowGrounding)
	}
	if j.Confidence == "low" {
		flags = append(flags, verify.LowConfidence)
	}
	return own, "", flags
}
