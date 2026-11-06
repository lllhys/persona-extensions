import type { Program } from "@persona/extension";
import manifest from "../persona-extension.json";
import { createCard, createWidget, createShortcut, snapshot } from "./ui.tsx";

export { createPage, createCard, createWidget, createShortcut, ShowcaseRoot } from "./ui.tsx";
export { ShowcaseStore } from "./services.ts";

// Until navigation is served, system surfaces must not advertise links to it.
export const sourceContributions = {
  ui: manifest.contributes.ui.filter(ui => ui.id !== "showcase").map(({ opens: _opens, ...ui }) => ui),
  shortcuts: manifest.contributes.shortcuts.map(shortcut => {
    const { ui: _ui, ...item } = shortcut as typeof shortcut & { ui?: string };
    return item;
  }),
};

/** Source-host default export. Navigation is deliberately not advertised before the manager bridge lands. */
export function createProgram(sourceManifest?: unknown): Program {
  let language = "zh-Hans";
  return {
    id: manifest.id, version: manifest.version, manifest: sourceManifest,
    init(input) {
      language = input.hub?.locale || "zh-Hans";
      return sourceContributions;
    },
    ui: {
      "showcase-card": { render: req => snapshot(createCard(req.locale || language)) },
      "showcase-widget": { render: req => snapshot(createWidget(req.size || "medium", req.locale || language)) },
    },
    shortcuts: {
      "showcase-echo": req => createShortcut("run", req.contribution, req.values, req.locale || language),
      "showcase-count": req => createShortcut("run", req.contribution, req.values, req.locale || language),
    },
  };
}
export default createProgram();
