import { componentPage, type Hub, type Program } from "@persona/extension";
import type { FetchAdapter } from "@persona/extension/ui";
import released from "../persona-extension.json";
import { Podcasts, type ServiceOptions } from "./service.ts";
import { connector } from "./tools.ts";
import { root } from "./root.tsx";
import { ListeningStore, MemoryStore, type Storage } from "./store.ts";
export { Podcasts, budget, limits, ReadError } from "./service.ts";
export { connector, callTool, tools } from "./tools.ts";
export { Root, root } from "./root.tsx";
export { page, loadPage, readState, transition, encodeState } from "./page.tsx";
export { snapshotPage } from "./snapshot.ts";
export { ListeningStore, MemoryStore, decodeList, encodeList } from "./store.ts";
export type { PageState, PageProps, Loaded } from "./page.tsx";
export const sourceManifest = { ...released, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/podcasts.mjs", sha256: "" } } };
delete (sourceManifest as { icon?: string }).icon;
delete (sourceManifest as { replaces?: string }).replaces;
export function createProgram(fetch: FetchAdapter = (input, init) => globalThis.fetch(input, init), options: ServiceOptions = {}, store?: Storage): Program {
  let service: Podcasts | undefined;
  let listening: Storage | undefined;
  const current = () => { if (!service || !listening) throw new Error("Podcasts has not been initialized"); return { service, store: listening }; };
  return {
    id: released.id, version: released.version, manifest: sourceManifest,
    init: async (init, hub: Hub) => {
      service = new Podcasts(fetch, init.config, { ...options, locale: init.hub?.locale ?? options.locale });
      listening = store ?? new ListeningStore(hub);
      if (!store) await listening.load();
    },
    namespaces: { connector: connector(() => current().service) },
    ui: { discover: componentPage(req => root({ service: current().service, store: current().store, state: req.state, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })) },
  };
}
export default createProgram();
