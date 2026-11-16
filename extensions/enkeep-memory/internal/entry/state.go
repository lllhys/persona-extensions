package entry

// Contact is one person on the intersection list (5.7): someone the user has dealt with. Being on the list decides
// whom a card is made for; what goes on the card is everything resolvable to them, not only the exchanges.
type Contact struct {
	Person string `json:"person"` // "person:<kb id>": one card per person entry (sources are not merged by the hub)
	Name   string `json:"name"`
	Source string `json:"source,omitempty"` // lark, wechat, mail…
	// Ties counts the kinds of dealings: dm (days with a direct chat), group (days both spoke in one group, or
	// replied / @-mentioned each other), mail, meeting, commitment.
	Ties   map[string]int `json:"ties"`
	Score  int            `json:"score"`            // ordering: more dealings first
	Manual string         `json:"manual,omitempty"` // added | removed: the user's choice, kept over the computed one
	First  string         `json:"first"`
	Last   string         `json:"last"`
}

// PersonMerge is the user saying several person entries are one person: kept in the plugin only, never written to
// the hub's people, and undoable.
type PersonMerge struct {
	ID      string   `json:"id"`
	Persons []string `json:"persons"`
	Name    string   `json:"name,omitempty"`
	At      string   `json:"at"`
}

// Correction is one user message judged a correction of the AI (5.1), filed against an entry or as a new issue.
type Correction struct {
	ID     string `json:"id"`
	At     string `json:"at"` // RFC 3339 of the message
	Day    string `json:"day"`
	Source string `json:"source"` // S1, S2, S3
	Ref    string `json:"ref"`
	N      int    `json:"n,omitempty"`
	Seq    int    `json:"seq,omitempty"`
	Quote  string `json:"quote"`
	Prior  string `json:"prior,omitempty"` // the AI's line before it, cut short
	Entry  string `json:"entry,omitempty"` // the entry it is about (any facet; mostly ①②④⑥)
	Issue  string `json:"issue,omitempty"` // a new issue's cluster key, when no entry matched
	// Recurrence: it came after Entry was effective (more than 10 minutes after, not the same turn).
	Recurrence bool      `json:"recurrence,omitempty"`
	Verdicts   []Verdict `json:"verdicts,omitempty"`
}

// Signal kinds of friction (3.1), cheapest first.
const (
	SigCorrection = "correction" // the user corrected the AI
	SigAskInfra   = "ask_infra"  // the AI asked the user about a machine, port, account or which tool
	SigRetry      = "retry"      // errors then a retry or another way (folded error count, same tool twice)
	SigBlocked    = "blocked"    // a permission card, a refusal, an intercept notice
	SigRework     = "rework"     // a to-do reopened or reassigned, a session interrupted or timed out
	SigStuck      = "stuck"      // the agent said it is stuck
	SigRepeat     = "repeat"     // a known ② object shows up in a new error segment
	SigPraise     = "praise"     // the user approved a named approach ("this is right")
	SigExplicit   = "explicit"   // the user said "always do it this way" / "remember this"
)

// Dig is one friction signal and what came of it: the cheap screen hit, the model's call whether to dig and
// where, and the segment read. Every ② entry that came from a segment points back here through its evidence.
type Dig struct {
	ID       string   `json:"id"`
	Signal   string   `json:"signal"`
	Ref      string   `json:"ref"` // "agents/<machine>/<session>" or the assistant session
	Day      string   `json:"day"`
	At       string   `json:"at"`
	HitSeq   int      `json:"hit_seq"`
	Hit      string   `json:"hit"`                // what the screen matched (a word, a count), not the content
	Decision string   `json:"decision"`           // dig | skip | waiting_hub
	Reason   string   `json:"reason,omitempty"`   // the model's reason
	FromSeq  int      `json:"from_seq,omitempty"` // the segment read
	ThruSeq  int      `json:"through_seq,omitempty"`
	Bytes    int      `json:"bytes,omitempty"` // bytes read for it
	Entries  []string `json:"entries,omitempty"`
}

