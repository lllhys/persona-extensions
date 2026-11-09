package main

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// The RSSHub instance and the sources a user subscribes to: a route of the instance (/github/issue/owner/repo) or the
// address of any RSS, Atom or JSON feed.

const (
	defaultInstance = "https://rsshub.app"
	// sandboxHost is the name the hub's egress proxy connects to the hub machine's 127.0.0.1 (a sandbox has a
	// loopback of its own, and Go's ProxyFromEnvironment never proxies loopback addresses).
	sandboxHost = "host.persona.internal"
	routeMost   = 1024
)

var errAddress = errors.New("RSSHub 实例要是 https 网址；同一台机器上的实例（127.0.0.1、localhost、[::1]）可以用 http")

// isLoopbackHost reports whether h names this machine's loopback: 127.0.0.1 (any 127/8), localhost or ::1.
func isLoopbackHost(h string) bool {
	h = strings.ToLower(strings.TrimSuffix(strings.Trim(strings.TrimSpace(h), "[]"), "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// instance is the RSSHub instance the settings name.
type instance struct {
	base *url.URL // scheme and host, with a path prefix when the instance is mounted under one; no trailing slash
	key  string   // access key (secret), "" when none
	code bool     // send ?code=md5(route+key) instead of ?key=
	json bool     // ask for ?format=json
}

// parseInstance reads the instance_url setting: https to any host, or http to a loopback host on this machine, with
// no user info, query or fragment. An empty value is the public instance.
func parseInstance(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultInstance
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errAddress
	}
	u.Scheme = strings.ToLower(u.Scheme)
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && isLoopbackHost(u.Hostname()):
	default:
		return nil, errAddress
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

// proxied reports whether the environment points HTTP at a proxy (the hub's egress proxy in a sandbox).
func proxied(getenv func(string) string) bool {
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "ALL_PROXY", "all_proxy"} {
		if getenv(k) != "" {
			return true
		}
	}
	return false
}

// dialURL is the address to request for u: with a proxy in the environment, an http loopback host becomes
// host.persona.internal with the same port, which the proxy connects to the hub machine's loopback. https keeps its
// host, so the certificate still matches.
func dialURL(u *url.URL, getenv func(string) string) *url.URL {
	if u == nil || u.Scheme != "http" || !isLoopbackHost(u.Hostname()) || !proxied(getenv) {
		return u
	}
	cp := *u
	if port := u.Port(); port != "" {
		cp.Host = net.JoinHostPort(sandboxHost, port)
	} else {
		cp.Host = sandboxHost
	}
	return &cp
}

// source is what a subscription or a preview reads: a route of the instance or a feed's own address.
type source struct {
	Route string // "/github/issue/owner/repo?limit=10", or ""
	URL   string // "https://example.com/feed.xml", or ""
}

func (s source) String() string {
	if s.Route != "" {
		return s.Route
	}
	return s.URL
}

// parseSource reads what a user or the assistant wrote: a route (/a/b, a/b, rsshub://a/b), a link to the instance
// (taken as its route, so it follows a later change of instance), or the address of another feed.
func parseSource(raw string, base *url.URL) (source, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return source{}, errors.New("要写一条 RSSHub 路由（如 /github/issue/nwverse/persona）或订阅源的网址")
	}
	if len(s) > routeMost || strings.ContainsAny(s, " \t\r\n") {
		return source{}, fmt.Errorf("「%s」不是路由也不是网址", clip(s, 80))
	}
	if rest, ok := strings.CutPrefix(s, "rsshub://"); ok {
		s = "/" + rest
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" || u.User != nil {
			return source{}, fmt.Errorf("「%s」不是网址", clip(s, 80))
		}
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		u.Fragment = ""
		if base != nil && u.Host == base.Host && (base.Path == "" || u.Path == base.Path || strings.HasPrefix(u.Path, base.Path+"/")) {
			return routeSource(strings.TrimPrefix(u.EscapedPath(), base.Path), u.RawQuery)
		}
		if err := feedAddressOK(u); err != nil {
			return source{}, err
		}
		return source{URL: u.String()}, nil
	}
	if strings.Contains(s, "://") {
		return source{}, fmt.Errorf("「%s」只能是 http(s) 网址或 RSSHub 路由", clip(s, 80))
	}
	path, query, _ := strings.Cut(s, "?")
	return routeSource(path, query)
}

