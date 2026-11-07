// Package memory is the user's memory as the design's chapter 6 settles it: conclusions (principles, guidelines,
// experiences) and facts drawn from what the user said, each with its subject, use, kind of matter, life areas and
// evidence; a confidence that grows with independent occurrences and decays with time; states that follow the
// confidence; and the injection level that says how the assistant gets to see it.
//
// It is deterministic. A model tags every sentence (rule D's two dimensions and the flags beside them) and says
// what each conclusion is; everything that turns those tags into a score, a state, a layer and an injection level
// is here, replayed day by day from the evidence, so the same evidence always gives the same result.
package memory

// Layer is how widely a conclusion holds (6.4), or that it is a fact (6.8).
type Layer string

const (
	Principle  Layer = "principle"  // 原则: holds across kinds of matter
	Guideline  Layer = "guideline"  // 准则: holds within one kind of matter
	Experience Layer = "experience" // 经验: holds for one matter
	Fact       Layer = "fact"       // 事实型: attributes, decisions, events, progress, to-dos, knowledge, opinions, intents
)

// Subject is whose conclusion it is (6.7).
type Subject string

const (
	SubjectSelf  Subject = "self"           // 用户本人的结论
	SubjectAI    Subject = "ai_requirement" // 用户对 AI 的要求
	SubjectTeam  Subject = "team"           // 团队规范: a fact on the org or project
	SubjectOther Subject = "other"          // 他人观点: a fact on that person
)

// Use is what the entry is for (6.16).
type Use string

const (
	UseFollow     Use = "follow"     // 要 AI 照做
	UseUnderstand Use = "understand" // 帮 AI 理解你
	UseReference  Use = "reference"  // 备查
)

// FactCategory is the kind of a fact (6.8).
type FactCategory string

const (
	Attribute FactCategory = "attribute" // 属性 (a descriptive attribute when Descriptive is set)
	Decision  FactCategory = "decision"
	Event     FactCategory = "event"
	Progress  FactCategory = "progress"
	Todo      FactCategory = "todo"
	Knowledge FactCategory = "knowledge"
	Opinion   FactCategory = "opinion"
	Intent    FactCategory = "intent" // 意向: proposed, not settled; includes proposals put as questions
)

// Kinds of matter (6.5): the nodes principles and guidelines hang on.
var ThingTypes = []string{"决策与权衡", "判断与验证", "沟通与关系", "计划与执行", "学习与思考", "情绪与精力", "风险与边界", "表达与创作", "设计与构建"}

// Life areas (6.6): labels only, one or two per entry.
var LifeAreas = []string{"工作", "生意与副业", "AI 与个人系统", "家庭", "亲密关系", "健康", "财务", "社交", "兴趣", "修行与信念", "创作", "个人成长"}

// Self subnodes (6.9): facts about the user hang on one of these, never on the user directly.
var SelfSubnodes = []string{"身份、居留与职业", "财务", "健康", "关系", "认知风格", "表达偏好", "兴趣与信念", "价值观与状态"}

// Speaker is whose words a piece of evidence is.
type Speaker string

const (
	SpeakerUser   Speaker = "user"   // the user's own words
	SpeakerOther  Speaker = "other"  // someone else's words
	SpeakerAI     Speaker = "ai"     // an AI summary, the assistant's memory, a rule an AI wrote
	SpeakerLegacy Speaker = "legacy" // an old import (enkeep legacy_kb / legacy_index) without quotes
)

// Scene is rule D's first dimension: the setting a sentence was said in (6.21 §1).
type Scene string

const (
	SceneReview  Scene = "review"  // 评审决策
	SceneWork    Scene = "work"    // 工作讨论
	SceneAssign  Scene = "assign"  // 日常交代
	SceneCorrect Scene = "correct" // 纠正AI
	SceneChat    Scene = "chat"    // 闲聊
)

// Expression is rule D's second dimension: how it was said.
type Expression string

