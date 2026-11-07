// Package schema holds what goes to the model and what must come back, for each of the four calls: the triage
// tag, the daily digest, the weekly consolidation and the daily memory pass. Inputs are JSON so the prompt text stays fixed and
// versioned; answers are JSON held to a schema, then to the verify package's checks.
package schema

import "encoding/json"

// Versions of the prompts. A change to a prompt's wording bumps its version; the ledger keys every call by it, so
// the same chat-day is digested again only when the prompt actually changed (and raw answers stay comparable).
const (
	TriagePrompt = "triage/2"
	DailyPrompt  = "daily/3"
	WeeklyPrompt = "weekly/2"
	MemoryPrompt = "memory/1"
)

// Ruleset is the version of the deterministic checks: raising it lets stored raw answers be verified again
// without calling the model (Revalidate).
const Ruleset = "rules/3"

// MsgLine is one message as the model sees it.
type MsgLine struct {
	N       int    `json:"n"`
	Time    string `json:"time,omitempty"`
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}

// TriageInput: the head and tail of a chat-day the rules could not place.
type TriageInput struct {
	Chat     string    `json:"chat"`
	Kind     string    `json:"kind"`
	Source   string    `json:"source"`
	Members  int       `json:"members"`
	OneLine  string    `json:"one_line,omitempty"`
	Owner    []string  `json:"owner_names"`
	Lines    []MsgLine `json:"lines"`
	Skipped  int       `json:"skipped_lines"`
	Language string    `json:"language"`
}

// TriageOutput is the tag.
type TriageOutput struct {
	Admit      string     `json:"admit"`    // include | digest_only | drop
	Category   string     `json:"category"` // see TriageCategories
	Importance string     `json:"importance"`
	Relevance  int        `json:"relevance"`
	Reason     string     `json:"reason"`
	Evidence   []Evidence `json:"evidence"`
}

// TriageCategories: mail first (the user wants mail sorted and marketing dropped), then the rest.
var TriageCategories = []string{"personal", "work_action", "work_fyi", "transactional", "notification", "marketing", "newsletter", "social", "other"}

// Evidence is a citation: the line number (as kb.day gives it) and the words quoted from that line.
type Evidence struct {
	N       int    `json:"n"`
	Quote   string `json:"quote"`
	Speaker string `json:"speaker,omitempty"`
}

// Entity is a known entity the model may refer to by id instead of by name.
type Entity struct {
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
}

// Predicate is one relation type the model may use.
type Predicate struct {
	Name    string   `json:"name"`
	Meaning string   `json:"meaning"`
	Subject []string `json:"subject"`
	Object  []string `json:"object"`
}

// DailyInput is one admitted chat-day (or the window of it the triage admitted).
type DailyInput struct {
	Day        string      `json:"day"`
	Chat       string      `json:"chat"`
	ChatName   string      `json:"chat_name"`
	Kind       string      `json:"kind"`
	Owner      []string    `json:"owner_names"`
	Lines      []MsgLine   `json:"lines"`
	Known      []Entity    `json:"known_entities"`
	Types      []string    `json:"node_types"`
	Predicates []Predicate `json:"predicates"`
	Pinned     []string    `json:"confirmed_facts,omitempty"`
	Language   string      `json:"language"`
	Graph      bool        `json:"graph"` // extract mentions and assertions too
}

// DailyOutput is the digest: what happened, then the three kinds of items and the graph's raw material.
type DailyOutput struct {
	Summary    []Point     `json:"summary"`
	Facts      []Item      `json:"facts"`
	Practices  []Item      `json:"practices"`
	Knowledge  []Item      `json:"knowledge"`
	Mentions   []Mention   `json:"mentions"`
	Assertions []Assertion `json:"assertions"`
}

// Point is a line of the summary with its citations.
type Point struct {
	Text     string     `json:"text"`
	Evidence []Evidence `json:"evidence"`
}

// Item is a fact, a practice or a piece of knowledge.
type Item struct {
	Claim      string     `json:"claim"`
	About      string     `json:"about,omitempty"` // the entity it is about: a known id or a name in the text
	When       string     `json:"when,omitempty"`  // YYYY-MM-DD the claim is dated to, if any
	Scope      string     `json:"scope,omitempty"` // practices: who or what it applies to
	Evidence   []Evidence `json:"evidence"`
	Confidence string     `json:"confidence"` // high | medium | low
}

// Mention is a name for an entity as written in a line.
type Mention struct {
	Surface string `json:"surface"`
	Type    string `json:"type"`
	N       int    `json:"n"`
	ID      string `json:"id,omitempty"` // a known entity's id, if it is one
}

// Assertion is a typed relation between two entities, with its citations.
type Assertion struct {
	S          string            `json:"s"`
	P          string            `json:"p"`
	O          string            `json:"o"`
	Attrs      map[string]string `json:"attrs,omitempty"`
	When       string            `json:"when,omitempty"`
	Evidence   []Evidence        `json:"evidence"`
	Confidence string            `json:"confidence"`
}

// WeeklyInput: one cluster of items that the deterministic pass found alike, for the model to say which say the
// same thing, which contradict, and how the survivors read best.
type WeeklyInput struct {
	Week     string        `json:"week"`
	Class    string        `json:"class"`
	Items    []WeeklyEntry `json:"items"`
	Language string        `json:"language"`
}

