import { callback, useLoadEffect, useState, type Node } from "@persona/extension/ui";
import { loadPage, page, readState, transition, type Loaded } from "./page.tsx";
import { ReadError, type LinuxDo } from "./service.ts";
import { textsFor } from "./i18n.ts";
export interface RootOptions { service: LinuxDo; state?: unknown; locale?: string; uiVersion?: number; tz?: string }
const sameError = (a: unknown, b: unknown) => a === b || (a instanceof ReadError && b instanceof ReadError && a.code === b.code && a.status === b.status);
function sameLoaded(a: Loaded, b: Loaded) {
  if (a.kind === "list" && b.kind === "list") return a.listing.value === b.listing.value && a.listing.at === b.listing.at && sameError(a.listing.stale, b.listing.stale);
  if (a.kind === "topic" && b.kind === "topic") return a.thread.value === b.thread.value && a.thread.at === b.thread.at && sameError(a.thread.stale, b.thread.stale);
  return a.kind === "error" && b.kind === "error" ? sameError(a.error, b.error) : a.kind === "loading" && b.kind === "loading";
}
export function Root(options: RootOptions): Node {
  const [state, setState] = useState(() => readState(options.state, options.service));
  const [request, setRequest] = useState({ revision: 0, refresh: false });
  const [result, setResult] = useState<{ service: LinuxDo; identity: string; loaded: Loaded } | undefined>(undefined);
  const identity = `${state.l}:${state.t}:${request.revision}`;
  useLoadEffect(async signal => {
    const loaded = await loadPage(options.service, state, request.refresh, signal);
    if (!signal.aborted) {
      // Resume checks the shared cache, not a repeated forced refresh. Equivalent
      // data does not publish another revision and invalidate saved callback tables.
      if (request.refresh) setRequest(previous => previous.revision === request.revision ? { ...previous, refresh: false } : previous);
      setResult(previous => previous?.service === options.service && previous.identity === identity && sameLoaded(previous.loaded, loaded) ? previous : { service: options.service, identity, loaded });
    }
  }, [options.service, identity]);
  return page({ ...options, state, loaded: result?.service === options.service && result.identity === identity ? result.loaded : { kind: "loading" }, action: id => callback(values => {
    if (id === "refresh") {
      setRequest(previous => ({ revision: previous.revision + 1, refresh: true }));
      return { toast: textsFor(options.locale ?? options.service.locale).refreshed };
    }
    else {
      setState(previous => transition(previous, id, values));
      setRequest(previous => previous.refresh ? { ...previous, refresh: false } : previous);
    }
  }) });
}
export const root = (options: RootOptions): Node => <Root key="linuxdo-root" {...options} />;
