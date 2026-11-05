package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	if mode == "oversize" {
		// One byte more than the scanner's max token size: read() must
		// fail ErrTooLong and close the bridge (fail closed).
		fmt.Fprintf(os.Stdout, "%s\n", strings.Repeat("x", frameScanLimit+1))
		os.Exit(0)
	}
	if mode == "malformed" {
		// A line that is not JSON (an SSH banner or shell garbage),
		// followed by a valid event frame. read() must tear down on the
		// bad line and never process the frame after it. The helper stays
		// alive until its stdin closes, so a reader that wrongly skipped
		// the bad line would keep scanning instead of tearing down.
		fmt.Println("SSH-2.0-synthetic-banner this is not json")
		fmt.Println(`{"event":"message","message":{"id":"message-test","chat":"chat-test","sender":"sender-test","text":"Synthetic event after garbage"}}`)
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	fmt.Fprintln(os.Stderr, "synthetic-secret-must-not-be-forwarded")
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 4096), frameScanLimit)
	for sc.Scan() {
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(sc.Bytes(), &req)
		if req.Method == "fail" {
			fmt.Printf("{\"id\":%d,\"error\":{\"code\":\"synthetic-private-exception\"}}\n", req.ID)
			continue
		}
		if req.Method == "frame.echo" {
			var p struct {
				Size int `json:"size"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.Size > 0 {
				prefix := fmt.Appendf(nil, `{"id":%d,"result":{"pad":"`, req.ID)
				suffix := []byte(`"}}`)
				filler := []byte(strings.Repeat("x", p.Size-len(prefix)-len(suffix)))
				fmt.Fprintf(os.Stdout, "%s%s%s\n", prefix, filler, suffix)
				continue
			}
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

// TestFrameLimitBoundary mirrors the Python companion rule (bridge.py:
// len(frame) > LIMIT rejects): a JSON payload of exactly frameLimit bytes
// must pass in both directions, one byte more must be rejected. Everything
// runs over a synthetic in-process re-exec, no network.
func TestFrameLimitBoundary(t *testing.T) {
	t.Setenv("PERSONA_LARKAGENTX_TEST_HELPER", "normal")
	start := func(t *testing.T) bridge {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		b, err := startBridge(ctx, []string{os.Args[0], "-test.run=^TestCompanionProcessHelper$"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(b.Close)
		return b
	}

	t.Run("outbound exact limit is sent and over limit is rejected", func(t *testing.T) {
		b := start(t)
		// A fresh bridge assigns id 1; map keys marshal in the same order
		// Call uses, so this measures the exact wire size of the request.
		measure, err := json.Marshal(map[string]any{
			"id": 1, "method": "frame.echo", "params": map[string]string{"z": ""},
		})
		if err != nil {
			t.Fatal(err)
		}
		exact := map[string]string{"z": strings.Repeat("x", frameLimit-len(measure))}
		if raw, _ := json.Marshal(map[string]any{
			"id": 1, "method": "frame.echo", "params": exact,
		}); len(raw) != frameLimit {
			t.Fatalf("synthetic frame is %d bytes, want %d", len(raw), frameLimit)
		}
		var ok struct {
			State string `json:"state"`
		}
		if err = b.Call(context.Background(), "frame.echo", exact, &ok); err != nil || ok.State != "ok" {
			t.Fatal("exact-limit frame was not sent", err, ok)
		}
		over := map[string]string{"z": strings.Repeat("x", frameLimit-len(measure)+1)}
		if raw, _ := json.Marshal(map[string]any{
			"id": 2, "method": "frame.echo", "params": over,
		}); len(raw) != frameLimit+1 {
			t.Fatalf("synthetic frame is %d bytes, want %d", len(raw), frameLimit+1)
		}
		if err = b.Call(context.Background(), "frame.echo", over, nil); err == nil {
			t.Fatal("frame one byte over the limit was accepted")
		}
		// The rejected frame must not have killed the bridge.
		if err = b.Call(context.Background(), "session.ping", nil, &ok); err != nil || ok.State != "ok" {
			t.Fatal("bridge did not survive an over-limit frame", err)
		}
	})

	t.Run("inbound exact limit is scanned", func(t *testing.T) {
		b := start(t) // fresh bridge, first request gets id 1
		var reply struct {
			Pad string `json:"pad"`
		}
		if err := b.Call(context.Background(), "frame.echo", map[string]int{"size": frameLimit}, &reply); err != nil {
			t.Fatal("exact-limit inbound frame broke the scanner", err)
		}
		prefix, suffix := `{"id":1,"result":{"pad":"`, `"}}`
		if strings.Trim(reply.Pad, "x") != "" || len(prefix)+len(reply.Pad)+len(suffix) != frameLimit {
			t.Fatalf("inbound reply was not a %d-byte frame", frameLimit)
		}
	})

	t.Run("inbound beyond scanner limit closes the bridge", func(t *testing.T) {
		t.Setenv("PERSONA_LARKAGENTX_TEST_HELPER", "oversize")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		b, err := startBridge(ctx, []string{os.Args[0], "-test.run=^TestCompanionProcessHelper$"})
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		select {
		case <-b.Done():
		case <-time.After(7 * time.Second):
			t.Fatal("oversized inbound frame did not stop the reader")
		}
	})
}

// TestMalformedFrameFailsClosed pins the read() contract in transport.go:
// any inbound line that is not valid JSON must tear the bridge down instead
// of being skipped. The helper emits garbage, then a valid event frame, and
// stays alive; tolerating the bad line would deliver that frame and keep
// Done() open.
func TestMalformedFrameFailsClosed(t *testing.T) {
	t.Setenv("PERSONA_LARKAGENTX_TEST_HELPER", "malformed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, err := startBridge(ctx, []string{os.Args[0], "-test.run=^TestCompanionProcessHelper$"})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	select {
	case <-b.Done():
	case <-time.After(7 * time.Second):
		t.Fatal("reader tolerated a non-JSON line instead of tearing down")
	}
	select {
	case ev := <-b.Events():
		t.Fatalf("frame after the garbage line was processed: %+v", ev)
	default:
	}
	if err = b.Call(ctx, "session.ping", nil, nil); err == nil {
		t.Fatal("call on the torn-down bridge succeeded")
	}
}

// TestMalformedCompanionOutputMakesConnectorUnavailable watches the same
// teardown through the connector: watch() must detach the dead bridge and
// move the connector out of "ok", so a confirmed send is refused.
func TestMalformedCompanionOutputMakesConnectorUnavailable(t *testing.T) {
	t.Setenv("PERSONA_LARKAGENTX_TEST_HELPER", "malformed")
	c, _, _ := fixture(t)
	c.argv = []string{os.Args[0], "-test.run=^TestCompanionProcessHelper$"}
	c.factory = startBridge
	c.op.Lock()
	c.detach()
	b, err := c.create()
	if err != nil {
		c.op.Unlock()
		t.Fatal(err)
	}
	c.mu.Lock()
	c.state = "ok"
	c.mu.Unlock()
	c.op.Unlock()
	select {
	case <-b.Done():
	case <-time.After(7 * time.Second):
		t.Fatal("bridge tolerated a non-JSON line instead of tearing down")
	}
	deadline := time.Now().Add(7 * time.Second)
	for {
		c.mu.Lock()
		state, attached := c.state, c.b != nil
		c.mu.Unlock()
		if state == "error" && !attached {
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("connector stayed available: state %q, bridge attached %v", state, attached)
		}
		time.Sleep(time.Millisecond)
	}
	status, err := invoke(c, "status", map[string]any{})
	if err != nil || status.(map[string]any)["state"] != "error" {
		t.Fatal("connector did not report the error state", status, err)
	}
	if _, err = invoke(c, "actions.do", sendParams("malformed-output-test", "Synthetic text")); err == nil {
		t.Fatal("send accepted after malformed companion output")
	}
}
