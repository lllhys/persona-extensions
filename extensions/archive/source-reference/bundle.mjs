// ../../sdk/ts/src/exact-json.ts
var EXT_ERROR = Symbol.for("@persona/extension.ExtError");
var JSONIntegerError = class extends TypeError {
  [EXT_ERROR] = true;
  code = "invalid_params";
};
var MAX_EXACT_LENGTH = 8 << 20;
var VALIDATED_JSON = Symbol("validated JSON slice");

// ../../sdk/ts/src/components.ts
var callbackTag = Symbol("callback");
function stringValues(value) {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid action values");
  const entries = Object.entries(value);
  if (entries.length > 128 || entries.some(([key, item]) => key.length > 256 || typeof item !== "string" || item.length > 16384)) {
    throw new Error("invalid action values");
  }
  return Object.freeze(Object.fromEntries(entries));
}
function callback(fn, options = {}, parse = stringValues) {
  return Object.freeze({ [callbackTag]: true, options, invoke: (value, context2) => fn(parse(value), context2) });
}
var elementTag = Symbol("element");
var nativeTag = Symbol("native");
var Fragment = ({ children }) => children;
function createElement(type, props, key) {
  if (typeof type !== "function" && type !== Fragment) throw new Error("only registered native or function components are supported");
  const { key: propKey, ...rest } = props ?? {};
  const resolvedKey = key ?? propKey;
  if (resolvedKey !== void 0 && (typeof resolvedKey !== "string" || !resolvedKey || resolvedKey.length > 256)) {
    throw new Error("UI keys must be nonempty strings of at most 256 characters");
  }
  return Object.freeze({ [elementTag]: true, type, props: Object.freeze(rest), key: resolvedKey });
}
function native(kind) {
  const component = ((props) => createElement(component, props));
  Object.defineProperty(component, nativeTag, { value: kind });
  return component;
}
var View = native("view");
var Section = native("section");
var Text = native("text");
var Metric = native("metric");
var Progress = native("progress");
var Row = native("row");
var Button = native("button");
var Link = native("link");
var Field = native("field");
var Divider = native("divider");
var Chart = native("chart");
var StatGrid = native("stat_grid");
var Segmented = native("segmented");
var Markdown = native("markdown");
var Table = native("table");
var Empty = native("empty");
var Loading = native("loading");
var Header = native("header");
var Breadcrumb = native("breadcrumb");
var FileTree = native("file_tree");
var Code = native("code");
var Diff = native("diff");
var CommentThread = native("comment_thread");
var Composer = native("composer");
var Image = native("image");
var Grid = native("grid");
var Carousel = native("carousel");

// ../../sdk/ts/src/hooks.ts
var context;
function withHooks(owner, instance, render) {
  const previous = context;
  const current = { owner, instance, index: 0 };
  context = current;
  try {
    const node = render();
    if (instance.initialized && current.index !== instance.hooks.length) throw new Error("hook order changed: fewer hooks");
    instance.initialized = true;
    return node;
  } finally {
    context = previous;
  }
}
function hook(kind, create) {
  if (!context) throw new Error("hooks require a mounted function component");
  const { instance, index, owner } = context;
  if (index >= owner.maxHooks) throw new Error("component hook limit exceeded");
  context.index++;
  let slot = instance.hooks[index];
  if (!slot) {
    if (instance.initialized) throw new Error("hook order changed: more hooks");
    slot = create();
    instance.hooks.push(slot);
  }
  if (slot.kind !== kind) throw new Error("hook order changed: different hook");
  return [slot, context];
}
function useState(initial) {
  const [slot, { owner, instance }] = hook("state", () => ({ kind: "state", cell: context.owner.state.create(typeof initial === "function" ? initial() : initial) }));
  return [slot.cell.get(), (update) => {
    if (owner.disposed || !instance.active) return;
    const previous = slot.cell.get();
    const next = typeof update === "function" ? update(previous) : update;
    if (!Object.is(previous, next)) {
      slot.cell.set(next);
      owner.invalidate();
    }
  }];
}
function equalDeps(a, b) {
  return a !== void 0 && b !== void 0 && a.length === b.length && a.every((item, index) => Object.is(item, b[index]));
}
function effectHook(effect, dependencies, snapshot) {
  const [slot] = hook("effect", () => ({ kind: "effect", effect, deps: dependencies?.slice(), changed: true, snapshot }));
  if (!equalDeps(slot.deps, dependencies) || slot.snapshot !== snapshot) slot.changed = true;
  slot.effect = effect;
  slot.deps = dependencies?.slice();
  slot.snapshot = snapshot;
}
function useLoadEffect(effect, dependencies) {
  effectHook(effect, dependencies, true);
}

