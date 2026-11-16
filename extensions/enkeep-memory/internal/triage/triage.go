// Package triage decides, for every chat-day, whether the curator reads it, how much of it, and how urgently.
// Deterministic rules come first and cost nothing; what they cannot place goes to a small model that sees only the
// head and the tail of the day. It only decides what the curator reads: the hub's own archive is not touched.
//
// Precedence: the user's pins and blocks > the rules > the model's tag > the default.
package triage

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

// Actions.
const (
	Include    = "include"     // the whole day
	Window     = "window"      // only the lines around the owner's mentions and messages
	DigestOnly = "digest_only" // background: counted for the week, not digested daily
	Drop       = "drop"
	AI         = "ai" // the rules cannot tell: ask the model
)

// Rule ids, as in docs/design.md §3.
const (
	RPin       = "R0_pinned"
	RBlock     = "R1_blocked"
	RDM        = "R2_dm"
	RMention   = "R3_mention"
	RActive    = "R5_owner_active"
	RSilent    = "R6_silent_group" // no longer given (3.3): replaced by RIntersect / RNoIntersect
	RAllOnly   = "R8_at_all_only"
	RHubRanked = "R9_hub_ranked" // no longer given for groups (3.3)
	MBulk      = "M1_bulk_mail"
	MMine      = "M2_owner_thread"
	MOther     = "M3_other_mail"
	BBot       = "B1_bot"
	GCode      = "G1_code_review"
	RSmall     = "S0_small_volume"
	RPrivate   = "P0_private"
	DMeeting   = "D1_meeting"
	DEvent     = "D2_event"
	DDoc       = "D3_doc"
	RPersona   = "C1_persona_conversation"
	// With tiers (Config.Tiers):
	RParticipant = "R4_participant" // the owner wrote in the group within engaged_days: read whole, engaged
	RBackground  = "R7_background"  // no longer given (3.3): a quiet group is read for the lines that intersect
	RNoise       = "N1_noise"       // a group whose day is only bots, alerts or one sender's broadcast: skipped
	// RIntersect: a group the user is quiet in, read for the lines whose speaker is resolved and who has dealings with
	// the user (3.3): the user's contacts, or lines naming the user, the user's projects or contacts.
	RIntersect = "R10_intersect"
	// RNoIntersect: nothing left of such a day: every speaker unresolved, or no dealings with the user.
	RNoIntersect = "N2_no_intersection"
)

// Tiers say which layers a unit may give (docs/extensions/enkeep-memory.md "Tiers"): every layer from the user's
// own conversations; in the groups the user takes part in, only what concerns the user; from the rest, entities.
const (
	TierOwn        = "own"        // the user's own conversations: DMs, Persona, the user's mail and reviews; every layer
	TierEngaged    = "engaged"    // groups the user takes part in, mention windows, other mail, documents: what concerns the user
	TierCode       = "code"       // code review and issues: the user's own judgements and practices; no progress facts or relations
	TierBackground = "background" // groups the user keeps quiet in, muted, folded or done: entities and their relations only
)

// Config is the part of the settings the rules use.
type Config struct {
	// Tiers turns on the tiers: a unit is given one, and the background groups are read for entities instead of
	// being skipped or read whole. Off: the rules as before tiers, every unit giving every layer.
	Tiers          bool
	EngagedDays    int // the owner wrote in a group within this many days: the group is one the owner takes part in
	NoiseMembers   int // unused since 3.3: a big group is not noise for being big
	ActivityDays   int
	MentionWindow  int
	AdmitAllBelow  int // tokens: a day this small is read whole, rules aside (pins and blocks still apply)
	AIMinRelevance int
	PinChats       []string
	BlockChats     []string
	BlockSenders   []string
	BulkDomains    []string
	Private        []string // lines containing any of these are never read
}

