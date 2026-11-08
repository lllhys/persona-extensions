import { Renderer } from "@persona/extension/ui";
import type { Page } from "@persona/extension";
import { page, readState, transition, loadPage } from "./page.tsx";
import { textsFor } from "./i18n.ts";
import type { LinuxDo } from "./service.ts";
/** Pure native View helper for tests. The main page's legacy HTTP fallback is
 * provided by the manager's owner-bound bridge, not a second UI contribution. */
export function snapshotPage(service: LinuxDo): Page {
  const render = async (req: Parameters<Page["render"]>[0], refresh = false) => {
    const state = readState(req.state, service), loaded = await loadPage(service, state, refresh);
    const renderer = new Renderer({ authorize: () => false });
    try { return structuredClone(renderer.mount(page({ service, state, loaded, locale: req.locale, uiVersion: req.ui_version, tz: req.tz })).view); }
    finally { renderer.dispose(); }
  };
  return { render, action: async req => {
    const state = transition(readState(req.state, service), req.action ?? "", req.values);
    return { view: await render({ ...req, state: JSON.stringify(state) }, req.action === "refresh"), ...(req.action === "refresh" ? { toast: textsFor(req.locale ?? service.locale).refreshed } : {}) };
  } };
}
