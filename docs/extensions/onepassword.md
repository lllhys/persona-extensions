# 1Password

[Documentation](../README.md) · [Source](../../extensions/onepassword/)

This extension (id `onepassword`) connects Persona to the 1Password vaults you grant to a 1Password [service account](https://developer.1password.com/docs/service-accounts/). The assistant gets three read-only tools: the vaults the service account can read, item search by title, tag or website, and one item's fields with each field's `op://` [secret reference](https://developer.1password.com/docs/cli/secret-reference-syntax/). No tool returns a secret value, and nothing the extension does changes your vaults.

This page describes version 0.1.0. When the manifest shown in the install preview disagrees with this page, the manifest wins.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `connectors` | `onepassword` (1Password) | Capabilities `status` and `tools`; no actions, no Inbox. No `agent_tools`: only the assistant gets the tools, not coding agents. |
| `ui` | `connection` (连接, `extension_page`) | The connection page, a row in the extension's details. |

It also declares `settings_check`, so the hub asks the extension before it saves a new token, and one secret, `service_account_token`. It needs a hub built on or after 2026-11-06 15:02 UTC (`engines.persona` `>=202611061502`), the first with settings checks.

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

## Setting it up

1. Sign in on 1Password.com, select **Developer** in the sidebar and create a service account. 1Password's [service account guide](https://developer.1password.com/docs/service-accounts/get-started/) says which accounts have them and which vaults can be granted.
2. Grant it only the vaults Persona may look into, with read access.
3. Copy the token (it starts with `ops_`). In Persona, open Settings › Extensions › 1Password and paste it as **服务账号令牌**.

The hub asks the running extension to check a new token before it saves it (`settings.check`): the extension signs in with a client of its own, lists the vaults and drops the client. A token that does not start with `ops_`, or that 1Password refuses, is not saved, and the reason is shown with the token removed. The first token after installing is saved unchecked, because the extension does not run until its required secret is set; the connection page then shows whether 1Password accepts it.

Changing or deleting the token restarts the extension with the new one. To stop the extension reading any vault, delete the token, switch the extension off, or revoke the token on 1Password.com.

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

This version only shows references. Persona cannot use them yet: entering a reference wherever Persona takes a key, and handing a value to an Agent's run after a phone approval, need host support (nwverse/persona#3962).

## Keeping values out

- Only `onepassword_item` reads a whole item, since 1Password's SDK has no call that returns an item's fields without their values. The values exist in the extension's memory only while that answer is turned into metadata: field values, notes, one-time codes and field details are dropped there, and any of those values that also appears in a name the extension keeps (a title, a tag, a website, a field or section name) is replaced by `[hidden]` first. Searches and vault lists read overviews only, which carry no values.
- A saved website keeps only its scheme, host and path: a user name, password, query or fragment in a saved URL can carry a credential, so they are left out.
- Every tool result, error, status line and page is redacted before it leaves the program: the service account token, its first or last 12 characters, and anything shaped like a service account token become `[hidden]`. Errors are cut to one line of at most 300 characters.
- Nothing is written to disk, and the extension logs nothing about vaults, items or the token.

## Status and the connection page

The connector's status answers from memory, as the hub asks every 30 seconds: `not_configured` without a token; `not_signed_in` after 1Password refused the token (the tools are not offered until the token is changed or the page signs in again successfully); `rate_limited` for 10 minutes after 1Password's rate limit; `ok` otherwise, also after an ordinary error such as a network failure, so one failed call does not take the tools away.

Settings › Extensions › 1Password › 连接 signs in (if this run has not yet), lists the vaults, and shows:

- **Not set up**: how to create the service account and where to paste the token.
- **Connected**: the number of vaults and each vault with its item count, plus what the assistant can and cannot do.
- **Can't reach 1Password**, **1Password refused the token** or **rate limit reached**: the redacted reason, what to do, and **Check again**.

The page is drawn in English, Japanese and Simplified Chinese, following the app's language.

## Service account limits

1Password limits how many requests a service account may make per hour and per day, depending on the account type. A search without `vault` makes one request for the vault list and one per vault; `onepassword_item` makes three (vaults, the vault's items, the item). When 1Password refuses for the limit, the tools answer with the `rate_limited` code and the status says so.

## Not in this version

- References in Persona's settings, the approved hand-off of a value to an Agent's run, and a record of each use (reference, time, who; never the value): nwverse/persona#3962, then this extension.
- Connecting through the `op` CLI on one of your computers, signed in through the 1Password desktop app: nwverse/persona-extensions#376.
- Creating or editing items, browser autofill, and 1Password's SSH agent or Git signing.

## Tests

```sh
go -C extensions/onepassword test ./...
scripts/package.sh onepassword
```

The tests use a fake 1Password that answers the SDK's own types, with synthetic secret values in its items, and check that no tool result, error, status or page carries any of them or the token. They also run the SDK's process-protocol conformance checks and validate the connection page's views. No test needs a real 1Password account.
