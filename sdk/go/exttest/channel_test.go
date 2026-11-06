package exttest_test

import (
	"context"
	"sync"
	"testing"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

var channelManifest = []byte(`{
  "manifest_version": 1, "id": "examplebot", "name": "Example Bot", "version": "1.0.0",
  "engines": {"extension_protocol": 1},
  "runtime": {"command": "bin/examplebot"},
  "activation_events": ["on_startup"],
  "contributes": {"channel": [{"id": "examplebot", "name": "Example Bot", "kind": "im",
    "capabilities": {"format": "markdown", "max_text": 4000, "asks": "text", "push": true, "typing": true}}]}
}`)

// exampleBot is a channel written with the SDK alone: it sends each ref once, and hands over the owner's message
// it "heard" as soon as it starts.
type exampleBot struct {
	mu   sync.Mutex
	sent map[string]wire.ChannelSendResult
}

func (b *exampleBot) program() ext.Program {
	return ext.Program{ID: "examplebot", Version: "1.0.0", Manifest: channelManifest,
		Init: func(ctx context.Context, in *ext.Init, hub *ext.Hub) (map[string]any, error) {
			go func() {
				_ = hub.ChannelStatus(ctx, wire.ChannelStatusParams{Contribution: "examplebot", State: wire.ChannelStateConnected,
					Account: &wire.ChannelAccount{Name: "Example A"}})
				_ = hub.ChannelReceive(ctx, "examplebot", wire.ChannelMessage{ID: "msg_1", Text: "Hello"})
			}()
			return nil, nil
		},
		Namespaces: map[string]ext.Handler{ext.ChannelNamespace: ext.Methods{
			"send": ext.Method(func(ctx context.Context, p wire.ChannelSendParams) (wire.ChannelSendResult, error) {
				if p.Ref == "" {
					return wire.ChannelSendResult{}, &ext.Error{Message: "channel.send without a ref"}
				}
				b.mu.Lock()
				defer b.mu.Unlock()
				if r, ok := b.sent[p.Ref]; ok {
					return r, nil
				}
				r := wire.ChannelSendResult{MessageID: "out_" + p.Ref}
				b.sent[p.Ref] = r
				return r, nil
			}),
			"typing": ext.Method(func(ctx context.Context, p wire.ChannelTypingParams) (ext.Empty, error) {
				return ext.Empty{}, nil
			}),
		}.For("examplebot")},
	}
}

func TestAChannelWrittenWithTheSDK(t *testing.T) {
	b := &exampleBot{sent: map[string]wire.ChannelSendResult{}}
	h := exttest.CheckChannels(t, channelManifest, exttest.Serve(t, b.program()))
	waitFor(t, func() bool { return len(h.Received()) > 0 })
	if got := h.Received()[0]; got.Contribution != "examplebot" || got.Message.ID != "msg_1" || got.Message.Text != "Hello" {
		t.Fatalf("received %+v", got)
	}
	if len(b.sent) != 1 {
		t.Fatalf("sent %v: the same ref goes out once", b.sent)
	}
}

func TestCannotSendIsItsCode(t *testing.T) {
	var e *ext.Error
	if err := ext.CannotSend("nowhere yet"); !asError(err, &e) || e.Code != "cannot_send" {
		t.Fatalf("%#v", err)
	}
}

func asError(err error, out **ext.Error) bool {
	e, ok := err.(*ext.Error)
	*out = e
	return ok
}
