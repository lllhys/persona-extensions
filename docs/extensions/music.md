# Music

[Documentation](../README.md) · [Source](../../extensions/music/) · [Design](../../extensions/music/DESIGN.md)

Music in Persona. The extension is a generic music extension: **one connector per service**, with Apple Music as the
first. Apple Music has two endpoints:

- **Mac**: the Music library and player on a Mac you choose.
- **iPhone**: a snapshot of the Music app on your iPhone, read by the Persona app.

Spotify, NetEase Cloud Music and QQ Music are planned as their own connectors; see [adding a source](../../extensions/music/ADDING_A_SOURCE.md).

The **Music** page in the navigation opens with a source switch — **All sources** and **Apple Music** — and merges the endpoints you configured below it, one labeled section per device. If none is configured, it shows how to connect one.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `connectors` | `applemusic` | The Mac's library and player, and the iPhone snapshot, with assistant tools and one gated write. |
| `ui` | `music`, placement `nav_page`, icon `music` | The Music page: the source switch (All sources, Apple Music), a connect page when nothing is on, and the player page. |
| `ui` | `page`, placement `plugin_page` | The same page for older hubs. |
| `skills` | `music` | The assistant's workflow: read, play, sync the phone, then write after confirmation. |
| `prompts` | `listening-now`, `build-playlist`, `play-something` | Three quick prompts in the assistant's input box. |

## Setup

The extension is not in the market catalog yet. Build a local package and install it on the hub:

```sh
scripts/package.sh music
```

