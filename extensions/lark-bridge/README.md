# Lark Bridge

Read Lark context in Persona through the host-managed `lark-cli` integration. Reuse the existing sign-in; this extension never starts a second QR login.

- Recent chats and bounded message/topic history.
- Search messages, chats, people and documents with the existing user authorization.
- A native TypeScript/JSX search page and read-only assistant tools.
- English, Simplified Chinese and Japanese UI text.

## Security and scope

The extension calls only the host's typed `cli.call` operations. The hub manager owns the CLI executable, profile, credentials, version check, timeouts and output bounds. The extension cannot supply a shell command, choose an executable/profile, read CLI credentials or invoke a write operation.

There is no unofficial web protocol, Python runtime, browser-cookie access, message sending, read-receipt update, automatic `/agent` execution or message archiving in this package. Document links open the original service; no document content is executed.

The existing host CLI integration must be explicitly enabled by the hub owner. Missing configuration, sign-in and read scopes are reported instead of bypassed.

See [setup and tool contracts](../../docs/extensions/lark-bridge.md).

## Development

```sh
npm ci
npm test
npm run build:source
```

The build checks the official source bundle and writes a deterministic development archive. Hosted rebuild inputs are in `source-rebuild.json` and `source-reference/`. This does not sign a catalog or activate the extension in production.
