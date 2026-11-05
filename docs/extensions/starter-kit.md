# Starter kit and starter pack

[Documentation](../README.md) · [Development](../development.md)

These examples demonstrate extensions that run **no program**. Both require a hub satisfying `engines.persona >=202611042338`; older hubs that parse manifests strictly cannot use these contribution types.

## Starter kit

[Source](../../extensions/starter-kit/) · [Manifest](../../extensions/starter-kit/persona-extension.json)

| Contribution | Example |
| --- | --- |
| `skills` | A weekly-review skill in `skills/weekly-review/`. |
| `prompts` | Assistant answer-style instructions in `assistant/answer-style.md`, plus three quick prompts for a daily summary, outstanding tasks and a short explanation. |
| `triage_templates` | Two inbox rule templates: mentions/direct messages, and work/life priorities. |
| `themes` | The `forest` theme with light and dark accent/positive colors. |

The manifest names the contribution files; there is no `runtime`. Assistant instructions, skills and prompt/template text are shipped runtime content, so they retain their original language in this documentation cleanup.

## Starter pack

[Source](../../extensions/starter-pack/) · [Manifest](../../extensions/starter-pack/persona-extension.json)

An `extension_pack` lists `starter-kit` and `linuxdo` for installation together from the market. The pack itself contains only the manifest, with no executable or separate assistant files. Dependency installation and host-version checks remain the hub's responsibility.

## Packaging and installation

From the repository root:

```sh
scripts/package.sh starter-kit
scripts/package.sh starter-pack
```

The packaging script writes the manifest under both names (`persona-extension.json` and the legacy `persona-plugin.json`, without `tags`), copies any `skills/` or `assistant/` directories, skips compilation without `runtime.command`, and does not leave an empty `bin/` directory. The legacy filename does not make the examples' new contribution types work on older hubs.

Install through the [public market](../installation.md). Review contributions in the hub's install preview and ensure the hub meets the declared engine requirement. These declarative examples are already in `main`; no source merge from an open PR is needed.
