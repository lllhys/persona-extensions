# RSSHub

Subscribe to [RSSHub](https://docs.rsshub.app) routes or any RSS, Atom or JSON feed in Persona: a navigation page with subscriptions by tag, unread entries, their text and route previews; a background refresh with conditional requests; and assistant tools, whose subscription changes are confirmed on the phone.

- [Extension guide and configuration](../../docs/extensions/rsshub.md)
- [Installation](../../docs/installation.md)
- [Development and tests](../../docs/development.md)
- [Manifest](persona-extension.json)

```sh
go -C extensions/rsshub test -race -count=1 ./...
scripts/package.sh rsshub
```

`TestLiveInstance` reads a real instance when `RSSHUB_LIVE_URL` is set (with `RSSHUB_LIVE_KEY` and `RSSHUB_LIVE_ROUTE` when needed); it is skipped otherwise, so the suite needs no network.
