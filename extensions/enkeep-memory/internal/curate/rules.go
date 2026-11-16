package curate

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/schema"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

// Rejection codes of the rule gate and of the fact gate.
const (
	// ruleNotToAI: no AI answers in the unit: an instruction to a person is not a rule for the assistant.
	ruleNotToAI = "RULE_NOT_TO_AI"
	// factNoImpact: a fact with no kind, or none that says what the assistant would do differently for knowing it.
	factNoImpact = "FACT_NO_IMPACT"
	// relationEmpty: a relation that carries no information of its own (who works with whom, on nothing).
	relationEmpty = "RELATION_EMPTY"
	// The impact check (design 5.0): an entry names the moment (entry.Moments) at which the assistant acts
	// differently, the action it takes then, and the object, for the moments that are about one. One that cannot
	// is not kept.
	impactMoment = "IMPACT_MOMENT" // no moment, or one that is not in entry.Moments (or not allowed for the facet)
	impactAction = "IMPACT_ACTION" // no action, or one that says nothing the assistant does ("understand the user better")
	impactObject = "IMPACT_OBJECT" // a moment about an object (a person, project, machine, kind of task) without one
)

// A rule that is said on two days, or once as a standing rule, is in force; one not heard for this long is dormant.
const (
	ruleActiveDays  = 2
	ruleDormantDays = 90
	ruleBriefChars  = 1200
)

// minRuleText is the shortest rule (daily/7) that says anything, in characters.
const minRuleText = 8

// objectMoments are the moments that are about one object: the kind of task, the machine or tool, the person
// replied to, the thing looked up. dispatch is not among them: a way of handing work to agents often holds for every
// agent task (the replay of 2026-11 rejected only such entries for want of an object, all of them real).
var objectMoments = []string{"task_start", "use_object", "reply_person", "kb_lookup"}

// emptyActions are what an impact says when it names no action: true of every entry, so of none. They are taken out
// of the action before what is left is measured.
var emptyActions = regexp.MustCompile(`(?i)更好地|更好的|更准确地?|更贴心地?|更有针对性地?|更加|理解用户|了解用户|理解他|了解他|用户的?(偏好|习惯|需求|风格|想法)|` +
	`参考(这一?点|这条|此)?|注意(到)?(这一?点|这条|此)?|考虑(到)?(这一?点|这条|此)?|记住(这一?点|这条|此)?|牢记|知道(这一?点|这条|此)?|意识到|` +
	`提供更好的(服务|帮助|建议|支持)|个性化|` +
	`\bbetter\b|\bmore (accurately|appropriately|effectively|relevant|personali[sz]ed)\b|\bunderstand(s|ing)?\b|\bknow(s|ing)?\b|\bbe aware( of)?\b|` +
	`\bkeep(s)? (this|that|it) in mind\b|\bkeep(s)? in mind\b|\bremember(s)?( this| that)?\b|\btake(s)? (this |that |it )?into account\b|` +
	`\bconsider(s)?( this| that)?\b|\bnote(s)? that\b|\btailor(s|ed)?\b|\bpersonali[sz]e(s|d)?\b|\b(the )?user'?s? (preferences?|habits?|needs?|style)\b|` +
	`\bthe user\b|用户|ユーザー`)

// minAction is the least an action says once the empty phrases are out: CJK characters, or Latin words.
const (
	minActionHan   = 5
	minActionWords = 3
)

// impactCheck is the real check of an impact (design 5.0). part is the ⑤′ card part, when the facet is people:
// evaluations and inferred lines never reach the per-turn block, so their moment is a reply or a lookup.
func impactCheck(f entry.Facet, part string, im schema.ObsImpact, text string) string {
	moment := strings.TrimSpace(im.Moment)
	if !slices.Contains(entry.Moments, moment) {
		return impactMoment
	}
	if f == entry.People && (part == entry.PartUserEval || part == entry.PartInferred) && moment != "reply_person" && moment != "kb_lookup" {
		return impactMoment
	}
	action := strings.TrimSpace(im.Action)
	if action == "" || norm.Q(action) == norm.Q(text) || norm.Jaccard(action, text) >= 0.85 {
		return impactAction // the entry's own text again is not what the assistant does with it
	}
	rest := emptyActions.ReplaceAllString(action, " ")
	c := norm.Count(rest)
	if c.Han+c.Kana+c.Hangul < minActionHan && c.Latin+c.Cyrillic < minActionWords && (c.Han+c.Kana+c.Hangul)/2+c.Latin < minActionWords {
		return impactAction
	}
	if slices.Contains(objectMoments, moment) && strings.TrimSpace(im.Object) == "" {
		return impactObject
	}
	return ""
}

