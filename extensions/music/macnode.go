package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/sdk/go/ext"
)

// The Mac program (the manifest's node section): it runs on the Mac selected in the extension's settings and answers
// the hub program's nodes.call. Every read is Music.app AppleScript; nothing leaves the Mac except the fields below.

// macProgram is the ext.Program the node runs.
func macProgram() ext.Program {
	n := &macNode{run: osaRunner{}, now: time.Now, cache: map[string]macCache{}}
	if host, err := os.Hostname(); err == nil {
		n.host = host
	}
	return ext.Program{
		ID: about.ID, Version: about.Version, Manifest: manifest,
		Namespaces: map[string]ext.Handler{"music": n.handle},
	}
}

type macNode struct {
	run  scriptRunner
	host string
	now  func() time.Time

	mu    sync.Mutex
	cache map[string]macCache
}

type macCache struct {
	at  time.Time
	val any
}

func (n *macNode) cached(key string, ttl time.Duration, load func() (any, error)) (any, error) {
	n.mu.Lock()
	if c, ok := n.cache[key]; ok && n.now().Sub(c.at) < ttl {
		n.mu.Unlock()
		return c.val, nil
	}
	n.mu.Unlock()
	v, err := load()
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	n.cache[key] = macCache{at: n.now(), val: v}
	n.mu.Unlock()
	return v, nil
}

func (n *macNode) clearCache() {
	n.mu.Lock()
	n.cache = map[string]macCache{}
	n.mu.Unlock()
}

type macStatus struct {
	Running   bool   `json:"running"`
	Host      string `json:"host,omitempty"`
	Version   string `json:"version,omitempty"`
	Tracks    int    `json:"tracks"`
	Playlists int    `json:"playlists"`
}

type macTrack struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Artist      string `json:"artist,omitempty"`
	Album       string `json:"album,omitempty"`
	DurationMS  int    `json:"duration_ms,omitempty"`
	PlayedCount int    `json:"played_count,omitempty"`
	PlayedAt    string `json:"played_at,omitempty"`
	Rating      int    `json:"rating,omitempty"`
}

func (t macTrack) item() item {
	return item{Provider: macID, ID: t.ID, Kind: "song", Title: t.Title, Artist: t.Artist, Album: t.Album, DurationMS: t.DurationMS}
}

type macPlaylist struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Descr      string `json:"description,omitempty"`
	TrackCount int    `json:"track_count"`
	DurationMS int    `json:"duration_ms,omitempty"`
	Class      string `json:"class,omitempty"`
}

func (p macPlaylist) playlist() playlist {
	return playlist{item: item{Provider: macID, ID: p.ID, Kind: "playlist", Title: p.Title, Descr: p.Descr, DurationMS: p.DurationMS}, TrackCount: p.TrackCount}
}

type macNow struct {
	State      string `json:"state"`
	ID         string `json:"id,omitempty"`
	Title      string `json:"title,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	PositionMS int    `json:"position_ms,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
}

type macDetail struct {
	Playlist macPlaylist `json:"playlist"`
	Tracks   []macTrack  `json:"tracks"`
}

type macCreate struct {
	Playlist macPlaylist `json:"playlist"`
	Added    int         `json:"added"`
}

func (n *macNode) handle(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	switch method {
	case "status":
		return n.status(ctx)
	case "now":
		return n.nowPlaying(ctx)
	case "artwork":
		var in struct {
			Kind  string `json:"kind"`
			ID    string `json:"id"`
			Width int    `json:"width"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, &ext.Error{Code: "invalid", Message: "invalid arguments"}
		}
		return n.artwork(ctx, in.Kind, in.ID, in.Width)
	case "recent":
		var in struct {
			Limit int `json:"limit"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, &ext.Error{Code: "invalid", Message: "invalid arguments"}
		}
		return n.recent(ctx, in.Limit)
	case "playlists":
		return n.playlists(ctx)
	case "playlist":
		var in struct {
			ID     string `json:"id"`
			Offset int    `json:"offset"`
			Limit  int    `json:"limit"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, &ext.Error{Code: "invalid", Message: "invalid arguments"}
		}
		return n.playlist(ctx, in.ID, in.Offset, in.Limit)
	case "search":
		var in struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, &ext.Error{Code: "invalid", Message: "invalid arguments"}
		}
		return n.search(ctx, in.Query, in.Limit)
	case "stats":
		return n.stats(ctx)
	case "lyrics":
		return n.lyrics(ctx)
	case "control":
		var in struct {
			Action   string  `json:"action"`
			ID       string  `json:"id"`
			Position float64 `json:"position"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, &ext.Error{Code: "invalid", Message: "invalid arguments"}
		}
		return n.control(ctx, in.Action, in.ID, in.Position)
	case "create_playlist":
		var in struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			TrackIDs    []string `json:"track_ids"`
		}
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, &ext.Error{Code: "invalid", Message: "invalid arguments"}
		}
		return n.createPlaylist(ctx, in.Name, in.Description, in.TrackIDs)
	case "open":
		if err := exec.CommandContext(ctx, "/usr/bin/open", "-a", "Music").Run(); err != nil {
			return nil, macError(ctx, err)
		}
		return n.status(ctx)
	case "refresh":
		n.clearCache()
		return map[string]any{"ok": true}, nil
	}
	return nil, ext.UnknownMethod("music." + method)
}

