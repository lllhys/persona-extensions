package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestURICComponent(t *testing.T) {
	for in, want := range map[string]string{
		"https://persona.example.com/": "https%3A%2F%2Fpersona.example.com%2F",
		"话":                            "%E8%AF%9D",
		"对":                            "%E5%AF%B9",
		"a b":                          "a%20b",
		"-_.!~*'()":                    "-_.!~*'()",
	} {
		if got := uriComponent(in); got != want {
			t.Errorf("uriComponent(%q) = %q, want %q", in, got, want)
		}
	}
}

// applinkURL is the applink's url parameter, read back the way any URL parser (Feishu's included) reads it.
func applinkURL(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("%q: %v", link, err)
	}
	return u.Query().Get("url")
}

func TestBuildLink(t *testing.T) {
	a := testApp(t, `{"hub_url": "https://persona.example.com", "path": "/"}`)
	for path, want := range map[string]string{
		"/":                   "https://persona.example.com/",
		"/j/abc":              "https://persona.example.com/j/abc",
		"/j/abc?x=1&y=2#frag": "https://persona.example.com/j/abc?x=1&y=2#frag",
		"/j/对话?q=中文&x=1#片段":   "https://persona.example.com/j/对话?q=中文&x=1#片段",
	} {
		link, target, err := a.buildLink(path)
		if err != nil {
			t.Fatalf("%q: %v", path, err)
		}
		if !strings.HasPrefix(link, "https://applink.feishu.cn/client/web_url/open?mode=sidebar-semi&url=") {
			t.Errorf("%q: link %q", path, link)
		}
		if target != want {
			t.Errorf("%q: target %q, want %q", path, target, want)
		}
		if got := applinkURL(t, link); got != want {
			t.Errorf("%q: url decodes to %q, want %q", path, got, want)
		}
	}
}

func TestBuildLinkNormalizesTrailingSlash(t *testing.T) {
	a := testApp(t, `{"hub_url": "https://persona.example.com/", "path": "/j/abc"}`)
	link, target, err := a.buildLink("/j/abc")
	if err != nil {
		t.Fatal(err)
	}
	if target != "https://persona.example.com/j/abc" {
		t.Errorf("target %q", target)
	}
	if got := applinkURL(t, link); got != target {
		t.Errorf("decoded %q", got)
	}
}

func TestBuildLinkNoAddress(t *testing.T) {
	for _, a := range []*app{testApp(t, ""), testApp(t, `{"path": "/"}`)} {
		if _, _, err := a.buildLink("/"); err == nil {
			t.Errorf("no address answered")
		}
	}
}

func TestBuildLinkBadPath(t *testing.T) {
	a := testApp(t, `{"hub_url": "https://persona.example.com"}`)
	for _, path := range []string{"j/abc", "https://evil.com/x", "//evil.com", "/j/../secret"} {
		if _, _, err := a.buildLink(path); err == nil {
			t.Errorf("buildLink(%q) answered", path)
		}
	}
}

func TestNormalizeBase(t *testing.T) {
	for in, want := range map[string]string{
		"https://persona.example.com":       "https://persona.example.com",
		"https://persona.example.com/":      "https://persona.example.com",
		"http://192.168.1.5:8080/":          "http://192.168.1.5:8080",
		"https://persona.example.com/base/": "https://persona.example.com/base",
	} {
		got, err := normalizeBase(in)
		if err != nil || got != want {
			t.Errorf("normalizeBase(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "persona.example.com", "ftp://x", "https://", "not a url"} {
		if _, err := normalizeBase(in); err == nil {
			t.Errorf("normalizeBase(%q) answered", in)
		}
	}
}

func TestValidPath(t *testing.T) {
	for _, ok := range []string{"/", "/j/abc", "/j/abc?x=1&y=2#frag", "/j/对话?q=中文", "/a/./b", "/j/abc?redirect=../x"} {
		if !validPath(ok) {
			t.Errorf("validPath(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "j/abc", "https://evil.com/x", "http://evil.com", "//evil.com", "/j/../secret", "/../etc", "file:///etc/passwd"} {
		if validPath(bad) {
			t.Errorf("validPath(%q) = true", bad)
		}
	}
}
