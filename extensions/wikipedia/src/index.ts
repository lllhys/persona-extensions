import { componentPage, type Program } from "@persona/extension";
import type { FetchAdapter } from "@persona/extension/ui";
import released from "../persona-extension.json";
import { Wikipedia, type ServiceOptions } from "./service.ts";
import { connector } from "./tools.ts";
import { root } from "./root.tsx";
export { Wikipedia, budget, limits, ReadError } from "./service.ts";
export { connector, callTool, tools } from "./tools.ts";
export { Root, root } from "./root.tsx";
export { page, loadPage, readState, transition, encodeState } from "./page.tsx";
export { snapshotPage } from "./snapshot.ts";
export type { PageState, PageProps, Loaded } from "./page.tsx";
// Package metadata carries the final artifact digest; the guest cannot embed it itself.
export const sourceManifest = { ...released, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/wikipedia.mjs", sha256: "" } } };
/** Only the trusted host installs the default grant-checked fetch. Adapters are for synthetic tests. */
export function createProgram(fetch: FetchAdapter = (input, init) => globalThis.fetch(input, init), options: ServiceOptions = {}): Program {
  let service: Wikipedia | undefined;
  const current = () => { if (!service) throw new Error("Wikipedia has not been initialized"); return service; };
  return {
    id: released.id, version: released.version, manifest: sourceManifest,
    init: (init, _hub) => { service = new Wikipedia(fetch, init.config, { ...options, locale: init.hub?.locale ?? options.locale, timezone: init.hub?.timezone ?? options.timezone }); },
    namespaces: { connector: connector(current) },
    ui: { today: componentPage(req => root({ service: current(), state: req.state, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })) },
  };
}
export default createProgram();
