package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCompanionProcessHelper(t *testing.T) {
	mode := os.Getenv("PERSONA_LARKAGENTX_TEST_HELPER")
	if mode == "" {
		return
	}
	if mode == "stall" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	fmt.Fprintln(os.Stderr, "synthetic-secret-must-not-be-forwarded")
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal(sc.Bytes(), &req)
		if req.Method == "fail" {
			fmt.Printf("{\"id\":%d,\"error\":{\"code\":\"synthetic-private-exception\"}}\n", req.ID)
			continue
		}
		if req.Method == "event" {
			fmt.Println(`{"event":"message","message":{"id":"message-test","chat":"chat-test","sender":"sender-test","text":"Synthetic event"}}`)
		}
		fmt.Printf("{\"id\":%d,\"result\":{\"state\":\"ok\"}}\n", req.ID)
	}
	os.Exit(0)
}

func TestStdioBridgeProtocolAndShutdown(t *testing.T) {
	t.Setenv("PERSONA_LARKAGENTX_TEST_HELPER", "normal")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, err := startBridge(ctx, []string{os.Args[0], "-test.run=^TestCompanionProcessHelper$"})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var result struct {
		State string `json:"state"`
	}
	if err = b.Call(ctx, "event", map[string]string{"credentials": "synthetic"}, &result); err != nil || result.State != "ok" {
		t.Fatal(err, result)
	}
	select {
	case ev := <-b.Events():
		if ev.Message.Text != "Synthetic event" {
			t.Fatal(ev)
		}
	case <-time.After(time.Second):
		t.Fatal("missing event")
	}
	if err = b.Call(ctx, "fail", nil, nil); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("leaked companion error", err)
	}
	b.Close()
	select {
	case <-b.Done():
	case <-time.After(7 * time.Second):
		t.Fatal("did not exit on EOF")
	}
}

func TestBlockedCompanionHasBoundedTimeout(t *testing.T) {
	t.Setenv("PERSONA_LARKAGENTX_TEST_HELPER", "stall")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, err := startBridge(ctx, []string{os.Args[0], "-test.run=^TestCompanionProcessHelper$"})
	if err != nil {
		t.Fatal(err)
	}
	callCtx, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if err = b.Call(callCtx, "session.open", strings.Repeat("x", 65536), nil); err == nil {
		t.Fatal("stalled write succeeded")
	}
	select {
	case <-b.Done():
	case <-time.After(7 * time.Second):
		t.Fatal("stalled child did not stop")
	}
}
