// Package schema holds what goes to the model and what must come back, for each of the four calls: the triage
// tag, the daily digest, the weekly consolidation and the daily memory pass. Inputs are JSON so the prompt text stays fixed and
// versioned; answers are JSON held to a schema, then to the verify package's checks.
package schema

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Versions of the prompts. A change to a prompt's wording bumps its version; the ledger keys every call by it, so
// the same chat-day is digested again only when the prompt actually changed (and raw answers stay comparable).
const (
	TriagePrompt = "triage/2"
	DailyPrompt  = "daily/15"
	WeeklyPrompt = "weekly/2"
	MemoryPrompt = "memory/2"
	SynthPrompt  = "synth/1"
	// RewritePrompt asks once more for wording the daily checks rejected (language, the word for the user).
	RewritePrompt = "rewrite/1"
)

// Ruleset is the version of the deterministic checks: raising it lets stored raw answers be verified again
// without calling the model (Revalidate).
const Ruleset = "rules/5"

// MsgLine is one message as the model sees it.
type MsgLine struct {
	N       int    `json:"n"`
	Time    string `json:"time,omitempty"`
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
	// Who is the line's speaker after resolution (daily/8): "self", "person:<id>", or empty when unresolved.
	Who string `json:"who,omitempty"`
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
	// Tier bounds what the unit may give (empty: tiers off, everything): own, engaged or background.
	Tier string `json:"tier,omitempty"`
	// daily/8: the unit's source code (S1…S11), its origin (behavior, self_report, handwritten) and the word for
	// the user in Language ("用户", "the user"…).
	Source   string `json:"source,omitempty"`
	Origin   string `json:"origin,omitempty"`
	UserWord string `json:"user_word,omitempty"`
}

// DailyOutput is the digest: what happened, then the three kinds of items and the graph's raw material.
type DailyOutput struct {
	Summary   []Point `json:"summary"`
	Facts     []Item  `json:"facts"`
	Practices []Item  `json:"practices"`
	Knowledge []Item  `json:"knowledge"`
	// Judgements are how the owner decides and reasons (daily/7); an answer to an earlier prompt has none.
	Judgements []Judgement `json:"judgements,omitempty"`
	// Rules are what the owner asks of the AI they work with (daily/7).
	Rules      []Rule      `json:"rules,omitempty"`
	Mentions   []Mention   `json:"mentions"`
	Assertions []Assertion `json:"assertions"`
	// Observations (v4, daily/8 and later) are the unit's action-grain entries for every facet: ① ② ③ ④ ⑤′ ⑥.
	// ⑤ world facts stay in Facts and relations in Assertions. Judgements and Rules are not asked for any more:
	// they are observations of facets judgement and interaction.
	Observations []Observation `json:"observations,omitempty"`
}

// Observation is one concrete thing seen in a unit, for one facet (entry.Facet). Fields that do not apply to the
// facet stay empty. Quotes in Evidence are verbatim; everything else is written in the target language.
type Observation struct {
	Facet string `json:"facet"`          // interaction | tools | judgement | workflow | people | persona
	Type  string `json:"type,omitempty"` // the facet's open type (fork type, task type, object kind, card dimension, profile key)
	Text  string `json:"text"`           // one sentence, in the target language
	// ① ③ ④: the situation or fork, what the user chose or does, what was not chosen, the reason, the exception.
	Situation string `json:"situation,omitempty"`
	Choice    string `json:"choice,omitempty"`
	Rejected  string `json:"rejected,omitempty"`
	Why       string `json:"why,omitempty"`
	Exception string `json:"exception,omitempty"`
	// ②: the machine, tool or runtime, what fails and how it was got around. ④: the project, when one.
	Object  string `json:"object,omitempty"`
	Pitfall string `json:"pitfall,omitempty"`
	Fix     string `json:"fix,omitempty"`
	// ⑤′: whom the card line is about (a known id or the name as written), which part of the card, and whether the
	// lines are by others talking about them (reported).
	Person     string         `json:"person,omitempty"`
	Part       string         `json:"part,omitempty"` // fact | user_eval | inferred | commitment
	Commitment *ObsCommitment `json:"commitment,omitempty"`
	Inferred   bool           `json:"inferred,omitempty"`
	Reported   bool           `json:"reported,omitempty"`
	Domain     string         `json:"domain"`              // work | life
	Sensitive  string         `json:"sensitive,omitempty"` // emotion | finance | health | family, or ""
	Scope      string         `json:"scope,omitempty"`     // global, or the project, runtime, machine or tool
	Durable    bool           `json:"durable,omitempty"`   // the user said it holds from now on ("always", "remember this")
	Impact     ObsImpact      `json:"impact"`
	Evidence   []Evidence     `json:"evidence"`
	Confidence string         `json:"confidence"` // high | medium | low
}

