package curate

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	srcs "github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/sources"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

// The daily pass's observations (daily/8): one prompt gives every facet's action-grain entries; each is checked here
// and what passes goes through c.Observe (entries.go). The checks (design 5.0, 3.2, 3.3, 4.1, 5.5):
//   - the citations check out (verify.Window) and each line is attributed: Unit.Who (internal/speaker), or, until
//     it is filled, the line's role ("我" is the user, a member's display name the member it uniquely names);
//   - judgement, workflow, persona and the user's asks of the AI (interaction) rest on the user's own words: a bare
//     agreement does not count, and words found only in the AI's text are not the user's;
//   - a person's card (people) rests only on lines attributed to them, the user's lines about them, and others'
//     lines naming them (reported); unattributed lines are nobody's;
//   - the user's "about me" (about/me) gives interaction, tools and persona only; the assistant's memory files
//     (memory/*) are kept as hand-written;
//   - the impact names a moment, an action and its object (rules.go impactCheck);
//   - the wording is in the target language and never speaks of the user as "I" or "owner": rewritten once, then
//     dropped;
//   - sensitive categories (K13) are dropped unless the user turned that category on; then kept, marked;
//   - no credential is ever kept: an observation that carries one in its own words is dropped, a quote that carries
//     one is kept with the credential cut out.

// Rejection codes of the observation checks (with verify's and rules.go's).
const (
	obsFacetNotAllowed = "FACET_NOT_ALLOWED"     // the unit may not give this facet (about/me: no ③④; a background group)
	obsNotSelf         = "NOT_SELF"              // ③④⑥ or a user's ask with no line of the user's own
	obsAgreementOnly   = "AGREEMENT_ONLY"        // the user's only lines are bare agreement
	obsNotToAI         = "INTERACTION_NOT_TO_AI" // ① in a unit where no AI takes part
	obsPersonUnknown   = "PERSON_UNRESOLVED"     // ⑤′ about someone who resolves to no person entry, or to the user
	obsUnattributed    = "SPEAKER_UNRESOLVED"    // no cited line is attributable (3.2)
	obsEvalNotSelf     = "EVAL_NOT_SELF"         // ⑤′ user_eval without the user's own line
	obsLanguage        = "LANGUAGE"              // the wording is not mainly in the target language, even rewritten
	obsSelfWord        = "SELF_WORD"             // the wording calls the user "I", "我", "owner"… even rewritten
	obsSensitiveOff    = "SENSITIVE_OFF"         // a K13 category the user has not turned on
)

// ObsRejectCodes lists the codes of this file in report order.
var ObsRejectCodes = []string{obsFacetNotAllowed, obsNotSelf, obsAgreementOnly, obsNotToAI, obsPersonUnknown, obsUnattributed,
	obsEvalNotSelf, obsLanguage, obsSelfWord, obsSensitiveOff, verify.SecretFound, impactMoment, impactAction, impactObject}

// Flags an entry may carry from the daily pass.
const (
	flagDurable   = "durable"   // the user said it holds from now on
	flagRewritten = "rewritten" // the wording was rewritten once for the language checks
)

// Limits of the daily calls (design 5.0: inputs cut to a conservative context window, no long context assumed).
// dailyBatchTokens is the most a daily call's lines may weigh (norm.Tokens); a unit over it is read in parts.
// With the prompt (about 4k tokens), the known entities and the answer (dailyMaxTokens), a call stays within 32k.
const (
	dailyBatchTokens = 12000
	dailyMaxTokens   = 6000
	rewriteMaxTokens = 4000
)

// dailyParts cuts an admitted unit's shown lines into parts that each fit dailyBatchTokens, in line order. A unit
// that fits is one part with no name ("").
func dailyParts(u *Unit, d triage.Decision) []struct {
	d    triage.Decision
	part string
} {
	type part = struct {
		d    triage.Decision
		part string
	}
	lines := shownLines(d)
	byN := map[int]norm.Line{}
	for _, l := range u.Lines {
		byN[l.N] = l
	}
	total := 0
	for _, n := range lines {
		l := byN[n]
		total += norm.Tokens(l.Speaker+": "+l.Body) + 4
	}
	if total <= dailyBatchTokens || len(lines) < 2 {
		return []part{{d, ""}}
	}
	var out []part
	var cur []int
	size := 0
	flush := func() {
		if len(cur) == 0 {
			return
		}
		pd := d
		pd.Shown = map[int]bool{}
		for _, n := range cur {
			pd.Shown[n] = true
		}
		out = append(out, part{pd, fmt.Sprintf("%d-%d", cur[0], cur[len(cur)-1])})
		cur, size = nil, 0
	}
	for _, n := range lines {
		l := byN[n]
		t := norm.Tokens(l.Speaker+": "+l.Body) + 4
		if size+t > dailyBatchTokens {
			flush()
		}
		cur = append(cur, n)
		size += t
	}
	flush()
	return out
}

