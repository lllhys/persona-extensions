package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The iPhone provider is a snapshot: the Persona app's MusicKit tool reads the phone, and the assistant hands the
// result to this extension with iphone_sync. A hub extension cannot call a phone tool itself, so the page shows the
// last snapshot and says when it was taken.

type iphoneNow struct {
	Title      string `json:"title"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	State      string `json:"state,omitempty"`
	PositionMS int    `json:"position_ms,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
	ArtworkURL string `json:"artwork_url,omitempty"`
	URL        string `json:"url,omitempty"`
}

type iphoneItem struct {
	ID         string `json:"id,omitempty"`
	Title      string `json:"title"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	Descr      string `json:"description,omitempty"`
	ArtworkURL string `json:"artwork_url,omitempty"`
	URL        string `json:"url,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
	PlayedAt   string `json:"played_at,omitempty"`
}

type iphonePlaylist struct {
	ID         string `json:"id,omitempty"`
	Title      string `json:"title"`
	Descr      string `json:"description,omitempty"`
	ArtworkURL string `json:"artwork_url,omitempty"`
	URL        string `json:"url,omitempty"`
	TrackCount int    `json:"track_count,omitempty"`
}

type iphoneStats struct {
	Tracks     int `json:"tracks,omitempty"`
	Albums     int `json:"albums,omitempty"`
	Artists    int `json:"artists,omitempty"`
	Playlists  int `json:"playlists,omitempty"`
	DurationMS int `json:"duration_ms,omitempty"`
}

type iphoneSnapshot struct {
	SyncedAt   time.Time        `json:"synced_at"`
	NowPlaying *iphoneNow       `json:"now_playing,omitempty"`
	Recent     []iphoneItem     `json:"recent,omitempty"`
	Playlists  []iphonePlaylist `json:"playlists,omitempty"`
	Stats      *iphoneStats     `json:"stats,omitempty"`
}

func (s iphoneSnapshot) empty() bool {
	return s.SyncedAt.IsZero() && s.NowPlaying == nil && len(s.Recent) == 0 && len(s.Playlists) == 0 && s.Stats == nil
}

// snapshotStore keeps the last snapshot in the extension's data directory.
type snapshotStore struct {
	path string

	mu     sync.Mutex
	snap   iphoneSnapshot
	loaded bool
	err    error
}

func openSnapshotStore(dir string) *snapshotStore {
	s := &snapshotStore{}
	if dir != "" {
		s.path = filepath.Join(dir, "iphone-snapshot.json")
	}
	return s
}

func (s *snapshotStore) get() iphoneSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		s.loaded = true
		if s.path == "" {
			return s.snap
		}
		data, err := os.ReadFile(s.path)
		if errors.Is(err, os.ErrNotExist) {
			return s.snap
		}
		if err != nil {
			s.err = err
			return s.snap
		}
		if len(data) > 4<<20 {
			s.err = errors.New("the iPhone snapshot is too large")
			return s.snap
		}
		if err := json.Unmarshal(data, &s.snap); err != nil {
			s.err = err
			s.snap = iphoneSnapshot{}
		}
	}
	return s.snap
}

func (s *snapshotStore) save(snap iphoneSnapshot) error {
	normalizeSnapshot(&snap)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		data, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		if len(data) > 4<<20 {
			return errors.New("the iPhone snapshot is too large; sync fewer playlists or tracks")
		}
		dir := filepath.Dir(s.path)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		f, err := os.CreateTemp(dir, ".iphone-*")
		if err != nil {
			return err
		}
		name := f.Name()
		defer os.Remove(name)
		if err := f.Chmod(0600); err != nil {
			f.Close()
			return err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if err := os.Rename(name, s.path); err != nil {
			return err
		}
	}
	s.snap = snap
	s.loaded = true
	s.err = nil
	return nil
}

