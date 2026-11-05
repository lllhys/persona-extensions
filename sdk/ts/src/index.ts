// Persona extension SDK for TypeScript (docs/extensions.md「写扩展：SDK、模板和命令行」): an extension program's side of
// the process protocol, for Bun. Build the program with `bun build --compile` into one executable per platform:
// the package holds only that, and the hub needs no runtime.
//
//   import { main } from "@persona/extension";
//   main({ id: "example", version: "1.0.0", ui: { home: { render: () => ({ sections: [] }) } } });

import type * as wire from "./wire.ts";

export type * from "./wire.ts";
export { COMPONENT_TYPES, ERROR_CODES, TONES } from "./wire.ts";

/** The version of the process protocol this SDK speaks. */
export const PROTOCOL = 1;

/** An error on the wire: a message for people (never a secret or a message's body), and a code the hub acts on. */
export class ExtError extends Error {
  constructor(
    message: string,
    readonly code?: string,
    readonly data?: unknown,
  ) {
    super(message);
  }
}

export const noContribution = (id: string) => new ExtError(`no contribution ${id}`, "no_contribution");
export const unknownMethod = (method: string) => new ExtError(`unknown method ${method}`, "unknown_method");
export const unsupported = (why: string) => new ExtError(why, "unsupported");

/** Answers the calls of one namespace: method is without the namespace ("status" for connector.status). */
export type Handler = (method: string, params: any) => unknown | Promise<unknown>;

/** One ui contribution: what it draws, and what its buttons do. */
export interface Page {
  render(req: UIRequest): wire.View | Promise<wire.View>;
  /** Nothing (or an empty result) draws the page again, with req.state as the action left it. */
  action?(req: UIRequest): wire.ActionResult | void | Promise<wire.ActionResult | void>;
}

/** A ui.render or ui.action, and the hub to call while drawing. */
export interface UIRequest extends wire.UIParams {
  hub: Hub;
}

/** A shortcut.run: its params' values (strings), for whom; and the hub to call while running. */
export interface ShortcutRequest extends wire.ShortcutRunParams {
  hub: Hub;
}

/** Whether a page draws a widget (placement widget); its size is then the family: draw less, nothing to tap. */
export const isWidget = (req: { placement?: string }) => req.placement === "widget";

export interface Init extends wire.InitializeParams {
  /** The extension's own (id, version, directories), under whichever key the hub sent it. */
  info: wire.ExtensionInfo;
}

export interface Program {
  id: string;
  version: string;
  /** persona-extension.json, answered to manifest (an extension running elsewhere must have it). */
  manifest?: unknown;
  /** Called once with what the hub sent; says what the program contributes this run (undefined: all of it). */
  init?(init: Init, hub: Hub): Record<string, unknown> | void | Promise<Record<string, unknown> | void>;
  /** Handlers by namespace ("connector", "quota", "event", …). */
  namespaces?: Record<string, Handler>;
  /** Pages by ui contribution id (widgets too: placement widget, drawn per family; see isWidget). */
  ui?: Record<string, Page>;
  /** Shortcuts by contribution id: shortcut.run goes to them (docs/extensions.md「系统级位置」). */
  shortcuts?: Record<string, (req: ShortcutRequest) => wire.ShortcutResult | void | Promise<wire.ShortcutResult | void>>;
  /** If set, served on the hub's socket (runtime.http in the manifest) by main(). */
  fetch?(req: Request): Response | Promise<Response>;
}

/** Reads a page's state, which the program wrote with encodeState. */
export function decodeState<T>(req: { state?: string }, fallback: T): T {
  if (!req.state) return fallback;
  try {
    return { ...fallback, ...JSON.parse(req.state) };
  } catch {
    return fallback;
  }
}

/** Writes a page's state (at most 16 KB). */
export const encodeState = (v: unknown) => JSON.stringify(v);

const PUSH_PREFIX = "ext.push.";