func routeSource(path, query string) (source, error) {
	path = "/" + strings.Trim(path, "/")
	if path == "/" {
		return source{}, errors.New("路由不能是空的，要写成 /github/issue/nwverse/persona 这样")
	}
	for _, seg := range strings.Split(path[1:], "/") {
		if seg == "" || seg == "." || seg == ".." {
			return source{}, fmt.Errorf("路由「%s」里有空的或 .. 的一段", clip(path, 80))
		}
	}
	if _, err := url.ParseRequestURI(path); err != nil {
		return source{}, fmt.Errorf("路由「%s」不对", clip(path, 80))
	}
	if query != "" {
		q, err := url.ParseQuery(query)
		if err != nil {
			return source{}, fmt.Errorf("路由的参数「%s」不对", clip(query, 80))
		}
		// the access key is the extension's to add: one written into a route would be kept and shown
		q.Del("key")
		q.Del("code")
		if enc := q.Encode(); enc != "" {
			return source{Route: path + "?" + enc}, nil
		}
	}
	return source{Route: path}, nil
}

// feedAddressOK refuses a feed address that leads to this machine or to a link-local or metadata address by its
// literal host (the instance's own loopback is a route, not a feed address). Names are checked again when dialled.
func feedAddressOK(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("订阅源只能是 http(s) 网址")
	}
	h := u.Hostname()
	if isLoopbackHost(h) || strings.EqualFold(h, sandboxHost) {
		return errors.New("本机的订阅源要通过设置里的 RSSHub 实例地址来读")
	}
	if ip := net.ParseIP(h); ip != nil && blockedIP(ip) {
		return fmt.Errorf("不能读这个地址（%s）", h)
	}
	return nil
}

// blockedIP is an address a feed may never be read from: link-local (cloud metadata included), multicast,
// unspecified, and the well-known metadata addresses.
func blockedIP(ip net.IP) bool {
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, s := range []string{"100.100.100.200", "fd00:ec2::254"} {
		if ip.Equal(net.ParseIP(s)) {
			return true
		}
	}
	return false
}

// accessCode is RSSHub's ?code=: the hex md5 of the route's path and the access key.
func accessCode(path, key string) string {
	sum := md5.Sum([]byte(path + key))
	return hex.EncodeToString(sum[:])
}

// url is the address to read src at, with the access key and the format added for a route of the instance.
func (in instance) url(src source) (*url.URL, error) {
	if src.Route == "" {
		u, err := url.Parse(src.URL)
		if err != nil {
			return nil, err
		}
		return u, nil
	}
	path, query, _ := strings.Cut(src.Route, "?")
	q, _ := url.ParseQuery(query)
	if in.json && q.Get("format") == "" {
		q.Set("format", "json")
	}
	if in.key != "" {
		if in.code {
			q.Set("code", accessCode(path, in.key))
		} else {
			q.Set("key", in.key)
		}
	}
	u := *in.base
	u.Path = in.base.Path + path
	u.RawPath = ""
	u.RawQuery = q.Encode()
	return &u, nil
}

// link is src's address as people may see it: never the access key.
func (in instance) link(src source) string {
	if src.Route == "" {
		return src.URL
	}
	return strings.TrimRight(in.base.String(), "/") + src.Route
}

// redact removes the access key and code from a text (an error that quotes a URL).
func (in instance) redact(s string) string {
	if in.key == "" {
		return s
	}
	s = strings.ReplaceAll(s, in.key, "***")
	if esc := url.QueryEscape(in.key); esc != in.key {
		s = strings.ReplaceAll(s, esc, "***")
	}
	return s
}

// scrub drops links that carry the access key: RSSHub echoes the request's own address (with ?key=) in places, and
// nothing kept or shown may hold the key.
func (in instance) scrub(f *parsedFeed) {
	if in.key == "" || f == nil {
		return
	}
	has := func(s string) bool { return in.redact(s) != s }
	if has(f.Link) {
		f.Link = ""
	}
	for i := range f.Entries {
		e := &f.Entries[i]
		if has(e.Link) {
			e.Link = ""
		}
		if has(e.Key) {
			e.Key = "h:" + hashText(in.redact(e.Key))
		}
		e.Title, e.Summary, e.Content = in.redact(e.Title), in.redact(e.Summary), in.redact(e.Content)
	}
}
