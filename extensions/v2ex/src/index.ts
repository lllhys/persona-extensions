import { componentPage, withExactInit, type Program } from "@persona/extension";
import type { FetchAdapter } from "@persona/extension/ui";
import released from "../persona-extension.json";
import { V2EX, type ServiceOptions } from "./service.ts";
import { connector } from "./tools.ts";
import { root } from "./root.tsx";
import { parseJSON } from "./model.ts";
export { V2EX, budget, limits, ReadError } from "./service.ts";
export { connector, callTool, tools } from "./tools.ts";
export { Root, root } from "./root.tsx";
export { page, loadPage, readState, transition, encodeState } from "./page.tsx";
export { snapshotPage } from "./snapshot.ts";
export type { RootOptions } from "./root.tsx";
export type { PageState, PageProps, Loaded } from "./page.tsx";
// The package manifest stamps the final bundle hash; a bundle cannot embed its own
// digest. Protocol metadata describes this source profile, never the Go executable.
export const sourceManifest = { ...released, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/v2ex.mjs" } } };
/** This default is the immutable, grant-checked fetch installed by the trusted Deno host.
 * No transport, process launcher, filesystem access or permission escape is implemented here.
 * An alternative adapter and timing options are for synthetic fixtures only. */
export function createProgram(fetch: FetchAdapter = (input, init) => globalThis.fetch(input, init), options: ServiceOptions = {}): Program {
  let service: V2EX | undefined;
  const current = () => { if (!service) throw new Error("V2EX has not been initialized"); return service; };
  return {
    id: released.id, version: released.version,
    manifest: sourceManifest,
    init: withExactInit((init, _hub, call) => {
      // Only an actual initialize RPC carries original tokens. Direct probes keep
      // their supplied object; never serialize it to fabricate raw metadata.
      const raw = call?.params?.field("config")?.raw;
      const config = raw === undefined ? init.config : parseJSON(raw);
      service = new V2EX(fetch, config, { ...options, timezone: init.hub?.timezone ?? options.timezone, locale: init.hub?.locale ?? options.locale });
    }),
    namespaces: { connector: connector(current) },
    ui: {
      topics: componentPage(req => root({ service: current(), state: req.state, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })),
    },
  };
}
export default createProgram();
