import { callback, useLoadEffect, useState, type Node } from "@persona/extension/ui";
import { encodeState, loadPage, page, readState, transition, type Loaded } from "./page.tsx";
import type { V2EX } from "./service.ts";
export interface RootOptions { service: V2EX; locale?: string; tz?: string; uiVersion?: number; state?: unknown }
const comparable = (loaded: Loaded) => JSON.stringify(loaded, (_key, value) => typeof value === "bigint" ? value.toString() : value);
export function Root(options: RootOptions): Node {
  const [state, setState] = useState(() => readState(options.state, options.service));
  const [request, setRequest] = useState({ revision: 0, refresh: false });
  const [result, setResult] = useState<{ service: V2EX; identity: string; loaded: Loaded } | undefined>(undefined);
  const identity = `${state.l}:${state.t}:${request.revision}`;
  useLoadEffect(async signal => {
    const loaded = await loadPage(options.service, state, request.refresh, signal);
    // A cached resume cannot invalidate the callback table for an unchanged tree.
    if (!signal.aborted) {
      setResult(previous => previous?.service === options.service && previous.identity === identity && comparable(previous.loaded) === comparable(loaded)
        ? previous : { service: options.service, identity, loaded });
      // Refresh is an action, not a persistent cache policy. Keep it pending only while cancelled.
      setRequest(previous => previous.revision === request.revision && previous.refresh ? { ...previous, refresh: false } : previous);
    }
  }, [options.service, identity]);
  return page({ ...options, state, loaded: result?.service === options.service && result.identity === identity ? result.loaded : { kind: "loading" },
    action: (id, next, title) => {
      if (next && (options.uiVersion ?? 0) >= 3) {
        const push = { ui: "topics", state: encodeState(next), ...(title ? { title } : {}) };
        // The host opens another owner-bound session. Keep the parent list/filter untouched.
        return callback(() => ({ push }), { push });
      }
      return callback(values => {
        if (id === "refresh") setRequest(previous => ({ revision: previous.revision + 1, refresh: true }));
        else {
          setState(previous => transition(previous, id, options.service, values));
          setRequest(previous => previous.refresh ? { ...previous, refresh: false } : previous);
        }
      });
    } });
}
export function root(options: RootOptions): Node { return <Root key="v2ex-root" {...options} />; }