// minGroundedRule is the bigrams a rule shares with its quotes: an instruction is the owner's words made imperative, so it
// is worded more freely than a judgement is.
const minGroundedRule = 2

// factCheck says whether a fact of the current prompt has a kind, what its kind needs (an entity it is about, for
// the world kinds), and an impact that passes the real check: the moment, the action and its object (an answer to
// an earlier prompt is held to the earlier rules and never comes here). It returns the rejection code, or "".
func factCheck(it schema.Item) string {
	switch {
	case slices.Contains(schema.WorldKinds, it.Kind):
		if strings.TrimSpace(it.About) == "" {
			return factNoImpact // about whom or what: the entity it will be filed under
		}
	case !slices.Contains(schema.FactKinds, it.Kind):
		return factNoImpact
	}
	return impactCheck(entry.World, "", schema.ObsImpact{Moment: it.Moment, Action: it.Impact, Object: it.ImpactObject}, it.Claim)
}

// ingestRules checks the rules of a daily answer and keeps the ones that pass. A rule rests on the owner's own lines
// in a conversation where an AI answers; the same instruction said again joins the rule it repeats.
func (c *Curator) ingestRules(w *verify.Window, u *Unit, tier string, rs []schema.Rule, done *store.Done, run *store.Run) {
	hasAI := u.Meta.Kind == "bot"
	for _, l := range w.All {
		if l.Speaker == voice.AI {
			hasAI = true
		}
	}
	for _, r := range rs {
		rej, own, flags := c.ruleCheck(w, r, tier, hasAI)
		if rej != "" {
			c.reject(done, "rule", rej, r.Text, u.Day)
			run.Counts["rule_rejected"]++
			continue
		}
		cur := c.sameRule(r)
		if cur != nil && cur.Deleted {
			run.Counts["rule_deleted_skipped"]++
			continue // the user deleted it: it is not made again
		}
		if cur == nil {
			cur = &store.Rule{ID: "r-" + hash(norm.Q(r.Text))[:12], Text: strings.TrimSpace(r.Text), Scope: strings.TrimSpace(r.Scope), FirstSeen: u.Day}
			c.St.S.Rules[cur.ID] = cur
			run.Counts["rule_new"]++
		}
		cur.Hard = cur.Hard || r.Hard
		cur.Durable = cur.Durable || r.Durable
		if cur.Why == "" {
			cur.Why = strings.TrimSpace(r.Why)
		}
		for _, e := range own {
			if !slices.ContainsFunc(cur.Evidence, func(x store.Evidence) bool { return x.Chat == e.Chat && x.Day == e.Day && x.N == e.N }) {
				cur.Evidence = append(cur.Evidence, e)
			}
		}
		if !slices.Contains(cur.Days, u.Day) {
			cur.Days = append(cur.Days, u.Day)
			sort.Strings(cur.Days)
		}
		cur.Flags = flags
		run.Counts["rule"]++
	}
	c.settleRules(u.Day)
}

func (c *Curator) ruleCheck(w *verify.Window, r schema.Rule, tier string, hasAI bool) (string, []store.Evidence, []string) {
	n := func(s string) int { return utf8.RuneCountInString(strings.TrimSpace(s)) }
	if !judgementTier(tier) {
		return verify.TierNotAllowed, nil, nil
	}
	if n(r.Text) < minRuleText || n(r.Text) > schema.MaxRuleText || n(r.Scope) == 0 || n(r.Scope) > schema.MaxRuleScope ||
		!slices.Contains([]string{"high", "medium", "low"}, r.Confidence) {
		return verify.Schema, nil, nil
	}
	if !hasAI {
		return ruleNotToAI, nil, nil
	}
	all, rej, flags := w.Evidence(r.Evidence)
	if rej != "" {
		return rej, nil, nil
	}
	var own []store.Evidence
	said := false
	var quotes []string
	for _, e := range all {
		if e.Role == voice.RoleUser && e.Speaker == norm.Me {
			own = append(own, e)
			quotes = append(quotes, e.Quote)
			if !voice.Agreement(e.Quote) {
				said = true
			}
		}
	}
	if len(own) == 0 {
		return verify.NotAboutOwner, nil, nil
	}
	if !said {
		return verify.AssistantSource, nil, nil
	}
	if voice.Finance(r.Text+r.Why) || slices.ContainsFunc(quotes, voice.Finance) {
		return verify.PrivateFinance, nil, nil
	}
	if generic(r.Text) {
		return judgementGeneric, nil, nil
	}
	switch grounded(r.Text+r.Why+r.Scope, strings.Join(quotes, ""), minGroundedRule) {
	case groundedNo:
		return judgementUngrounded, nil, nil
	case groundedWeak:
		flags = append(flags, lowGrounding)
	}
	if r.Confidence == "low" {
		flags = append(flags, verify.LowConfidence)
	}
	return "", own, flags
}

