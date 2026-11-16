// Package speaker says who each line of a unit is by (design 3.2): the user (entry.WhoSelf), the one person among
// the unit's own members the line's name points at ("person:<kb id>"), or nobody ("": unresolved). An unresolved
// line is never put on anyone: it teaches nothing about the user (③④⑥), feeds no one's card (⑤′) and no relation
// between people; at most it is a topic-level source of a ⑤ fact that passes the release rules (5.4).
//
// The rules, by source:
//   - the user is the connector's Mine (the knowledge base writes "我"), or a speaker that is one of kb.owner's
//     names, aliases or accounts; in the assistant's and agents' conversations role=user is the user whatever
//     kb.owner says;
//   - a direct chat has two sides: anyone but the user is the other member;
//   - a group (and a mail thread, a pull request, an issue) matches the display name against the unit's own members
//     only, never the whole knowledge base (one name is several people there); a name two members share is
//     unresolved;
//   - mail is by the sender: a forwarded or quoted older mail inside a message is not the outer sender's words
//     (OwnText, Quoted);
//   - recordings have no speaker separation: a single-person voice memo is the user's as a whole, a minutes line
//     that names its speaker (who: …) is that speaker's, everything else is unresolved;
//   - WeChat groups are unresolved until it is known whether the connector gives the sender's account.
package speaker

import (
	"regexp"
	"slices"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
	"github.com/nwverse/persona/sdk/go/wire"
)

// Person is one member of a unit as the knowledge base has it.
type Person struct {
	ID      string   // kb id, without "person:"
	Name    string   // the current display name
	Aliases []string // earlier names, nicknames
	Handles []string // accounts at the source: a mail address, a login, an open id
}

// Owner is who the user is, from kb.owner (and the user's own additions in the configuration).
type Owner struct {
	Names   []string // the name and aliases, without generic words ("用户", "我", "owner")
	Handles []string // accounts at the sources, lower case
	// Weak: kb.owner has a name at most (no alias, no account). The user's lines in groups and mail are then known
	// only by the connector's Mine, and a line naming the user cannot be told: ③④⑥ are not taken from groups and
	// mail (5.0 "本人核验"), the assistant's and agents' conversations are used as usual.
	Weak bool
}

// OwnerOf reads kb.owner plus extra aliases (the configuration's).
func OwnerOf(o wire.KBOwner, extra []string) Owner {
	var out Owner
	aliases := voice.Names(append(slices.Clone(o.Aliases), extra...))
	out.Names = voice.Names(append([]string{o.Name}, aliases...))
	for _, h := range o.Handles {
		if v := strings.ToLower(strings.TrimSpace(h.Value)); v != "" && !slices.Contains(out.Handles, v) {
			out.Handles = append(out.Handles, v)
		}
	}
	// An alias that only repeats the name is no alias.
	distinct := slices.DeleteFunc(slices.Clone(aliases), func(a string) bool { return norm.Q(a) == norm.Q(o.Name) })
	out.Weak = len(distinct) == 0 && len(out.Handles) == 0
	return out
}

// Is says whether a speaker label is the user by name or account.
func (o Owner) Is(speaker string) bool {
	q := norm.Q(speaker)
	if q == "" {
		return false
	}
	for _, n := range o.Names {
		if norm.Q(n) == q {
			return true
		}
	}
	low := strings.ToLower(speaker)
	for _, h := range o.Handles {
		if h != "" && (low == h || strings.Contains(low, "<"+h+">") || norm.Q(h) == q) {
			return true
		}
	}
	return false
}

// Unit is what resolution needs of one unit of a day.
type Unit struct {
	Entity  string // chats | docs | meetings | events | recordings | agents | asks | todos
	Source  string // lark, mail, wechat, github, persona…
	Kind    string // dm | group | mail | bot | pr | issue; recordings: memo for a voice memo
	ID      string
	Tags    []string
	Members []Person // the unit's own members (meta.people), the user's own person entries included or not
	Lines   []norm.Line
}

// Reasons a line got its Who, for the attribution table (U8) and the sources card.
const (
	WhySelf        = "self"        // the user
	WhyUnique      = "unique"      // the one member the name points at
	WhyAmbiguous   = "ambiguous"   // two or more members go by the name
	WhyNone        = "none"        // no member goes by the name, or the line has no speaker
	WhyUnsupported = "unsupported" // a source whose senders cannot be told yet (WeChat groups)
	WhyNotPerson   = "not_person"  // the assistant, an agent's draft, a bot or an alert
	WhyQuoted      = "quoted"      // a mail that is only a forwarded or quoted older mail
)

