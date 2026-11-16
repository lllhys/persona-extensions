package export

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/entry"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/norm"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/prompts"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/verify"
)

// Every exit line is one entry rewritten on its own (6.4) and checked on its own: no placeholder, no internal id,
// the target language, no credential, no first person or "owner". The rewrite carries the hash of the body it was
// written from (Entry.Export.Basis): a changed body is rewritten, an unchanged one is not, and the user's own exit
// wording (Export.User) is never overwritten.

// Reasons a line fails the check.
const (
	BadEmpty       = "empty"
	BadPlaceholder = "placeholder"
	BadInternalID  = "internal_id"
	BadLanguage    = "language"
	BadSecret      = "secret"
	BadSelf        = "self_reference"
	BadQuotesCard  = "quotes_card" // a people tip that copies an evaluation or inferred line (never_quote)
)

var (
	placeholder = regexp.MustCompile(`【[^】]*】|\[(?:[XYZ]|\.\.\.|…|TODO|TBD|[a-z _-]+)\]|(?:^|[^/\w])<[a-z][a-z _-]*>(?:[^/\w]|$)|\{[a-z_]+\}|(?i:\b(?:todo|tbd|fixme|lorem ipsum|placeholder)\b)|某某|XXX`)
	slotXY      = regexp.MustCompile(`(^|[^A-Za-z0-9])X([^A-Za-z0-9]|$)`)
	slotY       = regexp.MustCompile(`(^|[^A-Za-z0-9])Y([^A-Za-z0-9]|$)`)
	internalID  = regexp.MustCompile(`\b[a-z]-[0-9a-f]{10}\b|\b(?:person|project|memory|dig|ledger|signal):[A-Za-z0-9_.-]+|\b(?:chats|docs|agents|asks|todos|recordings|meetings)/[A-Za-z0-9_.-]+|\bc-[0-9a-f]{10}\b`)
)

// Check says what is wrong with an exit line (nil: it may go out).
func Check(text, lang string) []string {
	var bad []string
	t := strings.TrimSpace(text)
	if t == "" {
		return []string{BadEmpty}
	}
	if placeholder.MatchString(t) || (slotXY.MatchString(t) && slotY.MatchString(t)) {
		bad = append(bad, BadPlaceholder)
	}
	if internalID.MatchString(t) {
		bad = append(bad, BadInternalID)
	}
	if !norm.InLanguage(t, lang) || mixedEnglish(t, lang) {
		bad = append(bad, BadLanguage)
	}
	if verify.Secret(t) {
		bad = append(bad, BadSecret)
	}
	if norm.SelfReference(t, lang) {
		bad = append(bad, BadSelf)
	}
	return bad
}

// englishGlue are English function words: names and terms in English are fine in a line of another language, but a
// sentence frame in English ("When …, … usually chooses … over …, because …") is a mixed line.
var englishGlue = regexp.MustCompile(`(?i)\b(?:when|usually|chooses|choose|over|because|unlike|the|is|are|in|like|of|and|with|for|to|handling|way|matters|except)\b`)

// mixedEnglish says whether a line meant for a language not written in Latin script carries English sentence glue
// (two or more function words outside quotes and identifiers).
func mixedEnglish(text, lang string) bool {
	switch norm.Base(lang) {
	case "zh", "ja", "ko", "ru", "uk", "bg", "sr":
	default:
		return false
	}
	return len(englishGlue.FindAllString(norm.StripQuoted(text), -1)) >= 2
}

