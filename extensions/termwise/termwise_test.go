package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
	"golang.org/x/net/html"
)

type staticTranslator struct {
	text  string
	err   error
	calls int
}

func (s *staticTranslator) Translate(context.Context, string, string) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return s.text, nil
}

func testApp(t *testing.T, tr translator) *app {
	t.Helper()
	a := newApp(t.Logf)
	a.tr = tr
	a.store = openStore(t.TempDir(), t.Logf)
	return a
}

func uiReq(action, locale string, version int, values map[string]string, st pageState) *ext.UIRequest {
	r := &ext.UIRequest{}
	r.Action = action
	r.Locale = locale
	r.UIVersion = version
	r.Values = values
	r.State = ext.EncodeState(st)
	return r
}

func viewText(v *wire.View) string {
	var b strings.Builder
	b.WriteString(v.Title)
	for _, s := range v.Sections {
		b.WriteString("\n")
		b.WriteString(s.Title)
		b.WriteString("\n")
		b.WriteString(s.Footer)
		for _, c := range s.Items {
			b.WriteString("\n")
			b.WriteString(c.Title)
			b.WriteString("\n")
			b.WriteString(c.Subtitle)
			b.WriteString("\n")
			b.WriteString(c.Text)
			if c.Badge != nil {
				b.WriteString("\n")
				b.WriteString(c.Badge.Text)
			}
			for _, choice := range c.Choices {
				b.WriteString("\n")
				b.WriteString(choice.Title)
			}
		}
	}
	return b.String()
}

func checkView(t *testing.T, v *wire.View) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := exttest.CheckView(raw, false); err != nil {
		t.Fatalf("view: %v\n%s", err, raw)
	}
}

func TestLicenseNotice(t *testing.T) {
	text := string(licenseNotice)
	if !strings.Contains(text, "MIT License") || !strings.Contains(text, "Copyright (c) 2025 Linwei") {
		t.Fatal("ECDICT notice is not embedded")
	}
}

func TestLexiconCandidates(t *testing.T) {
	lemma, g, ok := lexicon.lookup("lanterns", false)
	if !ok || lemma != "lantern" || g.ZH == "" {
		t.Fatalf("lanterns -> %+v %q %v", lemma, g.ZH, ok)
	}
	if _, _, ok := lexicon.lookup("morning", false); ok {
		t.Fatal("morning is in the basic band and is not a candidate")
	}
	if _, _, ok := lexicon.lookup("beside", false); ok {
		t.Fatal("beside is a function word")
	}
	if _, _, ok := lexicon.lookup("the", false); ok {
		t.Fatal("the is not vocabulary")
	}
	if _, _, ok := lexicon.lookup("endpoint", false); ok {
		t.Fatal("endpoint is in devwords.txt and should not be underlined by default")
	}
	if lem, g, ok := lexicon.lookup("endpoint", true); !ok || lem != "endpoint" || g.ZH == "" {
		t.Fatalf("saved devword lookup failed: %q %q %v", lem, g.ZH, ok)
	}
	if _, _, ok := lexicon.lookup("QUAY", false); ok {
		t.Fatal("short uppercase acronym should be skipped unless remembered")
	}
	if len(lexicon.lemmas) < 1000 {
		t.Fatalf("lexicon too small: %d", len(lexicon.lemmas))
	}
}

