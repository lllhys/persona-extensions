package connector

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Egress is how a connector reaches the services it reads: Proxy, when set, is an http://, https://, socks5:// or
// socks5h:// [user:pass@]host:port proxy (Go's transport speaks all four)
// for everything the connector sends out (some networks reach a service only through one). Empty uses the proxy the
// program's environment gives (the hub's egress proxy when the extension runs in the sandbox), else direct.
type Egress struct {
	Proxy string `json:"proxy,omitempty"`
}

// HTTPClient is the connector's client: through the proxy when one is set. A malformed proxy is an error, never a
// silent fall back to direct.
func (e Egress) HTTPClient(timeout time.Duration) (*http.Client, error) {
	u, err := ParseProxy(e.Proxy)
	if err != nil {
		return nil, err
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = http.ProxyFromEnvironment
	if u != nil {
		t.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Timeout: timeout, Transport: t}, nil
}

// ParseProxy reads a proxy URL of one of proxySchemes; "" (or blanks) is none (nil). Errors never show a password.
func ParseProxy(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("proxy must be http(s):// or socks5(h)://[user:pass@]host:port: %v", redactErr(raw, err))
	}
	if !slices.Contains(proxySchemes, u.Scheme) || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return nil, fmt.Errorf("proxy must be http(s):// or socks5(h)://[user:pass@]host:port, got %q", u.Redacted())
	}
	return u, nil
}

// redactErr keeps a password in raw out of a parse error's text.
func redactErr(raw string, err error) string {
	s := err.Error()
	if i := strings.Index(raw, "://"); i >= 0 {
		if j := strings.LastIndex(raw, "@"); j > i {
			s = strings.ReplaceAll(s, raw[i+3:j], "xxxxx")
		}
	}
	return s
}

// proxySchemes are the proxies Go's http.Transport dials (an https one is reached over TLS).
var proxySchemes = []string{"http", "https", "socks5", "socks5h"}
