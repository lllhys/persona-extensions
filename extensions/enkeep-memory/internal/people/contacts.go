// Package people keeps the people the user deals with (⑤′, design 5.7). This file is the intersection list: who
// the user has dealt with. Being on it decides whom a card is made for and whose chit-chat is kept (3.3); what goes
// on a card is everything resolvable to the person, not only the exchanges with the user.
//
// Dealings, counted a day at a time from the units' resolved speakers (internal/speaker), never from names looked
// up in the whole knowledge base:
//   - dm: a day with a direct chat with them;
//   - group: a day both the user and they spoke in one group (or thread), or one @-mentioned the other;
//   - mail: a day of a mail thread both wrote in, or a thread of the user's they wrote in;
//   - meeting: a meeting, event or recording both are in;
//   - commitment: a promise either way (from the entries).
//
// The list is ordered by how much they dealt; it has no cap. The user's own additions and removals are kept over
// whatever the counts say.
package people

import (
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/speaker"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

// Tie kinds (entry.Contact.Ties keys).
const (
	TieDM         = "dm"
	TieGroup      = "group"
	TieMail       = "mail"
	TieMeeting    = "meeting"
	TieCommitment = "commitment"
)

// Ties in display order.
var Ties = []string{TieDM, TieGroup, TieMail, TieMeeting, TieCommitment}

// weight of each tie in the score: a direct chat or a promise says more than a day in one group.
var weight = map[string]int{TieDM: 3, TieGroup: 1, TieMail: 2, TieMeeting: 2, TieCommitment: 3}

// Manual choices.
const (
	Added   = "added"
	Removed = "removed"
)

// Unit is what the list needs of one unit of a day.
type Unit struct {
	Entity  string // chats | meetings | events | recordings | docs…
	Source  string
	Kind    string // dm | group | mail | bot | pr | issue
	Mine    bool   // a thread of the user's (mail)
	Who     map[int]string
	Lines   []norm.Line
	Members []string          // kb person ids
	Names   map[string]string // kb person id → display name, for @-mentions by the user
}

// Observe counts one day's dealings into the list. Call it once per day (the caller keeps a cursor): counts are
// days, so a day counted twice would count double.
func Observe(list map[string]*entry.Contact, day string, us []Unit, owner []string) {
	for _, u := range us {
		for who, tie := range dealings(u, owner) {
			add(list, who, u.Names[speaker.PersonID(who)], u.Source, tie, day)
		}
	}
}

// dealings are the people a unit says the user dealt with that day, and how.
func dealings(u Unit, owner []string) map[string]string {
	out := map[string]string{}
	selfSpoke := false
	spoke := map[string]bool{}
	mentioned := map[string]bool{}
	for _, l := range u.Lines {
		who := u.Who[l.N]
		switch {
		case who == entry.WhoSelf:
			selfSpoke = true
			replyTo := ""
			if m := replyMark.FindStringSubmatch(l.Body); m != nil {
				replyTo = strings.TrimSpace(m[1])
			}
			for id, name := range u.Names {
				if name != "" && slices.Contains(u.Members, id) && (mentionsName(l.Body, name) || replyTo == name) {
					mentioned[speaker.PersonRef(id)] = true // the user @-mentioned or replied to them
				}
			}
		case speaker.PersonID(who) != "":
			spoke[who] = true
			if voice.Mentions(l.Body, owner) {
				mentioned[who] = true // they @-mentioned or named the user
			}
		}
	}
	switch {
	case u.Entity == "meetings" || u.Entity == "events":
		for _, id := range u.Members {
			out[speaker.PersonRef(id)] = TieMeeting
		}
		for who := range spoke {
			out[who] = TieMeeting
		}
	case u.Entity == "recordings":
		for who := range spoke { // only lines a minutes entry names them in
			out[who] = TieMeeting
		}
	case u.Entity != "chats":
	case u.Kind == "dm":
		for who := range spoke {
			out[who] = TieDM
		}
		if len(spoke) == 0 && selfSpoke && len(u.Members) == 1 {
			out[speaker.PersonRef(u.Members[0])] = TieDM
		}
	case u.Kind == "mail":
		if selfSpoke || u.Mine {
			for who := range spoke {
				out[who] = TieMail
			}
		}
	case u.Kind == "bot":
	default: // groups, pull requests and issues
		for who := range spoke {
			if selfSpoke {
				out[who] = TieGroup
			}
		}
		for who := range mentioned {
			out[who] = TieGroup
		}
	}
	return out
}

// replyMark is how a reply is written at the start of a message: "（回复 name：quoted…）" (Lark), "(Reply name: …)".
var replyMark = regexp.MustCompile(`^\s*[（(](?:回复|Reply to|Reply)\s+([^：:）)]{1,40})[：:]`)

// mentionsName: the user @-mentions a person ("@name"); a bare name in the user's text is not an exchange.
func mentionsName(body, name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && !voice.Generic(name) && (strings.Contains(body, "@"+name) || strings.Contains(body, "@ "+name))
}

func add(list map[string]*entry.Contact, who, name, source, tie, day string) {
	c := list[who]
	if c == nil {
		c = &entry.Contact{Person: who, Ties: map[string]int{}, First: day, Last: day}
		list[who] = c
	}
	if c.Ties == nil {
		c.Ties = map[string]int{}
	}
	if name != "" {
		c.Name = name
	}
	if c.Source == "" {
		c.Source = sourceOf(who, source)
	}
	c.Ties[tie]++
	if day < c.First || c.First == "" {
		c.First = day
	}
	if day > c.Last {
		c.Last = day
	}
	c.Score = Score(c)
}

// sourceOf is the connector a person entry is from: the unit's source, else the prefix of the kb id.
func sourceOf(who, source string) string {
	if source != "" {
		return source
	}
	s, _, _ := strings.Cut(speaker.PersonID(who), "-")
	return s
}

// Score orders the list: weighted days of dealings.
func Score(c *entry.Contact) int {
	n := 0
	for t, v := range c.Ties {
		n += weight[t] * v
	}
	return n
}

// Commitments counts the promises either way between the user and each person, from the entries (⑤ and ⑤′). It
// replaces the commitment tie, so it may be called at any time.
func Commitments(list map[string]*entry.Contact, entries map[string]*entry.Entry, name func(who string) string) {
	count := map[string]int{}
	first := map[string]string{}
	last := map[string]string{}
	for _, e := range entries {
		if e.Commitment == nil || e.Deleted {
			continue
		}
		from, to := e.Commitment.From, e.Commitment.To
		var other string
		switch {
		case from == entry.WhoSelf && speaker.PersonID(to) != "":
			other = to
		case to == entry.WhoSelf && speaker.PersonID(from) != "":
			other = from
		default:
			continue
		}
		count[other]++
		if first[other] == "" || (e.First != "" && e.First < first[other]) {
			first[other] = e.First
		}
		if e.Last > last[other] {
			last[other] = e.Last
		}
	}
	for _, c := range list {
		delete(c.Ties, TieCommitment)
	}
	for who, n := range count {
		c := list[who]
		if c == nil {
			c = &entry.Contact{Person: who, Ties: map[string]int{}, First: first[who], Last: last[who], Source: sourceOf(who, "")}
			if name != nil {
				c.Name = name(who)
			}
			list[who] = c
		}
		c.Ties[TieCommitment] = n
		if last[who] > c.Last {
			c.Last = last[who]
		}
	}
	for _, c := range list {
		c.Score = Score(c)
	}
}

// On says whether a person is on the list: dealt with and not removed by the user, or added by the user.
func On(list map[string]*entry.Contact, who string) bool {
	c := list[who]
	if c == nil {
		return false
	}
	switch c.Manual {
	case Added:
		return true
	case Removed:
		return false
	}
	return c.Score > 0
}

// Set records the user's choice for a person (Added, Removed, or "" to go back to the counts). The counts go on
// being kept; the choice wins over them.
func Set(list map[string]*entry.Contact, who, name, choice, day string) {
	c := list[who]
	if c == nil {
		c = &entry.Contact{Person: who, Name: name, Ties: map[string]int{}, First: day, Last: day, Source: sourceOf(who, "")}
		list[who] = c
	}
	if name != "" && c.Name == "" {
		c.Name = name
	}
	c.Manual = choice
}

// Ranked is the list in order: most dealings first (the user's additions with them), then the latest, then id.
// Removed people are left out.
func Ranked(list map[string]*entry.Contact) []*entry.Contact {
	var out []*entry.Contact
	for who, c := range list {
		if On(list, who) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Last != out[j].Last {
			return out[i].Last > out[j].Last
		}
		return out[i].Person < out[j].Person
	})
	return out
}

// Names are the display names of everyone on the list, for "the content names someone the user deals with".
func Names(list map[string]*entry.Contact) []string {
	var out []string
	for who, c := range list {
		if On(list, who) && c.Name != "" && !slices.Contains(out, c.Name) {
			out = append(out, c.Name)
		}
	}
	sort.Strings(out)
	return out
}
