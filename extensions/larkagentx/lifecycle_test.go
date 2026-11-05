package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCachedInboxRespectsTightenedAllowlists(t *testing.T) {
	c, _, _ := fixture(t)
	c.data.Inbox = []thing{{Thread: "chat-test:topic-test", Sender: "sender-test"}, {Thread: "chat-removed", Sender: "sender-test"}, {Thread: "chat-test", Sender: "sender-removed"}}
	out, err := invoke(c, "inbox.collect", map[string]any{})
	if err != nil || len(out.(map[string]any)["things"].([]thing)) != 1 {
		t.Fatal("cached unauthorized content exposed", out, err)
	}
}

func TestImportPromptAndQRCleanup(t *testing.T) {
	c, _, _ := fixture(t)
	c.loginMethod = "import"
	prompt, err := invoke(c, "login.step", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(prompt)
	if !strings.Contains(string(raw), `"secret":true`) || strings.Contains(string(raw), "synthetic-not-a-credential") {
		t.Fatal("unsafe import prompt")
	}
	c.qr = "synthetic-qr-payload"
	if _, err = invoke(c, "logout", map[string]any{}); err != nil || c.qr != "" {
		t.Fatal("QR state retained", err)
	}
}

func TestFailedReauthenticationStopsOldSession(t *testing.T) {
	c, _, old := fixture(t)
	bad := newFakeBridge()
	bad.fail = "session.open"
	c.factory = func(context.Context, []string) (bridge, error) { return bad, nil }
	c.op.Lock()
	err := c.adopt(context.Background(), syntheticSession)
	c.op.Unlock()
	if err == nil || c.state != "not_signed_in" || c.b != nil {
		t.Fatal("failed authentication stayed live", err)
	}
	select {
	case <-old.Done():
	default:
		t.Fatal("old account connection was not stopped")
	}
	if _, err = invoke(c, "actions.do", sendParams("auth-test", "Synthetic text")); err == nil {
		t.Fatal("sent after authentication failure")
	}
}