func (n *macNode) status(ctx context.Context) (macStatus, error) {
	v, err := n.cached("status", 30*time.Second, func() (any, error) {
		out, err := n.run.Run(ctx, statusScript)
		if err != nil {
			return nil, macError(ctx, err)
		}
		rows := records(out)
		if len(rows) != 1 || len(rows[0]) < 4 {
			return nil, &ext.Error{Code: "macos_music", Message: "Music returned an answer that could not be read."}
		}
		f := rows[0]
		return macStatus{
			Running: f[0] == "1", Version: f[1], Host: n.host,
			Tracks: atoi(f[2]), Playlists: atoi(f[3]),
		}, nil
	})
	if err != nil {
		return macStatus{}, err
	}
	return v.(macStatus), nil
}

func (n *macNode) nowPlaying(ctx context.Context) (macNow, error) {
	out, err := n.run.Run(ctx, nowScript)
	if err != nil {
		return macNow{}, macError(ctx, err)
	}
	rows := records(out)
	if len(rows) != 1 || len(rows[0]) < 7 {
		return macNow{}, &ext.Error{Code: "macos_music", Message: "Music returned an answer that could not be read."}
	}
	f := rows[0]
	return macNow{State: f[0], ID: f[1], Title: f[2], Artist: f[3], Album: f[4], PositionMS: atoi(f[5]) * 1000, DurationMS: atoi(f[6]) * 1000}, nil
}

// lyrics reads the words of the track Music is playing; a track without them answers empty.
func (n *macNode) lyrics(ctx context.Context) (string, error) {
	out, err := n.run.Run(ctx, lyricsScript)
	if err != nil {
		return "", macError(ctx, err)
	}
	return strings.TrimSpace(out), nil
}

func (n *macNode) recent(ctx context.Context, limit int) ([]macTrack, error) {
	limit = clamp(limit, 20, 1, 50)
	v, err := n.cached("recent", 2*time.Minute, func() (any, error) {
		out, err := n.run.Run(ctx, recentScript, "2000")
		if err != nil {
			return nil, macError(ctx, err)
		}
		var tracks []macTrack
		for _, f := range records(out) {
			if len(f) < 8 {
				continue
			}
			tracks = append(tracks, macTrack{ID: f[0], Title: f[1], Artist: f[2], Album: f[3],
				DurationMS: atoi(f[4]) * 1000, PlayedCount: atoi(f[5]), PlayedAt: f[6], Rating: atoi(f[7])})
		}
		sort.SliceStable(tracks, func(i, j int) bool {
			return playedAt(tracks[i].PlayedAt).After(playedAt(tracks[j].PlayedAt))
		})
		return tracks, nil
	})
	if err != nil {
		return nil, err
	}
	tracks := v.([]macTrack)
	if len(tracks) > limit {
		tracks = tracks[:limit]
	}
	return tracks, nil
}

func (n *macNode) playlists(ctx context.Context) ([]macPlaylist, error) {
	v, err := n.cached("playlists", 5*time.Minute, func() (any, error) {
		out, err := n.run.Run(ctx, playlistsScript)
		if err != nil {
			return nil, macError(ctx, err)
		}
		var playlists []macPlaylist
		for _, f := range records(out) {
			if len(f) < 6 || strings.EqualFold(f[5], "folder playlist") {
				continue
			}
			playlists = append(playlists, macPlaylist{ID: f[0], Title: f[1], TrackCount: atoi(f[2]), DurationMS: atoi(f[3]) * 1000, Descr: f[4], Class: f[5]})
		}
		return playlists, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]macPlaylist), nil
}