// ../../sdk/ts/src/renderer.ts
var defaultState = { create(initial) {
  let value = initial;
  return { get: () => value, set: (next) => {
    value = next;
  } };
} };
var defaultTimers = { setInterval: (fn, ms) => globalThis.setInterval(fn, ms), clearInterval: (id) => globalThis.clearInterval(id) };
var fields = {
  text: ["text", "style"],
  metric: ["label", "value", "detail", "tone", "icon", "icon_color", "spark"],
  progress: ["label", "ratio", "detail", "tone"],
  row: ["title", "subtitle", "value", "badge", "action", "icon", "avatar", "thumbnail", "icon_color", "control", "actions", "spark"],
  button: ["title", "action", "role"],
  link: ["title", "url"],
  field: ["name", "label", "kind", "value", "choices", "placeholder", "earliest", "latest", "detail"],
  divider: [],
  chart: ["kind", "series", "title", "unit", "stacked", "detail", "center", "rising"],
  stat_grid: ["stats"],
  segmented: ["name", "choices", "value", "action"],
  markdown: ["text"],
  table: ["columns", "rows", "caption"],
  empty: ["title", "detail", "icon", "button", "icon_color"],
  loading: ["title"],
  header: ["title", "subtitle", "badges", "facts", "avatar"],
  breadcrumb: ["crumbs"],
  file_tree: ["entries", "selected", "action", "expand"],
  code: ["path", "language", "text", "start_line", "highlight", "truncated", "load", "line_url"],
  diff: ["path", "old_path", "status", "language", "additions", "deletions", "binary", "collapsed", "hunks", "threads", "load", "expand", "comment", "context", "viewed", "mark_viewed", "revision", "generated", "similarity", "draft", "suggestions"],
  comment_thread: ["title", "comments", "resolved", "reply"],
  composer: ["name", "placeholder", "value", "buttons"],
  image: ["src", "alt", "caption", "aspect", "fit", "no_zoom"],
  grid: ["tiles", "max_columns"],
  carousel: ["title", "tiles", "width"]
};
function pathPart(parent, ...segments) {
  return parent + segments.map((segment) => `${segment.length}:${segment}`).join("");
}
function freeze(value) {
  if (value && typeof value === "object") {
    for (const child of Object.values(value)) freeze(child);
    Object.freeze(value);
  }
  return value;
}
var StringBudget = class {
  constructor(limit, message) {
    this.limit = limit;
    this.message = message;
  }
  used = 0;
  add(value) {
    if (value.length > this.limit - this.used) throw new Error(this.message);
    this.used += value.length;
  }
};
var encoder = new TextEncoder();
var Renderer = class {
  constructor(options) {
    this.options = options;
    if (typeof options?.authorize !== "function") throw new Error("a trusted authorize callback is required");
    this.state = options.state ?? defaultState;
    this.timers = options.timers ?? defaultTimers;
    this.fetch = options.fetch ?? (() => Promise.reject(new Error("no sanctioned fetch adapter")));
    this.limits = { nodes: 2048, depth: 64, events: 1024, callbacks: 2048, props: 32768, hooks: 128, pendingEffects: 128, frameBytes: 1048576, ...options.limits };
    for (const limit of Object.values(this.limits)) if (!Number.isSafeInteger(limit) || limit < 1) throw new Error("invalid renderer limit");
  }
  generation = globalThis.crypto.randomUUID();
  state;
  timers;
  fetch;
  disposed = false;
  suspended = false;
  tree;
  instances = /* @__PURE__ */ new Map();
  nativeNodes = /* @__PURE__ */ new Map();
  registrations = /* @__PURE__ */ new Map();
  callbackIDs = /* @__PURE__ */ new Map();
  // identities only survive suspend; closures do not
  receipts = /* @__PURE__ */ new Map();
  activeEvents = /* @__PURE__ */ new Map();
  errors = [];
  snapshot;
  scheduled = false;
  rendering = false;
  ticket = 0;
  visibility = 0;
  pendingEffects = 0;
  pendingLoads = /* @__PURE__ */ new Set();
  dirtyLoads = /* @__PURE__ */ new Set();
  loadFailed = false;
  readiness;
  readinessGuard;
  limits;
  get maxHooks() {
    return this.limits.hooks;
  }
  /** Mount once. A new Renderer is a new generation and owns independent state/callbacks. */
  mount(tree) {
    if (this.snapshot || this.disposed) throw new Error("renderer cannot mount");
    this.tree = tree;
    return this.render();
  }
  /** Full existing-wire snapshot. The manager computes deltas and handles version negotiation. */
  render(tree = this.tree) {
    if (this.disposed || this.suspended || this.rendering) throw new Error("renderer cannot render");
    this.tree = tree;
    this.rendering = true;
    this.scheduled = false;
    ++this.ticket;
    const visited = /* @__PURE__ */ new Set();
    const keys = /* @__PURE__ */ new Set();
    const registrations = /* @__PURE__ */ new Map();
    const nativeNodes = /* @__PURE__ */ new Map();
    const nativeIdentity = (path) => {
      const id = this.nativeNodes.get(path) ?? globalThis.crypto.randomUUID();
      nativeNodes.set(path, id);
      return id;
    };
    const previousActions = this.callbackIDs;
    let nodeCount = 0;
    let propCount = 0;
    let traversalCount = 0;
    const strings2 = new StringBudget(this.limits.frameBytes, "UI frame limit exceeded");
    const addKey = (key) => {
      if (typeof key !== "string" || !key || key.length > 256 || keys.has(key)) throw new Error("missing or duplicate UI key");
      if (++nodeCount > this.limits.nodes) throw new Error("UI node limit exceeded");
      keys.add(key);
      return key;
    };
    const lower = (value, key, depth, ancestors, slot) => {
      if (++propCount > this.limits.props) throw new Error("UI prop limit exceeded");
      if (depth > this.limits.depth) throw new Error("UI depth limit exceeded");
      if (typeof value === "string") {
        strings2.add(value);
        return value;
      }
      if (value === void 0 || value === null || typeof value === "boolean") return value;
      if (typeof value === "number" && Number.isFinite(value)) return value;
      if (typeof value !== "object") throw new Error("only native JSON props and registered callbacks are allowed");
      if (callbackTag in value) {
        const cb = value;
        const identity = JSON.stringify([key, slot]);
        if (registrations.size >= this.limits.callbacks) throw new Error("UI callback limit exceeded");
        const id = previousActions.get(identity) ?? `cb.${globalThis.crypto.randomUUID()}`;
        const options = lower(cb.options, key, depth + 1, ancestors, pathPart(slot, "options"));
        if (Object.keys(options).some((name) => !["confirm", "submit", "push", "values"].includes(name))) throw new Error("invalid callback action options");
        if (options.push && (options.push.page !== void 0 || Object.keys(options.push).some((name) => !["ui", "state", "title"].includes(name)))) throw new Error("assistant-only action");
        const action = { ...options, id };
        registrations.set(id, { identity, key, action, callback: cb });
        return action;
      }
      if (ancestors.has(value)) throw new Error("cyclic UI props");
      ancestors.add(value);
      try {
        if (Array.isArray(value)) {
          if (value.length > this.limits.props - propCount) throw new Error("UI prop limit exceeded");
          return Array.from(value, (item, index) => lower(item, key, depth + 1, ancestors, pathPart(slot, "array", String(index))));
        }
        if (Object.getPrototypeOf(value) !== Object.prototype && Object.getPrototypeOf(value) !== null) throw new Error("native props must be plain JSON");
        if (elementTag in value) throw new Error("unsupported native prop");
        const out = {};
        for (const name in value) {
          if (!Object.prototype.hasOwnProperty.call(value, name)) continue;
          strings2.add(name);
          if (propCount >= this.limits.props) throw new Error("UI prop limit exceeded");
          const item = value[name];
          if (item !== void 0) {
            Object.defineProperty(out, name, { value: lower(item, key, depth + 1, ancestors, pathPart(slot, "prop", name)), enumerable: true });
          } else ++propCount;
        }
        return out;
      } finally {
        ancestors.delete(value);
      }
    };
    const resolve = (node, parent, depth) => {
      if (++traversalCount > this.limits.props) throw new Error("UI prop limit exceeded: child traversal limit");
      if (depth > this.limits.depth) throw new Error("UI depth limit exceeded");
      if (node === null || node === void 0 || node === false) return [];
      if (Array.isArray(node)) {
        if (node.length > this.limits.props - traversalCount) throw new Error("UI prop limit exceeded: child traversal limit");
        const siblingKeys = /* @__PURE__ */ new Set();
        for (const child of node) {
          if (child && !Array.isArray(child) && child.type !== Fragment) {
            if (!child.key || siblingKeys.has(child.key)) throw new Error("missing or duplicate sibling key");
            siblingKeys.add(child.key);
          }
        }
        const items = [];
        for (const child of node) items.push(...resolve(child, parent, depth + 1));
        return items;
      }
      const element = node;
      if (typeof element !== "object" || element[elementTag] !== true) throw new Error("only SDK elements are allowed");
      if (element.type === Fragment) return resolve(element.props.children, element.key ? pathPart(parent, element.key) : parent, depth + 1);
      if (typeof element.type !== "function") throw new Error("unknown JSX element");
      if (!element.key) throw new Error("every component requires an explicit key");
      const path = pathPart(parent, element.key);
      const kind = element.type[nativeTag];
      if (!kind) {
        if (visited.has(path)) throw new Error("duplicate function component key");
        visited.add(path);
        if (visited.size > this.limits.nodes) throw new Error("function component limit exceeded");
        let instance = this.instances.get(path);
        if (instance && instance.type !== element.type) {
          this.remove(instance);
          this.instances.delete(path);
          instance = void 0;
        }
        if (!instance) {
          instance = { id: globalThis.crypto.randomUUID(), type: element.type, hooks: [], initialized: false, active: true };
          this.instances.set(path, instance);
        }
        const result = withHooks(this, instance, () => element.type(element.props));
        return resolve(result, pathPart(path, instance.id), depth + 1);
      }
      const key = addKey(element.key);
      const identity = nativeIdentity(JSON.stringify([path, kind]));
      const { children, ...props } = element.props;
      const allowed = kind === "view" ? ["title", "state", "refresh_after", "search", "filter", "tabs", "layout", "review"] : kind === "section" ? ["title", "footer", "more", "pane"] : fields[kind];
      if (!allowed || Object.keys(props).some((name) => !allowed.includes(name))) throw new Error("unknown native component prop");
      if (kind === "view" || kind === "section") {
        const items = resolve(children, path, depth + 1);
        const expected = kind === "view" ? "section" : "component";
        if (items.some((item) => item.kind !== expected)) throw new Error("invalid native UI nesting");
        const out2 = lower(props, key, depth + 1, /* @__PURE__ */ new Set(), JSON.stringify([identity, kind]));
        return [{ kind, key, value: kind === "view" ? { ...out2, sections: items.map((item) => item.value) } : { ...out2, key, items: items.map((item) => item.value) } }];
      }
      if (children !== void 0) throw new Error("native leaf components do not accept children");
      let out;
      if (kind === "grid" || kind === "carousel") {
        const { tiles, ...rest } = props;
        if (!Array.isArray(tiles)) throw new Error("tiles must be an array");
        const lowered = lower(rest, key, depth + 1, /* @__PURE__ */ new Set(), JSON.stringify([identity, kind]));
        const tileCount = tiles.length;
        if (++propCount > this.limits.props || tileCount > this.limits.props - propCount) throw new Error("UI prop limit exceeded");
        if (tileCount > this.limits.nodes - nodeCount) throw new Error("UI node limit exceeded");
        out = { ...lowered, tiles: Array.from({ length: tileCount }, (_, index) => {
          const tile = tiles[index];
          const tileKey = addKey(tile?.key);
          return lower(tile, tileKey, depth + 1, /* @__PURE__ */ new Set(), nativeIdentity(JSON.stringify([identity, kind, tileKey])));
        }) };
      } else out = lower(props, key, depth + 1, /* @__PURE__ */ new Set(), JSON.stringify([identity, kind]));
      return [{ kind: "component", key, value: { ...out, type: kind, key } }];
    };
    try {
      const roots = resolve(this.tree, "", 0);
      if (roots.length !== 1 || roots[0].kind !== "view") throw new Error("one native View root is required");
      const snapshot = { generation: this.generation, version: (this.snapshot?.version ?? 0) + 1, rootKey: roots[0].key, view: roots[0].value };
      if (!Number.isSafeInteger(snapshot.version) || snapshot.version > 9007199254740991) throw new Error("rotate UI generation before revision exhaustion");
      if (encoder.encode(JSON.stringify(snapshot)).byteLength > this.limits.frameBytes) throw new Error("UI frame limit exceeded");
      for (const [path, instance] of this.instances) if (!visited.has(path)) {
        this.remove(instance);
        this.instances.delete(path);
      }
      for (const [controller, id] of this.activeEvents) if (!registrations.has(id)) controller.abort();
      for (const instance of this.instances.values()) for (const hook2 of instance.hooks) {
        if (hook2.kind === "effect") {
          if (hook2.snapshot && hook2.changed) this.dirtyLoads.add(hook2);
          else this.dirtyLoads.delete(hook2);
          if (hook2.changed) hook2.stop?.();
        }
      }
      this.registrations = registrations;
      this.callbackIDs = new Map([...registrations].map(([id, registration]) => [registration.identity, id]));
      this.nativeNodes = nativeNodes;
      const published = this.snapshot = freeze(snapshot);
      try {
        this.options.onRender?.(published);
      } catch (error) {
        this.report(error);
      }
      this.commitEffects();
      return published;
    } catch (error) {
      this.dispose();
      throw error;
    } finally {
      this.rendering = false;
      this.readiness?.();
    }
  }
  /** Read the latest immutable snapshot without triggering rendering or advancing its version. */
  current() {
    if (!this.snapshot || this.disposed) throw new Error("UI session is not mounted");
    return this.snapshot;
  }
  /** Drain only opted-in finite loads and already scheduled renders, never invalidate a cached view.
   * A timeout is a failure, not a successful loading snapshot. One bounded waiter per renderer. */
  async settle(signal, timeoutMs = 1e4) {
    if (this.readiness) throw new Error("snapshot readiness already running");
    if (!Number.isFinite(timeoutMs) || timeoutMs <= 0 || timeoutMs > 1e4) throw new Error("invalid snapshot readiness timeout");
    const visibility = this.visibility;
    const deadline = performance.now() + timeoutMs;
    const workLimit = 512;
    let drains = 0, renders = 0;
    let turnTimer;
    let resolve;
    let failure;
    const wake = () => resolve?.();
    const check = () => {
      if (signal.aborted || failure) throw failure ?? new Error("snapshot readiness canceled");
      if (this.disposed || this.suspended || this.visibility !== visibility) throw new Error("inactive snapshot readiness");
      if (performance.now() >= deadline) throw new Error("snapshot readiness timeout");
    };
    const guard = () => {
      try {
        check();
        if (++renders > workLimit) throw new Error("snapshot readiness work limit");
        turnTimer ??= globalThis.setTimeout(() => {
          renders = 0;
          turnTimer = void 0;
        }, 0);
      } catch (error) {
        failure = error;
        if (this.visibility === visibility) this.suspend();
        wake();
        throw error;
      }
    };
    const abort = () => {
      failure = new Error("snapshot readiness canceled");
      wake();
    };
    const timer = globalThis.setTimeout(() => {
      failure = new Error("snapshot readiness timeout");
      wake();
    }, timeoutMs);
    this.readiness = wake;
    this.readinessGuard = guard;
    signal.addEventListener("abort", abort, { once: true });
    try {
      for (; ; ) {
        if (drains > workLimit) throw new Error("snapshot readiness work limit");
        check();
        const waiting = new Promise((done) => {
          resolve = done;
        });
        if (this.scheduled) {
          drains++;
          this.render();
        }
        if (drains > workLimit) throw new Error("snapshot readiness work limit");
        check();
        if (this.loadFailed) throw new Error("snapshot loading effect failed");
        if (!this.pendingLoads.size && !this.dirtyLoads.size && !this.scheduled && !this.activeEvents.size) return this.current();
        if (this.scheduled) continue;
        await waiting;
        resolve = void 0;
        drains = 0;
      }
    } catch (error) {
      if (this.visibility === visibility) this.suspend();
      throw error;
    } finally {
      globalThis.clearTimeout(timer);
      globalThis.clearTimeout(turnTimer);
      signal.removeEventListener("abort", abort);
      this.readiness = void 0;
      this.readinessGuard = void 0;
    }
  }
  invalidate() {
    if (this.disposed || this.suspended || this.scheduled) return;
    this.scheduled = true;
    const ticket = ++this.ticket;
    (this.options.schedule ?? queueMicrotask)(() => {
      if (ticket !== this.ticket || this.disposed || this.suspended) return;
      this.scheduled = false;
      try {
        this.readinessGuard?.();
        this.render();
      } catch (error) {
        this.report(error);
      }
    });
    this.readiness?.();
  }
  commitEffects() {
    for (const instance of this.instances.values()) {
      for (const hook2 of instance.hooks) {
        if (this.disposed || this.suspended) return;
        if (hook2.kind === "effect" && hook2.changed) this.runEffect(hook2);
      }
    }
  }
  runEffect(hook2) {
    if (this.pendingEffects >= this.limits.pendingEffects) {
      this.report(new Error("pending effect limit exceeded"));
      return;
    }
    hook2.changed = false;
    this.dirtyLoads.delete(hook2);
    const snapshot = hook2.snapshot;
    const controller = new AbortController();
    let cleanup;
    let stopped = false;
    const clean = () => {
      if (cleanup) {
        const fn = cleanup;
        cleanup = void 0;
        try {
          fn();
        } catch (error) {
          this.report(error);
        }
      }
    };
    const stop = () => {
      if (!stopped) {
        stopped = true;
        controller.abort();
        clean();
      }
      this.pendingLoads.delete(controller);
      this.readiness?.();
    };
    hook2.stop = stop;
    const resolved = (result) => {
      if (result !== void 0 && typeof result !== "function") throw new Error("invalid effect cleanup");
      cleanup = result || void 0;
      if (stopped) clean();
    };
    const failed = (error) => {
      if (snapshot && !controller.signal.aborted) this.loadFailed = true;
      stop();
      this.report(error);
    };
    try {
      const result = hook2.effect(controller.signal);
      if (result && typeof result.then === "function") {
        ++this.pendingEffects;
        if (snapshot && !stopped) this.pendingLoads.add(controller);
        Promise.resolve(result).then(resolved).catch(failed).finally(() => {
          --this.pendingEffects;
          this.pendingLoads.delete(controller);
          this.commitEffects();
          this.readiness?.();
        });
      } else resolved(result);
    } catch (error) {
      failed(error);
    }
  }
  /** Returns and clears bounded local diagnostics. Errors never become user UI text automatically. */
  takeErrors() {
    const errors = this.errors;
    this.errors = [];
    return errors;
  }
  report(error) {
    if (this.errors.length < 64) this.errors.push(error);
    try {
      this.options.onError?.(error);
    } catch {
    }
  }
  remove(instance) {
    instance.active = false;
    for (const hook2 of instance.hooks) if (hook2.kind === "effect") {
      this.dirtyLoads.delete(hook2);
      hook2.stop?.();
    }
  }
  cancelEvents() {
    for (const controller of this.activeEvents.keys()) controller.abort();
  }
  /** Only accepts saved callbacks. The manager first authenticates owner/extension/contribution/session.
   * Identical accepted retries replay their original promise, even after a render advances the version.
   * Every invoked/ambiguous callback consumes a native revision. Bounded receipts
   * can then be evicted safely: their old revisions never execute again.
   */
  event(event, controller = new AbortController()) {
    try {
      if (this.disposed || this.suspended || !this.snapshot) throw new Error("inactive UI session");
      if (event.generation !== this.generation) throw new Error("stale UI event generation");
      if (typeof event.eventId !== "string" || !event.eventId || event.eventId.length > 256) throw new Error("invalid event ID");
      if (typeof event.key !== "string" || !event.key || event.key.length > 256 || typeof event.actionId !== "string" || !event.actionId || event.actionId.length > 256) throw new Error("invalid callback key or ID");
      const strings2 = new StringBudget(this.limits.frameBytes, "UI event frame limit exceeded");
      const saved = freeze({
        generation: event.generation,
        version: event.version,
        eventId: event.eventId,
        key: event.key,
        actionId: event.actionId,
        values: this.eventValues(event.values, strings2)
      });
      const fingerprint = JSON.stringify([saved.generation, saved.version, saved.key, saved.actionId, saved.values]);
      if (encoder.encode(fingerprint).byteLength > this.limits.frameBytes) throw new Error("UI event frame limit exceeded");
      const receipt = this.receipts.get(saved.eventId);
      if (receipt) {
        if (receipt.fingerprint !== fingerprint) throw new Error("event ID collision: body mismatch");
        return receipt.result;
      }
      if (saved.version !== this.snapshot.version) throw new Error("stale UI event version");
      if (this.snapshot.version >= 9007199254740991) throw new Error("rotate UI generation before revision exhaustion");
      const registration = this.registrations.get(saved.actionId);
      if (!registration || registration.key !== saved.key) throw new Error("unknown or stale callback");
      if (this.activeEvents.size) throw new Error("UI event already running");
      while (this.receipts.size >= this.limits.events) this.receipts.delete(this.receipts.keys().next().value);
      this.activeEvents.set(controller, saved.actionId);
      let invoked = false;
      const result = Promise.resolve().then(async () => {
        if (controller.signal.aborted) throw new Error("stale UI event");
        const allowed = await this.options.authorize(registration.action, saved, controller.signal);
        if (allowed !== true) throw new Error("native action gate not satisfied");
        if (controller.signal.aborted || this.disposed || this.suspended || saved.version !== this.snapshot?.version || this.registrations.get(saved.actionId) !== registration) throw new Error("stale UI event");
        let values = saved.values;
        if (registration.action.values) {
          if (!values || typeof values !== "object" || Array.isArray(values)) throw new Error("native action values require a record");
          values = freeze({ ...values, ...registration.action.values });
        }
        invoked = true;
        const value = await registration.callback.invoke(values, { signal: controller.signal });
        return this.options.transformResult ? this.options.transformResult(value) : value;
      }).finally(() => {
        if (invoked && this.snapshot && this.snapshot.version >= 9007199254740991) this.dispose();
        if (invoked && this.snapshot && !this.disposed) {
          this.snapshot = freeze({ ...this.snapshot, version: this.snapshot.version + 1 });
          if (!this.suspended) {
            try {
              this.options.onRender?.(this.snapshot);
            } catch (error) {
              this.report(error);
            }
          }
        }
        this.activeEvents.delete(controller);
        this.readiness?.();
      });
      this.receipts.set(saved.eventId, { fingerprint, result });
      return result;
    } catch (error) {
      return Promise.reject(error);
    }
  }
  eventValues(value, strings2) {
    let count = 0;
    const ancestors = /* @__PURE__ */ new Set();
    const copy = (item, depth) => {
      if (++count > this.limits.props || depth > this.limits.depth) throw new Error("UI event value limit exceeded");
      if (typeof item === "string") {
        strings2.add(item);
        return item;
      }
      if (item === null || typeof item === "boolean") return item;
      if (typeof item === "number" && Number.isFinite(item)) return item;
      if (!item || typeof item !== "object" || ancestors.has(item)) throw new Error("UI event values must be acyclic JSON");
      ancestors.add(item);
      try {
        if (Array.isArray(item)) {
          if (item.length > this.limits.props - count) throw new Error("UI event value limit exceeded");
          return Array.from(item, (child) => copy(child, depth + 1));
        }
        if (Object.getPrototypeOf(item) !== Object.prototype && Object.getPrototypeOf(item) !== null) throw new Error("UI event values must be plain JSON");
        const keys = [];
        for (const key in item) {
          if (!Object.prototype.hasOwnProperty.call(item, key)) continue;
          if (keys.length >= this.limits.props - count) throw new Error("UI event value limit exceeded");
          strings2.add(key);
          keys.push(key);
        }
        return Object.fromEntries(keys.sort().map((key) => [key, copy(item[key], depth + 1)]));
      } finally {
        ancestors.delete(item);
      }
    };
    return copy(value, 0);
  }
  /** Abort effects/fetches/events and clear callbacks immediately; retain only bounded component state. */
  suspend() {
    if (this.disposed || this.suspended) return;
    this.suspended = true;
    this.visibility++;
    this.loadFailed = false;
    this.readiness?.();
    this.scheduled = false;
    ++this.ticket;
    this.cancelEvents();
    this.registrations.clear();
    for (const instance of this.instances.values()) for (const hook2 of instance.hooks) if (hook2.kind === "effect") {
      hook2.stop?.();
      hook2.changed = true;
    }
  }
  /** Publish a new full snapshot before effects/timers restart. Never replay a stale callback table. */
  resume() {
    if (this.disposed || !this.suspended) throw new Error("renderer cannot resume");
    this.suspended = false;
    return this.render();
  }
  dispose() {
    if (this.disposed) return;
    this.disposed = true;
    this.readiness?.();
    this.scheduled = false;
    ++this.ticket;
    this.cancelEvents();
    for (const instance of this.instances.values()) this.remove(instance);
    this.instances.clear();
    this.nativeNodes.clear();
    this.callbackIDs.clear();
    this.registrations.clear();
    this.receipts.clear();
    this.tree = void 0;
    this.snapshot = void 0;
  }
};

