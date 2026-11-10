# Music extension — design

[Source](README.md) · [User guide](../../docs/extensions/music.md)

**Status.** v0.8 is a generic music extension: one **connector per service** and one page over all of them. Apple
Music is the first source, with two endpoints (a Mac running Music.app read and controlled by a program on that Mac,
and an iPhone snapshot read by the Persona app). The **Music** page opens with a **source** switch — All sources and
Apple Music — merging the endpoints of each service, labeling every item by service and device; it shows a player
page with lyrics, playlists that page through their tracks 50 at a time with covers, and a connect page when nothing
is on. The program is a source registry (app.go): a new service adds a provider file, a manifest connector and a
skill without touching the page or the shared tool builders ([ADDING_A_SOURCE.md](ADDING_A_SOURCE.md)).

## Goals

- One place in Persona for Apple Music on the devices the user chooses.
- Assistant first: the most useful surface is the assistant's tools, not another player.
- One page that shows the configured endpoints with the same layout on every client.
- Local-first: library data stays on the hub and the devices the user chose.
- No developer account, no token and no network from the extension for the device endpoints.

## Non-goals

- Streaming audio inside Persona.
- A complete listening history.
- Reading a phone's library from the hub without code on the phone. iOS exposes no such remote API; the Persona app's
  phone tool is the bridge.
- A source that a contributed connector cannot express (for example an OAuth redirect flow until the platform has
  one; see ADDING_A_SOURCE.md, "Authentication today").

## Architecture

One extension package, one connector per service, one page over all of them:

```text
music (extension)
├── connector applemusic        status, tools, actions      (one per service)
│   ├── endpoint mac             Music.app on the selected Mac
│   └── endpoint iphone          a snapshot from the Persona app
├── ui
│   ├── music                    nav_page, and plugin_page "page" for older hubs
└── assistant
    ├── music                    the rules every service shares
    └── applemusic               Apple Music's tools and limits
```

The program keeps a **source registry** (`app.go`): a source is a connector id, its display names and its endpoints,
built from the configuration. Each endpoint implements the required `provider` and the optional capability
interfaces it can:

| Interface | Methods | Mac | iPhone |
| --- | --- | --- | --- |
| `searchProvider` | library search | yes | — |
| `recentProvider` | recently played | local play dates | snapshot |
| `playlistsProvider` | list playlists | yes | snapshot |
| `playlistProvider` | one playlist and a window of its tracks (offset, limit) | yes | — |
| `nowPlayingProvider` | what is playing | yes | snapshot |
| `playbackController` | play, pause, next, previous | yes | — (phone tools) |
| `statsProvider` | library size | tracks, playlists, duration | snapshot counts |
| `playlistCreator` | create a playlist (gated) | yes | — |
| `snapshotSyncer` | store a phone snapshot | — | yes |
| `artworkProvider` | a page picture (ui.image) | yes | — |
| `artworkHostsProvider` | https artwork hosts a snapshot may name | — | Apple's artwork CDN |
| `lyricsProvider` | the words of the current track | yes | — (the snapshot has none) |

The tool builder and the page use type assertions. Each connector prefixes its endpoints' tools with its own id, so
the assistant sees `applemusic_mac_search`, `applemusic_iphone_sync` and, for a second service, `spotify_web_search`
and so on. One place (`splitTool`, per contribution) maps those names back to the endpoint and the endpoint-local
tool.

### Why one connector per service

The user configures one connection per service: a Mac selector and an iPhone switch for Apple Music, a sign-in for a
cloud service. A Mac and a phone are endpoints of the same account, not separate services, so they live under one
connector; a different streaming service is a different connector. The hub sees one connector per service for the
status gate, the settings page and the assistant's tool namespace; the page merges all of them under All sources and
shows one tab per service. A new service is a manifest connector, a provider file, a builder call and a skill — the
page, the tool builders and the write gate do not change ([ADDING_A_SOURCE.md](ADDING_A_SOURCE.md)).

## Contributions

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `connectors` | `applemusic` (one per service) | Status, the configured endpoints' tools and the Mac playlist write. A second service adds its own connector id, hosts and `confirm_always`. |
| `ui` | `music`, placement `nav_page`, icon `music` | The Music page: the source switch, the merged services, the player page; a connect page if none. |
| `ui` | `page`, placement `plugin_page` | The same page for hubs that do not know `nav_page`. |
| `skills` | `music` | The rules every service shares: writes are gated, a link is not playback, snapshots are not live, name the service. |
| `skills` | `applemusic` | Apple Music's tools, the iPhone sync, the Mac's library-only limit and the write boundary. |
| `prompts` | three quick prompts | "What am I listening to", "Build a playlist", "Play something". |