See [installation](../installation.md#install-a-local-package) for the install step.

Then open **Settings › Extensions › Music** and choose one or both endpoints.

### Mac

Turn on the Mac. It must be online, signed in to Music, and running a Persona node that supports programs on computers.

The first read makes macOS ask for **Automation** permission for Persona to control Music. Allow it; if the prompt was dismissed, open **System Settings › Privacy & Security › Automation** on the Mac and turn on Persona under Music. The endpoint reads the Mac's local library (including cloud tracks when Music syncs its library), shows artwork, and can play, pause, skip and create playlists there. It does not search the Apple Music catalog.

### iPhone

Turn on **iPhone**. The extension cannot call the phone directly; the assistant reads a snapshot:

1. Open Persona on the iPhone (the Music source must be on).
2. Ask the assistant, for example: "Sync my iPhone music."
3. The assistant calls the phone's `phone_music_snapshot` tool, then passes the result to `applemusic_iphone_sync`.
4. The Music page shows the snapshot and when it was taken.

Playback on the iPhone still uses the phone tools: ask the assistant to play something on the iPhone, or use `phone_media_control` to pause, skip or check what is playing.

## The page

The switch atop the page chooses the **sources**, the services you connected: **All sources** and **Apple Music**.
With one service both choices show the same music; All sources is there for the day a second service is added. The
switch stays inside the page while every label fits (at most five services of twelve characters each, which covers
Apple Music, Spotify, NetEase and QQ Music); with more or longer services it becomes the page's tabs, then a filter.
There is no source-status table, and an endpoint's data appears only when that endpoint is configured.

Inside Apple Music, the Mac and iPhone endpoints are merged, and every item says which endpoint it came from when both are configured (a **Mac** or **iPhone** badge on now playing, the endpoint after the artist on a tile or playlist). The hub cannot read the Apple Music account identifier from either device, so the page never claims two endpoints are the same account: items and library sizes stay attributed to the device they came from. If the two devices are signed in to different Apple IDs, their libraries appear side by side instead of being silently mixed.

Each source shows:

- **Now playing**: artwork, title, artist, state, position and duration. On the Mac, the row's menu has play/pause, previous and next. Tapping the row opens the player page: large artwork, the Mac's controls, and the lyrics when the Mac has them. The iPhone snapshot has no lyrics and no playback control; its player page says so. There is no progress bar, and the row shows the track length rather than the position: an extension page cannot redraw faster than every 30 seconds, so a live reading would only jump.
- **Recently played** as a carousel with artwork.
- **Playlists** with artwork; a row opens the playlist's tracks, 50 per page with covers, and **Previous tracks** / **Next tracks** page through the rest. Each page stays inside the 50-picture limit.
- **Library** size; with both endpoints, one library section per device.
- A search box on the Mac.
- **Refresh**; the page draws again every 60 seconds.

The iPhone page adds **Last synced …** and the sync instruction. When no endpoint is configured, the page is a connect card pointing to Settings; when the Apple Music connector itself is switched off, it says that instead.

Lyrics come from the Mac's Music library (`lyrics of current track`) and are cached for ten minutes per track. Apple Music's cloud-only lyrics are not guaranteed; a track without lyrics says so.

The extension page cannot pin a floating mini-player to the bottom of the screen: the page schema has no such component, and only the app itself could draw one. The player page is what an extension can offer.

The Mac has no artwork URL through AppleScript, so the extension exports the picture on the Mac, shrinks it and serves it through the hub. The hub caches it. A missing picture shows a placeholder. iPhone artwork URLs are loaded directly, but only from Apple's artwork CDN; any other host in a snapshot is dropped.

## The tools

Mac:

- `applemusic_mac_search {query, limit?}` — the Mac's library (songs).
- `applemusic_mac_recent`, `applemusic_mac_playlists`, `applemusic_mac_playlist {id}`, `applemusic_mac_now`, `applemusic_mac_stats`.
- `applemusic_mac_control {action, id?}` — play, pause, playpause, next, previous, stop, play_track with a track ID, or play_playlist with a playlist ID.
- `applemusic_mac_create_playlist {name, description?, track_ids, play?}` — creates a playlist in the Mac's Music app from library track IDs; with `play: true` it starts playing there. Always confirmed on the phone.

iPhone:

- `applemusic_iphone_recent`, `applemusic_iphone_playlists`, `applemusic_iphone_now`, `applemusic_iphone_stats` — the last snapshot.
- `applemusic_iphone_sync` — stores the JSON from the phone's `phone_music_snapshot`.

On the iPhone the assistant also has the phone's own music tools: `phone_media_play {query, kind?}` plays one catalog song, album or playlist, and `phone_music_create_playlist {name, songs, description?, play?}` searches the catalog for each song, creates the playlist in the user's library and plays it (the user approves it on the phone).

The confirmation card names the destination and the track count. The extension keeps a write receipt, so a retried request with the same ID is not made twice.

## Playing

- On the Mac, the page's now-playing row and `applemusic_mac_control` control that Mac's player. The assistant can play a library track from `applemusic_mac_search` by ID, play an existing playlist with `play_playlist`, or create a playlist from library tracks with `applemusic_mac_create_playlist {play: true}` and have it start. The Mac cannot play a song that is not in its library.
- On the iPhone, the assistant uses `phone_media_play` to search and play a catalog song, `phone_music_create_playlist` to create a playlist from several songs and play it, and `phone_media_control` to pause, skip or report the current track.
- The iPhone snapshot is not a player: syncing it does not start playback.

## Caching

The Mac provider caches status, now playing, recent plays, playlists, search, lyrics and library size for a short while (15 seconds to 10 minutes); the Mac program has its own cache behind it. The page's **Refresh** clears both. The iPhone snapshot changes only when the assistant syncs it. Nothing is fetched from the internet.

## Limits

- Recently played is short; there is no complete listening history.
- The Mac endpoint reads only that Mac's library and cannot search the catalog.
- The iPhone endpoint is a snapshot, not live; sync it when it is stale.
- Lyrics are read on the Mac only, and only when the track has them in the library; the iPhone snapshot has no lyrics.
- The page cannot float a bottom mini-player; the player page is the extension-side equivalent.
- A Mac can be offline or missing Automation permission; the page says what failed.

## Development

```sh
go -C extensions/music test -race -count=1 -timeout=120s ./...
python3 -I scripts/manifest.py check
scripts/package.sh music
scripts/ci.sh
```

The tests use a fake Mac node, a fake iPhone snapshot and a fake second service; they need no account, key or Mac. The
design is in the [design](../../extensions/music/DESIGN.md); adding Spotify, NetEase Cloud Music, QQ Music or another
service is described in [adding a source](../../extensions/music/ADDING_A_SOURCE.md).
