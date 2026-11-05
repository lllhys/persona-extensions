package telegram

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// A picture is fetched from the data center it is kept on (#1272, measured in #1255), as Telegram's own apps do. Asked
// on the account's own data center first, Telegram answers FILE_MIGRATE and gotd goes on to the right one: a request
// more for every picture there, and the first one to a data center also connects to it, makes its key and transfers
// the authorization (about 4 s), all of it inside the one request the gate let through, so everything else waited.
// Now a picture whose data center is known goes there on that data center's own connection (dcPools): connecting
// happens outside the gate, only the authorization's export is a request through it, like every request on the
// connection. Still gotd's MTProto over TCP to the data center (telegram.Client.DC): nothing over HTTP.

// dcPools are the connections to the data centers other than the account's, one each, made when a picture there is
// first fetched (none made in advance). Pictures on one data center wait for its one connection; different ones connect
// at the same time. A connection that could not be made is not kept: the next picture tries again.
type dcPools struct {
	// open connects to a data center and transfers the authorization to it (telegram.Client.DC); only its export goes
	// through the gate.
	open func(ctx context.Context, dc int) (tg.Invoker, error)
	gate *gate

	mu    sync.Mutex
	pools map[int]*dcPool
}

type dcPool struct {
	ready chan struct{} // closed once inv or err is set
	inv   tg.Invoker
	err   error
}

// get is data center dc's connection, made now if there is none; every request on it goes through the gate. During a
// flood wait it makes nothing and asks nothing.
func (d *dcPools) get(ctx context.Context, dc int) (tg.Invoker, error) {
	if left := d.gate.left(); left > 0 {
		return nil, heldError{left}
	}
	d.mu.Lock()
	p, ok := d.pools[dc]
	if !ok {
		if d.pools == nil {
			d.pools = map[int]*dcPool{}
		}
		p = &dcPool{ready: make(chan struct{})}
		d.pools[dc] = p
	}
	d.mu.Unlock()
	if ok {
		select {
		case <-p.ready:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	} else {
		start := time.Now()
		p.inv, p.err = d.open(ctx, dc)
		traceOf(ctx).update(func(t *fetchTrace) { t.connect += time.Since(start) })
		if p.err != nil {
			d.mu.Lock()
			delete(d.pools, dc)
			d.mu.Unlock()
		}
		close(p.ready)
	}
	if p.err != nil {
		return nil, p.err
	}
	return gatedInvoker{inv: p.inv, gate: d.gate}, nil
}

// close closes the connections made (when the client stops).
func (d *dcPools) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for dc, p := range d.pools {
		select {
		case <-p.ready:
			if c, ok := p.inv.(io.Closer); ok {
				_ = c.Close()
			}
		default: // still connecting: its client is going away with it
		}
		delete(d.pools, dc)
	}
}

// gatedInvoker sends each request on a data center's connection through the gate, as the client's middleware does on
// the account's own.
type gatedInvoker struct {
	inv  tg.Invoker
	gate *gate
}

func (g gatedInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	if inGate(ctx) {
		return g.inv.Invoke(ctx, input, output)
	}
	return g.gate.do(ctx, func(ctx context.Context) error { return g.inv.Invoke(ctx, input, output) })
}

// fetchFrom downloads f, a part (512 KB) a request: from its own data center's connection when that is not the
// account's (home), else on the account's (main), where gotd follows FILE_MIGRATE for a picture whose data center is
// not known. One that has moved since (FILE_MIGRATE from its data center) is asked on the account's too.
func fetchFrom(ctx context.Context, f *fileLoc, home int, main tg.Invoker, pools *dcPools, most int) ([]byte, error) {
	if f.dc != 0 && f.dc != home && pools != nil {
		traceOf(ctx).update(func(t *fetchTrace) { t.from = f.dc })
		inv, err := pools.get(ctx, f.dc)
		if err != nil {
			return nil, err
		}
		data, err := download(ctx, inv, f, most)
		if !tgerr.Is(err, "FILE_MIGRATE") {
			return data, err
		}
	}
	return download(ctx, main, f, most)
}

func download(ctx context.Context, inv tg.Invoker, f *fileLoc, most int) ([]byte, error) {
	w := &capped{n: most}
	if _, err := downloader.NewDownloader().Download(tg.NewClient(inv), f.loc).Stream(ctx, w); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return w.buf.Bytes(), nil
}