// ../../sdk/ts/src/page-bridge.ts
function componentPage(root2, options = {}) {
  return { componentPage: true, snapshotReadiness: 1, callbackScope: 1, root: root2, options, createRenderer: (host) => new Renderer({
    ...options,
    ...host,
    // The manager evicts after 64 events and rejects those old wire revisions;
    // retaining more native results cannot serve an authenticated retry.
    limits: { ...options.limits, events: Math.min(options.limits?.events ?? 64, 64) }
  }) };
}
var RESULT_MOST = 64 << 10;
var encoder2 = new TextEncoder();

// ../../sdk/ts/src/index.ts
var EXT_ERROR2 = Symbol.for("@persona/extension.ExtError");
var ExtError = class extends Error {
  constructor(message, code, data) {
    super(message);
    this.code = code;
    this.data = data;
  }
  [EXT_ERROR2] = true;
};
var noContribution = (id) => new ExtError(`no contribution ${id}`, "no_contribution");
var unknownMethod = (method) => new ExtError(`unknown method ${method}`, "unknown_method");
var EXACT_PARAMS = Symbol.for("@persona/extension.exactParams");
function withExactParams(handler) {
  const wrapped = (method, params, call) => handler(method, params, call);
  Object.defineProperty(wrapped, EXACT_PARAMS, { value: true });
  return wrapped;
}

