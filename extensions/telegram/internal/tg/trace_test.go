package telegram

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	gotdlog "github.com/gotd/log"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

// Each picture fetched says in the log how it went through the gate, in times and data center numbers: never its
// ref, a person's or chat's id, or the picture. A picture that asked Telegram nothing says nothing.
func TestMediaTrace(t *testing.T) {
	c, _, ctx := threadAccount(t)
	var lines []string
	was := traceLogf
	traceLogf = func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	t.Cleanup(func() { traceLogf = was })
	if _, err := c.Thread(ctx, "c2", "", 30); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Media(ctx, "a.u1.501"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Media(ctx, "m.c2.12.f"); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "Telegram avatar: ") || !strings.Contains(lines[0], "; 1 request(s), queued ") ||
		!strings.Contains(lines[0], "; not sent on") || !strings.HasPrefix(lines[1], "Telegram message picture: ") {
		t.Fatalf("lines %q", lines)
	}
	for _, l := range lines {
		for _, secret := range []string{"u1", "501", "c2", "12.f", "made-up"} {
			if strings.Contains(l, secret) {
				t.Errorf("%q says %q", l, secret)
			}
		}
	}
	lines = nil
	if _, err := c.Media(ctx, "a.u9.509"); err == nil || len(lines) != 0 {
		t.Fatalf("a person no page read: %v, lines %q", err, lines)
	}
}

// gotd's own records of a request sent on to another data center and a connection made there give the numbers, the
// attributes it attaches with With too; nothing else is taken, and without a trace the logger is off.
func TestTraceLog(t *testing.T) {
	if (traceLog{}).Enabled(context.Background(), gotdlog.LevelDebug) {
		t.Fatal("on without a trace")
	}
	ctx, tr := withTrace(context.Background())
	l := gotdlog.With(traceLog{}, gotdlog.String("error_type", "FILE_MIGRATE"), gotdlog.Int("target_dc", 4))
	if !l.Enabled(ctx, gotdlog.LevelDebug) {
		t.Fatal("off for a trace")
	}
	l.Log(ctx, gotdlog.LevelDebug, "Invoking on target DC")
	traceLog{}.Log(ctx, gotdlog.LevelDebug, "Creating pool", gotdlog.Int("dc_id", 4), gotdlog.Int64("max", 1))
	traceLog{}.Log(ctx, gotdlog.LevelDebug, "Something else", gotdlog.Int("dc_id", 9))
	if fmt.Sprint(tr.sentOn, tr.opened) != "[4] [4]" {
		t.Fatalf("sent on %v, opened %v", tr.sentOn, tr.opened)
	}
}

// An authorization transfer is timed as a whole (OnTransfer), and its export inside the request that needed it
// (the middleware): the request is still one through the gate.
func TestTraceTransfer(t *testing.T) {
	ctx, tr := withTrace(context.Background())
	if err := timedTransfer(ctx, nil, func(context.Context) error { time.Sleep(15 * time.Millisecond); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := timedTransfer(context.Background(), nil, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err) // nothing traced: nothing to time
	}
	g := newGate()
	g.pace = func() time.Duration { return 0 }
	var h handler
	h.invoke = gated(g).Handle(invokerFunc(func(ctx context.Context, input bin.Encoder, _ bin.Decoder) error {
		if _, file := input.(*tg.UploadGetFileRequest); file {
			return h.invoke(ctx, &tg.AuthExportAuthorizationRequest{DCID: 4}, nil) // as gotd does on FILE_MIGRATE
		}
		time.Sleep(10 * time.Millisecond)
		return nil
	}))
	if err := h.invoke(ctx, &tg.UploadGetFileRequest{}, nil); err != nil {
		t.Fatal(err)
	}
	if tr.requests != 1 || tr.export < 10*time.Millisecond || tr.call < tr.export || tr.transfer < 15*time.Millisecond {
		t.Fatalf("trace %+v", tr)
	}
}

// The line: a picture asked on the account's data center and sent on (as before #1272), and one asked on its own
// data center's new connection.
func TestTraceLine(t *testing.T) {
	tr := &fetchTrace{home: 5, from: 5, requests: 1, queued: 620 * time.Millisecond, call: 4100 * time.Millisecond, sentOn: []int{4}, opened: []int{4},
		transfer: 1900 * time.Millisecond, export: 410 * time.Millisecond}
	want := "Telegram avatar: 4.78 s; from DC 5 (account on DC 5); 1 request(s), queued 0.62 s, in Telegram 4.10 s; sent on to DC 4; " +
		"new connection to DC 4, authorization transfer 1.90 s (export 0.41 s)"
	if got := tr.line("avatar", 4780*time.Millisecond, nil); got != want {
		t.Fatalf("%q\nwant %q", got, want)
	}
	tr = &fetchTrace{home: 5, from: 4, requests: 2, queued: 10 * time.Millisecond, call: 800 * time.Millisecond, opened: []int{4},
		connect: 4200 * time.Millisecond, transfer: 3900 * time.Millisecond, export: 350 * time.Millisecond}
	want = "Telegram avatar: 4.70 s; from DC 4 (account on DC 5); 2 request(s), queued 0.01 s, in Telegram 0.80 s; not sent on; " +
		"new connection to DC 4 in 4.20 s outside the gate, authorization transfer 3.90 s (export 0.35 s)"
	if got := tr.line("avatar", 4700*time.Millisecond, nil); got != want {
		t.Fatalf("%q\nwant %q", got, want)
	}
}

// handler lets the invoker below call the middleware it is behind, as gotd's client does for a transfer.
type handler struct {
	invoke func(ctx context.Context, input bin.Encoder, output bin.Decoder) error
}

type invokerFunc func(ctx context.Context, input bin.Encoder, output bin.Decoder) error

func (f invokerFunc) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	return f(ctx, input, output)
}
