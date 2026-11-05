# Installation

[Documentation](README.md) · [Development](development.md)

## Register the public market

In the Persona app, open **Settings → Extensions → Extension markets → Add** and enter `github:nwverse/persona-extensions`. Choose an extension from the market to install.

The command-line equivalent, on the hub's machine, is:

```sh
personad extension market add github:nwverse/persona-extensions
```

This repository is public: no GitHub token is required. Tokens mentioned by the generic market CLI are for private markets, not this one.

The market indexes released extensions; it is not a directory listing of every example or open PR. Packages are GitHub Release assets with pinned SHA-256 hashes. The hub records the market's public signing key and verifies subsequent indexes. A missing or invalid signature, or a changed key, prevents installation and upgrades. See [market signing](releases.md#market-signing).

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

- [LINUX DO](extensions/linuxdo.md): no account; the hub must reach `linux.do`. The in-app reader fetches on the hub, while opening the original web page also requires network access on the device.
- [Telegram](extensions/telegram.md): sign in through **Settings → Connectors → Telegram**. The hub must reach Telegram data centers directly over TCP. Local or other non-official installation sources may require disabling the extension sandbox; follow the [Telegram network instructions](extensions/telegram.md#network-and-sandbox).
- [Bypass auto-allow](extensions/claude-bypass-allow.md): needs a hub that runs extension programs on computers (`node`). Nothing happens until you switch on a computer in its settings; it then edits that computer's Claude Code `settings.json`.

Do not paste credentials into repository files or installation examples. Configure accounts through the hub/app's protected credential flow.
