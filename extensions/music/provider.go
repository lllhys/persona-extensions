package main

import (
	"context"
	"encoding/json"
	"time"
)

// item is one thing a provider can return: a song, album, playlist or artist. The page and the tools only need these
// fields, so a second provider can fill them from its own API without changing them.
type item struct {
	Provider   string
	ID         string
	Kind       string // song, album, playlist or artist
	Title      string
	Artist     string
	Album      string
	Descr      string
	ArtworkURL string
	URL        string
	DurationMS int
	ISRC       string
}

// playlist is an item that is a playlist, with its track count and owner.
type playlist struct {
	item
	TrackCount int
	Owner      string
}

// searchRequest is one catalog search.
type searchRequest struct {
	Query string
	Kind  string // song (default), album, playlist or artist
	Limit int
}

// createPlaylistRequest is one playlist to create in the user's library.
type createPlaylistRequest struct {
	Name        string
	Description string
	TrackIDs    []string
}

// nowPlaying is what a player is playing right now.
type nowPlaying struct {
	ID         string
	Title      string
	Artist     string
	Album      string
	State      string // playing, paused, stopped
	PositionMS int
	DurationMS int
	ArtworkURL string
	URL        string
}

// libraryStats is the size of a provider's library, as far as it can tell.
type libraryStats struct {
	Tracks     int
	Albums     int
	Artists    int
	Playlists  int
	DurationMS int
}

// providerStatus is a connector's state, from memory only (the hub asks every 30 seconds).
type providerStatus struct {
	State    string    `json:"state"`
	Account  string    `json:"account,omitempty"`
	Error    string    `json:"error,omitempty"`
	Guide    []string  `json:"guide,omitempty"`
	LastSync time.Time `json:"last_sync,omitzero"`
}

// provider is what every music service has: an identity and a state. The capabilities below are optional, so a
// provider implements only what its API can do and the tool and page builders adapt. A provider is one endpoint of
// one source (a Mac, a phone, a cloud account); the connector id and the source's tools live in app.go.
type provider interface {
	ID() string
	Name() string
	// Status reads the endpoint's state for a page draw in the user's language: guides and errors are user-visible.
	Status(ctx context.Context, locale string) providerStatus
	// OpenHosts are the hosts whose https links the page may open; empty means no links.
	OpenHosts() []string
}

type searchProvider interface {
	Search(ctx context.Context, req searchRequest) ([]item, error)
}

// kindSearchProvider says which kinds a search can return; a source without it searches songs only.
type kindSearchProvider interface {
	SearchKinds() []string
}

func searchKinds(p provider) []string {
	if k, ok := p.(kindSearchProvider); ok && len(k.SearchKinds()) > 0 {
		return k.SearchKinds()
	}
	return []string{"song"}
}

type recentProvider interface {
	Recent(ctx context.Context, limit int) ([]item, error)
}

type playlistsProvider interface {
	Playlists(ctx context.Context, limit int) ([]playlist, error)
}

type playlistProvider interface {
	// Playlist returns the playlist and the window of tracks starting at offset: a page shows one window at a time so
	// every row can carry its cover and stay under the page's picture limit.
	Playlist(ctx context.Context, id string, offset, limit int) (playlist, []item, error)
}

type playlistCreator interface {
	CreatePlaylist(ctx context.Context, req createPlaylistRequest) (playlist, error)
}

// artworkProvider serves a picture for a page ref (ui.image): bytes of a png, jpeg, webp or gif.
type artworkProvider interface {
	Artwork(ctx context.Context, kind, id string, width int) ([]byte, error)
}

// artworkHostsProvider is the hosts whose https artwork URLs a source may put on the page; the client loads them
// directly, so a snapshot the assistant can write never gets an arbitrary host. A source without it gives no URLs.
type artworkHostsProvider interface {
	ArtworkHosts() []string
}

// lyricsProvider reads the words of what a source is playing.
type lyricsProvider interface {
	Lyrics(ctx context.Context, id string) (string, error)
}

type nowPlayingProvider interface {
	NowPlaying(ctx context.Context) (nowPlaying, error)
}

type playbackController interface {
	Control(ctx context.Context, action string) error
}

// controlActionsProvider says which control actions an endpoint really has: the tool schema and the page's menus
// list only these (a source without "stop" or "previous" does not advertise them).
type controlActionsProvider interface {
	ControlActions() []string
}

// createPlaylistHintProvider adds one sentence to the create-playlist tool description and card, in the endpoint's
// own terms (the Mac plays only its library, say). A source without it gets no extra sentence.
type createPlaylistHintProvider interface {
	CreatePlaylistHint() string
}

// playbackNoteProvider explains where playback happens on an endpoint the page cannot control (the iPhone's
// snapshot): the player page shows it instead of Apple-specific copy.
type playbackNoteProvider interface {
	PlaybackNote(locale string) string
}

type statsProvider interface {
	Stats(ctx context.Context) (libraryStats, error)
}

// snapshotSyncer is a provider that takes a snapshot of another device's music; the iPhone provider stores what the
// assistant read through the phone's own tools.
type snapshotSyncer interface {
	Sync(ctx context.Context, raw json.RawMessage) (string, error)
}

// refresher lets the page's refresh button clear a recorded error before it tries again.
type refresher interface {
	Refresh()
}
