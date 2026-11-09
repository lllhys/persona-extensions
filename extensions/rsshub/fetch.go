package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Reading a feed over HTTP: conditional requests (ETag / If-Modified-Since), a size limit, redirects held to the
// same rules as the first address, and errors that never quote the access key.

const (
	bodyMost     = 8 << 20 // bytes of one feed
	fetchTimeout = 25 * time.Second
	redirectMost = 5
	userAgent    = "persona-rsshub/" // + version
)

// newClient is the HTTP client feeds are read with: the environment's proxy (the hub's egress proxy in a sandbox),
// and a dialer that never connects to a link-local or metadata address, whatever a name resolves to.
func newClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip != nil && blockedIP(ip) {
				return fmt.Errorf("不能连接 %s", host)
			}
			return nil
		}}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: fetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= redirectMost {
				return errors.New("跳转太多次")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("跳转到了不是 http(s) 的地址")
			}
			// https may not be downgraded, and a feed may not send the reader on to this machine
			if via[0].URL.Scheme == "https" && req.URL.Scheme == "http" {
				return errors.New("从 https 跳转到了 http")
			}
			if h := req.URL.Hostname(); !strings.EqualFold(h, via[0].URL.Hostname()) {
				if isLoopbackHost(h) || strings.EqualFold(h, sandboxHost) {
					return errors.New("跳转到了本机地址")
				}
				if ip := net.ParseIP(h); ip != nil && blockedIP(ip) {
					return errors.New("跳转到了不能读的地址")
				}
			}
			return nil
		}}
}

// fetchResult is one read of a feed.
type fetchResult struct {
	NotModified  bool
	Body         []byte
	ETag         string
	LastModified string
}

// httpError is an answer that is not a feed: its status, in words people can act on.
type httpError struct {
	Status int
	Detail string // the first line of a plain-text answer (rsshub.app says why it refuses)
}

func (e *httpError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		if e.Detail != "" {
			return fmt.Sprintf("RSSHub 拒绝访问（%d：%s）：公共实例 rsshub.app 会限制部分访问，长期用请换自建实例；自建实例检查访问密钥和它的用法（key / code）", e.Status, e.Detail)
		}
		return fmt.Sprintf("RSSHub 拒绝访问（%d）：公共实例 rsshub.app 会限制部分访问，长期用请换自建实例；自建实例检查访问密钥和它的用法（key / code）", e.Status)
	case http.StatusNotFound:
		return "没有这条路由或订阅源（404）：检查路由的写法，或者这个实例没有这条路由"
	case http.StatusTooManyRequests:
		return "请求太频繁（429），稍后再试；常用的路由可以换自建实例"
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		return fmt.Sprintf("RSSHub 暂时读不到这个来源（%d），稍后再试", e.Status)
	}
	return fmt.Sprintf("服务器回答 %d", e.Status)
}

// fetch reads u, conditionally when etag or lastModified is set.
func fetch(ctx context.Context, c *http.Client, u *url.URL, etag, lastModified, version string) (*fetchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("地址不对")
	}
	req.Header.Set("User-Agent", userAgent+version)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/feed+json, application/json;q=0.9, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.5")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, netError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return &fetchResult{NotModified: true, ETag: firstNonEmpty(resp.Header.Get("ETag"), etag),
			LastModified: firstNonEmpty(resp.Header.Get("Last-Modified"), lastModified)}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		head, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<10))
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, &httpError{Status: resp.StatusCode, Detail: errorDetail(resp.Header.Get("Content-Type"), head)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyMost+1))
	if err != nil {
		return nil, netError(err)
	}
	if len(body) > bodyMost {
		return nil, fmt.Errorf("订阅源太大（超过 %d MB）", bodyMost>>20)
	}
	return &fetchResult{Body: body, ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}, nil
}

// errorDetail is the first line of a plain-text error answer, short; nothing for HTML or other bodies.
func errorDetail(contentType string, head []byte) string {
	if !strings.HasPrefix(strings.ToLower(contentType), "text/plain") || !utf8.Valid(head) {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(head)), "\n")
	return clip(oneLine(line), 160)
}

// netError says what went wrong without the URL (which may carry the access key).
func netError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("连接超时")
	case errors.Is(err, context.Canceled):
		return errors.New("请求被取消")
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return errors.New("连接超时")
	}
	return fmt.Errorf("连不上（%s）", clip(oneLine(err.Error()), 160))
}
