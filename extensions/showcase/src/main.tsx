import { componentPage, decodeState, type Program } from "@persona/extension";
import manifest from "../persona-extension.json";
import { createRoot, createCard, createWidget, createShortcut, snapshot, type PageInput } from "./ui.tsx";
import { ShowcaseStore, type PageState } from "./services.ts";
import { configuredMedia } from "./media.ts";

export { createRoot, createCard, createWidget, createShortcut, ShowcaseRoot } from "./ui.tsx";
export { ShowcaseStore } from "./services.ts";

export const sourceContributions = manifest.contributes;

function pageState(request: { state?: string }): PageState {
  const decoded = decodeState<Record<string, unknown>>(request, {}), state: PageState = {};
  for (const key of ["p", "q", "kind", "id", "range", "answer"] as const) {
    if (typeof decoded[key] === "string") state[key] = decoded[key];
  }
  if (typeof decoded.shown === "number" && Number.isSafeInteger(decoded.shown) && decoded.shown > 0) state.shown = decoded.shown;
  return state;
}

/** Each host session gets its own root/hooks and the renderer from this bundle's SDK instance. */
export function createPage(store: ShowcaseStore, defaults: () => PageInput = () => ({})) {
  return componentPage(request => {
    const input = defaults();
    return createRoot(store, {
      ...input, state: pageState(request),
      language: request.locale || input.language,
      uiVersion: request.ui_version ?? 3, timeZone: request.tz || input.timeZone,
    });
  });
}

/** Unreleased source profile; the independently released Go package is not changed. */
export function createProgram(sourceManifest?: unknown, store = new ShowcaseStore()): Program {
  let input: PageInput = { language: "zh-Hans" };
  return {
    id: manifest.id, version: manifest.version, manifest: sourceManifest,
    init(init, hub) {
      input = { language: init.hub?.locale || "zh-Hans", timeZone: init.hub?.timezone, media: configuredMedia(init.config) };
      // Service data remains shared while the manager owns page visibility and subscriptions.
      store.subscribe(() => { if (hub.closed) store.dispose(); else hub.invalidate("showcase"); });
      return sourceContributions;
    },
    ui: {
      showcase: createPage(store, () => input),
      "showcase-card": { render: req => snapshot(createCard(req.locale || input.language)) },
      "showcase-widget": { render: req => snapshot(createWidget(req.size || "medium", req.locale || input.language)) },
    },
    shortcuts: {
      "showcase-echo": req => createShortcut("run", req.contribution, req.values, req.locale || input.language),
      "showcase-count": req => createShortcut("run", req.contribution, req.values, req.locale || input.language),
    },
  };
}
export default createProgram();
