import type { UIStreamActionResult } from "./wire.ts";
import type { Hub, UIRequest } from "./index.ts";
import type { Node } from "./components.ts";
import { Renderer, type RendererOptions, type Snapshot, type UIEvent } from "./renderer.ts";

/** Root factories run once per manager-owned session. Closures never leave this process. */
export interface ComponentPage {
  readonly componentPage: true;
  readonly root: (request: UIRequest) => Node | Promise<Node>;
  readonly options: Omit<RendererOptions, "authorize" | "onRender" | "transformResult">;
  /** Construct in the page bundle's module instance: element symbols alone cannot share hook context. */
  readonly createRenderer: (host: Pick<RendererOptions, "authorize" | "onRender" | "transformResult">) => Renderer;
}
export function componentPage(root: ComponentPage["root"], options: ComponentPage["options"] = {}): ComponentPage {
  return { componentPage: true, root, options, createRenderer: host => new Renderer({
    ...options, ...host,
    // The manager evicts after 64 events and rejects those old wire revisions;
    // retaining more native results cannot serve an authenticated retry.
    limits: { ...options.limits, events: Math.min(options.limits?.events ?? 64, 64) },
  }) };
}
export function isComponentPage(page: unknown): page is ComponentPage {
  return !!page && (page as ComponentPage).componentPage === true;
}
export async function componentSnapshot(page: ComponentPage, request: UIRequest) {
 const renderer=page.createRenderer({authorize:()=>false});
 try {renderer.mount(await page.root(request));return renderer.current().view;}
 finally {renderer.dispose();}
}

const RESULT_MOST = 64 << 10;
// Allow the native 20,000-line maximum plus bounded result/container metadata.
const RESULT_NODES_MOST = 32768;
const encoder = new TextEncoder();
/** Copy before caching: guest objects/closures cannot survive as unbounded event receipts.
 * Exact native shape, saved-action and URL policy are independently checked by the manager. */
function pageResult(value: unknown): Readonly<UIStreamActionResult> {
  if (value === undefined || value === null) return Object.freeze({});
  let nodes = 0, strings = 0;
  const ancestors = new Set<object>();
  const string = (text: string) => {
    strings += text.length;
    if (strings > RESULT_MOST) throw new Error("page result byte limit");
    return text;
  };
  const copy = (item: unknown, depth: number): unknown => {
    if (++nodes > RESULT_NODES_MOST || depth > 16) throw new Error("page result structure limit");
    if (typeof item === "string") return string(item);
    if (item === null || typeof item === "boolean") return item;
    if (typeof item === "number" && Number.isFinite(item)) return item;
    if (!item || typeof item !== "object" || ancestors.has(item)) throw new Error("page results require acyclic JSON");
    ancestors.add(item);
    try {
      if (Array.isArray(item)) {
        if (item.length > RESULT_NODES_MOST - nodes) throw new Error("page result structure limit");
        const out: unknown[] = [];
        for (let i = 0; i < item.length; i++) out.push(copy(item[i], depth + 1));
        return Object.freeze(out);
      }
      if (Object.getPrototypeOf(item) !== Object.prototype && Object.getPrototypeOf(item) !== null) throw new Error("page results require plain JSON");
      const out: Record<string, unknown> = Object.create(null);
      for (const key in item) if (Object.prototype.hasOwnProperty.call(item, key)) {
        string(key); out[key] = copy((item as Record<string, unknown>)[key], depth + 1);
      }
      return Object.freeze(out);
    } finally { ancestors.delete(item); }
  };
  if (typeof value !== "object" || Array.isArray(value)) throw new Error("page results require a native result object");
  const result = copy(value, 0) as UIStreamActionResult;
  if (encoder.encode(JSON.stringify(result)).length > RESULT_MOST) throw new Error("page result byte limit");
  return result;
}

interface Mounted { renderer: Renderer; contribution: string; ticket?: string; busy: boolean }

/** The process bridge trusts only calls from its host, not values inside an event.
 * A ticket is host-issued after saved-native-action gates, scoped to this invocation.
 * The bridge does not interpret a source authorize function as user approval. */
export class PageBridge {
  private sessions = new Map<string, Mounted>();
  constructor(private pages: Record<string, unknown>, private hub: Hub) {}
  async call(method: string, params: any): Promise<unknown> {
    const id = params?.session;
    if (typeof id !== "string" || !id || id.length > 128) throw new Error("invalid page session");
    if (method === "dispose") { this.sessions.get(id)?.renderer.dispose(); this.sessions.delete(id); return {}; }
    if (method === "mount") {
      if (this.sessions.has(id) || this.sessions.size >= 256) throw new Error("page session quota");
      const page = this.pages[params.params?.contribution];
      if (!isComponentPage(page)) throw new Error("not a component page");
      const entry = { contribution: params.params.contribution, busy: true } as Mounted;
      if (typeof page.createRenderer !== "function") throw new Error("component page requires its bundled renderer factory");
      const renderer = entry.renderer = page.createRenderer({
        authorize: (_action, _event, signal) => !!entry.ticket && !signal.aborted,
        transformResult: pageResult,
        onRender: snapshot => { if (!entry.busy) this.hub.notify("ui.page.snapshot", { session: id, snapshot }); },
      });
      // Reserve before awaiting a factory, so concurrent mounts cannot evade totals.
      this.sessions.set(id, entry);
      try { renderer.mount(await page.root({ ...params.params, hub: this.hub })); entry.busy = false; return renderer.current(); }
      catch (error) { renderer.dispose(); this.sessions.delete(id); throw error; }
    }
    const entry = this.sessions.get(id);
    if (!entry) throw new Error("unknown page session");
    // Visibility changes abort UI-only work even while a callback is awaiting.
    if (method === "suspend") { entry.renderer.suspend(); return {}; }
    if (entry.busy) throw new Error("page session busy");
    entry.busy = true;
    try {
      if (method === "snapshot") return entry.renderer.current();
      if (method === "resume") return entry.renderer.suspended ? entry.renderer.resume() : entry.renderer.current();
      if (method === "event") {
        if (typeof params.ticket !== "string" || !params.ticket) throw new Error("missing host action ticket");
        entry.ticket = params.ticket;
        let result: unknown;
        try { result = await entry.renderer.event(params.interaction as UIEvent); }
        catch { return { snapshot: entry.renderer.current(), status: "ambiguous" }; }
        return { snapshot: entry.renderer.current(), status: "accepted", result };
      }
      throw new Error("unknown page method");
    } finally { entry.ticket = undefined; entry.busy = false; }
  }
  close(): void { for (const entry of this.sessions.values()) entry.renderer.dispose(); this.sessions.clear(); }
}
