package main

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// The Feishu applink (docs/extensions/feishu-sidebar.md): a general link that needs no Feishu app id. mode=sidebar-semi
// opens a half-width sidebar (Feishu 3.41+); window and appCenter are Feishu 7.5+ but this extension uses only
// sidebar-semi. The target URL goes in url as one percent-encoded value, the way JavaScript's encodeURIComponent does.
const applink = "https://applink.feishu.cn/client/web_url/open?mode=sidebar-semi&url="

var (
	errNoHubURL = errors.New("还没有 hub 的对外地址：在这个扩展的设置里填「hub 对外地址」")
	errBadPath  = errors.New("path 要是站内相对路径（以 / 开头、不是完整 URL、不含 ..）")
)

// normalizeBase is the hub address without its trailing slash, or why it cannot be one. A path already in the
// address (a hub behind a prefix) is kept; only a trailing slash is dropped so the path below joins cleanly.
func normalizeBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errNoHubURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("「%s」不是 http(s) 地址", clip(raw, 80))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("「%s」不是 http(s) 地址", clip(raw, 80))
	}
	return strings.TrimRight(raw, "/"), nil
}

// validPath says whether p is a same-site relative path: it starts with one /, is not a full or scheme-relative URL,
// and has no .. segment. Query and hash are kept as they are (a .. there is not a path step).
func validPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.Contains(p, "://") {
		return false
	}
	before, _, _ := strings.Cut(p, "?")
	before, _, _ = strings.Cut(before, "#")
	for _, seg := range strings.Split(before, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// buildLink is the Feishu applink for path (a same-site relative path) on the hub's address; target is the page it
// opens.
func (a *app) buildLink(path string) (link, target string, err error) {
	base, err := normalizeBase(a.hubURL)
	if err != nil {
		return "", "", err
	}
	if path == "" {
		path = "/"
	}
	if !validPath(path) {
		return "", "", errBadPath
	}
	target = base + path
	return applink + uriComponent(target), target, nil
}

// uriComponent percent-encodes s the way JavaScript's encodeURIComponent does: everything but A-Z a-z 0-9 - _ . ! ~
// * ' ( ) becomes %XX, each byte of UTF-8 on its own. The applink's url parameter wants exactly this.
func uriComponent(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; uriSafe(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
}

func uriSafe(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
		c == '-' || c == '_' || c == '.' || c == '!' || c == '~' || c == '*' || c == '\'' || c == '(' || c == ')'
}

// clip cuts s to at most n characters, on a rune boundary.
func clip(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
