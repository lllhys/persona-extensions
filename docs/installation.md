# Installation

[Documentation](README.md) · [Development](development.md)

## Register the public market

In the Persona app, open **Settings → Extensions → Extension markets → Add** and enter `github:nwverse/persona-extensions`. Choose an extension from the market to install.

The command-line equivalent, on the hub's machine, is:

```sh
personad extension market add github:nwverse/persona-extensions
```

This repository is public: no GitHub token is required. Tokens mentioned by the generic market CLI are for private markets, not this one.

The market lists published extensions; it is not a directory listing of every example or open PR. The single catalog embeds an Ed25519 signature over its raw UTF-8 payload. The official hub verifies the fixed approved public key; a missing or invalid signature prevents installation and upgrades. OCI packages on GHCR are pinned by manifest digest, while catalog SHA-256 and size refer to the archive blob. The initial converted catalog retains frozen cleaned Release references until authorized bootstrap publication finishes. Upgrade the hub for embedded catalog/OCI support and the matching official pin before switching. See [market signing](releases.md#market-signing).

If a lookup fails, first check that the extension has a release in this market and that the hub meets its manifest's `engines` requirements. If verification fails, do not bypass it or silently trust a replacement key; ask the market maintainer to resolve the index/signature problem.

## Install a local package

Run from the repository root with the toolchain described in the [development guide](development.md):

```sh
scripts/package.sh linuxdo
```

This writes `dist/linuxdo-<version>.tar.gz` and `dist/linuxdo-<version>.tar.gz.sha256`. Replace `<version>` below with the version in [the manifest](../extensions/linuxdo/persona-extension.json), then copy the archive to the hub's machine and run:

```sh
personad extension install file:/path/to/linuxdo-<version>.tar.gz
```

The package includes compiled binaries for the platforms listed in its manifest; the hub does not need a Go toolchain or an extra runtime. Packaging is local; it does not create a release or register the package in the market.

## Older hubs

Hubs from before the Plugin → Extension rename use `personad plugin …` instead of `personad extension …`, for example:

```sh
personad plugin install file:/path/to/linuxdo-<version>.tar.gz
```

Both `persona-extension.json` and the compatible `persona-plugin.json` are shipped, generated from the one source manifest with the same contents. New hubs prefer the former; old hubs read the latter. Some extensions also declare a minimum hub version in `engines.persona`; renaming the command does not bypass that requirement.

Legacy protocol names and UI placements, such as `plugin_protocol` and `plugin_page`, are intentional compatibility identifiers. See [reference](reference.md#compatibility).

## Extension-specific setup

- [GitHub](extensions/github.md): sign in through **Settings → Connectors → GitHub**, either by sharing a computer's `gh` login (that computer must be online and the extension uses its `gh_login` permission) or through the device flow. The hub must reach `github.com`, `api.github.com` and GitHub's avatar and CI-log hosts.
- [Home Assistant](extensions/homeassistant.md): fill in the address and a long-lived access token in its settings; the hub must reach Home Assistant. A local package starts sandboxed and needs `personad extension sandbox homeassistant off`, see [Network and sandbox](extensions/homeassistant.md#network-and-sandbox).
- [LINUX DO](extensions/linuxdo.md): no account; the hub must reach `linux.do`. The in-app reader fetches on the hub, while opening the original web page also requires network access on the device.
- [V2EX](extensions/v2ex.md): no account; the hub must reach `www.v2ex.com`. The in-app reader fetches on the hub, while opening the original web page also requires network access on the device.
- [Feishu Sidebar](extensions/feishu-sidebar.md): no account; builds a Feishu applink to the configured Persona web address without calling Feishu or storing credentials.
- [Lark Bridge](extensions/lark-bridge.md): reuse the existing host-managed `lark-cli` sign-in; enable `lark.cli.extensions` in the hub configuration. Read-only typed chat, message, people and document lookups. No second QR login, arbitrary commands or message sending.
- [RSSHub](extensions/rsshub.md): no account. The hub must reach the RSSHub instance (`rsshub.app` by default, which may refuse some networks; a self-hosted instance is recommended) and any feed addresses you subscribe to. A self-hosted instance on another host, or other feed hosts, need the sandbox off (`personad extension sandbox rsshub off`), the default for the official market; an instance on the hub machine works in the sandbox through `host.persona.internal`.
- [Termwise](extensions/termwise.md): no account. Vocabulary mode works offline from the embedded glossary. Translation mode calls `api.mymemory.translated.net` from the hub and shows a failure when that call does not succeed.
- [Search as Code](extensions/search-as-code.md): no account and no network; session transcripts and saved recipes stay local. For the assistant tools, put `search-as-code` on `PATH` or in `~/bin` on the computer that runs the assistant (or set `PERSONA_SEARCH_AS_CODE_BIN`); it shares the hub extension's data directory on the same computer, or set `PERSONA_SEARCH_AS_CODE_DATA`.
- [Markets](extensions/markets.md): no account; the hub must reach `www.okx.com`, and for stocks `qt.gtimg.cn`, `smartbox.gtimg.cn`, `web.ifzq.gtimg.cn` and `ifzq.gtimg.cn`.
- [Telegram](extensions/telegram.md): sign in through **Settings → Connectors → Telegram**. The hub must reach Telegram data centers directly over TCP. Local or other non-official installation sources may require disabling the extension sandbox; follow the [Telegram network instructions](extensions/telegram.md#network-and-sandbox).
- [X](extensions/x.md): either way is optional. **X API**: your own X developer app with pay-per-use credits; sign in through **Settings → Connectors → X** with its API Key and Secret and your account's Access Token and Secret; the hub must reach `api.x.com`, and reading the timeline spends your credits up to the daily post budget. **Grok**: install the official `grok` CLI on the hub machine and run `grok login` with a SuperGrok or X Premium+ subscription (sign-ins expire after 7 days); this needs the extension sandbox off, which is the default for the official market.
- [Bypass auto-allow](extensions/agent-bypass-allow.md): needs a hub that runs extension programs on computers (`node`). Nothing happens until you switch on a computer in its settings; it then edits that computer's Claude Code `settings.json`.
- [ATS](../extensions/ats/README.md): no account. Needs a hub that runs extension programs on computers (`node`) and a coding agent (Claude Code) on the chosen computers. Nothing runs until you choose computers and the repository directories tasks may run in, in its settings.
- [Disklessly](extensions/disklessly.md): no account. Probes the local machine directly and reaches remote machines over your existing OpenSSH setup (`~/.ssh/config` and `~/.persona/config.json`). Put the extension binary on `PATH` or in `~/bin` (or set `PERSONA_DISKLESSLY_BIN`) on the assistant's computer to enable `disklessly mcp`.
- [mnemo](../extensions/mnemo/README.md): no account. Install [mnemo](https://github.com/szupzj18/mnemo#install) on the computer that runs the assistant, where Persona finds it on `PATH` or in `~/bin` (or through `PERSONA_MNEMO_BIN`). Only the Claude assistant gets its tools.
- [Spaces](extensions/spaces.md): no account and no network. The page and the overview card work as soon as the package is installed. For the assistant's tools, the `spaces` binary from the package (`bin/spaces-<platform>`) must be on `PATH` or in `~/bin` under the name `spaces`, or be named by `PERSONA_SPACES_BIN`; set that to `off` to leave the tools out. See [Making the assistant's `spaces` command findable](extensions/spaces.md#making-the-assistants-spaces-command-findable).
- [Household](extensions/household.md): no account and no network. The 家庭管家 page, the overview card and the reminders work as soon as the package is installed; the reminders need the hub's `notify` permission (and `todos.write` for to-dos). For the assistant's tools, the `household` binary from the package (`bin/household-<platform>`) must be on `PATH` or in `~/bin` under the name `household`, or be named by `PERSONA_HOUSEHOLD_BIN`; set that to `off` to leave the tools out. The steps are the same as for [spaces](extensions/spaces.md#making-the-assistants-spaces-command-findable), with the other name.
- [Custom looks](extensions/custom-looks.md#setup): no settings. A hub pack of the same id keeps showing until it is removed; the chosen look does not change either way.
- [Currency and units](../extensions/currency/README.md): no account. The hub reaches `api.frankfurter.dev` for dated ECB reference rates; local unit conversions need no network.
- [Stack Exchange](../extensions/stackexchange/README.md): no account. The hub reaches `api.stackexchange.com` for anonymous question searches and answer retrieval; read-only, with caching, bounded responses and original attribution links.
- [npm Packages](../extensions/npmpackages/README.md): no account. The hub reaches `registry.npmjs.org` and `api.npmjs.org` for read-only package metadata and download counts. No package is downloaded or installed.
- [Rust Crates](../extensions/crates/README.md): no account. The hub reaches `crates.io` for anonymous read-only crate metadata. No crate is downloaded, built or installed.
- [NuGet Packages](../extensions/nuget/README.md): no account. The hub reaches `azuresearch-usnc.nuget.org` and `api.nuget.org` for anonymous read-only package metadata, following NuGet's official redirect to `nuget.azure.cn`. No package is downloaded, restored or installed.
- [RubyGems](../extensions/rubygems/README.md): no account. The hub reaches `rubygems.org` for anonymous read-only gem metadata and runtime dependencies. No gem is downloaded, built or installed.
- [Docker Hub](../extensions/dockerhub/README.md): no account. The hub reaches `hub.docker.com` for anonymous read-only repository and tag metadata. No image is pulled or run, and no container daemon is contacted.
- [ChatGPT apps](extensions/chatgpt-apps.md#sign-in): your own ChatGPT account. Sign in from the extension's details with a device code (the sign-in page says Codex). The hub must reach `chatgpt.com`, `auth.openai.com` and `*.oaiusercontent.com`. It uses ChatGPT's non-public web backend; see the guide before installing.
- [1Password](extensions/onepassword.md#setting-it-up): a 1Password service account with read access to the vaults Persona may look into. Paste its token (`ops_…`) into the extension's settings; the hub checks it with 1Password before saving it. The hub must reach `*.1password.com` (or `.ca` / `.eu`, where the account is). Or, without a service account: pick a Mac or Linux computer with the 1Password app and `op` (CLI integration on) in the extension's settings ([With op on one of your computers](extensions/onepassword.md#with-op-on-one-of-your-computers)).

Do not paste credentials into repository files or installation examples. Configure accounts through the hub/app's protected credential flow.
