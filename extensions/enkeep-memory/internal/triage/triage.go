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

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
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
	RSilent    = "R6_silent_group"
	RAllOnly   = "R8_at_all_only"
	RHubRanked = "R9_hub_ranked"
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
)

// Config is the part of the settings the rules use.
type Config struct {
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
	OwnerNames    []string
}

// Decision is the verdict on one chat-day.
type Decision struct {
	Chat   string
	Name   string
	Kind   string
	Rule   string
	Action string
	Score  float64
	Shown  map[int]bool // line numbers the curator may read
	Tokens int          // estimated tokens of the shown lines
	Reason string
}

var (
	Contributor     = regexp.MustCompile(`(?i)no-?reply|do-?not-?reply|notifications?@|newsletter|mailer-daemon|marketing@|news@|bounce`)
	atAll       = regexp.MustCompile(`(?i)@(所有人|全体成员|all|everyone|here)\b?`)
	mailAddr    = regexp.MustCompile(`[A-Za-z0-9._%+-]+@([A-Za-z0-9.-]+\.[A-Za-z]{2,})`)
	defaultBulk = []string{"mailchimp.com", "sendgrid.net", "mailgun.org", "amazonses.com", "list-manage.com", "substack.com", "medium.com", "linkedin.com", "facebookmail.com", "twitter.com", "x.com", "quora.com", "pinterest.com"}
)

// Decide applies the rules to one chat-day.
func Decide(c Chat, cfg Config) Decision {
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
	switch {
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
	if m.Relevance >= cfg.AIMinRelevance || m.Importance == "P0" || m.Importance == "P1" {
		return set(RHubRanked, AI, 0, "你没发言，但知识库认为这个群和你相关")
	}
	return set(RSilent, Drop, 0, fmt.Sprintf("%d 天内你没发言、也没人提到你", cfg.ActivityDays))
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

func mentionsOwner(body string, names []string) bool {
	for _, n := range names {
		if n == "" || n == norm.Me {
			continue
		}
		if strings.Contains(body, "@"+n) || (len([]rune(norm.Q(n))) >= 2 && norm.Contains(body, n)) {
			return true
		}
	}
	return false
}

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
