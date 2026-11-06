import { callback, useEffect, useState, type Node } from "@persona/extension/ui";
import { loadPage, page, readState, transition, type Loaded } from "./page.tsx";
import type { HackerNews } from "./service.ts";
export interface RootOptions { service: HackerNews; locale?: string; uiVersion?: number; state?: unknown }
/** Server hooks only: no transport, ambient fetch, client-side code or speculative bridge API. */
export function Root(options: RootOptions): Node {
  const [state, setState] = useState(() => readState(options.state, options.service));
  const [revision, setRevision] = useState(0);
  const [result, setResult] = useState<{ identity: string; loaded: Loaded } | undefined>(undefined);
  const identity = `${state.l}:${state.s}:${revision}`;
  useEffect(async signal => {
    const loaded = await loadPage(options.service, state, revision > 0, signal);
    if (!signal.aborted) setResult({ identity, loaded });
  }, [options.service, identity]);
  return page({ ...options, state, loaded: result?.identity === identity ? result.loaded : { kind: "loading" },
    action: id => callback(values => {
      if (id === "refresh") setRevision(n => n + 1);
      else setState(previous => transition(previous, id, values));
    }) });
}
export function root(options: RootOptions): Node { return <Root key="hackernews-root" {...options} />; }
