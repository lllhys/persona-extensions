# Feishu via a user-installed LarkAgentX companion

[Documentation](../README.md) · [Extension source](../../extensions/larkagentx/README.md)

**Experimental, Feishu-only, not real-account validated.** This is a new connector named `larkagentx`, not an larkintegration replacement. Installing it does not migrate, overwrite or delete larkintegration configuration, tokens, accounts or routes.

## Licensing and architecture

[LarkAgentX](https://github.com/cv-cat/LarkAgentX) has no declared license at the reviewed revision, `97ad48e69e34bd8c78ee30f795b7c3759dd81659`. Public visibility does not grant redistribution rights.

The chosen route is **user-installed upstream with an original thin adapter**:

- This repository and its packages contain no upstream source, generated protobuf files, command maps, screenshots or other upstream assets. They do not download or install upstream automatically.
- The original Python adapter calls the separately installed library's authentication, WebSocket and text-send APIs. It does not reproduce the reverse-engineered wire protocol. Only API signatures and return behavior were inspected for adaptation.
- Users must independently obtain permission to use upstream, review its dependency licenses and comply with Feishu's terms and their account/organization policies. This adapter grants no upstream redistribution rights. If that review does not permit use, do not install upstream or enable the extension.
- Pin upstream to the revision above. Upgrades need a new API/security review and authorized account validation, not an automatic dependency update. Upstream dependencies are installed by the user and are not part of Persona's distribution.

The hub runs the repository's standard **Go executable**, with no Python runtime or upstream installation. It starts a user-configured SSH command whose stdio connects to an isolated companion on another machine. Credentials never appear in that command. The companion requires **Python 3.9+ and user-installed LarkAgentX** in a virtual environment. The original helper is embedded in the Go executable and can be extracted for copying to that machine.

| Component | Platform scope |
| --- | --- |
| Hub executable | Linux amd64/arm64 and macOS amd64/arm64; statically cross-built with `CGO_ENABLED=0` |
| Companion | Linux or macOS with Python 3.9+; POSIX stdio pipes, a private temporary directory and the pinned upstream |
| Windows companion, international Lark | Not supported in 0.1.0; upstream claims are not Persona validation |

The extension does not request hub HTTP API access or contribute HTTP routes. Its only hub callbacks are scoped `connector.secrets.*` and `connector.changed`. The configured SSH command is executable code the user trusts, not a sandbox. Allow SSH egress only to the chosen companion, and limit the companion's egress to the public service endpoints needed by the user-installed upstream. Network restrictions must be enforced by the user's OS/firewall; the manifest cannot enumerate a user-specific SSH host. A compromised companion can access the account session handed to it, so use a dedicated account and machine.

## Capabilities and security boundaries

- Receive **text messages only** through upstream's WebSocket client into Persona's generic inbox. Both the conversation ID **and** sender ID must be in nonempty exact allowlists; self-authored echoes, unknown senders, other conversations and non-text messages are discarded. No wildcard authorization is supported.
- Send one text message of up to 4,096 characters to an allowed conversation, optionally in a topic. `larkagentx_send_text` is an action, not a read tool. `confirm_always` makes Persona confirm the destination, topic and complete text even if a gate rule says `allow`; `deny` remains effective.
- There is deliberately no `channel.send`, `tools.call` send shortcut, `inbox.reply` or autonomous answer delivery. The inbox is read-only (`can_reply:false`). Ask Persona to use the named action when sending. This avoids automatic channel replies bypassing per-write confirmation.
- App QR login/polling, private credential-JSON import, periodic validation, explicit re-login and local logout are adapted. QR state is ephemeral. A failed authentication probe stops reception/sending and requests login again; upstream may not reliably distinguish temporary network failures from expiry, so this is conservative.
- No upstream agent execution, CLI execution tools, unrestricted API, history backfill, attachments, search, knowledge-base archives, read receipts or conversation-management writes. Upstream's normal WebSocket ACK/heartbeat remains necessary; its library may perform read-only conversation metadata lookups while dispatching messages.

The helper imports only auth/client/region APIs, disables upstream logging and stdout/stderr output, and forces `LARKX_AGENT_BACKEND=none` and `LARKX_MARK_READ=false` in a private runtime home. It never starts the upstream CLI or agent. Only fixed error codes cross the bridge; exceptions, cookies, tokens, account profiles and upstream error bodies are not logged or returned as status.

## Installation

First review the licensing and service-policy boundary above. Use an explicitly authorized test account, not an unattended production rollout.

1. Build the standard package and install it on the hub (commands run from this repository's root):

   ```sh
   scripts/package.sh larkagentx
   personad extension install file:/absolute/path/to/larkagentx-<version>.tar.gz
   ```

   Replace `<version>` with the version in [the manifest](../../extensions/larkagentx/persona-extension.json).

   This delivery uses local verification and does not include manual market publication. A merged version is published to GHCR by the normal publication workflow ([releases](../releases.md)); until the signed catalog lists it, install the local package.

2. On the companion, create a private virtual environment and manually install the upstream checkout at the reviewed revision. Follow upstream's installation documentation after obtaining permission. Do not vendor that checkout into this repository or a redistributed Persona package. `python -I` disables user-site packages; install upstream **in the same virtual environment** used for the companion command.

3. Extract the original helper from the hub binary and copy it to a private path on the companion:

   ```sh
   /absolute/path/to/larkagentx-linux-amd64 companion-script > bridge.py
   scp bridge.py '<companion SSH alias>:<absolute adapter script path>'
   ```

   Choose the binary for the hub's actual platform. Extraction needs no Python on the hub. Keep the script outside the upstream checkout, protect it against other users modifying it, and do not copy upstream assets with it.

4. Configure **only this extension** in Persona:

   | Setting | Value |
   | --- | --- |
   | `bridge_command` | JSON array of executable and arguments, as below |
   | `allowed_chats` | Exact authorized conversation IDs, comma- or newline-separated; no wildcard |
   | `allowed_senders` | Exact authorized sender IDs, comma- or newline-separated; both lists are required |
   | `login_method` | `qr` (default) or `import` |
   | `triage_rules` | Optional per-connector inbox rules; empty uses Persona's global rules |

   Replace all placeholders locally; never post actual values or account output in an issue:

   ```json
   ["ssh", "-T", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10", "<companion SSH alias>", "<absolute venv python path>", "-I", "<absolute adapter script path>"]
   ```

   Configure and verify the SSH host key and key authentication yourself first. Do not disable host-key checking. SSH constructs the remote command through the remote shell: use absolute paths without whitespace or shell metacharacters, or use a dedicated forced-command SSH key. No session JSON, cookie or token belongs in `bridge_command`, environment configuration or shell history. Determine the allowlisted IDs privately with the account's own tools; this extension intentionally provides no enumeration API.

5. Open the connector's **Sign in** flow in Persona. In `qr` mode, scan with the authorized Feishu account and finish upstream's account approval. Persona polls the same step until success or expiry. Starting sign-in again replaces the old pending QR flow. In `import` mode, paste the pinned upstream's credential JSON into the hidden secret field. If obtaining that JSON through upstream's CLI, use a private temporary upstream home and remove it after import; never reuse larkintegration's data directory.

   This is headless on the companion: it needs no browser/display, and the QR appears through Persona's existing login UI. The implementation is covered with synthetic QR/auth objects only; actual Feishu QR acceptance remains unverified.

6. After signing in, send a synthetic test message from an allowed sender in an allowed conversation, verify it appears once in the inbox, then request `larkagentx_send_text`. Verify the confirmation shows the full text and destination before approving. Verify a denied sender/conversation produces no inbox item. **These real-account steps have not yet been performed.**

## Credential and delivery lifecycle

Persona's encrypted `connector.secrets` store is the sole durable store for this connector's session, bounded inbox cache, dedup hashes and send journal. It uses the connector ID `larkagentx`, independent of larkintegration. The helper holds session objects in memory and uses only 0700 temporary directories/0600 credential files required by upstream's file-based auth object. Normal EOF, replacement and logout remove them; a hard kill or machine crash can leave private temporary files, so use a private temporary filesystem and remove stale `persona-larkx-*` directories on the companion after stopping its processes. This is not an OS-level credential sandbox.

Logout closes the bridge, drops inbox/session/QR state and deletes this connector's encrypted secret. A failed secret deletion is reported as a failure, not as successful durable logout. It **does not revoke all Feishu web sessions or log out larkintegration**. Revoke a compromised session using Feishu's own account controls.

Transient bridge/WS disconnects restart with bounded exponential backoff (2–60 seconds). A failed probe or send that drops the companion reconnects on the same backoff, starting at 2 seconds. Status and inbox collection are cached and do not issue network requests; auth probes run in the background about once a minute. Messages that arrive while a probe runs are still delivered, also when the probe or a send then fails and the companion restarts; only a sign-out discards them. A probe that returns a different account ID than the one checked at sign-in reports the session as signed out. Incoming message hashes survive bridge and extension restarts, with a rolling 4,096-ID window; the cache holds the latest message per thread for up to 128 threads. Collection also filters the saved cache against the current allowlists, so tightening authorization does not re-expose previously cached conversations through this connector. This is a bounded live inbox, not a complete archive or guaranteed catch-up: upstream reconnect replay is not assumed, and messages received while disconnected may be missed. A message outside the dedup window can be accepted again.

Before an outbound request crosses SSH, its hashed request ID and exact account/payload fingerprint are durably reserved. Repeating the same request returns its saved receipt or `unknown_result`, never another send; changing the account or text under that ID is rejected. Timeouts, missing message IDs and lost responses are unknown outcomes, not proof of failure. Check the account before intentionally creating a new request. The journal fails closed at 1,024 entries rather than evicting reservations and accidentally resending. Logout clears it, so do not replay old requests after logout/re-login. This deliberately favors avoiding duplicates over automatically recovering a lost reply.

## Bridge contract and compatibility

Persona process protocol 1 and connector protocol 2 are used through the repository's official Go SDK copy. Both manifest names are packaged. The minimum hub build matches the existing connector extension baseline; no UI 3, runtime resource or scoped HTTP permission feature is required.

The original companion speaks JSON-lines over stdio, limited to 1 MiB per frame:

- Requests: `{id, method, params}`; replies: `{id, result}` or `{id, error:{code}}`. `params` is always an object, including no-argument probes. An unset send `anchor` is omitted rather than sent as an empty string; the synthetic Go-to-Python protocol test covers both rules.
- Methods: `session.open`, `session.check`, `session.send`, `session.close`, `login.begin`, `login.poll`, `login.cancel`. There is no generic upstream API/command method.
- Events: `{event:"message", message:{id,chat,sender,text,anchor?,at?}}` or `{event:"status",state}`.
- Session/QR payloads are private protocol traffic, not diagnostic logs. SSH banners/PTY output on stdout break the protocol; use `-T` and a clean remote command.
- Only `feishu` is accepted. Updating the helper and Go binary together avoids protocol drift.

## Local validation and remaining work

```sh
python3 -I -B extensions/larkagentx/bridge_test.py
go -C extensions/larkagentx test -race ./...
scripts/ci.sh
```

The tests use only synthetic auth/client/QR objects, messages, IDs and subprocesses. They cover protocol/conformance, secret and exception redaction, auth failure, both allowlists, self-echo/non-text filtering, QR completion/expiry, private temporary files and cleanup, receive dedup across reconnect/restart, durable send reservations and unknown outcomes, concurrent duplicate sends, timeouts and isolation from larkintegration identities. Standard repository checks also validate manifests, inbox settings, documentation links, all extensions' Go tests, four-platform packaging and the unchanged signed indexes.

Remaining: authorized real Feishu login/receive/confirmed-send/expiry testing, physical-platform execution (cross-builds are not runtime tests), international Lark, and optional market publication. No claim of real-account interoperability, upstream license clearance or market availability is made by the synthetic tests.
