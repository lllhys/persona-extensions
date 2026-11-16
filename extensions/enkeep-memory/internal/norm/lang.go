package norm

import (
	"regexp"
	"strings"
	"unicode"
)

// Language rules of the target language the curator writes in (design 5.0): the word for the user, whether a text
// is mainly in the language, and whether it speaks of the user in a way it must not (first person, "owner").

// Base is the language subtag of a BCP 47 tag, lower case: "zh-Hans" → "zh", "en_US" → "en".
func Base(lang string) string {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if i := strings.IndexAny(lang, "-_"); i >= 0 {
		lang = lang[:i]
	}
	if lang == "" {
		return "zh"
	}
	return lang
}

// userWords: the word for the user, by language. A language not listed uses English.
var userWords = map[string]string{
	"zh": "用户", "en": "the user", "ja": "ユーザー", "ko": "사용자", "fr": "l'utilisateur", "de": "der Nutzer",
	"es": "el usuario", "pt": "o usuário", "it": "l'utente", "ru": "пользователь",
}

// UserWord is how texts in lang refer to the user.
func UserWord(lang string) string {
	if w, ok := userWords[Base(lang)]; ok {
		return w
	}
	return userWords["en"]
}

// quoted spans: what a text copies from someone keeps its own language and words.
var quoted = regexp.MustCompile("「[^」]*」|『[^』]*』|“[^”]*”|‘[^’]*’|\"[^\"]*\"|`[^`]*`")

// StripQuoted removes quoted spans from a text.
func StripQuoted(s string) string { return quoted.ReplaceAllString(s, " ") }

// latinWord is a run of Latin letters of at least two (a word, not an "A" or an "x" in "2x").
var latinWord = regexp.MustCompile(`[A-Za-zÀ-ɏ]{2,}`)

// identifier is something named in any language: a URL, a command-line flag, a model id or version ("model-x-1.0"),
// a path or dotted name, a CamelCase name. It is not counted as a word of any language.
var identifier = regexp.MustCompile(`https?://\S+|--?[a-z][a-z\-]+|[A-Za-z0-9_\-]*\d[A-Za-z0-9_.\-]*|[A-Za-z0-9]+(?:[_./\\@:][A-Za-z0-9]+)+|[a-z]+[A-Z][A-Za-z]*|[A-Za-z]+(?:-[A-Za-z0-9]+)+`)

// Scripts counts a text's units by script after quotes and identifiers are taken out: Han characters (CJK), kana,
// Hangul, and Latin words. Two Han characters weigh about one Latin word.
type Scripts struct {
	Han, Kana, Hangul, Cyrillic, Latin int
}

// Count counts the scripts of s.
func Count(s string) Scripts {
	s = identifier.ReplaceAllString(StripQuoted(s), " ")
	var c Scripts
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r):
			c.Han++
		case unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r):
			c.Kana++
		case unicode.Is(unicode.Hangul, r):
			c.Hangul++
		case unicode.Is(unicode.Cyrillic, r):
			c.Cyrillic++
		}
	}
	c.Latin = len(latinWord.FindAllString(s, -1))
	return c
}

// InLanguage says whether a text is mainly in lang: the target script carries a clear share of the text's weight
// (Latin words and Cyrillic words count one each, two CJK characters one). Names, commands and terms in another
// script may stay; a sentence written in another language may not. A text with too little to judge passes.
func InLanguage(s, lang string) bool {
	c := Count(s)
	cjk := float64(c.Han+c.Kana+c.Hangul) / 2
	latin, cyr := float64(c.Latin), float64(c.Cyrillic)/5
	total := cjk + latin + cyr
	if total < 2 {
		return true
	}
	switch Base(lang) {
	case "zh":
		// Chinese technical text is full of English terms: the Han share needs to be clearly there, not dominant.
		return float64(c.Han)/2 >= 0.3*total && c.Kana*4 <= c.Han
	case "ja":
		return c.Kana > 0 && cjk >= 0.4*total
	case "ko":
		return float64(c.Hangul)/2 >= 0.4*total
	case "ru", "uk", "bg", "sr":
		return cyr >= 0.5*total
	default: // Latin-script languages
		return latin >= 0.6*total
	}
}

// selfWords are ways of speaking of the user an entry must not use, by language: the first person (the entry is
// read by an assistant, for whom "I" is itself) and "owner"-like words.
var selfWords = map[string]*regexp.Regexp{
	"zh": regexp.MustCompile(`(?i)我|主人|机主|\bowner\b`),
	"ja": regexp.MustCompile(`(?i)私|僕|俺|オーナー|ご主人|\bowner\b`),
	"ko": regexp.MustCompile(`(?i)나는|내가|저는|제가|주인|오너|\bowner\b`),
	"en": regexp.MustCompile(`(?i)\b(i|i'm|i've|i'd|i'll|me|my|mine|myself)\b|\bowner\b(\s+of\b)?`),
}

// notSelf are words that contain a self word without being one ("自我" is "self-", as in 自我介绍).
var notSelf = regexp.MustCompile(`自我|忘我`)

// SelfReference says whether a text (outside quotes) speaks of the user in the first person or as an "owner". The
// user's own name is checked by the caller, which knows it. "The owner of the repository" names a role and passes.
func SelfReference(s, lang string) bool {
	s = notSelf.ReplaceAllString(StripQuoted(s), " ")
	re, ok := selfWords[Base(lang)]
	if !ok {
		re = selfWords["en"]
	}
	for _, m := range re.FindAllString(s, -1) {
		if low := strings.ToLower(m); strings.HasPrefix(low, "owner") && strings.HasSuffix(low, "of") {
			continue
		}
		return true
	}
	return false
}
