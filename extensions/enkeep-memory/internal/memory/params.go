package memory

// Params are every number the memory model uses (docs/extensions/enkeep-memory.md, "Memory"). They are the first
// version, set from the fifth trial replay; keep them here and nowhere else, so a retune is one change.
type Params struct {
	// Source weights (design 6.18 §1): the start of a new entry and what each independent occurrence adds.
	High float64 `json:"high"` // 60: a correction of the AI, a stated requirement or stance in a serious setting, a confirmation
	Mid  float64 `json:"mid"`  // 35: the user's ordinary words: a suggestion, a description, a complaint, an opinion
	Low  float64 `json:"low"`  // 15: someone else's words, an AI summary, an old import without quotes

	// Facts start elsewhere (6.18 §1): one plain statement is enough.
	FactQuote  float64 `json:"fact_quote"`  // 50: stated in someone's own words
	FactAI     float64 `json:"fact_ai"`     // 25: an AI summary, or a line that does not state it
	FactLegacy float64 `json:"fact_legacy"` // 15: an old import
	Intent     float64 `json:"intent"`      // 35: a proposal put as a question

	// Accumulation (6.18 §2): new = old + weight × k × (1 − old/100).
	RepeatWeekFactor float64 `json:"repeat_week_factor"` // 0.5: k for an occurrence in a week the entry was already seen in (1 otherwise)

	// Decay (6.18 §3).
	GraceDays       int     `json:"grace_days"`         // 7: no decay this long after the last occurrence
	HalfLifeDays    float64 `json:"half_life_days"`     // 14: half-life = this × (1 + weeks seen)
	MaxHalfLifeDays float64 `json:"max_half_life_days"` // 84
	LoserHalfLife   float64 `json:"loser_half_life"`    // 0.25: an entry that lost a contradiction decays this much faster, without grace

	// Lines (6.18 §4).
	ActiveLine          float64 `json:"active_line"`              // 50: guidelines, experiences, descriptive attributes, facts
	PrincipleLine       float64 `json:"principle_line"`           // 70
	DormantLine         float64 `json:"dormant_line"`             // 15: below it an entry is dormant (or, after losing, obsolete)
	PrincipleWeeks      int     `json:"principle_weeks"`          // 3
	PrincipleSettings   int     `json:"principle_settings"`       // 2: kinds of matter or settings a principle's evidence spans
	MidActivationWeeks  int     `json:"mid_activation_weeks"`     // 2: weeks a guideline without a high-band source needs
	DescriptiveWeeks    int     `json:"descriptive_weeks"`        // 2
	SuggestionBWeeks    int     `json:"suggestion_b_weeks"`       // 2: a suggestion repeated in this many weeks satisfies rule B
	ImportDormantScore  float64 `json:"import_dormant_score"`     // 14: an entry dormant in enkeep is imported just under the dormant line
	FollowUpWindowDays  int     `json:"follow_up_window_days"`    // 30: how long a suggestion waits for a follow-through
	ResidentPrinciples  int     `json:"resident_principles"`      // 25 at most
	ResidentPrincipleCh int     `json:"resident_principle_chars"` // 1500 characters
	ResidentHardCh      int     `json:"resident_hard_chars"`      // 2000 characters
}

// DefaultParams are the values the design settles on.
func DefaultParams() Params {
	return Params{
		High: 60, Mid: 35, Low: 15,
		FactQuote: 50, FactAI: 25, FactLegacy: 15, Intent: 35,
		RepeatWeekFactor: 0.5,
		GraceDays:        7, HalfLifeDays: 14, MaxHalfLifeDays: 84, LoserHalfLife: 0.25,
		ActiveLine: 50, PrincipleLine: 70, DormantLine: 15,
		PrincipleWeeks: 3, PrincipleSettings: 2, MidActivationWeeks: 2, DescriptiveWeeks: 2, SuggestionBWeeks: 2,
		ImportDormantScore: 14, FollowUpWindowDays: 30,
		ResidentPrinciples: 25, ResidentPrincipleCh: 1500, ResidentHardCh: 2000,
	}
}

// Weight is the number for a band.
func (p Params) Weight(b Band) float64 {
	switch b {
	case BandHigh:
		return p.High
	case BandMid:
		return p.Mid
	case BandLow:
		return p.Low
	case BandFactQuote:
		return p.FactQuote
	case BandFactAI:
		return p.FactAI
	case BandFactLegacy:
		return p.FactLegacy
	case BandIntent:
		return p.Intent
	}
	return 0
}
