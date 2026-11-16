# Podcasts

Discover public podcasts in Persona through Apple's podcast directory. No account is required.

- Search by show, publisher or topic.
- See publisher, category, episode count and recent episode metadata.
- Open canonical Apple Podcasts pages or the publisher's HTTPS RSS feed.
- Save up to 50 shows in a listening list owned by this Persona installation.
- Ask the assistant to find shows or recent episodes with `podcasts_search` and `podcasts_episodes`.

The listening list uses Persona's declared installation-owned `subscriptions` storage. Mutations use host-issued request IDs and compare-and-swap revisions. It does not subscribe in Apple Podcasts, call another extension's private API, fetch podcast feeds or download audio. Persona can open a show or episode link for the user's preferred next step.

Apple supplies the directory metadata. Publisher feeds and episode content belong to their respective owners. This source package is an original implementation for Persona; it contains no Raycast source or UI.

## Development

```sh
npm ci
npm test
npm run build:source
```

The build checks the official source bundle and writes a deterministic development archive. It does not sign a catalog or activate the extension in production.
