package main

import (
	"context"
	"slices"
	"sync"
	"time"
)

// Discovery: what the bot can read, asked from Discord's REST API whenever it is needed (the archive's passes, the
// tools), and kept for two minutes so the hub's back-to-back calls and the tools share it. Nothing is kept up from
// events, so nothing drifts: a role, a permission or a channel changed in Discord shows at the next discovery.

const (
	discoveryTTL  = 2 * time.Minute // the hub's back-to-back calls of one run share it
	discoveryJobs = 4               // servers asked at once
)

// view is a channel (or thread, or forum post) as the archive and the tools see it.
type view struct {
	channel
	guildName, parentName string
}

// chatName is "Server #channel", or "Server #channel › thread".
func (v view) chatName(sep string) string {
	if v.thread() {
		return oneLine(v.guildName) + " #" + oneLine(v.parentName) + sep + oneLine(v.Name)
	}
	return oneLine(v.guildName) + " #" + oneLine(v.Name)
}

// discovery is one look at what the bot can read.
type discovery struct {
	at      time.Time
	guilds  []guild
	views   map[snowflake]view   // channels with messages, and active threads
	parents []view               // channels that can have threads
	names   map[snowflake]string // every channel's name, for #mentions
	partial bool                 // a server could not be read this time
}

// discover answers the last discovery if it is recent, else asks Discord: the bot's servers, then each server's
// channels and active threads. A server that fails is left out of this one (its channels wait for the next); it
// fails only when no server could be read, or Discord refused the token.
func (s *session) discover(ctx context.Context) (*discovery, error) {
	s.dmu.Lock()
	defer s.dmu.Unlock() // one discovery at a time; the others wait for it and share it
	if s.disc != nil && time.Since(s.disc.at) < discoveryTTL {
		return s.disc, nil
	}
	guilds, err := s.api.guilds(ctx)
	if err != nil {
		return nil, err
	}
	d := &discovery{at: time.Now(), guilds: guilds, views: map[snowflake]view{}, names: map[snowflake]string{}}
	var mu sync.Mutex
	var failed []error
	var wg sync.WaitGroup
	jobs := make(chan struct{}, discoveryJobs)
	for _, g := range guilds {
		wg.Add(1)
		jobs <- struct{}{}
		go func(g guild) {
			defer wg.Done()
			defer func() { <-jobs }()
			chans, err := s.api.channels(ctx, g.ID)
			if err != nil {
				mu.Lock()
				failed = append(failed, err)
				mu.Unlock()
				return
			}
			threads, err := s.api.activeThreads(ctx, g.ID)
			if err != nil && statusOf(err) != 403 { // some servers do not let the bot list threads: the channels still count
				mu.Lock()
				failed = append(failed, err)
				mu.Unlock()
				return
			}
			mu.Lock()
			defer mu.Unlock()
			d.add(g, chans, threads)
		}(g)
	}
	wg.Wait()
	for _, err := range failed {
		if statusOf(err) == 401 || len(failed) == len(guilds) {
			return nil, err
		}
	}
	d.partial = len(failed) > 0
	slices.SortFunc(d.parents, func(a, b view) int { return cmpID(b.LastMessageID, a.LastMessageID) })
	s.disc = d
	return d, nil
}

// add puts in one server's channels and active threads. Callers hold the discovery's lock.
func (d *discovery) add(g guild, chans, threads []channel) {
	byID := map[snowflake]channel{}
	for _, c := range chans {
		c.GuildID = g.ID
		byID[c.ID] = c
		d.names[c.ID] = c.Name
		v := view{channel: c, guildName: g.Name}
		if c.readable() {
			d.views[c.ID] = v
		}
		if c.hasThreads() {
			d.parents = append(d.parents, v)
		}
	}
	for _, t := range threads {
		t.GuildID = g.ID
		d.names[t.ID] = t.Name
		d.views[t.ID] = view{channel: t, guildName: g.Name, parentName: byID[t.ParentID].Name}
	}
}

// guildName is a server's name ("" when not known).
func (d *discovery) guildName(id snowflake) string {
	for _, g := range d.guilds {
		if g.ID == id {
			return g.Name
		}
	}
	return ""
}

func cmpID(a, b snowflake) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// roleName is a role's name for an @role mention, asked once an hour per server and only when a message has one
// ("" when not known).
func (s *session) roleName(ctx context.Context, g, id snowflake) string {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	r, ok := s.roleCache[g]
	if !ok || time.Now().After(r.until) {
		roles, err := s.api.roles(ctx, g)
		r = roleNames{until: time.Now().Add(time.Hour), names: map[snowflake]string{}}
		if err != nil {
			r.until = time.Now().Add(time.Minute) // asked again soon, not left as "@?" for an hour
		}
		for _, x := range roles {
			r.names[x.ID] = x.Name
		}
		s.roleCache[g] = r
	}
	return r.names[id]
}

type roleNames struct {
	until time.Time
	names map[snowflake]string
}
