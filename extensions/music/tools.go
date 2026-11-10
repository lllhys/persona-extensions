package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// tool is a read tool or a write as the connector protocol declares it.
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// toolName is the assistant-facing name of one endpoint tool: the connector id, the endpoint and the local name.
func toolName(src *source, p provider, local string) string {
	return src.id + "_" + p.ID() + "_" + local
}

// toolsFor builds the read tools a provider supports. A provider gets its own prefix without changing this.
func toolsFor(src *source, p provider) []tool {
	var out []tool
	if _, ok := p.(searchProvider); ok {
		kinds := searchKinds(p)
		out = append(out, tool{
			Name:        p.ID() + "_search",
			Description: "Search " + p.Name() + " for " + strings.Join(kinds, ", ") + ". Returns titles, artists, albums, durations, opaque IDs and product links. Use an ID with the playlist actions; a link opens the item in the music app.",
			InputSchema: map[string]any{"type": "object", "required": []string{"query"}, "properties": map[string]any{
				"query": map[string]any{"type": "string", "minLength": 1, "maxLength": 200, "description": "What to search for"},
				"kind":  map[string]any{"type": "string", "enum": kinds, "description": "What to search for; default " + kinds[0]},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 25, "description": "Maximum results, default 10"},
			}},
		})
	}
	if _, ok := p.(recentProvider); ok {
		out = append(out, tool{
			Name:        p.ID() + "_recent",
			Description: "Read recently played tracks from " + p.Name() + " with title, artist, album, duration and product link. This is a short recent list, not a complete listening history.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "Maximum tracks, default 20"},
			}},
		})
	}
	if _, ok := p.(playlistsProvider); ok {
		description := "List the user's " + p.Name() + " library playlists with name, track count, description and opaque playlist ID."
		if _, hasDetail := p.(playlistProvider); hasDetail {
			description += " Use the ID with " + toolName(src, p, "playlist") + "."
		}
		out = append(out, tool{
			Name:        p.ID() + "_playlists",
			Description: description,
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "Maximum playlists, default 25"},
			}},
		})
	}
	if _, ok := p.(playlistProvider); ok {
		out = append(out, tool{
			Name:        p.ID() + "_playlist",
			Description: "Read one " + p.Name() + " library playlist and its tracks. Use the opaque playlist ID from " + toolName(src, p, "playlists") + ".",
			InputSchema: map[string]any{"type": "object", "required": []string{"id"}, "properties": map[string]any{
				"id":    map[string]any{"type": "string", "minLength": 1, "maxLength": 200, "description": "Playlist ID from the playlists tool"},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Maximum tracks, default 100"},
			}},
		})
	}
	if _, ok := p.(nowPlayingProvider); ok {
		out = append(out, tool{
			Name:        p.ID() + "_now",
			Description: "Read what " + p.Name() + " is playing right now: title, artist, album, state, position and duration. A stopped player returns an empty title.",
			InputSchema: map[string]any{"type": "object"},
		})
	}
	if _, ok := p.(statsProvider); ok {
		out = append(out, tool{
			Name:        p.ID() + "_stats",
			Description: "Read the size of the " + p.Name() + " library: tracks, albums, artists, playlists and total duration, as far as the source can tell.",
			InputSchema: map[string]any{"type": "object"},
		})
	}
	if _, ok := p.(playbackController); ok {
		actions := controlActions(p)
		out = append(out, tool{
			Name:        p.ID() + "_control",
			Description: "Control playback on " + p.Name() + ": " + strings.Join(actions, ", ") + ". Use play_track with a track ID from a search and play_playlist with a playlist ID from the playlists tool. This changes what is playing, never the library.",
			InputSchema: map[string]any{"type": "object", "required": []string{"action"}, "properties": map[string]any{
				"action": map[string]any{"type": "string", "enum": actions, "description": "What to do"},
				"id":     map[string]any{"type": "string", "maxLength": 200, "description": "Track ID for play_track, playlist ID for play_playlist"},
			}},
		})
	}
	if _, ok := p.(snapshotSyncer); ok {
		out = append(out, tool{
			Name:        p.ID() + "_sync",
			Description: "Store a snapshot of the " + p.Name() + " music for the Music page. First call the matching phone tool, then pass its JSON object here unchanged. The page shows the last snapshot; this never changes the phone.",
			InputSchema: map[string]any{"type": "object", "description": "The snapshot the phone returned: synced_at, now_playing, recent, playlists, stats.",
				"additionalProperties": true},
		})
	}
	return out
}

