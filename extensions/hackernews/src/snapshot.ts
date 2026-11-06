import { Renderer, type FetchAdapter } from "@persona/extension/ui";
import type { Page } from "@persona/extension";
import { page, loadPage, readState, transition, type Loaded } from "./page.tsx";
import { HackerNews, ReadError } from "./service.ts";
import { connector } from "./tools.ts";
import type { Config } from "./model.ts";
/** APP fitted snapshots remain available through the same JSX, without mounting server callbacks. */
export function snapshotPage(service: HackerNews): Page {
  const render = async (req: Parameters<Page["render"]>[0], refresh = false) => {
    const state = readState(req.state, service);
    let loaded: Loaded;
    try { loaded = await loadPage(service, state, refresh, AbortSignal.timeout(15_000)); }
    catch (error) {
      if (!(error instanceof Error) || error.name !== "TimeoutError") throw error;
      loaded = { kind: "error", error: new ReadError("slow") };
    }
    const renderer = new Renderer({ authorize: () => false });
    // The legacy SDK rewrites push IDs for older clients; give it a mutable snapshot copy.
    try { return structuredClone(renderer.mount(page({ service, state, loaded, locale: req.locale, uiVersion: req.ui_version })).view); }
    finally { renderer.dispose(); }
  };
  return { render, action: async req => {
    const state = transition(readState(req.state, service), req.action ?? "", req.values);
    return { view: await render({ ...req, state: JSON.stringify(state) }, req.action === "refresh") };
  } };
}
/** Host integration pieces, deliberately not a runnable Deno/process entry point yet. */
export function services(fetch: FetchAdapter, config: Config = {}) {
  const service = new HackerNews(fetch, config);
  return { service, namespaces: { connector: connector(service) }, ui: { stories: snapshotPage(service) } };
}
