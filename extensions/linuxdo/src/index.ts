import { componentPage, type Program } from "@persona/extension";
import type { FetchAdapter } from "@persona/extension/ui";
import released from "../persona-extension.json";
// The host validates the final bundle hash from the package manifest before
// evaluation. A Program cannot embed its own final SHA-256; omit only that field.
const manifest = { ...released, runtime: { mode: "deno", source: { entry: "extensions/linuxdo/src/index.ts", bundle: "extensions/linuxdo/build/linuxdo.mjs" } } };
import { LinuxDo, errorText } from "./service.ts";
import { InputError } from "./errors.ts";
import type { SiteOptions } from "./site.ts";
import { connector } from "./tools.ts";
import { root } from "./root.tsx";
export { LinuxDo, ReadError, limits, errorText } from "./service.ts";
export { root, Root } from "./root.tsx";
export { page, readState, transition, loadPage } from "./page.tsx";
export { connector, callTool, tools, toolsFor } from "./tools.ts";
export { snapshotPage } from "./snapshot.ts";
/** The source guest is evaluated only by the trusted Deno adapter, which installs
 * immutable grant-checked fetch before module evaluation. Capture that capability;
 * synthetic fixture injection is the only alternate transport, not an ambient fallback.
 */
const trustedFetch: FetchAdapter = globalThis.fetch.bind(globalThis);
export function createProgram(fetch: FetchAdapter = trustedFetch, fixtureOptions: SiteOptions = {}): Program {
  let service: LinuxDo | undefined;
  const current = () => { if (!service) throw new Error("LINUX DO is not initialized"); return service; };
  return {
    id: manifest.id, version: manifest.version, manifest,
    init: init => {
      try { service = new LinuxDo(fetch, init.config, init.hub?.timezone || "Local", init.hub?.locale || "", fixtureOptions); }
      catch (error) { if (error instanceof InputError) throw new Error(errorText(error, init.hub?.locale)); throw error; }
      return manifest.contributes;
    },
    namespaces: { connector: connector(current) },
    // Legacy HTTP clients mount this same contribution through the manager's
    // owner-bound bridge. Direct SDK ui.render/ui.action intentionally reject it.
    ui: { hot: componentPage(req => root({ service: current(), state: req.state, locale: req.locale, uiVersion: req.ui_version, tz: req.tz })) },
  };
}
export default createProgram();