/** An action that opens a page on top of this one: ui.render of ui ("" this one) with state, title shown until it is
 * drawn. id names it for ui.action in apps before UI version 3 (the SDK answers those itself). */
export function open(id: string, ui: string, state: unknown, title?: string): wire.Action {
  return { id, push: { ...(ui ? { ui } : {}), state: encodeState(state), ...(title ? { title } : {}) } };
}

interface Frame {
  id?: string;
  method?: string;
  event?: string;
  params?: unknown;
  result?: unknown;
  error?: { message: string; code?: string; data?: unknown };
}

type Write = (line: string) => void;

/** The hub, for a program's calls and events. */
export class Hub {
  private next = 0;
  private pending = new Map<string, { resolve(v: unknown): void; reject(e: unknown): void }>();
  closed = false;

  constructor(private write: Write) {}

  /** Calls a method of the hub's (connector.secrets.load, quota.http, …). */
  call<T = unknown>(method: string, params: unknown = {}): Promise<T> {
    if (this.closed) return Promise.reject(new ExtError("the hub has gone"));
    const id = `p${++this.next}`;
    return new Promise<T>((resolve, reject) => {
      this.pending.set(id, { resolve: resolve as (v: unknown) => void, reject });
      this.write(JSON.stringify({ id, method, params }));
    });
  }

  /** Sends the hub an event (connector.changed, …). */
  notify(event: string, params: unknown = {}) {
    if (!this.closed) this.write(JSON.stringify({ event, params }));
  }

  /** The badge on a nav_page's entry; empty text clears it. */
  badge(contribution: string, text: string, tone?: wire.Tone) {
    this.notify("ui.badge", { contribution, text, ...(tone ? { tone } : {}) });
  }

  /** Tells the apps a ui contribution's data changed ("" is all of this extension's): the pages on screen draw again. */
  invalidate(contribution = "") {
    this.notify("ui.invalidate", contribution ? { contribution } : {});
  }

  /** @internal */
  answer(f: Frame) {
    const p = this.pending.get(f.id!);
    if (!p) return;
    this.pending.delete(f.id!);
    if (f.error) p.reject(new ExtError(f.error.message, f.error.code, f.error.data));
    else p.resolve(f.result);
  }

  /** @internal */
  close() {
    this.closed = true;
    for (const p of this.pending.values()) p.reject(new ExtError("the hub has gone"));
    this.pending.clear();
  }
}

