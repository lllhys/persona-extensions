package kbsrc

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/wire"
)

// fakeHub is the hub's side of an extension connection (JSON lines over pipes) answering kb.update with a profile
// and kb.get with profiles by writer, as hub main does after nwverse/persona#5934; old plays a hub before it (the
// profile is dropped silently, kb.get has no profiles). Placeholder data only.
type fakeHub struct {
	t        *testing.T
	old      bool
	strict   bool // with old: a hub before #5934 as it is, decoding strictly: an unknown field is refused, nothing runs
	mu       sync.Mutex
	profiles map[string]map[string]ProfileRecord
	updates  []json.RawMessage
}

type frameT struct {
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *ext.Error      `json:"error,omitempty"`
}

// connect starts the extension side with ext.Serve and answers its calls; it returns the *ext.Hub the extension got.
func (f *fakeHub) connect(ctx context.Context) *ext.Hub {
	toExt, fromHub := io.Pipe()
	fromExt, toHub := io.Pipe()
	got := make(chan *ext.Hub, 1)
	prog := ext.Program{ID: "enkeep-memory", Init: func(_ context.Context, _ *ext.Init, h *ext.Hub) (map[string]any, error) {
		got <- h
		return nil, nil
	}}
	go func() { _ = ext.Serve(ctx, toExt, toHub, prog) }()
	var wmu sync.Mutex
	send := func(fr frameT) {
		b, _ := json.Marshal(fr)
		wmu.Lock()
		defer wmu.Unlock()
		_, _ = fromHub.Write(append(b, '\n'))
	}
	go func() {
		sc := bufio.NewScanner(fromExt)
		sc.Buffer(make([]byte, 1<<16), 1<<24)
		for sc.Scan() {
			var fr frameT
			if json.Unmarshal(sc.Bytes(), &fr) != nil || fr.Method == "" {
				continue // answers to the hub's own requests
			}
			res, err := f.answer(fr.Method, fr.Params)
			out := frameT{ID: fr.ID}
			if err != nil {
				out.Error = err
			} else {
				out.Result, _ = json.Marshal(res)
			}
			send(out)
		}
	}()
	send(frameT{ID: "h1", Method: "initialize", Params: json.RawMessage(`{"protocol":1}`)})
	select {
	case h := <-got:
		return h
	case <-time.After(5 * time.Second):
		f.t.Fatal("the extension did not initialize")
		return nil
	}
}

func (f *fakeHub) answer(method string, raw json.RawMessage) (any, *ext.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	const writer = "ext:enkeep-memory"
	switch method {
	case "kb.update":
		f.updates = append(f.updates, raw)
		var p struct {
			Kind    string         `json:"kind"`
			ID      string         `json:"id"`
			Profile *PersonProfile `json:"profile"`
		}
		_ = json.Unmarshal(raw, &p)
		if f.old && f.strict && p.Profile != nil {
			return nil, &ext.Error{Code: "invalid_params", Message: `kb.update: json: unknown field "profile"`}
		}
		if f.old || p.Profile == nil {
			return map[string]any{"id": p.ID}, nil
		}
		if err := CheckProfile(p.Profile); err != nil {
			return nil, &ext.Error{Code: "invalid_params", Message: err.Error()}
		}
		all := f.profiles[p.ID]
		before := json.RawMessage("null")
		if old, ok := all[writer]; ok {
			before, _ = json.Marshal(old.Profile)
		}
		if all == nil {
			all = map[string]ProfileRecord{}
			f.profiles[p.ID] = all
		}
		if p.Profile.Empty() {
			delete(all, writer)
		} else {
			all[writer] = ProfileRecord{Profile: *p.Profile, At: time.Now().UTC()}
		}
		return map[string]any{"id": p.ID, "before": map[string]json.RawMessage{"profile": before},
			"ignored": []IgnoredField{{Field: "summary", Reason: "a person's summary is rendered"}}}, nil
	case "kb.get":
		var p wire.KBGetParams
		_ = json.Unmarshal(raw, &p)
		d := map[string]any{"kind": p.Kind, "id": p.ID, "meta": map[string]any{"id": p.ID}}
		if !f.old && len(f.profiles[p.ID]) > 0 {
			d["profiles"] = f.profiles[p.ID]
		}
		return map[string]any{"detail": d}, nil
	}
	return nil, &ext.Error{Code: "unknown_method", Message: method}
}