// Basis is the hash of what an entry's exit text is written from (and the rewrite prompt's version).
func Basis(e *entry.Entry, mode string) string {
	h := sha1.New()
	for _, p := range []string{string(e.Facet), string(e.Grain), e.Text, e.Situation, e.Choice, e.Rejected, e.Why,
		e.Exception, e.Object, e.Pitfall, e.Fix, e.Default, strings.Join(e.Situations, "|"), e.Part, mode, prompts.ExportPrompt,
		prompts.EmptyPrompt} {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Mode of a rewrite: a plain line, or a communication tip from a people card (exit E).
const (
	ModePlain = "plain"
	ModeTip   = "tip"
)

type want struct {
	e    *entry.Entry
	mode string
	exit string
}

// texts gives every wanted entry its exit line: the user's own wording, the kept rewrite when the basis is the same
// and it still passes, or a new rewrite. A rewrite that fails falls back to the entry's own text when that passes
// (never for tips: a card's text is not a tip). Entries without a line are left out of the exits.
func (c *compiler) texts(ctx context.Context, ws []want) map[string]string {
	out := map[string]string{}
	var need []want
	for _, w := range ws {
		e := w.e
		if _, ok := out[e.ID]; ok {
			continue
		}
		basis := Basis(e, w.mode)
		if e.Export != nil && e.Export.User != "" {
			out[e.ID] = e.Export.User
			continue
		}
		if e.Export != nil && e.Export.Basis == basis && e.Export.Withheld && !ownWords(e) {
			delete(out, e.ID)
			c.rep.Dropped = append(c.rep.Dropped, e.ID)
			continue
		}
		if e.Export != nil && e.Export.Basis == basis && c.ok(e, w.mode, e.Export.Text) {
			out[e.ID] = e.Export.Text
			c.rep.Reused++
			continue
		}
		need = append(need, w)
		out[e.ID] = "" // placeholder in the map only: filled below or dropped
	}
	const batch = 12
	for i := 0; i < len(need); i += batch {
		part := need[i:min(i+batch, len(need))]
		got, withheld := c.rewrite(ctx, part)
		for j, w := range part {
			e, text := w.e, strings.TrimSpace(got[j])
			if withheld[j] && w.mode == ModePlain && !ownWords(e) {
				// No moment the assistant would act differently: empty talk, kept off the exits (5.0, 9.4).
				delete(out, e.ID)
				c.rep.Dropped = append(c.rep.Dropped, e.ID)
				user := ""
				if e.Export != nil {
					user = e.Export.User
				}
				e.Export = &entry.ExportText{Basis: Basis(e, w.mode), At: c.now().UTC().Format(time.RFC3339), User: user, Withheld: true}
				continue
			}
			if !c.ok(e, w.mode, text) {
				if text != "" {
					c.reject(e, text, w.mode)
				}
				text = ""
				if w.mode == ModePlain && c.ok(e, w.mode, e.Shown()) {
					text = strings.TrimSpace(e.Shown())
				}
			}
			if text == "" {
				delete(out, e.ID)
				c.rep.Dropped = append(c.rep.Dropped, e.ID)
				continue
			}
			out[e.ID] = text
			c.rep.Rewritten++
			user := ""
			if e.Export != nil {
				user = e.Export.User
			}
			e.Export = &entry.ExportText{Text: text, Basis: Basis(e, w.mode), At: c.now().UTC().Format(time.RFC3339), User: user}
		}
	}
	return out
}

// checkFor is Check for one entry's exit text. The user's own words (an answer to the first-run questions, "about
// me", or text the user wrote over an entry) go out as written whatever the output language: the language check
// does not hold them back, the other checks do.
func (c *compiler) checkFor(e *entry.Entry, text string) []string {
	why := Check(text, c.o.Lang)
	own := e.Origin == entry.FromOnboarding || e.Origin == entry.SelfReport || (e.User != nil && e.User.Text != "")
	if own && strings.TrimSpace(text) == strings.TrimSpace(e.Shown()) {
		kept := why[:0]
		for _, w := range why {
			if w != BadLanguage {
				kept = append(kept, w)
			}
		}
		why = kept
	}
	return why
}

func (c *compiler) ok(e *entry.Entry, mode, text string) bool {
	if len(c.checkFor(e, text)) > 0 {
		return false
	}
	if mode == ModeTip && quotesCard(text, c.never) {
		return false
	}
	return true
}

func (c *compiler) reject(e *entry.Entry, text, mode string) {
	why := c.checkFor(e, text)
	if mode == ModeTip && quotesCard(text, c.never) {
		why = append(why, BadQuotesCard)
	}
	c.rep.Rejected = append(c.rep.Rejected, e.ID+": "+strings.Join(why, ","))
}

// quotesCard says whether a tip contains (or is contained in) an evaluation or inferred line of a card.
func quotesCard(text string, never []string) bool {
	t := squash(text)
	for _, n := range never {
		n = squash(n)
		if len([]rune(n)) >= 6 && (strings.Contains(t, n) || strings.Contains(n, t)) {
			return true
		}
	}
	return false
}

func squash(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(norm.StripQuoted(s))), "")
}

