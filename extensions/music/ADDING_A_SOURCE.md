# Adding a music source

The extension contributes **one connector per service**. Adding Spotify, NetEase Cloud Music, QQ Music or anything
else is a manifest entry, a provider file, a builder, a skill, tests and docs. The platform contract lives in the
Persona repository: `docs/extensions/connectors.md`, `docs/protocol/connector-protocol.md` and
`docs/extensions/extensions.md` ("Sign-in (auth)").

## 1. Manifest

In `persona-extension.json`:

- Add the connector: `{"id":"spotify","name":"Spotify","capabilities":["status","tools","actions","login"],`
  `"hosts":["open.spotify.com"],"confirm_always":["spotify_*_create_playlist"]}`. The id is the tool prefix, so it
  must be unique across the hub.
- Configuration: the form subset has no arrays of objects, so give the service its own fixed field (for example a
  `spotify` boolean or a `spotify_account` string) and put `x-confirm` on the field that grants access. Credentials
  are never configuration: use the connector `login` capability or `secrets` + `connector.secrets.*`.
- `permissions.network`: the API hosts the program calls. `secrets`: `<source>_token` when the service needs a stored
  token.
- Add `{"id":"spotify","dir":"skills/spotify"}` to `contributes.skills`.
- Translate the new fields in `l10n/zh-Hans.json` and `l10n/ja.json` (title, description, confirm).
- Raise `engines.persona` when you use a manifest field older hubs do not know.

## 2. Provider

Create `spotify.go` with a type that implements `provider` (`ID`, `Name`, `Status(ctx, locale)`, `OpenHosts`) and the
optional interfaces its API has:

| Interface | Use it when the service has |
| --- | --- |
| `searchProvider` (+ `kindSearchProvider`) | catalog or library search |
| `recentProvider`, `playlistsProvider`, `playlistProvider` | history and playlists (the playlist one returns a window: offset, limit) |
| `nowPlayingProvider`, `playbackController` (+ `controlActionsProvider`) | a player the tool may drive |
| `playlistCreator` (+ `createPlaylistHintProvider`) | creating playlists (a write; the card is automatic) |
| `statsProvider`, `artworkProvider` / `artworkHostsProvider`, `lyricsProvider` | counts, pictures, lyrics |
| `playbackNoteProvider` | a page that cannot control playback but can say where it happens |
| `snapshotSyncer` | an endpoint another device exports (the iPhone does) |
| `refresher` | a refresh that clears caches |

Rules:

- Endpoint ids match `^[a-z][a-z0-9]{0,31}$` and are unique **within the source**. Page state and actions carry the
  source id too, so two services may both have a `web` endpoint.
- `Status` answers in the request's locale (`textsFor(locale)`); its `Guide` is what the page shows when the endpoint
  is not set up. Do not hard-code English in a status.
- Return `*ext.Error` with the connector protocol's codes (`not_signed_in`, `rate_limited`, `invalid`, `not_found`,
  `denied`, `unsupported`, timeout). The write receipts treat those as "nothing was written"; any other error keeps
  the receipt pending, so a retry cannot write twice.
- Put service-specific words here, not in `tools.go`, `actions.go` or `page.go`: those builders are shared.

## 3. Register the source

In `app.go`, extend the source builders (today `addAppleMusicSource`): read the source's configuration, create its
providers, `src.add(provider)` each, then `a.addSource(src)`. `init` already skips a connector the user switched off
and keeps the others running.

## 4. The page and the tools are generic

`connectorTools`, `connectorActions`, `splitTool` and the page route by contribution and by source; a new source gets
its tools (`spotify_web_search`), its status, its page tab and its card/player text without editing them. The page's
source switch falls back from the in-page segmented control to tabs and then a filter as services grow.

## 5. Skill and docs

- `skills/<source>/SKILL.md`: the service's tools, capabilities and limits; add it to `contributes.skills`.
- Add the source (name only) to the shared `skills/music/SKILL.md` list, and a setup section to
  `docs/extensions/music.md`.
- `skills_test.go` checks that every `<source>_*` name a skill mentions exists in the connector's tool list.

## 6. Tests

- A fake provider like `source_test.go`'s `fakeCloud`, covering the capabilities you implemented.
- Assert the manifest's connector entry, the tool list per contribution, the status per contribution, the page tab,
  and that switching one connector off leaves the others alone.
- Run `go -C extensions/music test -race -count=1 -timeout=120s ./...`, `python3 -I scripts/manifest.py check` and
  `scripts/package.sh music`.

## Authentication today

The extension platform supports fill-in fields, `device_code` and `qr` login steps, which every client renders.
**There is no redirect/callback OAuth step yet.** Spotify's Authorization Code + PKCE flow therefore needs one of:
each user's own Spotify app with a redirect to their hub, a manual code paste through a `fields` step, or a platform
feature (track it in the Persona repository). NetEase Cloud Music and QQ Music can use `qr` today.

Credentials belong in `connector_secrets` (the program calls `secrets.*`), never in configuration, logs or the
repository.
