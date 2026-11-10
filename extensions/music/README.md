# Music

A generic music extension for Persona: one page and assistant tools over the music services you connect. Today it
ships **Apple Music**; Spotify, NetEase Cloud Music and QQ Music are the planned next sources (see [DESIGN](DESIGN.md)).

## How it is built

- **One connector per service** (`contributes.connectors`): `applemusic` today. A service has one or more endpoints —
  for Apple Music, a Mac running Music.app through a program on that Mac, and an iPhone snapshot read by the Persona
  app.
- **The hub program keeps a source registry** (`app.go`): each source's endpoints implement the optional capability
  interfaces in `provider.go` (search, recent, playlists, playlist detail, now playing, playback control, stats,
  lyrics, artwork, playlist creation, snapshot sync), so a service implements only what its API can do.
- **Tools are named `<connector>_<endpoint>_<tool>`** (`applemusic_mac_search`, `applemusic_iphone_sync`); each
  service ships its own skill and the shared `music` skill holds the rules common to all of them.
- **The page's source switch** is All sources plus one tab per connected service. It uses the in-page segmented
  control while every label fits (at most 5 choices of 12 characters) and falls back to the page's tabs, then a
  filter, so a new service can never break the page.

## Adding a source

See [ADDING_A_SOURCE.md](ADDING_A_SOURCE.md): the manifest entry and credentials, a provider file, a source builder,
the skill, tests with a fake provider, and the docs to update.

## Develop

```sh
go -C extensions/music test -race -count=1 -timeout=120s ./...
python3 -I scripts/manifest.py check
scripts/package.sh music
```

Install the package on a hub's machine: `personad extension install file:dist/music-<version>.tar.gz`.

The tests use a fake Mac node, a fake phone snapshot and a fake second service; they need no account, key or Mac.
`macscript_darwin_test.go` compiles every AppleScript against Music's dictionary on macOS.

- [User guide](../../docs/extensions/music.md)
- [Design](DESIGN.md)
- [Adding a source](ADDING_A_SOURCE.md)
- [Manifest](persona-extension.json)
- [Installation](../../docs/installation.md)