// Over the extension connection: a write, read back under ext:<id>, the before for undo, the hub's ignored fields,
// a refused profile, removal; a hub before #5934 reads back nothing.
func TestHubProfileRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &fakeHub{t: t, profiles: map[string]map[string]ProfileRecord{}}
	h := Hub{H: f.connect(ctx), Ext: "enkeep-memory"}
	var w ProfileWriter = h
	p := &PersonProfile{
		Facts:       []ProfileItem{{Text: "role-x at project-x", Sources: []string{"chats/chat-a/messages/2026-11-02.md"}}},
		Views:       []ProfileItem{{Text: "words-a", Sources: []string{"user"}}},
		Inferences:  []ProfileItem{{Text: "style-x", Basis: "证据少（1 条）；只来自转述", Sources: []string{"recordings/rec-a"}}},
		Commitments: []ProfileCommitment{{Text: "send-x", Owes: "them", Due: "2026-11-10", Sources: []string{"chats/chat-a/messages/2026-11-03.md"}}},
	}
	before, ignored, err := w.UpdateProfile(ctx, "person-a", p)
	if err != nil || string(before) != "null" || len(ignored) != 1 {
		t.Fatalf("write: %s %v %v", before, ignored, err)
	}
	var sent map[string]json.RawMessage
	_ = json.Unmarshal(f.updates[0], &sent)
	if string(sent["kind"]) != `"people"` || string(sent["id"]) != `"person-a"` || len(sent["profile"]) == 0 {
		t.Fatalf("sent %s", f.updates[0])
	}
	t.Logf("kb.update params sent (placeholders): %s", f.updates[0])
	back, err := w.ReadProfile(ctx, "person-a")
	if err != nil || back == nil {
		t.Fatalf("read back %v %v", back, err)
	}
	bj, _ := json.Marshal(back)
	pj, _ := json.Marshal(p)
	if string(bj) != string(pj) {
		t.Fatalf("read back %s", bj)
	}
	before, _, _ = w.UpdateProfile(ctx, "person-a", &PersonProfile{Facts: p.Facts})
	if string(before) != string(pj) {
		t.Fatalf("before %s", before)
	}
	bad := &PersonProfile{Inferences: []ProfileItem{{Text: "no basis", Sources: []string{"user"}}}}
	if _, _, err := w.UpdateProfile(ctx, "person-a", bad); err == nil {
		t.Fatal("a profile breaking a rule was taken")
	}
	if _, _, err := w.UpdateProfile(ctx, "person-a", &PersonProfile{}); err != nil {
		t.Fatal(err)
	}
	if back, _ := w.ReadProfile(ctx, "person-a"); back != nil {
		t.Fatalf("not removed: %+v", back)
	}
	f.old = true
	if _, _, err := w.UpdateProfile(ctx, "person-a", p); err != nil {
		t.Fatal(err)
	}
	if back, _ := w.ReadProfile(ctx, "person-a"); back != nil {
		t.Fatal("an old hub read back a profile")
	}
}

// The probe carries the person it is addressed to and the profile, nothing else.
func TestProbeOnlyCarriesProfile(t *testing.T) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(ProbeParams("person-a"), &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 || keys["kind"] == nil || keys["id"] == nil || keys["profile"] == nil {
		t.Fatalf("probe params: %s", ProbeParams("person-a"))
	}
}

// A hub with profiles refuses the probe by its rules (yes); a hub before #5934 refuses the unknown field (no), and a
// lenient one that dropped it would be a no as well. The probe is never taken for a write.
func TestProbeHubs(t *testing.T) {
	for _, tc := range []struct {
		name        string
		old, strict bool
		want        bool
	}{{"profiles", false, false, true}, {"before #5934, strict", true, true, false}, {"before #5934, lenient", true, false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &fakeHub{t: t, old: tc.old, strict: tc.strict, profiles: map[string]map[string]ProfileRecord{}}
			h := Hub{H: f.connect(ctx), Ext: "enkeep-memory"}
			ok, err := h.ProbeProfile(ctx, "person-a")
			if err != nil || ok != tc.want {
				t.Fatalf("probe: %v %v, want %v", ok, err, tc.want)
			}
			if len(f.profiles["person-a"]) != 0 {
				t.Fatal("the probe left a profile")
			}
			if len(f.updates) != 1 || string(f.updates[0]) != string(ProbeParams("person-a")) {
				t.Fatalf("sent %s", f.updates)
			}
		})
	}
}
