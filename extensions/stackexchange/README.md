# Stack Exchange

Search Stack Overflow and selected developer communities without leaving Persona. Read a question's accepted answer, or its highest-voted answer when none is accepted. Code blocks, HTTPS references, attribution, content license and canonical links remain visible.

## Usage

1. Enter an error message, API name or question.
2. Optionally choose a site or enter a tag such as `typescript`.
3. Open a result to read its answer. Unanswered questions link to the original discussion.

The UI and assistant tools are localized in English, Simplified Chinese and Japanese.

### Assistant tools

- `stackexchange_search`: search by query, site, optional tag and result limit.
- `stackexchange_answer`: retrieve the accepted or highest-voted answer for a question ID.

The supported sites are Stack Overflow, Super User, Server Fault, Unix & Linux, Ask Ubuntu, Ask Different, Software Engineering, Code Review, Web Applications, Database Administrators, DevOps and Information Security. This explicit allowlist prevents arbitrary site parameters.

## Network and privacy

The hub makes anonymous, read-only `GET` requests to `https://api.stackexchange.com` using the [Stack Exchange API v2.3](https://api.stackexchange.com/docs). No account, application key, access token, browser cookie or web scraping is used. Search text and the optional tag are sent to Stack Exchange. No write or voting endpoint exists in this extension.

The [API overview](https://api.stackexchange.com/docs) asks clients to avoid repeating identical requests within a minute. The service caches identical requests for at least that long, serializes requests, observes the server's `backoff` field, limits response sizes and supports cancellation. Transient failures may display a clearly marked previously validated cached answer. No cached content is treated as authoritative or current indefinitely.

The implementation follows the [API throttling rules](https://api.stackexchange.com/docs/throttle) and the [published API terms](https://stackexchange.com/legal/api-terms-of-use). API endpoints and sample responses were checked before implementation. The extension does not claim to be endorsed by Stack Exchange.

## Content and safety

Questions and answers are community contributions, not reviewed instructions. Check an answer before executing commands or applying security-related advice. Preserve author attribution and the displayed CC BY-SA license when reusing content. Third-party HTML is converted to the SDK's bounded Markdown subset and is never injected into a client. Links are validated against the selected site's canonical HTTPS host.

## Development

```sh
npm ci --registry=https://registry.npmjs.org
npm test
npm run build:source
```

The deterministic source archive includes the bundle, source, tests, lockfile and official SDK authoring closure. Native TypeScript/JSX runs on the hub; clients do not execute extension JavaScript. Local packaging does not sign a market catalog, publish an OCI package or activate production isolation.