func TestAnnotateSkipsNonReading(t *testing.T) {
	const src = `<header>harbor</header><nav><a href="/keep">harbor home</a></nav>` +
		`<style>.harbor{color:red}</style><script>var harbor = "merchant";</script>` +
		`<form><textarea>harbor merchant</textarea><input value="harbor">` +
		`<p contenteditable="true">harbor stays</p></form>` +
		`<article><p>The harbor lantern is here.</p></article>`
	out, hits, err := apply(src, renderOption{Mode: modeVocabulary})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<style") && strings.Contains(out, "tw-term") {
		nav := out[:strings.Index(out, "<article>")]
		if strings.Contains(nav, "tw-term") || strings.Contains(nav, "data-lemma") {
			t.Fatalf("annotation escaped the article:\n%s", nav)
		}
	}
	if !strings.Contains(out, `href="/keep"`) {
		t.Fatalf("navigation href changed:\n%s", out)
	}
	if strings.Count(out, "data-lemma") != len(hits) {
		t.Fatalf("hits %d marks %d", len(hits), strings.Count(out, "data-lemma"))
	}
	for _, h := range hits {
		if h.Lemma != "harbor" && h.Lemma != "lantern" {
			t.Fatalf("unexpected lemma %s in %s", h.Lemma, out)
		}
	}
	if !strings.Contains(out, "var harbor") || !strings.Contains(out, ".harbor{color:red}") {
		t.Fatalf("script or style was rewritten:\n%s", out)
	}
	if strings.Contains(out, "<style") {
		style := out[strings.Index(out, "<style"):strings.Index(out, "</style>")]
		if strings.Contains(style, "tw-term") {
			t.Fatal("style was annotated")
		}
	}
	for _, plain := range []string{"harbor stays", "harbor merchant"} {
		i := strings.Index(out, plain)
		if i < 0 {
			t.Fatalf("missing %q in %s", plain, out)
		}
		window := out[max(0, i-60):i]
		if strings.Contains(window, "tw-term") || strings.Contains(window, "<button") {
			t.Fatalf("%q was annotated: %s", plain, window)
		}
	}
	again, hits2, err := apply(out, renderOption{Mode: modeVocabulary})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(again, "tw-term") != strings.Count(out, "tw-term") || len(hits2) != len(hits) {
		t.Fatalf("reapplying stacked marks: %d -> %d", len(hits), len(hits2))
	}
	if strings.Contains(again, `class="tw-term`) && strings.Contains(again, "<button") {
		doc, err := html.Parse(strings.NewReader(again))
		if err != nil {
			t.Fatal(err)
		}
		var nested bool
		var walk func(*html.Node, bool)
		walk = func(n *html.Node, inside bool) {
			if n.Type == html.ElementNode && hasClass(n, "tw-term") {
				if inside {
					nested = true
				}
				inside = true
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c, inside)
			}
		}
		walk(doc, false)
		if nested {
			t.Fatal("underlines nested")
		}
	}
}

