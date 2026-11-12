# ChatGPT apps

[Documentation](../README.md) · [Source](../../extensions/chatgpt-apps/)

This extension (id `chatgpt-apps`) lets the Persona assistant use the apps connected to your ChatGPT account (mail, calendar, code hosting, brokerage, documents and others) and the ChatGPT plugin marketplace. You sign in to ChatGPT once, on the phone, with a device code. Reads run directly; every call the assistant makes that changes something waits for your approval on the phone. The extension also has its own pages in the app: the plugin marketplace, a plugin's details with Install and Uninstall, your installed plugins and your connected apps.

It talks to ChatGPT's own web backend (`chatgpt.com/backend-api`), which is not a documented public API and can change without notice, and signs in with the OAuth client the Codex CLI uses. OpenAI does not offer either for other programs: use it at your own risk under OpenAI's terms, which can include limits on your ChatGPT account. When ChatGPT changes, the extension's errors say so; update the extension.

Up to 0.2.2 it was published in a private market. From 0.2.3 it is published here and behaves the same.

This page describes version 0.2.3. When the manifest shown in the install preview disagrees with this page, the manifest wins.

| Extension point | Contribution | Behavior |
| --- | --- | --- |
| `connectors` | `chatgpt` (ChatGPT 应用) | Capabilities `status`, `tools`, `actions`. Every action is `confirm_always` (`chatgpt_*`): an allow rule in `connector_gate` cannot skip the card. No `agent_tools`: only the assistant gets the tools, not coding agents. |
| `assistant` | `chatgpt-apps` | The `chatgpt-apps` skill in `assistant/skills/`. |
| `auth` | ChatGPT 登录 | Sign-in with a device code, in the extension's details. |
| `ui` | `plugins` (插件, `extension_page`) | The plugin pages, a row in the extension's details. |

## Architecture

```text
chatgpt.com/backend-api (MCP of the connected apps, plugin marketplace)     auth.openai.com (device code, tokens)
*.oaiusercontent.com (plugin packages, no token sent)
                 ▲                                                                   ▲
                 └──────── HTTPS (sandboxed: through the hub's egress proxy) ────────┘
                                              │
extension process chatgpt-apps (started by the hub) ── protocol 1 + connector protocol v2 (stdin / stdout) ──▶ hub (personad)
                                                                          gate and phone cards / assistant tools / app
```

- The program is Go, built with the repository SDK, one binary per platform.
- The tokens stay in the extension process and in the hub's encrypted connector secrets (`connector.secrets.*`). They are never written to a file, a log or an answer, never given to the assistant, and never put in an environment variable. The assistant runs on the hub machine as the same user, so a plain file would be readable by it.
- Its manifest lists only `chatgpt.com`, `auth.openai.com` and `*.oaiusercontent.com` (plugin packages; the host is regional, such as `sdmntpr<region>.oaiusercontent.com`). Installed from this official market it runs without the hub's sandbox by default; with the sandbox on (`personad extension sandbox chatgpt-apps on`) it reaches only those hosts, through the hub's egress proxy, which goes out through the hub's own upstream proxy when it has one.

## Sign-in

After installing, the app opens the sign-in card; later it is under Settings › Extensions › ChatGPT 应用 (**登录**, **重新登录**, **退出登录**).

1. Tap **登录**. The card shows a link (`https://auth.openai.com/codex/device`), a code and a QR code of the link.
2. Open the link on the phone (or scan the QR code with another device), sign in to ChatGPT, enter the code and allow access. **The page says Codex**: the extension uses the Codex CLI's OAuth client, so this is expected. Revoking that access later is revoking Codex's.
3. The card turns into the signed-in account (mail address and plan).

The extension polls in the background until the code expires, so the sign-in also finishes when you leave the card. If your ChatGPT account asks you to allow device-code sign-in for Codex in its security settings first, do that and start again.

The sign-in is the extension's own; it never reads or writes `~/.codex/auth.json`. ChatGPT's refresh tokens work once: the extension refreshes one at a time and stores the new tokens before using them. When a refresh is refused (the sign-in was revoked or used elsewhere), the extension signs out, the details say the sign-in expired, and the assistant's ChatGPT tools disappear until you sign in again.

## What the assistant gets

The tools are few and general; the connected apps' own tools are named as an argument:

| Kind | Tool | What it does |
| --- | --- | --- |
| read | `chatgpt_apps` | The connected apps' tools, each with its app, arguments and whether it is read-only; `filter`, `refresh` |
| read | `chatgpt_tool_schema` | One tool's description and argument schema |
| read | `chatgpt_read` | Runs a read-only tool; refuses one that can change data |
| read | `chatgpt_plugin_search`, `chatgpt_plugin_info`, `chatgpt_plugins_installed` | The marketplace, one plugin's apps (connected or not) and skills with its `connect_url`, the installed plugins |
| read | `chatgpt_plugin_skill` | An installed plugin's skills (SKILL.md and files), from its package downloaded into the extension's data directory |
| action | `chatgpt_write` | Runs a tool that changes data (sends mail, creates or deletes things, places order instructions…) |
| action | `chatgpt_plugin_install`, `chatgpt_plugin_uninstall` | Installs or removes a plugin on your ChatGPT account |

**Read-only** means: ChatGPT marks the tool read-only, does not mark it destructive, and no word of its name is a write verb (send, create, delete, update, order, transfer, reply, forward, archive and others). Anything else is a change. Apps from other developers can mark a write read-only; the name check catches the common cases, and a card too many is the safe side.