// controlActions are the actions a controller advertises: all of them unless the provider says which it has.
func controlActions(p provider) []string {
	if cp, ok := p.(controlActionsProvider); ok {
		if actions := cp.ControlActions(); len(actions) > 0 {
			return actions
		}
	}
	return []string{"play", "pause", "playpause", "next", "previous", "stop", "play_track", "play_playlist"}
}

func (a *app) callTool(ctx context.Context, p provider, name string, raw json.RawMessage) (string, error) {
	action, ok := strings.CutPrefix(name, p.ID()+"_")
	if !ok {
		return "", fmt.Errorf("no tool %s", name)
	}
	switch action {
	case "search":
		sp, ok := p.(searchProvider)
		if !ok {
			return "", fmt.Errorf("no tool %s", name)
		}
		var in struct {
			Query string `json:"query"`
			Kind  string `json:"kind"`
			Limit int    `json:"limit"`
		}
		if err := decode(raw, &in); err != nil {
			return "", err
		}
		items, err := sp.Search(ctx, searchRequest{Query: in.Query, Kind: in.Kind, Limit: in.Limit})
		if err != nil {
			return "", err
		}
		return formatItems(fmt.Sprintf("%s results for %q", p.Name(), strings.TrimSpace(in.Query)), items), nil
	case "recent":
		rp, ok := p.(recentProvider)
		if !ok {
			return "", fmt.Errorf("no tool %s", name)
		}
		var in struct {
			Limit int `json:"limit"`
		}
		if err := decode(raw, &in); err != nil {
			return "", err
		}
		items, err := rp.Recent(ctx, in.Limit)
		if err != nil {
			return "", err
		}
		return formatItems(p.Name()+" recently played", items), nil
	case "playlists":
		pp, ok := p.(playlistsProvider)
		if !ok {
			return "", fmt.Errorf("no tool %s", name)
		}
		var in struct {
			Limit int `json:"limit"`
		}
		if err := decode(raw, &in); err != nil {
			return "", err
		}
		playlists, err := pp.Playlists(ctx, in.Limit)
		if err != nil {
			return "", err
		}
		return formatPlaylists(p.Name()+" playlists", playlists), nil
	case "playlist":
		pp, ok := p.(playlistProvider)
		if !ok {
			return "", fmt.Errorf("no tool %s", name)
		}
		var in struct {
			ID    string `json:"id"`
			Limit int    `json:"limit"`
		}
		if err := decode(raw, &in); err != nil {
			return "", err
		}
		playlist, tracks, err := pp.Playlist(ctx, in.ID, 0, in.Limit)
		if err != nil {
			return "", err
		}
		return formatPlaylist(p.Name(), playlist, tracks), nil
	case "now":
		np, ok := p.(nowPlayingProvider)
		if !ok {
			return "", fmt.Errorf("no tool %s", name)
		}
		now, err := np.NowPlaying(ctx)
		if err != nil {
			return "", err
		}
		return formatNow(p.Name(), now), nil
	case "stats":
		sp, ok := p.(statsProvider)
		if !ok {
			return "", fmt.Errorf("no tool %s", name)
		}
		stats, err := sp.Stats(ctx)
		if err != nil {
			return "", err
		}
		return formatStats(p.Name(), stats), nil
	case "control":
		cp, ok := p.(playbackController)
		if !ok {
			return "", fmt.Errorf("no tool %s", name)
		}
		var in struct {
			Action string `json:"action"`
			ID     string `json:"id"`
		}
		if err := decode(raw, &in); err != nil {
			return "", err
		}
		control := in.Action
		if in.Action == "play_track" || in.Action == "play_playlist" {
			if strings.TrimSpace(in.ID) == "" {
				return "", fmt.Errorf("%s needs an id", in.Action)
			}
			control = in.Action + ":" + in.ID
		}
		if err := cp.Control(ctx, control); err != nil {
			return "", err
		}
		a.invalidate()
		return fmt.Sprintf("%s: %s accepted.", p.Name(), in.Action), nil
	case "sync":
		sp, ok := p.(snapshotSyncer)
		if !ok {
			return "", fmt.Errorf("no tool %s", name)
		}
		text, err := sp.Sync(ctx, raw)
		if err != nil {
			return "", err
		}
		a.invalidate()
		return text, nil
	}
	return "", fmt.Errorf("no tool %s", name)
}

func decode(raw json.RawMessage, out any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	if dec.More() {
		return errors.New("invalid arguments: trailing JSON value")
	}
	return nil
}

func formatItems(title string, items []item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%d):\n", title, len(items))
	if len(items) == 0 {
		b.WriteString("None.\n")
	}
	for _, it := range items {
		writeItem(&b, it)
	}
	return clip(b.String(), 80000)
}

