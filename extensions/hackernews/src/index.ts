import { componentPage, type Program } from "@persona/extension";
import type { FetchAdapter } from "@persona/extension/ui";
import { HackerNews } from "./service.ts";
import { connector } from "./tools.ts";
import { root } from "./root.tsx";
import { snapshotPage } from "./snapshot.ts";
import type { Config } from "./model.ts";
export { HackerNews, ReadError, errorText, limits } from "./service.ts";
export { connector, callTool, tools } from "./tools.ts";
export { Root, root } from "./root.tsx";
export { page, loadPage, readState, transition } from "./page.tsx";
export { services, snapshotPage } from "./snapshot.ts";
export type { RootOptions } from "./root.tsx";
export type { PageState, PageProps, Loaded } from "./page.tsx";

/** One hub-owned service/cache shared by tools, connector, sessions and system snapshots.
 * Production supplies the trusted Deno adapter's immutable, grant-checked fetch.
 * Injection is only for synthetic fixtures; this module never starts a transport.
 */
export function createProgram(fetch: FetchAdapter = (input, init) => globalThis.fetch(input, init)): Program {
  let service: HackerNews | undefined;
  const current = () => {
    if (!service) throw new Error("Hacker News has not been initialized");
    return service;
  };
  return {
    id: "hackernews", version: "0.1.2",
    init: init => { service = new HackerNews(fetch, (init.config ?? {}) as Config); },
    namespaces: { connector: (method, params) => connector(current())(method, params) },
    ui: {
      stories: componentPage(req => root({ service: current(), state: req.state, locale: req.locale, uiVersion: req.ui_version })),
      "stories-snapshot": {
        render: req => snapshotPage(current()).render(req),
        action: req => snapshotPage(current()).action!(req),
      },
    },
  };
}
export default createProgram();