// ObsCommitment is a promise as the model reads it: from and to are "me" (the user) or a known id or name.
type ObsCommitment struct {
	From string `json:"from"`
	To   string `json:"to"`
	What string `json:"what"`
	Due  string `json:"due,omitempty"`
}

// ObsImpact: which assistant action changes, when, and for what (entry.Moments).
type ObsImpact struct {
	Moment string `json:"moment"`
	Action string `json:"action"`
	Object string `json:"object,omitempty"`
}

// Point is a line of the summary with its citations.
type Point struct {
	Text     string     `json:"text"`
	Evidence []Evidence `json:"evidence"`
}

// Item is a fact, a practice or a piece of knowledge.
type Item struct {
	Claim string `json:"claim"`
	About string `json:"about,omitempty"` // the entity it is about: a known id or a name in the text
	When  string `json:"when,omitempty"`  // YYYY-MM-DD the claim is dated to, if any
	Scope string `json:"scope,omitempty"` // practices: who or what it applies to
	Why   string `json:"why,omitempty"`   // the reason the owner gave for it, when they did (daily/7)
	// Facts only (daily/7): what kind of fact it is, and how knowing it would change what the assistant does.
	Kind   string `json:"kind,omitempty"`   // decision | commitment | status | evaluation | dependency
	Impact string `json:"impact,omitempty"` // one sentence: what the assistant does differently for knowing it
	// daily/8: the moment the impact is at (entry.Moments) and its object; Impact is then the action.
	Moment       string     `json:"moment,omitempty"`
	ImpactObject string     `json:"impact_object,omitempty"`
	Domain       string     `json:"domain,omitempty"`    // work | life
	Sensitive    string     `json:"sensitive,omitempty"` // emotion | finance | health | family, or ""
	Evidence     []Evidence `json:"evidence"`
	Confidence   string     `json:"confidence"` // high | medium | low
}

// Judgement dimensions: how the owner decides, thinks, reacts, pays attention and says things.
var Dimensions = []string{"决策", "思考", "反应", "注意力", "沟通"}

// Limits of a judgement's fields, in characters.
const (
	MaxJudgementClaim    = 160
	MaxJudgementTrigger  = 80
	MaxJudgementChoice   = 120
	MaxJudgementRejected = 80
	MaxJudgementWhy      = 160
)

// Judgement is one situation and what the owner chose in it, resting on the owner's own lines.
type Judgement struct {
	Claim      string     `json:"claim"`
	Trigger    string     `json:"trigger"`
	Choice     string     `json:"choice"`
	Rejected   string     `json:"rejected,omitempty"` // what was turned down or given up, when the lines show it
	Why        string     `json:"why,omitempty"`      // the owner's own reason, when given
	Dimension  string     `json:"dimension"`
	Inferred   bool       `json:"inferred,omitempty"` // the choice or the reason is the model's reading of the words, not stated
	Evidence   []Evidence `json:"evidence"`
	Confidence string     `json:"confidence"`
}

// Fact kinds: what is worth keeping about the owner's world.
var FactKinds = []string{"decision", "commitment", "evaluation", "status", "role", "relationship", "term", "dependency"}

