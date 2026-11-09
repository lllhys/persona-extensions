package wechat

import (
	"embed"
	"encoding/json"
	"strings"
)

// What the bot writes for people, in the user's language (en, zh-Hans, ja: locales/<lang>.json, one set of keys). It
// is the plugin's own: the hub's catalogs are not its to read.

//go:embed locales/*.json
var localeFiles embed.FS

// The hub sends one of its own languages (or none): none said is defaultLang, as the hub's default; a hub language the
// plugin has no file for is English, as the hub's fallback for a text a language lacks.
const (
	langEn      = "en"
	langZhHans  = "zh-Hans"
	langJa      = "ja"
	defaultLang = langZhHans
)

// catalogs are locales/<lang>.json by lang: adding a language is adding a file.
var catalogs = func() map[string]map[string]string {
	out := map[string]map[string]string{}
	entries, err := localeFiles.ReadDir("locales")
	if err != nil {
		panic(err)
	}
	for _, e := range entries {
		l, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		raw, err := localeFiles.ReadFile("locales/" + e.Name())
		if err != nil {
			panic(err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(raw, &m); err != nil {
			panic("wechat: locales/" + e.Name() + ": " + err.Error())
		}
		out[l] = m
	}
	return out
}()

// langOf is the language for what the hub said at initialize (hub.locale, one of the hub's languages): that one when
// the plugin has it, defaultLang when none was said, else English.
func langOf(locale string) string {
	if _, ok := catalogs[locale]; ok {
		return locale
	}
	if locale == "" {
		return defaultLang
	}
	return langEn
}

// t is key in l with each {name} replaced by args' name, value next: t(l, "wechat.account", "id", "123456"). A key
// l lacks is English's, then Chinese's, then the key itself.
func t(l, key string, args ...string) string {
	s, ok := catalogs[langOf(l)][key]
	if !ok {
		if s, ok = catalogs[langEn][key]; !ok {
			if s, ok = catalogs[langZhHans][key]; !ok {
				return key
			}
		}
	}
	for i := 0; i+1 < len(args); i += 2 {
		s = strings.ReplaceAll(s, "{"+args[i]+"}", args[i+1])
	}
	return s
}