func TestModesDoNotStack(t *testing.T) {
	tr := func(string) (string, error) { return "<b>译文</b>", nil }
	optT := renderOption{Mode: modeTranslation, Lang: "zh-Hans", Translate: tr, Failed: func(error) string { return "翻译没有成功" }}
	translated, hits, err := apply(articleSource(false), optT)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 || strings.Contains(translated, "tw-term") || !strings.Contains(translated, "tw-tr") {
		t.Fatalf("translation mode mixed with vocabulary:\n%s", translated)
	}
	if !strings.Contains(translated, "A morning in the harbor") || !strings.Contains(translated, "&lt;b&gt;译文&lt;/b&gt;") {
		t.Fatalf("original lost or translation treated as HTML:\n%s", translated)
	}
	if strings.Count(translated, "tw-tr") != strings.Count(applyMust(t, translated, optT), "tw-tr") {
		t.Fatal("translation pass stacked")
	}
	vocab, vhits, err := apply(translated, renderOption{Mode: modeVocabulary})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(vocab, "tw-tr") || strings.Contains(vocab, "译文") {
		t.Fatalf("vocabulary mode kept translation markup:\n%s", vocab)
	}
	if len(vhits) == 0 || !strings.Contains(vocab, "tw-term") {
		t.Fatal("vocabulary mode did not underline")
	}
	if strings.Count(vocab, `data-lemma="resilient"`) != 2 {
		t.Fatalf("resilient occurrences: %s", vocab)
	}
	back, _, err := apply(vocab, optT)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(back, "tw-term") || !strings.Contains(back, "tw-tr") {
		t.Fatalf("switching back left vocabulary markup:\n%s", back)
	}
	more, moreHits, err := apply(articleSource(true), renderOption{Mode: modeVocabulary})
	if err != nil {
		t.Fatal(err)
	}
	if len(moreHits) <= len(vhits) || !strings.Contains(more, "vessel") || strings.Contains(applyMust(t, articleSource(false), renderOption{Mode: modeVocabulary}), "vessel") {
		t.Fatal("extra paragraph was not limited to the expanded article")
	}
	saved := map[string]bool{"lantern": true}
	marked, _, err := apply(articleSource(false), renderOption{Mode: modeVocabulary, Saved: saved, Open: "lantern"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(marked, "tw-saved") != 1 || strings.Count(marked, "tw-current") != 1 {
		t.Fatalf("saved/current marks:\n%s", marked)
	}
}

func applyMust(t *testing.T, src string, opt renderOption) string {
	t.Helper()
	out, _, err := apply(src, opt)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPlacePopup(t *testing.T) {
	top, left := placePopup(20, 40, 30, 16, 120, 80, 400, 800, 0, 0)
	if top != 40+16+8 || left != 20 {
		t.Fatalf("below the word: %v %v", top, left)
	}
	top, left = placePopup(20, 700, 30, 16, 120, 80, 400, 760, 0, 10)
	if top >= 700 {
		t.Fatalf("popup covered the lower edge: top %v", top)
	}
	_, left = placePopup(350, 40, 40, 16, 120, 80, 400, 800, 0, 0)
	if left < 8 || left+120 > 400-8 {
		t.Fatalf("popup left the viewport: %v", left)
	}
}

func TestStoreIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := openStore(dir, t.Logf)
	if s.snapshot().Mode != modeTranslation {
		t.Fatal(s.snapshot().Mode)
	}
	s.setMode("nope")
	if s.snapshot().Mode != modeTranslation {
		t.Fatal("invalid mode stored")
	}
	s.setMode(modeVocabulary)
	if already := s.save("lantern", "灯笼"); already {
		t.Fatal("first save")
	}
	first := s.snapshot().Words["lantern"]
	if already := s.save("lantern", "另一条释义"); !already {
		t.Fatal("second save should be idempotent")
	}
	second := s.snapshot().Words["lantern"]
	if len(s.snapshot().Words) != 1 || second != first || second.Gloss != "灯笼" {
		t.Fatalf("record changed: %+v %+v", first, second)
	}
	s.setMode(modeTranslation)
	reopened := openStore(dir, t.Logf)
	got := reopened.snapshot()
	if got.Mode != modeTranslation || len(got.Words) != 1 || got.Words["lantern"].Gloss != "灯笼" {
		t.Fatalf("reopen: %+v", got)
	}
	if err := os.WriteFile(dir+"/"+stateFile, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := openStore(dir, t.Logf)
	if fresh.snapshot().Mode != modeTranslation || len(fresh.snapshot().Words) != 0 {
		t.Fatalf("corrupt file was kept: %+v", fresh.snapshot())
	}
	if _, err := os.Stat(dir + "/" + stateFile + ".corrupt"); err != nil {
		t.Fatal(err)
	}
}

func TestPageModesAndSave(t *testing.T) {
	tr := &staticTranslator{text: "港湾的早晨"}
	a := testApp(t, tr)
	ctx := context.Background()
	view, err := a.render(ctx, uiReq("", "zh-Hans", 3, nil, pageState{}))
	if err != nil {
		t.Fatal(err)
	}
	checkView(t, view)
	text := viewText(view)
	if !strings.Contains(text, "翻译模式") || !strings.Contains(text, "生词模式") || !strings.Contains(text, "A morning in the harbor") || !strings.Contains(text, "港湾的早晨") {
		t.Fatalf("translation page:\n%s", text)
	}
	if strings.Contains(text, "记下这个词") || tr.calls == 0 {
		t.Fatalf("translation page called out vocabulary or skipped translation: calls %d\n%s", tr.calls, text)
	}
	tr.calls = 0
	res, err := a.action(ctx, uiReq("mode", "zh-Hans", 3, map[string]string{"mode": modeVocabulary}, pageState{}))
	if err != nil {
		t.Fatal(err)
	}
	checkView(t, res.View)
	text = viewText(res.View)
	if tr.calls != 0 || strings.Contains(text, "港湾的早晨") || !strings.Contains(text, "A morning in the harbor") {
		t.Fatalf("vocabulary page still translated: calls %d\n%s", tr.calls, text)
	}
	if !strings.Contains(text, "出现 2 次") || !strings.Contains(text, "lantern") {
		t.Fatalf("missing repeated-word row:\n%s", text)
	}
	res, err = a.action(ctx, uiReq("word:lantern", "zh-Hans", 3, nil, pageState{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(viewText(res.View), "记下这个词") || strings.Contains(viewText(res.View), "已记下") {
		t.Fatalf("open word:\n%s", viewText(res.View))
	}
	res, err = a.action(ctx, uiReq("save:lantern", "zh-Hans", 3, nil, pageState{Word: "lantern"}))
	if err != nil {
		t.Fatal(err)
	}
	res, err = a.action(ctx, uiReq("save:lantern", "zh-Hans", 3, nil, pageState{Word: "lantern"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(a.store.snapshot().Words) != 1 || !strings.Contains(viewText(res.View), "已记下") {
		t.Fatalf("save was not idempotent: %+v\n%s", a.store.snapshot().Words, viewText(res.View))
	}
	res, err = a.action(ctx, uiReq("mode:translation", "zh-Hans", 3, nil, pageState{Word: "lantern"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(viewText(res.View), "记下这个词") || strings.Contains(viewText(res.View), "出现 2 次") {
		t.Fatalf("translation mode kept vocabulary UI:\n%s", viewText(res.View))
	}
	if len(a.store.snapshot().Words) != 1 {
		t.Fatal("mode switch dropped saved words")
	}
	old, err := a.render(ctx, uiReq("", "zh-Hans", 1, nil, pageState{}))
	if err != nil {
		t.Fatal(err)
	}
	checkView(t, old)
	if strings.Contains(string(mustJSON(t, old)), `"segmented"`) {
		t.Fatal("UI version 1 got a segmented control")
	}
	en, err := a.action(ctx, uiReq("mode", "en", 3, map[string]string{"mode": modeVocabulary}, pageState{}))
	if err != nil {
		t.Fatal(err)
	}
	_, g, _ := lexicon.lookup("quay", false)
	if !strings.Contains(viewText(en.View), "Translation") || !strings.Contains(viewText(en.View), "Vocabulary") || (g.EN != "" && !strings.Contains(viewText(en.View), g.EN)) {
		t.Fatalf("english page:\n%s", viewText(en.View))
	}
	ja, err := a.render(ctx, uiReq("", "ja", 3, nil, pageState{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(viewText(ja), "翻訳モード") || !strings.Contains(viewText(ja), "単語モード") {
		t.Fatalf("japanese page:\n%s", viewText(ja))
	}
	res, err = a.action(ctx, uiReq("more", "zh-Hans", 3, nil, pageState{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(viewText(res.View), "vessel") {
		t.Fatal("extra paragraph missing")
	}
	res, err = a.action(ctx, uiReq("more", "zh-Hans", 3, nil, pageState{}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(viewText(res.View), "vessel") {
		t.Fatal("extra paragraph stayed after hiding it")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestLocales(t *testing.T) {
	for id, text := range locales {
		if utf8.RuneCountInString(text.modeTranslation) > 12 || utf8.RuneCountInString(text.modeVocabulary) > 12 {
			t.Fatalf("%s mode title too long", id)
		}
		for _, format := range []string{text.times, text.savedCount} {
			if strings.Count(format, "%d") != 1 || strings.Contains(format, "%s") {
				t.Fatalf("%s format %q", id, format)
			}
		}
		if strings.Count(text.failed, "%s") != 1 {
			t.Fatalf("%s failed format", id)
		}
	}
}

func TestPreviewFlow(t *testing.T) {
	tr := &staticTranslator{text: "港湾的早晨"}
	a := testApp(t, tr)
	h := a.previewHandler()
	body := previewGet(t, h, "/")
	if !strings.Contains(body, `aria-pressed="true">翻译模式`) || !strings.Contains(body, `class="tw-tr"`) || strings.Contains(body, "data-lemma") {
		t.Fatalf("default translation page:\n%s", body)
	}
	if !strings.Contains(body, "A morning in the harbor") || !strings.Contains(body, "港湾的早晨") {
		t.Fatal("translation text missing")
	}
	previewPost(t, h, "/mode", "mode=vocabulary")
	body = previewGet(t, h, "/")
	if strings.Contains(body, `class="tw-tr"`) || strings.Contains(body, "港湾的早晨") || !strings.Contains(body, "data-lemma") {
		t.Fatalf("vocabulary page:\n%s", body)
	}
	if !strings.Contains(body, `aria-pressed="true">生词模式`) || !strings.Contains(body, ">lanterns</button> along the") || !strings.Contains(body, ">quay</button>") {
		t.Fatalf("selector or original text missing:\n%s", body)
	}
	if strings.Count(body, `data-lemma="resilient"`) != 2 {
		t.Fatal("repeated word was not marked twice")
	}
	body = previewGet(t, h, "/?word=lantern")
	if !strings.Contains(body, `id="tw-pop"`) || !strings.Contains(body, "记下这个词") || !strings.Contains(body, "灯笼") {
		t.Fatalf("popup:\n%s", body)
	}
	res := previewPost(t, h, "/save", "lemma=lantern")
	if res.Code != http.StatusSeeOther || !strings.Contains(res.Header().Get("Location"), "word=lantern") {
		t.Fatalf("save redirect: %d %s", res.Code, res.Header().Get("Location"))
	}
	body = previewGet(t, h, "/?word=lantern")
	if !strings.Contains(body, "已记下") || !strings.Contains(body, `class="tw-term tw-saved`) {
		t.Fatalf("saved feedback:\n%s", body)
	}
	previewPost(t, h, "/save", "lemma=lantern")
	if len(a.store.snapshot().Words) != 1 {
		t.Fatalf("words: %+v", a.store.snapshot().Words)
	}
	bad := httptest.NewRequest(http.MethodPost, "/save", strings.NewReader("lemma=beside"))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bad)
	if rec.Code != http.StatusBadRequest || len(a.store.snapshot().Words) != 1 {
		t.Fatalf("function word saved: %d %+v", rec.Code, a.store.snapshot().Words)
	}
	previewPost(t, h, "/mode", "mode=translation")
	body = previewGet(t, h, "/?word=lantern")
	if strings.Contains(body, "data-lemma") || strings.Contains(body, `id="tw-pop"`) || !strings.Contains(body, `class="tw-tr"`) {
		t.Fatalf("translation mode kept vocabulary UI:\n%s", body)
	}
	previewPost(t, h, "/mode", "mode=vocabulary")
	body = previewGet(t, h, "/")
	if !strings.Contains(body, `class="tw-term tw-saved`) || strings.Contains(body, `class="tw-tr"`) {
		t.Fatalf("saved underline did not return with vocabulary mode:\n%s", body)
	}
	previewPost(t, h, "/more", "")
	body = previewGet(t, h, "/")
	if !strings.Contains(body, "vessel") || strings.Count(body, "data-lemma") == 0 {
		t.Fatal("dynamic paragraph was not annotated")
	}
	nested := strings.Count(body, "data-lemma")
	previewPost(t, h, "/more", "")
	body = previewGet(t, h, "/")
	if strings.Contains(body, "vessel") || strings.Count(body, "data-lemma") >= nested {
		t.Fatalf("hiding the extra paragraph left marks: before %d after %d", nested, strings.Count(body, "data-lemma"))
	}
}

func previewGet(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func previewPost(t *testing.T, h http.Handler, path, form string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form))
	if form != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther && rec.Code != http.StatusBadRequest {
		body, _ := io.ReadAll(rec.Body)
		t.Fatalf("%s: %d %s", path, rec.Code, body)
	}
	return rec
}

func TestTranslatorRejectsWarnings(t *testing.T) {
	out, err := (myMemory{}).parseTranslation([]byte(`{"responseStatus":200,"responseData":{"translatedText":"MYMEMORY WARNING: YOU USED ALL AVAILABLE FREE TRANSLATIONS"}}`), 200)
	if err == nil || out != "" {
		t.Fatalf("warning was accepted: %q %v", out, err)
	}
	out, err = (myMemory{}).parseTranslation([]byte(`{"responseStatus":200,"responseData":{"translatedText":"港湾的早晨"}}`), 200)
	if err != nil || out != "港湾的早晨" {
		t.Fatalf("real translation: %q %v", out, err)
	}
	if _, err := (myMemory{}).parseTranslation([]byte(`{"responseStatus":200,"responseData":{"translatedText":""}}`), 200); err == nil {
		t.Fatal("empty translation was accepted")
	}
}

func TestConformance(t *testing.T) {
	a := testApp(t, &staticTranslator{text: "港湾的早晨"})
	exttest.Check(t, manifest, exttest.Serve(t, program(a)))
}

func TestManifest(t *testing.T) {
	if err := wire.ValidateJSON("Manifest", manifest); err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID, Version string
		Runtime     struct{ Command map[string]string }
		Contributes struct {
			UI []struct{ ID, Title, Placement string }
		}
		Permissions struct{ Network []string }
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "termwise" || m.ID != about.ID || m.Version != about.Version || m.Version != "0.1.0" {
		t.Fatalf("%+v", m)
	}
	if len(m.Contributes.UI) != 1 || m.Contributes.UI[0].ID != pageUI || m.Contributes.UI[0].Placement != "plugin_page" {
		t.Fatalf("ui %+v", m.Contributes.UI)
	}
	if len(m.Permissions.Network) != 1 || m.Permissions.Network[0] != translateHost {
		t.Fatalf("network %v", m.Permissions.Network)
	}
	for _, plat := range []string{"linux-amd64", "linux-arm64", "darwin-arm64", "darwin-amd64"} {
		if m.Runtime.Command[plat] != "bin/termwise-"+plat {
			t.Fatalf("command %s = %s", plat, m.Runtime.Command[plat])
		}
	}
}
