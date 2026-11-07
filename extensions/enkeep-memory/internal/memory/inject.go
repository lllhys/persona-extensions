package memory

import (
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// Level is how the assistant gets to see an entry (6.19).
type Level string

const (
	Resident Level = "resident" // 常驻: always in the assistant's instructions
	Recall   Level = "recall"   // 按场景召回: put in context when the kind of matter or the setting comes up
	Search   Level = "search"   // 只检索: found when the assistant looks
	None     Level = "none"     // 不注入
)

// Injection is one entry's level and the text that may be shown (private sentences removed, rule C).
type Injection struct {
	ID    string `json:"id"`
	Level Level  `json:"level"`
	// Why: which row of 6.19 decided it.
	Why  string `json:"why"`
	Text string `json:"text,omitempty"`
	// Candidate: a reference entry still a candidate: searchable, marked as such.
	Candidate bool    `json:"candidate,omitempty"`
	Score     float64 `json:"score"`
}

// privateDomains are enkeep profile domains that are private as a whole (6.17, enkeep's T3).
var privateDomains = map[string]bool{"relationship_family": true, "health": true, "finance": true, "beliefs_practice": true}

// Privacy applies rule C: a sentence that gives away a specific personal matter (health, finance, relationships,
// beliefs) is not injected; the rest of the entry is. It returns whether the whole entry is private, and the text
// that may be shown. A guard rule for the AI ("感情话题除非用户主动提起，否则不评价") is not private for its topic:
// only a sentence in it that carries a personal detail is.
func Privacy(e *Entry) (whole bool, visible string) {
	if privateDomains[e.Domain] && e.Use != UseFollow {
		return true, ""
	}
	if len(e.PrivateSentences) == 0 {
		return false, e.Text
	}
	sentences := e.Sentences
	if len(sentences) == 0 {
		sentences = []string{e.Text}
	}
	private := map[int]bool{}
	for _, s := range e.PrivateSentences {
		if s.I >= 0 && s.I < len(sentences) {
			private[s.I] = true
		}
	}
	var keep []string
	for i, s := range sentences {
		if !private[i] {
			keep = append(keep, s)
		}
	}
	if len(keep) == 0 {
		return true, ""
	}
	return false, strings.Join(keep, "")
}

// Inject gives every entry its level as of its last Tick (6.19, first match wins):
//
//  1. private as a whole → none (searched only when the user names that kind in the conversation)
//  2. obsolete, dormant, dropped, or lost a contradiction → none
//  3. candidate → none; a reference entry is searchable, marked candidate
//  4. an active principle on the resident list → resident (the top 25 by score, within 1,500 characters)
//  5. an active hard rule the AI must follow, global → resident (within 2,000 characters, counted apart;
//     the rest by score go to recall)
//  6. other active guidelines, active principles off the list → recall
//  7. active descriptive attributes → recall
//  8. experiences and facts of every kind → search
func Inject(b *Book, p Params) []Injection {
	var out []Injection
	var principles, hard []int
	for _, id := range b.ids() {
		e := b.Entries[id]
		st := e.Status
		in := Injection{ID: id, Score: st.Score}
		whole, text := Privacy(e)
		in.Text = text
		switch {
		case whole:
			in.Level, in.Why, in.Text = None, "private", ""
		case st.State == Obsolete || st.State == Dormant || st.State == Dropped || st.Lost:
			in.Level, in.Why = None, string(st.State)
			if st.Lost {
				in.Why = "lost"
			}
		case st.State == Candidate:
			in.Level, in.Why = None, "candidate"
			if usage(e, st) == UseReference {
				in.Level, in.Candidate = Search, true
			}
		case st.Layer == Principle:
			in.Level, in.Why = Recall, "principle"
			principles = append(principles, len(out))
		case st.Layer == Guideline && e.Use == UseFollow && e.Hard && e.Global:
			in.Level, in.Why = Recall, "hard_rule"
			hard = append(hard, len(out))
		case st.Layer == Guideline:
			in.Level, in.Why = Recall, "guideline"
		case st.Descriptive:
			in.Level, in.Why = Recall, "descriptive"
		default:
			in.Level, in.Why = Search, "reference"
		}
		out = append(out, in)
	}
	residentBy(out, principles, p.ResidentPrinciples, p.ResidentPrincipleCh)
	residentBy(out, hard, 0, p.ResidentHardCh)
	return out
}

// residentBy makes the highest-scoring of idx resident, within max entries (0: no count limit) and chars.
func residentBy(out []Injection, idx []int, max, chars int) {
	sort.SliceStable(idx, func(i, j int) bool {
		if out[idx[i]].Score != out[idx[j]].Score {
			return out[idx[i]].Score > out[idx[j]].Score
		}
		return out[idx[i]].ID < out[idx[j]].ID
	})
	n, used := 0, 0
	for _, i := range idx {
		l := utf8.RuneCountInString(out[i].Text)
		if (max > 0 && n >= max) || used+l > chars {
			continue // over the budget: stays recalled
		}
		out[i].Level = Resident
		n++
		used += l
	}
}

// usage is the entry's use in force (6.16): a conclusion turned into an experience is for reference.
func usage(e *Entry, st Status) Use {
	switch {
	case st.Descriptive:
		return UseUnderstand
	case st.Layer == Fact || st.Layer == Experience:
		return UseReference
	}
	if e.Use != "" {
		return e.Use
	}
	return UseUnderstand
}

// Levels counts the injections by level, for the report.
func Levels(in []Injection) map[Level]int {
	n := map[Level]int{}
	for _, x := range in {
		n[x.Level]++
	}
	return n
}

// ByLevel picks the injections at one level.
func ByLevel(in []Injection, l Level) []Injection {
	return slices.DeleteFunc(slices.Clone(in), func(x Injection) bool { return x.Level != l })
}
