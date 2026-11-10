package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

// nodeCaller is the hub's nodes.call: one method of this extension's program on a computer.
type nodeCaller interface {
	Call(ctx context.Context, machine, method string, params, out any) error
}

// hubNodeCaller reaches a computer's program through the hub.
type hubNodeCaller struct{ hub *ext.Hub }

func (c hubNodeCaller) Call(ctx context.Context, machine, method string, params, out any) error {
	return c.hub.Call(ctx, "nodes.call", map[string]any{
		"machine": machine, "method": "music." + method, "params": params,
	}, out)
}

// macProvider reads and controls Music.app on the selected Mac through the node program. It caches reads for a short
// while, so a page redraw, a status poll and the assistant's tools share one library scan rather than each pulling.
type macProvider struct {
	machine string
	node    nodeCaller
	now     func() time.Time

	mu    sync.Mutex
	cache map[string]macCache
}

func newMacProvider(machine string, node nodeCaller) *macProvider {
	return &macProvider{machine: strings.TrimSpace(machine), node: node, now: time.Now, cache: map[string]macCache{}}
}

func (p *macProvider) ID() string   { return macID }
func (p *macProvider) Name() string { return "Mac" }

// ControlActions: what Music.app exposes here, so the tool schema advertises exactly these.
func (p *macProvider) ControlActions() []string {
	return []string{"play", "pause", "playpause", "next", "previous", "stop", "play_track", "play_playlist"}
}

// CreatePlaylistHint is the one sentence the create-playlist tool and card add for this endpoint.
func (p *macProvider) CreatePlaylistHint() string {
	return "This Mac can only play tracks in its library, not the Apple Music catalog."
}

func (p *macProvider) OpenHosts() []string { return nil }

func (p *macProvider) SearchKinds() []string { return []string{"song"} }

func (p *macProvider) Refresh() {
	p.mu.Lock()
	p.cache = map[string]macCache{}
	p.mu.Unlock()
	if p.machine != "" && p.node != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var out map[string]any
		_ = p.node.Call(ctx, p.machine, "refresh", map[string]any{}, &out)
	}
}

func (p *macProvider) cached(key string, ttl time.Duration, load func() (any, error)) (any, error) {
	p.mu.Lock()
	if c, ok := p.cache[key]; ok && p.now().Sub(c.at) < ttl {
		p.mu.Unlock()
		return c.val, nil
	}
	p.mu.Unlock()
	v, err := load()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.cache == nil {
		p.cache = map[string]macCache{}
	}
	p.cache[key] = macCache{at: p.now(), val: v}
	p.mu.Unlock()
	return v, nil
}

func (p *macProvider) clearCache() {
	p.mu.Lock()
	p.cache = map[string]macCache{}
	p.mu.Unlock()
}

