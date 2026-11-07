package memory

// Band is the weight class a sentence falls in (6.18 §1), or none.
type Band string

const (
	BandNone       Band = ""
	BandLow        Band = "low"         // 15
	BandMid        Band = "mid"         // 35
	BandHigh       Band = "high"        // 60
	BandFactQuote  Band = "fact_quote"  // 50
	BandFactAI     Band = "fact_ai"     // 25
	BandFactLegacy Band = "fact_legacy" // 15
	BandIntent     Band = "intent"      // 35
)

// Route is where a sentence's weight goes instead of the entry's three layers.
type Route string

const (
	RouteLayers   Route = ""         // to the entry as it is
	RouteDescribe Route = "describe" // small talk and jokes: evidence for a descriptive attribute only
	RouteIntent   Route = "intent"   // a proposal put as a question: an intent, retrieval only
)

// Target is what kind of entry a sentence is weighed for: the matrix differs (6.21 §2, §3).
type Target string

const (
	TargetLayers      Target = "layers"      // a principle, a guideline or an experience
	TargetDescriptive Target = "descriptive" // a descriptive attribute
	TargetFact        Target = "fact"        // any other fact
)

// Grade is what rule D makes of one sentence for one entry.
type Grade struct {
	Band Band
	// RuleB: this sentence lets the entry be a guideline (6.3, rule B, judged by stance since the fourth revision).
	RuleB bool
	Route Route
	// OneOff: an assignment or a remark on one case: an experience until the same meaning comes again.
	OneOff bool
	// Suggestion: a suggestion or an assignment, waiting for a follow-through (6.21 §4) or a second week.
	Suggestion bool
	// Unsupported: it did not count because it does not support the entry (enkeep attached it wrongly, or it
	// speaks of something else). An entry whose every sentence is unsupported is registered once at the low band.
	Unsupported bool
}

func (t Tags) serious() bool {
	return t.Scene == SceneReview || t.Scene == SceneWork || t.Scene == SceneCorrect
}

// hasStance: the sentence expresses a requirement or a position (rule B). Not the wording: "我是在和你讨论我的判断，
// 不是改你的结论" has a position as much as "以后先给方案再动手"; "太难用了" has none.
func (t Tags) hasStance() bool {
	return t.HoldsStance && (t.Stance == StanceRequirement || t.Stance == StancePosition)
}

// GradeSentence weighs one sentence for one entry: rule D's matrix (6.21 §2), as revised on 11-07 (fourth revision:
// a correction counts by its stance, not by whether it has a normative word).
func GradeSentence(target Target, sp Speaker, t Tags) Grade {
	switch sp {
	case SpeakerOther:
		if target == TargetFact {
			return Grade{Band: BandFactQuote}
		}
		return Grade{Band: BandLow}
	case SpeakerAI:
		if target == TargetFact {
			return Grade{Band: BandFactAI}
		}
		return Grade{Band: BandLow}
	case SpeakerLegacy:
		if target == TargetFact {
			return Grade{Band: BandFactLegacy}
		}
		return Grade{Band: BandLow}
	}
	switch target {
	case TargetFact:
		return gradeFact(t)
	case TargetDescriptive:
		return gradeDescriptive(t)
	}
	return gradeLayers(t)
}

// gradeLayers: the matrix for principles, guidelines and experiences.
func gradeLayers(t Tags) Grade {
	if t.Support == SupportNone {
		return Grade{Unsupported: true}
	}
	if t.Scene == SceneChat {
		// Small talk adds nothing to the three layers; what says who the user is goes to a descriptive attribute,
		// a proposal question to an intent.
		if t.Expression == ExprQuestion && t.ProposalQuestion {
			return Grade{Route: RouteIntent}
		}
		return Grade{Route: RouteDescribe}
	}
	if t.Expression == ExprQuestion && !t.HoldsStance && t.Scene != SceneCorrect {
		if t.ProposalQuestion {
			return Grade{Route: RouteIntent}
		}
		return Grade{}
	}
	g := Grade{}
	switch t.Scene {
	case SceneCorrect:
		switch t.Expression {
		case ExprClaim:
			// A correction of the AI is a high source whether it says "以后都先查证" or "我是在和你讨论我的判断，不是改你
			// 的结论"; with a stance it is rule B, like any other sentence. No special threshold.
			g.Band, g.RuleB = BandHigh, t.hasStance()
		case ExprSuggest:
			g.Band, g.Suggestion = BandMid, true
		case ExprQuestion:
			if t.HoldsStance { // a rhetorical question that corrects: "这是你查过的？"
				g.Band, g.RuleB = BandHigh, t.hasStance()
			}
		case ExprVent:
			if t.HoldsStance { // "网页全是假的": high; a requirement behind it makes it rule B, plain venting does not
				g.Band, g.RuleB = BandHigh, t.hasStance()
			}
		case ExprJoke:
			if t.HoldsStance || t.Attitude != "" {
				g.Band = BandMid
			}
		case ExprAside:
			g.Band = BandLow
		}
	case SceneReview, SceneWork:
		switch t.Expression {
		case ExprClaim:
			if t.hasStance() {
				g.Band, g.RuleB = BandHigh, true
			} else {
				g.Band = BandMid // a plain report in a discussion: the user's ordinary words
			}
		case ExprSuggest:
			g.Band, g.Suggestion = BandMid, true
		case ExprQuestion:
			if t.ProposalQuestion {
				return Grade{Route: RouteIntent}
			}
		case ExprVent:
			if t.HoldsStance {
				g.Band, g.RuleB = BandMid, t.hasStance()
			}
		case ExprAside:
			g.Band = BandLow
		}
	case SceneAssign:
		switch t.Expression {
		case ExprClaim:
			if t.LongTerm && t.hasStance() {
				g.Band, g.RuleB = BandHigh, true
			} else {
				g.Band, g.OneOff, g.Suggestion = BandMid, true, true
			}
		case ExprSuggest:
			g.Band, g.OneOff, g.Suggestion = BandMid, true, true
		case ExprQuestion:
			if t.ProposalQuestion {
				return Grade{Route: RouteIntent}
			}
		case ExprVent:
			g.Band = BandLow
		}
	}
	return g
}

// gradeDescriptive: a descriptive attribute takes any setting, small talk included.
func gradeDescriptive(t Tags) Grade {
	switch {
	case t.Support == SupportNone:
		return Grade{Unsupported: true}
	case t.Expression == ExprAside:
		return Grade{Band: BandLow}
	case t.Expression == ExprJoke:
		if t.Attitude != "" {
			return Grade{Band: BandMid} // the attitude, not the words
		}
		return Grade{}
	}
	return Grade{Band: BandMid}
}

// gradeFact: a fact rests on a line that states it, wherever it was said ("我下个月去日本" in small talk is a fact).
func gradeFact(t Tags) Grade {
	switch {
	case t.Expression == ExprJoke:
		return Grade{} // the literal words of a joke are not a fact
	case t.Expression == ExprQuestion && t.ProposalQuestion:
		return Grade{Band: BandIntent, Route: RouteIntent}
	case t.Support == SupportNone:
		return Grade{Band: BandFactAI, Unsupported: true} // a question, an instruction, or another matter
	case t.Expression == ExprQuestion && t.Support != SupportDirect:
		return Grade{Band: BandFactAI}
	}
	return Grade{Band: BandFactQuote}
}

// TargetOf is how an entry's sentences are weighed.
func TargetOf(e *Entry) Target {
	switch {
	case e.Descriptive:
		return TargetDescriptive
	case e.Layer == Fact:
		return TargetFact
	}
	return TargetLayers
}
