package telegram

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
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
// otherwise it is dialed directly, as before. failed hears a dial the proxy failed: gotd retries it until its
// context ends without saying why, so the caller says it instead (#283).
func resolver(failed func(error)) gotddcs.Resolver {
	dial := proxyDial(proxyFromEnvironment)
	return gotddcs.Plain(gotddcs.PlainOptions{Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := dial(ctx, network, addr)
		var pe proxyError
		if errors.As(err, &pe) && ctx.Err() == nil {
			failed(err)
		}
		return c, err
	}})
}

// proxyFromEnvironment is the proxy for a request as the environment says (a variable: tests set it, since
// http.ProxyFromEnvironment reads the environment once per process).
var proxyFromEnvironment = http.ProxyFromEnvironment

// proxyDial dials addr through the proxy proxyOf names for https://addr (nil: directly).
func proxyDial(proxyOf func(*http.Request) (*url.URL, error)) gotddcs.DialFunc {
	var d net.Dialer
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		p, err := proxyOf(&http.Request{URL: &url.URL{Scheme: "https", Host: addr}})
		if err != nil {
			return nil, proxyError{err}
		}
		if p == nil {
			return d.DialContext(ctx, network, addr)
		}
		if p.Scheme != "http" && p.Scheme != "" {
			return nil, proxyError{fmt.Errorf("proxy %s: only http:// proxies are supported", p.Redacted())}
		}
		c, err := connectTunnel(ctx, &d, p, addr)
		if err != nil {
			return nil, proxyError{err}
		}
		return c, nil
	}
}

// proxyError is a dial that failed because of the proxy (its setting, or the proxy itself), not the data center.
type proxyError struct{ error }

func (e proxyError) Unwrap() error { return e.error }

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