// Chat is what the rules know about a unit of one day: a chat's messages, or a document, meeting, event or
// recording that changed that day. Every source goes through the same rules; the source and kind are features.
type Chat struct {
	Entity        string // chats | docs | meetings | events | recordings
	Meta          kbsrc.Meta
	Lines         []norm.Line
	OwnerRecently bool // the owner wrote in it within ActivityDays before this day
	// OwnerLast: how many days before this day the owner last wrote in the chat, within EngagedDays (0: not within).
	OwnerLast  int
	OwnerNames []string
	// Who is each line's resolved speaker (internal/speaker): entry.WhoSelf, "person:<id>" or "" (unresolved). Nil:
	// not resolved, and any named human speaker counts as resolved.
	Who map[int]string
	// Contacts are the people on the intersection list ("person:<id>").
	Contacts map[string]bool
	// Topics are names whose mention makes a line the user's business: the user's projects and their aliases, and the
	// names of the people on the intersection list.
	Topics []string
}

// Decision is the verdict on one chat-day.
type Decision struct {
	Chat   string
	Name   string
	Kind   string
	Rule   string
	Action string
	Tier   string // with tiers: TierOwn, TierEngaged or TierBackground (empty when tiers are off, or dropped)
	Score  float64
	Shown  map[int]bool // line numbers the curator may read
	Tokens int          // estimated tokens of the shown lines
	Reason string
}

var (
	noreply     = regexp.MustCompile(`(?i)no-?reply|do-?not-?reply|notifications?@|newsletter|mailer-daemon|marketing@|news@|bounce`)
	atAll       = regexp.MustCompile(`(?i)@(所有人|全体成员|all|everyone|here)\b?`)
	mailAddr    = regexp.MustCompile(`[A-Za-z0-9._%+-]+@([A-Za-z0-9.-]+\.[A-Za-z]{2,})`)
	defaultBulk = []string{"mailchimp.com", "sendgrid.net", "mailgun.org", "amazonses.com", "list-manage.com", "substack.com", "medium.com", "linkedin.com", "facebookmail.com", "twitter.com", "x.com", "quora.com", "pinterest.com"}
)

// Decide applies the rules to one chat-day, and with tiers gives the unit its tier.
func Decide(c Chat, cfg Config) Decision {
	d := decide(c, cfg)
	if cfg.Tiers && d.Action != Drop {
		d.Tier = tierOf(d, c)
	}
	return d
}

// tierOf is the tier of an admitted unit, by the rule that admitted it.
func tierOf(d Decision, c Chat) string {
	switch d.Rule {
	case RPin, RPersona, RDM, MMine:
		return TierOwn
	case GCode:
		return TierCode
	case BBot:
		if d.Action == Include && slices.ContainsFunc(c.Lines, func(l norm.Line) bool { return l.Speaker == norm.Me }) {
			return TierOwn // the user's own conversation with a bot
		}
		return TierBackground
	case RActive, RMention, RParticipant, RIntersect, MOther, DMeeting, DEvent, DDoc:
		return TierEngaged
	}
	return TierBackground
}