// WorldKinds are the facts about the owner's world, which anyone's words may support (block A); the others are the
// owner's own decisions, promises and judgements (block B, the owner's words only).
var WorldKinds = []string{"status", "role", "relationship", "term", "dependency"}

// Limits of a rule's fields, in characters.
const (
	MaxRuleText  = 140
	MaxRuleScope = 40
)

// Rule is one instruction the owner gives the AI they work with, resting on the owner's own lines.
type Rule struct {
	Text       string     `json:"text"`    // an instruction to the AI, in the imperative
	Scope      string     `json:"scope"`   // "global", or the project or kind of work it is about
	Hard       bool       `json:"hard"`    // 必须 / 禁止 / 一律 / 绝不
	Durable    bool       `json:"durable"` // the owner said it holds from now on (以后 / 每次 / 别再 / 一律)
	Why        string     `json:"why,omitempty"`
	Evidence   []Evidence `json:"evidence"`
	Confidence string     `json:"confidence"`
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

// MemoryInput is one admitted unit for the memory pass (design chapter 6): the user's own turns with a few lines of
// context around each, judged sentence by sentence.
type MemoryInput struct {
	Day          string        `json:"day"`
	Chat         string        `json:"chat"`
	ChatName     string        `json:"chat_name"`
	Kind         string        `json:"kind"`
	Source       string        `json:"source,omitempty"` // lark, wechat, github, mail, persona, …: a hint only
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

// Memory categories of the legacy observation layer: how the user thinks and decides, what the user asks of
// the AI, what the user put forward or settled, and what the user said about themself.
const (
	CatCognitive   = "cognitive"
	CatInteraction = "interaction"
	CatKnowledge   = "knowledge"
	CatProfile     = "profile"
)

// MemoryCategories are the categories a memory conclusion may have.
var MemoryCategories = []string{CatCognitive, CatInteraction, CatKnowledge, CatProfile}

// MemoryAttrs are a category's attributes: dimension and pattern_hint (cognitive), itype and rule_hint
// (interaction), ktype and topic (knowledge), domain, key and value (profile).
type MemoryAttrs struct {
	Dimension   string `json:"dimension,omitempty"`
	PatternHint string `json:"pattern_hint,omitempty"`
	IType       string `json:"itype,omitempty"`
	RuleHint    string `json:"rule_hint,omitempty"`
	KType       string `json:"ktype,omitempty"`
	Topic       string `json:"topic,omitempty"`
	Domain      string `json:"domain,omitempty"`
	Key         string `json:"key,omitempty"`
	Value       string `json:"value,omitempty"`
}

// MemoryConclusion is a conclusion or a fact, with every sentence it rests on tagged.
type MemoryConclusion struct {
	Match        string           `json:"match,omitempty"`
	Category     string           `json:"category,omitempty"` // empty only in answers to memory/1
	Text         string           `json:"text"`
	Context      string           `json:"context,omitempty"` // what the user was responding to
	Attrs        MemoryAttrs      `json:"attrs,omitempty"`
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

// SynthInput is one batch of new judgements, with the patterns they may be filed under (synth/1).
type SynthInput struct {
	Week       string          `json:"week"`
	Judgements []SynthJudgment `json:"judgements"`
	Patterns   []SynthPattern  `json:"patterns"`
	Language   string          `json:"language"`
}

// SynthJudgment is a judgement as the weekly synthesis shows it.
type SynthJudgment struct {
	ID        string   `json:"id"`
	Claim     string   `json:"claim"`
	Trigger   string   `json:"trigger"`
	Choice    string   `json:"choice"`
	Rejected  string   `json:"rejected,omitempty"`
	Why       string   `json:"why,omitempty"`
	Dimension string   `json:"dimension"`
	Quotes    []string `json:"quotes"`
}

// SynthPattern is a pattern as the synthesis shows it.
type SynthPattern struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Dimension  string `json:"dimension"`
	Definition string `json:"definition"`
	Count      int    `json:"count"`
}

// SynthOutput files each judgement under a pattern.
type SynthOutput struct {
	Assignments []SynthAssignment `json:"assignments"`
}

// SynthAssignment: map (to Pattern), counter (the owner chose the opposite of Pattern in the same kind of
// situation), new (NewPattern) or none (no nameable pattern: a one-off).
type SynthAssignment struct {
	ID         string  `json:"id"`
	Action     string  `json:"action"`
	Pattern    string  `json:"pattern,omitempty"`
	NewPattern *NewPat `json:"new_pattern,omitempty"`
	Reason     string  `json:"reason"`
}

// NewPat is a pattern the model proposes.
type NewPat struct {
	Name       string `json:"name"`
	Dimension  string `json:"dimension"`
	Definition string `json:"definition"`
	Trigger    string `json:"trigger"`
	Choice     string `json:"choice"`
	NotWhen    string `json:"not_when,omitempty"` // a kind of situation where the owner chose differently
	Closest    string `json:"closest,omitempty"`
	NotBecause string `json:"not_closest_because,omitempty"`
}

// TraitInput: the patterns with evidence, and the traits there are, for the model to group (synth/1, second call).
type TraitInput struct {
	Patterns []TraitPattern `json:"patterns"`
	Traits   []TraitKnown   `json:"traits"`
	Language string         `json:"language"`
}

// TraitPattern is a pattern with a few of its quotes.
type TraitPattern struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Dimension  string   `json:"dimension"`
	Definition string   `json:"definition"`
	Count      int      `json:"count"`
	Quotes     []string `json:"quotes"`
	Trait      string   `json:"trait,omitempty"`
}

// TraitKnown is a trait as shown.
type TraitKnown struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Definition string `json:"definition"`
}

