package main

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// network is how the extension reaches Discord: through the proxy the user set (the secret proxy: http://,
// https://, socks5:// or socks5h://, which Go's HTTP client speaks), else as the environment says (HTTPS_PROXY; in the sandbox
// that is the hub's egress proxy, which lets only the manifest's hosts through). A proxy that does not work fails
// every request rather than being bypassed.
type network struct {
	proxy *url.URL
}

// parseProxy reads the proxy secret. Its errors never repeat the value, which can carry a password.
func parseProxy(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("not a URL")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, errors.New("not http://, https://, socks5:// or socks5h://") // never echo it: a pasted "user:password@host" parses as a scheme
	}
	if u.Hostname() == "" || u.Port() == "" {
		return nil, errors.New("no host or port")
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" {
		return nil, errors.New("a path or query")
	}
	return u, nil
}

// httpClient is the client for Discord's REST API. Every request also has the deadline of its call.
func (n network) httpClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone() // Proxy: the environment's
	if n.proxy != nil {
		tr.Proxy = http.ProxyURL(n.proxy)
	}
	return &http.Client{Transport: tr, Timeout: 45 * time.Second}
}