func decide(c Chat, cfg Config) Decision {
	m := c.Meta
	d := Decision{Chat: m.ID, Name: m.Name, Kind: m.Kind, Shown: map[int]bool{}}
	lines := privateFiltered(c.Lines, cfg.Private)
	all := func() {
		for _, l := range lines {
			d.Shown[l.N] = true
		}
	}
	set := func(rule, action string, score float64, reason string) Decision {
		d.Rule, d.Action, d.Score, d.Reason = rule, action, score, reason
		if action == Include || action == AI || action == DigestOnly {
			all()
		}
		d.Tokens = tokens(lines, d.Shown)
		if score > 0 && action != Drop {
			d.Score = score + float64(m.Relevance)*0.3
		}
		return d
	}
	if listed(cfg.BlockChats, m.ID, m.Name) {
		return set(RBlock, Drop, 0, "你屏蔽了这个会话")
	}
	if listed(cfg.PinChats, m.ID, m.Name) {
		return set(RPin, Include, 100, "你钉住了这个会话")
	}
	ownerToday := 0
	mentions := []int{}
	allOnly := true
	for _, l := range lines {
		if l.Speaker == norm.Me {
			ownerToday++
		}
		if mentionsOwner(l.Body, c.OwnerNames) {
			mentions = append(mentions, l.N)
		}
		if !atAll.MatchString(l.Body) {
			allOnly = false
		}
	}
	if len(lines) == 0 {
		return set(RPrivate, Drop, 0, "当天没有可读的消息")
	}
	if blockedSender(lines, cfg.BlockSenders) && ownerToday == 0 {
		return set(RBlock, Drop, 0, "发件人在你的屏蔽名单里")
	}
	switch c.Entity {
	case "meetings", "recordings":
		return set(DMeeting, Include, 65, "会议 / 录音纪要")
	case "events":
		if m.Relevance >= cfg.AIMinRelevance || m.Importance == "P0" || m.Importance == "P1" {
			return set(DEvent, Include, 55, "重要日程")
		}
		return set(DEvent, DigestOnly, 15, "普通日程：只进周级")
	case "docs":
		if m.Relevance >= 40 || m.Importance == "P0" || m.Importance == "P1" || m.Importance == "P2" {
			return set(DDoc, Include, 55, "和你相关的文档")
		}
		return set(DDoc, AI, 0, "文档，交给模型判断")
	}
	isCode := m.Source == "github" || strings.HasPrefix(m.ID, "github-") || strings.HasPrefix(m.ID, "gh-")
	persona := m.Source == "persona" || strings.HasPrefix(m.ID, "persona-")
	switch {
	case persona:
		// The user's own conversation with the assistant: the closest thing to the user thinking aloud.
		return set(RPersona, Include, 90, "你和 Persona 助手的对话")
	case isCode:
		return set(GCode, Include, 60, "和你有关的代码评审 / issue")
	case m.Kind == "dm":
		return set(RDM, Include, 80, "私聊")
	case m.Kind == "mail":
		if ownerToday > 0 || m.Mine {
			return set(MMine, Include, 70, "你参与的邮件往来")
		}
		if bulkMail(m, lines, cfg.BulkDomains) {
			return set(MBulk, Drop, 0, "群发 / 通知类邮件")
		}
		return set(MOther, AI, 0, "其他邮件，交给模型分类")
	case m.Kind == "bot":
		if ownerToday > 0 {
			return set(BBot, Include, 50, "你和机器人的对话")
		}
		return set(BBot, AI, 0, "机器人消息，交给模型判断")
	}
	// Groups.
	if cfg.Tiers {
		return groupTiered(c, cfg, lines, ownerToday, mentions, allOnly, set, &d)
	}
	if ownerToday > 0 || c.OwnerRecently {
		d2 := set(RActive, Include, 60, fmt.Sprintf("你最近 %d 天在群里发过言", cfg.ActivityDays))
		if ownerToday >= 3 {
			d2.Score += 10
		}
		return d2
	}
	if len(mentions) > 0 {
		for _, n := range mentions {
			for _, l := range lines {
				if l.N >= n-cfg.MentionWindow*2 && l.N <= n+cfg.MentionWindow*2 && within(lines, n, l.N, cfg.MentionWindow) {
					d.Shown[l.N] = true
				}
			}
		}
		return set(RMention, Window, 75, fmt.Sprintf("当天有人提到你：只读前后各 %d 条", cfg.MentionWindow))
	}
	if allOnly {
		return set(RAllOnly, DigestOnly, 20, "只有 @所有人 的通知")
	}
	return intersect(c, lines, set, &d)
}

// SmallDay: when everything admitted or undecided that day is below the threshold, read it all (light users never
// need to tune anything). Blocks, bulk mail and private lines still stay out.
func SmallDay(ds []Decision, cfg Config) bool {
	if cfg.AdmitAllBelow <= 0 {
		return false
	}
	total := 0
	for _, d := range ds {
		if d.Rule != RBlock && d.Rule != MBulk && d.Rule != RPrivate {
			total += d.Tokens
		}
	}
	return total <= cfg.AdmitAllBelow
}

// Order sorts admitted chat-days by score, most valuable first (the order the daily run digests them in).
func Order(ds []Decision) {
	sort.SliceStable(ds, func(i, j int) bool { return ds[i].Score > ds[j].Score })
}

