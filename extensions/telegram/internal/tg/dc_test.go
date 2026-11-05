package telegram

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// fakeDCs is a made-up Telegram for pictures (dc.go): data centers answering upload.getFile with a picture's bytes, or
// FILE_MIGRATE when it is kept on another, each counting the requests it got. Connecting to one (open) sends the
// authorization's export through the gate, then takes connect outside it, as gotd's Client.DC does; the account's own
// connection (main) goes through the gate and follows FILE_MIGRATE, as gotd's client does.
type fakeDCs struct {
	gate    *gate
	home    int
	connect time.Duration

	mu       sync.Mutex
	files    map[int64]fakeFile // by photo id
	got      map[int]int        // upload.getFile requests per data center
	opens    map[int]int
	exports  int
	failOpen int // opens to fail
}

type fakeFile struct {
	dc   int
	data string
}

func newFakeDCs(home int, connect time.Duration, files map[int64]fakeFile) *fakeDCs {
	g := newGate()
	g.pace = func() time.Duration { return 5 * time.Millisecond }
	return &fakeDCs{gate: g, home: home, connect: connect, files: files, got: map[int]int{}, opens: map[int]int{}}
}

func (f *fakeDCs) pools() *dcPools { return &dcPools{gate: f.gate, open: f.open} }

func (f *fakeDCs) open(ctx context.Context, dc int) (tg.Invoker, error) {
	f.mu.Lock()
	f.opens[dc]++
	fail := f.failOpen > 0
	f.failOpen--
	f.mu.Unlock()
	export := func(context.Context) error { f.mu.Lock(); f.exports++; f.mu.Unlock(); return nil }
	if err := (gatedInvoker{inv: invokerFunc(func(ctx context.Context, _ bin.Encoder, _ bin.Decoder) error { return export(ctx) }),
		gate: f.gate}).Invoke(ctx, nil, nil); err != nil { // through the gate, as the client's middleware sends it
		return nil, err
	}
	time.Sleep(f.connect) // connecting, making the key, importing the authorization: outside the gate
	if fail {
		return nil, errors.New("transfer: made-up failure")
	}
	return fakeDC{f, dc}, nil
}

// main is the account's own connection.
func (f *fakeDCs) main() tg.Invoker { return gatedInvoker{inv: following{f}, gate: f.gate} }

func (f *fakeDCs) counts() (got map[int]int, opens map[int]int, exports int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	got, opens = map[int]int{}, map[int]int{}
	for k, v := range f.got {
		got[k] = v
	}
	for k, v := range f.opens {
		opens[k] = v
	}
	return got, opens, f.exports
}

type fakeDC struct {
	f  *fakeDCs
	dc int
}

func (d fakeDC) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	req, ok := input.(*tg.UploadGetFileRequest)
	if !ok {
		return fmt.Errorf("unexpected %T", input)
	}
	d.f.mu.Lock()
	d.f.got[d.dc]++
	file := d.f.files[req.Location.(*tg.InputPeerPhotoFileLocation).PhotoID]
	d.f.mu.Unlock()
	if file.dc != d.dc {
		return tgerr.New(303, fmt.Sprintf("FILE_MIGRATE_%d", file.dc))
	}
	part := file.data[min(int(req.Offset), len(file.data)):min(int(req.Offset)+req.Limit, len(file.data))]
	var b bin.Buffer
	if err := (&tg.UploadFile{Type: &tg.StorageFileJpeg{}, Bytes: []byte(part)}).Encode(&b); err != nil {
		return err
	}
	return output.Decode(&b)
}

// following asks the account's data center and, told FILE_MIGRATE, the one named, inside the same request.
type following struct{ f *fakeDCs }

func (m following) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	err := fakeDC{m.f, m.f.home}.Invoke(ctx, input, output)
	if rpc, ok := tgerr.As(err); ok && rpc.Type == "FILE_MIGRATE" {
		return fakeDC{m.f, rpc.Argument}.Invoke(ctx, input, output)
	}
	return err
}

func avatarOn(photo int64, dc int) *fileLoc {
	return avatarLoc(peer{Kind: 'u', ID: 1, Hash: 11}, photo, dc)
}

// fetch fetches loc as the connector does, with its trace; a failure is the test's (from any goroutine).
func fetch(t *testing.T, f *fakeDCs, pools *dcPools, loc *fileLoc) (string, *fetchTrace) {
	t.Helper()
	ctx, tr := withTrace(context.Background())
	data, err := fetchFrom(ctx, loc, f.home, f.main(), pools, 1<<20)
	if err != nil {
		t.Errorf("dc %d: %v", loc.dc, err)
	}
	return string(data), tr
}