// normalizeSnapshot trims what a phone can send and keeps the page within its limits.
func normalizeSnapshot(s *iphoneSnapshot) {
	if len(s.Recent) > 100 {
		s.Recent = s.Recent[:100]
	}
	if len(s.Playlists) > 100 {
		s.Playlists = s.Playlists[:100]
	}
	for i := range s.Recent {
		s.Recent[i].ID = clip(s.Recent[i].ID, 200)
		s.Recent[i].Title = clip(s.Recent[i].Title, 500)
		s.Recent[i].Artist = clip(s.Recent[i].Artist, 300)
		s.Recent[i].Album = clip(s.Recent[i].Album, 300)
		s.Recent[i].Descr = clip(s.Recent[i].Descr, 1000)
		s.Recent[i].ArtworkURL = clip(s.Recent[i].ArtworkURL, 2000)
		s.Recent[i].URL = clip(s.Recent[i].URL, 2000)
		s.Recent[i].PlayedAt = clip(s.Recent[i].PlayedAt, 100)
	}
	// A row or tile without a title is refused by the page checker, and the bad snapshot is kept: drop such entries
	// here rather than break every later render.
	recent := s.Recent[:0]
	for _, it := range s.Recent {
		if strings.TrimSpace(it.Title) != "" {
			recent = append(recent, it)
		}
	}
	s.Recent = recent
	for i := range s.Playlists {
		s.Playlists[i].ID = clip(s.Playlists[i].ID, 200)
		s.Playlists[i].Title = clip(s.Playlists[i].Title, 500)
		s.Playlists[i].Descr = clip(s.Playlists[i].Descr, 1000)
		s.Playlists[i].ArtworkURL = clip(s.Playlists[i].ArtworkURL, 2000)
		s.Playlists[i].URL = clip(s.Playlists[i].URL, 2000)
		if s.Playlists[i].TrackCount < 0 {
			s.Playlists[i].TrackCount = 0
		}
	}
	playlists := s.Playlists[:0]
	for _, pl := range s.Playlists {
		if strings.TrimSpace(pl.Title) != "" {
			playlists = append(playlists, pl)
		}
	}
	s.Playlists = playlists
	if s.NowPlaying != nil {
		s.NowPlaying.Title = clip(s.NowPlaying.Title, 500)
		s.NowPlaying.Artist = clip(s.NowPlaying.Artist, 300)
		s.NowPlaying.Album = clip(s.NowPlaying.Album, 300)
		s.NowPlaying.State = clip(s.NowPlaying.State, 20)
		s.NowPlaying.ArtworkURL = clip(s.NowPlaying.ArtworkURL, 2000)
		s.NowPlaying.URL = clip(s.NowPlaying.URL, 2000)
		if s.NowPlaying.Title == "" {
			s.NowPlaying = nil
		}
	}
}

type iphoneProvider struct {
	store *snapshotStore
	now   func() time.Time
}

func newIPhoneProvider(store *snapshotStore) *iphoneProvider {
	return &iphoneProvider{store: store, now: time.Now}
}

func (p *iphoneProvider) ID() string   { return iphoneID }
func (p *iphoneProvider) Name() string { return "iPhone" }

func (p *iphoneProvider) OpenHosts() []string { return []string{"music.apple.com"} }

// ArtworkHosts: MusicKit's artwork URLs come from Apple's artwork CDN; any other host in a snapshot is dropped.
func (p *iphoneProvider) ArtworkHosts() []string { return []string{"mzstatic.com"} }

func (p *iphoneProvider) Status(_ context.Context, locale string) providerStatus {
	t := textsFor(locale)
	snap := p.store.get()
	if snap.empty() {
		// The source is usable before the first snapshot: iphone_sync is how the first one arrives. The connector must
		// stay ok, or the hub would not offer that tool.
		return providerStatus{State: "ok", Account: "iPhone",
			Guide: []string{t.iphoneGuide1, t.iphoneGuide2}}
	}
	st := providerStatus{State: "ok", Account: "iPhone", LastSync: snap.SyncedAt}
	if !snap.SyncedAt.IsZero() && p.now().Sub(snap.SyncedAt) > 7*24*time.Hour {
		st.Error = t.iphoneStale
		st.Guide = []string{t.iphoneSyncAgain}
	}
	return st
}