// Result is a unit's resolution.
type Result struct {
	Who map[int]string // line number → entry.WhoSelf, "person:<id>", or ""
	Why map[int]string // line number → Why*
}

// Count of lines by reason.
func (r Result) Count(why string) int {
	n := 0
	for _, w := range r.Why {
		if w == why {
			n++
		}
	}
	return n
}

// PersonRef is the Who of a knowledge-base person.
func PersonRef(id string) string { return "person:" + id }

// PersonID is the kb id of a Who, or "" when it is not a person.
func PersonID(who string) string {
	id, ok := strings.CutPrefix(who, "person:")
	if !ok {
		return ""
	}
	return id
}

// conversational sources: role=user is the user, whatever kb.owner says.
func conversational(u Unit) bool {
	return u.Entity == "agents" || u.Entity == "asks" || u.Source == "persona" || strings.HasPrefix(u.ID, "persona-")
}

func wechat(source string) bool {
	s := strings.ToLower(source)
	return strings.Contains(s, "wechat") || s == "wx" || s == "weixin"
}

// userLabels are the speaker labels the user's own side carries in the assistant's and agents' conversations.
var userLabels = []string{norm.Me, "user", "User", "用户"}

// Resolve resolves every line of a unit.
func Resolve(u Unit, o Owner) Result {
	r := Result{Who: map[int]string{}, Why: map[int]string{}}
	set := func(n int, who, why string) { r.Who[n], r.Why[n] = who, why }
	if u.Entity == "recordings" || u.Entity == "meetings" {
		recording(u, o, set)
		return r
	}
	others := otherMembers(u.Members, o)
	idx := index(others)
	for _, l := range u.Lines {
		sp := strings.TrimSpace(l.Speaker)
		switch {
		case sp == norm.Me && !voice.GhostWritten(l.Body):
			if u.Kind == "mail" && pureForward(l.Body) {
				set(l.N, "", WhyQuoted)
				continue
			}
			set(l.N, entry.WhoSelf, WhySelf)
		case conversational(u) && slices.Contains(userLabels, sp):
			set(l.N, entry.WhoSelf, WhySelf)
		case sp == voice.Ghost || sp == voice.AI || voice.Automated(sp) || (u.Kind == "bot" && sp != norm.Me):
			set(l.N, "", WhyNotPerson)
		case sp == "" || sp == "?":
			set(l.N, "", WhyNone)
		case o.Is(sp):
			if u.Kind == "mail" && pureForward(l.Body) {
				set(l.N, "", WhyQuoted)
				continue
			}
			set(l.N, entry.WhoSelf, WhySelf)
		case wechat(u.Source) && u.Kind != "dm":
			set(l.N, "", WhyUnsupported)
		case u.Kind == "mail" && pureForward(l.Body):
			set(l.N, "", WhyQuoted)
		case u.Kind == "dm":
			// Two sides: anyone but the user is the other member. With several non-user members (the user's own
			// entries not known as such) the name decides among them.
			switch ids := idx.find(sp); {
			case len(others) == 1:
				set(l.N, PersonRef(others[0].ID), WhyUnique)
			case len(ids) == 1:
				set(l.N, PersonRef(ids[0]), WhyUnique)
			case len(ids) > 1:
				set(l.N, "", WhyAmbiguous)
			default:
				set(l.N, "", WhyNone)
			}
		default:
			switch ids := idx.find(sp); {
			case len(ids) == 1:
				set(l.N, PersonRef(ids[0]), WhyUnique)
			case len(ids) > 1:
				set(l.N, "", WhyAmbiguous)
			default:
				set(l.N, "", WhyNone)
			}
		}
	}
	return r
}

// otherMembers are the members that are not the user: a person entry whose name or account is the user's is the
// user's own entry at that source.
func otherMembers(ms []Person, o Owner) []Person {
	var out []Person
	for _, m := range ms {
		self := o.Is(m.Name)
		for _, h := range m.Handles {
			if o.Is(h) {
				self = true
			}
		}
		if !self {
			out = append(out, m)
		}
	}
	return out
}

type nameIndex map[string][]string // canonical name, alias or handle → member ids

func index(ms []Person) nameIndex {
	idx := nameIndex{}
	add := func(key, id string) {
		if key = norm.Q(key); key != "" && !slices.Contains(idx[key], id) {
			idx[key] = append(idx[key], id)
		}
	}
	for _, m := range ms {
		add(m.Name, m.ID)
		for _, a := range m.Aliases {
			add(a, m.ID)
		}
		for _, h := range m.Handles {
			add(h, m.ID)
		}
	}
	return idx
}

