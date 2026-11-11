import { Renderer } from "@persona/extension/ui";
import type { Page } from "@persona/extension";
import { loadPage, page, readState } from "./page.tsx";
import type { CurrencyService } from "./service.ts";
export function snapshotPage(service: CurrencyService): Page {
  return { render: async req => {
    const state = readState(req.state);
    if (!state.converted) return structuredClone(new Renderer({ authorize: () => false }).mount(page({ service, state, loaded: { kind: "error", error: new Error("not converted") }, locale: req.locale, uiVersion: req.ui_version })).view);
    const loaded = await loadPage(service, state, false, req.signal);
    const renderer = new Renderer({ authorize: () => false });
    try { return structuredClone(renderer.mount(page({ service, state, loaded, locale: req.locale, uiVersion: req.ui_version })).view); }
    finally { renderer.dispose(); }
  } };
}