No `archive` and no `inbox`: the extension does not write listening data into the knowledge base or the Inbox.

## Configuration

The settings form is the extension's, so every service's fields live in it (the form subset has no arrays of objects,
so a service gets fixed fields rather than a dynamic list). The convention for a new source: `<source>` boolean or
`<source>_account` string, `x-confirm` on the field that grants access, and credentials never in the form.

- `macs`: a list of computers (at most one) whose Music library is read. The manifest's `node.machines` points to this field, so the settings page draws one switch per Mac. The node program runs on the selected Mac.
- `iphone`: whether the iPhone endpoint is on. It needs the Persona app's `phone_music_snapshot` tool; the assistant uses it.

Apple Music declares no secrets and no network permission: the extension program talks only to the hub (the Mac
through `nodes.call`), and the iPhone through the assistant. A cloud source adds its `permissions.network` hosts and
a `<source>_token` secret (or a connector `login` step) in its own change; see ADDING_A_SOURCE.md.

## The Mac program

The manifest's `node` section runs the same binary with `node` on the selected Mac. The hub program calls `nodes.call {machine, method, params}`; the node program answers with Music.app AppleScript:

- `library playlist`, `user playlist`, `track` (`played count`, `played date`, `rating`, `disliked`, `lyrics`), `current track`, `player state`, `player position`, `play`, `pause`, `playpause`, `next track`, `previous track`, `make new user playlist`, `duplicate`, `artwork`.
- The local library only, including cloud tracks when the Mac syncs its library. It cannot search the catalog.
- It reads Music.app's data; it does not read `Library.musicdb`.
- The first AppleScript call makes macOS ask for Automation permission. The manifest's `node.permissions.macos_privacy` shows the warning; the grant is on that Mac in System Settings › Privacy & Security › Automation.
- Artwork is exported to a temporary file, shrunk with `/usr/bin/sips` and returned base64; the `node.permissions.files` covers the temporary file. The hub's `ui.image` caches the picture per ref and width.
- Arguments reach the script as `argv`; user text is never pasted into AppleScript source.
- The node caches recent/playlists for two and five minutes and stats for ten; the page's refresh calls `music.refresh` to clear it.

## The iPhone snapshot

The hub cannot call a phone tool: phone tools belong to the assistant, and extensions have no call relay to them. The bridge is an explicit snapshot:

1. The assistant calls the Persona app's `phone_music_snapshot` on the iPhone. The app uses MusicKit to read now playing, recently played and library playlists and returns one JSON object.
2. The assistant passes that object unchanged to `applemusic_iphone_sync`.
3. The extension validates and stores it in its data directory; the Music page draws the last snapshot and its sync time.

The snapshot is deliberately a snapshot: it is not live, and the page says when it was taken. Playback on the iPhone still uses `phone_media_play` / `phone_media_control`; the extension cannot start it.

## The page

