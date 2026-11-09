package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

type app struct {
	logf  func(string, ...any)
	tr    translator
	store *store
}

func newApp(logf func(string, ...any)) *app {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &app{
		logf:  logf,
		tr:    myMemory{client: nil},
		store: openStore("", logf),
	}
}

func (a *app) init(_ context.Context, in *ext.Init, _ *ext.Hub) (map[string]any, error) {
	a.store = openStore(in.Info().DataDir, a.logf)
	return nil, nil
}

type pageState struct {
	Word string `json:"w,omitempty"`
}

func (a *app) readState(r *ext.UIRequest) pageState {
	var st pageState
	_ = r.DecodeState(&st)
	return st
}

func (a *app) render(ctx context.Context, r *ext.UIRequest) (*wire.View, error) {
	return a.page(ctx, r, a.readState(r)), nil
}

func (a *app) action(ctx context.Context, r *ext.UIRequest) (*wire.ActionResult, error) {
	st := a.readState(r)
	kind, arg, _ := strings.Cut(r.Action, ":")
	switch kind {
	case "mode":
		if arg == "" {
			arg = r.Values["mode"]
		}
		a.store.setMode(arg)
		st.Word = ""
	case "word":
		if lem, _, ok := lexicon.lookup(arg, false); ok {
			st.Word = lem
		}
	case "save":
		if lem, g, ok := lexicon.lookup(arg, false); ok {
			a.store.save(lem, g.text(r.Locale))
			st.Word = lem
		}
	case "close":
		st.Word = ""
	case "more":
		a.store.toggleMore()
		st.Word = ""
	}
	return &wire.ActionResult{View: a.page(ctx, r, st)}, nil
}

func (a *app) page(ctx context.Context, r *ext.UIRequest, st pageState) *wire.View {
	t := textsFor(r.Locale)
	snap := a.store.snapshot()
	mode := snap.Mode
	if mode != modeVocabulary {
		mode = modeTranslation
	}
	src := articleSource(snap.More)
	v := &wire.View{Title: t.title, State: ext.EncodeState(st), Sections: []wire.Section{modeSection(r, t, mode)}}

	if mode == modeVocabulary && st.Word != "" {
		if lem, g, ok := lexicon.lookup(st.Word, true); ok && (lem == st.Word || snap.Words[lem].Lemma == lem) {
			v.Sections = append(v.Sections, wordSection(t, r.Locale, lem, g, snap.Words[lem]))
		}
	}

	paras, _ := blocks(src)
	article := wire.Section{Title: t.reading}
	if mode == modeTranslation {
		_, target := translationLang(r.Locale)
		for _, para := range paras {
			article.Items = append(article.Items, wire.Component{Type: wire.ComponentText, Text: clipBytes(para, 1800)})
			translated, err := a.translateCached(ctx, para, target)
			line := t.failedBare
			if err == nil && translated != "" {
				line = translated
			} else if err != nil {
				line = fmt.Sprintf(t.failed, oneLine(err))
			}
			article.Items = append(article.Items, wire.Component{Type: wire.ComponentText, Text: clipBytes(line, 1800), Style: wire.TextStyleSecondary})
		}
	} else {
		for _, para := range paras {
			article.Items = append(article.Items, wire.Component{Type: wire.ComponentText, Text: clipBytes(para, 1800)})
		}
	}
	moreTitle := t.more
	if snap.More {
		moreTitle = t.less
	}
	article.Items = append(article.Items, wire.Component{Type: wire.ComponentButton, Title: moreTitle, Action: &wire.Action{ID: "more"}})
	article.Footer = t.attribution
	v.Sections = append(v.Sections, article)

	if mode == modeVocabulary {
		_, hits, err := apply(src, renderOption{Mode: modeVocabulary, Locale: r.Locale, Saved: savedSet(snap)})
		if err == nil && len(hits) > 0 {
			sec := wire.Section{Title: t.wordsTitle}
			for _, g := range groupHits(hits) {
				title := g.Surface
				if !strings.EqualFold(g.Surface, g.Lemma) {
					title = g.Surface + " · " + g.Lemma
				}
				sub := g.gloss(r.Locale)
				if word, ok := snap.Words[g.Lemma]; ok && word.Gloss != "" {
					sub = word.Gloss
				}
				if g.Count > 1 {
					sub = fmt.Sprintf(t.times, g.Count) + " · " + sub
				}
				row := wire.Component{Type: wire.ComponentRow, Title: clipBytes(title, 300), Subtitle: clipBytes(sub, 300), Action: &wire.Action{ID: "word:" + g.Lemma}}
				if _, ok := snap.Words[g.Lemma]; ok {
					row.Badge = &wire.Badge{Text: t.saved, Tone: wire.TonePositive}
				}
				sec.Items = append(sec.Items, row)
			}
			v.Sections = append(v.Sections, sec)
		}
	}

	if len(snap.Words) > 0 {
		sec := wire.Section{Title: fmt.Sprintf(t.savedCount, len(snap.Words))}
		for _, word := range sortedWords(snap.Words) {
			sec.Items = append(sec.Items, wire.Component{
				Type: wire.ComponentRow, Title: word.Lemma, Subtitle: clipBytes(word.Gloss, 300),
				Badge: &wire.Badge{Text: t.saved, Tone: wire.TonePositive},
			})
		}
		v.Sections = append(v.Sections, sec)
	}
	return v
}

