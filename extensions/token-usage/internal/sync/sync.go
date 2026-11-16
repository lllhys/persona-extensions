// Package sync pulls each machine's token usage from the hub into the local store and sends the budget alerts. The
// hub keeps about a week; the store keeps 400 days, so the pull is what makes the longer history possible.
package sync

import (
	"context"
	"log"
	"time"

	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/budget"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/hub"
	"github.com/nwverse/persona-extensions/extensions/token-usage/internal/store"
)

const (
	// Every is how often the loop pulls. The hub's own rollup is coarser than this, so nothing is missed.
	Every = 10 * time.Minute
	// recentRange is what every pull asks for; backfillRange is what the first pull of a process asks for, to fill the
	// week the hub still has.
	recentRange   = "24h"
	backfillRange = "7d"
)

// Loop pulls every machine's usage into st and sends the alerts, once soon after it starts and then every `Every`,
// until ctx ends. The first pull asks for 7 days; the later ones ask for 24 hours, which is enough to catch a minute
// the hub corrected. loc is the hub's time zone.
func Loop(ctx context.Context, c *hub.Client, st *store.Store, bf *budget.File, loc *time.Location, every time.Duration) {
	if !c.Usable() {
		return
	}
	t := time.NewTimer(time.Second)
	defer t.Stop()
	first := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		rng := recentRange
		if first {
			rng = backfillRange
			first = false
		}
		pull(ctx, c, st, bf, loc, rng)
		t.Reset(every)
	}
}

// pull reads every machine once and merges what it got, then sends the alerts. A machine that fails is logged and
// skipped; the next round tries it again.
func pull(ctx context.Context, c *hub.Client, st *store.Store, bf *budget.File, loc *time.Location, rng string) {
	machines, err := c.Machines(ctx)
	if err != nil {
		log.Printf("token-usage: 读取机器列表失败：%v", err)
		return
	}
	for _, m := range machines {
		buckets, err := c.Pull(ctx, m.ID, rng)
		if err != nil {
			log.Printf("token-usage: 读取机器 %s 的用量失败：%v", m.ID, err)
			continue
		}
		if _, err := st.Merge(m.ID, buckets, loc); err != nil {
			log.Printf("token-usage: 保存机器 %s 的用量失败：%v", m.ID, err)
		}
	}
	alert(ctx, c, st, bf, loc)
}

// alert sends each alert the budgets produced. A failed send is logged; the alert was already remembered, so it is not
// retried the same day.
func alert(ctx context.Context, c *hub.Client, st *store.Store, bf *budget.File, loc *time.Location) {
	err := st.View(func(s *store.State) error {
		notes, err := bf.Alerts(s, time.Now(), loc)
		if err != nil {
			return err
		}
		for _, n := range notes {
			if err := c.Notify(ctx, n.Title, n.Body, "token-usage-"+n.Key); err != nil {
				log.Printf("token-usage: 发送提醒失败：%v", err)
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("token-usage: 检查预算失败：%v", err)
	}
}
