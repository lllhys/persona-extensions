package serve

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/nwverse/persona-extensions/extensions/github/internal/connector"
	"github.com/nwverse/persona/sdk/go/ext"
)

// fake is a connector with the capabilities the hub's own GitHub connector had beyond the protocol's first version.
type fake struct {
	opts    json.RawMessage
	online  []string
	choice  connector.AuthChoice
	refused bool
}

func (f *fake) ID() string   { return "github" }
func (f *fake) Name() string { return "GitHub" }
func (f *fake) Manifest() connector.Manifest {
	return connector.Manifest{Capabilities: []connector.Capability{connector.CapStatus, connector.CapInbox, connector.CapSettle, connector.CapOptions, connector.CapMachines}}
}
func (f *fake) Status(context.Context) connector.Status {
	return connector.Status{State: connector.StateOK}
}
func (f *fake) Collect(context.Context) ([]connector.InboxThing, error) {
	return nil, nil
}
func (f *fake) Reply(context.Context, connector.InboxReply) error { return nil }
func (f *fake) Settle(_ context.Context, threads []string) map[string]string {
	out := map[string]string{}
	for _, t := range threads {
		if t == "o/r#1" {
			out[t] = "merged"
		}
	}
	return out
}
func (f *fake) Options() json.RawMessage { return f.opts }
func (f *fake) SetOptions(raw json.RawMessage) error {
	if f.refused {
		return errors.New("refused")
	}
	f.opts = raw
	return nil
}
func (f *fake) MachineOnline(id string) { f.online = append(f.online, id) }
func (f *fake) Auth() connector.AuthState {
	return connector.AuthState{Source: f.choice.Source, Machine: f.choice.Machine}
}
func (f *fake) SetAuth(_ context.Context, ch connector.AuthChoice) (connector.AuthState, error) {
	f.choice = ch
	return f.Auth(), nil
}

func call(t *testing.T, s *server, method, params string) (any, error) {
	t.Helper()
	return s.handle(context.Background(), method, json.RawMessage(params))
}

func TestNewMethods(t *testing.T) {
	f := &fake{opts: json.RawMessage(`{"ci":"mine"}`)}
	s := &server{c: f}
	if got, err := call(t, s, MOptionsGet, `{"contribution":"github"}`); err != nil || string(got.(OptionsResult).Options) != `{"ci":"mine"}` {
		t.Fatalf("options.get: %v %v", got, err)
	}
	if _, err := call(t, s, MOptionsSet, `{"options":{"ci":"all"}}`); err != nil || string(f.opts) != `{"ci":"all"}` {
		t.Fatalf("options.set: %s %v", f.opts, err)
	}
	f.refused = true
	if _, err := call(t, s, MOptionsSet, `{"options":{"ci":"x"}}`); err == nil {
		t.Fatal("refused options came back as fine")
	}
	got, err := call(t, s, MSettle, `{"threads":["o/r#1","o/r#2"]}`)
	if err != nil || !reflect.DeepEqual(got.(SettleResult).Settled, map[string]string{"o/r#1": "merged"}) {
		t.Fatalf("inbox.settle: %v %v", got, err)
	}
	if _, err := call(t, s, MMachineOnline, `{"machine":"m1"}`); err != nil || !reflect.DeepEqual(f.online, []string{"m1"}) {
		t.Fatalf("machines.online: %v %v", f.online, err)
	}
	st, err := call(t, s, MAuthSet, `{"source":"machine","machine":"m1","machine_name":"Laptop"}`)
	if err != nil || st.(connector.AuthState).Machine != "m1" || f.choice.MachineName != "Laptop" {
		t.Fatalf("auth.set: %+v %v", st, err)
	}
	if st, err := call(t, s, MAuthGet, `{}`); err != nil || st.(connector.AuthState).Source != "machine" {
		t.Fatalf("auth.get: %+v %v", st, err)
	}
	if _, err := call(t, s, MOptionsGet, `{"contribution":"other"}`); err == nil {
		t.Fatal("another contribution answered")
	}
}

// A connector without those capabilities says so with the protocol's code.
func TestNewMethodsWithoutTheCapability(t *testing.T) {
	s := &server{c: &bare{}}
	for _, m := range []string{MOptionsGet, MSettle, MAuthGet, MMachineOnline} {
		_, err := call(t, s, m, `{}`)
		var e *ext.Error
		if !errors.As(err, &e) || e.Code != "no_capability" {
			t.Errorf("%s: %v", m, err)
		}
	}
}

type bare struct{}

func (bare) ID() string   { return "github" }
func (bare) Name() string { return "GitHub" }
func (bare) Manifest() connector.Manifest {
	return connector.Manifest{Capabilities: []connector.Capability{connector.CapStatus}}
}
func (bare) Status(context.Context) connector.Status {
	return connector.Status{State: connector.StateOK}
}
