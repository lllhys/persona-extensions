# 1Password

[Documentation](../README.md) · [Source](../../extensions/onepassword/)

This extension (id `onepassword`) connects Persona to your 1Password vaults, either those you grant to a 1Password [service account](https://developer.1password.com/docs/service-accounts/) or those of the account the [1Password CLI](https://developer.1password.com/docs/cli/) (`op`) is signed in to on one of your computers. The assistant gets three read-only tools: the vaults it can read, item search by title, tag or website, and one item's fields with each field's `op://` [secret reference](https://developer.1password.com/docs/cli/secret-reference-syntax/). No tool returns a secret value, and nothing the extension does changes your vaults. It is also Persona's secret provider for `op://` references: when one of Persona's settings holds a reference, or you approve handing one to an Agent run, Persona's manager asks this extension for the value.

This page describes version 0.3.1. When the manifest shown in the install preview disagrees with this page, the manifest wins.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `connectors` | `onepassword` (1Password) | Capabilities `status` and `tools`; no actions, no Inbox. No `agent_tools`: only the assistant gets the tools, not coding agents. |
| `ui` | `connection` (连接, `extension_page`) | The connection page, a row in the extension's details. |
| `secret_providers` | `vault` (1Password, scheme `op`) | Describes and resolves `op://` references for Persona's manager; see [Secret provider](#secret-provider). |

It also declares a `node` program, the same binary run as `onepassword node` on the computer picked in its `machines` setting (see [With op on one of your computers](#with-op-on-one-of-your-computers)); `settings_check`, so the hub asks the extension before it saves a new token; and one secret, `service_account_token`, optional since 0.3.0. It needs a hub built on or after 2026-11-06 15:02 UTC (`engines.persona` `>=202611061502`), the first with settings checks. Secret references need a hub with the secret provider extension point (nwverse/persona#3962, from 2026-11-11); an older hub installs the extension and leaves `secret_providers` unused.

## Architecture

```text
<account>.1password.com / .ca / .eu
                ▲
                │ HTTPS (sandboxed: through the hub's egress proxy)
                │
1Password Go SDK (WebAssembly core) inside the extension process onepassword (started by the hub)
                │
                └── protocol 1 + connector protocol v2 (stdin / stdout) ──▶ hub (personad) ──▶ assistant tools / app pages
```

- The program is Go, built with the repository SDK and the official [1Password Go SDK](https://github.com/1Password/onepassword-sdk-go) (`onepassword-sdk-go` v0.4.1), one binary per platform. Service accounts have no public REST API; the SDKs and the `op` CLI are the supported ways in.
- The SDK's core is WebAssembly compiled in the process. The first sign-in of a run takes a second or two and the process then uses about 170 MB of memory, so the extension signs in only when a tool, the connection page or a settings check needs it, and keeps the client for the rest of the run.
- The manifest lists `*.1password.com`, `*.1password.ca` and `*.1password.eu`, the hosts the SDK itself is limited to. Installed from this official market it runs without the hub's sandbox by default; with the sandbox on (`personad extension sandbox onepassword on`) it reaches only those hosts, through the hub's egress proxy.

With a computer picked instead (since 0.3.0):

```text
1Password app (signed in, unlocked, CLI integration on) ◀── op ◀── onepassword node   (on that computer, started by its node)
                                                                      │
                                                                      └── plugin.node.call ◀── hub nodes.call ◀── onepassword (on the hub)
```

- The program on the hub makes no requests to 1Password then and signs in with no token: each read is a `nodes.call` to the program on that computer (`op.vaults`, `op.items`, `op.item`, `op.read`, `op.status`), which runs `op vault list`, `op item list --vault <id>`, `op item get <id> --vault <id>` (each `--format json`) or `op read --no-newline -- <reference>`.
- `op` runs in the app's sign-in only: the program removes `OP_SERVICE_ACCOUNT_TOKEN`, `OP_CONNECT_HOST`, `OP_CONNECT_TOKEN`, `OP_SESSION*` and `OP_FORMAT` from its environment. It finds `op` on its `PATH` or in `/opt/homebrew/bin`, `/usr/local/bin` or `/usr/bin`, runs one `op` at a time (so the app asks once), and takes only 1Password IDs and `op://` references as arguments. Each `op` gets up to 60 seconds (time to approve it in the app), or less: the hub program sends how long it still waits (`within_ms`), and `op` is stopped half a second before that, so nothing, a value least of all, is read for a caller that has given up. An answer over 8 MB fails, and a value that is not text (a file's bytes) is refused rather than mangled.
- On Linux, `op` reaches the 1Password app through the user's session, which the node's clean environment leaves out; the program adds `XDG_RUNTIME_DIR` and the session bus from `/run/user/<uid>` when they exist. This has been tried only with a stand-in for `op`, not with the 1Password app on Linux.
- The node program's permissions in the manifest are `network` (the same three hosts, which `op` reaches) and `files` (`op` keeps its settings in the home directory). Programs on computers run without a sandbox; the hub shows these and asks before the program first runs on a computer.

## Setting it up

1. Sign in on 1Password.com, select **Developer** in the sidebar and create a service account. 1Password's [service account guide](https://developer.1password.com/docs/service-accounts/get-started/) says which accounts have them and which vaults can be granted.
2. Grant it only the vaults Persona may look into, with read access.
3. Copy the token (it starts with `ops_`). In Persona, open Settings › Extensions › 1Password and paste it as **服务账号令牌**.

The hub asks the running extension to check a new token before it saves it (`settings.check`): the extension signs in with a client of its own, lists the vaults and drops the client. A token that does not start with `ops_`, or that 1Password refuses, is not saved, and the reason is shown with the token removed. The first token after installing is saved unchecked, because the extension does not run until its required secret is set; the connection page then shows whether 1Password accepts it.

Changing or deleting the token restarts the extension with the new one. To stop the extension reading any vault, delete the token, switch the extension off, or revoke the token on 1Password.com.

Since 0.3.0 the token is optional, so the extension runs without one, and a first token is checked like any other.

### With op on one of your computers

For people without a service account (they need 1Password Business or Teams), or who would rather not keep a token on the hub:

1. On a Mac or Linux computer connected to Persona, install the 1Password app and the [1Password CLI](https://developer.1password.com/docs/cli/get-started/), and sign in to the app.
2. In the 1Password app, open Settings › Developer and turn on **Integrate with 1Password CLI**.
3. In Persona, open Settings › Extensions › 1Password and switch that computer on under **用哪台电脑上的 op** (at most one). Persona asks first, then runs the extension's program there.
4. The first time Persona reads through it, and again after the app locks, the 1Password app on that computer asks you to approve `op`.

With a computer picked, the extension uses it and ignores the token. Switching the computer off, or the extension, stops the program there.

## Tools

| Tool | Arguments | Returns |
| --- | --- | --- |
| `onepassword_vaults` | none | Each vault's name, ID, number of active items and last change. |
| `onepassword_search` | `query`, `tag`, `website`, `vault`; at least one | Matching active items, sorted by title, at most 50: name, ID, category, vault, tags, websites, last change. |
| `onepassword_item` | `vault` and `item` (both required) | One item's category, tags, websites and last change, and its fields by name, type and section, each with its `op://` reference. Notes and attached files are only counted. |

- `query` matches items whose title has every word of it, case aside. `tag` matches a tag or one nested under it (`work` matches `work/servers`). `website` matches a saved website on that host or one of its subdomains (`github.com` matches `gist.github.com`). `vault` is a vault's ID or its name; a name two vaults share is refused with both IDs.
- A search without `vault` looks through every vault the service account can read, at most 32 of them, and says when it left some out.
- `item` is an item's ID, or its exact title (case aside); a title two items share is refused with both IDs.
- Titles, tags and websites are the user's data; the tool descriptions tell the model they are not instructions.

### `op://` references

Each field's reference names the vault and the item by ID, so it keeps working after either is renamed. The section and the field are named by their names when the name is unique (a field outside any section among all the item's fields, one in a section among that section's) and uses only the characters 1Password allows in a reference (ASCII letters and digits, `-`, `_`, `.` and spaces); otherwise by ID. For example `op://<vault ID>/<item ID>/password` or `op://<vault ID>/<item ID>/Backup/recovery code`.

These references are what Persona's settings take (below).

## Secret provider

Persona's manager, and nothing else, calls the `secrets` methods (Persona's `docs/extensions/secret-providers.md` describes the host side: which settings take a reference, the approval of a hand-off to an Agent run, and Persona's own record of each use).

| Method | What it does |
| --- | --- |
| `secrets.describe` | Names what a reference points at for people: the title is `item › field` (`item › section › field` when another field of the item has the same name, so the card you approve tells them apart) and the detail only the vault, at most 40 characters, which fits a card's line on a phone. The names come from the item as 1Password has it, scrubbed of its values as the tools' are; a reference written with IDs is looked up to its names. When the reference cannot be looked up (no token, not found, a name two vaults or fields share), the reference's own names are used. A reference 1Password's own parser refuses (a `%` escape, an unknown `?attribute=`) is answered `invalid`, so nothing names what `secrets.resolve` would refuse. Never a value. |
| `secrets.resolve` | The value of one reference, through the service account (`Secrets().Resolve` in 1Password's SDK), or `op read` on the picked computer. |
| `secrets.used` | An event Persona sends after each resolution it asked for (the reference, what it was for, when, and whether Settings, an Always rule or a device approved it). The last 20, ordered by when they happened, are kept in memory and listed on the connection page as **Used by Persona**; they are gone when the extension restarts. A key Persona reuses from its own short-lived memory is not reported again, and Persona keeps its own record. |

Both check the reference with 1Password's own parser first (it runs locally, without a request). A failed resolution answers with Persona's codes, in the hub's language: `not_signed_in` (neither a token nor a computer, 1Password refused the token, or `op` on the computer is missing, has no account, or the app is locked or its approval was dismissed), `rate_limited` with `retry_after` 600 seconds, `not_found` (no such vault, item, section or field), `refused` (an empty field, a reference that matches more than one item or field, or one the parser refuses), and otherwise a failure in 1Password's words, redacted and on one line. With a computer picked, the code the program there gives decides, never `op`'s wording, which quotes item and vault names; an approval not given within the manager's time, an offline computer and one the hub cannot reach answer as plain failures that say so. Persona's manager may show its own words for `not_signed_in` and `not_found`. Each call gives 1Password up to 9 seconds, under the manager's 10; a call that first waits behind another (a sign-in, a tool's request) can still run out the manager's time.

## Keeping values out

- A value leaves the extension only as the answer to the manager's `secrets.resolve` (with a computer picked, `op read`'s answer travels from that computer through the hub to the manager). It is never kept, logged, put in an error or shown on the page, and the assistant's tools never return one.
- Only `onepassword_item` and `secrets.describe` read a whole item, since 1Password's SDK has no call that returns an item's fields without their values. The values exist in the extension's memory only while that answer is turned into metadata: field values, notes, one-time codes and field details are dropped there, and any of those values that also appears in a name the extension keeps (a title, a tag, a website, a field or section name) is replaced by `[hidden]` first. Searches and vault lists read overviews only, which carry no values. With a computer picked, all of this happens on that computer: the program there drops values, notes, one-time codes (including the current code `op` adds), file names and field details, scrubs names and cleans website addresses before it answers, and leaves out `op`'s extra line about an item (often a user name) and the accounts' addresses.
- A saved website keeps only its scheme, host and path: a user name, password, query or fragment in a saved URL can carry a credential, so they are left out.
- Every tool result, error, status line and page is redacted before it leaves the program: the service account token, its first or last 12 characters, and anything shaped like a service account token become `[hidden]`. Errors are cut to one line of at most 300 characters.
- Nothing is written to disk, and the extension logs nothing about vaults, items, references or the token.

## Status and the connection page

The connector's status answers from memory, as the hub asks every 30 seconds: `not_configured` without a token or a computer; `not_signed_in` after 1Password refused the token, or `op` on the computer was missing or had no account (a locked app or a dismissed approval is said in that answer and on the page, but keeps the tools offered, as it is routine) (the tools are not offered until the token is changed or the page signs in again successfully); `rate_limited` for 10 minutes after 1Password's rate limit; `ok` otherwise, also after an ordinary error such as a network failure, so one failed call does not take the tools away.

Settings › Extensions › 1Password › 连接 signs in (if this run has not yet), lists the vaults, and shows:

- **Not set up**: how to create the service account and where to paste the token, or how to use `op` on a computer instead.
- **Connected**: the number of vaults and each vault with its item count, the recent uses of references by Persona (**Used by Persona**: the reference, what it was for, who approved it and when), plus what the assistant can and cannot do.
- **Can't reach 1Password**, **1Password refused the token** or **rate limit reached**: the redacted reason, what to do, and **Check again**.
- With a computer picked: first whether `op` is installed there (`op --version`, which asks the app nothing), then the vaults, which asks the app. **op isn't installed on that computer**, **op on that computer isn't signed in**, **1Password on that computer is locked** (also when the app's prompt was dismissed or not answered within the page's 20 seconds) or **That computer is offline**, each with what to do. Nothing retries by itself: the next tool call, resolution or **Check again** tries once more. A vault `op` gives no item count for is listed without one.

The page is drawn in English, Japanese and Simplified Chinese, following the app's language.

## Service account limits

1Password limits how many requests a service account may make per hour (per token) and per day (per account), depending on the account type. Without the cache below, a search without `vault` makes one request for the vault list and one per vault; `onepassword_item` makes three (vaults, the vault's items, the item), and so does `secrets.describe` (Persona keeps each description for 10 minutes); `secrets.resolve` makes one. When 1Password refuses for the limit, the tools answer with the `rate_limited` code and the status says so.

### What the extension keeps in memory

Since 0.3.1 the extension keeps 1Password's metadata in its own memory, for both connections (the service account and `op` on a computer):

- **What**: the vault list, each vault's item overviews and each item's field structure, as the extension keeps them anyway: names, IDs, categories, tags, cleaned websites, field names, types, sections and `op://` references. Never a value, a note or a one-time code: those are dropped before anything is kept (on the computer, for `op`). A field or section name that held a value is kept scrubbed, beside a keyed hash of the original (the key is random and lives only in the process), so a reference that spells it out still finds it and its description never shows the value. Nothing is written to disk.
- **For how long**: 5 minutes from when it was asked for. Within them, a describe, a search, a vault list or an item look answers from memory: describing a reference again, or another field of an item already read, makes no request. A vault, item or field named that a kept answer lacks is looked up again once when that answer is older than 30 seconds (it may have just been made). The connection page asks for the vault list afresh each time it is drawn.
- **Emptied**: when the program starts, which the hub does whenever the token, the computer or a setting changes, or the extension is upgraded or switched on again; and by **Check again** on the connection page (except while the rate limit holds). A read that was under way when it was emptied keeps nothing it got.
- **Under the rate limit** (the 10 minutes after 1Password refused for it, ended early only by a request that succeeds, such as a resolve): reads answer from what is kept, however old, and ask 1Password nothing; one that finds nothing kept answers the limit at once. A refresh or a second look that 1Password refuses for the limit answers with what was kept, which stays. The connection page then shows the limit above the vaults it kept, never **Connected**.
- **Never for values**: `secrets.resolve` asks 1Password every time. Persona's manager keeps a resolved model provider key for 10 minutes on its own side (Persona's `docs/extensions/secret-providers.md`, "Launcher keys"); the extension keeps none.

## Not in this version

- Creating or editing items, browser autofill, and 1Password's SSH agent or Git signing.

## Tests

```sh
go -C extensions/onepassword test ./...
scripts/package.sh onepassword
```

The tests use a fake 1Password that answers the SDK's own types, with synthetic secret values in its items, and check that no tool result, description, error, status or page carries any of them or the token; that a reference resolves to its value by names or IDs and fails with the right codes; and that the page lists uses newest first, at most 20. For `op` on a computer, a fake `op` answers in the shape `op` 2 writes: the tests check that no answer leaving the computer carries a value, a file name or an address with a password in it, that only IDs and references reach `op`'s arguments, that `op` runs without a service account token in its environment (a real process, from a script standing in for `op`), and what the page, the status and the secret provider say when `op` is missing, locked, offline or finds nothing. They also run the SDK's process-protocol conformance checks and validate the connection page's views. No test needs a real 1Password account.
