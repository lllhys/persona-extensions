import { callback, useEffect, useLoadEffect, useInterval, useState, type Node } from "@persona/extension/ui";
import { page, loadPage, loading, readPageState, fingerprint, transition, refreshDelay, type Loaded } from "./page.tsx";
import { actionError } from "./errors.ts";
import { translator } from "./locale.ts";
import { isCancelled } from "./http.ts";
import type { Markets } from "./service.ts";
export interface RootOptions { service: Markets; locale?: string; uiVersion?: number; state?: unknown; tz?: string; card?: boolean }
/** A stable root owns navigation. The shared service owns persisted watchlist, alerts and caches. */
export function Root(options: RootOptions): Node {
  const [state, setState] = useState(() => readPageState(options.state, options.service));
  const [request, setRequest] = useState({ revision: 0, refresh: false });
  const [gate] = useState(() => ({ acting: false, dirty: false }));
  const [result, setResult] = useState<{ service: Markets; identity: string; loaded: Loaded } | undefined>(undefined);
  const identity = JSON.stringify([state, request.revision, options.uiVersion, options.card]);
  useEffect(() => options.service.subscribe(() => {
    // A write invalidation must not unmount the very callback completing that write.
    if (gate.acting) gate.dirty = true;
    else setRequest(old => ({ revision: old.revision + 1, refresh: false }));
  }), [options.service]);
  useLoadEffect(async signal => {
    try {
      const loaded = await loadPage(options.service, state, request.refresh, !!options.card, options.uiVersion, signal);
      // Reading an unchanged shared cache on resume must not expire the saved callback table.
      if (!signal.aborted) setResult(old => old?.service === options.service && old.identity === identity && fingerprint(old.loaded) === fingerprint(loaded)
        ? old : { service: options.service, identity, loaded });
    } catch (error) { if (!signal.aborted && !isCancelled(error)) throw error; }
  }, [options.service, identity]);
  const loaded = result?.service === options.service && result.identity === identity ? result.loaded : loading();
  useInterval(async signal => {
    // The SDK owns visibility, cancellation and the overlap guard. Poll without a Loading flash.
    if (gate.acting) return;
    try {
      const next = await loadPage(options.service, state, false, !!options.card, options.uiVersion, signal);
      if (!signal.aborted) setResult(old => old?.service === options.service && old.identity === identity && fingerprint(old.loaded) === fingerprint(next)
        ? old : { service: options.service, identity, loaded: next });
    } catch (error) { if (!signal.aborted && !isCancelled(error)) throw error; }
  }, refreshDelay(options.service, state, loaded, !!options.card));
  return page({ ...options, state, loaded,
    action: (id, metadata) => callback(async (values, context) => {
      const t = translator(options.locale);
      gate.acting = true;
      try {
        const next = await transition(options.service, state, id, values, context.signal);
        if (context.signal.aborted) return;
        setState(previous => JSON.stringify(previous) === JSON.stringify(next.state) ? previous : next.state);
        if (next.refresh) setRequest(old => ({ revision: old.revision + 1, refresh: true }));
        else setRequest(old => old.refresh ? { ...old, refresh: false } : old);
        return next.toast ? { toast: t(next.toast, { symbol: next.symbol ?? "" }) } : undefined;
      } catch (error) {
        if (context.signal.aborted || isCancelled(error)) throw error;
        return { toast: t(actionError(error)) };
      } finally {
        gate.acting = false;
        if (gate.dirty) { gate.dirty = false; setRequest(old => ({ revision: old.revision + 1, refresh: false })); }
      }
    }, metadata) });
}
export function root(options: RootOptions): Node { return <Root key="markets-root" {...options} />; }
