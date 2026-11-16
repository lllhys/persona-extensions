import { callback, useLoadEffect, useState, type Node } from "@persona/extension/ui";
import { encodeState, loadPage, page, readState, transition, type Loaded } from "./page.tsx";
import type { Domains } from "./service.ts";
import type { HistoryStorage } from "./store.ts";
export interface RootOptions { service: Domains; history: HistoryStorage; locale?: string; tz?: string; uiVersion?: number; state?: unknown }
export function Root(options: RootOptions): Node {
  const [state, setState] = useState(() => readState(options.state));
  const [refresh, setRefresh] = useState(0);
  const [result, setResult] = useState<{ service: Domains; identity: string; loaded: Loaded } | undefined>(undefined);
  const identity = `${state.v}:${state.q}:${refresh}`;
  useLoadEffect(async signal => {
    const loaded = await loadPage(options.service, options.history, state, refresh > 0, signal);
    if (!signal.aborted) setResult(previous => previous?.service === options.service && previous.identity === identity && JSON.stringify(previous.loaded) === JSON.stringify(loaded)
      ? previous : { service: options.service, identity, loaded });
  }, [options.service, identity]);
  return page({ ...options, state, loaded: result?.service === options.service && result.identity === identity ? result.loaded : { kind: "loading" },
    action: (id, next, title) => {
      if (next && (options.uiVersion ?? 0) >= 3) {
        const push = { ui: "lookup", state: encodeState(next), ...(title ? { title } : {}) };
        return callback(() => ({ push }), { push });
      }
      return callback(async values => {
        if (id === "refresh") { setRefresh(previous => previous + 1); return; }
        if (id === "clear") { await options.history.clear(); setState({ v: "history", q: "", message: "cleared" }); return; }
        if (id.startsWith("domain:")) {
          const domain = id.slice(7);
          if (result?.loaded.kind === "history" && result.loaded.domains.includes(domain)) setState({ v: "result", q: domain });
          return;
        }
        setState(previous => transition(previous, id, values));
      }, { submit: true });
    } });
}
export function root(options: RootOptions): Node { return <Root key="domains-root" {...options} />; }
