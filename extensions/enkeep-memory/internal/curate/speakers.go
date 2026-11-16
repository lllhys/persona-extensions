package curate

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/people"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/speaker"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/voice"
)

// Speaker attribution (design 3.2), the intersection list (5.7) and the attribution checks (U8, K16), wired into
// the daily pass.

// whoCache keeps what resolution reads from the knowledge base during a curator's life: kb.owner (re-read every few
// minutes), every person's name (one kb.list), and the full entries (aliases, accounts) of the members it needed.
type whoCache struct {
	mu      sync.Mutex
	ownerAt time.Time
	owner   speaker.Owner
	names   map[string]string
	listed  bool
	details map[string]speaker.Person
}

var whoInit sync.Mutex

func (c *Curator) whoC() *whoCache {
	whoInit.Lock()
	defer whoInit.Unlock()
	if c.who == nil {
		c.who = &whoCache{details: map[string]speaker.Person{}}
	}
	return c.who
}

// speakerOwner is kb.owner with the configuration's aliases, as resolution reads it.
func (c *Curator) speakerOwner(ctx context.Context) speaker.Owner {
	w := c.whoC()
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.ownerAt.IsZero() && time.Since(w.ownerAt) < 5*time.Minute {
		return w.owner
	}
	o, _ := c.Src.Owner(ctx)
	w.owner, w.ownerAt = speaker.OwnerOf(o, c.Cfg.OwnerAliases), time.Now()
	return w.owner
}

// fullMembers: a unit with at most this many members has every member's whole entry read (aliases, accounts); a
// bigger one is matched on the names kb.list gives, so a ten-thousand-member group costs one list, not ten thousand
// reads.
const fullMembers = 400

// members are a unit's members as resolution needs them.
func (c *Curator) members(ctx context.Context, ids []string, full bool) []speaker.Person {
	w := c.whoC()
	w.mu.Lock()
	defer w.mu.Unlock()
	if !full && !w.listed {
		w.names = map[string]string{}
		if es, err := c.Src.List(ctx, "people"); err == nil {
			for _, e := range es {
				w.names[e.ID] = e.Name
			}
		}
		w.listed = true
	}
	out := make([]speaker.Person, 0, len(ids))
	for _, id := range ids {
		if p, ok := w.details[id]; ok {
			out = append(out, p)
			continue
		}
		if !full {
			out = append(out, speaker.Person{ID: id, Name: w.names[id]})
			continue
		}
		p := speaker.Person{ID: id}
		if d, err := c.Src.Get(ctx, "people", id); err == nil {
			m := kbsrc.DecodeMeta(d)
			p.Name, p.Aliases = m.Name, m.Aliases
			var extra struct {
				SourceID string   `json:"source_id"`
				Handles  []string `json:"handles"`
			}
			_ = json.Unmarshal(d.Meta, &extra)
			if extra.SourceID != "" {
				p.Handles = append(p.Handles, strings.ToLower(extra.SourceID))
			}
			for _, h := range extra.Handles {
				p.Handles = append(p.Handles, strings.ToLower(h))
			}
		}
		w.details[id] = p
		out = append(out, p)
	}
	return out
}

// personName is a person's display name as far as the cache knows it.
func (c *Curator) personName(id string) string {
	w := c.whoC()
	w.mu.Lock()
	defer w.mu.Unlock()
	if p, ok := w.details[id]; ok && p.Name != "" {
		return p.Name
	}
	return w.names[id]
}

// speakerUnit is what resolution sees of a unit.
func speakerUnit(u *Unit) speaker.Unit {
	return speaker.Unit{Entity: u.Entity, Source: unitSource(u), Kind: u.Meta.Kind, ID: u.Meta.ID, Tags: u.Meta.Tags, Lines: u.Lines}
}

// attribute resolves a unit's speakers into u.Who (and says why in u.WhoWhy), and marks a unit whose user lines may
// not teach ③④⑥ (u.SelfOff).
func (c *Curator) attribute(ctx context.Context, u *Unit) {
	o := c.speakerOwner(ctx)
	su := speakerUnit(u)
	full := len(u.Meta.People) <= fullMembers || u.Meta.Kind == "dm" || u.Meta.Kind == "mail"
	su.Members = c.members(ctx, u.Meta.People, full)
	r := speaker.Resolve(su, o)
	u.Who, u.WhoWhy = r.Who, r.Why
	u.SelfOff = speaker.SelfOff(su, o)
}

// unitSource is a unit's connector: lark, mail, wechat, github, persona…; recordings and documents by kind.
func unitSource(u *Unit) string {
	if u.Entity != "chats" {
		return u.Entity
	}
	if u.Meta.Source != "" {
		return u.Meta.Source
	}
	s, _, _ := strings.Cut(u.Meta.ID, "-")
	return s
}