// A picture on another data center is asked there, on its own connection: one request a picture, nothing on the
// account's. The first one there also exports the authorization (the one request connecting makes). Without its data
// center it is asked on the account's first, as before: two requests.
func TestFetchGoesToItsDataCenter(t *testing.T) {
	f := newFakeDCs(5, 10*time.Millisecond, map[int64]fakeFile{501: {4, "avatar 501"}, 502: {4, "avatar 502"}, 503: {4, "avatar 503"}})
	pools := f.pools()
	data, tr := fetch(t, f, pools, avatarOn(501, 4))
	if data != "avatar 501" || tr.requests != 2 || tr.from != 4 || tr.connect < 10*time.Millisecond {
		t.Fatalf("first: %q, trace %+v", data, tr)
	}
	data, tr = fetch(t, f, pools, avatarOn(502, 4))
	if data != "avatar 502" || tr.requests != 1 || tr.connect != 0 {
		t.Fatalf("second: %q, trace %+v", data, tr)
	}
	if got, opens, exports := f.counts(); fmt.Sprint(got) != "map[4:2]" || fmt.Sprint(opens) != "map[4:1]" || exports != 1 {
		t.Fatalf("got %v, opens %v, exports %d", got, opens, exports)
	}
	if _, tr := fetch(t, f, pools, avatarOn(503, 0)); tr.requests != 1 { // gotd's following is inside the one request
		t.Fatalf("unknown: trace %+v", tr)
	}
	if got, _, _ := f.counts(); fmt.Sprint(got) != "map[4:3 5:1]" {
		t.Fatalf("not known: got %v", got)
	}
}

// Connecting to a data center (seconds, on the real one) does not hold the gate: other requests go on meanwhile.
func TestConnectingLeavesTheGateOpen(t *testing.T) {
	f := newFakeDCs(5, 400*time.Millisecond, map[int64]fakeFile{501: {4, "avatar 501"}})
	done := make(chan struct{})
	go func() { defer close(done); fetch(t, f, f.pools(), avatarOn(501, 4)) }()
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(time.Millisecond) {
		if _, _, exports := f.counts(); exports == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never connected")
		}
	}
	start := time.Now()
	if err := f.gate.do(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 150*time.Millisecond {
		t.Fatalf("another request waited %v for the connection", took)
	}
	<-done
}

// Pictures on two data centers connect to both at once; three on one connect to it once.
func TestDataCentersConnectAtOnce(t *testing.T) {
	f := newFakeDCs(5, 300*time.Millisecond, map[int64]fakeFile{501: {4, "a"}, 502: {2, "b"}, 503: {4, "c"}, 504: {4, "d"}})
	pools := f.pools()
	start := time.Now()
	var wg sync.WaitGroup
	for _, l := range []*fileLoc{avatarOn(501, 4), avatarOn(502, 2), avatarOn(503, 4), avatarOn(504, 4)} {
		wg.Go(func() { fetch(t, f, pools, l) })
	}
	wg.Wait()
	if took := time.Since(start); took > 550*time.Millisecond {
		t.Fatalf("took %v: connected one after the other", took)
	}
	if got, opens, exports := f.counts(); fmt.Sprint(opens) != "map[2:1 4:1]" || exports != 2 || fmt.Sprint(got) != "map[2:1 4:3]" {
		t.Fatalf("got %v, opens %v, exports %d", got, opens, exports)
	}
}

// During a flood wait nothing goes to Telegram: no connecting, no request, on any data center.
func TestNoRequestDuringAFloodWait(t *testing.T) {
	f := newFakeDCs(5, 10*time.Millisecond, map[int64]fakeFile{501: {4, "a"}, 502: {5, "b"}})
	f.gate.until = time.Now().Add(time.Hour)
	for _, l := range []*fileLoc{avatarOn(501, 4), avatarOn(502, 5), avatarOn(501, 0)} {
		if _, err := fetchFrom(context.Background(), l, f.home, f.main(), f.pools(), 1<<20); !errors.As(err, new(heldError)) {
			t.Fatalf("dc %d: %v", l.dc, err)
		}
	}
	if got, opens, exports := f.counts(); len(got) != 0 || len(opens) != 0 || exports != 0 {
		t.Fatalf("got %v, opens %v, exports %d", got, opens, exports)
	}
}

// The account's own data center's pictures stay on its connection; one that moved is asked there too (gotd follows);
// a connection that could not be made is made again next time.
func TestFetchFallsBack(t *testing.T) {
	f := newFakeDCs(5, 10*time.Millisecond, map[int64]fakeFile{501: {5, "home"}, 502: {2, "moved"}, 503: {3, "c"}})
	pools := f.pools()
	if data, tr := fetch(t, f, pools, avatarOn(501, 5)); data != "home" || tr.requests != 1 {
		t.Fatalf("home: %q %+v", data, tr)
	}
	if data, _ := fetch(t, f, pools, avatarOn(502, 4)); data != "moved" {
		t.Fatalf("moved: %q", data)
	}
	if got, opens, _ := f.counts(); fmt.Sprint(got) != "map[2:1 4:1 5:2]" || fmt.Sprint(opens) != "map[4:1]" {
		t.Fatalf("got %v, opens %v", got, opens)
	}
	f.failOpen = 1
	if _, err := fetchFrom(context.Background(), avatarOn(503, 3), f.home, f.main(), pools, 1<<20); err == nil {
		t.Fatal("a failed connection fetched")
	}
	if data, _ := fetch(t, f, pools, avatarOn(503, 3)); data != "c" {
		t.Fatalf("again: %q", data)
	}
	if _, opens, _ := f.counts(); opens[3] != 2 {
		t.Fatalf("opens %v", opens)
	}
}
