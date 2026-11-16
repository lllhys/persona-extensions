import { componentPage, type Hub, type Program } from "@persona/extension";
import type { FetchAdapter } from "@persona/extension/ui";
import released from "../persona-extension.json";
import { Domains, type ServiceOptions } from "./service.ts";
import { connector } from "./tools.ts";
import { root } from "./root.tsx";
import { DomainHistory, MemoryHistory, type HistoryStorage } from "./store.ts";
export { Domains, budget, limits, ReadError } from "./service.ts";
export { connector, callTool, tools } from "./tools.ts";
export { Root, root } from "./root.tsx";
export { page, loadPage, readState, transition, encodeState } from "./page.tsx";
export { snapshotPage } from "./snapshot.ts";
export { DomainHistory, MemoryHistory, decodeHistory, encodeHistory } from "./store.ts";
export type { PageState, PageProps, Loaded } from "./page.tsx";
export const sourceManifest = { ...released, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/domains.mjs", sha256: "" } } };
delete (sourceManifest as { icon?: string }).icon;
delete (sourceManifest as { replaces?: string }).replaces;
export function createProgram(fetch: FetchAdapter = (input, init) => globalThis.fetch(input, init), options: ServiceOptions = {}, history?: HistoryStorage): Program {
  let service: Domains | undefined;
  let recent: HistoryStorage | undefined;
  const current = () => { if (!service || !recent) throw new Error("Domains has not been initialized"); return { service, history: recent }; };
  return {
    id: released.id, version: released.version, manifest: sourceManifest,
    init: async (init, hub: Hub) => {
      service = new Domains(fetch, init.config, { ...options, locale: init.hub?.locale ?? options.locale });
      recent = history ?? new DomainHistory(hub);
      if (!history) await recent.load();
    },
    namespaces: { connector: connector(() => current().service, () => current().history) },
    ui: { lookup: componentPage(req => root({ service: current().service, history: current().history, state: req.state, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })) },
  };
}
export default createProgram();