func modeSection(r *ext.UIRequest, t *texts, mode string) wire.Section {
	if r.UIVersion >= 2 {
		return wire.Section{Items: []wire.Component{{
			Type: wire.ComponentSegmented, Name: "mode", Value: mode,
			Action: &wire.Action{ID: "mode"},
			Choices: []wire.Choice{
				{Value: modeTranslation, Title: t.modeTranslation},
				{Value: modeVocabulary, Title: t.modeVocabulary},
			},
		}}}
	}
	sec := wire.Section{}
	for _, choice := range []struct{ id, title string }{
		{modeTranslation, t.modeTranslation},
		{modeVocabulary, t.modeVocabulary},
	} {
		row := wire.Component{Type: wire.ComponentRow, Title: choice.title, Action: &wire.Action{ID: "mode:" + choice.id}}
		if choice.id == mode {
			row.Badge = &wire.Badge{Text: t.current, Tone: wire.TonePositive}
		}
		sec.Items = append(sec.Items, row)
	}
	return sec
}

func wordSection(t *texts, locale, lemma string, g gloss, saved savedWord) wire.Section {
	text := g.text(locale)
	items := []wire.Component{{Type: wire.ComponentText, Text: lemma, Style: wire.TextStyleBody}}
	if saved.Lemma == lemma {
		text = saved.Gloss
		items = append(items,
			wire.Component{Type: wire.ComponentText, Text: text},
			wire.Component{Type: wire.ComponentText, Text: t.saved, Style: wire.TextStyleSecondary},
		)
	} else {
		items = append(items,
			wire.Component{Type: wire.ComponentText, Text: text},
			wire.Component{Type: wire.ComponentButton, Title: t.save, Action: &wire.Action{ID: "save:" + lemma}},
		)
	}
	items = append(items, wire.Component{Type: wire.ComponentButton, Title: t.close, Action: &wire.Action{ID: "close"}})
	return wire.Section{Title: t.wordsTitle, Items: items}
}

func savedSet(s readingState) map[string]bool {
	out := make(map[string]bool, len(s.Words))
	for lemma := range s.Words {
		out[lemma] = true
	}
	return out
}

type hitGroup struct {
	hit
	Count int
}

func (g hitGroup) gloss(locale string) string {
	return (gloss{ZH: g.GlossZH, EN: g.GlossEN}).text(locale)
}

func groupHits(hits []hit) []hitGroup {
	var out []hitGroup
	index := map[string]int{}
	for _, h := range hits {
		if i, ok := index[h.Lemma]; ok {
			out[i].Count++
			continue
		}
		index[h.Lemma] = len(out)
		out = append(out, hitGroup{hit: h, Count: 1})
	}
	return out
}

func sortedWords(words map[string]savedWord) []savedWord {
	out := make([]savedWord, 0, len(words))
	for _, word := range words {
		out = append(out, word)
	}
	// Insertion order is not stable; sort by lemma so the page does not jump.
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && out[j].Lemma < out[j-1].Lemma {
			out[j], out[j-1] = out[j-1], out[j]
			j--
		}
	}
	return out
}