// TraitOutput merges patterns that are one behaviour worded twice, and groups patterns under traits.
type TraitOutput struct {
	Merges []PatternMerge `json:"merges"`
	Groups []TraitGroup   `json:"groups"`
}

// PatternMerge: these patterns describe the same way of choosing in the same kind of situation; keep one.
type PatternMerge struct {
	Keep       string   `json:"keep"`
	Drop       []string `json:"drop"`
	Name       string   `json:"name,omitempty"`       // a better name for the kept pattern
	Definition string   `json:"definition,omitempty"` // a better definition, from the patterns' own words
}

// TraitGroup: patterns that are one underlying trait of how the owner works (Trait set: an existing one; NewTrait:
// a new one).
type TraitGroup struct {
	Trait    string    `json:"trait,omitempty"`
	NewTrait *NewTrait `json:"new_trait,omitempty"`
	Patterns []string  `json:"patterns"`
	Reason   string    `json:"reason"`
	// Open are what the evidence leaves unsettled about the trait (at most 3).
	Open []string `json:"open_questions,omitempty"`
}

// NewTrait is a trait the model proposes.
type NewTrait struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
	Predicts   string `json:"predicts"`
	Applies    string `json:"applies,omitempty"`     // the kind of situation it holds in
	NotApplies string `json:"not_applies,omitempty"` // a kind of situation it does not
}

// RewriteInput: items whose wording the daily checks rejected (rewrite/1), to be written again once.
type RewriteInput struct {
	Language string        `json:"language"`
	UserWord string        `json:"user_word"`
	Items    []RewriteItem `json:"items"`
}

// RewriteItem is one item: its index, what was wrong (input only) and its fields, field name → text.
type RewriteItem struct {
	I       int               `json:"i"`
	Problem string            `json:"problem,omitempty"`
	Fields  map[string]string `json:"fields"`
}

// RewriteOutput is the same items, written again.
type RewriteOutput struct {
	Items []RewriteItem `json:"items"`
}

// DailyAtLeast says whether a daily prompt version ("daily/9") is n or later: answers of every version since n share
// one answer shape.
func DailyAtLeast(prompt string, n int) bool {
	v, ok := strings.CutPrefix(prompt, "daily/")
	if !ok {
		return false
	}
	i, err := strconv.Atoi(v)
	return err == nil && i >= n
}