// contactsCursor is the last day counted into the intersection list: counts are days, so a day is counted once.
const contactsCursor = "people/contacts"

// contactsWarmup is how far back the list is counted from the user's own history the first time (4.1: a new user
// starts from their own data, not from an empty list).
const contactsWarmup = 28

// observeContacts counts a day's dealings into the intersection list, once per day and in order. The first time
// (an empty list, no cursor) it counts the contactsWarmup days before day first. Commitments are recounted from the
// entries every time.
func (c *Curator) observeContacts(ctx context.Context, day string, units []Unit) {
	s := c.St.S
	cur := s.Cursors[contactsCursor]
	if cur == "" && len(s.Contacts) == 0 {
		for d := addDays(day, -contactsWarmup); d < day; d = addDays(d, 1) {
			us, err := c.units(ctx, d)
			if err != nil {
				c.logf("contacts warm-up %s: %v", d, err)
				continue
			}
			people.Observe(s.Contacts, d, c.contactUnits(us), c.ownerNames(ctx))
		}
		cur = addDays(day, -1)
	}
	if day > cur {
		people.Observe(s.Contacts, day, c.contactUnits(units), c.ownerNames(ctx))
		s.Cursors[contactsCursor] = day
	}
	people.Commitments(s.Contacts, s.Entries, func(who string) string { return c.personName(speaker.PersonID(who)) })
}

func (c *Curator) contactUnits(us []Unit) []people.Unit {
	out := make([]people.Unit, 0, len(us))
	for i := range us {
		u := &us[i]
		if u.Who == nil {
			continue
		}
		names := map[string]string{}
		for _, id := range u.Meta.People {
			if n := c.personName(id); n != "" {
				names[id] = n
			}
		}
		for _, who := range u.Who {
			if id := speaker.PersonID(who); id != "" && names[id] == "" {
				names[id] = c.personName(id)
			}
		}
		out = append(out, people.Unit{Entity: u.Entity, Source: unitSource(u), Kind: u.Meta.Kind, Mine: u.Meta.Mine,
			Who: u.Who, Lines: u.Lines, Members: u.Meta.People, Names: names})
	}
	return out
}

// contactSet is the intersection list as the triage reads it.
func (c *Curator) contactSet() map[string]bool {
	out := map[string]bool{}
	for who := range c.St.S.Contacts {
		if people.On(c.St.S.Contacts, who) {
			out[who] = true
		}
	}
	return out
}

// topics are the names whose mention makes a line of a quiet group the user's business: the user's projects and
// their aliases, and the people on the intersection list.
func (c *Curator) topics(k *known) []string {
	var out []string
	if k != nil {
		out = append(out, k.topics...)
	}
	for _, n := range people.Names(c.St.S.Contacts) {
		if distinctiveName(n) {
			out = append(out, n)
		}
	}
	return out
}

// distinctiveName: long enough not to turn up by chance (two Han characters, or four letters), and not a generic
// word ("周会", "项目", "用户").
func distinctiveName(n string) bool {
	n = strings.TrimSpace(n)
	if n == "" || voice.Generic(n) || graphGeneric(n) {
		return false
	}
	han, letters := 0, 0
	for _, r := range n {
		switch {
		case r >= 0x4e00 && r <= 0x9fff:
			han++
		case r < utf8.RuneSelf && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'):
			letters++
		}
	}
	return han >= 2 || letters >= 4
}

// Attribution checks (U8): every source gets up to SpeakerSample attributed lines a round, picked at random (by a
// hash, so a rerun picks the same); the user marks them on the check page.
const (
	SpeakerSample = 30
	speakerRound  = 14 // days
	// SpeakerPass is the share of right marks a source needs to go on teaching people and relations (K16). One line
	// put on the user wrongly fails the round whatever the share.
	SpeakerPass = 0.95
)

// roundOf is the first day of the attribution round a day falls in.
func roundOf(day string) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	n := int(t.Unix()/86400) / speakerRound * speakerRound
	return time.Unix(int64(n)*86400, 0).UTC().Format("2006-01-02")
}

// checkKey is a source's key in State.Sources ("S8/lark", "S11/recordings").
func checkKey(u *Unit) (string, string) {
	src := unitSource(u)
	switch u.Entity {
	case "recordings", "meetings":
		return entry.SrcRecording + "/" + src, src
	}
	return entry.SrcChat + "/" + src, src
}

// attributed: the sources whose attribution is checked: connected messages and recordings. The assistant's and
// agents' conversations are the user by role and need no check.
func attributed(u *Unit) bool {
	if u.Who == nil {
		return false
	}
	if u.Entity == "chats" {
		return unitSource(u) != "persona" && !strings.HasPrefix(u.Meta.ID, "persona-")
	}
	return u.Entity == "recordings" || u.Entity == "meetings"
}

