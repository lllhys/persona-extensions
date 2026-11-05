# Documentation

[Project overview](../README.md) · [Contributing](../CONTRIBUTING.md)

## Using extensions

- [Installation](installation.md): register the public market, install local packages and handle older hubs.
- [LINUX DO](extensions/linuxdo.md): public topics, filtering, request limits and the app view.
- [Telegram](extensions/telegram.md): login, inbox, archives, network requirements, tools and the `tg` CLI.

## Developing and maintaining extensions

- [Development](development.md): repository layout, adding an extension, local checks and packaging.
- [Reference](reference.md): manifests, configuration, process protocol, SDK ownership and compatibility.
- [Releases and signing](releases.md): release automation, index format, verification and key management.

This repository documents its public extensions and build/release workflow. The [local reference](reference.md) links to the published SDK contracts and schemas; it does not rely on access to the host implementation repository.

## Example extensions

- [Showcase](extensions/showcase.md): a local-only component catalog, widget and shortcut examples, view snapshots and older-app fallbacks.
- [Starter kit and starter pack](extensions/starter-kit.md): runtime-free skills, assistant instructions, quick prompts, triage templates and a theme; a pack that installs starter-kit and LINUX DO together. These require a hub with declarative-contribution support (`engines.persona >=202611042338`).

These examples are available in `main` and the market. Keep both project READMEs and this index aligned when adding extensions.
