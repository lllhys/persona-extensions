---
name: applemusic
description: Use Apple Music in Persona: see what is playing, read playlists and recently played tracks, search, play songs, create playlists and play them on the iPhone or the Mac, and read an iPhone snapshot. Use when the user asks for Apple Music, Music.app or the iPhone's music.
---

# Apple Music

Apple Music is one source of the Music extension; the shared `music` skill has the rules that apply to every source. This skill covers the Apple Music connector and the phone's own Apple Music tools.

- `applemusic_mac_*` — Music on the selected Mac: library search, playlists, recent plays, now playing, library size, playback control, playlist playback.
- `applemusic_iphone_*` — a snapshot of the Music app on the iPhone: recently played, playlists, now playing and a sync tool.

Only the endpoints configured in **Settings › Extensions › Music** have tools. If an endpoint is missing, say it is not connected; do not pretend to read it.

## Reading and playing

- `applemusic_mac_search {query, limit?}` searches the Mac's library (songs). `applemusic_mac_recent`, `applemusic_mac_playlists` and `applemusic_mac_playlist {id}` read the library. `applemusic_mac_now` reads what the Mac is playing. `applemusic_mac_stats` reads its size.
- `applemusic_mac_control {action, id?}` controls the Mac: `play`, `pause`, `playpause`, `next`, `previous`, `stop`, `play_track` with a track ID from a search, or `play_playlist` with a playlist ID from `applemusic_mac_playlists`.
- `applemusic_iphone_recent`, `applemusic_iphone_playlists`, `applemusic_iphone_now` and `applemusic_iphone_stats` read the last iPhone snapshot. It is not live.
- To refresh the iPhone snapshot: call the iPhone's `phone_music_snapshot` tool, then pass its JSON object unchanged to `applemusic_iphone_sync`. The Music page then shows it.
- iPhone playback uses the phone tools: `phone_media_play {query, kind?}` searches the Apple Music catalog and plays a song (default), album or playlist in the phone's Music app; `phone_media_control {action}` does `play`, `pause`, `next`, `previous` or `status`.
- `phone_music_create_playlist {name, songs, description?, play?}` makes a playlist on the phone: one catalog search per entry in `songs` (title and artist), the best match of each is added, the playlist is created in the user's library and, unless `play` is false, played on the phone. It asks the user on the phone before creating. Report the songs added and any searches that found nothing.
- Say what actually happened. A tool that returns a link or an opened search did not start playback. Never claim music started when no player tool ran.

## Recommendation flows

- **Recommend and play on the iPhone**: `phone_media_play` for one song; `phone_music_create_playlist` with the recommendations as `songs` to create the playlist and play it in one confirmed step.
- **Recommend and play on the Mac**: the Mac can play its library only, not the Apple Music catalog. Search the library for each recommendation with `applemusic_mac_search`; if at least one is there, create a playlist from those track IDs with `applemusic_mac_create_playlist {name, track_ids, play: true}`, which starts it after the confirmation card. Tell the user which songs were not in the library; those can be played on the iPhone with `phone_media_play`.
- Do not offer single catalog-song playback on the Mac: it cannot play a song that is not in its library.

## Writing

- Build a playlist only after the user agreed on the idea. Show the proposed songs with title and artist in the conversation first.
- `applemusic_mac_create_playlist {name, description?, track_ids, play?}` creates a playlist in the Mac's Music app from track IDs returned by `applemusic_mac_search`; with `play: true` it also starts playing it there. It is behind a confirmation card; it exists only after the tool returns.
- If it fails, say what failed; do not retry with a made-up ID. Do not create a second playlist when one with the same name already exists; read the playlists first (`applemusic_mac_playlists` or `phone_music_snapshot` / `applemusic_iphone_playlists`).

## Limits

- Recently played is a short list, not a complete listening history.
- The Mac endpoint reads and plays only that Mac's library; it cannot search the Apple Music catalog. A catalog-only song has to be played on the iPhone.
- The iPhone page is a snapshot: it updates when the assistant syncs it.
- A Mac can be offline or missing Automation permission. Report what the tool says; do not invent data.