// checkSpeakers samples the day's attributed lines into the round's checks, counts the unresolved share, and
// applies the user's marks (K16). Days are counted once (a cursor per source).
func (c *Curator) checkSpeakers(day string, units []Unit) {
	s := c.St.S
	round := roundOf(day)
	type cand struct {
		chk entry.SpeakerCheck
		key string
	}
	bySource := map[string][]cand{}
	total, unres := map[string]int{}, map[string]int{}
	for i := range units {
		u := &units[i]
		if !attributed(u) {
			continue
		}
		key, src := checkKey(u)
		for _, l := range u.Lines {
			why := u.WhoWhy[l.N]
			if why == speaker.WhyNotPerson || why == "" {
				continue
			}
			total[key]++
			who := u.Who[l.N]
			if who == "" {
				unres[key]++
				continue
			}
			name := l.Speaker
			if id := speaker.PersonID(who); id != "" && c.personName(id) != "" {
				name = c.personName(id)
			}
			bySource[key] = append(bySource[key], cand{
				chk: entry.SpeakerCheck{Source: src, Ref: u.Ref, Day: day, N: l.N, Who: who, Name: name, Quote: clip(l.Body, 200)},
				key: hash("speaker", u.Ref, day, strconv.Itoa(l.N)),
			})
		}
	}
	keys := make([]string, 0, len(total))
	for k := range total {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		st := c.sourceStatus(key)
		code, src, _ := strings.Cut(key, "/")
		cursor := "speaker/" + key
		r, last, n, un := parseSpeakerCursor(s.Cursors[cursor])
		if day <= last && r == round {
			continue // counted already
		}
		if r != round {
			n, un = 0, 0
		}
		n += total[key]
		un += unres[key]
		s.Cursors[cursor] = fmt.Sprintf("%s|%s|%d|%d", round, day, n, un)
		if n > 0 {
			st.Unresolved = float64(un) / float64(n)
		}
		// Sample: the round's checks of the source, plus today's candidates, keep the marked ones and the lowest
		// hashes up to SpeakerSample.
		var mine, others []entry.SpeakerCheck
		for _, chk := range s.SpeakerChecks {
			if chk.Source == src && roundOf(chk.Day) == round && checkCode(chk) == code {
				mine = append(mine, chk)
			} else {
				others = append(others, chk)
			}
		}
		pool := map[string]entry.SpeakerCheck{}
		order := map[string]string{}
		for _, chk := range mine {
			k := hash("speaker", chk.Ref, chk.Day, strconv.Itoa(chk.N))
			pool[k], order[k] = chk, k
		}
		for _, cd := range bySource[key] {
			if _, ok := pool[cd.key]; !ok {
				pool[cd.key], order[cd.key] = cd.chk, cd.key
			}
		}
		ks := make([]string, 0, len(pool))
		for k := range pool {
			ks = append(ks, k)
		}
		sort.Slice(ks, func(i, j int) bool {
			mi, mj := pool[ks[i]].Verdict != nil, pool[ks[j]].Verdict != nil
			if mi != mj {
				return mi
			}
			return ks[i] < ks[j]
		})
		kept := others
		for i, k := range ks {
			if i >= SpeakerSample && pool[k].Verdict == nil {
				break
			}
			kept = append(kept, pool[k])
		}
		s.SpeakerChecks = kept
	}
	c.applySpeakerMarks(day)
}

// checkCode tells recordings' checks from chats' by their ref.
func checkCode(chk entry.SpeakerCheck) string {
	if strings.HasPrefix(chk.Ref, "recordings/") || strings.HasPrefix(chk.Ref, "meetings/") {
		return entry.SrcRecording
	}
	return entry.SrcChat
}

func parseSpeakerCursor(v string) (round, last string, n, un int) {
	parts := strings.Split(v, "|")
	if len(parts) != 4 {
		return "", "", 0, 0
	}
	n, _ = strconv.Atoi(parts[2])
	un, _ = strconv.Atoi(parts[3])
	return parts[0], parts[1], n, un
}

// sourceStatus is a source's row, made when missing.
func (c *Curator) sourceStatus(key string) *entry.SourceStatus {
	st := c.St.S.Sources[key]
	if st == nil {
		code, name, _ := strings.Cut(key, "/")
		st = &entry.SourceStatus{Code: code, Name: name, Available: true, LearnPeople: true}
		c.St.S.Sources[key] = st
	}
	return st
}

