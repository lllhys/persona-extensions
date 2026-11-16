package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"

	"github.com/nwverse/persona/sdk/go/ext"
	"github.com/nwverse/persona/sdk/go/exttest"
	"github.com/nwverse/persona/sdk/go/wire"
)

// The program speaks the protocol and draws every UI contribution in its slot (with no hub data yet).
func TestConformance(t *testing.T) {
	exttest.Check(t, manifest, exttest.Serve(t, program(newApp())))
}

type updateErrorSource struct {
	kbsrc.Source
	err     error
	called  chan struct{}
	release chan struct{}
}

func (s *updateErrorSource) Update(context.Context, wire.KBUpdateParams) (map[string]json.RawMessage, error) {
	if s.called != nil {
		close(s.called)
	}
	if s.release != nil {
		<-s.release
	}
	return nil, s.err
}

func TestInitDoesNotWaitForProposalRecovery(t *testing.T) {
	fx, err := kbsrc.LoadFixture("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	updateErr := errors.New("knowledge base is not ready")
	next := 5
	change, _ := json.Marshal(curate.Change{Update: &wire.KBUpdateParams{Kind: "projects", ID: "atlas", Relevance: &next}})
	p := &store.Proposal{ID: "K-0001", Kind: "mail_downgrade", Risk: "low", Status: "pending", Change: change}
	st.S.Proposals = append(st.S.Proposals, p)
	st.S.NextProp = 1
	cfg := curate.Defaults()
	cfg.Mode = "review"
	c := &curate.Curator{Src: &updateErrorSource{Source: fx, err: updateErr}, St: st, Cfg: cfg, Loc: time.UTC}
	if _, err := c.Decide(t.Context(), p.ID, "accept", ""); !errors.Is(err, updateErr) {
		t.Fatalf("WAL setup: %v", err)
	}

	called := make(chan struct{})
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	a := newApp()
	a.source = &updateErrorSource{Source: fx, err: updateErr, called: called, release: release}
	in := &ext.Init{InitializeParams: wire.InitializeParams{
		Extension: &wire.ExtensionInfo{ID: "enkeep-memory", DataDir: dir},
		Hub:       wire.HubInfo{Role: wire.HubRoleMember, Timezone: "UTC"},
	}}
	initDone := make(chan error, 1)
	go func() {
		_, err := a.init(t.Context(), in, nil)
		initDone <- err
	}()
	select {
	case err := <-initDone:
		if err != nil {
			t.Fatalf("init with unavailable recovery source: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("init waited for proposal recovery")
	}
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("background recovery did not run")
	}
	close(release)
	released = true

	// Recovery held the store lock across Update. Taking it here waits for the failed attempt to finish.
	a.c.St.Lock()
	wal, err := a.c.St.ReadProposalWAL(p.ID)
	a.c.Src = fx
	a.c.St.Unlock()
	if err != nil || wal == nil {
		t.Fatalf("failed startup WAL was not retained: %+v, %v", wal, err)
	}
	a.recover(t.Context())
	wal, err = a.c.St.ReadProposalWAL(p.ID)
	if err != nil || wal != nil {
		t.Fatalf("successful later recovery did not clear WAL: %+v, %v", wal, err)
	}
	if got := a.c.St.S.Proposal(p.ID); got == nil || got.Status != "accepted" || len(a.c.St.S.Applied) != 1 {
		t.Fatalf("later recovery: %+v, applied %+v", got, a.c.St.S.Applied)
	}
}

// The manifest's configuration defaults are the ones the curator falls back to.
func TestManifestDefaults(t *testing.T) {
	var m struct {
		Configuration struct {
			Properties map[string]struct {
				Default any `json:"default"`
			} `json:"properties"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"daily_budget_usd", "weekly_budget_usd", "graph_sources"} {
		if _, ok := m.Configuration.Properties[k]; ok {
			t.Errorf("%s is gone: no budget caps, no per-source pipelines", k)
		}
	}
	if d := m.Configuration.Properties["mode"].Default; d != "auto" {
		t.Errorf("mode default %v", d)
	}
}

// The migrate command moves the old judgement layer into entries, backs the state up once, and does nothing the second
// time.
func TestMigrateCommand(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.S.Rules["r-1"] = &store.Rule{ID: "r-1", Text: "rule-x for project-x", Scope: "global", Status: "active", FirstSeen: "2026-11-01", LastSeen: "2026-11-01",
		Evidence: []store.Evidence{{Chat: "chats/persona-a", Day: "2026-11-01", N: 1, Speaker: "我", Role: "user", Quote: "quote-x"}}}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := migrate([]string{"-state", dir}); err != nil {
			t.Fatal(err)
		}
	}
	st, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.S.Entries) != 1 {
		t.Fatalf("entries %d", len(st.S.Entries))
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "state.json.bak-migrate-*"))
	if len(backups) != 1 {
		t.Errorf("backups %v", backups)
	}
	if err := migrate(nil); err == nil {
		t.Fatal("missing -state accepted")
	}
}
