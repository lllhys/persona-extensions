import { Renderer } from "@persona/extension/ui";
import type { Page, UIRequest } from "@persona/extension";
import { page } from "../src/page.tsx";
import { loadPage, readState, transition } from "../src/page-model.ts";
import { perform, preview } from "../src/root.tsx";
import { GitHubError } from "../src/client.ts";
import type { GitHubService } from "../src/service.ts";
import type { WriteReceipts } from "../src/connector.ts";
/** Regression-only model adapter. It is not registered or exported by the runtime Program. */
export function snapshotPage(service: GitHubService, receipts: WriteReceipts): Page {
  const render = async (req: UIRequest) => {
    const state = readState(req.state);
    if (state.authEpoch !== undefined && state.authEpoch !== service.client.epoch || state.pv && state.authEpoch === undefined) throw new GitHubError("Account changed since this snapshot", "auth_changed");
    state.authEpoch = service.client.epoch;
    const loaded = await loadPage(service, state, AbortSignal.timeout(15_000), req.locale);
    if (loaded.kind === "detail" && loaded.files.length) state.head = loaded.pull?.head.sha;
    if (state.authEpoch !== service.client.epoch) throw new GitHubError("Account changed during snapshot read", "auth_changed");
    const renderer = new Renderer({ authorize: () => false });
    try { return structuredClone(renderer.mount(page({ state, loaded, locale: req.locale, uiVersion: req.ui_version, action: (id, options) => ({ id, ...options }) })).view); }
    finally { renderer.dispose(); }
  };
  return { render, action: async req => {
    let state = readState(req.state); const action = req.action ?? "", values = req.values ?? {};
    if (state.authEpoch !== undefined && state.authEpoch !== service.client.epoch) throw new GitHubError("Account changed since this snapshot", "auth_changed");
    if (action.startsWith("commit:")) return perform(service, receipts, state, action, values, AbortSignal.timeout(15_000)) as never;
    if (action === "load_diff" && (req.ui_version ?? 0) >= 5) {
      const view = await render({ ...req, state: JSON.stringify(transition(state, action, values)) });
      return { items: view.sections.flatMap(section => section.items).filter(item => item.type === "diff" && item.path === values.path) };
    }
    if (action === "preview") state = await preview(service, state, values, AbortSignal.timeout(15_000));
    else if (["send", "context", "mark_viewed", "resolve", "unresolve", "discard_review", "rerun", "rerun_all"].includes(action)) {
      const out = await perform(service, receipts, state, action, values, AbortSignal.timeout(15_000));
      if (action === "context") return out as never;
      if (action === "send") state = transition(state, "cancel");
    } else {
      if (action === "refresh") service.client.invalidate(state.r);
      if (action === "file" && state.v === "repo") {
        const content = await service.content(state.r!, values.path ?? "", state.ref, AbortSignal.timeout(15_000));
        state = transition(state, action, { ...values, kind: Array.isArray(content) ? "dir" : "file" });
      } else state = transition(state, action, values);
    }
    return { view: await render({ ...req, state: JSON.stringify(state) }) };
  } };
}
