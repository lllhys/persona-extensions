import { componentPage, type Program, type Hub } from "@persona/extension";
import { Markets, type ServiceOptions } from "./service.ts";
import { connector } from "./tools.ts";
import { root } from "./root.tsx";
import { snapshotPage } from "./snapshot.ts";
import { readPageState } from "./page.tsx";
import type { Config } from "./model.ts";
import released from "../persona-extension.json";
export { Markets } from "./service.ts";
export { ReadError } from "./http.ts";
export { connector, tools, callTool } from "./tools.ts";
export { Root, root } from "./root.tsx";
export { page, loadPage, transition, readPageState } from "./page.tsx";
export { snapshotPage } from "./snapshot.ts";
export { encodeState, decodeState, legacyState, parseStateJSON } from "./state.ts";
/** Injectable fixture seam; production uses only the trusted adapter's grant-checked fetch. */
export function services(hub: Pick<Hub, "call" | "notify" | "invalidate">, config: Config = {}, options: ServiceOptions = {}) {
  const service = new Markets(hub, config, options);
  return { service, namespaces: { connector: connector(service) }, ui: { watchlist: snapshotPage(service), card: snapshotPage(service, true) } };
}
export function createProgram(options: ServiceOptions = {}): Program {
  let service: Markets | undefined;
  const current = () => { if (!service) throw new Error("Markets has not been initialized"); return service; };
  return {
    id: released.id, version: released.version,
    init: async (init, hub) => {
      service?.dispose();
      const next = new Markets(hub, (init.config ?? {}) as Config, options);
      try { await next.initialize(); } catch (error) { next.dispose(); throw error; }
      service = next;
      return released.contributes;
    },
    namespaces: { connector: connector(current) },
    ui: {
      watchlist: componentPage(req => root({ service: current(), state: readPageState(req.state, current()), locale: req.locale, uiVersion: req.ui_version, tz: req.tz })),
      card: { render: req => snapshotPage(current(), true).render(req), action: req => snapshotPage(current(), true).action!(req) },
    },
  };
}
export default createProgram();
