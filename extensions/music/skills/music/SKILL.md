---
name: music
description: Use the music services connected to Persona: find which services and devices are connected, see what is playing, search, play, create playlists, and sync a phone snapshot. Use when the user asks about music, playlists or listening.
---

# Music

Persona's Music extension aggregates one or more music services. Each service is its own connector with its own tools,
named `<service>_<endpoint>_<tool>`:

- `<service>_<endpoint>_<tool>`, for example a service's `<service>_web_search` or a Mac's `<service>_mac_control`.
  The Apple Music skill lists that service's concrete tools.

The services connected right now are the ones whose tools are in your tool list. Read that list instead of assuming a
service; if the one the user names is missing, say it is not connected and point to Settings › Connectors.

## Rules that apply to every service

- Say what actually happened. A link or an opened search is not playback; only a player tool starts music.
- A write is not done until the gated action returns; its confirmation card names the destination and what will change.
- Recently played is a short list, not a complete listening history.
- A snapshot endpoint is not live: it shows what was last synced, and the page says when.
- When more than one service is connected, name the service and the endpoint in your answer ("on Spotify", "on the
  Mac"). Never merge two services as though they were one account.
- Prefer the service the user names. Without a preference, ask when the choice changes what happens.
- A song missing from one service may be on another: search each connected service before saying it is unavailable.

## Per-service skills

- `applemusic` — Apple Music on a Mac, the iPhone snapshot and the phone's Apple Music tools.

A new service ships its own skill next to this one and is listed here (see ADDING_A_SOURCE.md). Keep this file free of
one service's tool names.
