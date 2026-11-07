import { callback, useEffect, useState, type Node } from "@persona/extension/ui";
import { loadPage, page, readState, transition, type Loaded } from "./page.tsx";
import { ReadError, type HackerNews } from "./service.ts";
const sameItems = <T,>(a: T[], b: T[]) => a.length === b.length && a.every((item, i) => item === b[i]);
const sameFailure = (a: unknown, b: unknown) => a === b || (a instanceof ReadError && b instanceof ReadError && a.code === b.code && a.status === b.status);
function sameLoaded(a: Loaded, b: Loaded): boolean {
  if (a.kind === "list" && b.kind === "list") return a.listing.at === b.listing.at && a.listing.missed === b.listing.missed
    && sameFailure(a.listing.stale, b.listing.stale) && sameItems(a.listing.stories, b.listing.stories);
  if (a.kind === "story" && b.kind === "story") return a.thread.story === b.thread.story && a.thread.missed === b.thread.missed && sameItems(a.thread.comments, b.thread.comments);
  if (a.kind === "error" && b.kind === "error") return sameFailure(a.error, b.error);
  return a.kind === "loading" && b.kind === "loading";
}
export interface RootOptions { service: HackerNews; locale?: string; uiVersion?: number; state?: unknown }
/** Server hooks only: no transport, ambient fetch, client-side code or speculative bridge API. */
export function Root(options: RootOptions): Node {
  const [state, setState] = useState(() => readState(options.state, options.service));
  const [request, setRequest] = useState({ revision: 0, refresh: false });
  const [result, setResult] = useState<{ service: HackerNews; identity: string; loaded: Loaded } | undefined>(undefined);
  const identity = `${state.l}:${state.s}:${request.revision}`;
  useEffect(async signal => {
    const loaded = await loadPage(options.service, state, request.refresh, signal);
    // Resume reads the shared cache again. Do not invalidate a saved callback for
    // an equivalent result; genuinely changed data or a new request still publishes.
    if (!signal.aborted) setResult(previous => previous?.service === options.service && previous.identity === identity && sameLoaded(previous.loaded, loaded)
      ? previous : { service: options.service, identity, loaded });
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
