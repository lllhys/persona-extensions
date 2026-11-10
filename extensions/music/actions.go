package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// actionToolsFor builds the writes an endpoint supports. They are declared separately from the read tools, so the hub
// puts every one behind its confirmation card.
func actionToolsFor(src *source, p provider) []tool {
	var out []tool
	if _, ok := p.(playlistCreator); ok {
		props := map[string]any{
			"name":        map[string]any{"type": "string", "minLength": 1, "maxLength": 100, "description": "Playlist name"},
			"description": map[string]any{"type": "string", "maxLength": 1000, "description": "Optional description"},
			"track_ids":   map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 200}, "description": "Track IDs from " + toolName(src, p, "search")},
		}
		description := "Create a playlist in the user's " + p.Name() + " library after explicit confirmation. Give the name, an optional description and 1 to 100 track IDs from " + toolName(src, p, "search") + "."
		if _, canPlay := p.(playbackController); canPlay {
			props["play"] = map[string]any{"type": "boolean", "description": "Start playing the new playlist after creating it"}
			description += " With play: true it also starts playing the new playlist. The card shows the destination, the track count and whether it will play."
		} else {
			description += " The card shows the destination and the track count."
		}
		if hp, ok := p.(createPlaylistHintProvider); ok {
			if hint := strings.TrimSpace(hp.CreatePlaylistHint()); hint != "" {
				description += " " + hint
			}
		}
		out = append(out, tool{
			Name:        p.ID() + "_create_playlist",
			Description: description,
			InputSchema: map[string]any{"type": "object", "required": []string{"name", "track_ids"}, "properties": props},
		})
	}
	return out
}

type createPlaylistArgs struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	TrackIDs    []string `json:"track_ids"`
	Play        bool     `json:"play"`
}

func parseCreatePlaylist(raw json.RawMessage) (createPlaylistRequest, bool, error) {
	var in createPlaylistArgs
	if err := decode(raw, &in); err != nil {
		return createPlaylistRequest{}, false, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len([]rune(name)) > 100 {
		return createPlaylistRequest{}, false, errors.New("name must be 1 to 100 characters")
	}
	description := strings.TrimSpace(in.Description)
	if len([]rune(description)) > 1000 {
		return createPlaylistRequest{}, false, errors.New("description is too long")
	}
	if len(in.TrackIDs) == 0 || len(in.TrackIDs) > 100 {
		return createPlaylistRequest{}, false, errors.New("track_ids must have 1 to 100 entries")
	}
	ids := make([]string, 0, len(in.TrackIDs))
	for _, id := range in.TrackIDs {
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 200 {
			return createPlaylistRequest{}, false, errors.New("every track_id must be 1 to 200 characters")
		}
		ids = append(ids, id)
	}
	return createPlaylistRequest{Name: name, Description: description, TrackIDs: ids}, in.Play, nil
}

func describeAction(p provider, name string, raw json.RawMessage) (map[string]any, error) {
	action, ok := strings.CutPrefix(name, p.ID()+"_")
	if !ok {
		return nil, fmt.Errorf("unknown action %q", name)
	}
	var summary string
	switch action {
	case "create_playlist":
		if _, ok := p.(playlistCreator); !ok {
			return nil, fmt.Errorf("unknown action %q", name)
		}
		req, play, err := parseCreatePlaylist(raw)
		if err != nil {
			return nil, err
		}
		summary = fmt.Sprintf("Create a playlist in %s: %q (%d tracks).", p.Name(), req.Name, len(req.TrackIDs))
		if play {
			if _, canPlay := p.(playbackController); canPlay {
				summary += "\nIt will start playing the new playlist."
			}
		}
		if req.Description != "" {
			summary += "\nDescription: " + clip(oneLine(req.Description), 300)
		}
	default:
		return nil, fmt.Errorf("unknown action %q", name)
	}
	return map[string]any{"action": map[string]any{"name": name, "visible": "owner"}, "summary": summary}, nil
}

func (a *app) doAction(ctx context.Context, src *source, p provider, requestID, name string, raw json.RawMessage) (map[string]any, error) {
	action, ok := strings.CutPrefix(name, p.ID()+"_")
	if !ok {
		return nil, fmt.Errorf("unknown action %q", name)
	}
	var key string
	var run func() (string, error)
	switch action {
	case "create_playlist":
		creator, ok := p.(playlistCreator)
		if !ok {
			return nil, fmt.Errorf("unknown action %q", name)
		}
		req, play, err := parseCreatePlaylist(raw)
		if err != nil {
			return nil, err
		}
		if _, canPlay := p.(playbackController); !canPlay {
			play = false // a provider without playback must not get a play:true it cannot honor
		}
		key = keyFor(action, src.id, p.ID(), map[string]any{"args": req, "play": play})
		run = func() (string, error) {
			pl, err := creator.CreatePlaylist(ctx, req)
			if err != nil {
				return "", err
			}
			out := fmt.Sprintf("Created a playlist in %s: %q (%d tracks).", p.Name(), pl.Title, pl.TrackCount)
			if play {
				cp, ok := p.(playbackController)
				if !ok {
					play = false
				} else if err := cp.Control(ctx, "play_playlist:"+pl.ID); err != nil {
					// The write itself happened: keep the receipt so a retry cannot create the playlist twice, and say
					// exactly what did and did not happen.
					text := fmt.Sprintf("Created the playlist %q (%d tracks) in %s, but playing it failed: %s", pl.Title, pl.TrackCount, p.Name(), oneLine(err.Error()))
					return "", &writtenError{text: text, err: errors.New(text)}
				}
				if play {
					out += "\nPlaying it now."
				}
			}
			if pl.URL != "" {
				out += "\n" + pl.URL
			}
			return out, nil
		}
	default:
		return nil, fmt.Errorf("unknown action %q", name)
	}
	text, err := a.receipts.commit(requestID, key, run)
	if err != nil {
		return nil, err
	}
	a.invalidate()
	return map[string]any{"text": text}, nil
}

func keyFor(action, sourceID, endpointID string, args any) string {
	b, _ := json.Marshal(map[string]any{"action": action, "source": sourceID, "endpoint": endpointID, "args": args})
	return string(b)
}