/** One side of the protocol: feed it lines, it writes answers with write. Returned for tests and other transports. */
export function connect(prog: Program, write: Write) {
  const hub = new Hub(write);
  let initialized = false;
  let contributes: Record<string, unknown> | undefined;
  const inflight = new Set<Promise<unknown>>();
  let initializing: Promise<void> | undefined;

  async function request(method: string, params: any): Promise<unknown> {
    if (method === "initialize") {
      if (params?.protocol !== PROTOCOL) {
        throw new ExtError(`protocol ${params?.protocol}, not ${PROTOCOL}`, "wrong_protocol");
      }
      if (!initialized) {
        const init: Init = { ...params, info: params.extension ?? params.plugin ?? { id: "", version: "" } };
        initializing ??= Promise.resolve(prog.init?.(init, hub)).then((c) => {
          contributes = c || undefined;
          initialized = true;
        });
        try {
          await initializing;
        } catch (e) {
          initializing = undefined;
          throw e;
        }
      }
      return { protocol: PROTOCOL, id: prog.id, version: prog.version, ...(contributes ? { contributes } : {}) };
    }
    if (method === "manifest") {
      if (prog.manifest === undefined) throw new ExtError("no manifest here: it is in the package", "unknown_method");
      return answeredManifest(prog.manifest);
    }
    // A call sent right behind initialize (without waiting for its answer) waits for it.
    if (!initialized && initializing) await initializing.catch(() => {});
    if (!initialized) throw new ExtError("not initialized", "not_initialized");
    if (method === "ping") return {};
    const dot = method.indexOf(".");
    if (dot < 0) throw unknownMethod(method);
    const ns = method.slice(0, dot);
    const rest = method.slice(dot + 1);
    const h = prog.namespaces?.[ns];
    if (h) return h(rest, params ?? {});
    if (ns === "ui" && prog.ui) return ui(rest, params ?? {});
    if (ns === "shortcut" && prog.shortcuts) {
      if (rest !== "run") throw unknownMethod(method);
      const run = prog.shortcuts[params?.contribution];
      if (!run) throw noContribution(params?.contribution);
      return (await run({ ...params, values: params.values ?? {}, hub })) ?? {};
    }
    throw unknownMethod(method);
  }

  // Apps before UI version 3 do not know push and pop: an action that pushes gets an id of the SDK's for them, and
  // that ui.action draws the pushed page in place of this one; a result's push draws the page, a pop the page as
  // first drawn. The program writes push and pop once, for every app.
  const pushes = new Map<string, wire.Push>();
  let pushN = 0;
  const older = (p: wire.UIParams) => p.ui_version > 0 && p.ui_version < 3;
  function forOlder(v: wire.View | undefined, contribution: string) {
    const fix = (a?: wire.Action) => {
      if (!a?.push) return;
      const id = `${PUSH_PREFIX}${++pushN}`;
      pushes.set(id, { ...a.push, ui: a.push.ui || contribution });
      if (pushes.size > 512) pushes.delete(pushes.keys().next().value!);
      a.id = id;
    };
    for (const sec of v?.sections ?? []) {
      for (const c of sec.items as any[]) {
        fix(c.action);
        fix(c.button?.action);
      }
    }
  }
  async function draw(req: UIRequest, contribution: string, state: string) {
    const page = prog.ui?.[contribution];
    if (!page) throw noContribution(contribution);
    const v = await page.render({ ...req, contribution, state, action: undefined, values: undefined });
    if (older(req)) forOlder(v, contribution);
    return v;
  }

  async function ui(method: string, params: wire.UIParams): Promise<unknown> {
    if (method !== "render" && method !== "action") throw unknownMethod(`ui.${method}`);
    const page = prog.ui![params.contribution];
    if (!page) throw noContribution(params.contribution);
    const req: UIRequest = { ...params, hub };
    if (method === "render") return draw(req, req.contribution, req.state ?? "");
    if (older(req) && req.action?.startsWith(PUSH_PREFIX)) {
      const push = pushes.get(req.action);
      return { view: await draw(req, push?.ui || req.contribution, push ? push.state ?? "" : req.state ?? "") };
    }
    const res = await page.action?.(req);
    if (res && (res.view || res.toast || res.open_url || res.push || res.pop)) {
      if (!older(req)) return res;
      if (res.push) return { view: await draw(req, res.push.ui || req.contribution, res.push.state ?? ""), ...(res.toast ? { toast: res.toast } : {}) };
      if (res.pop) return { view: await draw(req, req.contribution, ""), ...(res.toast ? { toast: res.toast } : {}) };
      forOlder(res.view, req.contribution);
      return res;
    }
    const v = await page.render(req);
    if (older(req)) forOlder(v, req.contribution);
    return { view: v };
  }

  function line(text: string) {
    let f: Frame;
    try {
      f = JSON.parse(text);
    } catch {
      return;
    }
    if (f.method) {
      const id = f.id;
      const done = Promise.resolve()
        .then(() => request(f.method!, f.params))
        .then(
          (result) => write(JSON.stringify({ id, result: result ?? {} })),
          (err) => {
            const e = err instanceof ExtError ? err : new ExtError(err instanceof Error ? err.message : String(err));
            write(JSON.stringify({ id, error: { message: e.message, ...(e.code ? { code: e.code } : {}), ...(e.data !== undefined ? { data: e.data } : {}) } }));
          },
        );
      inflight.add(done);
      done.finally(() => inflight.delete(done));
    } else if (f.event) {
      const dot = f.event.indexOf(".");
      const h = initialized && dot > 0 ? prog.namespaces?.[f.event.slice(0, dot)] : undefined;
      if (h) Promise.resolve().then(() => h(f.event!.slice(dot + 1), f.params ?? {})).catch(() => {});
    } else if (f.id) {
      hub.answer(f);
    }
  }

  return {
    hub,
    line,
    /** Waits for the answers to the requests already read. */
    drain: () => Promise.all([...inflight]),
    close: () => hub.close(),
  };
}

