import { callback, useLoadEffect, useState, type Node } from "@persona/extension/ui";
import { encodeState, loadPage, page, readState, transition, type Loaded, type PageState } from "./page.tsx";
import type { LarkBridge } from "./service.ts";
export interface RootOptions { service: LarkBridge; locale?: string; uiVersion?: number; state?: unknown }
export function Root(options: RootOptions): Node {
  const [state, setState] = useState<PageState>(() => readState(options.state));
  const [refresh, setRefresh] = useState(0);
  const [result, setResult] = useState<{ service: LarkBridge; identity: string; loaded: Loaded } | undefined>(undefined);
  const identity = `${JSON.stringify(state)}:${refresh}`;
  useLoadEffect(async signal => {
    const loaded = await loadPage(options.service, state, signal);
    if (!signal.aborted) setResult(previous => previous?.service === options.service && previous.identity === identity && JSON.stringify(previous.loaded) === JSON.stringify(loaded) ? previous : { service: options.service, identity, loaded });
  }, [options.service, identity]);
  return page({ ...options, state, loaded: result?.service === options.service && result.identity === identity ? result.loaded : { kind: "loading" },
    action: (id, next, title) => {
      if (next && (options.uiVersion ?? 0) >= 3) {
        const push = { ui: "larkbridge", state: encodeState(next), ...(title ? { title } : {}) };
        return callback(() => { setState(next); return { push }; }, { push });
      }
      return callback(values => {
        if (id === "refresh") setRefresh(previous => previous + 1);
        else if (next) setState(next);
        else setState(previous => transition(previous, id, values, options.locale));
      }, { submit: true });
    } });
}
export function root(options: RootOptions): Node { return <Root key="larkbridge-root" {...options} />; }
