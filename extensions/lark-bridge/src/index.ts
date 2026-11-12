import { componentPage, type Program } from "@persona/extension";
import released from "../persona-extension.json";
import type { HostCaller } from "./model.ts";
import { LarkBridge } from "./service.ts";
import { connector } from "./tools.ts";
import { root } from "./root.tsx";
export { LarkBridge } from "./service.ts";
export { connector, callTool, tools } from "./tools.ts";
export { Root, root } from "./root.tsx";
export { page, loadPage, readState, transition, encodeState } from "./page.tsx";
export { snapshotPage } from "./snapshot.ts";
export const sourceManifest = { ...released, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/lark-bridge.mjs", sha256: "" } } };
delete (sourceManifest as { icon?: string }).icon;
delete (sourceManifest as { replaces?: string }).replaces;
export function createProgram(): Program {
  let service: LarkBridge | undefined;
  let locale = "en";
  const current = () => { if (!service) throw new Error("Lark Bridge has not been initialized"); return service; };
  return {
    id: released.id, version: released.version, manifest: sourceManifest,
    init: (init, hub) => { service = new LarkBridge(hub); locale = init.hub?.locale ?? "en"; },
    namespaces: { connector: connector(current, () => locale) },
    ui: { larkbridge: componentPage(req => root({ service: current(), state: req.state, locale: req.locale, uiVersion: req.ui_version })) },
  };
}
export default createProgram();