// factGate is the daily/8 checks of a fact past its citations: no credential in its words, its K13 category on (or
// none), its wording in the target language (rewritten once already: fixWording). It returns the rejection and the
// category.
func (c *Curator) factGate(it schema.Item, ev []store.Evidence, owner []string) (string, string) {
	if slices.ContainsFunc([]string{it.Claim, it.Impact, it.About, it.ImpactObject}, verify.Secret) {
		return verify.SecretFound, ""
	}
	texts := []string{it.Claim, it.Impact}
	for _, e := range ev {
		texts = append(texts, e.Quote)
	}
	sens := sensitiveOf(it.Sensitive, texts...)
	switch {
	case sens == "?":
		return verify.Schema, ""
	case sens == entry.SensFinance && strings.TrimSpace(it.Sensitive) == "":
		// A money notice the model did not label: itemGate drops it as ever (PRIVATE_FINANCE).
		sens = ""
	case sens != "" && !entry.On(c.St.S.Switches, entry.K13Sensitive+"."+sens):
		return obsSensitiveOff, ""
	}
	return wordingCheck(factFields(&it), c.language(), owner), sens
}

// maxObsField is the longest field of an observation, in characters.
const maxObsField = 240

// Unit refs of the user's own notes and the assistant's memory files (P1's catalogs S7, S6).
const (
	refAboutMe   = "about/me"
	refMemoryPre = "memory/"
)

// unitOrigin is where a unit's entries come from.
func unitOrigin(ref string) entry.Origin {
	switch {
	case ref == refAboutMe || strings.HasPrefix(ref, "about/"):
		return entry.SelfReport
	case strings.HasPrefix(ref, refMemoryPre):
		return entry.Handwritten
	}
	return entry.Behavior
}

// unitSource is a unit's source code (design §3).
func unitCode(u *Unit) string {
	ref := u.Ref
	switch {
	case strings.HasPrefix(ref, "chats/persona-"):
		return entry.SrcAssistant
	case strings.HasPrefix(ref, "agents/"):
		return entry.SrcAgent
	case strings.HasPrefix(ref, "asks/"):
		return entry.SrcAsk
	case strings.HasPrefix(ref, "todos/"):
		return entry.SrcTodo
	case strings.HasPrefix(ref, "about/"):
		return entry.SrcAbout
	case strings.HasPrefix(ref, refMemoryPre):
		return entry.SrcMemory
	case strings.HasPrefix(ref, "recordings/"):
		return entry.SrcRecording
	}
	return entry.SrcChat
}

// aiUnit says whether an AI takes part in the unit: the assistant's and agents' sessions, ask_user, the user's notes
// and memory for the AI, a bot chat, or an assistant line in it.
func aiUnit(u *Unit) bool {
	switch unitCode(u) {
	case entry.SrcAssistant, entry.SrcAgent, entry.SrcAsk, entry.SrcAbout, entry.SrcMemory, entry.SrcTools:
		return true
	}
	if u.Meta.Kind == "bot" || u.Meta.Kind == "assistant" || u.Meta.Kind == "agent" {
		return true
	}
	return slices.ContainsFunc(u.Lines, func(l norm.Line) bool { return l.Speaker == voice.AI })
}

// agentReport: in an agent's session, the agent's own text and its folded work (sources.SpeakerAgent,
// sources.SpeakerWork). They read as "another person" to voice.Role, which knows chat speakers only, but they are
// an agent's report of what a machine did: what a ② entry may rest on.
func agentReport(u *Unit, l norm.Line) bool {
	return unitCode(u) == entry.SrcAgent && (l.Speaker == srcs.SpeakerAgent || l.Speaker == srcs.SpeakerWork)
}

// whoOf is who a line is by (3.2): Unit.Who when the speaker resolution (P4) filled it; until then the user's own
// lines ("我"), the whole of the user's notes, and another speaker whose display name names exactly one member of the
// unit. Anything else is unresolved ("").
func whoOf(u *Unit, k *known, l norm.Line, kind string) string {
	if u.Who != nil {
		return u.Who[l.N]
	}
	if unitOrigin(u.Ref) == entry.SelfReport {
		return entry.WhoSelf
	}
	switch voice.Role(l, kind) {
	case voice.RoleUser:
		return entry.WhoSelf
	case voice.RoleOther:
		if k == nil {
			return ""
		}
		var hit []string
		for _, pid := range u.Meta.People {
			if name := k.people[pid]; name != "" && norm.Q(name) == norm.Q(l.Speaker) {
				hit = append(hit, pid)
			}
		}
		if len(hit) == 1 {
			return "person:" + hit[0]
		}
	}
	return ""
}

