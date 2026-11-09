package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// translateHost is the only translation service this extension calls.
// A failed response is shown as a failure; it is never replaced with a made-up translation.
const translateHost = "api.mymemory.translated.net"

type translator interface {
	Translate(ctx context.Context, text, target string) (string, error)
}

type myMemory struct {
	client *http.Client
}

func (m myMemory) Translate(ctx context.Context, text, target string) (string, error) {
	client := m.client
	if client == nil {
		client = &http.Client{Timeout: 6 * time.Second}
	}
	lang := "zh-CN"
	if strings.HasPrefix(strings.ToLower(target), "ja") {
		lang = "ja"
	}
	q := url.Values{}
	q.Set("q", text)
	q.Set("langpair", "en|"+lang)
	endpoint := "https://" + translateHost + "/get?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "persona-termwise/"+about.Version)
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return m.parseTranslation(body, res.StatusCode)
}

// parseTranslation accepts only a completed translation. Quota warnings and
// empty bodies are errors, so the page can show the failure instead of the warning text.
func (myMemory) parseTranslation(body []byte, status int) (string, error) {
	if status != http.StatusOK {
		return "", fmt.Errorf("translator HTTP %d", status)
	}
	var payload struct {
		ResponseStatus int `json:"responseStatus"`
		ResponseData   struct {
			TranslatedText string `json:"translatedText"`
		} `json:"responseData"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("translator response")
	}
	out := strings.TrimSpace(payload.ResponseData.TranslatedText)
	upper := strings.ToUpper(out)
	if payload.ResponseStatus != http.StatusOK || out == "" || strings.Contains(upper, "MYMEMORY WARNING") || strings.Contains(upper, "QUERY LENGTH LIMIT") {
		return "", fmt.Errorf("translator status %d", payload.ResponseStatus)
	}
	return clipBytes(out, 1800), nil
}

// translateCached returns a stored translation or asks the translator and stores a success.
func (a *app) translateCached(ctx context.Context, text, target string) (string, error) {
	key := target + "\n" + text
	if got, ok := a.store.translation(key); ok {
		return got, nil
	}
	out, err := a.tr.Translate(ctx, text, target)
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", fmt.Errorf("translator returned no usable text")
	}
	a.store.rememberTranslation(key, out)
	return out, nil
}

func translationLang(locale string) (tag, target string) {
	if strings.HasPrefix(strings.ToLower(locale), "ja") {
		return "ja", "ja"
	}
	return "zh-Hans", "zh-CN"
}

func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	if n <= 0 {
		return ""
	}
	return s[:n]
}

func oneLine(err error) string {
	if err == nil {
		return ""
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	return clipBytes(s, 180)
}