/** What the hub gives a program in its environment, under the names of any hub. */
export function env() {
  const get = (name: string) =>
    process.env[`PERSONA_EXTENSION_${name}`] || process.env[`PERSONA_PLUGIN_${name}`] || process.env[`JARVIS_PLUGIN_${name}`] || "";
  const json = (s: string) => {
    try {
      return s ? JSON.parse(s) : undefined;
    } catch {
      return undefined;
    }
  };
  return {
    id: get("ID"),
    dir: get("DIR"),
    data: get("DATA"),
    socket: get("SOCKET"),
    /** For a program that does not speak the protocol (rpc: none); otherwise initialize has it. */
    config: json(get("CONFIG")),
    secrets: json(get("SECRETS")) as Record<string, string> | undefined,
    hubURL: process.env.PERSONA_HUB_URL || process.env.JARVIS_HUB_URL || "",
    hubToken: process.env.PERSONA_HUB_TOKEN || process.env.JARVIS_HUB_TOKEN || "",
  };
}

/** The manifest as the program answers it to a hub: without `tags`, the market's (nwverse/persona#2424), which hubs
 * from before them refuse as an unknown field. */
function answeredManifest(m: unknown): unknown {
  if (m === null || typeof m !== "object" || Array.isArray(m) || !("tags" in m)) return m;
  const { tags: _tags, ...rest } = m as Record<string, unknown>;
  return rest;
}

/** Runs prog as the hub starts it: the protocol on stdin / stdout, HTTP on the hub's socket when it has fetch.
 * Exits when the hub closes stdin or stops the program. */
export async function main(prog: Program) {
  if (process.argv[2] === "manifest" && prog.manifest !== undefined) {
    process.stdout.write(JSON.stringify(answeredManifest(prog.manifest)) + "\n");
    return;
  }
  let server: { stop(): void } | undefined;
  if (prog.fetch) {
    const sock = env().socket;
    if (!sock) {
      console.error("no PERSONA_EXTENSION_SOCKET: runtime.http is not set in the manifest");
      process.exit(2);
    }
    try {
      require("node:fs").unlinkSync(sock);
    } catch {}
    const handler = prog.fetch;
    server = Bun.serve({
      unix: sock,
      fetch: (req) => (new URL(req.url).pathname === "/health" ? new Response("ok") : handler(req)),
    });
  }
  const conn = connect(prog, (l) => process.stdout.write(l + "\n"));
  for (const sig of ["SIGTERM", "SIGINT"] as const) process.on(sig, () => process.exit(0));
  const decoder = new TextDecoder();
  let buf = "";
  let spoke = false;
  for await (const chunk of Bun.stdin.stream()) {
    spoke = true;
    buf += decoder.decode(chunk, { stream: true });
    let nl: number;
    while ((nl = buf.indexOf("\n")) >= 0) {
      const l = buf.slice(0, nl).trim();
      buf = buf.slice(nl + 1);
      if (l) conn.line(l);
    }
  }
  await conn.drain();
  conn.close();
  // rpc: none (only HTTP): the hub sends nothing on stdin; serve until it stops the program.
  if (server && !spoke) return;
  server?.stop();
  process.exit(0);
}
