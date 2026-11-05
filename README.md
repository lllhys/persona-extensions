# Persona Extensions

[简体中文](README.zh-CN.md)

Public extensions for Persona: connect data sources, add assistant tools and skills, and contribute views rendered by the apps. This repository is also an extension market, with versioned packages published in GitHub Releases.

## Quick start

In the Persona app, open **Settings → Extensions → Extension markets → Add** and enter:

```text
github:nwverse/persona-extensions
```

Then choose an extension to install. This public market does not require a GitHub token.

On the hub's machine, you can register the same market with:

```sh
personad extension market add github:nwverse/persona-extensions
```

See [installation](docs/installation.md) for local packages, older hubs and extension-specific setup.

## Extensions

| Extension | Capabilities |
| --- | --- |
| [LINUX DO](docs/extensions/linuxdo.md) | Read public hot, daily, weekly and latest topics; filter by interests; read topics and popular replies in the app or through assistant tools. No account required. |
| [Telegram](docs/extensions/telegram.md) | Connect your own account: conversations, inbox, knowledge-base archives, assistant tools and the `tg` CLI. Writes require confirmation by default; credentials stay encrypted on the hub. |
| [Showcase](docs/extensions/showcase.md) | Local-only examples of every view component and state, widgets, shortcuts and shared screenshot inputs. |
| [Starter kit](docs/extensions/starter-kit.md) | A runtime-free example: a weekly-review skill, assistant instructions, quick prompts, inbox triage templates and a theme. |
| [Starter pack](docs/extensions/starter-kit.md#starter-pack) | An extension pack that installs starter-kit and LINUX DO together. |

## Documentation and contributing

- [Documentation index](docs/README.md)
- [Development guide](docs/development.md)
- [Manifest, configuration and protocol reference](docs/reference.md)
- [Release and market signing guide](docs/releases.md)
- [Contributing](CONTRIBUTING.md)

Documentation and manually maintained code comments default to English. The Chinese README is a corresponding translation; runtime localization and meaningful test fixtures are kept in their original language.
