# Feishu through LarkAgentX (larkx)

[Documentation](../README.md) · [Source](../../extensions/larkx/)

This extension (id `larkx`) reaches Feishu as the user, next to the hub's own `lark-cli` integration (nwverse/persona#1487). It runs [LarkAgentX](https://github.com/cv-cat/LarkAgentX), a Python library for the Feishu web client, as a connector program: the user signs in with a QR code, every message arrives over the web client's long connection, the assistant can send as the user after a confirmation on the phone, and `/agent` in any chat hands a request to the assistant. It is an ordinary connector and channel program (Persona's `docs/connector-protocol.md` and `docs/extensions/channels.md`), so the hub needs no change.

This page describes version 0.2.0. When the manifest shown in the install preview disagrees with this page, the manifest wins.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `connectors` | `larkx` (飞书（LarkAgentX）) | Capabilities `status`, `tools`, `actions`, `events`, `inbox`, `archive`, `login`, `threads`, `fallback`. Inbox kinds `dm`, `mention`, `group`. `larkx_*` actions are `confirm_always`. |
| `assistant` | `larkx` | Instructions in `assistant/instructions.md`: when to use `larkx_*` and when `lark-cli`. |
| `channel` | `larkx` (飞书 /agent) | Kind `im`, on the `larkx` connector: the user's own `/agent` messages reach the assistant's chat, and the answers go back under them. Plain text, at most 4,000 characters. |

## Before you use it

- **Unofficial protocol.** LarkAgentX reverse-engineers the Feishu web client's undocumented protocol. It can stop working whenever Feishu changes it, it may not fit Feishu's terms of use, and the account may be flagged by Feishu's risk controls. Whether to use it is the account owner's decision; for a company account, check the company's rules first.
- **The sign-in is a full web session.** Whoever holds it can read and write Feishu as the user. It is stored through `connector.secrets.*`, encrypted with the hub's key, and never written to a file or the log. To revoke it, sign out in the connector settings or remove the device under Feishu's signed-in devices. Signing out cancels startup restoration and its retries; an in-flight backend request finishes before sign-out returns, and cannot reactivate the old account. Starting a new QR sign-in also cancels restoration of the saved session.
- **It runs as the hub's system user.** LarkAgentX's code can read and write the files that user can; where the hub enforces it, its network is limited to the manifest's `permissions.network`. LarkAgentX is pinned to one commit and checked file by file, so an upstream change reaches the hub only through a new release of this extension.
- **Messages are stored in plain text** in the extension's data directory (`larkx.db`, mode 0600) and deleted when the extension is uninstalled. Messages older than 30 days are deleted once the knowledge base has archived them.

## LarkAgentX and its license

LarkAgentX has no license. Neither this repository nor the extension's package carries its code: the package holds only this extension's own program. On first start the program downloads LarkAgentX from GitHub (`codeload.github.com`) at the commit pinned in `src/larkx_persona/upstream.json`, keeps only the files listed there, and checks each one's SHA-256; a missing or different file fails the start and nothing is kept. The copy lives in the extension's data directory (`larkagentx-<commit>/`); a new pinned commit replaces it.

Its dependencies are installed the same way, with `pip install --require-hashes` from PyPI into the data directory (`deps-py3X-<hash>/`). `requirements.txt` pins every package, the transitive ones too, with the SHA-256 of each of its files on PyPI. Directories left by earlier Python versions or requirement files are removed.

## Two ways to reach Feishu

| | `lark-cli` (hub integration) | LarkAgentX (`larkx`) |
| --- | --- | --- |
| Interface | The official open platform | The reverse-engineered web protocol |
| Identity | An app, with the user's authorization | The user |
| Setup | Install and authorize `lark-cli` on the hub's machine | Install the extension, scan |
| Every message, live | No (IM events go to bots only) | Yes (long connection) |
| Sends as | A bot | The user, after a phone confirmation |
| Inbox and knowledge base | No | Yes |
| `/agent` in any chat | No | Yes |
| Docs, calendar, minutes | Yes | No (messages only) |

They can be installed together and know nothing of each other. `larkx` does not take over the hub's `lark` connector.

## Install and sign in

The machine the hub runs on needs Python 3.9 or newer with pip. Install the extension from this market (see [installation](../installation.md)), then:

1. On first start the program downloads LarkAgentX and installs its dependencies, which takes a minute or two. Meanwhile the connector's status reads 正在准备运行环境 ("preparing the runtime"). If it fails, the status says why and asks for Python 3.9+, pip, and access to PyPI (`pypi.org`, `files.pythonhosted.org`) and GitHub (`codeload.github.com`).
2. Open 飞书（LarkAgentX） in the connector settings and scan the QR code with the Feishu app. A code expires after about a minute and is replaced.
3. Optionally set 站点 (site: Feishu or Lark; changing it needs a new scan) and 分拣规则 (your own triage rules, used instead of the hub's global ones).

## What it does

| Capability | How |
| --- | --- |
| Sign-in | `login.step` with `kind: "qr"`; the code comes from LarkAgentX's `QrLogin` and is replaced when it expires. |
| Receiving | LarkAgentX's long connection, reconnecting with a 1–60 s backoff. After a reconnect it catches up on the 30 chats that moved last, up to 50 messages each. |
| Inbox | One item per chat. Direct messages and @-mentions go to the triage model; group messages without a mention are folded by rule (`tier: noise`) and cost no model call. An item ends when the user replied last. Every item carries a fallback tier for when the model cannot be called. |
| Thread page | `thread.page` serves what the program has kept and reads older messages from the server by position. Pictures come through `thread.media`; files show their names only. |
| Knowledge base | `archive.changes`; the cursor is a local row number, so nothing is delivered twice. The identity hint is `lark`. |
| Assistant tools | `larkx_status`, `larkx_chats`, `larkx_messages`, `larkx_search`. |
| Sending | `larkx_send` (to a chat; `reply_to` answers in a message's topic) and `larkx_send_dm` (a direct message to a person). |
| `/agent` | The user's own live message that starts with `/agent` goes to the hub with `channel.receive`, with the chat it was written in and the messages just before it. The answer comes back through `channel.send` and is posted as the user under the request, ending with an AI-answer mark. Only a reply to one of the user's own `/agent` messages is sent (anything else is `cannot_send`), each once. Fetched history, old messages, other people's `/agent` and the answers themselves never trigger it. |

**Writes.** `confirm_always: ["larkx_*"]`: even an `allow` in `connector_gate` is held for the phone confirmation, so the assistant cannot send as the user without them. The confirmation names the recipient, quotes the message a reply answers, and shows the full text. A direct chat seen only through the user's own messages says it does not know the other person yet. A reply the user sends from the app goes through `inbox.reply`, where pressing send is the confirmation.

Each send runs at most once per `request_id` (actions) or `client_id` (replies); an action without a `request_id` is refused. When the result is unknown (the request failed, or the answer has no message id), the program returns an error and never retries, because the message may have gone out.

**Nothing is marked read.** LarkAgentX's own `lark listen` marks handled messages read; this extension does not, so unread counts in Feishu stay as they are.

## Not there yet

LarkAgentX's `client.api()` reaches over 2,200 gateway commands, so most of this is wiring:

- Listing every chat: the program knows the chats it has seen a message in since sign-in. Older ones appear with their next message, or through `larkx_search`.
- The other person of a direct chat seen only through the user's own messages (the chat with oneself among them): it is named once they write.
- The app's Feishu chat page (`/api/v1/lark/…`): it would have to take over the `lark` connector's `api`.
- Docs, Moments, pins, reactions, Feishu's own encryption, read state, sending pictures and files.

## Structure

| File | Contents |
| --- | --- |
| `bin/larkx` | The launcher: `python3` with `src/` on the path. Starting needs nothing installed. |
| `src/larkx_persona/wire.py` | One JSON message a line; each request runs on its own thread, so a handler can call back into the hub. |
| `src/larkx_persona/main.py` | Handshake and namespaces. stdout is the protocol only; a library's `print` goes to the log. |
| `src/larkx_persona/connector.py` | Every connector method; knows nothing of LarkAgentX. |
| `src/larkx_persona/store.py` | SQLite: `status` and `inbox.collect` answer without the network; send deduplication. |
| `src/larkx_persona/real.py` | LarkAgentX: download and check it, install the dependencies, keep the sign-in in the hub, hold the long connection. Its log carries message bodies, so only where an error happened is kept. |
| `src/larkx_persona/upstream.json` | LarkAgentX's repository, pinned commit, archive URL and each file's SHA-256. |
| `src/larkx_persona/fixture.py` | A fake backend with synthetic data, used only when a test sets `LARKX_PERSONA_BACKEND=fixture` (an installed extension never gets that variable). |

## Tests and verification

```sh
(cd extensions/larkx && python3 -I -B -m unittest discover -s tests)
```

`scripts/ci.sh` runs these (step `python`). `tests/test_connector.py` covers what the conformance test does not call: `actions.do`, `inbox.reply`, failed and uncertain sends, sign-in and expiry, pruning. `tests/test_agent.py` covers `/agent`: what is handed over, what is not, and `channel.send` once per ref. `tests/test_upstream.py` checks the download and its file-by-file check against a synthetic archive, and that `upstream.json` and `requirements.txt` pin everything. `tests/test_real.py` decodes a synthetic push with the real LarkAgentX and checks the sign-in never reaches a file; it needs the directories the program downloads on first start (see the top of the file) and skips without them.

The conformance tests live in Persona, whose `internal/` packages only it can import: the real Python program on the fake backend passes `plugin/conformance.Check` and `CheckProgram`, and the channel passes `channel/conformance.Check`.

```sh
scripts/package.sh larkx   # the packaged manifest: the source one carries the market-only "icon"
LARKX_PERSONA_BACKEND=fixture PERSONA_TEST_CONNECTOR_PROGRAM="$PWD/dist/larkx/bin/larkx" \
  go -C <persona>/server test ./internal/connector/conformance -run ConnectorProgramLive -v
```

A real account can only be checked by hand: scan, receive a message, send one to yourself, `/agent` in a chat, sign out.