func (n *macNode) playlist(ctx context.Context, id string, offset, limit int) (macDetail, error) {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 200 {
		return macDetail{}, &ext.Error{Code: "invalid", Message: "invalid playlist id"}
	}
	offset = clamp(offset, 0, 0, maxPlaylistOffset)
	limit = clamp(limit, 100, 1, 100)
	out, err := n.run.Run(ctx, playlistScript, id, strconv.Itoa(offset), strconv.Itoa(limit))
	if err != nil {
		return macDetail{}, macError(ctx, err)
	}
	var d macDetail
	for _, f := range records(out) {
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "P":
			if len(f) >= 6 {
				d.Playlist = macPlaylist{ID: f[1], Title: f[2], TrackCount: atoi(f[3]), DurationMS: atoi(f[4]) * 1000, Descr: f[5]}
			}
		case "T":
			if len(f) >= 8 {
				d.Tracks = append(d.Tracks, macTrack{ID: f[1], Title: f[2], Artist: f[3], Album: f[4], DurationMS: atoi(f[5]) * 1000, PlayedCount: atoi(f[6]), Rating: atoi(f[7])})
			}
		}
	}
	if d.Playlist.ID == "" {
		return macDetail{}, &ext.Error{Code: "not_found", Message: "No playlist with that ID is in the Music library."}
	}
	return d, nil
}

func (n *macNode) search(ctx context.Context, query string, limit int) ([]macTrack, error) {
	query = strings.TrimSpace(query)
	if query == "" || len([]rune(query)) > 200 {
		return nil, &ext.Error{Code: "invalid", Message: "search must be 1 to 200 characters"}
	}
	limit = clamp(limit, 12, 1, 25)
	out, err := n.run.Run(ctx, searchScript, query, strconv.Itoa(limit))
	if err != nil {
		return nil, macError(ctx, err)
	}
	var tracks []macTrack
	for _, f := range records(out) {
		if len(f) < 7 {
			continue
		}
		tracks = append(tracks, macTrack{ID: f[0], Title: f[1], Artist: f[2], Album: f[3], DurationMS: atoi(f[4]) * 1000, PlayedCount: atoi(f[5]), Rating: atoi(f[6])})
	}
	return tracks, nil
}

func (n *macNode) stats(ctx context.Context) (libraryStats, error) {
	v, err := n.cached("stats", 10*time.Minute, func() (any, error) {
		out, err := n.run.Run(ctx, statsScript)
		if err != nil {
			return nil, macError(ctx, err)
		}
		rows := records(out)
		if len(rows) != 1 || len(rows[0]) < 3 {
			return nil, &ext.Error{Code: "macos_music", Message: "Music returned an answer that could not be read."}
		}
		return libraryStats{Tracks: atoi(rows[0][0]), DurationMS: atoi(rows[0][1]) * 1000, Playlists: atoi(rows[0][2])}, nil
	})
	if err != nil {
		return libraryStats{}, err
	}
	return v.(libraryStats), nil
}

func (n *macNode) control(ctx context.Context, action, id string, position float64) (string, error) {
	switch action {
	case "play", "pause", "playpause", "next", "previous", "stop", "position", "play_track", "play_playlist":
	default:
		return "", &ext.Error{Code: "invalid", Message: "action must be play, pause, playpause, next, previous, stop, position, play_track or play_playlist"}
	}
	args := []string{action}
	switch action {
	case "play_track", "play_playlist":
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 200 {
			return "", &ext.Error{Code: "invalid", Message: action + " needs an id"}
		}
		args = append(args, id)
	case "position":
		if position < 0 || position > 24*60*60 {
			return "", &ext.Error{Code: "invalid", Message: "position is out of range"}
		}
		args = append(args, strconv.FormatFloat(position, 'f', 1, 64))
	}
	out, err := n.run.Run(ctx, controlScript, args...)
	if err != nil {
		return "", macError(ctx, err)
	}
	return strings.TrimSpace(out), nil
}

