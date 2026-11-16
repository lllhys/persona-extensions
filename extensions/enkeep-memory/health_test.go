package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/store"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/stub"
	"github.com/nwverse/persona/sdk/go/ext"
)

// gated answers like the stub, except that while held its daily calls wait: a daily pass then sits in its run,
// holding the store's lock, as a real one does for minutes with a real model.
type gated struct {
	mu      sync.Mutex
	m       stub.Model
	hold    chan struct{} // nil: answer at once
	waiting chan struct{} // gets a value when a daily call starts waiting
	decline string        // a daily or memory call with this text is declined
}

func (g *gated) Complete(ctx context.Context, r model.Request) (model.Response, error) {
	g.mu.Lock()
	hold := g.hold
	g.mu.Unlock()
	if hold != nil && r.Step == "daily" {
		select {
		case g.waiting <- struct{}{}:
		default:
		}
		<-hold
	}
	if g.decline != "" && (r.Step == "daily" || r.Step == "memory") && strings.Contains(r.User, g.decline) {
		return model.Response{}, fmt.Errorf("%w: model.complete: ai: the model declined to answer", model.ErrRefused)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.m.Complete(ctx, r)
}

func viewText(t *testing.T, a *app) string {
	t.Helper()
	done := make(chan string, 1)
	go func() {
		v, err := a.health(context.Background(), &ext.UIRequest{})
		if err != nil {
			done <- "error: " + err.Error()
			return
		}
		b, _ := json.Marshal(v)
		done <- string(b)
	}()
	select {
	case s := <-done:
		return s
	case <-time.After(3 * time.Second): // the hub gives up after 20 seconds; the page must not wait at all
		t.Fatal("the health page waits for the run in progress")
		return ""
	}
}

// The health page opens while a daily pass holds the state: it shows the last snapshot and the run's progress,
// and, once the pass is done, the batches the model declined.
func TestHealthPageDuringARun(t *testing.T) {
	fx, err := kbsrc.LoadFixture("testdata/sample")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := &gated{waiting: make(chan struct{}, 1), decline: "限流上限"}
	cfg := curate.Defaults()
	cfg.AdmitAllBelowTokens = 0
	a := &app{c: &curate.Curator{Src: fx, Model: g, St: st, Cfg: cfg, Loc: time.UTC, Live: &curate.Live{}}}
	st.Lock()
	a.c.Publish()
	st.Unlock()
	ctx := context.Background()
	if _, err := a.c.RunDaily(ctx, "2026-11-03", false); err != nil {
		t.Fatal(err)
	}
	page := viewText(t, a)
	for _, s := range []string{"分拣 2026-11-03", "模型拒答 2026-11-03", "Atlas 项目群", "5–7（3 行）", "跳过（这几行没读）", "拆小重试"} {
		if !strings.Contains(page, s) {
			t.Errorf("after the run the page does not show %q: %s", s, page)
		}
	}
	if strings.Contains(page, "整理中") {
		t.Errorf("nothing runs, the page says it does: %s", page)
	}

	hold := make(chan struct{})
	g.mu.Lock()
	g.hold = hold
	g.mu.Unlock()
	if !a.start(ctx, "daily", "2026-11-04") {
		t.Fatal("the run did not start")
	}
	select {
	case <-g.waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("the run never reached a daily call")
	}
	page = viewText(t, a)
	for _, s := range []string{"整理中", "正在整理：日级 2026-11-04 · 摘要 1/", "分拣 2026-11-03", "模型拒答 2026-11-03"} {
		if !strings.Contains(page, s) {
			t.Errorf("during the run the page does not show %q: %s", s, page)
		}
	}
	g.mu.Lock()
	g.hold = nil
	g.mu.Unlock()
	close(hold)
	deadline := time.Now().Add(10 * time.Second)
	for {
		a.mu.Lock()
		running := a.running
		a.mu.Unlock()
		if running == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the run did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	page = viewText(t, a)
	if !strings.Contains(page, "分拣 2026-11-04") || strings.Contains(page, "整理中") {
		t.Errorf("after the second run the page is not the new snapshot: %s", page)
	}
}