// personOf resolves the person an observation names: a known id ("person:<id>", "people/<id>"), a member of the unit
// by name, or a name exactly one person entry goes by. "" when it cannot be told who it is.
func personOf(u *Unit, k *known, name string) string {
	name = strings.TrimSpace(name)
	if name == "" || k == nil {
		return ""
	}
	for _, p := range []string{"person:", "people/"} {
		if id, ok := strings.CutPrefix(name, p); ok {
			if k.people[id] != "" {
				return "person:" + id
			}
			return ""
		}
	}
	var hit []string
	for _, pid := range u.Meta.People {
		if n := k.people[pid]; n != "" && norm.Q(n) == norm.Q(name) {
			hit = append(hit, pid)
		}
	}
	if len(hit) == 1 {
		return "person:" + hit[0]
	}
	if len(hit) == 0 {
		// Called by the given name only ("小明" for "王小明", "Avery" for "Avery Lin"): one member of
		// the unit, else one person on the intersection list; never looked for among everyone the knowledge base
		// has, where given names repeat.
		switch id, n := byGivenName(u.Meta.People, k, name); {
		case n == 1:
			return "person:" + id
		case n > 1:
			return "" // two members go by that given name: it cannot be told which
		}
		if id, n := byGivenName(k.contacts, k, name); n == 1 {
			return "person:" + id
		}
	}
	if len(hit) == 0 {
		var refs []string
		for _, r := range k.byName[norm.Q(name)] {
			if strings.HasPrefix(r, "people/") {
				refs = append(refs, r)
			}
		}
		if len(refs) == 1 {
			return "person:" + strings.TrimPrefix(refs[0], "people/")
		}
	}
	return ""
}

// byGivenName is the one person among ids whose full name name is the given name of: a Han name without its one- or
// two-character surname (at least two characters left), or the first word of a Latin name; and how many there are
// (the id only when exactly one).
func byGivenName(ids []string, k *known, name string) (string, int) {
	if len([]rune(norm.Q(name))) < 2 {
		return "", 0
	}
	found, n := "", 0
	for _, pid := range ids {
		full := k.people[pid]
		if full == "" || pid == found || !givenNameOf(full, name) {
			continue
		}
		found, n = pid, n+1
	}
	if n != 1 {
		return "", n
	}
	return found, 1
}

func givenNameOf(full, name string) bool {
	f, n := []rune(strings.TrimSpace(full)), []rune(strings.TrimSpace(name))
	if len(n) >= 2 && allHan(f) && allHan(n) && len(f) > len(n) && len(f)-len(n) <= 2 {
		return string(f[len(f)-len(n):]) == string(n)
	}
	words := strings.Fields(full)
	return len(words) >= 2 && !allHan(n) && strings.EqualFold(words[0], strings.TrimSpace(name))
}

