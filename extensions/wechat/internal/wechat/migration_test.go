package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/wechat/internal/wechat/ilink"
	"github.com/nwverse/persona/sdk/go/wire"
)

// This is the built-in's literal sealed JSON shape, not a marshal of the new type.
// All credentials and message IDs are synthetic. A migration must not ask for a QR.
const legacySecret = `{"creds":{"token":"test-bot-token","bot_id":"test-bot@im.bot","user_id":"owner@im.wechat","base_url":"https://ilinkai.weixin.qq.com/"},"old":["test-old-token"],"cursor":"test-legacy-cursor","context":"test-legacy-context","seen":["message:7001"]}`

type migrationTransport struct {
	fake           *fakeILink
	mu             sync.Mutex
	firstCursor    string
	polls, qrCalls int
}

func (m *migrationTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/ilink/bot/getupdates" {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		var p struct {
			Cursor string `json:"get_updates_buf"`
		}
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		m.mu.Lock()
		if m.polls == 0 {
			m.firstCursor = p.Cursor
		}
		m.polls++
		m.mu.Unlock()
	}
	if r.URL.Path == "/ilink/bot/get_bot_qrcode" {
		m.mu.Lock()
		m.qrCalls++
		m.mu.Unlock()
	}
	return m.fake.RoundTrip(r)
}

func TestMigratedSecretRestoresWithoutQRCode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newFakeILink(t)
	transport := &migrationTransport{fake: f}
	s := &memSecrets{data: []byte(legacySecret)}
	b := New(Options{Hub: &fakeHub{}, NewClient: func(base, token string) (*ilink.Client, error) {
		if token != "test-bot-token" {
			t.Error("the old token was not reused")
		}
		return ilink.New(ilink.Options{BaseURL: base, Token: token, HTTP: &http.Client{Transport: transport}})
	}}, s)
	done := make(chan struct{})
	go func() { defer close(done); b.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("poller did not stop")
		}
	})
	eventually(t, "restored status and first poll", func() bool {
		transport.mu.Lock()
		n := transport.polls
		transport.mu.Unlock()
		return n > 0 && b.Status(ctx).State == wire.ConnectorStateOk
	})
	if _, err := b.Send(ctx, wire.ChannelSendParams{Ref: "migration-test", Text: "WeChat migration test", Kind: wire.ChannelSendKindAnswer}); err != nil {
		t.Fatal(err)
	}
	got := f.texts()
	if len(got) != 1 || got[0].Context != "test-legacy-context" || got[0].To != owner {
		t.Fatal("send did not use the old owner and context token")
	}
	transport.mu.Lock()
	cursor, qrCalls := transport.firstCursor, transport.qrCalls
	transport.mu.Unlock()
	if cursor != "test-legacy-cursor" || qrCalls != 0 {
		t.Fatal("the first poll lost the cursor or requested a QR")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("poller did not stop")
	}
	st := s.state(t)
	if st.Creds == nil || st.Creds.Token != "test-bot-token" || st.Context != "test-legacy-context" || !slices.Equal(st.Old, []string{"test-old-token"}) || !slices.Equal(st.Seen, []string{"message:7001"}) {
		t.Fatal("migration changed retained credentials or deduplication state")
	}
}
