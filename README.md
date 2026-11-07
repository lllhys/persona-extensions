# Persona Extensions

[简体中文](README.zh-CN.md)

Public extensions for Persona: connect data sources, add assistant tools and skills, and contribute views rendered by the apps. This repository is also an extension market, with versioned packages published in GitHub Releases.

[![Persona Extensions promo: add the market with one line, then pick the extensions you need](docs/assets/promo-preview.webp)](#video-tour)

[Watch the video tour (with voice-over)](#video-tour)

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

## Video tour

https://github.com/user-attachments/assets/0198ecd2-1e5f-4ca1-95f4-0473b0488670

[Download the HD version and subtitles](https://github.com/nwverse/persona-extensions/releases/tag/promo-persona-extensions-v1)

## Extensions

See the [`extensions/`](extensions/) directory for the available extensions. Each extension documents itself in its own directory, with longer guides under [`docs/extensions/`](docs/extensions/).

## Documentation and contributing

- [Documentation index](docs/README.md)
- [Development guide](docs/development.md)
- [Manifest, configuration and protocol reference](docs/reference.md)
- [Release and market signing guide](docs/releases.md)
- [Contributing](CONTRIBUTING.md)

Documentation and manually maintained code comments default to English. The Chinese README is a corresponding translation; runtime localization and meaningful test fixtures are kept in their original language.
