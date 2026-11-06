package telegram

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
)

// dialGotd connects a gotd client with c and keeps it running until ctx ends (it does not tell whether Telegram knows c:
// Check does). gotd logs nothing: its logger only feeds a picture's trace (trace.go). Every request goes through g,
// gotd's own too (its config, the sign-in's).
func dialGotd(ctx context.Context, c creds, store sessionStore, g *gate, updated func()) (client, error) {
	cl := telegram.NewClient(c.id, c.hash, telegram.Options{
		SessionStorage: gotdSession{store},
		Resolver:       resolver(), // through HTTPS_PROXY when there is one (proxy.go)
		Middlewares:    []telegram.Middleware{gated(g)},
		Logger:         traceLog{},
		OnTransfer:     timedTransfer,
		// How the session shows in the user's Telegram devices.
		Device: telegram.DeviceConfig{DeviceModel: "Persona", SystemVersion: runtime.GOOS, AppVersion: "Persona"},
		// What Telegram pushes only kicks the archive and the inbox: they read what is new themselves, by message id,
		// so a gap in the updates loses nothing.
		UpdateHandler: telegram.UpdateHandlerFunc(func(context.Context, tg.UpdatesClass) error {
			updated()
			return nil
		}),
	})
	gc := &gotdClient{c: cl, creds: c, done: make(chan struct{})}
	gc.pools = &dcPools{gate: g, open: func(ctx context.Context, dc int) (tg.Invoker, error) {
		pool, err := cl.DC(ctx, dc, 1)
		if err != nil {
			return nil, err
		}
		return pool, nil
	}}
	ready := make(chan error, 1)
	go func() {
		defer close(gc.done)
		defer gc.pools.close()
		err := cl.Run(ctx, func(ctx context.Context) error {
			ready <- nil
			<-ctx.Done()
			return ctx.Err()
		})
		select {
		case ready <- err: // it failed before it was ready
		default:
		}
	}()
	select {
	case err := <-ready:
		if err != nil {
			return nil, err
		}
		return gc, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// gated sends every request of the client through g. One gotd makes while another is in the gate (a data center's
// authorization transfer for a request sent on) is part of that one: it goes straight on, or it would wait for itself.
// An authorization's export is timed for the trace.
func gated(g *gate) telegram.Middleware {
	return telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
		return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			if _, export := input.(*tg.AuthExportAuthorizationRequest); export && traceOf(ctx) != nil {
				start := time.Now()
				defer func() { traceOf(ctx).update(func(t *fetchTrace) { t.export += time.Since(start) }) }()
			}
			if inGate(ctx) {
				return next.Invoke(ctx, input, output)
			}
			return g.do(ctx, func(ctx context.Context) error { return next.Invoke(ctx, input, output) })
		}
	})
}

// gotdSession is gotd's session.Storage on the connector's store.
type gotdSession struct{ s sessionStore }

func (g gotdSession) LoadSession(ctx context.Context) ([]byte, error) {
	data, err := g.s.LoadSession(ctx)
	if errors.Is(err, errNoSession) {
		return nil, session.ErrNotFound
	}
	return data, err
}

func (g gotdSession) StoreSession(ctx context.Context, data []byte) error {
	return g.s.StoreSession(ctx, data)
}

type gotdClient struct {
	c     *telegram.Client
	creds creds
	done  chan struct{}
	pools *dcPools // the other data centers' connections, for pictures kept there (dc.go)
}

func (g *gotdClient) Done() <-chan struct{} { return g.done }

func (g *gotdClient) Check(ctx context.Context) error {
	_, err := g.c.API().AuthExportLoginToken(ctx, &tg.AuthExportLoginTokenRequest{APIID: g.creds.id, APIHash: g.creds.hash})
	return err
}

func (g *gotdClient) SendCode(ctx context.Context, phone string) (string, string, error) {
	sent, err := g.c.Auth().SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return "", "", err
	}
	code, ok := sent.(*tg.AuthSentCode)
	if !ok {
		return "", "", errors.New("Telegram signed in without a code")
	}
	via := "验证码已经发出"
	switch code.Type.(type) {
	case *tg.AuthSentCodeTypeApp:
		via = "验证码发到了你别的 Telegram 设备上（Telegram 官方账号发来的消息）"
	case *tg.AuthSentCodeTypeSMS:
		via = "验证码用短信发到了这个号码"
	case *tg.AuthSentCodeTypeCall, *tg.AuthSentCodeTypeFlashCall, *tg.AuthSentCodeTypeMissedCall:
		via = "Telegram 会打电话告诉你验证码"
	}
	return code.PhoneCodeHash, via, nil
}

func (g *gotdClient) SignIn(ctx context.Context, phone, code, hash string) (string, error) {
	if _, err := g.c.Auth().SignIn(ctx, phone, code, hash); err != nil {
		if errors.Is(err, auth.ErrPasswordAuthNeeded) {
			return "", errPasswordNeeded
		}
		return "", err
	}
	return g.self(ctx)
}

func (g *gotdClient) PasswordHint(ctx context.Context) (string, error) {
	p, err := g.c.API().AccountGetPassword(ctx)
	if err != nil {
		return "", err
	}
	return p.Hint, nil
}

func (g *gotdClient) Password(ctx context.Context, password string) (string, error) {
	if _, err := g.c.Auth().Password(ctx, password); err != nil {
		return "", err
	}
	return g.self(ctx)
}

func (g *gotdClient) Me(ctx context.Context) (string, bool, error) {
	st, err := g.c.Auth().Status(ctx)
	if err != nil || !st.Authorized {
		return "", false, err
	}
	return name(st.User), true, nil
}

func (g *gotdClient) LogOut(ctx context.Context) error {
	_, err := g.c.API().AuthLogOut(ctx)
	return err
}

func (g *gotdClient) self(ctx context.Context) (string, error) {
	u, err := g.c.Self(ctx)
	if err != nil {
		return "", err
	}
	return name(u), nil
}

// name is how the account shows: the display name, and @username when there is one.
func name(u *tg.User) string {
	if u == nil {
		return ""
	}
	n := strings.TrimSpace(u.FirstName + " " + u.LastName)
	if u.Username != "" {
		if n == "" {
			return "@" + u.Username
		}
		n += "（@" + u.Username + "）"
	}
	return n
}
