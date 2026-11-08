import { componentPage, ExtError, withExactParams, type Program, type Init, type Hub } from "@persona/extension";
import { GitHubAuth } from "./auth.ts";
import { GitHubClient } from "./client.ts";
import { GitHubService } from "./service.ts";
import { InboxPoller } from "./inbox.ts";
import { ArchiveReader } from "./archive.ts";
import { GitHubConnector } from "./connector.ts";
import { root } from "./root.tsx";
import type { Config, Fetch } from "./model.ts";
export { GitHubAuth, missingScopes } from "./auth.ts";
export { GitHubClient, GitHubError, bounds } from "./client.ts";
export { GitHubService } from "./service.ts";
export { InboxPoller } from "./inbox.ts";
export { ArchiveReader } from "./archive.ts";
export { GitHubConnector, WriteReceipts, writeArgs } from "./connector.ts";
export { root, Root } from "./root.tsx";
/** Injectable options are for fixtures, never a replacement transport inside the guest runtime. */
export function createProgram(fetch: Fetch = (input, init) => globalThis.fetch(input, init), options: { background?: boolean; now?: () => number } = {}): Program {
  let current: { auth: GitHubAuth; service: GitHubService; connector: GitHubConnector; inbox: InboxPoller; activePages: Set<string>; hub: Hub } | undefined;
  const get = () => { if (!current) throw new ExtError("GitHub has not been initialized", "not_initialized"); return current; };
  const active = (init: Init, kind: string, id: string): boolean => !init.active || !(kind in init.active) || (init.active[kind] ?? []).includes(id);
  let pollTimer: ReturnType<typeof setTimeout> | undefined, authTimer: ReturnType<typeof setTimeout> | undefined, badgeTimer: ReturnType<typeof setTimeout> | undefined;
  const later = (run: () => void, delay: number) => { const timer = setTimeout(run, delay); (timer as unknown as { unref?: () => void }).unref?.(); return timer; };
  const start = () => {
    const c = get();
    const check = async () => { if (c.hub.closed) return; await c.auth.check(); if (!c.hub.closed) authTimer = later(() => void check(), c.auth.status.state === "ok" ? 1800_000 : 60_000); };
    const poll = async () => { if (c.hub.closed || !c.connector.active) return; let wait = 60_000; try { wait = await c.inbox.poll(); } catch {} if (!c.hub.closed) pollTimer = later(() => void poll(), wait); };
    const badge = async () => { if (c.hub.closed) return; if (c.activePages.has("github") && c.auth.status.state === "ok" && !c.service.client.low()) { try { const result = await c.service.client.json<{ total_count: number }>("/search/issues?q=is%3Apr%20is%3Aopen%20review-requested%3A%40me&per_page=1"); c.hub.badge("github", result.total_count ? String(result.total_count) : ""); } catch {} } if (!c.hub.closed) badgeTimer = later(() => void badge(), 600_000); };
    void check().then(() => { void poll(); void badge(); });
  };
  return {
    id: "github", version: "0.4.18",
    init: (init, hub) => {
      for (const timer of [pollTimer, authTimer, badgeTimer]) if (timer) clearTimeout(timer);
      // This connector declares no plaintext secret. Its legacy device state is read only from the encrypted host store.
      if (Object.keys(init.secrets ?? {}).length) throw new ExtError("GitHub uses the connector's encrypted secret store, not declared plaintext credentials", "invalid_params");
      const config = (init.config ?? {}) as Config, now = options.now ?? Date.now, auth = new GitHubAuth(hub, config, now, init.hub?.locale ?? "en"), client = new GitHubClient(fetch, config, auth, now); auth.client = client;
      const service = new GitHubService(client), inbox = new InboxPoller(service, auth, config, now, auth.locale), archive = new ArchiveReader(service, auth, now, auth.locale);
      current = { auth, service, inbox, connector: new GitHubConnector(auth, service, inbox, archive, active(init, "connectors", "github")), activePages: new Set(["github"].filter(id => active(init, "ui", id))), hub };
      if (options.background !== false && (current.connector.active || current.activePages.size > 0)) start();
    },
    namespaces: { connector: withExactParams((method, params, call) => get().connector.handle(method, params, call)) },
    ui: {
      github: componentPage(req => { const c = get(); if (!c.activePages.has("github")) throw new ExtError("GitHub page is not active", "no_contribution"); return root({ service: c.service, receipts: c.connector.receipts, locale: req.locale, uiVersion: req.ui_version, state: req.state }); }),
    },
  };
}
export default createProgram();