// rewrite asks the model for one line per entry; without a model (or on failure) every answer is "".
func (c *compiler) rewrite(ctx context.Context, ws []want) ([]string, []bool) {
	out := make([]string, len(ws))
	withheld := make([]bool, len(ws))
	if c.o.Model == nil {
		return out, withheld
	}
	type item struct {
		I          int      `json:"i"`
		Facet      string   `json:"facet"`
		Grain      string   `json:"grain"`
		Exit       string   `json:"exit"`
		Mode       string   `json:"mode"`
		Text       string   `json:"text"`
		Situation  string   `json:"situation,omitempty"`
		Choice     string   `json:"choice,omitempty"`
		Rejected   string   `json:"rejected,omitempty"`
		Why        string   `json:"why,omitempty"`
		Exception  string   `json:"exception,omitempty"`
		Object     string   `json:"object,omitempty"`
		Pitfall    string   `json:"pitfall,omitempty"`
		Fix        string   `json:"fix,omitempty"`
		Default    string   `json:"default,omitempty"`
		Situations []string `json:"situations,omitempty"`
		Type       string   `json:"type,omitempty"`
	}
	in := struct {
		Language string `json:"language"`
		UserWord string `json:"user_word"`
		Items    []item `json:"items"`
	}{Language: c.o.Lang, UserWord: norm.UserWord(c.o.Lang)}
	for i, w := range ws {
		e := w.e
		in.Items = append(in.Items, item{I: i, Facet: string(e.Facet), Grain: string(e.Grain), Exit: w.exit, Mode: w.mode,
			Text: e.Shown(), Situation: e.Situation, Choice: e.Choice, Rejected: e.Rejected, Why: e.Why, Exception: e.Exception,
			Object: e.Object, Pitfall: e.Pitfall, Fix: e.Fix, Default: e.Default, Situations: e.Situations, Type: e.Type})
	}
	user, _ := json.Marshal(in)
	resp, err := c.o.Model.Complete(ctx, model.Request{Step: "export_rewrite", Tier: c.o.Tier, System: prompts.Export,
		User: string(user), MaxTokens: 4000, Schema: prompts.ExportSchema})
	if err != nil {
		c.logf("export: rewrite: %v", err)
		return out, withheld
	}
	var got struct {
		Items []struct {
			I       int     `json:"i"`
			Text    string  `json:"text"`
			Changes *string `json:"changes"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &got); err != nil {
		c.logf("export: rewrite answer: %v", err)
		return out, withheld
	}
	for _, it := range got.Items {
		if it.I >= 0 && it.I < len(out) {
			out[it.I] = it.Text
			// An answer that says no moment changes (an empty "changes") is withheld; one that leaves the field out
			// (an older answer) is not judged.
			// A line about the user as a person (⑥) is understanding, not an instruction: it has its own test below.
			withheld[it.I] = it.Changes != nil && strings.TrimSpace(*it.Changes) == "" && ws[it.I].e.Facet != entry.Persona
		}
	}
	c.emptiness(ctx, ws, out, withheld)
	return out, withheld
}

// emptiness asks the empty-talk question (5.0, 9.4) of each plain line on its own, apart from the rewrite: the
// rewrite sees the entry's fields and can always name a moment for them; the line is what the assistant reads.
// A line it cannot answer for is withheld. A line about the user as a person (⑥) is not asked for a moment, which
// understanding has none of, but whether it is a specific fact about this user rather than something true of
// anyone. The user's own words go out as they put them.
func (c *compiler) emptiness(ctx context.Context, ws []want, out []string, withheld []bool) {
	type item struct {
		I    int    `json:"i"`
		Kind string `json:"kind"` // behaviour | person
		Line string `json:"line"`
	}
	var in struct {
		Items []item `json:"items"`
	}
	for i, w := range ws {
		if !withheld[i] && w.mode == ModePlain && strings.TrimSpace(out[i]) != "" && !ownWords(w.e) {
			kind := "behaviour"
			if w.e.Facet == entry.Persona {
				kind = "person"
			}
			in.Items = append(in.Items, item{I: i, Kind: kind, Line: strings.TrimSpace(out[i])})
		}
	}
	if len(in.Items) == 0 {
		return
	}
	user, _ := json.Marshal(in)
	resp, err := c.o.Model.Complete(ctx, model.Request{Step: "export_emptiness", Tier: c.o.Tier, System: prompts.Emptiness,
		User: string(user), MaxTokens: 3000, Schema: prompts.EmptinessSchema})
	if err != nil {
		c.logf("export: emptiness: %v", err) // not asked: the rewrite's own answer stands
		return
	}
	var got struct {
		Items []struct {
			I          int   `json:"i"`
			Answerable *bool `json:"answerable"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &got); err != nil {
		c.logf("export: emptiness answer: %v", err)
		return
	}
	for _, it := range got.Items {
		if it.I >= 0 && it.I < len(withheld) && it.Answerable != nil && !*it.Answerable { // only an explicit no
			withheld[it.I] = true
		}
	}
}

// ownWords: what the user said or wrote themself, or pinned: it goes out as the user put it, without the empty-talk
// question.
func ownWords(e *entry.Entry) bool {
	return e.Origin == entry.FromOnboarding || e.Origin == entry.SelfReport || e.Origin == entry.Handwritten || e.Pinned ||
		(e.User != nil && e.User.Text != "")
}
