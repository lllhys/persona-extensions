package main

import (
	"html/template"
	"net/http"
	"strings"
)

// previewHandler serves the browser reader. It uses the same article, lexicon,
// store and translator as the Persona page. Underlines exist here because a
// Persona view has no inline text decoration.
func (a *app) previewHandler() http.Handler {
	return http.HandlerFunc(a.servePreview)
}

func (a *app) servePreview(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/mode" && r.Method == http.MethodPost:
		a.postMode(w, r)
	case r.URL.Path == "/save" && r.Method == http.MethodPost:
		a.postSave(w, r)
	case r.URL.Path == "/more" && r.Method == http.MethodPost:
		a.postMore(w, r)
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		a.getPreview(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (a *app) postMode(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	a.store.setMode(r.PostForm.Get("mode"))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *app) postMore(w http.ResponseWriter, r *http.Request) {
	a.store.toggleMore()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *app) postSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	lemma := r.PostForm.Get("lemma")
	lem, g, ok := lexicon.lookup(lemma, false)
	if !ok {
		http.Error(w, "unknown word", http.StatusBadRequest)
		return
	}
	a.store.save(lem, g.text("zh-Hans"))
	http.Redirect(w, r, queryWord(lem), http.StatusSeeOther)
}

func (a *app) getPreview(w http.ResponseWriter, r *http.Request) {
	t := textsFor("zh-Hans")
	snap := a.store.snapshot()
	mode := snap.Mode
	if mode != modeVocabulary {
		mode = modeTranslation
	}
	open := ""
	if mode == modeVocabulary {
		if lem, _, ok := lexicon.lookup(r.URL.Query().Get("word"), false); ok {
			open = lem
		}
	}
	saved := savedSet(snap)
	opt := renderOption{Mode: mode, Locale: "zh-Hans", Saved: saved, Open: open, Lang: "zh-Hans"}
	if mode == modeTranslation {
		opt.Translate = func(text string) (string, error) {
			return a.translateCached(r.Context(), text, "zh-CN")
		}
		opt.Failed = func(err error) string {
			if err == nil {
				return t.failedBare
			}
			return strings.TrimSpace(strings.ReplaceAll(t.failed, "%s", oneLine(err)))
		}
	}
	article, hits, err := apply(articleSource(snap.More), opt)
	if err != nil {
		http.Error(w, "render", http.StatusInternalServerError)
		return
	}
	var pop *popupModel
	if open != "" {
		g := gloss{}
		for _, h := range hits {
			if h.Lemma == open {
				g = gloss{ZH: h.GlossZH, EN: h.GlossEN}
				break
			}
		}
		if g.ZH == "" {
			if _, found, ok := lexicon.lookup(open, true); ok {
				g = found
			}
		}
		text := g.text("zh-Hans")
		savedWord, isSaved := snap.Words[open]
		if isSaved && savedWord.Gloss != "" {
			text = savedWord.Gloss
		}
		pop = &popupModel{Lemma: open, Gloss: text, Saved: isSaved}
	}
	moreLabel := t.more
	if snap.More {
		moreLabel = t.less
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := previewTmpl.Execute(w, previewModel{
		L: labelsOf(t), Mode: mode, Article: template.HTML(article), Popup: pop,
		SavedN: len(snap.Words), MoreLabel: moreLabel,
	}); err != nil && a.logf != nil {
		a.logf("preview: %v", err)
	}
}

// labels is the page chrome the HTML template is allowed to read.
type labels struct {
	Title, ModeTranslation, ModeVocabulary string
	Saved, Save, Close, Attribution        string
	SavedCount                             string
}

func labelsOf(t *texts) labels {
	return labels{
		Title: t.title, ModeTranslation: t.modeTranslation, ModeVocabulary: t.modeVocabulary,
		Saved: t.saved, Save: t.save, Close: t.close, Attribution: t.attribution,
		SavedCount: t.savedCount,
	}
}

type previewModel struct {
	L         labels
	Mode      string
	Article   template.HTML
	Popup     *popupModel
	SavedN    int
	MoreLabel string
}

func (m previewModel) TranslationOn() bool { return m.Mode == modeTranslation }
func (m previewModel) VocabOn() bool       { return m.Mode == modeVocabulary }

type popupModel struct {
	Lemma string
	Gloss string
	Saved bool
}

var previewTmpl = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="zh-Hans">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.L.Title}}</title>
<style>
  :root { color-scheme: light; }
  body { margin: 0; background: #f7f6f2; color: #1c1c1c; font: 18px/1.65 "Iowan Old Style", Palatino, "Palatino Linotype", "Songti SC", "Noto Serif SC", serif; }
  .tw-wrap { max-width: 40rem; margin: 0 auto; padding: 1.25rem 1.25rem 4rem; }
  .tw-modes { display: flex; gap: 0.5rem; align-items: center; margin: 0 0 1rem; }
  .tw-modes button, .tw-more, .tw-pop button, .tw-close {
    font: 15px/1.2 system-ui, sans-serif; border-radius: 999px; border: 1px solid #c8c2b4; background: #fff; color: #1c1c1c; padding: 0.45rem 0.8rem; cursor: pointer;
  }
  .tw-modes button[aria-pressed="true"] { background: #1d4e89; color: #fff; border-color: #1d4e89; }
  .tw-count { margin-left: auto; font: 14px/1 system-ui, sans-serif; color: #3d5c45; }
  button.tw-term {
    font: inherit; color: inherit; background: none; border: 0; padding: 0; margin: 0; display: inline;
    text-decoration: underline; text-decoration-thickness: 1px; text-underline-offset: 0.18em; text-decoration-color: #8a5a12; cursor: pointer;
  }
  button.tw-term.tw-saved { text-decoration-color: #0d6b3d; text-decoration-thickness: 2px; }
  button.tw-term.tw-current { text-decoration-thickness: 2px; }
  .tw-tr { margin: 0.15rem 0 1rem; color: #444; font-size: 0.95rem; }
  .tw-tr-error { color: #8a3b12; }
  .tw-pop {
    position: absolute; z-index: 5; top: 5.5rem; left: 1rem; width: min(18rem, calc(100vw - 1rem));
    background: #fff; color: #1c1c1c; border: 1px solid #ddd6c8; border-radius: 12px; padding: 0.9rem 1rem 1rem;
    box-shadow: 0 10px 28px rgba(40, 32, 16, 0.12);
  }
  .tw-pop h2 { margin: 0 0 0.35rem; font: 600 1.15rem/1.3 system-ui, sans-serif; }
  .tw-pop p { margin: 0 0 0.75rem; }
  .tw-status { color: #0d6b3d; font: 600 0.95rem/1.3 system-ui, sans-serif; }
  .tw-term:focus-visible, .tw-modes button:focus-visible, .tw-pop button:focus-visible, .tw-close:focus-visible, .tw-more:focus-visible {
    outline: 2px solid #1d4e89; outline-offset: 2px;
  }
  .tw-foot { margin-top: 2rem; color: #666; font: 13px/1.4 system-ui, sans-serif; }
</style>
</head>
<body>
<div class="tw-wrap">
  <form id="tw-open" method="get" action="/"></form>
  <form class="tw-modes" method="post" action="/mode" aria-label="阅读模式">
    <button type="submit" name="mode" value="translation" aria-pressed="{{if .TranslationOn}}true{{else}}false{{end}}">{{.L.ModeTranslation}}</button>
    <button type="submit" name="mode" value="vocabulary" aria-pressed="{{if .VocabOn}}true{{else}}false{{end}}">{{.L.ModeVocabulary}}</button>
    <span class="tw-count">{{printf .L.SavedCount .SavedN}}</span>
  </form>
  <main>{{.Article}}</main>
  {{if .Popup}}
  <div class="tw-pop" id="tw-pop" role="dialog" aria-labelledby="tw-pop-title">
    <h2 id="tw-pop-title">{{.Popup.Lemma}}</h2>
    <p>{{.Popup.Gloss}}</p>
    {{if .Popup.Saved}}
      <p class="tw-status" role="status">{{$.L.Saved}}</p>
      <button type="button" disabled>{{$.L.Saved}}</button>
    {{else}}
      <form method="post" action="/save">
        <input type="hidden" name="lemma" value="{{.Popup.Lemma}}">
        <button type="submit">{{$.L.Save}}</button>
      </form>
    {{end}}
    <p><a class="tw-close" href="/">{{$.L.Close}}</a></p>
  </div>
  {{end}}
  <form method="post" action="/more"><button class="tw-more" type="submit">{{.MoreLabel}}</button></form>
  <p class="tw-foot">{{.L.Attribution}}</p>
</div>
<script>
function placePopup(anchorX, anchorY, anchorW, anchorH, popW, popH, viewW, viewH, scrollX, scrollY) {
  var top = anchorY + anchorH + 8;
  var left = anchorX;
  if (top + popH > viewH - 8) {
    top = anchorY - popH - 8;
    if (top < 8) top = 8;
  }
  if (left + popW > viewW - 8) {
    left = viewW - popW - 8;
    if (left < 8) left = 8;
  }
  if (left < 8) left = 8;
  return [top + scrollY, left + scrollX];
}
var pop = document.getElementById("tw-pop");
var anchor = document.querySelector(".tw-current");
if (pop && anchor) {
  var a = anchor.getBoundingClientRect();
  var r = pop.getBoundingClientRect();
  var pos = placePopup(a.left, a.top, a.width, a.height, r.width, r.height, document.documentElement.clientWidth, document.documentElement.clientHeight, window.scrollX, window.scrollY);
  pop.style.top = pos[0] + "px";
  pop.style.left = pos[1] + "px";
  var focus = pop.querySelector("button");
  if (focus) focus.focus();
}
document.addEventListener("keydown", function (e) {
  if (e.key === "Escape" && document.getElementById("tw-pop")) location.href = "/";
});
</script>
</body>
</html>
`))
