import { callback, useLoadEffect, useState, type Node } from "@persona/extension/ui";
import { encodeState, loadPage, page, readState, transition, type Loaded } from "./page.tsx";
import type { Podcasts } from "./service.ts";
import type { Storage } from "./store.ts";
export interface RootOptions { service: Podcasts; store: Storage; locale?: string; tz?: string; uiVersion?: number; state?: unknown }
export function Root(options: RootOptions): Node {
  const [state, setState] = useState(() => readState(options.state));
  const [refresh, setRefresh] = useState(0);
  const [result, setResult] = useState<{ service: Podcasts; identity: string; loaded: Loaded } | undefined>(undefined);
  const identity = `${state.v}:${state.q}:${state.k}:${refresh}`;
  useLoadEffect(async signal => {
    const loaded = await loadPage(options.service, options.store, state, refresh > 0, signal);
    if (!signal.aborted) setResult(previous => previous?.service === options.service && previous.identity === identity && JSON.stringify(previous.loaded) === JSON.stringify(loaded)
      ? previous : { service: options.service, identity, loaded });
  }, [options.service, identity]);
  return page({ ...options, state, loaded: result?.service === options.service && result.identity === identity ? result.loaded : { kind: "loading" },
    action: (id, next, title) => {
      if (next && (options.uiVersion ?? 0) >= 3) {
        const push = { ui: "discover", state: encodeState(next), ...(title ? { title } : {}) };
        return callback(() => ({ push }), { push });
      }
      return callback(async values => {
        if (id === "refresh") { setRefresh(previous => previous + 1); return; }
        if (id === "back" && state.v === "show" && (options.uiVersion ?? 0) >= 3) return { pop: true };
        if (id.startsWith("show:")) {
          const show = result?.loaded.kind === "search" ? result.loaded.result.shows.find(show => show.id === Number(id.slice(5)))
            : result?.loaded.kind === "library" ? result.loaded.shows.find(show => show.id === Number(id.slice(5))) : undefined;
          if (show) setState({ v: "show", q: state.q, k: show.id, from: state.v === "library" ? "library" : undefined, b: show });
          return;
        }
        if (id.startsWith("save:") || id.startsWith("remove:")) {
          const show = result?.loaded.kind === "show" ? result.loaded.result.show : state.b;
          if (!show || show.id !== Number(id.slice(id.indexOf(":") + 1))) return;
          if (id.startsWith("save:")) await options.store.save(show); else await options.store.remove(show.id);
          setState(previous => ({ ...previous, message: id.startsWith("save:") ? "saved" : "removed" }));
          return;
        }
        setState(previous => transition(previous, id, values));
      }, { submit: true });
    } });
}
export function root(options: RootOptions): Node { return <Root key="podcasts-root" {...options} />; }
