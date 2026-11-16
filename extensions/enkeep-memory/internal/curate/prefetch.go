package curate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/graph"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/model"
	"github.com/nwverse/persona-extensions/extensions/enkeep-memory/internal/triage"
)

// prefetched answers a request it has already made with the answer it got, once; anything else goes to the model.
// The daily pass with Workers > 1 makes every admitted unit's calls concurrently up front (prefetch), then runs its
// usual sequential loop over this model: a request built the same way gets the answer made in advance, a request
// that changed in the meantime (the memory call's known entries, after an earlier unit added one) is made live.
type prefetched struct {
	next model.Model
	mu   sync.Mutex
	got  map[string][]model.Response
	hits int
}

// requestKey is a request's identity: everything the model sees and the limits it is held to.
func requestKey(r model.Request) string {
	h := sha256.New()
	for _, p := range []string{r.Step, string(r.Tier), r.System, r.User, string(r.Schema), fmt.Sprint(r.MaxTokens)} {
		fmt.Fprintf(h, "%d:%s\x00", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (p *prefetched) Complete(ctx context.Context, r model.Request) (model.Response, error) {
	k := requestKey(r)
	p.mu.Lock()
	if rs := p.got[k]; len(rs) > 0 {
		p.got[k] = rs[1:]
		p.hits++
		p.mu.Unlock()
		return rs[0], nil
	}
	p.mu.Unlock()
	return p.next.Complete(ctx, r)
}

// prefetch makes the digest and memory calls of every admitted unit not done before, at most c.Workers at once,
// against c.Model directly. Each answer's usage is then recorded through metered, one at a time, so a call is
// metered exactly once whether its answer is used or not; the returned model hands the answers out and meters
// only the calls it has to make live.
func (c *Curator) prefetch(ctx context.Context, metered *model.Metered, admitted []triage.Decision, byRef map[string]*Unit,
	b *graph.Builder, owner []string, k *known) model.Model {
	var reqs []model.Request
	seen := map[string]bool{}
	add := func(r model.Request, key string) {
		if c.St.S.Done[key] != nil {
			return
		}
		if rk := requestKey(r); !seen[rk] {
			seen[rk] = true
			reqs = append(reqs, r)
		}
	}
	for _, d := range admitted {
		u := byRef[d.Chat]
		for _, p := range dailyParts(u, d) {
			req, key := c.digestRequest(b, u, p.d, owner, k)
			add(req, key)
		}
	}
	type answer struct {
		resp model.Response
		err  error
	}
	answers := make([]answer, len(reqs))
	var down atomic.Bool
	sem := make(chan struct{}, c.Workers)
	var wg sync.WaitGroup
	for i, r := range reqs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil || down.Load() {
				answers[i].err = errors.New("skipped")
				return
			}
			resp, err := c.Model.Complete(ctx, r)
			if errors.Is(err, model.ErrUnavailable) {
				down.Store(true) // the live call in the sequential pass will say so; no point asking the rest
			}
			answers[i] = answer{resp, err}
		}()
	}
	wg.Wait()
	p := &prefetched{next: metered, got: map[string][]model.Response{}}
	before := metered.Calls
	for i, a := range answers {
		if a.err != nil {
			continue // asked again live
		}
		metered.Calls++
		metered.Usage.Record(metered.Day, reqs[i].Step, a.resp.InTokens, a.resp.OutTokens)
		rk := requestKey(reqs[i])
		p.got[rk] = append(p.got[rk], a.resp)
	}
	c.logf("prefetched %d of %d calls with %d workers", metered.Calls-before, len(reqs), c.Workers)
	return p
}
