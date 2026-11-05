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

See the [`extensions/`](extensions/) directory for the available extensions. Each extension documents itself in its own directory, with longer guides under [`docs/extensions/`](docs/extensions/).

## Documentation and contributing

- [Documentation index](docs/README.md)
- [Development guide](docs/development.md)
- [Manifest, configuration and protocol reference](docs/reference.md)
- [Release and market signing guide](docs/releases.md)
- [Contributing](CONTRIBUTING.md)

Documentation and manually maintained code comments default to English. The Chinese README is a corresponding translation; runtime localization and meaningful test fixtures are kept in their original language.
