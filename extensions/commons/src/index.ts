import { componentPage, type Program } from "@persona/extension";
import type { FetchAdapter } from "@persona/extension/ui";
import released from "../persona-extension.json";
import { Commons, type ServiceOptions } from "./service.ts";
import { connector } from "./tools.ts";
import { root } from "./root.tsx";
export { Commons, budget, limits, ReadError } from "./service.ts";
export { connector, callTool, tools } from "./tools.ts";
export { Root, root } from "./root.tsx";
export { page, loadPage, readState, transition, encodeState } from "./page.tsx";
export { snapshotPage } from "./snapshot.ts";
export type { PageState, PageProps, Loaded } from "./page.tsx";
export const sourceManifest = { ...released, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/commons.mjs", sha256: "" } } };
// Source-owned catalog metadata is not part of the strict runtime manifest.
delete (sourceManifest as { icon?: string }).icon;
delete (sourceManifest as { replaces?: string }).replaces;
export function createProgram(fetch: FetchAdapter = (input, init) => globalThis.fetch(input, init), options: ServiceOptions = {}): Program {
  let service: Commons | undefined;
  const current = () => { if (!service) throw new Error("Commons has not been initialized"); return service; };
  return {
    id: released.id, version: released.version, manifest: sourceManifest,
    init: (init, _hub) => { service = new Commons(fetch, init.config, { ...options, locale: init.hub?.locale ?? options.locale }); },
    namespaces: { connector: connector(current) },
    ui: { search: componentPage(req => root({ service: current(), state: req.state, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })) },
  };
}
export default createProgram();
