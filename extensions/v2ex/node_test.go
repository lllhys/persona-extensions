package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

func TestMyNodesAreMerged(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, `{"nodes": ["python", "go"]}`)
	l, err := a.list(context.Background(), "nodes", false)
	if err != nil || len(l.missed) > 0 || l.stale != nil {
		t.Fatalf("nodes: missed %v, stale %v, %v", l.missed, l.stale, err)
	}
	// python's newest is in both: once, and the latest activity first
	if got := idsOf(l.topics); !slices.Equal(got, []int64{1005, 1001, 1006}) {
		t.Fatalf("merged %v", got)
	}
	if f.hit(nodePath("python")) != 1 || f.hit(nodePath("go")) != 1 {
		t.Fatalf("python %d, go %d requests", f.hit(nodePath("python")), f.hit(nodePath("go")))
	}
}

func TestMergedIsAsOldAsItsOldestPart(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, `{"nodes": ["python", "go"]}`)
	old := time.Now().Add(-2 * time.Minute)
	a.site.store(nodePath("python"), []byte(topicsJSON(pyTopic)), old)
	l, err := a.list(context.Background(), "nodes", false)
	if err != nil || !l.at.Equal(old) {
		t.Fatalf("at %v, want %v (%v)", l.at, old, err)
	}
	if f.hit(nodePath("python")) != 0 || f.hit(nodePath("go")) != 1 {
		t.Fatal("python should have come from the cache, go from the site")
	}
}

func TestANodeThatFailsIsMissed(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, `{"nodes": ["nosuch", "python", "bad/name"]}`)
	if !slices.Equal(a.nodes, []string{"nosuch", "python"}) || !slices.Equal(a.badNodes, []string{"bad/name"}) {
		t.Fatalf("nodes %v, bad %v", a.nodes, a.badNodes)
	}
	ctx := context.Background()
	l, err := a.list(ctx, "nodes", false)
	if err != nil || len(l.topics) != 2 || len(l.missed) != 1 ||
		!strings.HasPrefix(l.missed[0], "nosuch（") || !strings.Contains(l.missed[0], errNoNode.Error()) {
		t.Fatalf("topics %v, missed %v, %v", idsOf(l.topics), l.missed, err)
	}
	if st := a.status(); st.State != "ok" {
		t.Fatalf("an unknown node is not the connector's state: %+v", st)
	}

	before := f.total()
	if _, err := a.node(ctx, "a b", listFor); !errors.Is(err, errNoNode) {
		t.Fatalf("a name that cannot be one: %v", err)
	}
	if f.total() != before {
		t.Fatal("a name that cannot be a node went into a request")
	}

	f.put(nodePath("python"), answer{status: http.StatusBadGateway})
	b := testApp(t, f, `{"nodes": ["go", "python"]}`) // python read last: its failure is the latest
	l, err = b.list(ctx, "nodes", false)
	if err != nil || !slices.Equal(idsOf(l.topics), []int64{1005, 1001}) || len(l.missed) != 1 || !strings.Contains(l.missed[0], "502") {
		t.Fatalf("topics %v, missed %v, %v", idsOf(l.topics), l.missed, err)
	}
	if st := b.status(); st.State != "error" {
		t.Fatalf("a node the site fails is the connector's state: %+v", st)
	}
}

func TestInit(t *testing.T) {
	ctx := context.Background()
	a := newApp()
	in := &ext.Init{}
	in.InitializeParams.Config = json.RawMessage(`{"tz": "Mars/Olympus"}`)
	if _, err := a.init(ctx, in, nil); err == nil {
		t.Fatal("an unknown time zone was taken")
	}
	in.InitializeParams.Config = json.RawMessage(`{"tz": "Asia/Tokyo", "list": "latest", "nodes": ["go", "节点"]}`)
	if _, err := a.init(ctx, in, nil); err != nil {
		t.Fatal(err)
	}
	if a.loc.String() != "Asia/Tokyo" || a.cfg.List != "latest" || !slices.Equal(a.nodes, []string{"go"}) || !slices.Equal(a.badNodes, []string{"节点"}) {
		t.Fatalf("loc %v, cfg %+v, nodes %v, bad %v", a.loc, a.cfg, a.nodes, a.badNodes)
	}
}

func TestStatusMovesOnlyOnALiveRead(t *testing.T) {
	f := newFakeSite(t)
	sample(f)
	a := testApp(t, f, "")
	if st := a.status(); st.State != "ok" || st.LastSync != nil || st.Account == "" {
		t.Fatalf("before any read: %+v", st)
	}
	ctx := context.Background()
	l, err := a.list(ctx, "hot", false)
	if err != nil {
		t.Fatal(err)
	}
	if st := a.status(); st.LastSync == nil || !st.LastSync.Equal(l.at) {
		t.Fatalf("after a read at %v: %+v", l.at, st)
	}
	a.site.store("/topics/latest.json", []byte(topicsJSON(pyTopic)), time.Now().Add(-time.Minute))
	if _, err := a.list(ctx, "latest", false); err != nil || f.hit("/topics/latest.json") != 0 {
		t.Fatalf("latest from the cache: %v", err)
	}
	if st := a.status(); !st.LastSync.Equal(l.at) {
		t.Fatalf("a read from the cache moved last_sync: %+v", st)
	}
}
