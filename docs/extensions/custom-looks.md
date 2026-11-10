# Custom looks (自建形象)

[Documentation](../README.md) · [Source](../../extensions/custom-looks/)

A declarative extension with five `looks` (Persona's docs/extensions.md "Avatars"): `claude` (Claude), `gpt` (GPT), `baobao` (包包), `dafeiyu` (大肥鱼) and `doudou` (奶娃). Each has square avatar PNGs (64–512), colors for light and dark, and an APNG status dot for each of the five states.

## Where they come from

They were avatar packs in a hub's `personas/<id>/` directory. `looks/<id>/` keeps each pack's files as they were, its `persona.json` and the `preview.png` source image included; the manifest names the avatar and dot files and copies the name, subtitle, colors, `safe` and each state's `fps`. `persona.json`'s `frames` has no place in a look: the dots are APNGs that carry their frame counts, and the apps read those.

## Ids and the hub's own packs

The ids are the packs' ids, so the look a hub has chosen, and the look on each device, carry on when the extension takes over.

- A hub pack of the same id (`personas/<id>/`) wins: while it is there, the picker lists that look once, from the pack, and the extension's copy is left out.
- Remove a pack and the extension's look of that id takes its place; the chosen look stays chosen.
- Uninstalling the extension while the packs are there leaves the choice alone. Without the packs, a chosen look of this extension goes back to the default (团子), as for any looks extension.

## Setup

1. Install Custom looks from this repository's market in the app (**Settings → Extensions**), or install a local package as in [Installation](../installation.md).
2. Check that the five looks are in Settings › 形象 and that the chosen one did not change.
3. Optionally remove the hub's own packs of these ids afterwards; the extension then supplies them.