func allHan(rs []rune) bool {
	for _, r := range rs {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return len(rs) > 0
}

// personName is a person's canonical name, for matching it in lines.
func personName(k *known, person string) string {
	if k == nil {
		return ""
	}
	return k.people[strings.TrimPrefix(person, "person:")]
}

// obsEvidence is one checked citation with its attribution.
type obsEvidence struct {
	store.Evidence
	who string
}

func (c *Curator) attributeEvidence(u *Unit, k *known, w *verify.Window, ev []store.Evidence) []obsEvidence {
	out := make([]obsEvidence, 0, len(ev))
	for _, e := range ev {
		e.Quote = verify.Redact(e.Quote)
		out = append(out, obsEvidence{Evidence: e, who: whoOf(u, k, w.All[e.N], w.Kind)})
	}
	return out
}

func (c *Curator) entryEvidence(u *Unit, ev []obsEvidence, reported func(obsEvidence) bool) []entry.Evidence {
	src := unitCode(u)
	out := make([]entry.Evidence, 0, len(ev))
	for _, e := range ev {
		x := entry.Evidence{Source: src, Ref: u.Ref, Day: u.Day, N: e.N, Speaker: e.Speaker, Who: e.who, Role: e.Role, Quote: e.Quote}
		if reported != nil {
			x.Reported = reported(e)
		}
		out = append(out, x)
	}
	return out
}

// own are the user's own lines among the evidence: attributed to the user and written by them (not an agent's
// draft posted under their name, not the AI's). The user's notes are the user's throughout.
func own(u *Unit, ev []obsEvidence) []obsEvidence {
	var out []obsEvidence
	for _, e := range ev {
		if e.who == entry.WhoSelf && (e.Role == voice.RoleUser || unitOrigin(u.Ref) == entry.SelfReport) {
			out = append(out, e)
		}
	}
	return out
}

// selfCheck: the observation rests on the user's own words (③④⑥, the user's asks of the AI). It returns the user's
// lines (the evidence kept) or the rejection.
func selfCheck(u *Unit, ev []obsEvidence) ([]obsEvidence, string) {
	mine := own(u, ev)
	if len(mine) == 0 {
		if slices.ContainsFunc(ev, func(e obsEvidence) bool {
			return e.Role == voice.RoleAssistant || e.Role == voice.RoleAgentDraft || e.Role == voice.RoleBot
		}) {
			return nil, verify.AssistantSource // the words are only in the AI's (or an agent's, a bot's) text
		}
		return nil, obsNotSelf
	}
	if !slices.ContainsFunc(mine, func(e obsEvidence) bool { return !voice.Agreement(e.Quote) }) {
		return nil, obsAgreementOnly
	}
	return mine, ""
}

// obsFields are an observation's own words, by name: what the language checks read and the rewrite gets.
func obsFields(o *schema.Observation) map[string]*string {
	return map[string]*string{"text": &o.Text, "situation": &o.Situation, "choice": &o.Choice, "rejected": &o.Rejected,
		"why": &o.Why, "exception": &o.Exception, "pitfall": &o.Pitfall, "fix": &o.Fix, "action": &o.Impact.Action}
}

func factFields(it *schema.Item) map[string]*string {
	return map[string]*string{"claim": &it.Claim, "impact": &it.Impact}
}

// wordingCheck is the language check over fields (design 5.0): mainly in the target language, and never speaking
// of the user in the first person, as "owner", or by the user's name.
func wordingCheck(fields map[string]*string, lang string, owner []string) string {
	var all []string
	for _, name := range sortedKeys(fields) {
		if v := strings.TrimSpace(*fields[name]); v != "" {
			all = append(all, v)
		}
	}
	text := strings.Join(all, "\n")
	if !norm.InLanguage(text, lang) {
		return obsLanguage
	}
	if norm.SelfReference(text, lang) {
		return obsSelfWord
	}
	bare := norm.StripQuoted(text)
	for _, o := range voice.Names(owner) {
		if utf8.RuneCountInString(o) >= 2 && voice.Mentions(bare, []string{o}) {
			return obsSelfWord
		}
	}
	return ""
}

// rewriter asks once more for the wording of items the language checks rejected (nil: no second asking, they are
// dropped).
type rewriter func(in schema.RewriteInput) (schema.RewriteOutput, bool)

// sensitiveOf is an observation's K13 category: the model's label, or finance when the words are a money matter
// (voice.Finance) whatever the label says. "?" is a label that is none of the categories.
func sensitiveOf(label string, texts ...string) string {
	label = strings.ToLower(strings.TrimSpace(label))
	if label != "" && !slices.Contains(entry.Sensitives, label) {
		return "?"
	}
	if label == "" && slices.ContainsFunc(texts, voice.Finance) {
		return entry.SensFinance
	}
	return label
}

// scopesOf turns an observation's scope into entry scopes: global, or a project, runtime or object.
func scopesOf(f entry.Facet, scope, object string, k *known) []entry.Scope {
	scope = strings.TrimSpace(scope)
	switch q := norm.Q(scope); {
	case q == "" || globalScope(scope):
		if f == entry.Tools && strings.TrimSpace(object) != "" {
			return []entry.Scope{{Kind: entry.ScopeObject, Name: strings.TrimSpace(object)}}
		}
		return []entry.Scope{{Kind: entry.ScopeGlobal}}
	case f == entry.Tools:
		return []entry.Scope{{Kind: entry.ScopeObject, Name: scope}}
	case runtimeName(q):
		return []entry.Scope{{Kind: entry.ScopeRuntime, Name: scope}}
	case k != nil && slices.ContainsFunc(k.byName[q], func(r string) bool { return strings.HasPrefix(r, "projects/") }):
		return []entry.Scope{{Kind: entry.ScopeProject, Name: scope}}
	}
	return []entry.Scope{{Kind: entry.ScopeProject, Name: scope}}
}

// runtimeName says whether a canonical scope names an assistant or agent runtime.
func runtimeName(q string) bool {
	for _, r := range []string{"persona", "助手", "assistant", "claudecode", "claude", "codex", "cursor", "gemini", "agent", "pi"} {
		if q == r || strings.HasPrefix(q, r) {
			return true
		}
	}
	return false
}

// observation is one observation checked: the entry to keep, or the rejection.
type observation struct {
	e    *entry.Entry
	rej  string
	note string // what the rejection is about, for the log (the detail no line has)
}

// checkObservation runs every check but the wording on one observation and builds its entry.
func (c *Curator) checkObservation(w *verify.Window, u *Unit, k *known, tier string, o schema.Observation) observation {
	f := entry.Facet(strings.TrimSpace(o.Facet))
	bad := func(code string) observation { return observation{rej: code} }
	if o.Confidence == "" {
		o.Confidence = "medium" // a smaller model leaves it out now and then; the schema count still says so
	}
	if strings.TrimSpace(o.Text) == "" {
		// Some models fill a judgement's or a way's fields and leave the sentence out: the sentence is the fields'
		// own words put together (nothing added); without the fields it stays a schema rejection.
		o.Text = textFromFields(f, o, c.language())
	}
	if !f.Valid() || f == entry.World || strings.TrimSpace(o.Text) == "" || (o.Domain != entry.Work && o.Domain != entry.Life) ||
		!slices.Contains([]string{"high", "medium", "low"}, o.Confidence) {
		return bad(verify.Schema)
	}
	for _, p := range obsFields(&o) {
		if utf8.RuneCountInString(*p) > maxObsField {
			return bad(verify.Schema)
		}
	}
	origin := unitOrigin(u.Ref)
	switch {
	case origin == entry.SelfReport && f != entry.Interaction && f != entry.Tools && f != entry.Persona:
		return bad(obsFacetNotAllowed) // what the user says about how they choose is not how they choose (4.1, 4.3)
	case tier == triage.TierBackground && f != entry.People && f != entry.Persona:
		return bad(verify.TierNotAllowed)
	}
	// Secrets: never in the entry's own words.
	all := []string{o.Type, o.Object, o.Scope, o.Person}
	for _, p := range obsFields(&o) {
		all = append(all, *p)
	}
	if o.Commitment != nil {
		all = append(all, o.Commitment.What)
	}
	if slices.ContainsFunc(all, verify.Secret) {
		return bad(verify.SecretFound)
	}
	cited, rej, flags := w.Evidence(o.Evidence)
	if rej != "" {
		return bad(rej)
	}
	if p := personOf(u, k, o.Person); f == entry.People && p != "" {
		// A person card's names, numbers and times are in the lines it cites (F1, 9.5). Whom it is about is checked
		// below.
		if cited, rej = w.Supply(o.Text, cited, personName(k, p), o.Person); rej != "" {
			return observation{rej: rej, note: w.Missing}
		}
	}
	ev := c.attributeEvidence(u, k, w, cited)
	e := &entry.Entry{Facet: f, Grain: entry.Action, Type: strings.TrimSpace(o.Type), Text: strings.TrimSpace(o.Text),
		Situation: strings.TrimSpace(o.Situation), Choice: strings.TrimSpace(o.Choice), Rejected: strings.TrimSpace(o.Rejected),
		Why: strings.TrimSpace(o.Why), Exception: strings.TrimSpace(o.Exception), Object: strings.TrimSpace(o.Object),
		Pitfall: strings.TrimSpace(o.Pitfall), Fix: strings.TrimSpace(o.Fix), Inferred: o.Inferred, Domain: o.Domain,
		Impact: entry.Impact{Moment: strings.TrimSpace(o.Impact.Moment), Action: strings.TrimSpace(o.Impact.Action), Object: strings.TrimSpace(o.Impact.Object)},
		Origin: origin, OriginVersion: schema.DailyPrompt + "@" + schema.Ruleset,
		Scopes: scopesOf(f, o.Scope, o.Object, k), Flags: flags}
	var kept []obsEvidence
	var reportedOf func(obsEvidence) bool
	switch f {
	case entry.Interaction, entry.Judgement, entry.Workflow, entry.Persona:
		if f == entry.Interaction && !aiUnit(u) {
			return bad(obsNotToAI) // an instruction to a person is not how the user works with the AI
		}
		if origin == entry.Handwritten {
			kept = ev // the assistant's memory of the user: kept as written, compared and counted (S6)
			break
		}
		if kept, rej = selfCheck(u, ev); rej != "" {
			return bad(rej)
		}
		if f != entry.Persona {
			if (f == entry.Judgement || f == entry.Workflow || f == entry.Interaction) && e.Choice == "" {
				return bad(verify.Schema)
			}
			if (f == entry.Judgement || f == entry.Workflow) && generic(e.Choice) {
				return bad(judgementGeneric)
			}
			var quotes []string
			for _, x := range kept {
				quotes = append(quotes, x.Quote)
			}
			switch grounded(e.Text+e.Situation+e.Choice+e.Rejected+e.Why, strings.Join(quotes, ""), minGrounded) {
			case groundedNo:
				return bad(judgementUngrounded) // shares nothing with the user's words it cites
			case groundedWeak:
				e.Flags = append(e.Flags, lowGrounding)
			}
		}
	case entry.Tools:
		if e.Object == "" {
			return bad(verify.Schema)
		}
		// The user's word, the AI's or an agent's report of what a machine did, a bot's alert: any attributable line.
		// Another person's line that cannot be told whose it is goes.
		for _, x := range ev {
			if x.who != "" || x.Role == voice.RoleAssistant || x.Role == voice.RoleAgentDraft || x.Role == voice.RoleBot || origin != entry.Behavior ||
				agentReport(u, w.All[x.N]) {
				kept = append(kept, x)
			}
		}
		if len(kept) == 0 {
			return bad(obsUnattributed)
		}
		if !slices.ContainsFunc(kept, func(x obsEvidence) bool { return !voice.Agreement(x.Quote) }) {
			return bad(obsAgreementOnly)
		}
	case entry.People:
		person := personOf(u, k, o.Person)
		if person == "" {
			return bad(obsPersonUnknown)
		}
		part := strings.TrimSpace(o.Part)
		if part == "" {
			part = entry.PartFact
		}
		if !slices.Contains([]string{entry.PartFact, entry.PartUserEval, entry.PartInferred, entry.PartCommitment}, part) {
			return bad(verify.Schema)
		}
		e.Person, e.Part = person, part
		name := personName(k, person)
		for _, x := range ev {
			switch {
			case x.who == person, x.who == entry.WhoSelf && x.Role == voice.RoleUser:
				kept = append(kept, x) // by them, or the user speaking of them
			case x.who != "" && x.Role == voice.RoleOther && name != "" && norm.Contains(w.All[x.N].Body, name):
				kept = append(kept, x) // someone else speaking of them by name: reported
			}
		}
		if len(kept) == 0 {
			return bad(obsUnattributed)
		}
		reportedOf = func(x obsEvidence) bool { return x.who != person && x.who != entry.WhoSelf }
		e.ReportedOnly = !slices.ContainsFunc(kept, func(x obsEvidence) bool { return !reportedOf(x) })
		switch part {
		case entry.PartUserEval:
			if !slices.ContainsFunc(kept, func(x obsEvidence) bool { return x.who == entry.WhoSelf && !voice.Agreement(x.Quote) }) {
				return bad(obsEvalNotSelf)
			}
		case entry.PartInferred:
			e.Inferred = true
		case entry.PartCommitment:
			if o.Commitment == nil || strings.TrimSpace(o.Commitment.What) == "" {
				return bad(verify.Schema)
			}
			party := func(s string) string {
				if s = strings.TrimSpace(s); s == "" || strings.EqualFold(s, "me") || s == entry.WhoSelf || w.IsOwner(s) || voice.Generic(s) {
					return entry.WhoSelf
				}
				return personOf(u, k, s)
			}
			from, to := party(o.Commitment.From), party(o.Commitment.To)
			if from == "" || to == "" || (from != person && to != person) {
				return bad(verify.Schema)
			}
			if !slices.ContainsFunc(kept, func(x obsEvidence) bool { return x.who == from }) {
				return bad(verify.SpeakerMismatch) // a promise rests on the line of whoever promised
			}
			e.Commitment = &entry.Commitment{From: from, To: to, What: strings.TrimSpace(o.Commitment.What), Due: strings.TrimSpace(o.Commitment.Due)}
		}
	}
	if rej := impactCheck(f, e.Part, o.Impact, e.Text); rej != "" {
		return bad(rej)
	}
	var quotes []string
	for _, x := range kept {
		quotes = append(quotes, x.Quote)
	}
	sens := sensitiveOf(o.Sensitive, append(slices.Clone(all), quotes...)...)
	if sens == "?" {
		return bad(verify.Schema)
	}
	if sens != "" {
		if !entry.On(c.St.S.Switches, entry.K13Sensitive+"."+sens) {
			return bad(obsSensitiveOff)
		}
		e.Sensitive = sens // the user turned it on: kept for their own understanding; the exits keep it out
	}
	e.Evidence = c.entryEvidence(u, kept, reportedOf)
	if o.Durable {
		e.Flags = append(e.Flags, flagDurable)
	}
	if o.Confidence == "low" && !slices.Contains(e.Flags, verify.LowConfidence) {
		e.Flags = append(e.Flags, verify.LowConfidence)
	}
	return observation{e: e}
}

// fixWording is the language check of a daily/8 answer (design 5.0) before anything else: facts and observations
// whose wording is not mainly in the target language, or speaks of the user as "I", "owner" or by name, are asked
// for once more (fix, one call for the answer); the rewritten wording replaces the old in out. It returns which were
// rewritten ("f<i>" for facts, "o<i>" for observations). Whatever is still wrong is rejected by the checks after.
func (c *Curator) fixWording(out *schema.DailyOutput, owner []string, fix rewriter, run *store.Run) map[string]bool {
	lang := c.language()
	in := schema.RewriteInput{Language: lang, UserWord: norm.UserWord(lang)}
	add := func(i int, fields map[string]*string) {
		rej := wordingCheck(fields, lang, owner)
		if rej == "" {
			return
		}
		it := schema.RewriteItem{I: i, Problem: problemText(rej), Fields: map[string]string{}}
		for name, p := range fields {
			if *p != "" {
				it.Fields[name] = *p
			}
		}
		in.Items = append(in.Items, it)
	}
	for i := range out.Facts {
		add(-1-i, factFields(&out.Facts[i])) // facts are numbered below zero
	}
	for i := range out.Observations {
		add(i, obsFields(&out.Observations[i]))
	}
	done := map[string]bool{}
	if len(in.Items) == 0 || fix == nil {
		return done
	}
	run.Counts["wording_asked_again"] += len(in.Items)
	got, ok := fix(in)
	if !ok {
		return done
	}
	for _, it := range got.Items {
		var fields map[string]*string
		key := ""
		switch {
		case it.I < 0 && -1-it.I < len(out.Facts):
			fields, key = factFields(&out.Facts[-1-it.I]), fmt.Sprintf("f%d", -1-it.I)
		case it.I >= 0 && it.I < len(out.Observations):
			fields, key = obsFields(&out.Observations[it.I]), fmt.Sprintf("o%d", it.I)
		default:
			continue
		}
		for name, p := range fields {
			if v, ok := it.Fields[name]; ok && *p != "" && strings.TrimSpace(v) != "" {
				*p = v
			}
		}
		done[key] = true
	}
	return done
}

// ingestObservations checks a daily/8 answer's observations and keeps what passes as entries. The wording was
// asked for again already (fixWording): what is still not in the language is dropped.
func (c *Curator) ingestObservations(w *verify.Window, u *Unit, k *known, tier string, obs []schema.Observation, owner []string,
	rewritten map[string]bool, done *store.Done, run *store.Run) {
	lang := c.language()
	for i, o := range obs {
		r := c.checkObservation(w, u, k, tier, o)
		if r.rej == "" {
			checked := o
			checked.Text = r.e.Text // checkObservation may have synthesized Text from Object/Pitfall/Fix; check that text
			if rej := wordingCheck(obsFields(&checked), lang, owner); rej != "" {
				r = observation{rej: rej}
			} else if rewritten[fmt.Sprintf("o%d", i)] {
				r.e.Flags = append(r.e.Flags, flagRewritten)
				run.Counts["obs_rewritten"]++
			}
		}
		c.keepObservation(u, o, r, done, run)
	}
}

// problemText is what the rewrite is told was wrong.
func problemText(code string) string {
	switch code {
	case obsLanguage:
		return "not written in the target language"
	case obsSelfWord:
		return "refers to the user as I/me/我/owner/主人 or by name instead of user_word"
	}
	return code
}

// keepObservation records one checked observation: the entry, or its rejection.
func (c *Curator) keepObservation(u *Unit, o schema.Observation, r observation, done *store.Done, run *store.Run) {
	facet := strings.TrimSpace(o.Facet)
	if r.rej != "" {
		c.reject(done, "obs:"+facet, r.rej, withMissing(o.Text, r.note), u.Day)
		run.Counts["obs_rejected"]++
		run.Counts["obs_rejected:"+r.rej]++
		return
	}
	if id := c.Observe(r.e, run.ID); id == "" {
		run.Counts["obs_tombstoned"]++
		return
	}
	run.Counts["obs_kept"]++
	run.Counts["obs:"+facet]++
}

// worldEntry makes the ⑤ entry of a fact kept by the daily pass (daily/8): the claim, its kind, what it is about, its
// impact, and the lines it rests on with who said them.
func (c *Curator) worldEntry(u *Unit, k *known, w *verify.Window, it schema.Item, ev []store.Evidence, flags []string, sens string) *entry.Entry {
	subject := strings.TrimSpace(it.About)
	if kind, id, ok := strings.Cut(subject, "/"); ok && kind == "people" {
		subject = "person:" + id
	} else if ok && kind == "projects" {
		subject = "project:" + id
	} else if p := personOf(u, k, subject); p != "" {
		subject = p
	}
	att := c.attributeEvidence(u, k, w, ev)
	domain := it.Domain
	if domain != entry.Life {
		domain = entry.Work
	}
	return &entry.Entry{Facet: entry.World, Grain: entry.Action, Type: it.Kind, Text: strings.TrimSpace(it.Claim), Subject: subject,
		Impact:       entry.Impact{Moment: strings.TrimSpace(it.Moment), Action: strings.TrimSpace(it.Impact), Object: strings.TrimSpace(it.ImpactObject)},
		SingleSource: true, Domain: domain, Sensitive: sens, Evidence: c.entryEvidence(u, att, nil),
		Origin: unitOrigin(u.Ref), OriginVersion: schema.DailyPrompt + "@" + schema.Ruleset, Flags: flags,
		Scopes: []entry.Scope{{Kind: entry.ScopeGlobal}}}
}

// relationEntry makes the ⑤ entry of a relation the graph took: subject, predicate and target, as a class (a
// relation is the class grain of the world facet).
func (c *Curator) relationEntry(u *Unit, k *known, w *verify.Window, s, p, o, sName, oName, meaning string, ev []store.Evidence) *entry.Entry {
	ref := func(x string) string {
		switch kind, id, ok := strings.Cut(x, "/"); {
		case ok && kind == "people":
			return "person:" + id
		case ok && kind == "projects":
			return "project:" + id
		}
		return x
	}
	att := c.attributeEvidence(u, k, w, ev)
	return &entry.Entry{Facet: entry.World, Grain: entry.Class, Type: p, Text: relationText(c.language(), sName, p, oName),
		Subject: ref(s), Predicate: p, Target: ref(o), SingleSource: true, Domain: entry.Work,
		Impact:   entry.Impact{Moment: "kb_lookup", Action: meaning, Object: sName},
		Evidence: c.entryEvidence(u, att, nil), Origin: unitOrigin(u.Ref), OriginVersion: schema.DailyPrompt + "@" + schema.Ruleset,
		Scopes: []entry.Scope{{Kind: entry.ScopeGlobal}}}
}

// rewriteKey is where the rewrite of a daily answer is kept, next to the answer itself, so Revalidate replays it.
func rewriteKey(key string) string { return key + "-rw" }

// storedRewrite answers a rewrite from what was stored when the answer was first taken (Revalidate: no model).
func (c *Curator) storedRewrite(done *store.Done) rewriter {
	return func(in schema.RewriteInput) (schema.RewriteOutput, bool) {
		raw, err := c.St.Raw(rewriteKey(done.Key) + ".json")
		if err != nil {
			return schema.RewriteOutput{}, false
		}
		var out schema.RewriteOutput
		return out, json.Unmarshal(raw, &out) == nil
	}
}

// withMissing adds to a rejected claim the detail no line has, so the log says what was not found.
func withMissing(claim, missing string) string {
	if missing == "" {
		return claim
	}
	return claim + " [not in the lines: " + missing + "]"
}

// textFromFields is an observation's sentence made of its own fields when the model left the sentence out: a fork's
// situation, choice, what was turned down and why; a way's situation and steps; a machine's pitfall and fix. ""
// when the fields do not say enough.
func textFromFields(f entry.Facet, o schema.Observation, lang string) string {
	t := func(x string) string { return strings.TrimRight(strings.TrimSpace(x), "。.；;，, ") }
	sit, choice, rej, why := t(o.Situation), t(o.Choice), t(o.Rejected), t(o.Why)
	zh := !strings.HasPrefix(strings.ToLower(lang), "en")
	switch f {
	case entry.Judgement, entry.Workflow, entry.Interaction:
		if choice == "" {
			return ""
		}
		var b strings.Builder
		if zh {
			if sit != "" {
				b.WriteString(sit + "时，")
			}
			b.WriteString(choice)
			if rej != "" {
				b.WriteString("，而不是" + rej)
			}
			if why != "" {
				b.WriteString("，因为" + why)
			}
			b.WriteString("。")
		} else {
			if sit != "" {
				b.WriteString("When " + sit + ": ")
			}
			b.WriteString(choice)
			if rej != "" {
				b.WriteString(", not " + rej)
			}
			if why != "" {
				b.WriteString(", because " + why)
			}
			b.WriteString(".")
		}
		return b.String()
	case entry.Tools:
		obj, pit, fix := t(o.Object), t(o.Pitfall), t(o.Fix)
		if obj == "" || (pit == "" && fix == "") {
			return ""
		}
		parts := []string{}
		for _, x := range []string{pit, fix} {
			if x != "" {
				parts = append(parts, x)
			}
		}
		if zh {
			return obj + "：" + strings.Join(parts, "；") + "。"
		}
		return obj + ": " + strings.Join(parts, "; ") + "."
	}
	return ""
}