const (
	ExprClaim    Expression = "claim"    // 明确主张或规定
	ExprSuggest  Expression = "suggest"  // 建议
	ExprQuestion Expression = "question" // 提问或反问
	ExprVent     Expression = "vent"     // 吐槽或情绪化表达
	ExprJoke     Expression = "joke"     // 玩笑、反讽、夸张
	ExprAside    Expression = "aside"    // 顺口带过
)

// Stance is whether a sentence expresses a requirement or a position (rule B, 11-07 fourth revision: judged by
// stance, not by wording). A complaint that asks for nothing has none.
type Stance string

const (
	StanceRequirement Stance = "requirement" // 要求: asks for a way of doing things
	StancePosition    Stance = "position"    // 立场: takes a position ("I was discussing my judgement, not changing your conclusion")
	StanceNone        Stance = "none"        // 无: venting, a plain report, an aside
)

// Support is whether a sentence actually supports the entry it is attached to.
type Support string

const (
	SupportDirect  Support = "direct"
	SupportPartial Support = "partial"
	SupportNone    Support = "none"
)

// Tags are what the model says about one sentence (6.21 §1 and §6). They live on the evidence, not on the entry:
// the same sentence attached to two entries carries Support for each separately.
type Tags struct {
	Scene            Scene      `json:"scene"`
	Expression       Expression `json:"expression"`
	HoldsStance      bool       `json:"holds_stance"`                // 真持立场: the user really holds what is behind it (stance, not tone)
	Stance           Stance     `json:"stance"`                      // 立场: a requirement, a position, or none
	ProposalQuestion bool       `json:"proposal_question"`           // 提议问句: a question followed by a concrete way of doing it
	DescriptiveValue string     `json:"descriptive_value,omitempty"` // 兴趣 | 口味 | 看人看事 | 习惯 | "" (none)
	Attitude         string     `json:"attitude,omitempty"`          // behind a joke or a complaint
	Support          Support    `json:"support"`
	LongTerm         bool       `json:"long_term,omitempty"`  // an assignment that sets a standing rule ("以后都…")
	ThingType        string     `json:"thing_type,omitempty"` // the kind of matter this occurrence was about, for the principle's spread
}

// Evidence is one sentence behind an entry.
type Evidence struct {
	Day     string  `json:"day"`    // YYYY-MM-DD
	Source  string  `json:"source"` // the session, workspace, document or meeting: independence is counted per (day, source)
	Anchor  string  `json:"anchor"` // "<kind>/<id>#<line>" or an enkeep observation id
	Quote   string  `json:"quote,omitempty"`
	Speaker Speaker `json:"speaker"`
	Tags    Tags    `json:"tags"`
	// Violation: an enkeep violate event (the user caught the AI breaking the rule); its setting is a correction.
	Violation bool `json:"violation,omitempty"`
	// Imported: history carried over from enkeep, accumulated without decay up to the import day (6.18 §7).
	Imported bool `json:"imported,omitempty"`
}

// EventKind is something that happened to an entry other than evidence.
type EventKind string

const (
	EvConfirm    EventKind = "confirm"    // the user confirmed: a high occurrence, at least the line, active that day
	EvReject     EventKind = "reject"     // the user rejected: score 0, obsolete, vetoed
	EvFollowUp   EventKind = "follow_up"  // a suggestion was carried out (6.21 §4)
	EvLost       EventKind = "lost"       // lost a contradiction: no injection, faster decay
	EvHeld       EventKind = "held"       // the newer side of a contradiction, not yet ahead: cannot become active
	EvReleased   EventKind = "released"   // no longer held
	EvSuperseded EventKind = "superseded" // replaced by a new version
	EvImport     EventKind = "import"     // imported from enkeep with a state (Status)
)

// FollowKind is the evidence a follow-through rests on. Only the first two count (11-07 fourth revision).
type FollowKind string

const (
	FollowStated   FollowKind = "stated"   // the other side says it was done: "已经加到周报最前面了"
	FollowArtifact FollowKind = "artifact" // it shows in a work product: a document, a code change
	FollowVerbal   FollowKind = "verbal"   // only agreed in words: "好", "合理", "我可以试试" — never counts
)

// EnkeepStatus is an enkeep record's state at import, mapped by 6.18 §7.
type EnkeepStatus string

