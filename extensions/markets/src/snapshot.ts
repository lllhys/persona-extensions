import { Renderer } from "@persona/extension/ui";
import type { Page } from "@persona/extension";
import { page, loadPage, readPageState, transition, refreshDelay } from "./page.tsx";
import { actionError } from "./errors.ts";
import { translator } from "./locale.ts";
import { isCancelled } from "./http.ts";
import type { Markets } from "./service.ts";
/** Pure snapshot consumer of the same native JSX, not an extra navigation contribution. */
export function snapshotPage(service: Markets, card = false): Page {
  const render = async (req: Parameters<Page["render"]>[0], refresh = false) => {
    const state = readPageState(req.state, service);
    const loaded = await loadPage(service, state, refresh, card, req.ui_version, AbortSignal.timeout(60_000));
    const renderer = new Renderer({ authorize: () => false });
    try {
      const view = structuredClone(renderer.mount(page({ service, state, loaded, card, locale: req.locale, tz: req.tz, uiVersion: req.ui_version, action: (id, metadata) => ({ id, ...metadata }) })).view);
      // Only legacy snapshot consumers receive their original refresh hint; native streams use lifecycle timers.
      view.refresh_after = (refreshDelay(service, state, loaded, card) ?? 30_000) / 1000;
      return view;
    }
    finally { renderer.dispose(); }
  };
  return { render, action: async req => {
    const t = translator(req.locale);
    let next: Awaited<ReturnType<typeof transition>> = { state: readPageState(req.state, service), refresh: false };
    try { if (!card) next = await transition(service, next.state, req.action ?? "", req.values); }
    catch (error) { if (isCancelled(error)) throw error; next.toast = actionError(error); }
    return { view: await render({ ...req, state: JSON.stringify(next.state) }, next.refresh), ...(next.toast ? { toast: t(next.toast, { symbol: next.symbol ?? "" }) } : {}) };
  } };
}
