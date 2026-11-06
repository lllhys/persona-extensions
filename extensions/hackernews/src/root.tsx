import { callback, useEffect, useState, type Node } from "@persona/extension/ui";
import { loadPage, page, readState, transition, type Loaded } from "./page.tsx";
import type { HackerNews } from "./service.ts";
export interface RootOptions { service: HackerNews; locale?: string; uiVersion?: number; state?: unknown }
/** Server hooks only: no transport, ambient fetch, client-side code or speculative bridge API. */
export function Root(options: RootOptions): Node {
  const [state, setState] = useState(() => readState(options.state, options.service));
  const [request, setRequest] = useState({ revision: 0, refresh: false });
  const [result, setResult] = useState<{ service: HackerNews; identity: string; loaded: Loaded } | undefined>(undefined);
  const identity = `${state.l}:${state.s}:${request.revision}`;
  useEffect(async signal => {
    const loaded = await loadPage(options.service, state, request.refresh, signal);
    if (!signal.aborted) setResult({ service: options.service, identity, loaded });
  }, [options.service, identity]);
  return page({ ...options, state, loaded: result?.service === options.service && result.identity === identity ? result.loaded : { kind: "loading" },
    action: id => callback(values => {
      if (id === "refresh") setRequest(previous => ({ revision: previous.revision + 1, refresh: true }));
      else {
        setState(previous => transition(previous, id, values));
        setRequest(previous => previous.refresh ? { ...previous, refresh: false } : previous);
      }
    }) });
}
export function root(options: RootOptions): Node { return <Root key="hackernews-root" {...options} />; }
