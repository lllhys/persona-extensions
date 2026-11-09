import { Renderer } from "@persona/extension/ui";
import type { Page } from "@persona/extension";
import { encodeState, loadPage, page, readState, transition } from "./page.tsx";
import type { V2EX } from "./service.ts";
/** Pure full-view export for tests; legacy HTTP compatibility belongs to the manager's session bridge. */
export function snapshotPage(service: V2EX): Page {
  const render = async (req: Parameters<Page["render"]>[0], refresh = false) => {
    const state = readState(req.state, service), loaded = await loadPage(service, state, refresh);
    const renderer = new Renderer({ authorize: () => false });
    try { return structuredClone(renderer.mount(page({ service, state, loaded, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })).view); }
    finally { renderer.dispose(); }
  };
  return { render, action: async req => {
    const state = transition(readState(req.state, service), req.action ?? "", service, req.values);
    return { view: await render({ ...req, state: encodeState(state) }, req.action === "refresh") };
  } };
}