// applySpeakerMarks turns the user's marks into K16 for every source: the latest round with marks decides. Below
// SpeakerPass, or one line put on the user that was someone else's, the source stops teaching people and relations
// from chit-chat; a later round that passes turns it back on. Unmarked rounds change nothing (it stays on until the
// user has marked something). A switch the user set on the switches page wins.
func (c *Curator) applySpeakerMarks(day string) {
	s := c.St.S
	latest := map[string]string{} // source key → latest round with a mark
	for _, chk := range s.SpeakerChecks {
		if chk.Verdict == nil || chk.Verdict.Mark == "unsure" {
			continue
		}
		key := checkCode(chk) + "/" + chk.Source
		if r := roundOf(chk.Day); r > latest[key] {
			latest[key] = r
		}
	}
	for key, st := range s.Sources {
		if st.Code != entry.SrcChat && st.Code != entry.SrcRecording {
			continue
		}
		round := latest[key]
		sampled, right, wrong, wrongSelf := 0, 0, 0, false
		for _, chk := range s.SpeakerChecks {
			if checkCode(chk)+"/"+chk.Source != key {
				continue
			}
			r := roundOf(chk.Day)
			if round == "" && r == roundOf(day) || r == round {
				sampled++
			}
			if r != round || chk.Verdict == nil {
				continue
			}
			switch chk.Verdict.Mark {
			case "right":
				right++
			case "wrong":
				wrong++
				if chk.Who == entry.WhoSelf {
					wrongSelf = true
				}
			}
		}
		st.Sampled, st.Right = sampled, right
		learn, note := true, ""
		if marked := right + wrong; marked > 0 {
			acc := float64(right) / float64(marked)
			switch {
			case wrongSelf:
				learn, note = false, "核对里有别人的话被算成你说的：先不从这个来源的闲聊里学人物和关系"
			case acc < SpeakerPass:
				learn, note = false, fmt.Sprintf("说话人归属准确率 %.0f%%（低于 95%%）：先不从这个来源的闲聊里学人物和关系", acc*100)
			}
		}
		sw := entry.K16LearnPeople + "." + st.Name
		if set, ok := s.Switches[sw]; ok && set.By == "user" {
			st.LearnPeople = set.Value != "off"
			continue
		}
		value := "on"
		if !learn {
			value = "off"
		}
		// Only a change is written: off when a round fails, back on when a later one passes.
		if cur := s.Switches[sw]; cur.Value != value && (cur.Value != "" || value == "off") {
			s.Switches[sw] = entry.SwitchSetting{Value: value, By: "auto", Why: note, At: day}
		}
		st.LearnPeople = learn
		if note != "" || strings.HasPrefix(st.Note, "核对里") || strings.HasPrefix(st.Note, "说话人归属") {
			st.Note = note
		}
	}
}

// LearnPeople says whether a source may teach people and relations from chit-chat (K16): its switch, which the
// checks turn off and on (applySpeakerMarks) and the user may set. SourceStatus.LearnPeople mirrors it for the page;
// a row another package made (its zero value false) does not turn learning off.
func LearnPeople(s *store.State, source string) bool {
	return entry.Switch(s.Switches, entry.K16LearnPeople+"."+source) != "off"
}

// MarkSpeakerCheck records the user's mark on one attribution check (the check page): right, wrong or unsure. It
// re-applies K16 at once. Call it with the store's lock held.
func (c *Curator) MarkSpeakerCheck(ref, day string, n int, mark string) error {
	if mark != "right" && mark != "wrong" && mark != "unsure" {
		return fmt.Errorf("unknown mark %q", mark)
	}
	for i := range c.St.S.SpeakerChecks {
		chk := &c.St.S.SpeakerChecks[i]
		if chk.Ref == ref && chk.Day == day && chk.N == n {
			chk.Verdict = &entry.Verdict{Mark: mark, At: c.now().UTC().Format(time.RFC3339), By: "user"}
			c.applySpeakerMarks(c.today())
			return nil
		}
	}
	return fmt.Errorf("no check %s@%s#%d", ref, day, n)
}

// weakOwnerNote leaves the page the reason groups and mail teach nothing about the user (5.0), on every source row
// that had such a unit today.
func (c *Curator) weakOwnerNote(units []Unit) {
	for i := range units {
		u := &units[i]
		if u.SelfOff == "" {
			continue
		}
		key, _ := checkKey(u)
		st := c.sourceStatus(key)
		if st.Note == "" {
			st.Note = u.SelfOff
		}
	}
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// graphGeneric reports the graph's generic words ("周会", "项目"): never a name to match.
func graphGeneric(n string) bool {
	return slices.Contains([]string{"周会", "例会", "这个需求", "这个项目", "项目", "需求", "群", "大家", "所有人", "全体成员", "team", "the team", "project", "this", "meeting", "weekly", "standup"}, strings.ToLower(strings.TrimSpace(n)))
}