func writeItem(b *strings.Builder, it item) {
	fmt.Fprintf(b, "\n- %s", clip(it.Title, 300))
	var meta []string
	if it.Artist != "" {
		meta = append(meta, it.Artist)
	}
	if it.Album != "" && it.Album != it.Artist {
		meta = append(meta, it.Album)
	}
	if len(meta) > 0 {
		b.WriteString(" — " + strings.Join(meta, " · "))
	}
	if it.DurationMS > 0 {
		fmt.Fprintf(b, " (%s)", formatDuration(it.DurationMS))
	}
	if it.ID != "" {
		fmt.Fprintf(b, "\n  ID: %s", it.ID)
	}
	if it.URL != "" {
		b.WriteString("\n  " + it.URL)
	}
	if it.Descr != "" {
		b.WriteString("\n  " + clip(oneLine(it.Descr), 300))
	}
	b.WriteString("\n")
}

func formatPlaylists(title string, playlists []playlist) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%d):\n", title, len(playlists))
	if len(playlists) == 0 {
		b.WriteString("None.\n")
	}
	for _, pl := range playlists {
		fmt.Fprintf(&b, "\n- %s", clip(pl.Title, 300))
		if pl.TrackCount > 0 {
			fmt.Fprintf(&b, " (%d %s)", pl.TrackCount, trackWord(pl.TrackCount))
		}
		if pl.Owner != "" {
			b.WriteString(" — " + clip(pl.Owner, 200))
		}
		if pl.ID != "" {
			fmt.Fprintf(&b, "\n  ID: %s", pl.ID)
		}
		if pl.Descr != "" {
			b.WriteString("\n  " + clip(oneLine(pl.Descr), 300))
		}
		if pl.URL != "" {
			b.WriteString("\n  " + pl.URL)
		}
		b.WriteString("\n")
	}
	return clip(b.String(), 80000)
}

func formatPlaylist(providerName string, pl playlist, tracks []item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s playlist %q", providerName, clip(pl.Title, 300))
	if pl.TrackCount > 0 {
		fmt.Fprintf(&b, " (%d %s)", pl.TrackCount, trackWord(pl.TrackCount))
	}
	if pl.Owner != "" {
		b.WriteString(" — " + clip(pl.Owner, 200))
	}
	b.WriteString(":\n")
	if pl.URL != "" {
		b.WriteString(pl.URL + "\n")
	}
	for _, t := range tracks {
		writeItem(&b, t)
	}
	return clip(b.String(), 80000)
}

func formatNow(providerName string, now nowPlaying) string {
	if now.Title == "" {
		return fmt.Sprintf("%s is not playing anything.", providerName)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s is %s: %s", providerName, cmpOr(now.State, "playing"), clip(now.Title, 300))
	var meta []string
	if now.Artist != "" {
		meta = append(meta, now.Artist)
	}
	if now.Album != "" && now.Album != now.Artist {
		meta = append(meta, now.Album)
	}
	if len(meta) > 0 {
		b.WriteString(" — " + strings.Join(meta, " · "))
	}
	if now.DurationMS > 0 {
		fmt.Fprintf(&b, " (%s", formatDuration(now.PositionMS))
		fmt.Fprintf(&b, " / %s)", formatDuration(now.DurationMS))
	}
	if now.URL != "" {
		b.WriteString("\n" + now.URL)
	}
	return b.String()
}

func formatStats(providerName string, s libraryStats) string {
	var parts []string
	if s.Tracks > 0 {
		parts = append(parts, fmt.Sprintf("%d tracks", s.Tracks))
	}
	if s.Albums > 0 {
		parts = append(parts, fmt.Sprintf("%d albums", s.Albums))
	}
	if s.Artists > 0 {
		parts = append(parts, fmt.Sprintf("%d artists", s.Artists))
	}
	if s.Playlists > 0 {
		parts = append(parts, fmt.Sprintf("%d playlists", s.Playlists))
	}
	if s.DurationMS > 0 {
		parts = append(parts, formatLongDuration(s.DurationMS))
	}
	if len(parts) == 0 {
		return providerName + " has no library statistics."
	}
	return providerName + ": " + strings.Join(parts, ", ") + "."
}

func trackWord(n int) string {
	if n == 1 {
		return "track"
	}
	return "tracks"
}

func formatDuration(ms int) string {
	seconds := ms / 1000
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func formatLongDuration(ms int) string {
	minutes := ms / 60000
	if minutes < 60 {
		return fmt.Sprintf("%d min", minutes)
	}
	return fmt.Sprintf("%d h %d min", minutes/60, minutes%60)
}

func cmpOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
