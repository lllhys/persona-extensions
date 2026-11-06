package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/curate"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/kbsrc"
	"github.com/nwverse/persona-extensions/extensions/kb-curator/internal/store"
)

// A trial of the daily pass on the user's real knowledge base with the model the hub gives the extension
// (model.complete), that changes nothing: the state is a fresh one in a temporary directory (the curator's own is
// not read or touched), knowledge-base writes are recorded instead of made (kbsrc.DryRun), and the run is never
// saved. What is left is the report, in <data dir>/dryruns/daily-<day>.md, and one log line with the counts and
// the tokens.

// dryRunDaily runs the trial for day.
func (a *app) dryRunDaily(ctx context.Context, day string) (string, error) {
	tmp, err := os.MkdirTemp("", "kb-curator-trial-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	st, err := store.Open(tmp)
	if err != nil {
		return "", err
	}
	src := &kbsrc.DryRun{Source: a.c.Src}
	c := *a.c
	c.Src, c.St = src, st
	cfg := c.Cfg
	cfg.Mode = "review" // proposals only, whatever the real one is set to
	c.Cfg = cfg
	started := time.Now()
	run, err := c.RunDaily(ctx, day, true)
	if err != nil {
		return "", err
	}
	in, out := 0, 0
	var steps []string
	for _, u := range st.S.Usage {
		if u.Day == day {
			in += u.In
			out += u.Out
			steps = append(steps, fmt.Sprintf("%s %d 次 %d+%d", u.Step, u.Calls, u.In, u.Out))
		}
	}
	sort.Strings(steps)
	counts := map[string]int{}
	for _, p := range st.S.Proposals {
		counts[p.Kind]++
	}
	summary := fmt.Sprintf("试跑 %s：%s，模型调用 %d 次，输入 %d、输出 %d token（%s）；条目 %d，图谱节点 %d、断言 %d，提议 %v，知识库写入 %d（未写）",
		day, time.Since(started).Round(time.Second), run.Counts["model_calls"], in, out, strings.Join(steps, "，"),
		len(st.S.Items), len(st.S.Graph.Nodes), len(st.S.Graph.Assertions), counts, len(src.Writes))
	if dir := a.dataDir; dir != "" {
		path := filepath.Join(dir, "dryruns", "daily-"+day+".md")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
			_ = os.WriteFile(path, []byte(run.Report+"\n\n---\n"+summary+"\n"), 0o600)
		}
	}
	log.Printf("kb-curator %s", summary)
	return summary, nil
}

// startTrial runs dryRunDaily in the background, one run of any kind at a time.
func (a *app) startTrial(ctx context.Context, day string) bool {
	a.mu.Lock()
	if a.running != "" {
		a.mu.Unlock()
		return false
	}
	a.running = "trial " + day
	a.mu.Unlock()
	go func() {
		summary, err := a.dryRunDaily(ctx, day)
		a.mu.Lock()
		a.running, a.lastErr, a.lastTrial = "", "", summary
		if err != nil {
			a.lastErr = "试跑 " + day + "：" + err.Error()
			log.Printf("kb-curator trial %s: %v", day, err)
		}
		a.mu.Unlock()
		if a.hub != nil {
			_ = a.hub.Invalidate("kb-curator-health")
		}
	}()
	return true
}

var _ = curate.ISOWeek // the weekly trial would go here