// Head is what the model sees of an undecided day: the first 20 and the last 10 lines.
func Head(lines []norm.Line, shown map[int]bool) (head []norm.Line, skipped int) {
	var vis []norm.Line
	for _, l := range lines {
		if shown[l.N] {
			vis = append(vis, l)
		}
	}
	if len(vis) <= 30 {
		return vis, 0
	}
	return append(slices.Clone(vis[:20]), vis[len(vis)-10:]...), len(vis) - 30
}

// Fingerprint is what makes a cached tag stale: the chat's signals, not its text.
func Fingerprint(c Chat, d Decision) string {
	return fmt.Sprintf("%s|%s|%d|%s|%d", d.Rule, c.Meta.Kind, c.Meta.Relevance, c.Meta.Importance, len(c.Meta.People)/10)
}

func listed(list []string, id, name string) bool {
	for _, s := range list {
		s = strings.TrimSpace(s)
		if s != "" && (s == id || norm.Q(s) == norm.Q(name) || (len([]rune(s)) >= 4 && norm.Contains(name, s))) {
			return true
		}
	}
	return false
}

// mentionsOwner: an @-mention of one of the owner's names, or the owner's real name written out. Generic words the
// hub or the configuration may list as a name ("用户", "我", "owner") never count, whatever list they come in: they
// are in half the lines of any group (voice.Mentions).
func mentionsOwner(body string, names []string) bool { return voice.Mentions(body, names) }

func blockedSender(lines []norm.Line, block []string) bool {
	if len(block) == 0 {
		return false
	}
	for _, l := range lines {
		if l.Speaker == norm.Me {
			continue
		}
		blocked := false
		for _, b := range block {
			if b != "" && (norm.Q(b) == norm.Q(l.Speaker) || strings.Contains(strings.ToLower(l.Speaker), strings.ToLower(b))) {
				blocked = true
			}
		}
		if !blocked {
			return false
		}
	}
	return true
}

func bulkMail(m kbsrc.Meta, lines []norm.Line, extra []string) bool {
	hay := strings.ToLower(m.ID + " " + m.Name)
	for _, l := range lines {
		hay += " " + strings.ToLower(l.Speaker)
	}
	if noreply.MatchString(hay) {
		return true
	}
	domains := append(slices.Clone(defaultBulk), extra...)
	for _, a := range mailAddr.FindAllStringSubmatch(hay, -1) {
		for _, dm := range domains {
			dm = strings.ToLower(strings.TrimSpace(dm))
			if dm != "" && (a[1] == dm || strings.HasSuffix(a[1], "."+dm)) {
				return true
			}
		}
	}
	for _, dm := range extra {
		dm = strings.ToLower(strings.TrimSpace(dm))
		if dm != "" && strings.Contains(hay, dm) {
			return true
		}
	}
	return false
}

func privateFiltered(lines []norm.Line, private []string) []norm.Line {
	if len(private) == 0 {
		return lines
	}
	out := lines[:0:0]
	for _, l := range lines {
		hide := false
		for _, p := range private {
			if p != "" && norm.Contains(l.Body, p) {
				hide = true
			}
		}
		if !hide {
			out = append(out, l)
		}
	}
	return out
}

// within: line b is at most w messages away from line a (by position, not by file line number, since a message
// may span several file lines).
func within(lines []norm.Line, a, b, w int) bool {
	ia, ib := -1, -1
	for i, l := range lines {
		if l.N == a {
			ia = i
		}
		if l.N == b {
			ib = i
		}
	}
	if ia < 0 || ib < 0 {
		return false
	}
	d := ia - ib
	if d < 0 {
		d = -d
	}
	return d <= w
}

func tokens(lines []norm.Line, shown map[int]bool) int {
	t := 0
	for _, l := range lines {
		if shown[l.N] {
			t += norm.Tokens(l.Speaker+": "+l.Body) + 4
		}
	}
	return t
}

