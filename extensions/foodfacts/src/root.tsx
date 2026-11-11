import { callback, useLoadEffect, useState, type Node } from "@persona/extension/ui";
import { encodeState, loadPage, page, readState, transition, type Loaded } from "./page.tsx";
import type { FoodFacts } from "./service.ts";
export interface RootOptions { service: FoodFacts; locale?: string; uiVersion?: number; state?: unknown }
export function Root(options: RootOptions): Node {
  const [state, setState] = useState(() => readState(options.state));
  const [refresh, setRefresh] = useState(0);
  const [result, setResult] = useState<{ service: FoodFacts; identity: string; loaded: Loaded } | undefined>(undefined);
  const identity = `${state.q}:${state.found}:${refresh}`;
  useLoadEffect(async signal => {
    const loaded = await loadPage(options.service, state, refresh > 0, signal);
    if (!signal.aborted) setResult(previous => previous?.service === options.service && previous.identity === identity && JSON.stringify(previous.loaded) === JSON.stringify(loaded)
      ? previous : { service: options.service, identity, loaded });
  }, [options.service, identity]);
  return page({ ...options, state, loaded: result?.service === options.service && result.identity === identity ? result.loaded : { kind: "loading" },
    action: (id, next, title) => {
      if (next && (options.uiVersion ?? 0) >= 3) {
        const push = { ui: "products", state: encodeState(next), ...(title ? { title } : {}) };
        return callback(() => ({ push }), { push });
      }
      return callback(values => {
        if (id === "refresh") setRefresh(previous => previous + 1);
        else setState(previous => transition(previous, id, values));
      }, { submit: true });
    } });
}
export function root(options: RootOptions): Node { return <Root key="foodfacts-root" {...options} />; }