// sameRule is the rule an instruction repeats: the same text, or one that reads almost the same in the same scope.
func (c *Curator) sameRule(r schema.Rule) *store.Rule {
	var best *store.Rule
	top := sameName
	for _, id := range sortedKeys(c.St.S.Rules) {
		x := c.St.S.Rules[id]
		if norm.Q(x.Text) == norm.Q(r.Text) {
			return x
		}
		if j := norm.Jaccard(x.Text, r.Text); j >= top && norm.Q(x.Scope) == norm.Q(r.Scope) {
			best, top = x, j
		}
	}
	return best
}

// settleRules works out every rule's days and state as of day.
func (c *Curator) settleRules(asOf string) {
	for _, r := range c.St.S.Rules {
		r.Count = len(r.Days)
		if len(r.Days) > 0 {
			r.FirstSeen, r.LastSeen = r.Days[0], r.Days[len(r.Days)-1]
		}
		switch {
		case r.Deleted:
			r.Status = "deleted"
		case r.Pinned:
			r.Status = "active"
		case r.LastSeen != "" && r.LastSeen < addDays(asOf, -ruleDormantDays):
			r.Status = "dormant"
		case r.Durable || r.Count >= ruleActiveDays:
			r.Status = "active"
		default:
			r.Status = "candidate"
		}
	}
}

// ActiveRules are the rules in force, hard first, then the most often said, then the latest.
func ActiveRules(s *store.State) []*store.Rule {
	var out []*store.Rule
	for _, r := range s.Rules {
		if r.Status == "active" && !r.Deleted {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		if a.Hard != b.Hard {
			return a.Hard
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if a.LastSeen != b.LastSeen {
			return a.LastSeen > b.LastSeen
		}
		return a.ID < b.ID
	})
	return out
}

// rulesBrief is the rules section of the brief: instructions to the assistant, within ruleBriefChars.
func (c *Curator) rulesBrief(s *store.State) string {
	var b strings.Builder
	n := 0
	for _, r := range ActiveRules(s) {
		if voice.Finance(r.Text) || !globalScope(r.Scope) {
			continue // a rule tied to one project is for that project: the page lists it, the brief does not carry it everywhere
		}
		private := false
		for _, k := range c.Cfg.PrivateKeywords {
			if k != "" && strings.Contains(r.Text, k) {
				private = true
			}
		}
		if private {
			continue
		}
		line := "- " + r.Text
		if r.Hard && (r.Durable || r.Count >= ruleActiveDays || r.Pinned) {
			line = "- 【必须】" + strings.TrimPrefix(line, "- ")
		}
		line += "\n"
		if n+utf8.RuneCountInString(line) > ruleBriefChars {
			break
		}
		n += utf8.RuneCountInString(line)
		b.WriteString(line)
	}
	if b.Len() == 0 {
		return ""
	}
	return "## 跟我共事的规矩（直接照做）\n" + b.String() + "\n"
}

// globalScope says whether a rule holds everywhere.
func globalScope(scope string) bool {
	switch norm.Q(scope) {
	case "global", "全局", "通用", "所有", "所有场景", "所有工作区", "always", "all":
		return true
	}
	return false
}

func ruleLabel(r *store.Rule) string {
	return fmt.Sprintf("%d 天 · %s", r.Count, map[string]string{"active": "生效", "candidate": "候选", "dormant": "休眠", "deleted": "已删"}[r.Status])
}