// find looks a speaker label up: the label itself, then a mail address inside it ("Name <a@b>").
func (idx nameIndex) find(speaker string) []string {
	if ids := idx[norm.Q(speaker)]; len(ids) > 0 {
		return ids
	}
	if a := mailAddr.FindString(speaker); a != "" {
		return idx[norm.Q(strings.ToLower(a))]
	}
	return nil
}

var mailAddr = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

// Mail: where the sender's own words end. A forwarded message, a quoted older mail ("> …", "On … wrote:",
// "-----Original Message-----", "发件人：…") is by whoever wrote it, not by the outer sender.
var quoteStart = regexp.MustCompile(`(?im)^\s*(>|-{2,}\s*(forwarded message|original message|转发的邮件|原始邮件)\s*-{2,}|` +
	`begin forwarded message:|on .{4,120} wrote:|在 .{4,120}写道[：:]|(from|发件人)\s*[:：].{1,200}$)`)

// OwnText is the part of a message body the sender wrote: everything before the first quoted or forwarded part.
func OwnText(body string) string {
	if loc := quoteStart.FindStringIndex(body); loc != nil {
		return strings.TrimSpace(body[:loc[0]])
	}
	return body
}

// Quoted says whether quote, taken from body, lies in a quoted or forwarded part of it (not the sender's words).
// A quote found in the sender's own part is the sender's; one found only after the quote mark is not.
func Quoted(body, quote string) bool {
	own := OwnText(body)
	if own == body {
		return false
	}
	if ok, _ := norm.Match(quote, own); ok {
		return false
	}
	ok, _ := norm.Match(quote, body)
	return ok
}

// pureForward: the message is nothing but a forwarded or quoted older mail.
func pureForward(body string) bool {
	return OwnText(body) == "" && quoteStart.MatchString(body)
}

// whoMark is a minutes line naming its speaker: "who: X", "（who：X）", "发言人：X", "说话人: X".
var whoMark = regexp.MustCompile(`(?i)(?:\bwho|发言人|说话人)\s*[:：=]\s*([^\s,，;；)）\]】]{1,40})`)

// memo says whether a recording is a single-person voice memo: marked as one by its kind or tags, and with no
// member other than the user.
func memo(u Unit, o Owner) bool {
	marked := slices.ContainsFunc(append([]string{u.Kind}, u.Tags...), func(t string) bool {
		t = strings.ToLower(strings.TrimSpace(t))
		return t == "memo" || t == "voice_memo" || t == "voice memo" || t == "语音备忘" || t == "语音备忘录" || t == "备忘录音"
	})
	return marked && len(otherMembers(u.Members, o)) == 0
}

// recording: a voice memo is the user's whole; a minutes line naming its speaker is that speaker's; the rest is
// unresolved (no speaker separation).
func recording(u Unit, o Owner, set func(n int, who, why string)) {
	whole := u.Entity == "recordings" && memo(u, o)
	idx := index(otherMembers(u.Members, o))
	for _, l := range u.Lines {
		if whole {
			set(l.N, entry.WhoSelf, WhySelf)
			continue
		}
		m := whoMark.FindStringSubmatch(l.Body)
		if m == nil {
			set(l.N, "", WhyNone)
			continue
		}
		name := m[1]
		switch ids := idx.find(name); {
		case name == norm.Me || o.Is(name):
			set(l.N, entry.WhoSelf, WhySelf)
		case len(ids) == 1:
			set(l.N, PersonRef(ids[0]), WhyUnique)
		case len(ids) > 1:
			set(l.N, "", WhyAmbiguous)
		default:
			set(l.N, "", WhyNone)
		}
	}
}

// SelfOff says why the user's own lines of a unit may not teach ③④⑥, or "": with a weak kb.owner, groups and mail
// (5.0). The assistant's and agents' conversations, direct chats and the rest are used as usual.
func SelfOff(u Unit, o Owner) string {
	if !o.Weak || conversational(u) {
		return ""
	}
	if u.Kind == "group" || u.Kind == "mail" {
		return NoteWeakOwner
	}
	return ""
}

// NoteWeakOwner is the page's hint when kb.owner is too thin for groups and mail.
const NoteWeakOwner = "补上你在各来源的账号和别名后，能从群聊和邮件里学到更多"
