package telegram

import (
	"context"
	"fmt"
	stdlog "log"
	"strings"
	"sync"
	"time"

	gotdlog "github.com/gotd/log"
	"github.com/gotd/td/telegram"
)

// What one picture cost (#1255: why an avatar is slow the first time), a line in the connector's log for each Media:
// how long it waited in the gate and how long Telegram took, whether Telegram sent it on to another data center
// (FILE_MIGRATE) and a connection was made there, how long the authorization transfer took. Times and data center
// numbers only: never a ref, an id, a name or the picture.

type fetchTrace struct {
	mu       sync.Mutex
	home     int           // the account's data center (0: not known)
	from     int           // the data center it was asked on
	requests int           // through the gate
	queued   time.Duration // waiting for the request before it, and the gap after it (gate.do)
	call     time.Duration // the requests themselves, with all gotd did inside them
	sentOn   []int         // data centers Telegram sent a request on to (FILE_MIGRATE)
	opened   []int         // data centers a new connection was made to
	connect  time.Duration // making one, outside the gate but for its export (dcPools.get)
	transfer time.Duration // authorization transfers to them: export and import
	export   time.Duration // the exports among them
}

type traceKey struct{}

// traceLogf writes the line (tests read it).
var traceLogf = stdlog.Printf

func withTrace(ctx context.Context) (context.Context, *fetchTrace) {
	t := &fetchTrace{}
	return context.WithValue(ctx, traceKey{}, t), t
}

// traceOf is ctx's trace: nil when nothing is traced.
func traceOf(ctx context.Context) *fetchTrace {
	t, _ := ctx.Value(traceKey{}).(*fetchTrace)
	return t
}

func (t *fetchTrace) update(f func(*fetchTrace)) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	f(t)
}

// line is the log's words for a picture of kind that took took and ended in err; "" when it asked Telegram nothing
// (a person no page has read: their initial).
func (t *fetchTrace) line(kind string, took time.Duration, err error) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.requests == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Telegram %s: %s", kind, secs(took))
	if err != nil {
		b.WriteString(" (failed)")
	}
	if t.from > 0 {
		fmt.Fprintf(&b, "; from DC %d", t.from)
	}
	if t.home > 0 {
		fmt.Fprintf(&b, " (account on DC %d)", t.home)
	}
	fmt.Fprintf(&b, "; %d request(s), queued %s, in Telegram %s", t.requests, secs(t.queued), secs(t.call))
	if len(t.sentOn) == 0 {
		b.WriteString("; not sent on")
	} else {
		fmt.Fprintf(&b, "; sent on to DC %s", dcs(t.sentOn))
	}
	if len(t.opened) > 0 {
		fmt.Fprintf(&b, "; new connection to DC %s", dcs(t.opened))
		if t.connect > 0 {
			fmt.Fprintf(&b, " in %s outside the gate", secs(t.connect))
		}
		fmt.Fprintf(&b, ", authorization transfer %s (export %s)", secs(t.transfer), secs(t.export))
	}
	return b.String()
}

func secs(d time.Duration) string { return fmt.Sprintf("%.2f s", d.Seconds()) }

func dcs(ns []int) string {
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = fmt.Sprint(n)
	}
	return strings.Join(s, ", ")
}

// traceLog is gotd's logger: on only for a traced request, it takes the data center from gotd's own two records of
// one (a request sent on, a connection made) and says nothing itself. gotd's words are matched as v0.163 writes them;
// should they change, the line only stops naming the data centers.
type traceLog struct{}

func (traceLog) Enabled(ctx context.Context, _ gotdlog.Level) bool { return traceOf(ctx) != nil }

func (traceLog) Log(ctx context.Context, _ gotdlog.Level, msg string, attrs ...gotdlog.Attr) {
	t := traceOf(ctx)
	if t == nil {
		return
	}
	key := map[string]string{"Invoking on target DC": "target_dc", "Creating pool": "dc_id"}[msg]
	for _, a := range attrs {
		if key == "" || a.Key != key {
			continue
		}
		dc := int(a.Value.Int64())
		t.update(func(t *fetchTrace) {
			if key == "target_dc" {
				t.sentOn = append(t.sentOn, dc)
			} else {
				t.opened = append(t.opened, dc)
			}
		})
	}
}

// timedTransfer is gotd's OnTransfer: it times an authorization transfer (export, then import) for the trace.
func timedTransfer(ctx context.Context, _ *telegram.Client, fn func(context.Context) error) error {
	start := time.Now()
	err := fn(ctx)
	traceOf(ctx).update(func(t *fetchTrace) { t.transfer += time.Since(start) })
	return err
}