// PlaybackNote is the player page's sentence for an endpoint the page cannot control.
func (p *iphoneProvider) PlaybackNote(locale string) string { return textsFor(locale).phonePlayback }

func (p *iphoneProvider) Recent(_ context.Context, limit int) ([]item, error) {
	limit = clamp(limit, 20, 1, 50)
	snap := p.store.get()
	if limit <= 0 || limit > len(snap.Recent) {
		limit = len(snap.Recent)
	}
	out := make([]item, 0, limit)
	for _, it := range snap.Recent[:limit] {
		out = append(out, it.item())
	}
	return out, nil
}

func (p *iphoneProvider) Playlists(_ context.Context, limit int) ([]playlist, error) {
	limit = clamp(limit, 25, 1, 50)
	snap := p.store.get()
	if limit <= 0 || limit > len(snap.Playlists) {
		limit = len(snap.Playlists)
	}
	out := make([]playlist, 0, limit)
	for _, pl := range snap.Playlists[:limit] {
		out = append(out, playlist{
			item:       item{Provider: iphoneID, ID: pl.ID, Kind: "playlist", Title: pl.Title, Descr: pl.Descr, ArtworkURL: pl.ArtworkURL, URL: pl.URL},
			TrackCount: pl.TrackCount,
		})
	}
	return out, nil
}

func (p *iphoneProvider) Stats(context.Context) (libraryStats, error) {
	snap := p.store.get()
	if snap.Stats == nil {
		return libraryStats{Tracks: 0, Playlists: len(snap.Playlists)}, nil
	}
	return libraryStats{Tracks: snap.Stats.Tracks, Albums: snap.Stats.Albums, Artists: snap.Stats.Artists,
		Playlists: max(snap.Stats.Playlists, len(snap.Playlists)), DurationMS: snap.Stats.DurationMS}, nil
}

func (p *iphoneProvider) NowPlaying(context.Context) (nowPlaying, error) {
	snap := p.store.get()
	if snap.NowPlaying == nil {
		return nowPlaying{}, nil
	}
	n := snap.NowPlaying
	return nowPlaying{Title: n.Title, Artist: n.Artist, Album: n.Album, State: n.State,
		PositionMS: n.PositionMS, DurationMS: n.DurationMS, ArtworkURL: n.ArtworkURL, URL: n.URL}, nil
}

// Sync stores a snapshot the assistant read through the phone's tools.
func (p *iphoneProvider) Sync(_ context.Context, raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errors.New("the snapshot is empty")
	}
	if len(raw) > 4<<20 {
		return "", errors.New("the snapshot is too large")
	}
	var snap iphoneSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return "", fmt.Errorf("the snapshot is not valid JSON: %w", err)
	}
	normalizeSnapshot(&snap)
	if snap.empty() {
		return "", errors.New("the snapshot has no now playing, recent tracks or playlists")
	}
	// The hub's own time, not the caller's: the snapshot is only as fresh as the sync that brought it, and the
	// assistant can write any synced_at it likes.
	snap.SyncedAt = p.now().UTC()
	if err := p.store.save(snap); err != nil {
		return "", err
	}
	return fmt.Sprintf("Synced the iPhone music snapshot: %d recent tracks, %d playlists.", len(snap.Recent), len(snap.Playlists)), nil
}

func (it iphoneItem) item() item {
	return item{Provider: iphoneID, ID: it.ID, Kind: "song", Title: it.Title, Artist: it.Artist, Album: it.Album,
		Descr: it.Descr, ArtworkURL: it.ArtworkURL, URL: it.URL, DurationMS: it.DurationMS}
}