// groupTiered places a group's day with tiers on: the user's own groups are read whole; a mention is read around it;
// the rest is read for entities only, and skipped whole when the day is nothing but feeds, alerts or a big crowd's
// chatter. A group the owner muted, folded away or marked done counts as one the owner keeps quiet in, unless the
// owner wrote in it that day or was named.
func groupTiered(c Chat, cfg Config, lines []norm.Line, ownerToday int, mentions []int, allOnly bool,
	set func(rule, action string, score float64, reason string) Decision, d *Decision) Decision {
	m := c.Meta
	flagged := m.Muted || m.Collapsed || m.Done
	switch {
	case ownerToday > 0:
		out := set(RActive, Include, 60, "你今天在群里发过言")
		if ownerToday >= 3 {
			out.Score += 10
		}
		return out
	case !flagged && c.OwnerRecently:
		return set(RActive, Include, 60, fmt.Sprintf("你最近 %d 天在群里发过言", cfg.ActivityDays))
	case len(mentions) > 0:
		for _, n := range mentions {
			for _, l := range lines {
				if l.N >= n-cfg.MentionWindow*2 && l.N <= n+cfg.MentionWindow*2 && within(lines, n, l.N, cfg.MentionWindow) {
					d.Shown[l.N] = true
				}
			}
		}
		return set(RMention, Window, 75, fmt.Sprintf("当天有人提到你：只读前后各 %d 条", cfg.MentionWindow))
	case !flagged && c.OwnerLast > 0:
		return set(RParticipant, Include, 45, fmt.Sprintf("你 %d 天前在群里发过言", c.OwnerLast))
	case allOnly:
		return set(RAllOnly, DigestOnly, 20, "只有 @所有人 的通知")
	}
	return intersect(c, lines, set, d)
}

// intersect places a group's day the user kept quiet in (3.3). Chit-chat is not dropped for being chit-chat; only two
// kinds of line are: a speaker that cannot be resolved, and a line with no dealings with the user (its speaker is
// neither the user nor on the intersection list, and it names neither the user, the user's projects nor anyone on
// the list). Bots, alerts, pushes and one sender's announcements are still dropped whole.
func intersect(c Chat, lines []norm.Line, set func(rule, action string, score float64, reason string) Decision, d *Decision) Decision {
	if why := noise(c, lines); why != "" {
		return set(RNoise, Drop, 0, why)
	}
	for _, l := range lines {
		if Intersects(c, l) {
			d.Shown[l.N] = true
		}
	}
	if len(d.Shown) == 0 {
		return set(RNoIntersect, Drop, 0, "说话人认不出，或和你没有交集")
	}
	return set(RIntersect, Window, 30, fmt.Sprintf("你没发言：只读认得出说话人、和你有交集的 %d 条", len(d.Shown)))
}

// Intersects says whether a line of a group the user is quiet in is kept: its speaker resolved, and the speaker the
// user or on the intersection list, or the line naming the user, a project of the user's or someone on the list.
func Intersects(c Chat, l norm.Line) bool {
	if l.Speaker == norm.Me {
		return true
	}
	if l.Speaker == "" || l.Speaker == "?" || l.Speaker == voice.Ghost || l.Speaker == voice.AI || voice.Automated(l.Speaker) {
		return false
	}
	who := l.Speaker
	if c.Who != nil {
		who = c.Who[l.N]
		if who == "" {
			return false // unresolved: never put on anyone, never kept from a group the user is quiet in
		}
		if who == entry.WhoSelf || c.Contacts[who] {
			return true
		}
	}
	if mentionsOwner(l.Body, c.OwnerNames) {
		return true
	}
	for _, t := range c.Topics {
		if voice.Mentions(l.Body, []string{t}) {
			return true
		}
	}
	return false
}

// noise says why a quiet group's day is noise, or "": nothing but automated senders, or a single sender (a feed, an
// alert stream, an announcement). A big group is no longer noise for being big (3.3): its lines go through intersect.
func noise(c Chat, lines []norm.Line) string {
	senders := map[string]bool{}
	humans := map[string]bool{}
	for _, l := range lines {
		if l.Speaker == "" || l.Speaker == norm.Me {
			continue
		}
		senders[l.Speaker] = true
		if !voice.Automated(l.Speaker) {
			humans[l.Speaker] = true
		}
	}
	switch {
	case len(humans) == 0:
		return "只有机器人、报警或推送"
	case len(senders) == 1:
		return "只有一个人在发：推送、报警或公告"
	}
	return ""
}
