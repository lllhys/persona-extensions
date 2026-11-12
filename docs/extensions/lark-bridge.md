# Lark Bridge

[Documentation](../README.md) · [Source](../../extensions/lark-bridge/)

Lark Bridge reads Lark chats, messages, contacts and document search results through the host-managed `lark-cli`. It is a native TypeScript/JSX connector for the existing CLI sign-in, not a second messaging login.

## Setup

Use a hub that supports bounded host CLI integrations and the Deno source runtime. The manifest requires `lark-cli >=1.0.72 <2.0.0`; the manager checks the installed version.

1. Configure the public [lark-cli](https://github.com/larksuite/cli) on the hub machine and sign in through the host's existing Lark settings.
2. Enable the host's `lark.cli.extensions` integration setting. The manager selects the configured executable and effective profile; the extension cannot override either.
3. Install Lark Bridge from the [public market](../installation.md), or build a local package with `scripts/package.sh lark-bridge`.
4. Grant the existing Lark app the read scopes needed for the requested messages, chats, contacts or documents. The extension reports missing authorization; it does not request broader access silently.

No token, app secret, executable path, profile name, proxy or tenant address belongs in this extension's settings. Do not put these values in repository files or issue reports.

## Reads

| Tool | Behavior |
| --- | --- |
| `larkbridge_chats` | Recent chats through the bounded `chats` operation. |
| `larkbridge_messages` | At most 50 messages from a supplied chat ID or topic ID. An optional opaque `before` token is forwarded for a subsequent page. |
| `larkbridge_search` | A required query with kind `messages`, `chats` or `people`. |
| `larkbridge_docs` | A required document search query; results retain only safe HTTPS links on the public Lark/Feishu domains. |

The page offers the same search and bounded reads. Message rich content is projected to plain text. IDs are shown only when needed to select a conversation or topic.

## Boundaries

Only `auth_status`, `chats`, `messages` and `search` cross `cli.call`. Inputs are restricted to the documented operation, kind, query, chat, thread, page token and limit. Responses must identify `lark-cli` and match the expected shape. A host error is reduced to a stable public category without exposing raw CLI output, paths or credentials.

This package has no unofficial web protocol, Python dependency, browser-cookie access, send/reply tool, read-receipt update, automatic `/agent` reply, background event collection or knowledge-base archiving. It does not duplicate Persona's built-in Lark connector or the host CLI's own settings UI.

## Verification

```sh
npm --prefix extensions/lark-bridge test
scripts/package.sh lark-bridge
scripts/ci.sh
```

Tests use synthetic public-shaped fixtures. They do not require or replace an existing Lark sign-in, and never send messages. The native source build also checks the official SDK authoring contract and emits a deterministic development source archive.