// persona-extension.json
var persona_extension_default = { manifest_version: 1, id: "archive", name: "Internet Archive \u9986\u85CF\u68C0\u7D22", version: "0.1.1", icon: "assets/icon.png", description: "\u6309\u6807\u9898\u3001\u4F5C\u8005\u6216\u4E3B\u9898\u641C\u7D22 Internet Archive \u9986\u85CF\uFF0C\u67E5\u770B\u7C7B\u578B\u3001\u5E74\u4EFD\u3001\u4E0B\u8F7D\u91CF\u548C\u539F\u59CB\u6761\u76EE\u3002\u5185\u5BB9\u7531 hub \u901A\u8FC7\u516C\u5F00 API \u83B7\u53D6\uFF0C\u65E0\u9700\u8D26\u53F7\u3002", publisher: "nwverse", homepage: "https://github.com/nwverse/persona-extensions/blob/main/extensions/archive/README.md", tags: ["Internet Archive", "\u9986\u85CF", "\u68C0\u7D22"], engines: { plugin_protocol: 1 }, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/archive.mjs", sha256: "" } }, contributes: { connectors: [{ id: "archive", name: "Internet Archive", capabilities: ["status", "tools"], hosts: ["archive.org"], agent_tools: ["archive_*"] }], ui: [{ id: "collections", title: "\u9986\u85CF\u68C0\u7D22", placement: "nav_page", icon: "doc" }] }, configuration: { type: "object", properties: { media: { type: "string", title: "\u9986\u85CF\u7C7B\u578B", default: "all", oneOf: [{ const: "all", title: "\u5168\u90E8\u7C7B\u578B" }, { const: "texts", title: "\u6587\u672C" }, { const: "audio", title: "\u97F3\u9891" }, { const: "movies", title: "\u89C6\u9891" }, { const: "image", title: "\u56FE\u7247" }] } } }, permissions: { network: ["archive.org"] } };

// src/model.ts
var limits = Object.freeze({ request: 12e3, page: 15e3, tool: 15e3, response: 2 << 20, fresh: 60 * 6e4, cache: 40, cacheBytes: 2 << 20, gap: 200 });
var ReadError = class extends Error {
  constructor(code, status) {
    super(code);
    this.code = code;
    this.status = status;
  }
};
function configFrom(raw) {
  if (raw == null) return { media: "all" };
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = raw.media;
  if (value == null || value === "") return { media: "all" };
  if (value !== "all" && value !== "texts" && value !== "audio" && value !== "movies" && value !== "image") throw new ReadError("invalid");
  return { media: value };
}
var clip = (text, most) => text.length <= most ? text : text.slice(0, most).trimEnd() + "\u2026";
var oneLine = (text) => text.replace(/\s+/gu, " ").trim();
function queryText(value) {
  if (typeof value !== "string") throw new ReadError("invalid");
  if (/[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  const query = oneLine(value);
  if (!query || Array.from(query).length > 160) throw new ReadError("invalid");
  return query;
}
function searchPath(query, media, limit = 20) {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
  const q = queryText(query);
  const filter = media === "all" ? "" : ` AND mediatype:${media}`;
  return `/advancedsearch.php?${new URLSearchParams({ q: `(${q})${filter}`, fl: "identifier,title,creator,year,mediatype,downloads,description", rows: String(limit), page: "1", output: "json" })}`;
}
function object(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  return value;
}
function array(value) {
  if (!Array.isArray(value)) throw new ReadError("bad_answer");
  return value;
}
function string(value, fallback = "") {
  return value == null ? fallback : typeof value === "string" ? value : (() => {
    throw new ReadError("bad_answer");
  })();
}
function integer(value, maximum = 1e9) {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0 || value > maximum) throw new ReadError("bad_answer");
  return value;
}
function strings(value) {
  if (value == null) return [];
  return (Array.isArray(value) ? value : [value]).slice(0, 8).map((x) => clip(oneLine(string(x)), 300)).filter(Boolean);
}
function parseItems(raw, at) {
  const response = object(object(raw).response), total = integer(response.numFound, 1e9);
  const seen = /* @__PURE__ */ new Set();
  const items = array(response.docs).slice(0, 20).map((rawItem) => {
    const item = object(rawItem), identifier = string(item.identifier);
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$/.test(identifier)) throw new ReadError("bad_answer");
    if (seen.has(identifier)) throw new ReadError("bad_answer");
    seen.add(identifier);
    const title = clip(oneLine(string(item.title)), 500);
    if (!title) throw new ReadError("bad_answer");
    const media = item.mediatype;
    if (media !== "texts" && media !== "audio" && media !== "movies" && media !== "image" && media !== "collection" && media !== "software" && media !== "etree" && media !== "data" && media !== "web") throw new ReadError("bad_answer");
    const year = item.year == null ? void 0 : integer(item.year, 2200);
    return {
      identifier,
      title,
      creators: strings(item.creator),
      ...year === void 0 ? {} : { year },
      media,
      downloads: integer(item.downloads ?? 0),
      description: clip(strings(item.description).join(" \xB7 "), 1e3),
      url: `https://archive.org/details/${encodeURIComponent(identifier)}`
    };
  });
  return { items, total, at };
}

// src/locales/en.json
var en_default = {
  search: "Search the archive",
  searchHint: "Title, creator or subject",
  searchAction: "Search",
  results: "Archive results for {query}",
  account: "Public Internet Archive catalog",
  loading: "Searching the archive",
  retry: "Try again",
  welcome: "Search Internet Archive by title, creator or subject.",
  empty: "No matching items. Try another title, creator or subject.",
  noDescription: "No description is provided.",
  year: "Year {year}",
  downloads: "{count} downloads",
  open: "View on Internet Archive",
  attribution: "Catalog metadata is provided by Internet Archive. File format, media type and download count do not establish permission for reuse; check the linked item's rights information.",
  readAt: "Catalog read {at}",
  toolSearch: "Search Internet Archive titles, creators and subjects with canonical item links",
  query: "Title, creator or subject",
  limit: "Maximum number of results",
  invalid: "Enter a valid title, creator or subject.",
  bad_answer: "The archive returned an unexpected response.",
  not_found: "No matching archive item was found.",
  rate_limited: "The archive is rate-limiting requests. Try again later.",
  slow: "The archive took too long to respond.",
  network: "The archive could not be reached.",
  http: "The archive returned HTTP {0}."
};

// src/locales/zh-Hans.json
var zh_Hans_default = {
  search: "\u9986\u85CF\u68C0\u7D22",
  searchHint: "\u6807\u9898\u3001\u4F5C\u8005\u6216\u4E3B\u9898",
  searchAction: "\u641C\u7D22",
  results: "\u4E0E\u201C{query}\u201D\u76F8\u5173\u7684\u9986\u85CF",
  account: "Internet Archive \u516C\u5171\u76EE\u5F55",
  loading: "\u6B63\u5728\u68C0\u7D22\u9986\u85CF",
  retry: "\u91CD\u8BD5",
  welcome: "\u6309\u6807\u9898\u3001\u4F5C\u8005\u6216\u4E3B\u9898\u641C\u7D22 Internet Archive \u9986\u85CF\u3002",
  empty: "\u6CA1\u6709\u627E\u5230\u9986\u85CF\uFF0C\u6362\u4E00\u4E2A\u6807\u9898\u3001\u4F5C\u8005\u6216\u4E3B\u9898\u518D\u8BD5\u3002",
  noDescription: "\u6CA1\u6709\u63D0\u4F9B\u9986\u85CF\u8BF4\u660E\u3002",
  year: "{year} \u5E74",
  downloads: "{count} \u6B21\u4E0B\u8F7D",
  open: "\u5728 Internet Archive \u67E5\u770B",
  attribution: "\u76EE\u5F55\u5143\u6570\u636E\u7531 Internet Archive \u63D0\u4F9B\u3002\u683C\u5F0F\u3001\u7C7B\u578B\u548C\u4E0B\u8F7D\u91CF\u4E0D\u4EE3\u8868\u53EF\u4EE5\u590D\u7528\uFF0C\u8BF7\u67E5\u770B\u539F\u59CB\u6761\u76EE\u7684\u6743\u5229\u8BF4\u660E\u3002",
  readAt: "\u8BFB\u53D6\u76EE\u5F55\u65F6\u95F4\uFF1A{at}",
  toolSearch: "\u641C\u7D22 Internet Archive \u6807\u9898\u3001\u4F5C\u8005\u548C\u4E3B\u9898\uFF0C\u8FD4\u56DE\u539F\u59CB\u6761\u76EE\u94FE\u63A5",
  query: "\u6807\u9898\u3001\u4F5C\u8005\u6216\u4E3B\u9898",
  limit: "\u6700\u591A\u8FD4\u56DE\u591A\u5C11\u6761\u7ED3\u679C",
  invalid: "\u8BF7\u8F93\u5165\u6709\u6548\u7684\u6807\u9898\u3001\u4F5C\u8005\u6216\u4E3B\u9898\u3002",
  bad_answer: "\u9986\u85CF\u670D\u52A1\u8FD4\u56DE\u4E86\u65E0\u6CD5\u8BC6\u522B\u7684\u54CD\u5E94\u3002",
  not_found: "\u6CA1\u6709\u627E\u5230\u5339\u914D\u7684\u9986\u85CF\u3002",
  rate_limited: "\u9986\u85CF\u670D\u52A1\u6B63\u5728\u9650\u5236\u8BF7\u6C42\uFF0C\u8BF7\u7A0D\u540E\u91CD\u8BD5\u3002",
  slow: "\u9986\u85CF\u670D\u52A1\u54CD\u5E94\u8D85\u65F6\u3002",
  network: "\u65E0\u6CD5\u8FDE\u63A5\u9986\u85CF\u670D\u52A1\u3002",
  http: "\u9986\u85CF\u670D\u52A1\u8FD4\u56DE HTTP {0}\u3002"
};

// src/locales/ja.json
var ja_default = {
  search: "\u30A2\u30FC\u30AB\u30A4\u30D6\u3092\u691C\u7D22",
  searchHint: "\u30BF\u30A4\u30C8\u30EB\u3001\u4F5C\u8005\u3001\u4E3B\u984C",
  searchAction: "\u691C\u7D22",
  results: "\u300C{query}\u300D\u306E\u691C\u7D22\u7D50\u679C",
  account: "Internet Archive \u516C\u958B\u30AB\u30BF\u30ED\u30B0",
  loading: "\u30A2\u30FC\u30AB\u30A4\u30D6\u3092\u691C\u7D22\u3057\u3066\u3044\u307E\u3059",
  retry: "\u518D\u8A66\u884C",
  welcome: "Internet Archive \u3092\u30BF\u30A4\u30C8\u30EB\u3001\u4F5C\u8005\u3001\u4E3B\u984C\u3067\u691C\u7D22\u3057\u307E\u3059\u3002",
  empty: "\u9805\u76EE\u304C\u898B\u3064\u304B\u308A\u307E\u305B\u3093\u3002\u5225\u306E\u30BF\u30A4\u30C8\u30EB\u3001\u4F5C\u8005\u3001\u4E3B\u984C\u3092\u304A\u8A66\u3057\u304F\u3060\u3055\u3044\u3002",
  noDescription: "\u3053\u306E\u9805\u76EE\u306E\u8AAC\u660E\u306F\u3042\u308A\u307E\u305B\u3093\u3002",
  year: "{year} \u5E74",
  downloads: "{count} \u30C0\u30A6\u30F3\u30ED\u30FC\u30C9",
  open: "Internet Archive \u3067\u8868\u793A",
  attribution: "\u30E1\u30BF\u30C7\u30FC\u30BF\u306F Internet Archive \u304B\u3089\u63D0\u4F9B\u3055\u308C\u307E\u3059\u3002\u5F62\u5F0F\u3001\u7A2E\u985E\u3001\u30C0\u30A6\u30F3\u30ED\u30FC\u30C9\u6570\u306F\u518D\u5229\u7528\u306E\u8A31\u53EF\u3092\u793A\u3057\u307E\u305B\u3093\u3002\u5143\u306E\u9805\u76EE\u306E\u6A29\u5229\u60C5\u5831\u3092\u3054\u78BA\u8A8D\u304F\u3060\u3055\u3044\u3002",
  readAt: "\u30AB\u30BF\u30ED\u30B0\u53D6\u5F97\u6642\u523B\uFF1A{at}",
  toolSearch: "Internet Archive \u306E\u30BF\u30A4\u30C8\u30EB\u3001\u4F5C\u8005\u3001\u4E3B\u984C\u3092\u691C\u7D22\u3057\u3001\u9805\u76EE\u3078\u306E\u30EA\u30F3\u30AF\u3092\u8FD4\u3057\u307E\u3059",
  query: "\u30BF\u30A4\u30C8\u30EB\u3001\u4F5C\u8005\u3001\u4E3B\u984C",
  limit: "\u6700\u5927\u7D50\u679C\u6570",
  invalid: "\u6709\u52B9\u306A\u30BF\u30A4\u30C8\u30EB\u3001\u4F5C\u8005\u3001\u4E3B\u984C\u3092\u5165\u529B\u3057\u3066\u304F\u3060\u3055\u3044\u3002",
  bad_answer: "\u30A2\u30FC\u30AB\u30A4\u30D6\u304B\u3089\u4E88\u671F\u3057\u306A\u3044\u5FDC\u7B54\u304C\u3042\u308A\u307E\u3057\u305F\u3002",
  not_found: "\u4E00\u81F4\u3059\u308B\u9805\u76EE\u306F\u898B\u3064\u304B\u308A\u307E\u305B\u3093\u3067\u3057\u305F\u3002",
  rate_limited: "\u30EA\u30AF\u30A8\u30B9\u30C8\u304C\u5236\u9650\u3055\u308C\u3066\u3044\u307E\u3059\u3002\u5F8C\u3067\u3082\u3046\u4E00\u5EA6\u304A\u8A66\u3057\u304F\u3060\u3055\u3044\u3002",
  slow: "\u30A2\u30FC\u30AB\u30A4\u30D6\u306E\u5FDC\u7B54\u304C\u30BF\u30A4\u30E0\u30A2\u30A6\u30C8\u3057\u307E\u3057\u305F\u3002",
  network: "\u30A2\u30FC\u30AB\u30A4\u30D6\u306B\u63A5\u7D9A\u3067\u304D\u307E\u305B\u3093\u3002",
  http: "\u30A2\u30FC\u30AB\u30A4\u30D6\u304C HTTP {0} \u3092\u8FD4\u3057\u307E\u3057\u305F\u3002"
};

// src/i18n.ts
var textsFor = (locale = "") => locale.toLowerCase().startsWith("ja") ? ja_default : locale.toLowerCase().startsWith("zh") ? zh_Hans_default : en_default;
var fmt = (text, values) => text.replace(/\{(\w+)\}/g, (_, key) => String(values[key] ?? ""));
function errorText(error, locale = "") {
  const t = textsFor(locale);
  if (!(error instanceof ReadError)) return t.network;
  return error.code === "http" ? fmt(t.http, { 0: error.status ?? 0 }) : t[error.code];
}
function readTime(at, timeZone = "Local", locale = "en") {
  return new Intl.DateTimeFormat(locale, { year: "numeric", month: "short", day: "numeric", timeZone: timeZone === "Local" ? void 0 : timeZone }).format(at);
}

// src/service.ts
function cancellation(signal) {
  return signal.reason instanceof ReadError ? signal.reason : new DOMException("Cancelled", "AbortError");
}
function checkSignal(signal) {
  if (signal?.aborted) throw cancellation(signal);
}
function budget(parent, milliseconds) {
  const controller = new AbortController();
  const cancel = () => controller.abort(parent?.reason ?? new DOMException("Cancelled", "AbortError"));
  if (parent?.aborted) cancel();
  else parent?.addEventListener("abort", cancel, { once: true });
  const timer = setTimeout(() => controller.abort(new ReadError("slow")), milliseconds);
  return { signal: controller.signal, dispose: () => {
    clearTimeout(timer);
    parent?.removeEventListener("abort", cancel);
  } };
}
var Archive = class {
  constructor(fetch, config = {}, options = {}) {
    this.fetch = fetch;
    this.config = configFrom(config);
    this.locale = options.locale ?? "en";
    this.now = options.now ?? Date.now;
    this.monotonic = options.monotonic ?? (() => performance.now());
    this.limits = { ...limits, ...options.limits };
  }
  config;
  locale;
  now;
  monotonic;
  limits;
  cache = /* @__PURE__ */ new Map();
  bytes = 0;
  lastRequest = -Infinity;
  queue = Promise.resolve();
  lastOK;
  lastError;
  cached(path, fresh) {
    const item = this.cache.get(path);
    return item && this.monotonic() - item.used < fresh ? { raw: item.raw, at: item.at } : void 0;
  }
  save(path, raw, at) {
    const bytes = new TextEncoder().encode(JSON.stringify(raw)).byteLength;
    if (bytes > this.limits.cacheBytes) return;
    const old = this.cache.get(path);
    if (old) this.bytes -= old.bytes;
    this.cache.set(path, { raw, at, used: this.monotonic(), bytes });
    this.bytes += bytes;
    while (this.cache.size > this.limits.cache || this.bytes > this.limits.cacheBytes) {
      const oldest = this.cache.keys().next().value;
      if (oldest === void 0) break;
      this.bytes -= this.cache.get(oldest).bytes;
      this.cache.delete(oldest);
    }
  }
  async response(path, signal) {
    let response;
    try {
      response = await this.fetch(`https://archive.org${path}`, {
        method: "GET",
        headers: { Accept: "application/json", "User-Agent": "persona-archive/0.1.0" },
        signal,
        redirect: "error"
      });
    } catch (error) {
      checkSignal(signal);
      throw error instanceof ReadError ? error : new ReadError("network");
    }
    if (!response.body) throw new ReadError("bad_answer");
    const reader = response.body.getReader(), chunks = [];
    let length = 0;
    const cancel = () => {
      void reader.cancel().catch(() => {
      });
    };
    signal.addEventListener("abort", cancel, { once: true });
    try {
      while (true) {
        checkSignal(signal);
        const chunk = await reader.read();
        checkSignal(signal);
        if (chunk.done) break;
        length += chunk.value.byteLength;
        if (length > this.limits.response) {
          await reader.cancel();
          throw new ReadError("bad_answer");
        }
        chunks.push(chunk.value);
      }
    } finally {
      signal.removeEventListener("abort", cancel);
      reader.releaseLock();
    }
    const data = new Uint8Array(length);
    let offset = 0;
    for (const chunk of chunks) {
      data.set(chunk, offset);
      offset += chunk.byteLength;
    }
    let text;
    try {
      text = new TextDecoder("utf-8", { fatal: true }).decode(data);
    } catch {
      throw new ReadError("bad_answer");
    }
    if (response.status === 401 || response.status === 403 || response.status === 429) throw new ReadError("rate_limited");
    if (response.status === 404) throw new ReadError("not_found");
    if (response.status !== 200) throw new ReadError("http", response.status);
    try {
      return JSON.parse(text);
    } catch {
      throw new ReadError("bad_answer");
    }
  }
  async read(path, fresh, signal) {
    checkSignal(signal);
    const cached = this.cached(path, fresh);
    if (cached) return cached;
    const run = this.queue.then(async () => {
      const again = this.cached(path, fresh);
      if (again) return again;
      const pause = Math.max(0, this.limits.gap - (this.monotonic() - this.lastRequest));
      if (pause) await new Promise((resolve, reject) => {
        const cancel = () => {
          clearTimeout(timer);
          reject(cancellation(signal));
        };
        const timer = setTimeout(() => {
          signal?.removeEventListener("abort", cancel);
          resolve();
        }, pause);
        if (signal?.aborted) cancel();
        else signal?.addEventListener("abort", cancel, { once: true });
      });
      checkSignal(signal);
      const request = budget(signal, this.limits.request);
      try {
        const raw = await this.response(path, request.signal);
        const at = this.now();
        this.save(path, raw, at);
        this.lastOK = at;
        this.lastError = void 0;
        return { raw, at };
      } catch (error) {
        if (request.signal.aborted) throw error instanceof Error && error.name === "AbortError" ? error : new DOMException("Cancelled", "AbortError");
        const failure = error instanceof ReadError ? error : error instanceof Error && error.name === "AbortError" ? error : new ReadError("network");
        if (failure instanceof ReadError) this.lastError = { code: failure.code, at: this.monotonic() };
        throw failure;
      } finally {
        this.lastRequest = this.monotonic();
        request.dispose();
      }
    });
    this.queue = run.then(() => {
    }, () => {
    });
    try {
      return await run;
    } catch (error) {
      if (error instanceof Error && error.name === "AbortError") throw error;
      const old = this.cached(path, Infinity);
      if (old && (!(error instanceof ReadError) || error.code !== "not_found")) return { ...old, stale: error instanceof ReadError ? error : new ReadError("network") };
      throw error;
    }
  }
  status() {
    const t = textsFor(this.locale);
    return {
      state: this.lastError ? this.lastError.code === "rate_limited" ? "rate_limited" : "error" : "ok",
      account: t.account,
      ...this.lastOK === void 0 ? {} : { last_sync: new Date(this.lastOK).toISOString() },
      ...this.lastError ? { error: t[this.lastError.code] } : {}
    };
  }
  async search(query, limit = 20, refresh = false, signal) {
    const rd = await this.read(searchPath(query, this.config.media, limit), refresh ? 0 : this.limits.fresh, signal);
    return { ...parseItems(rd.raw, rd.at), stale: rd.stale };
  }
  cacheStats() {
    return { entries: this.cache.size, bytes: this.bytes };
  }
};

// src/tools.ts
function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [
    { name: "archive_search", description: t.toolSearch, input_schema: { type: "object", required: ["query"], properties: { query: { type: "string", description: t.query }, limit: { type: "integer", minimum: 1, maximum: 20, description: t.limit } } } }
  ];
}
var tools = toolsFor();
function line(item, index) {
  return `${index + 1}. ${clip(item.title, 300)}
   ${[item.creators.join(", "), item.media, item.year, `${item.downloads} downloads`].filter(Boolean).join(" \xB7 ")}
   ${item.url}${item.description ? `
   ${clip(item.description, 800)}` : ""}`;
}
function argsFrom(raw, exact) {
  if (exact) {
    const args = {};
    for (const token of exact.fieldValues("query")) {
      const value2 = JSON.parse(token.raw);
      if (typeof value2 !== "string") throw new ReadError("invalid");
      args.query = value2;
    }
    const limit = exact.int64Fields(["limit"]).limit;
    if (limit !== 0n) args.limit = limit;
    return args;
  }
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = raw;
  if (value.query != null && typeof value.query !== "string") throw new ReadError("invalid");
  if (value.limit != null && (typeof value.limit !== "number" || !Number.isSafeInteger(value.limit))) throw new ReadError("invalid");
  return value;
}
async function callTool(service, name, raw = {}, signal, exact) {
  const args = argsFrom(raw, exact), t = textsFor(service.locale), request = budget(signal, service.limits.tool);
  try {
    if (name === "archive_search") {
      if (typeof args.query !== "string") throw new ReadError("invalid");
      const limit = args.limit == null ? 10 : Number(args.limit);
      if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
      const result = await service.search(args.query, limit, false, request.signal);
      return clip(result.items.length ? result.items.map(line).join("\n").trim() : fmt(t.results, { query: args.query }) + "\n" + t.empty, 29999);
    }
    throw unknownMethod(`connector.tools.${name}`);
  } finally {
    request.dispose();
  }
}
function connector(getService) {
  return withExactParams(async (method, params, call) => {
    const service = getService();
    if (params != null && (typeof params !== "object" || Array.isArray(params))) throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
    const p = params ?? {};
    if (p.contribution != null && typeof p.contribution !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
    if (p.contribution && p.contribution !== "archive") throw noContribution(String(p.contribution));
    if (method === "status") return service.status();
    if (method === "tools.list") return { tools: toolsFor(service.locale) };
    if (method === "tools.call") {
      if (typeof p.name !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
      try {
        return { text: await callTool(service, p.name, p.args, void 0, call?.params?.field("args")) };
      } catch (error) {
        if (error instanceof ReadError) throw new ExtError(errorText(error, service.locale), error.code === "invalid" ? "invalid" : void 0);
        throw error;
      }
    }
    throw unknownMethod("connector." + method);
  });
}

// ../../sdk/ts/src/jsx-runtime.ts
var jsx = createElement;
var jsxs = createElement;

// src/page.tsx
var initialState = { q: "", found: false };
function readState(raw) {
  if (typeof raw !== "string" || raw.length > 2e3) return initialState;
  try {
    const value = JSON.parse(raw);
    if (!value || Array.isArray(value) || typeof value.q !== "string" || typeof value.found !== "boolean") return initialState;
    const q = clip(value.q.trim(), 160);
    return q ? { q, found: value.found } : initialState;
  } catch {
    return initialState;
  }
}
var encodeState = (state) => JSON.stringify(state);
function transition(state, id, values = {}) {
  let fields2;
  if (typeof values === "string") {
    try {
      values = JSON.parse(values);
    } catch {
      return state;
    }
  }
  if (!values || typeof values !== "object" || Array.isArray(values)) return state;
  fields2 = values;
  if (id === "search") {
    const query = typeof fields2.query === "string" ? clip(fields2.query.trim(), 160) : "";
    return query ? { q: query, found: true } : state;
  }
  if (id === "home") return initialState;
  return state;
}
async function loadPage(service, state, refresh, signal) {
  const request = budget(signal, service.limits.page);
  try {
    if (!state.found) return { kind: "empty" };
    return { kind: "result", result: await service.search(state.q, 20, refresh, request.signal) };
  } catch (error) {
    checkSignal(signal);
    return { kind: "error", error };
  } finally {
    request.dispose();
  }
}
function page(props) {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale), tz = props.tz ?? "Local";
  const act = (id, next, title) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "collections", state: encodeState(next), ...title ? { title } : {} } } : { id });
  const text = (key, value, style = "secondary") => /* @__PURE__ */ jsx(Text, { text: clip(value, 4e3), style }, key);
  const search = /* @__PURE__ */ jsxs(Section, { title: t.search, children: [
    /* @__PURE__ */ jsx(Field, { name: "query", label: t.search, kind: "text", placeholder: t.searchHint, value: state.q }, "archive-query"),
    /* @__PURE__ */ jsx(Button, { title: t.searchAction, action: act("search") }, "archive-submit")
  ] }, "archive-search");
  if (loaded.kind === "loading") return /* @__PURE__ */ jsxs(View, { title: t.search, state: encodeState(state), children: [
    search,
    /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(Loading, { title: t.loading }, "archive-loading-indicator") }, "archive-loading")
  ] }, "archive");
  if (loaded.kind === "error") return /* @__PURE__ */ jsxs(View, { title: t.search, state: encodeState(state), children: [
    search,
    /* @__PURE__ */ jsxs(Section, { children: [
      text("archive-error-message", errorText(loaded.error, props.locale ?? service.locale)),
      /* @__PURE__ */ jsx(Button, { title: t.retry, action: act("refresh") }, "archive-retry")
    ] }, "archive-error")
  ] }, "archive");
  if (loaded.kind === "empty") return /* @__PURE__ */ jsxs(View, { title: t.search, state: encodeState(state), children: [
    search,
    /* @__PURE__ */ jsx(Section, { children: text("archive-welcome-text", t.welcome, "body") }, "archive-welcome"),
    /* @__PURE__ */ jsx(Section, { children: text("archive-license", t.attribution, "caption") }, "archive-attribution")
  ] }, "archive");
  const result = loaded.result;
  return /* @__PURE__ */ jsxs(View, { title: fmt(t.results, { query: clip(state.q, 100) }), state: encodeState(state), children: [
    search,
    result.items.length ? result.items.map((item) => /* @__PURE__ */ jsxs(Section, { title: item.title, footer: [item.creators.join(" \xB7 "), item.media, item.year ? fmt(t.year, { year: item.year }) : "", fmt(t.downloads, { count: item.downloads.toLocaleString("en-US") })].filter(Boolean).join(" \xB7 "), children: [
      text(`description-${item.identifier}`, item.description || t.noDescription, "body"),
      /* @__PURE__ */ jsx(Link, { title: t.open, url: item.url }, `open-${item.identifier}`)
    ] }, `card-${item.identifier}`)) : /* @__PURE__ */ jsx(Section, { children: text("archive-empty", t.empty) }, "archive-empty-results"),
    /* @__PURE__ */ jsxs(Section, { footer: `${fmt(t.readAt, { at: readTime(result.at, tz, props.locale ?? service.locale) })}${result.stale ? ` \xB7 ${errorText(result.stale, props.locale ?? service.locale)}` : ""}`, children: [
      /* @__PURE__ */ jsx(Button, { title: t.retry, action: act("refresh") }, "archive-refresh"),
      /* @__PURE__ */ jsx(Button, { title: t.search, action: act("home") }, "archive-home")
    ] }, "archive-footer"),
    /* @__PURE__ */ jsx(Section, { children: text("archive-license", t.attribution, "caption") }, "archive-attribution")
  ] }, "archive");
}