// call runs one method on the Mac with a deadline short enough for a page render.
func (p *macProvider) call(ctx context.Context, method string, in, out any) error {
	if p.machine == "" {
		return errors.New("no Mac is selected")
	}
	if p.node == nil {
		return errors.New("this hub cannot reach computers")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	return p.node.Call(ctx, p.machine, method, in, out)
}

func (p *macProvider) Status(ctx context.Context, locale string) providerStatus {
	t := textsFor(locale)
	st := providerStatus{State: "ok", Account: "Mac"}
	if p.machine == "" {
		st.State = "not_configured"
		st.Guide = []string{t.macTurnOn}
		return st
	}
	v, err := p.cached("status", 30*time.Second, func() (any, error) {
		var out macStatus
		if err := p.call(ctx, "status", map[string]any{}, &out); err != nil {
			return nil, err
		}
		return out, nil
	})
	if err != nil {
		st.State, st.Error = "error", clip(oneLine(err.Error()), 300)
		st.Guide = macGuide(err, t)
		return st
	}
	out := v.(macStatus)
	if out.Host != "" {
		st.Account = "Mac · " + out.Host
	}
	if !out.Running {
		st.Account += " · " + t.macClosed
		st.Guide = []string{t.macOpenMusic}
	}
	return st
}

func macGuide(err error, t texts) []string {
	var e *ext.Error
	if errors.As(err, &e) {
		switch e.Code {
		case "macos_privacy":
			return []string{e.Message, t.macPrivacy}
		case "music_not_running":
			return []string{t.macOpenMusic}
		}
	}
	return []string{t.macCheck}
}

func (p *macProvider) Recent(ctx context.Context, limit int) ([]item, error) {
	limit = clamp(limit, 20, 1, 50)
	v, err := p.cached(fmt.Sprintf("recent:%d", limit), 2*time.Minute, func() (any, error) {
		var out []macTrack
		if err := p.call(ctx, "recent", map[string]any{"limit": limit}, &out); err != nil {
			return nil, err
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	out := v.([]macTrack)
	items := make([]item, 0, len(out))
	for _, t := range out {
		items = append(items, t.item())
	}
	return items, nil
}

func (p *macProvider) Playlists(ctx context.Context, limit int) ([]playlist, error) {
	limit = clamp(limit, 25, 1, 50)
	v, err := p.cached("playlists", 2*time.Minute, func() (any, error) {
		var out []macPlaylist
		if err := p.call(ctx, "playlists", map[string]any{}, &out); err != nil {
			return nil, err
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	out := v.([]macPlaylist)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	playlists := make([]playlist, 0, len(out))
	for _, pl := range out {
		playlists = append(playlists, pl.playlist())
	}
	return playlists, nil
}

func (p *macProvider) Playlist(ctx context.Context, id string, offset, limit int) (playlist, []item, error) {
	offset = clamp(offset, 0, 0, maxPlaylistOffset)
	limit = clamp(limit, 100, 1, 100)
	v, err := p.cached(fmt.Sprintf("playlist:%s:%d:%d", id, offset, limit), 5*time.Minute, func() (any, error) {
		var out macDetail
		if err := p.call(ctx, "playlist", map[string]any{"id": id, "offset": offset, "limit": limit}, &out); err != nil {
			return nil, err
		}
		return out, nil
	})
	if err != nil {
		return playlist{}, nil, err
	}
	out := v.(macDetail)
	tracks := make([]item, 0, len(out.Tracks))
	for _, t := range out.Tracks {
		tracks = append(tracks, t.item())
	}
	return out.Playlist.playlist(), tracks, nil
}

func (p *macProvider) Search(ctx context.Context, req searchRequest) ([]item, error) {
	query := strings.TrimSpace(req.Query)
	limit := clamp(req.Limit, 12, 1, 25)
	v, err := p.cached(fmt.Sprintf("search:%s:%d", query, limit), time.Minute, func() (any, error) {
		var out []macTrack
		if err := p.call(ctx, "search", map[string]any{"query": query, "limit": limit}, &out); err != nil {
			return nil, err
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	out := v.([]macTrack)
	items := make([]item, 0, len(out))
	for _, t := range out {
		items = append(items, t.item())
	}
	return items, nil
}

func (p *macProvider) Stats(ctx context.Context) (libraryStats, error) {
	v, err := p.cached("stats", 5*time.Minute, func() (any, error) {
		var out libraryStats
		if err := p.call(ctx, "stats", map[string]any{}, &out); err != nil {
			return nil, err
		}
		return out, nil
	})
	if err != nil {
		return libraryStats{}, err
	}
	return v.(libraryStats), nil
}

func (p *macProvider) NowPlaying(ctx context.Context) (nowPlaying, error) {
	v, err := p.cached("now", 15*time.Second, func() (any, error) {
		var out macNow
		if err := p.call(ctx, "now", map[string]any{}, &out); err != nil {
			return nil, err
		}
		return out, nil
	})
	if err != nil {
		return nowPlaying{}, err
	}
	out := v.(macNow)
	return nowPlaying{ID: out.ID, Title: out.Title, Artist: out.Artist, Album: out.Album, State: out.State,
		PositionMS: out.PositionMS, DurationMS: out.DurationMS}, nil
}

// Lyrics reads the current track's words; keyed by track, so a change reads the new one and a repeat is free.
func (p *macProvider) Lyrics(ctx context.Context, id string) (string, error) {
	v, err := p.cached("lyrics:"+id, 10*time.Minute, func() (any, error) {
		var out string
		if err := p.call(ctx, "lyrics", map[string]any{}, &out); err != nil {
			return nil, err
		}
		return out, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

func (p *macProvider) Control(ctx context.Context, action string) error {
	in := map[string]any{"action": action}
	if id, ok := strings.CutPrefix(action, "play_track:"); ok {
		in = map[string]any{"action": "play_track", "id": id}
	}
	var state string
	if err := p.call(ctx, "control", in, &state); err != nil {
		return err
	}
	p.mu.Lock()
	delete(p.cache, "now")
	p.mu.Unlock()
	return nil
}

func (p *macProvider) CreatePlaylist(ctx context.Context, req createPlaylistRequest) (playlist, error) {
	var out macCreate
	if err := p.call(ctx, "create_playlist", map[string]any{
		"name": req.Name, "description": req.Description, "track_ids": req.TrackIDs,
	}, &out); err != nil {
		return playlist{}, err
	}
	p.clearCache()
	n := out.Playlist
	if n.Title == "" {
		n.Title = req.Name
	}
	return n.playlist(), nil
}

// Artwork fetches a track's or playlist's picture from the Mac for the page's ui.image ref.
func (p *macProvider) Artwork(ctx context.Context, kind, id string, width int) ([]byte, error) {
	var out macArtwork
	if err := p.call(ctx, "artwork", map[string]any{"kind": kind, "id": id, "width": width}, &out); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(out.Data)
	if err != nil || len(data) == 0 {
		return nil, errors.New("the Mac returned no picture")
	}
	return data, nil
}

func (p *macProvider) String() string { return fmt.Sprintf("mac(%s)", p.machine) }
