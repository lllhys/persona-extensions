# Termwise

[Documentation](../README.md) · [Source](../../extensions/termwise/)

Termwise adds a reading page with two modes. Translation mode keeps every sentence and places a translation under it. Vocabulary mode (生词模式) leaves the same sentences in place and underlines candidate words. Choosing an underlined word shows its gloss and can remember the word. Remembering the same word again does not create a second entry.

The page is the `read` contribution (`plugin_page`). The selected mode, the optional extra paragraph, remembered words and successful translations are stored in the extension data directory as `reading.json`.

## Modes

| Mode | What the reader shows |
| --- | --- |
| Translation (翻译模式) | The original block, then the translation. This is the mode used until a choice is stored. |
| Vocabulary (生词模式) | The original block, with candidate words underlined. A remembered word keeps a stronger underline. |

Switching modes redraws from the original article. A translation block is removed before vocabulary marks are added, and vocabulary marks are removed before a translation is added, including when the same HTML is rendered twice.

The in-app page uses the hub's locale for its own labels (zh-Hans, en, ja). The sample article stays English. zh-Hans and ja show the Chinese gloss; en shows the English gloss when ECDICT has one.

Persona views cannot draw an underline inside a sentence. The in-app page therefore keeps the sentence as text and lists the same candidate words as rows. `termwise preview` serves the browser reader, which is the surface with the underlines and the popup:

```sh
go -C extensions/termwise run . preview -addr 127.0.0.1:8731 -data /tmp/termwise-reading
```

That process uses the same article, lexicon, selection and `reading.json` format as the extension. `-data` defaults to a fresh temporary directory.

## What is underlined

A word is a candidate when the embedded lexicon has its lemma and the word is not a function word (articles, pronouns, prepositions, conjunctions, auxiliaries), a short uppercase acronym, or an everyday developer term from `devwords.txt`. The lexicon is a subset of [ECDICT](https://github.com/skywind3000/ECDICT) (MIT, Copyright (c) 2025 Linwei): headwords whose frequency rank is outside the most common 5000 and at most 20000, plus Collins or Oxford headwords outside that band, and inflected forms (`0:` and `p/d/i/3/s/r/t`) that point at those headwords. `dict/build.py` rebuilds `dict/lexicon.json.gz` from a local `ecdict.csv` (`ECDICT_CSV`). Words with no entry, and text inside `script`, `style`, `textarea`, `input`, `select`, `button`, `nav`, `header`, `footer`, links, `code`, `pre`, or an editable element, are left alone.

Candidate filtering and the developer word list (`devwords.txt`) follow the local ECDICT pipeline in [s546126/termwise](https://github.com/s546126/termwise); see [NOTICE.md](../../extensions/termwise/NOTICE.md).

## Translation

Sentence translation calls `https://api.mymemory.translated.net` (no key, declared in `permissions.network`). The target is Chinese, or Japanese when the hub locale is Japanese. A warning, an empty body, or a transport error is shown as a failure under the original sentence. The extension does not replace that failure with text of its own. Successful responses are cached in `reading.json` so the same sentence is not requested again. MyMemory's free quota can run out; the page then shows the failure until a later request succeeds.

## Tests

```sh
go -C extensions/termwise test ./...
```

The tests use a stand-in translator, so they do not call MyMemory. They cover candidate selection, skipped regions, repeated words, idempotent saves, mode switching without leftover marks, the extra paragraph, locales, the browser reader, and the extension protocol.