// src/root.tsx
function Root(options) {
  const [state, setState] = useState(() => readState(options.state));
  const [refresh, setRefresh] = useState(0);
  const [result, setResult] = useState(void 0);
  const identity = `${state.q}:${state.found}:${refresh}`;
  useLoadEffect(async (signal) => {
    const loaded = await loadPage(options.service, state, refresh > 0, signal);
    if (!signal.aborted) setResult((previous) => previous?.service === options.service && previous.identity === identity && JSON.stringify(previous.loaded) === JSON.stringify(loaded) ? previous : { service: options.service, identity, loaded });
  }, [options.service, identity]);
  return page({
    ...options,
    state,
    loaded: result?.service === options.service && result.identity === identity ? result.loaded : { kind: "loading" },
    action: (id, next, title) => {
      if (next && (options.uiVersion ?? 0) >= 3) {
        const push = { ui: "collections", state: encodeState(next), ...title ? { title } : {} };
        return callback(() => ({ push }), { push });
      }
      return callback((values) => {
        if (id === "refresh") setRefresh((previous) => previous + 1);
        else setState((previous) => transition(previous, id, values));
      }, { submit: true });
    }
  });
}
function root(options) {
  return /* @__PURE__ */ jsx(Root, { ...options }, "archive-root");
}

