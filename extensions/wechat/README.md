# WeChat

[Extension development](../../docs/development.md) · [Installation](../../docs/installation.md)

A personal WeChat bot using Tencent's public iLink protocol. It contributes both connector and channel id `wechat`. It runs independently of `personad` through the official Go process-extension SDK; no hub runtime or TypeScript source-profile change is required for its long-polling channel.

## Install and bind

Install **WeChat** from Persona's public extension marketplace, then open **Settings → Connectors → WeChat** and scan the QR code in WeChat. Confirm on the phone; if WeChat asks for a number, enter that number in Persona. QR expiry, scanned confirmation, redirects and verification-code steps retain the former connector's behavior.

Only the person who bound the bot can send messages into Persona. Other senders receive a rate-limited explanation and never enter the hub conversation. Text, pictures and WeChat's voice transcript come in. Plain-text replies are chunked at 3,500 characters; the channel supports questions, push messages and typing indicators. Files and videos are described but not transferred.

Conversation routing, `/new` and turn execution belong to the hub, not this extension. See [Persona's channel documentation](https://github.com/nwverse/persona/blob/main/docs/extensions/channels.md).

## Upgrading from the built-in

Do not sign out, delete connector data, or copy credentials to another polling process. The connector id remains `wechat`, and the extension loads the same hub-encrypted `connector.secrets` blob:

- `creds.token`, `creds.bot_id`, `creds.user_id`, `creds.base_url`;
- `old`, `cursor`, `context` (the last owner's iLink context token), `seen`.

The existing token is reused without a QR login. Polling resumes from the retained cursor; replies use the retained owner/context token. State never becomes a plaintext credential file in the extension directory.

A hub with the built-in must support registry handoff before installing this same-id extension. The old process must fully stop before the new process starts: two pollers with one token can steal messages. Keep the built-in available until a verified migration and the operator's final removal approval. During this window, uninstalling the extension restores the built-in through the registry's handoff mechanism; do not use logout as rollback.

## Privacy and network

The only network grant is `*.weixin.qq.com`, matching the previous iLink/media-CDN allowlist. API and redirect origins are restricted to HTTPS Tencent hosts. Tokens and message contents are not logged. Polling, outbound retries, message deduplication and stable send refs retain the built-in implementation.

The implementation was moved from Persona's MIT-licensed Go plugin. Its iLink wire behavior follows Tencent's MIT-licensed `openclaw-weixin` v2.4.6; the Go client does not embed Tencent's plugin runtime. [License notices](assistant/THIRD_PARTY_NOTICES.md) ship in the package.

## Development

```sh
go -C extensions/wechat test -race ./...
scripts/package.sh wechat
```

The tests cover QR binding and verification steps, owner-only forwarding, media, typing, revoked tokens, reply retries and duplicate refs, official SDK connector/channel conformance, manifest/runtime consistency, all three locale catalogs, and loading a literal synthetic legacy credential blob without requesting a QR.

Local tests are not production acceptance: inspect the installed hub's QR/status UI and verify single-poller ownership, credential reuse and one test-conversation round trip separately. Never test against the user's primary conversation.