const (
	EnkeepConfirmed EnkeepStatus = "confirmed" // rules confirmed, patterns is_core, profile_facts confirmed
	EnkeepOpen      EnkeepStatus = "open"      // candidate, non-core, extract, proposed: judged by the score
	EnkeepDormant   EnkeepStatus = "dormant"   // dormant / latent, patterns without evidence for 8 weeks
	EnkeepRetired   EnkeepStatus = "retired"   // deprecated, merged, retired, superseded, stale
	EnkeepRejected  EnkeepStatus = "rejected"  // a user_reject in rule_events
)

// EntryEvent is one event.
type EntryEvent struct {
	Day    string       `json:"day"`
	Kind   EventKind    `json:"kind"`
	Follow FollowKind   `json:"follow,omitempty"`
	Status EnkeepStatus `json:"status,omitempty"` // EvImport
	Anchor string       `json:"anchor,omitempty"`
	Quote  string       `json:"quote,omitempty"`
	With   string       `json:"with,omitempty"` // EvLost / EvHeld: the other entry
	// Imported: carried over from enkeep's history (its own confirmations, rejections).
	Imported bool `json:"imported,omitempty"`
}

// PrivateSentence marks one sentence of an entry as private (rule C).
type PrivateSentence struct {
	I        int    `json:"i"`        // index into Entry.Sentences
	Category string `json:"category"` // health | finance | relationship | belief
}

// Entry is one conclusion or fact.
type Entry struct {
	ID        string  `json:"id"`
	Text      string  `json:"text"`
	Layer     Layer   `json:"layer"` // as extracted; the layer in force is Status.Layer
	Subject   Subject `json:"subject"`
	Use       Use     `json:"use"`
	ThingType string  `json:"thing_type,omitempty"`
	// LifeAreas: one or two (6.6); three-layer conclusions must have one.
	LifeAreas    []string     `json:"life_areas,omitempty"`
	SelfSubnode  string       `json:"self_subnode,omitempty"`
	FactCategory FactCategory `json:"fact_category,omitempty"`
	// Descriptive: how the user usually is or does (6.16): a fact-type attribute, never one of the three layers.
	Descriptive bool   `json:"descriptive,omitempty"`
	Owner       string `json:"owner,omitempty"`       // the node it belongs to: a kind of matter, an entity, a self subnode, a topic
	Global      bool   `json:"global,omitempty"`      // a rule for everything, not for one setting (hard rules are resident only then)
	Hard        bool   `json:"hard,omitempty"`        // 必须 / 禁止 / 一律 / 绝不, or enkeep is_hard
	Prohibition bool   `json:"prohibition,omitempty"` // a "don't" rule: 不要 / 别 / 不得 / 禁止 / 避免…
	OneOff      bool   `json:"one_off,omitempty"`     // a one-off instruction or a diagnosis of one case (6.3)
	// Sentences of Text, for rule C; PrivateSentences index into them.
	Sentences        []string          `json:"sentences,omitempty"`
	PrivateSentences []PrivateSentence `json:"private_sentences,omitempty"`
	// Domain: an enkeep profile domain (relationship_family, health, finance, beliefs_practice) makes it private.
	Domain   string       `json:"domain,omitempty"`
	Evidence []Evidence   `json:"evidence"`
	Events   []EntryEvent `json:"events,omitempty"`
	// Imported: the enkeep record it came from ("<table>:<id>"); ImportDay starts its grace (6.18 §7).
	Imported  string `json:"imported,omitempty"`
	ImportDay string `json:"import_day,omitempty"`

	// Status is the result of the last Evaluate, kept for the pages and the report.
	Status Status `json:"status"`
}

// State is what the confidence makes of an entry (6.18 §4).
type State string

const (
	Candidate State = "candidate" // 候选
	Active    State = "active"    // 生效
	Dormant   State = "dormant"   // 休眠
	Obsolete  State = "obsolete"  // 过时
	Dropped   State = "dropped"   // 不收: nothing it rests on counts (all small talk without descriptive value)
)
