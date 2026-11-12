import { componentPage, type Program } from "@persona/extension";
import type { FetchAdapter } from "@persona/extension/ui";
import released from "../persona-extension.json";
import { StackExchange, type ServiceOptions } from "./service.ts";
import { connector } from "./tools.ts";
import { root } from "./root.tsx";
export { StackExchange, budget, limits, ReadError } from "./service.ts";
export { connector, callTool, tools } from "./tools.ts";
export { Root, root } from "./root.tsx";
export { page, loadPage, readState, transition, questionTransition, encodeState } from "./page.tsx";
export { snapshotPage } from "./snapshot.ts";
export type { PageState, PageProps, Loaded } from "./page.tsx";
export const sourceManifest = { ...released, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/stackexchange.mjs", sha256: "" } } };
// Source-owned catalog metadata is not part of the strict runtime manifest.
delete (sourceManifest as { icon?: string }).icon;
delete (sourceManifest as { replaces?: string }).replaces;
export function createProgram(fetch: FetchAdapter = (input, init) => globalThis.fetch(input, init), options: ServiceOptions = {}): Program {
  let service: StackExchange | undefined;
  const current = () => { if (!service) throw new Error("StackExchange has not been initialized"); return service; };
  return {
    id: released.id, version: released.version, manifest: sourceManifest,
    init: (init, _hub) => { service = new StackExchange(fetch, init.config, { ...options, locale: init.hub?.locale ?? options.locale }); },
    namespaces: { connector: connector(current) },
    ui: { answers: componentPage(req => root({ service: current(), state: req.state, locale: req.locale, uiVersion: req.ui_version })) },
  };
}
export default createProgram();
