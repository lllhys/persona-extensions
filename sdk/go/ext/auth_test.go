package ext

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nwverse/persona/sdk/go/wire"
)

type fakeAuth struct{ in bool }

func (f *fakeAuth) Status(context.Context) (wire.AuthStatus, error) {
	if f.in {
		return wire.AuthStatus{State: "signed_in", Account: "me"}, nil
	}
	return wire.AuthStatus{State: "signed_out"}, nil
}

func (f *fakeAuth) Step(_ context.Context, step string, values map[string]string) (wire.AuthPrompt, error) {
	if step == "" {
		return wire.AuthPrompt{Step: "code", Fields: []wire.AuthField{{Name: "code", Label: "验证码"}}}, nil
	}
	if values["code"] != "42" {
		return wire.AuthPrompt{Step: "code", Error: "不对"}, nil
	}
	f.in = true
	return wire.AuthPrompt{Done: true, Account: "me"}, nil
}

func (f *fakeAuth) Logout(ctx context.Context) (wire.AuthStatus, error) {
	f.in = false
	return f.Status(ctx)
}

func TestAuthHandler(t *testing.T) {
	ctx := context.Background()
	h := AuthHandler(&fakeAuth{})
	out, _ := h(ctx, "step", json.RawMessage(`{"step": ""}`))
	if p := out.(wire.AuthPrompt); p.Step != "code" || len(p.Fields) != 1 {
		t.Fatal(p)
	}
	out, _ = h(ctx, "step", json.RawMessage(`{"step": "code", "values": {"code": "42"}}`))
	if p := out.(wire.AuthPrompt); !p.Done {
		t.Fatal(p)
	}
	if out, _ := h(ctx, "status", nil); out.(wire.AuthStatus).State != "signed_in" {
		t.Fatal(out)
	}
	if out, _ := h(ctx, "logout", nil); out.(wire.AuthStatus).State != "signed_out" {
		t.Fatal(out)
	}
	if _, err := h(ctx, "nope", nil); err == nil {
		t.Fatal("unknown method")
	}
}
