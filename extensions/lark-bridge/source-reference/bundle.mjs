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
  constructor(limit2, message) {
    this.limit = limit2;
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
    for (const limit2 of Object.values(this.limits)) if (!Number.isSafeInteger(limit2) || limit2 < 1) throw new Error("invalid renderer limit");
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
    const strings = new StringBudget(this.limits.frameBytes, "UI frame limit exceeded");
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
        strings.add(value);
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
          strings.add(name);
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
      const strings = new StringBudget(this.limits.frameBytes, "UI event frame limit exceeded");
      const saved = freeze({
        generation: event.generation,
        version: event.version,
        eventId: event.eventId,
        key: event.key,
        actionId: event.actionId,
        values: this.eventValues(event.values, strings)
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
  eventValues(value, strings) {
    let count = 0;
    const ancestors = /* @__PURE__ */ new Set();
    const copy = (item, depth) => {
      if (++count > this.limits.props || depth > this.limits.depth) throw new Error("UI event value limit exceeded");
      if (typeof item === "string") {
        strings.add(item);
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
          strings.add(key);
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
var persona_extension_default = { manifest_version: 1, id: "lark-bridge", name: "Lark Bridge", version: "0.1.0", icon: "assets/icon.png", description: "Read Lark chats, messages, contacts and documents through the existing host-managed lark-cli sign-in. No second login, arbitrary commands or message writes.", publisher: "nwverse", homepage: "https://github.com/nwverse/persona-extensions/tree/main/extensions/lark-bridge", tags: ["Lark", "Feishu", "messages", "documents", "read-only"], engines: { persona: ">=202611111551", extension_protocol: 1 }, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/lark-bridge.mjs", sha256: "" } }, host_integrations: [{ id: "lark-cli", version: ">=1.0.72 <2.0.0" }], contributes: { connectors: [{ id: "larkbridge", name: "Lark Bridge", capabilities: ["status", "tools"], agent_tools: ["larkbridge_chats", "larkbridge_messages", "larkbridge_search", "larkbridge_docs"] }], ui: [{ id: "larkbridge", title: "Lark Bridge", placement: "nav_page", icon: "chat" }] } };

// src/model.ts
var clip = (value, most) => Array.from(value).length <= most ? value : Array.from(value).slice(0, most).join("").trimEnd() + "\u2026";
var oneLine = (value) => value.replace(/[\u0000-\u001f\u007f]+/gu, " ").replace(/\s+/gu, " ").trim();
var ReadError = class extends Error {
  constructor(code) {
    super(code);
    this.code = code;
  }
};
function query(value) {
  if (typeof value !== "string") throw new ReadError("invalid");
  const text2 = value.trim();
  if (!text2 || text2.length > 500 || /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/u.test(text2)) throw new ReadError("invalid");
  return text2;
}
function identifier(value, kind) {
  if (typeof value !== "string") throw new ReadError("invalid");
  const text2 = value.trim();
  const maximum = kind === "before" ? 500 : 240;
  if (!text2 || text2.length > maximum || !/^[A-Za-z0-9_.:-]+$/u.test(text2)) throw new ReadError("invalid");
  return text2;
}
function limit(value, fallback = 20) {
  if (value == null) return fallback;
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 1 || value > 50) throw new ReadError("invalid");
  return value;
}
function object(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  return value;
}
function text(value, fallback = "", most = 4e3) {
  if (value == null) return fallback;
  if (typeof value !== "string") throw new ReadError("bad_answer");
  return clip(value, most);
}
function bool(value) {
  if (typeof value !== "boolean") throw new ReadError("bad_answer");
  return value;
}
function hostResult(raw) {
  const answer = object(raw);
  if (answer.integration !== "lark-cli") throw new ReadError("bad_answer");
  return answer.result;
}
function content(value) {
  if (value == null) return "[message]";
  let data = value;
  if (typeof value === "string") {
    if (value.length > 1e5) return clip(value, 8e3);
    try {
      data = JSON.parse(value);
    } catch {
      return clip(value, 8e3);
    }
  }
  if (!data || typeof data !== "object" || Array.isArray(data)) return "[message]";
  const item = data;
  if (typeof item.text === "string") return clip(item.text, 8e3);
  if (!Array.isArray(item.content) && !Array.isArray(item.elements) && !Array.isArray(item.title) && !Array.isArray(item.body)) return "[rich message]";
  const collect = (node, depth) => {
    if (depth > 5) return [];
    if (typeof node === "string") return [node];
    if (Array.isArray(node)) return node.slice(0, 80).flatMap((child) => collect(child, depth + 1));
    if (!node || typeof node !== "object") return [];
    const value2 = node;
    if (typeof value2.text === "string") return [value2.text];
    return Object.values(value2).slice(0, 40).flatMap((child) => collect(child, depth + 1));
  };
  const result = collect(data, 0).join(" ").replace(/\s+/gu, " ").trim();
  return clip(result || "[rich message]", 8e3);
}
function parseStatus(raw) {
  const value = object(hostResult(raw));
  const loggedIn = bool(value.logged_in), expired = bool(value.expired);
  const name = clip(text(value.user_name, "lark-cli", 200), 200);
  if (!loggedIn) return { state: "not_configured", account: "", guide: ["Use the existing host-managed lark-cli sign-in. This extension cannot start another login."] };
  if (expired) return { state: "error", account: name, guide: ["The existing lark-cli authorization has expired. Renew it through the host's Lark settings."] };
  return { state: "ok", account: name, guide: [] };
}
function parseChats(raw) {
  const result = object(hostResult(raw));
  if (!Array.isArray(result.chats) || result.chats.length > 50) throw new ReadError("bad_answer");
  return result.chats.map((item) => {
    const value = object(item);
    return { id: identifier(value.chat_id, "chat"), name: clip(oneLine(text(value.name, "Chat")), 300), type: clip(text(value.chat_type), 40) };
  });
}
function parseMessages(raw) {
  const result = object(hostResult(raw));
  if (!Array.isArray(result.messages) || result.messages.length > 50) throw new ReadError("bad_answer");
  return result.messages.map((item) => {
    const value = object(item), sender = value.sender == null ? {} : object(value.sender);
    return {
      id: identifier(value.message_id, "thread"),
      chat: typeof value.chat_id === "string" && value.chat_id ? identifier(value.chat_id, "chat") : "",
      chatName: clip(oneLine(text(value.chat_name)), 300),
      sender: clip(oneLine(text(sender.name, "Unknown sender")), 200),
      text: content(value.content),
      at: clip(text(value.create_time), 80),
      type: clip(text(value.msg_type), 80),
      thread: typeof value.thread_id === "string" && value.thread_id ? identifier(value.thread_id, "thread") : ""
    };
  });
}
function publicURL(value) {
  if (typeof value !== "string" || value.length > 2e3) return "";
  try {
    const url = new URL(value);
    return url.protocol === "https:" && !url.username && !url.password && /(^|\.)(?:feishu\.cn|larksuite\.com)$/i.test(url.hostname) ? url.href : "";
  } catch {
    return "";
  }
}
function rawItems(data) {
  if (Array.isArray(data)) return data.slice(0, 50);
  const value = object(data);
  for (const key of ["results", "items", "chats", "users", "docs"]) if (Array.isArray(value[key])) return value[key].slice(0, 50);
  return [];
}
function parseSearch(raw, kind) {
  const result = hostResult(raw);
  if (kind === "messages") return { kind, messages: parseMessages({ integration: "lark-cli", result }) };
  const data = object(result).data;
  const items = rawItems(data).map((rawItem, index) => {
    const value = object(rawItem), meta = value.result_meta == null ? {} : object(value.result_meta);
    const title = text(value.title_highlighted ?? value.title ?? value.name ?? value.display_name, "Result").replace(/<\/?(?:em|b)>/gi, "");
    const detail = text(value.description ?? value.summary ?? value.chat_id ?? value.user_id ?? value.open_id ?? value.token, "");
    const url = publicURL(value.url ?? value.canonical_url ?? meta.url);
    return { id: text(value.chat_id ?? value.user_id ?? value.open_id ?? value.token, `result-${index}`, 240), title: clip(oneLine(title), 400), detail: clip(oneLine(detail), 600), url };
  });
  return { kind, items, raw: clip(JSON.stringify(data), 1e5) };
}
function errorFrom(raw) {
  const code = errorCode(raw), message = errorMessage(raw);
  if (code === "invalid" || code === "invalid_params") return new ReadError("invalid");
  if (code === "rate_limited") return new ReadError("rate_limited");
  if (code === "timeout") return new ReadError("slow");
  if (code === "unsupported" || code === "unavailable") return new ReadError("unavailable");
  if (code === "permission_denied") return new ReadError(message.includes("not_logged_in") || message.includes("expired") ? "not_logged_in" : "permission");
  return new ReadError("network");
}
function errorCode(raw) {
  return raw && typeof raw === "object" && !Array.isArray(raw) ? raw.code : void 0;
}
function errorMessage(raw) {
  return raw instanceof Error ? raw.message : raw && typeof raw === "object" && !Array.isArray(raw) && typeof raw.message === "string" ? raw.message : "";
}

// src/service.ts
var LarkBridge = class {
  constructor(hub) {
    this.hub = hub;
  }
  async call(operation, params = {}) {
    try {
      return await this.hub.call("cli.call", { operation, ...params });
    } catch (error) {
      throw errorFrom(error);
    }
  }
  async status() {
    try {
      return parseStatus(await this.call("auth_status"));
    } catch (error) {
      if (!(error instanceof ReadError)) throw error;
      return { state: error.code === "not_logged_in" ? "not_configured" : "error", account: "", guide: [] };
    }
  }
  async chats() {
    return parseChats(await this.call("chats", { limit: 30 }));
  }
  async messages(chat, before = "", count = 20) {
    return parseMessages(await this.call("messages", { chat_id: chat, ...before ? { before } : {}, limit: count }));
  }
  async thread(thread, count = 20) {
    return parseMessages(await this.call("messages", { thread, limit: count }));
  }
  async search(kind, value) {
    return parseSearch(await this.call("search", { kind, query: query(value), limit: 20 }), kind);
  }
};

// src/locales/en.json
var en_default = {
  title: "Lark Bridge",
  chatTitle: "Chat",
  topic: "Topic",
  search: "Search Lark",
  searchHint: "Messages, people, groups or documents",
  kind: "What to find",
  messages: "Messages",
  docs: "Documents",
  chats: "Groups and chats",
  people: "People",
  recent: "Recent chats",
  searchAction: "Search",
  welcome: "Find Lark context through the existing host-managed lark-cli sign-in. This extension never starts a second login.",
  readonly: "Read only: no sending, read receipts, arbitrary CLI commands or automatic Agent replies.",
  loading: "Reading through the host CLI",
  retry: "Try again",
  refresh: "Refresh",
  back: "Back to Lark Bridge",
  noResults: "No matching results were returned.",
  open: "Open the original document",
  read: "Read messages",
  thread: "Read the topic",
  readAt: "Read through the host CLI {at}",
  unknownTime: "Time not supplied",
  invalid: "Enter a valid bounded query or identifier.",
  unavailable: "The host lark-cli integration is unavailable. Enable it in the hub configuration; this extension cannot choose an executable or profile.",
  not_logged_in: "The host lark-cli is not signed in or its authorization has expired. Use the existing host Lark sign-in; no second QR login is offered here.",
  permission: "The host CLI is disabled or the existing Lark authorization lacks the required read scope. Update its configuration or authorization.",
  rate_limited: "Lark is rate-limiting reads. Try again later.",
  slow: "The host CLI took too long to respond.",
  bad_answer: "The host CLI returned an unexpected response.",
  not_found: "No matching Lark context was found.",
  network: "The host CLI could not complete the read.",
  toolChats: "List recent Lark chats through the host-managed lark-cli, read only",
  toolMessages: "Read a bounded Lark conversation or topic through the host-managed lark-cli",
  toolSearch: "Search Lark messages, chats or people through the host-managed lark-cli, read only",
  toolDocs: "Search Lark documents through the existing host-managed lark-cli sign-in, read only",
  query: "Search words",
  chat: "Chat ID returned by larkbridge_chats or larkbridge_search",
  threadArg: "Topic ID; when set, read this topic instead of the whole chat",
  before: "Opaque page token from a previous read",
  limit: "Maximum results, 1\u201350",
  kindArg: "One of messages, chats or people"
};

// src/locales/zh-Hans.json
var zh_Hans_default = {
  title: "\u98DE\u4E66 Bridge",
  chatTitle: "\u4F1A\u8BDD",
  topic: "\u8BDD\u9898",
  search: "\u641C\u7D22\u98DE\u4E66",
  searchHint: "\u6D88\u606F\u3001\u8054\u7CFB\u4EBA\u3001\u7FA4\u804A\u6216\u6587\u6863",
  kind: "\u641C\u7D22\u8303\u56F4",
  messages: "\u6D88\u606F",
  docs: "\u6587\u6863",
  chats: "\u7FA4\u804A\u548C\u4F1A\u8BDD",
  people: "\u8054\u7CFB\u4EBA",
  recent: "\u6700\u8FD1\u4F1A\u8BDD",
  searchAction: "\u641C\u7D22",
  welcome: "\u901A\u8FC7\u5BBF\u4E3B\u5DF2\u6709\u7684 lark-cli \u767B\u5F55\u8BFB\u53D6\u98DE\u4E66\u4E0A\u4E0B\u6587\uFF0C\u4E0D\u9700\u8981\u518D\u6B21\u626B\u7801\u767B\u5F55\u3002",
  readonly: "\u53EA\u8BFB\uFF1A\u4E0D\u53D1\u6D88\u606F\u3001\u4E0D\u6807\u8BB0\u5DF2\u8BFB\u3001\u4E0D\u6267\u884C\u4EFB\u610F CLI \u547D\u4EE4\uFF0C\u4E5F\u4E0D\u81EA\u52A8\u56DE\u590D Agent\u3002",
  loading: "\u6B63\u5728\u901A\u8FC7\u5BBF\u4E3B CLI \u8BFB\u53D6",
  retry: "\u91CD\u8BD5",
  refresh: "\u5237\u65B0",
  back: "\u8FD4\u56DE\u98DE\u4E66 Bridge",
  noResults: "\u6CA1\u6709\u627E\u5230\u5339\u914D\u7684\u5185\u5BB9\u3002",
  open: "\u6253\u5F00\u539F\u59CB\u6587\u6863",
  read: "\u67E5\u770B\u6D88\u606F",
  thread: "\u67E5\u770B\u8BDD\u9898",
  readAt: "\u901A\u8FC7\u5BBF\u4E3B CLI \u8BFB\u53D6\u4E8E {at}",
  unknownTime: "\u672A\u63D0\u4F9B\u65F6\u95F4",
  invalid: "\u8BF7\u8F93\u5165\u6709\u6548\u7684\u67E5\u8BE2\u6216\u6807\u8BC6\u3002",
  unavailable: "\u5BBF\u4E3B lark-cli \u96C6\u6210\u4E0D\u53EF\u7528\u3002\u8BF7\u5728 hub \u914D\u7F6E\u4E2D\u542F\u7528\uFF1B\u6269\u5C55\u4E0D\u80FD\u6307\u5B9A\u53EF\u6267\u884C\u6587\u4EF6\u6216\u914D\u7F6E\u6863\u3002",
  not_logged_in: "\u5BBF\u4E3B lark-cli \u672A\u767B\u5F55\u6216\u6388\u6743\u5DF2\u8FC7\u671F\u3002\u8BF7\u4F7F\u7528\u5BBF\u4E3B\u73B0\u6709\u7684\u98DE\u4E66\u767B\u5F55\uFF0C\u8FD9\u91CC\u4E0D\u4F1A\u8981\u6C42\u518D\u6B21\u626B\u7801\u3002",
  permission: "\u5BBF\u4E3B CLI \u672A\u542F\u7528\uFF0C\u6216\u73B0\u6709\u98DE\u4E66\u6388\u6743\u7F3A\u5C11\u6240\u9700\u7684\u8BFB\u53D6\u6743\u9650\u3002\u8BF7\u66F4\u65B0\u914D\u7F6E\u6216\u6388\u6743\u3002",
  rate_limited: "\u98DE\u4E66\u6B63\u5728\u9650\u6D41\uFF0C\u8BF7\u7A0D\u540E\u91CD\u8BD5\u3002",
  slow: "\u5BBF\u4E3B CLI \u54CD\u5E94\u8D85\u65F6\u3002",
  bad_answer: "\u5BBF\u4E3B CLI \u8FD4\u56DE\u4E86\u65E0\u6CD5\u8BC6\u522B\u7684\u7ED3\u679C\u3002",
  not_found: "\u6CA1\u6709\u627E\u5230\u76F8\u5173\u7684\u98DE\u4E66\u4E0A\u4E0B\u6587\u3002",
  network: "\u5BBF\u4E3B CLI \u65E0\u6CD5\u5B8C\u6210\u8BFB\u53D6\u3002",
  toolChats: "\u901A\u8FC7\u5BBF\u4E3B\u7BA1\u7406\u7684 lark-cli \u53EA\u8BFB\u5217\u51FA\u6700\u8FD1\u98DE\u4E66\u4F1A\u8BDD",
  toolMessages: "\u901A\u8FC7\u5BBF\u4E3B\u7BA1\u7406\u7684 lark-cli \u53EA\u8BFB\u67E5\u770B\u4E00\u6BB5\u98DE\u4E66\u4F1A\u8BDD\u6216\u8BDD\u9898",
  toolSearch: "\u901A\u8FC7\u5BBF\u4E3B\u7BA1\u7406\u7684 lark-cli \u53EA\u8BFB\u641C\u7D22\u98DE\u4E66\u6D88\u606F\u3001\u4F1A\u8BDD\u6216\u8054\u7CFB\u4EBA",
  toolDocs: "\u901A\u8FC7\u5BBF\u4E3B\u5DF2\u6709\u7684 lark-cli \u767B\u5F55\u53EA\u8BFB\u641C\u7D22\u98DE\u4E66\u6587\u6863",
  query: "\u641C\u7D22\u5173\u952E\u8BCD",
  chat: "larkbridge_chats \u6216 larkbridge_search \u8FD4\u56DE\u7684\u4F1A\u8BDD ID",
  threadArg: "\u8BDD\u9898 ID\uFF1B\u586B\u5199\u540E\u8BFB\u53D6\u8BE5\u8BDD\u9898\u800C\u4E0D\u662F\u6574\u4E2A\u4F1A\u8BDD",
  before: "\u4E0A\u6B21\u8BFB\u53D6\u8FD4\u56DE\u7684\u5206\u9875\u6E38\u6807",
  limit: "\u6700\u591A\u8FD4\u56DE\u591A\u5C11\u6761\uFF0C1\u201350",
  kindArg: "messages\u3001chats \u6216 people \u4E4B\u4E00"
};

// src/locales/ja.json
var ja_default = {
  title: "Lark Bridge",
  chatTitle: "\u30C1\u30E3\u30C3\u30C8",
  topic: "\u30C8\u30D4\u30C3\u30AF",
  search: "Lark\u3092\u691C\u7D22",
  searchHint: "\u30E1\u30C3\u30BB\u30FC\u30B8\u3001\u9023\u7D61\u5148\u3001\u30B0\u30EB\u30FC\u30D7\u3001\u30C9\u30AD\u30E5\u30E1\u30F3\u30C8",
  kind: "\u691C\u7D22\u5BFE\u8C61",
  messages: "\u30E1\u30C3\u30BB\u30FC\u30B8",
  docs: "\u30C9\u30AD\u30E5\u30E1\u30F3\u30C8",
  chats: "\u30B0\u30EB\u30FC\u30D7\u3068\u30C1\u30E3\u30C3\u30C8",
  people: "\u9023\u7D61\u5148",
  recent: "\u6700\u8FD1\u306E\u30C1\u30E3\u30C3\u30C8",
  searchAction: "\u691C\u7D22",
  welcome: "\u30DB\u30B9\u30C8\u3067\u7BA1\u7406\u3055\u308C\u3066\u3044\u308B\u65E2\u5B58\u306Elark-cli\u30ED\u30B0\u30A4\u30F3\u3092\u4F7F\u3063\u3066Lark\u306E\u30B3\u30F3\u30C6\u30AD\u30B9\u30C8\u3092\u691C\u7D22\u3057\u307E\u3059\u3002\u518D\u30ED\u30B0\u30A4\u30F3\u306F\u4E0D\u8981\u3067\u3059\u3002",
  readonly: "\u8AAD\u307F\u53D6\u308A\u5C02\u7528\u3067\u3059\u3002\u9001\u4FE1\u3001\u65E2\u8AAD\u5316\u3001\u4EFB\u610F\u306ECLI\u30B3\u30DE\u30F3\u30C9\u3001Agent\u306E\u81EA\u52D5\u8FD4\u4FE1\u306F\u884C\u3044\u307E\u305B\u3093\u3002",
  loading: "\u30DB\u30B9\u30C8CLI\u3067\u8AAD\u307F\u8FBC\u3093\u3067\u3044\u307E\u3059",
  retry: "\u518D\u8A66\u884C",
  refresh: "\u66F4\u65B0",
  back: "Lark Bridge\u306B\u623B\u308B",
  noResults: "\u4E00\u81F4\u3059\u308B\u7D50\u679C\u306F\u3042\u308A\u307E\u305B\u3093\u3002",
  open: "\u5143\u306E\u30C9\u30AD\u30E5\u30E1\u30F3\u30C8\u3092\u958B\u304F",
  read: "\u30E1\u30C3\u30BB\u30FC\u30B8\u3092\u8AAD\u3080",
  thread: "\u30C8\u30D4\u30C3\u30AF\u3092\u8AAD\u3080",
  readAt: "\u30DB\u30B9\u30C8CLI\u3067{at}\u306B\u8AAD\u307F\u8FBC\u307F",
  unknownTime: "\u6642\u523B\u60C5\u5831\u306A\u3057",
  invalid: "\u6709\u52B9\u306A\u691C\u7D22\u8A9E\u307E\u305F\u306FID\u3092\u5165\u529B\u3057\u3066\u304F\u3060\u3055\u3044\u3002",
  unavailable: "\u30DB\u30B9\u30C8\u306Elark-cli\u9023\u643A\u3092\u5229\u7528\u3067\u304D\u307E\u305B\u3093\u3002hub\u306E\u8A2D\u5B9A\u3067\u6709\u52B9\u306B\u3057\u3066\u304F\u3060\u3055\u3044\u3002\u62E1\u5F35\u6A5F\u80FD\u304C\u5B9F\u884C\u30D5\u30A1\u30A4\u30EB\u3084\u30D7\u30ED\u30D5\u30A1\u30A4\u30EB\u3092\u9078\u3076\u3053\u3068\u306F\u3067\u304D\u307E\u305B\u3093\u3002",
  not_logged_in: "\u30DB\u30B9\u30C8\u306Elark-cli\u306B\u30ED\u30B0\u30A4\u30F3\u3057\u3066\u3044\u306A\u3044\u304B\u3001\u8A8D\u8A3C\u306E\u671F\u9650\u304C\u5207\u308C\u3066\u3044\u307E\u3059\u3002\u65E2\u5B58\u306E\u30DB\u30B9\u30C8Lark\u30ED\u30B0\u30A4\u30F3\u3092\u4F7F\u7528\u3057\u3066\u304F\u3060\u3055\u3044\u3002\u65B0\u305F\u306AQR\u30ED\u30B0\u30A4\u30F3\u306F\u4E0D\u8981\u3067\u3059\u3002",
  permission: "\u30DB\u30B9\u30C8CLI\u304C\u7121\u52B9\u304B\u3001\u65E2\u5B58\u306ELark\u8A8D\u8A3C\u306B\u5FC5\u8981\u306A\u8AAD\u307F\u53D6\u308A\u6A29\u9650\u304C\u3042\u308A\u307E\u305B\u3093\u3002\u8A2D\u5B9A\u307E\u305F\u306F\u8A8D\u8A3C\u3092\u66F4\u65B0\u3057\u3066\u304F\u3060\u3055\u3044\u3002",
  rate_limited: "Lark\u306E\u8AAD\u307F\u53D6\u308A\u304C\u5236\u9650\u3055\u308C\u3066\u3044\u307E\u3059\u3002\u5F8C\u3067\u3082\u3046\u4E00\u5EA6\u304A\u8A66\u3057\u304F\u3060\u3055\u3044\u3002",
  slow: "\u30DB\u30B9\u30C8CLI\u306E\u5FDC\u7B54\u304C\u30BF\u30A4\u30E0\u30A2\u30A6\u30C8\u3057\u307E\u3057\u305F\u3002",
  bad_answer: "\u30DB\u30B9\u30C8CLI\u304B\u3089\u4E88\u671F\u3057\u306A\u3044\u5FDC\u7B54\u304C\u8FD4\u3055\u308C\u307E\u3057\u305F\u3002",
  not_found: "\u4E00\u81F4\u3059\u308BLark\u30B3\u30F3\u30C6\u30AD\u30B9\u30C8\u304C\u898B\u3064\u304B\u308A\u307E\u305B\u3093\u3002",
  network: "\u30DB\u30B9\u30C8CLI\u3067\u8AAD\u307F\u8FBC\u3081\u307E\u305B\u3093\u3067\u3057\u305F\u3002",
  toolChats: "\u30DB\u30B9\u30C8\u7BA1\u7406\u306Elark-cli\u3067\u6700\u8FD1\u306ELark\u30C1\u30E3\u30C3\u30C8\u3092\u8AAD\u307F\u53D6\u308A\u5C02\u7528\u3067\u4E00\u89A7\u8868\u793A",
  toolMessages: "\u30DB\u30B9\u30C8\u7BA1\u7406\u306Elark-cli\u3067Lark\u306E\u4F1A\u8A71\u3084\u30C8\u30D4\u30C3\u30AF\u3092\u8AAD\u307F\u53D6\u308A\u5C02\u7528\u3067\u8868\u793A",
  toolSearch: "\u30DB\u30B9\u30C8\u7BA1\u7406\u306Elark-cli\u3067Lark\u306E\u30E1\u30C3\u30BB\u30FC\u30B8\u3001\u30C1\u30E3\u30C3\u30C8\u3001\u9023\u7D61\u5148\u3092\u8AAD\u307F\u53D6\u308A\u5C02\u7528\u3067\u691C\u7D22",
  toolDocs: "\u30DB\u30B9\u30C8\u306E\u65E2\u5B58\u306Elark-cli\u30ED\u30B0\u30A4\u30F3\u3067Lark\u30C9\u30AD\u30E5\u30E1\u30F3\u30C8\u3092\u8AAD\u307F\u53D6\u308A\u5C02\u7528\u3067\u691C\u7D22",
  query: "\u691C\u7D22\u30AD\u30FC\u30EF\u30FC\u30C9",
  chat: "larkbridge_chats\u307E\u305F\u306Flarkbridge_search\u304C\u8FD4\u3057\u305F\u30C1\u30E3\u30C3\u30C8ID",
  threadArg: "\u30C8\u30D4\u30C3\u30AFID\u3002\u6307\u5B9A\u3059\u308B\u3068\u4F1A\u8A71\u5168\u4F53\u3067\u306F\u306A\u304F\u30C8\u30D4\u30C3\u30AF\u3092\u8AAD\u307F\u307E\u3059",
  before: "\u524D\u56DE\u306E\u8AAD\u307F\u8FBC\u307F\u3067\u8FD4\u3055\u308C\u305F\u30DA\u30FC\u30B8\u30C8\u30FC\u30AF\u30F3",
  limit: "\u6700\u5927\u7D50\u679C\u6570\uFF081\u301C50\uFF09",
  kindArg: "messages\u3001chats\u3001people\u306E\u3044\u305A\u308C\u304B"
};

// src/i18n.ts
var textsFor = (locale = "") => locale.toLowerCase().startsWith("ja") ? ja_default : locale.toLowerCase().startsWith("zh") ? zh_Hans_default : en_default;
var fmt = (text2, values) => text2.replace(/\{(\w+)\}/g, (_, key) => String(values[key] ?? ""));
function errorText(error, locale = "") {
  const t = textsFor(locale);
  return error instanceof ReadError ? t[error.code] : t.network;
}
function readTime(value, locale = "en") {
  if (!value) return textsFor(locale).unknownTime;
  const milliseconds = /^\d+$/.test(value) ? Number(value) * (value.length <= 10 ? 1e3 : 1) : Date.parse(value);
  if (!Number.isFinite(milliseconds) || milliseconds < 0 || milliseconds > 864e13) return textsFor(locale).unknownTime;
  try {
    return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(new Date(milliseconds));
  } catch {
    return new Intl.DateTimeFormat(locale, { dateStyle: "medium" }).format(new Date(milliseconds));
  }
}

// src/tools.ts
function toolsFor(locale = "en") {
  const t = textsFor(locale), count = { type: "integer", minimum: 1, maximum: 50, description: t.limit };
  return [
    { name: "larkbridge_chats", description: t.toolChats, input_schema: { type: "object", additionalProperties: false, properties: { limit: count } } },
    { name: "larkbridge_messages", description: t.toolMessages, input_schema: { type: "object", additionalProperties: false, properties: { chat: { type: "string", description: t.chat }, thread: { type: "string", description: t.threadArg }, before: { type: "string", description: t.before }, limit: count } } },
    { name: "larkbridge_search", description: t.toolSearch, input_schema: { type: "object", required: ["query", "kind"], additionalProperties: false, properties: { query: { type: "string", description: t.query }, kind: { type: "string", enum: ["messages", "chats", "people"], description: t.kindArg } } } },
    { name: "larkbridge_docs", description: t.toolDocs, input_schema: { type: "object", required: ["query"], additionalProperties: false, properties: { query: { type: "string", description: t.query } } } }
  ];
}
var tools = toolsFor();
var messageText = (message) => `${message.chatName ? message.chatName + "\n" : ""}${message.sender} \xB7 ${message.at}
${message.text}
Message: ${message.id}${message.chat ? `
Chat: ${message.chat}` : ""}${message.thread ? `
Topic: ${message.thread}` : ""}`;
function searchText(result) {
  if (result.kind === "messages") return result.messages.map(messageText).join("\n\n---\n\n") || "No results.";
  return result.items.map((item) => `${item.title}
${item.detail}${item.url ? `
${item.url}` : ""}`).join("\n\n---\n\n") || "No results.";
}
function argsFrom(raw) {
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const args = raw;
  for (const key of Object.keys(args)) if (!["chat", "thread", "before", "limit", "query", "kind"].includes(key)) throw new ReadError("invalid");
  return args;
}
async function callTool(service, name, raw = {}) {
  const args = argsFrom(raw);
  if (name === "larkbridge_chats" || name === "larkbridge_messages" || name === "larkbridge_search" || name === "larkbridge_docs") {
    if (name === "larkbridge_chats" && Object.keys(args).some((key) => key !== "limit")) throw new ReadError("invalid");
    if (name === "larkbridge_messages" && Object.keys(args).some((key) => !["chat", "thread", "before", "limit"].includes(key))) throw new ReadError("invalid");
    if (name === "larkbridge_search" && Object.keys(args).some((key) => !["query", "kind"].includes(key))) throw new ReadError("invalid");
    if (name === "larkbridge_docs" && Object.keys(args).some((key) => key !== "query")) throw new ReadError("invalid");
  } else throw unknownMethod(`connector.tools.${name}`);
  if (name === "larkbridge_chats") return (await service.chats()).map((chat) => `${chat.name}
Chat: ${chat.id}${chat.type ? `
Type: ${chat.type}` : ""}`).join("\n\n") || "No chats.";
  if (name === "larkbridge_messages") {
    const thread = args.thread == null || args.thread === "" ? "" : identifier(args.thread, "thread");
    const count = limit(args.limit);
    const rows = thread ? await service.thread(thread, count) : await service.messages(identifier(args.chat, "chat"), args.before == null || args.before === "" ? "" : identifier(args.before, "before"), count);
    return rows.map(messageText).join("\n\n---\n\n") || "No messages.";
  }
  if (name === "larkbridge_docs") return searchText(await service.search("docs", query(args.query)));
  if (name === "larkbridge_search") {
    if (args.kind !== "messages" && args.kind !== "chats" && args.kind !== "people") throw new ReadError("invalid");
    return searchText(await service.search(args.kind, query(args.query)));
  }
  throw unknownMethod(`connector.tools.${name}`);
}
function connector(getService, getLocale = () => "en") {
  return withExactParams(async (method, params) => {
    const service = getService(), locale = getLocale();
    if (params != null && (typeof params !== "object" || Array.isArray(params))) throw new ExtError(errorText(new ReadError("invalid"), locale), "invalid");
    const p = params ?? {};
    if (p.contribution != null && typeof p.contribution !== "string") throw new ExtError(errorText(new ReadError("invalid"), locale), "invalid");
    if (p.contribution && p.contribution !== "larkbridge") throw noContribution(String(p.contribution));
    if (method === "status") return service.status();
    if (method === "tools.list") return { tools: toolsFor(locale) };
    if (method === "tools.call") {
      if (typeof p.name !== "string") throw new ExtError(errorText(new ReadError("invalid"), locale), "invalid");
      try {
        return { text: await callTool(service, p.name, p.args) };
      } catch (error) {
        if (error instanceof ReadError) throw new ExtError(errorText(error, locale), error.code === "invalid" ? "invalid" : void 0);
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
var initialState = { mode: "home" };
function readState(raw) {
  if (typeof raw !== "string" || raw.length > 3e3) return initialState;
  try {
    const value = JSON.parse(raw);
    if (!value || Array.isArray(value) || typeof value.mode !== "string") return initialState;
    if (value.mode === "home") return initialState;
    if (value.mode === "search" && ["messages", "docs", "chats", "people"].includes(String(value.kind))) return { mode: "search", kind: value.kind, q: query(value.q) };
    if (value.mode === "chat") return { mode: "chat", chat: identifier(value.chat, "chat"), title: clip(String(value.title ?? "Chat"), 300) };
    if (value.mode === "thread") return { mode: "thread", thread: identifier(value.thread, "thread"), title: clip(String(value.title ?? "Topic"), 300) };
  } catch {
  }
  return initialState;
}
var encodeState = (state) => JSON.stringify(state);
function transition(state, id, values = {}, locale = "en") {
  const t = textsFor(locale);
  if (id === "home") return initialState;
  if (id.startsWith("chat:")) {
    try {
      return { mode: "chat", chat: identifier(id.slice(5), "chat"), title: t.chatTitle };
    } catch {
      return state;
    }
  }
  if (id.startsWith("thread:")) {
    try {
      return { mode: "thread", thread: identifier(id.slice(7), "thread"), title: t.topic };
    } catch {
      return state;
    }
  }
  if (id !== "search" || !values || typeof values !== "object" || Array.isArray(values)) return state;
  const fields2 = values;
  const kind = fields2.kind;
  if (kind !== "messages" && kind !== "docs" && kind !== "chats" && kind !== "people") return state;
  try {
    return { mode: "search", kind, q: query(fields2.query) };
  } catch {
    return state;
  }
}
async function loadPage(service, state, signal, at = /* @__PURE__ */ new Date()) {
  try {
    signal?.throwIfAborted();
    if (state.mode === "home") return { kind: "home", chats: await service.chats() };
    if (state.mode === "search") return { kind: "search", result: await service.search(state.kind, state.q) };
    if (state.mode === "chat") return { kind: "messages", messages: await service.messages(state.chat), at: at.toISOString() };
    return { kind: "messages", messages: await service.thread(state.thread), at: at.toISOString() };
  } catch (error) {
    signal?.throwIfAborted();
    return { kind: "error", error };
  }
}
function page(props) {
  const { state, loaded, locale = "en" } = props, t = textsFor(locale);
  const act = (id, next, title) => props.action?.(id, next, title) ?? (next && (props.uiVersion ?? 0) >= 3 ? { id, push: { ui: "larkbridge", state: encodeState(next), ...title ? { title } : {} } } : { id: next?.mode === "chat" ? `chat:${next.chat}` : next?.mode === "thread" ? `thread:${next.thread}` : id });
  const text2 = (key, value, style = "secondary") => /* @__PURE__ */ jsx(Text, { text: clip(value, 8e3), style }, key);
  const search = /* @__PURE__ */ jsxs(Section, { title: t.search, children: [
    /* @__PURE__ */ jsx(Field, { name: "query", label: t.search, kind: "text", placeholder: t.searchHint, value: state.mode === "search" ? state.q : "" }, "larkbridge-query"),
    /* @__PURE__ */ jsx(Field, { name: "kind", label: t.kind, kind: "choice", value: state.mode === "search" ? state.kind : "messages", choices: [{ value: "messages", title: t.messages }, { value: "docs", title: t.docs }, { value: "chats", title: t.chats }, { value: "people", title: t.people }] }, "larkbridge-kind"),
    /* @__PURE__ */ jsx(Button, { title: t.searchAction, action: act("search") }, "larkbridge-submit")
  ] }, "larkbridge-search");
  const home = state.mode !== "home" ? /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(Button, { title: t.back, action: act("home") }, "larkbridge-back") }, "larkbridge-home") : null;
  if (loaded.kind === "loading") return /* @__PURE__ */ jsxs(View, { title: t.title, state: encodeState(state), children: [
    search,
    home,
    /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(Loading, { title: t.loading }, "larkbridge-loading-indicator") }, "larkbridge-loading")
  ] }, "larkbridge");
  if (loaded.kind === "error") return /* @__PURE__ */ jsxs(View, { title: t.title, state: encodeState(state), children: [
    search,
    home,
    /* @__PURE__ */ jsxs(Section, { children: [
      text2("larkbridge-error-message", errorText(loaded.error, locale)),
      /* @__PURE__ */ jsx(Button, { title: t.retry, action: act("refresh") }, "larkbridge-retry")
    ] }, "larkbridge-error")
  ] }, "larkbridge");
  if (loaded.kind === "home") return /* @__PURE__ */ jsxs(View, { title: t.title, state: encodeState(state), children: [
    search,
    /* @__PURE__ */ jsxs(Section, { children: [
      text2("larkbridge-welcome-text", t.welcome, "body"),
      text2("larkbridge-readonly", t.readonly, "caption")
    ] }, "larkbridge-welcome"),
    /* @__PURE__ */ jsx(Section, { title: t.recent }, "larkbridge-recent"),
    loaded.chats.map((chat) => /* @__PURE__ */ jsxs(Section, { title: chat.name, footer: chat.type, children: [
      text2(`chat-id-${chat.id}`, chat.id),
      /* @__PURE__ */ jsx(Button, { title: t.read, action: act("chat", { mode: "chat", chat: chat.id, title: chat.name }, chat.name) }, `read-${chat.id}`)
    ] }, `chat-${chat.id}`)),
    !loaded.chats.length ? /* @__PURE__ */ jsx(Section, { children: text2("larkbridge-empty-text", t.noResults) }, "larkbridge-empty") : null
  ] }, "larkbridge");
  if (loaded.kind === "search") {
    const result = loaded.result;
    return /* @__PURE__ */ jsxs(View, { title: state.mode === "search" ? state.q : t.title, state: encodeState(state), children: [
      search,
      home,
      result.kind === "messages" ? messageSections(result.messages, t, act, text2) : result.items.map((item, index) => /* @__PURE__ */ jsxs(Section, { title: item.title, children: [
        item.detail ? text2(`detail-${index}`, item.detail, "body") : null,
        item.url ? /* @__PURE__ */ jsx(Link, { title: t.open, url: item.url }, `link-${index}`) : null,
        result.kind === "chats" ? /* @__PURE__ */ jsx(Button, { title: t.read, action: act("chat", { mode: "chat", chat: item.id, title: item.title }, item.title) }, `read-${index}`) : null
      ] }, `result-${index}-${item.id}`)),
      result.kind === "messages" && !result.messages.length || result.kind !== "messages" && !result.items.length ? /* @__PURE__ */ jsx(Section, { children: text2("larkbridge-no-results-text", t.noResults) }, "larkbridge-no-results") : null,
      /* @__PURE__ */ jsxs(Section, { children: [
        /* @__PURE__ */ jsx(Button, { title: t.refresh, action: act("refresh") }, "larkbridge-refresh"),
        text2("larkbridge-footer-readonly", t.readonly, "caption")
      ] }, "larkbridge-footer")
    ] }, "larkbridge");
  }
  return /* @__PURE__ */ jsxs(View, { title: state.mode === "chat" || state.mode === "thread" ? state.title : t.title, state: encodeState(state), children: [
    search,
    home,
    messageSections(loaded.messages, t, act, text2),
    !loaded.messages.length ? /* @__PURE__ */ jsx(Section, { children: text2("larkbridge-no-messages-text", t.noResults) }, "larkbridge-no-messages") : null,
    /* @__PURE__ */ jsx(Section, { footer: fmt(t.readAt, { at: readTime(loaded.at, locale) }), children: /* @__PURE__ */ jsx(Button, { title: t.refresh, action: act("refresh") }, "larkbridge-refresh") }, "larkbridge-footer")
  ] }, "larkbridge");
}
function messageSections(messages, t, act, text2) {
  return messages.map((message, index) => /* @__PURE__ */ jsxs(Section, { title: message.sender, footer: readTime(message.at), children: [
    message.chatName ? text2(`chat-name-${index}`, message.chatName) : null,
    text2(`body-${index}`, message.text, "body"),
    text2(`id-${index}`, message.id, "caption"),
    message.thread ? /* @__PURE__ */ jsx(Button, { title: t.thread, action: act("thread", { mode: "thread", thread: message.thread, title: message.chatName || t.thread }, message.chatName || t.thread) }, `thread-${index}`) : null
  ] }, `message-${index}-${message.id}`));
}

// src/root.tsx
function Root(options) {
  const [state, setState] = useState(() => readState(options.state));
  const [refresh, setRefresh] = useState(0);
  const [result, setResult] = useState(void 0);
  const identity = `${JSON.stringify(state)}:${refresh}`;
  useLoadEffect(async (signal) => {
    const loaded = await loadPage(options.service, state, signal);
    if (!signal.aborted) setResult((previous) => previous?.service === options.service && previous.identity === identity && JSON.stringify(previous.loaded) === JSON.stringify(loaded) ? previous : { service: options.service, identity, loaded });
  }, [options.service, identity]);
  return page({
    ...options,
    state,
    loaded: result?.service === options.service && result.identity === identity ? result.loaded : { kind: "loading" },
    action: (id, next, title) => {
      if (next && (options.uiVersion ?? 0) >= 3) {
        const push = { ui: "larkbridge", state: encodeState(next), ...title ? { title } : {} };
        return callback(() => {
          setState(next);
          return { push };
        }, { push });
      }
      return callback((values) => {
        if (id === "refresh") setRefresh((previous) => previous + 1);
        else if (next) setState(next);
        else setState((previous) => transition(previous, id, values, options.locale));
      }, { submit: true });
    }
  });
}
function root(options) {
  return /* @__PURE__ */ jsx(Root, { ...options }, "larkbridge-root");
}

// src/snapshot.ts
function snapshotPage(service) {
  return { render: async (req) => {
    const state = readState(req.state), loaded = await loadPage(service, state, req.signal);
    const renderer = new Renderer({ authorize: () => false });
    try {
      return structuredClone(renderer.mount(page({ service, state, loaded, locale: req.locale, uiVersion: req.ui_version })).view);
    } finally {
      renderer.dispose();
    }
  } };
}

// src/index.ts
var sourceManifest = { ...persona_extension_default, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/lark-bridge.mjs", sha256: "" } } };
delete sourceManifest.icon;
delete sourceManifest.replaces;
function createProgram() {
  let service;
  let locale = "en";
  const current = () => {
    if (!service) throw new Error("Lark Bridge has not been initialized");
    return service;
  };
  return {
    id: persona_extension_default.id,
    version: persona_extension_default.version,
    manifest: sourceManifest,
    init: (init, hub) => {
      service = new LarkBridge(hub);
      locale = init.hub?.locale ?? "en";
    },
    namespaces: { connector: connector(current, () => locale) },
    ui: { larkbridge: componentPage((req) => root({ service: current(), state: req.state, locale: req.locale, uiVersion: req.ui_version })) }
  };
}
var index_default = createProgram();
export {
  LarkBridge,
  Root,
  callTool,
  connector,
  createProgram,
  index_default as default,
  encodeState,
  loadPage,
  page,
  readState,
  root,
  snapshotPage,
  sourceManifest,
  tools,
  transition
};