// SourceStatus is one row of the sources card (§1.7, 4.2): is it there, how much was read, and why not.
type SourceStatus struct {
	Code      string `json:"code"` // S1…S11
	Name      string `json:"name"` // the source as shown, e.g. "lark", "Persona 对话"
	Available bool   `json:"available"`
	// Waiting names the hub change it waits for (H1…H5); Missing why it is not there otherwise
	// (no_scope, not_connected, empty, error).
	Waiting  string `json:"waiting,omitempty"`
	Missing  string `json:"missing,omitempty"`
	LastRead string `json:"last_read,omitempty"`
	Units    int    `json:"units"`
	Lines    int    `json:"lines"`
	Bytes    int    `json:"bytes"`
	// Speaker attribution checks (U8): sampled lines, marked right, unresolved share, and whether the source may
	// teach people and relations from chit-chat (K16; off when < 95% or one line put on the user wrongly).
	Sampled     int     `json:"sampled,omitempty"`
	Right       int     `json:"right,omitempty"`
	Unresolved  float64 `json:"unresolved,omitempty"`
	LearnPeople bool    `json:"learn_people"`
	Note        string  `json:"note,omitempty"`
}

// SpeakerCheck is one line put to the user on the check page: is this by Who? (U8)
type SpeakerCheck struct {
	Source  string   `json:"source"` // lark, wechat, mail… (the connector)
	Ref     string   `json:"ref"`
	Day     string   `json:"day"`
	N       int      `json:"n"`
	Who     string   `json:"who"`
	Name    string   `json:"name"`
	Quote   string   `json:"quote"`
	Verdict *Verdict `json:"verdict,omitempty"`
}

// Onboarding is the first-run questions (4.3): all skippable, editable later.
type Onboarding struct {
	Shown   string   `json:"shown,omitempty"`
	Skipped bool     `json:"skipped,omitempty"`
	Use     string   `json:"use,omitempty"` // work | life | both ("" = both)
	Musts   []string `json:"musts,omitempty"`
	Focus   []string `json:"focus,omitempty"`
	At      string   `json:"at,omitempty"`
}

// ExitFile is one file the curator wrote for an exit.
type ExitFile struct {
	Path    string `json:"path"` // relative to the data directory
	Hash    string `json:"hash"`
	Bytes   int    `json:"bytes"`
	Written string `json:"written"`
	Entries int    `json:"entries"`
}

// Exits is what was written and what the hub took (6.4: read back, warn on failure).
type Exits struct {
	Files map[string]*ExitFile `json:"files"`
	// Adopted is the hub's readback per exit (A…E): the hash it uses, when, and whether it truncated.
	Adopted map[string]Adoption `json:"adopted,omitempty"`
	// Waiting: exit → the hub change it waits for (H1, H4, H5) while the hub cannot take it yet.
	Waiting  map[string]string `json:"waiting,omitempty"`
	Warnings []string          `json:"warnings,omitempty"`
}

// Adoption is the hub's answer on one exit.
type Adoption struct {
	Hash      string `json:"hash"`
	At        string `json:"at"`
	Truncated bool   `json:"truncated,omitempty"`
	// Reason: why the hub did not take the current version (throttled, too large, rejected…), when it says.
	Reason string `json:"reason,omitempty"`
	// Earliest: the earliest time the hub will take it (its 24-hour throttle, H1), when it says.
	Earliest string `json:"earliest,omitempty"`
	// State is the hub's (adopted, pending, kept, none); Current: the hub uses the file as written now.
	State   string `json:"state,omitempty"`
	Current bool   `json:"current,omitempty"`
}

// Reading is one readout (§9.2) for one period: R1…R8, F1, U1…U8. Unmeasurable when chances < 20.
type Reading struct {
	Code    string  `json:"code"`
	Period  string  `json:"period"` // the two weeks ending on this day
	Value   float64 `json:"value"`
	Num     int     `json:"num"`
	Den     int     `json:"den"`
	Measure bool    `json:"measurable"`
	Note    string  `json:"note,omitempty"`
}