// src/snapshot.ts
function snapshotPage(service) {
  return { render: async (req) => {
    const state = readState(req.state), loaded = await loadPage(service, state, false, req.signal);
    const renderer = new Renderer({ authorize: () => false });
    try {
      return structuredClone(renderer.mount(page({ service, state, loaded, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })).view);
    } finally {
      renderer.dispose();
    }
  } };
}

// src/index.ts
var sourceManifest = { ...persona_extension_default, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/archive.mjs", sha256: "" } } };
delete sourceManifest.icon;
delete sourceManifest.replaces;
function createProgram(fetch = (input, init) => globalThis.fetch(input, init), options = {}) {
  let service;
  const current = () => {
    if (!service) throw new Error("Archive has not been initialized");
    return service;
  };
  return {
    id: persona_extension_default.id,
    version: persona_extension_default.version,
    manifest: sourceManifest,
    init: (init, _hub) => {
      service = new Archive(fetch, init.config, { ...options, locale: init.hub?.locale ?? options.locale });
    },
    namespaces: { connector: connector(current) },
    ui: { collections: componentPage((req) => root({ service: current(), state: req.state, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })) }
  };
}
var index_default = createProgram();
export {
  Archive,
  ReadError,
  Root,
  budget,
  callTool,
  connector,
  createProgram,
  index_default as default,
  encodeState,
  limits,
  loadPage,
  page,
  readState,
  root,
  snapshotPage,
  sourceManifest,
  tools,
  transition
};
