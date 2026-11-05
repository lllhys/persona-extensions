package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"
	"time"
)

func TestGoToPythonSyntheticProtocol(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python is required only for development checks", err)
	}
	c, h, _ := fixture(t)
	c.chats = []string{"chat-a"}
	c.senders = []string{"sender-a"}
	c.argv = []string{python, "-I", "-B", "protocol_fixture.py"}
	c.factory = startBridge
	c.op.Lock()
	err = c.adopt(context.Background(), json.RawMessage(`{"region":"feishu","cookies":{"session":"SYNTHETIC_COOKIE"}}`))
	c.op.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		c.mu.Lock()
		n := len(c.data.Inbox)
		c.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("bridge did not adapt synthetic event")
		}
		time.Sleep(time.Millisecond)
	}
	c.op.Lock()
	var check struct {
		State string `json:"state"`
	}
	err = c.b.Call(context.Background(), "session.check", nil, &check)
	c.op.Unlock()
	if err != nil || check.State != "ok" {
		t.Fatal("nil params wire compatibility", err, check)
	}
	p := map[string]any{"name": sendAction, "request_id": "python-send-test", "args": sendArgs{Chat: "chat-a", Text: "Synthetic confirmed text"}}
	if _, err = invoke(c, "actions.do", p); err != nil {
		t.Fatal("plain send without optional anchor", err)
	}
	if _, err = invoke(c, "actions.do", p); err != nil {
		t.Fatal("duplicate receipt", err)
	}
	if _, err = invoke(c, "logout", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.data) != 0 {
		t.Fatal("logout retained session")
	}
}
