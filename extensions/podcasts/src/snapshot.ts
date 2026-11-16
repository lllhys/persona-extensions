import { Renderer } from "@persona/extension/ui";
import type { Page } from "@persona/extension";
import { encodeState, loadPage, page, readState, transition } from "./page.tsx";
import type { Podcasts } from "./service.ts";
import type { Storage } from "./store.ts";
export function snapshotPage(service: Podcasts, store: Storage): Page {
  const render = async (req: Parameters<Page["render"]>[0], refresh = false) => {
    const state = readState(req.state), loaded = await loadPage(service, store, state, refresh);
    const renderer = new Renderer({ authorize: () => false });
    try { return structuredClone(renderer.mount(page({ service, store, state, loaded, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })).view); }
    finally { renderer.dispose(); }
  };
  return { render, action: async req => {
    const state = transition(readState(req.state), req.action ?? "", req.values as unknown);
    return { view: await render({ ...req, state: encodeState(state) }, req.action === "refresh") };
  } };
}