The switch atop the page chooses **sources**, not devices: **All sources** and one source per connected service (Apple Music today). All sources is always there, so adding a second service does not change where the user looks. The switch is the in-page segmented control while every label fits (at most 5 choices of 12 characters, the view schema's limit); with more or longer services the page uses its `tabs` (2–8, 40 characters) and then a `filter`, so a long service name can never fail the page check. Inside a source the configured endpoints are merged; with two endpoints every item carries its device (a Mac/iPhone badge on now playing, the endpoint after the artist on a tile or playlist), and the library stats are one section per device. With more than one service the badge and the row labels name the service too. The hub cannot read the account on either device, so the page never merges two endpoints as though they were one account: if the Mac and iPhone are signed in differently, their music stays visibly attributed rather than silently mixed.

The page shows:

- Now playing, with artwork, state, position and duration; the row's menu has play/pause, previous and next on the Mac, and tapping it pushes the player page. There is no progress bar, and the row shows the track length rather than the position: the hub redraws a page no faster than every 30 seconds, so a live reading would only jump.
- The player page: large artwork, title, artist, position, the Mac's controls, and the lyrics; the iPhone page says the snapshot has no lyrics or controls.
- Recently played, as a carousel with artwork, interleaved across the endpoints and deduplicated within one source.
- Playlists, with artwork, round-robin across the endpoints (so a later service is never hidden behind the first
  endpoint's list); a row opens the playlist's tracks, 50 per page with covers, and Previous tracks / Next tracks
  page through the rest. Each window stays under the page's 50-picture limit.
- Library stats (per endpoint when there are two).
- A search box when an endpoint can search; the search runs on the selected source.
- Refresh; `refresh_after` is 60 seconds.

There is no "source status" table. A failed endpoint contributes an error line and its guide (in the request's
language) to the top of the page; a page with nothing configured is a connect card naming the service, and a page
whose connectors are all switched off says so.

The page schema has no bottom mini-player, so an extension cannot pin a floating bar; the player page is the closest the extension side can get. A native bottom bar would be a new app component.

Artwork: the iPhone snapshot carries https URLs (only from Apple's artwork CDN are used); the Mac has no URL through AppleScript, so the page draws a `ui.image` ref (`applemusic:mac:track:…` or `applemusic:mac:playlist:…` — source, endpoint, kind, id) and the extension serves the bytes through the hub. The hub writes the app-facing path and caches it.

Lyrics: the Mac program reads `lyrics of current track`; the hub caches the words by track for ten minutes. The iPhone snapshot has none (MusicKit exposes no public lyrics), so its player page says so.

## Assistant tools

Each connector prefixes its endpoints' tools with its own id; the Apple Music connector's are:

Mac:

- `applemusic_mac_search {query, limit?}`.
- `applemusic_mac_recent`, `applemusic_mac_playlists`, `applemusic_mac_playlist {id}`, `applemusic_mac_now`, `applemusic_mac_stats`.
- `applemusic_mac_control {action, id?}` (play, pause, playpause, next, previous, stop, play_track, play_playlist).
- `applemusic_mac_create_playlist {name, description?, track_ids, play?}` (gated): with `play: true` the new playlist starts on that Mac.

iPhone:

- `applemusic_iphone_recent`, `applemusic_iphone_playlists`, `applemusic_iphone_now`, `applemusic_iphone_stats`.
- `applemusic_iphone_sync {…}` stores the object from `phone_music_snapshot`.

On the phone the assistant also has the phone's music tools (Persona's, not the connector's):

- `phone_media_play {query, kind?}` searches the Apple Music catalog and plays a song, album or playlist in the phone's Music app.
- `phone_music_create_playlist {name, songs, description?, play?}` searches the catalog for each song, creates the playlist in the user's library and plays it; creating it is a confirmed write on the phone.

The skill tells the assistant:

- Which endpoint a request names, and what to do when it names none.
- That a phone snapshot must be synced explicitly, and that the page is not live.
- That a write is not done until the gated action returns.
- That an existing playlist is found before a new one is created.
- That a link or an opened search is not playback.

## Data model

- `item`: provider (endpoint), id, kind, title, artist, album, description, artwork, product URL, duration, ISRC.
- `playlist`: an item with a track count and owner.
- `nowPlaying`: id, title, artist, album, state, position, duration, artwork, URL.
- `libraryStats`: tracks, albums, artists, playlists, duration.
- `searchRequest` and `createPlaylistRequest` for the tools.

Cross-service matching is not in this version.

## Caching and limits

- The Mac provider caches status (30 s), now playing (15 s), recent and playlists (2 min), a playlist (5 min), search (1 min), lyrics (10 min) and stats (5 min). The node program has its own cache behind it. The page's refresh clears both.
- The iPhone snapshot is stored and read from memory; it changes only when the assistant syncs. The hub stamps the sync time itself and drops snapshot rows without a title, so a bad payload cannot break every later render.
- A page render has a 17-second budget; each node call has 12; a tool call has 25.
- No network from the extension program; the Mac has no network permission; the iPhone's pictures are https URLs loaded by the client.

## Security and privacy

- Library contents and snapshots stay on the hub and the chosen devices. No `archive` and no `inbox` contribution.
- The Mac program runs only on computers the user turned on for this extension, with Automation permission; it has no network permission and no secrets. It writes only its own temporary artwork files.
- The phone snapshot is written atomically with 0600 permissions, capped at 4 MiB, and contains only titles, artists, albums, durations, artwork URLs and product links. The hub stamps its own sync time and drops empty-titled rows.
- The assistant can write the snapshot, so its artwork URLs are loaded by the client only from Apple's artwork CDN; any other host is dropped. Opened links are limited to `music.apple.com`.
- Every write is a connector action behind the gate; the card names the destination and the track count; `confirm_always` keeps even an `allow` rule on the card.
- A repeated `request_id` returns the first result; finished receipts are pruned, pending ones are kept. A definite failure (Automation denied, Music not running, invalid arguments, not signed in) clears its pending receipt; only a timeout or lost answer stays pending as an unknown outcome. A write that happened but whose follow-up failed (a created playlist that would not start) keeps a finished receipt, so a retry cannot create it twice.
- Page keys are unique, tile titles and subtitles are clipped to the view schema, and only https links and artwork URLs are used.
- An endpoint id is unique within its source and is used with the source id in page state, action values and `ui.image` refs, so two services can both have a `web` endpoint.

## Risks and open questions

1. **The Mac has no catalog search.** It searches the user's library only; say so.
2. **The iPhone snapshot needs the app tool.** Without `phone_music_snapshot`, the iPhone section stays empty; the assistant should say so.
3. **Mac Automation cannot be granted silently.** The user must allow it on that Mac; a headless or unapproved Mac shows a permission error.
4. **AppleScript library scans can be slow.** Reads are bounded and cached; the page's budget fails a slow endpoint visibly instead of hanging the render.
5. **Artwork via sips is best effort.** A missing or unreadable artwork shows a placeholder; the page still draws.
6. **Lyrics are the library's, not the service's.** Apple Music's synced lyrics are not exposed through AppleScript; a cloud-only track may show no words, and timed lyrics are not available.
7. **The accounts cannot be compared.** Neither device exposes its Apple ID to the extension; the page labels items by endpoint rather than pretending they are one library.
8. **No floating mini-player.** The page schema has no bottom bar; a native one is an app component, not an extension one.
9. **Provider terms.** Apple Music attribution and brand rules apply; no audio may be re-streamed.
10. **Sign-in for a new cloud service is limited to fields, device code and QR.** The platform has no redirect/callback OAuth step yet, so Spotify's PKCE flow needs a per-user app, a manual code paste or a platform change (ADDING_A_SOURCE.md).

## Roadmap

| Step | Contents |
| --- | --- |
| v0.3 | Separate Mac and iPhone connectors. |
| v0.4 | One Apple Music connector with Mac/iPhone endpoints, one page with configured tabs, Mac artwork through `ui.image`, cached reads, a connect page when empty. |
| v0.5 | Source tabs (All sources, Apple Music) with merged, per-device-labeled endpoints; a now-playing card with progress; a player page with lyrics; the 50-picture playlist fix. |
| v0.6 | The source switch inside the page; playback progress removed; a music note nav icon; playlist pages of 50 tracks with covers and Previous / Next tracks. |
| v0.7 | Play a playlist on the Mac and create one that starts playing; on the iPhone, create a playlist from catalog searches and play it, and play a catalog song directly. |
| v0.8 (current) | A source registry: one connector per service, per-contribution status/tools/actions, a source switch that falls back from segmented to tabs to filter, per-source skills, service-neutral page and tool text, localized provider guides, source-aware write receipts, `engines.persona`, translations, and a fake second source in the tests. |
| v1 | The first non-Apple source (see ADDING_A_SOURCE.md; its sign-in flow first); more than one Mac. |
| v2 | Cross-service transfer with a match report; listening memory and stats. |

## Development

```sh
go -C extensions/music test -race -count=1 -timeout=120s ./...
python3 -I scripts/manifest.py check
scripts/package.sh music
scripts/ci.sh
```

The extension is its own Go module (`github.com/nwverse/persona-extensions/extensions/music`) with the SDK replace directive. Tests use a fake Mac node, a fake iPhone snapshot and a fake second service (`source_test.go`); they need no account, key or Mac. The `macscript_darwin_test.go` compiles every AppleScript against Music's dictionary on macOS without sending Apple events; `skills_test.go` keeps each skill's tool names in step with what the connector lists. A version bump selects the release; `persona-extensions.json` is generated and is never edited by hand. A new service follows [ADDING_A_SOURCE.md](ADDING_A_SOURCE.md).