// WeeklyEntry is an item as the weekly pass shows it.
type WeeklyEntry struct {
	ID       string   `json:"id"`
	Claim    string   `json:"claim"`
	About    string   `json:"about,omitempty"`
	Days     []string `json:"days"`
	Quotes   []string `json:"quotes"`
	Sources  int      `json:"sources"`
	Status   string   `json:"status"`
	Pinned   bool     `json:"pinned,omitempty"`
	Valid    string   `json:"valid_from,omitempty"`
	Category string   `json:"category,omitempty"`
}

// WeeklyOutput is the model's verdict on a cluster.
type WeeklyOutput struct {
	Merges    []Merge    `json:"merges"`
	Conflicts []Conflict `json:"conflicts"`
}

// Merge: these items say the same; keep one, with an optional better wording drawn from them.
type Merge struct {
	Keep  string   `json:"keep"`
	Drop  []string `json:"drop"`
	Claim string   `json:"claim,omitempty"`
}

// Conflict: these items cannot all be true. The model does not pick: the user does (or later evidence).
type Conflict struct {
	IDs    []string `json:"ids"`
	Reason string   `json:"reason"`
}

// MemoryInput is one admitted unit for the memory pass (design chapter 6): what the user said, judged sentence by
// sentence.
type MemoryInput struct {
	Day          string        `json:"day"`
	Chat         string        `json:"chat"`
	ChatName     string        `json:"chat_name"`
	Kind         string        `json:"kind"`
	Members      int           `json:"members"`
	Owner        []string      `json:"owner_names"`
	Lines        []MsgLine     `json:"lines"`
	Known        []MemoryKnown `json:"known_entries"`
	Waiting      []MemoryKnown `json:"waiting_suggestions"`
	ThingTypes   []string      `json:"thing_types"`
	LifeAreas    []string      `json:"life_areas"`
	SelfSubnodes []string      `json:"self_subnodes"`
	Language     string        `json:"language"`
}

// MemoryKnown is an entry as the memory pass shows it.
type MemoryKnown struct {
	ID      string `json:"id"`
	Text    string `json:"text"`
	Layer   string `json:"layer,omitempty"`
	Subject string `json:"subject,omitempty"`
	When    string `json:"when,omitempty"`  // waiting suggestions: the day
	Quote   string `json:"quote,omitempty"` // waiting suggestions: what was said
}

// MemoryOutput is the memory pass's answer.
type MemoryOutput struct {
	Conclusions []MemoryConclusion `json:"conclusions"`
	FollowUps   []MemoryFollowUp   `json:"follow_ups"`
	Confirms    []MemoryVote       `json:"confirms"`
	Rejects     []MemoryVote       `json:"rejects"`
}

// MemoryConclusion is a conclusion or a fact, with every sentence it rests on tagged.
type MemoryConclusion struct {
	Match        string           `json:"match,omitempty"`
	Text         string           `json:"text"`
	Sentences    []string         `json:"sentences,omitempty"`
	Subject      string           `json:"subject"`
	Layer        string           `json:"layer"`
	Descriptive  bool             `json:"descriptive,omitempty"`
	FactCategory string           `json:"fact_category,omitempty"`
	ThingType    string           `json:"thing_type,omitempty"`
	LifeAreas    []string         `json:"life_areas,omitempty"`
	SelfSubnode  string           `json:"self_subnode,omitempty"`
	Owner        string           `json:"owner,omitempty"`
	Global       bool             `json:"global,omitempty"`
	Hard         bool             `json:"hard,omitempty"`
	Prohibition  bool             `json:"prohibition,omitempty"`
	OneOff       bool             `json:"one_off,omitempty"`
	Private      []MemoryPrivate  `json:"private_sentences,omitempty"`
	Contradicts  string           `json:"contradicts,omitempty"`
	Evidence     []MemoryEvidence `json:"evidence"`
}

// MemoryPrivate marks a sentence private (rule C).
type MemoryPrivate struct {
	I        int    `json:"i"`
	Category string `json:"category"`
}

// MemoryEvidence is a citation with rule D's tags.
type MemoryEvidence struct {
	N                int    `json:"n"`
	Quote            string `json:"quote"`
	Speaker          string `json:"speaker,omitempty"`
	Scene            string `json:"scene"`
	Expression       string `json:"expression"`
	HoldsStance      bool   `json:"holds_stance"`
	Stance           string `json:"stance"`
	ProposalQuestion bool   `json:"proposal_question,omitempty"`
	DescriptiveValue string `json:"descriptive_value,omitempty"`
	Attitude         string `json:"attitude,omitempty"`
	Support          string `json:"support"`
	LongTerm         bool   `json:"long_term,omitempty"`
}

// MemoryFollowUp: a waiting suggestion carried out (stated, artifact) or only agreed to (verbal).
type MemoryFollowUp struct {
	Entry string `json:"entry"`
	Kind  string `json:"kind"`
	N     int    `json:"n"`
	Quote string `json:"quote"`
}

// MemoryVote is the user confirming or rejecting a known entry.
type MemoryVote struct {
	Entry string `json:"entry"`
	N     int    `json:"n"`
	Quote string `json:"quote"`
}

// Must marshals v, for inputs that cannot fail.
func Must(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
