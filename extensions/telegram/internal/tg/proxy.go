package telegram

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	gotddcs "github.com/gotd/td/telegram/dcs"
)

// resolver is how the client reaches Telegram's data centers. gotd dials them with a bare net.Dialer, which never
// reads HTTPS_PROXY / ALL_PROXY: on a hub that only gets out through a proxy every login timed out. When the
// environment names a proxy for https://<data center>, the connection goes through an HTTP CONNECT tunnel to it;
// otherwise it is dialed directly, as before.
func resolver() gotddcs.Resolver {
	return gotddcs.Plain(gotddcs.PlainOptions{Dial: proxyDial(http.ProxyFromEnvironment)})
}

// proxyDial dials addr through the proxy proxyOf names for https://addr (nil: directly).
func proxyDial(proxyOf func(*http.Request) (*url.URL, error)) gotddcs.DialFunc {
	var d net.Dialer
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		p, err := proxyOf(&http.Request{URL: &url.URL{Scheme: "https", Host: addr}})
		if err != nil {
			return nil, err
		}
		if p == nil {
			return d.DialContext(ctx, network, addr)
		}
		if p.Scheme != "http" && p.Scheme != "" {
			return nil, fmt.Errorf("proxy %s: only http:// proxies are supported", p.Redacted())
		}
		return connectTunnel(ctx, &d, p, addr)
	}
}

// connectTunnel opens a CONNECT tunnel to addr through the HTTP proxy p.
func connectTunnel(ctx context.Context, d *net.Dialer, p *url.URL, addr string) (net.Conn, error) {
	host := p.Host
	if p.Port() == "" {
		host = net.JoinHostPort(p.Hostname(), "80")
	}
	c, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("proxy %s: %w", host, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	} else {
		_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	}
	req := "CONNECT " + addr + " HTTP/1.1\r\nHost: " + addr + "\r\n"
	if u := p.User; u != nil {
		pw, _ := u.Password()
		req += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(u.Username()+":"+pw)) + "\r\n"
	}
	if _, err := c.Write([]byte(req + "\r\n")); err != nil {
		c.Close()
		return nil, fmt.Errorf("proxy %s: %w", host, err)
	}
	br := bufio.NewReader(c)
	res, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("proxy %s: %w", host, err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		c.Close()
		return nil, fmt.Errorf("proxy %s: CONNECT %s: %s", host, addr, res.Status)
	}
	_ = c.SetDeadline(time.Time{})
	if br.Buffered() > 0 { // the proxy sent bytes past its answer: keep them
		return &bufferedConn{Conn: c, r: br}, nil
	}
	return c, nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }
