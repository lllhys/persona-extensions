package main

import "strings"

// Page chrome follows the hub locale (zh-Hans, en, ja). An empty or unknown locale
// is zh-Hans, matching the hub's default. The article itself stays English.

type texts struct {
	title                           string
	modeTranslation, modeVocabulary string
	current, saved, save, close     string
	wordsTitle, savedTitle          string
	times, savedCount               string
	more, less                      string
	failed, failedBare              string
	attribution, reading            string
}

var locales = map[string]*texts{
	"zh-Hans": {
		title: "阅读", modeTranslation: "翻译模式", modeVocabulary: "生词模式",
		current: "当前", saved: "已记下", save: "记下这个词", close: "收起",
		wordsTitle: "生词", savedTitle: "已记的词",
		times: "出现 %d 次", savedCount: "已记下 %d",
		more: "再显示一段", less: "收起新增的一段",
		failed: "翻译没有成功：%s", failedBare: "翻译没有成功",
		attribution: "释义来自 ECDICT（MIT，Linwei）。", reading: "正文",
	},
	"en": {
		title: "Reading", modeTranslation: "Translation", modeVocabulary: "Vocabulary",
		current: "On", saved: "Saved", save: "Save word", close: "Close",
		wordsTitle: "Words", savedTitle: "Saved words",
		times: "%d times", savedCount: "Saved %d",
		more: "Show another paragraph", less: "Hide the extra paragraph",
		failed: "Translation failed: %s", failedBare: "Translation failed",
		attribution: "Glosses from ECDICT (MIT), Linwei.", reading: "Article",
	},
	"ja": {
		title: "読書", modeTranslation: "翻訳モード", modeVocabulary: "単語モード",
		current: "選択中", saved: "記録済み", save: "この単語を覚える", close: "閉じる",
		wordsTitle: "単語", savedTitle: "記録した単語",
		times: "%d 回", savedCount: "記録 %d",
		more: "もう一段表示", less: "追加の段を隠す",
		failed: "翻訳に失敗しました：%s", failedBare: "翻訳に失敗しました",
		attribution: "語義は ECDICT（MIT、Linwei）によります。", reading: "本文",
	},
}

func textsFor(locale string) *texts {
	loc := strings.ToLower(strings.TrimSpace(locale))
	switch {
	case strings.HasPrefix(loc, "en"):
		return locales["en"]
	case strings.HasPrefix(loc, "ja"):
		return locales["ja"]
	default:
		return locales["zh-Hans"]
	}
}
