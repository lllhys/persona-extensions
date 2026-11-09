package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"io"
	"strings"
	"unicode"
)

//go:embed dict/lexicon.json.gz
var lexiconGZ []byte

//go:embed devwords.txt
var devwordsRaw string

//go:embed NOTICE.md
var licenseNotice []byte

// lexicon is the embedded ECDICT subset. It is read-only after init.
var lexicon = mustLexicon()

// devwords are everyday programming words from https://github.com/s546126/termwise
// (termwise/devwords.txt) that stay ordinary text unless explicitly remembered.
var devwords = parseWordList(devwordsRaw)

type gloss struct {
	ZH string
	EN string
}

type wordLexicon struct {
	lemmas map[string]gloss
	forms  map[string]string
}

func mustLexicon() *wordLexicon {
	lx, err := readLexicon(lexiconGZ)
	if err != nil {
		panic(err)
	}
	return lx
}

func readLexicon(gz []byte) (*wordLexicon, error) {
	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var file struct {
		Lemmas map[string]struct {
			Z string `json:"z"`
			E string `json:"e"`
		} `json:"lemmas"`
		Forms map[string]string `json:"forms"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	lx := &wordLexicon{lemmas: make(map[string]gloss, len(file.Lemmas)), forms: file.Forms}
	for word, entry := range file.Lemmas {
		if entry.Z == "" {
			continue
		}
		lx.lemmas[word] = gloss{ZH: entry.Z, EN: entry.E}
	}
	if lx.forms == nil {
		lx.forms = map[string]string{}
	}
	return lx, nil
}

// lookup resolves a surface word to its lemma and gloss.
// Function words, everyday developer words and short uppercase acronyms are not
// candidates unless remember is set (a word already saved).
func (lx *wordLexicon) lookup(surface string, remember bool) (lemma string, g gloss, ok bool) {
	if !remember && isShortAcronym(surface) {
		return "", gloss{}, false
	}
	word := foldWord(surface)
	if !surfaceOK(word) {
		return "", gloss{}, false
	}
	if g, ok = lx.lemmas[word]; ok {
		lemma = word
	} else if lem, isForm := lx.forms[word]; isForm {
		if g, ok = lx.lemmas[lem]; ok {
			lemma = lem
		}
	}
	if !ok {
		return "", gloss{}, false
	}
	if (stopwords[lemma] || devwords[word] || devwords[lemma]) && !remember {
		return "", gloss{}, false
	}
	return lemma, g, true
}

// foldWord is surface as the lexicon and the saved words spell it: lower case, with a typographic apostrophe (’) as '.
func foldWord(surface string) string {
	return strings.ToLower(strings.ReplaceAll(surface, "’", "'"))
}

func parseWordList(raw string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		w := strings.ToLower(strings.TrimSpace(line))
		if w == "" || strings.HasPrefix(w, "#") {
			continue
		}
		out[w] = true
	}
	return out
}

func isShortAcronym(surface string) bool {
	if surface == "" || len(surface) > 5 {
		return false
	}
	hasUpper := false
	for _, r := range surface {
		if !unicode.IsLetter(r) {
			return false
		}
		if !unicode.IsUpper(r) {
			return false
		}
		hasUpper = true
	}
	return hasUpper
}

func (g gloss) text(locale string) string {
	if strings.HasPrefix(strings.ToLower(locale), "en") && g.EN != "" {
		return g.EN
	}
	if g.ZH != "" {
		return g.ZH
	}
	return g.EN
}

func surfaceOK(word string) bool {
	if word == "" || len(word) > 32 {
		return false
	}
	for i, r := range word {
		if i == 0 && !unicode.IsLetter(r) {
			return false
		}
		if !unicode.IsLetter(r) && r != '\'' && r != '-' {
			return false
		}
	}
	return true
}

// stopwords are function words. They stay ordinary text even when the lexicon has a gloss.
var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "but": true, "if": true, "of": true,
	"to": true, "in": true, "on": true, "at": true, "by": true, "for": true, "from": true, "with": true,
	"as": true, "into": true, "over": true, "after": true, "before": true, "between": true, "through": true,
	"during": true, "without": true, "within": true, "along": true, "across": true, "against": true,
	"among": true, "around": true, "about": true, "above": true, "below": true, "under": true,
	"beside": true, "besides": true, "near": true, "off": true, "up": true, "down": true, "out": true,
	"than": true, "then": true, "so": true, "because": true, "while": true, "when": true, "where": true,
	"who": true, "whom": true, "whose": true, "which": true, "what": true, "that": true, "this": true,
	"these": true, "those": true, "there": true, "here": true, "not": true, "no": true, "nor": true,
	"too": true, "very": true, "just": true, "also": true, "only": true,
	"i": true, "me": true, "my": true, "you": true, "your": true, "he": true, "him": true, "his": true,
	"she": true, "her": true, "it": true, "its": true, "we": true, "our": true, "they": true, "them": true,
	"their": true,
	"be":    true, "is": true, "am": true, "are": true, "was": true, "were": true, "been": true, "being": true,
	"do": true, "does": true, "did": true, "have": true, "has": true, "had": true, "will": true, "would": true,
	"can": true, "could": true, "may": true, "might": true, "shall": true, "should": true, "must": true,
}