Results come back whole, however long: a mail app's list with every message's full body is still the complete JSON, so a source subscription can read it, and the Agent's runtime keeps a long result aside and reads it in parts. The only bounds protect the hub: at most 32 MB of one ChatGPT answer is read, and a result over 48 MB once encoded is not passed on, because the hub reads a program's answer as one line of at most 64 MB and drops the connection on a longer one. Either is an error asking for less at a time, never a cut result.

Apps that were installed with a plugin but never connected are listed, but not run: the assistant gives you the plugin's `connect_url`. The original Claude Code plugin calls such apps' public servers directly when they need no sign-in; this extension does not, because those hosts are not in its network permission.

## Plugin pages

Settings › Extensions › ChatGPT 应用 › **插件** opens the extension's own pages (declarative UI, drawn by each app in its own style; only components of UI version 3, which every app draws):

- **Marketplace**: a search box over ChatGPT's plugin marketplace; without a search, your installed plugins (the first five, then a row to all of them), a row to your connected apps (how many apps and tools) and the marketplace in ChatGPT's order. Each plugin shows its name, developer, what it does and whether it is installed; scrolling to the end loads 20 more.
- **A plugin**: its description, developer and version; its apps, each connected (with its tool count), to connect, or to connect after installing; its skills; and **Install** or **Uninstall**. When an installed plugin's app is not connected, the page links to its `connect_url`: open it in a browser signed in to ChatGPT, click **Connect**, and pull to refresh.
- **Installed plugins**: every installed plugin, each opening its page. Apps of UI version 6 also swipe to uninstall.
- **Connected apps**: each app with how many tools it has and how many are read-only, opening its tools, each marked read-only or changes data.

Installing and uninstalling on these pages is your own tap: the app asks once more in a confirm dialog, then it runs, without the hub's card. The assistant's calls still get the card every time. Signed out, the pages say to sign in from the details; when ChatGPT cannot be reached they say so with **Try again**. What other people wrote (descriptions, names) is shown as plain text, clipped.

The pages read ChatGPT when drawn: the installed plugins again after a minute or after an install or uninstall, the marketplace and the tool list after ten minutes.

## Approving changes

Every action shows a card on the phone (the hub's connector card). The card says, in your language, which app and tool it calls as you, the tool's description, and every argument in full: recipients first, then short values, then long text. Who it reaches (`to`, `cc`, `channel`, `attendees`…) is the card's 「谁会看到」 line. A plugin install card lists the plugin's developer, description, apps (and which need connecting) and how many skills it brings.

The card is drawn from what the extension already read, without asking ChatGPT: a write needs the tool in the last `chatgpt_apps` list, and a plugin install or uninstall needs `chatgpt_plugin_info` first. Otherwise the call is refused before you are asked, with that hint to the assistant.

An approved change runs once per hub request id, also after the extension restarts. Results are kept only in memory (they can hold mail or orders); the data directory keeps just the ids.

## Apps that need OAuth

Installing a plugin whose apps use OAuth answers with a `connect_url` (`https://chatgpt.com/plugins/<id>`). The assistant writes it as a link: open it in a browser where you are signed in to ChatGPT, click **Connect** once, and tell the assistant, which lists the tools again.

## Data and logs

- The extension's log lines (in the hub's log) hold the method, the path without its query, the status and the time of each request, sign-in steps, and once per host where plugin packages come from (with a warning when `permissions.network` does not list that host, so downloads are refused in the sandbox). Never a token, a query, an argument or a result.
- The data directory holds downloaded plugin packages (`plugins/<name>/<version>/`, extracted with path checks: no absolute paths, no `..`, no links, at most 64 MB and 5,000 files) and `done-requests.json` (request ids only).
- Plugin skills and what apps answer are written by others. The skill tells the assistant to treat them as data.

## Troubleshooting

| What you see | Why, and what to do |
| --- | --- |
| The tools are missing; the connector says 还没有登录 ChatGPT | Sign in from the extension's details. |
| 「ChatGPT 登录失效了」 | The refresh was refused: signed out elsewhere, revoked, or too old. Sign in again. |
| Sign-in says the code expired | Start again; enter the code within its time. |
| A tool answers `HTTP 451` or `HTTP 403` | ChatGPT refused the request from the hub's network or without a valid sign-in. Check the sign-in first; if it stays, the hub's network path to chatgpt.com is being refused. |
| 「could not reach ChatGPT」 | The hub cannot reach chatgpt.com (in the sandbox: through its egress proxy, and the extension's details show egress counts and the last denied host). |
| An answer that does not decode | ChatGPT's backend changed. Update the extension. |
| 「is over 32 MB」 or 「not passed on」 | The tool answered more than one call can carry. Ask for fewer items or a narrower filter or time range. |

## Tests

`go -C extensions/chatgpt-apps test ./...` runs the program in-process against the SDK's fake hub and a loopback fake of ChatGPT (`fake_test.go`): the device-code sign-in, single-use refresh tokens with refreshes in parallel, a refused refresh, a refused access token, event-stream answers, reads, long results returned byte for byte (synthetic mail listings over 200 KB, as structured content and as text) and the bounds that refuse rather than cut, the read-only rule (including tools marked wrongly), the write card and running a write once per request id, plugin search, info, install with its package and skills, uninstall, a package with a path out of it, and that no token, mail address or result reaches the log. `ui_test.go` draws the plugin pages the same way and checks every view as the hub would (`exttest.CheckView`): signed out, the marketplace, search, load more, a plugin's page, installing and uninstalling from it, the installed list (with the swipe only for UI version 6), the connected apps and their tools, and a page when ChatGPT fails. Writes are tested only there; on a real account check reads only.