func (n *macNode) createPlaylist(ctx context.Context, name, description string, ids []string) (macCreate, error) {
	name = cleanText(name, 100)
	if name == "" {
		return macCreate{}, &ext.Error{Code: "invalid", Message: "playlist name must be 1 to 100 characters"}
	}
	description = cleanText(description, 1000)
	if len(ids) == 0 || len(ids) > 100 {
		return macCreate{}, &ext.Error{Code: "invalid", Message: "a playlist needs 1 to 100 tracks"}
	}
	args := []string{name, description}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 200 {
			return macCreate{}, &ext.Error{Code: "invalid", Message: "invalid track id"}
		}
		args = append(args, id)
	}
	out, err := n.run.Run(ctx, createPlaylistScript, args...)
	if err != nil {
		return macCreate{}, macError(ctx, err)
	}
	rows := records(out)
	if len(rows) != 1 || len(rows[0]) < 4 {
		return macCreate{}, &ext.Error{Code: "macos_music", Message: "Music created the playlist but returned no details."}
	}
	f := rows[0]
	n.clearCache()
	return macCreate{Playlist: macPlaylist{ID: f[0], Title: f[1], TrackCount: atoi(f[2])}, Added: atoi(f[3])}, nil
}

type macArtwork struct {
	Data string `json:"data"`
}

// artwork exports a track's or playlist's artwork to a temporary file, shrinks it with sips and returns it base64.
func (n *macNode) artwork(ctx context.Context, kind, id string, width int) (macArtwork, error) {
	kind = strings.TrimSpace(kind)
	id = strings.TrimSpace(id)
	if (kind != "track" && kind != "playlist") || id == "" || len(id) > 200 {
		return macArtwork{}, &ext.Error{Code: "invalid", Message: "invalid artwork reference"}
	}
	dir, err := os.MkdirTemp("", "persona-music-artwork-*")
	if err != nil {
		return macArtwork{}, err
	}
	defer os.RemoveAll(dir)
	raw := filepath.Join(dir, "art")
	jpg := filepath.Join(dir, "art.jpg")
	out, err := n.run.Run(ctx, artworkScript, kind, id, raw)
	if err != nil {
		return macArtwork{}, macError(ctx, err)
	}
	if strings.TrimSpace(out) != "ok" {
		return macArtwork{}, &ext.Error{Code: "not_found", Message: "This item has no artwork."}
	}
	size := clamp(width, 600, 160, 1200)
	if err := exec.CommandContext(ctx, "/usr/bin/sips", "-Z", strconv.Itoa(size), "-s", "format", "jpeg", raw, "--out", jpg).Run(); err == nil {
		if data, err := os.ReadFile(jpg); err == nil && len(data) > 0 && len(data) <= 4<<20 {
			return macArtwork{Data: base64.StdEncoding.EncodeToString(data)}, nil
		}
	}
	if st, err := os.Stat(raw); err == nil && st.Size() > 4<<20 {
		return macArtwork{}, &ext.Error{Code: "too_large", Message: "The artwork is too large to show."}
	}
	data, err := os.ReadFile(raw)
	if err != nil {
		return macArtwork{}, &ext.Error{Code: "not_found", Message: "This item has no artwork."}
	}
	if len(data) > 4<<20 {
		return macArtwork{}, &ext.Error{Code: "too_large", Message: "The artwork is too large to show."}
	}
	return macArtwork{Data: base64.StdEncoding.EncodeToString(data)}, nil
}

func macError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return &ext.Error{Code: "timeout", Message: "Music did not answer in time."}
	}
	msg := strings.TrimSpace(err.Error())
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(msg, "-1743"), strings.Contains(lower, "not authorized"),
		strings.Contains(lower, "not allowed"), strings.Contains(lower, "assistive access"):
		return &ext.Error{Code: "macos_privacy", Message: "Allow Persona to control Music in System Settings › Privacy & Security › Automation, then try again."}
	case strings.Contains(lower, "isn’t running"), strings.Contains(lower, "isn't running"), strings.Contains(lower, "can’t get application"):
		return &ext.Error{Code: "music_not_running", Message: "Music is not running on this Mac."}
	}
	return &ext.Error{Code: "macos_music", Message: clip(oneLine(msg), 300)}
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func playedAt(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, strings.TrimSpace(s)); err == nil {
			return t
		}
	}
	return time.Time{}
}

// cleanText trims a user-supplied name or description and removes the separators the script protocol uses.
func cleanText(s string, max int) string {
	s = strings.NewReplacer(fieldSep, " ", recordSep, " ").Replace(strings.TrimSpace(s))
	if len([]rune(s)) > max {
		s = string([]rune(s)[:max])
	}
	return s
}
