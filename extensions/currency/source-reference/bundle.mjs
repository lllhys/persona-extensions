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
var persona_extension_default = { manifest_version: 1, id: "currency", name: "\u6C47\u7387\u548C\u5355\u4F4D\u6362\u7B97", version: "0.1.1", icon: "assets/icon.png", description: "\u6362\u7B97\u5E38\u7528\u8D27\u5E01\u4E0E\u65E5\u5E38\u5355\u4F4D\u3002\u6C47\u7387\u4F7F\u7528\u6CE8\u660E\u65E5\u671F\u7684\u6B27\u6D32\u592E\u884C\u53C2\u8003\u6570\u636E\uFF1B\u957F\u5EA6\u3001\u8D28\u91CF\u3001\u6E29\u5EA6\u3001\u4F53\u79EF\u3001\u9762\u79EF\u3001\u901F\u5EA6\u3001\u65F6\u957F\u548C\u6570\u636E\u5355\u4F4D\u5728 hub \u672C\u5730\u6362\u7B97\u3002", publisher: "nwverse", homepage: "https://github.com/nwverse/persona-extensions/blob/main/extensions/currency/README.md", tags: ["\u8D27\u5E01", "\u6C47\u7387", "\u5355\u4F4D\u6362\u7B97"], engines: { plugin_protocol: 1 }, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/currency.mjs", sha256: "" } }, contributes: { connectors: [{ id: "currency", name: "Currency and units", capabilities: ["status", "tools"], hosts: ["api.frankfurter.dev"], agent_tools: ["currency_*", "unit_convert"] }], ui: [{ id: "converter", title: "\u6C47\u7387\u548C\u5355\u4F4D\u6362\u7B97", placement: "nav_page", icon: "money" }] }, permissions: { network: ["api.frankfurter.dev"] } };

// src/model.ts
var limits = Object.freeze({ request: 12e3, page: 15e3, tool: 15e3, response: 256 << 10, fresh: 6 * 60 * 6e4, cache: 32, cacheBytes: 256 << 10, gap: 200 });
var ReadError = class extends Error {
  constructor(code, status) {
    super(code);
    this.code = code;
    this.status = status;
  }
};
var clip = (text2, most) => text2.length <= most ? text2 : text2.slice(0, most).trimEnd() + "\u2026";
var oneLine = (text2) => text2.replace(/\s+/gu, " ").trim();
function text(value, most) {
  if (typeof value !== "string" || /[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  const result = oneLine(value);
  if (!result || Array.from(result).length > most) throw new ReadError("invalid");
  return result;
}
var currencies = ["AUD", "BRL", "CAD", "CHF", "CNY", "CZK", "DKK", "EUR", "GBP", "HKD", "HUF", "IDR", "ILS", "INR", "ISK", "JPY", "KRW", "MXN", "MYR", "NOK", "NZD", "PHP", "PLN", "RON", "SEK", "SGD", "THB", "TRY", "USD", "ZAR"];
function currency(value) {
  const code = text(value, 3).toUpperCase();
  if (!currencies.includes(code)) throw new ReadError("unsupported");
  return code;
}
function ratesPath(base, quote) {
  return `/v2/providers/ecb/rates?${new URLSearchParams({ base: base.toLowerCase(), quotes: quote.toLowerCase() })}`;
}
function object(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  return value;
}
function decimal(value, name) {
  if (typeof value === "number" && (!Number.isFinite(value) || !/^[0-9]+(?:\.[0-9]+)?$/.test(String(value)))) throw new ReadError(name === "amount" ? "invalid" : "bad_answer");
  if (typeof value !== "string" && typeof value !== "number") throw new ReadError(name === "amount" ? "invalid" : "bad_answer");
  const raw = typeof value === "string" ? value : String(value);
  if (/^[\t\n\v\f\r ]/.test(raw) || /[\t\n\v\f\r ]$/.test(raw) || /[\u0000-\u001f\u007f]/u.test(raw)) throw new ReadError(name === "amount" ? "invalid" : "bad_answer");
  if (!/^[0-9]{1,18}(?:\.[0-9]{1,18})?$/.test(raw)) throw new ReadError(name === "amount" ? "invalid" : "bad_answer");
  const [whole, fraction2 = ""] = raw.split(".");
  const normalized = `${whole.replace(/^0+(?=\d)/, "")}${fraction2 ? `.${fraction2}` : ""}`;
  if (name === "rate" && normalized === "0") throw new ReadError("bad_answer");
  return normalized;
}
function amount(value) {
  return decimal(value, "amount");
}
function parseRate(raw, base, quote, at) {
  const rows = Array.isArray(raw) ? raw : (() => {
    throw new ReadError("bad_answer");
  })();
  if (rows.length !== 1) throw new ReadError("bad_answer");
  const row = object(rows[0]);
  if (row.base?.toString().toUpperCase() !== base || row.quote?.toString().toUpperCase() !== quote) throw new ReadError("bad_answer");
  if (typeof row.date !== "string" || !/^\d{4}-\d{2}-\d{2}$/.test(row.date) || Number.isNaN(Date.parse(row.date + "T00:00:00Z")) || (/* @__PURE__ */ new Date(row.date + "T00:00:00Z")).toISOString().slice(0, 10) !== row.date) throw new ReadError("bad_answer");
  return { base, quote, rate: decimal(row.rate, "rate"), date: row.date, at };
}
function decimalParts(raw) {
  const [whole, fraction2 = ""] = raw.split(".");
  return { coefficient: BigInt(whole + fraction2), scale: fraction2.length };
}
function multiply(a, b) {
  const x = decimalParts(a), y = decimalParts(b), negative = x.coefficient < 0n !== y.coefficient < 0n;
  const abs = (n) => n < 0n ? -n : n;
  const digits = (abs(x.coefficient) * abs(y.coefficient)).toString().padStart(x.scale + y.scale + 1, "0");
  const split = digits.length - x.scale - y.scale;
  const fraction2 = digits.slice(split).replace(/0+$/, "");
  return (negative ? "-" : "") + digits.slice(0, split).replace(/^0+(?=\d)/, "") + (fraction2 ? `.${fraction2}` : "");
}
function roundedNumber(value, maximumDigits = 6) {
  const parts = decimalParts(value);
  return rationalText(reduce(parts.coefficient, 10n ** BigInt(parts.scale)), maximumDigits);
}
function formatForDisplay(value, locale = "en") {
  const rounded = roundedNumber(value, 6);
  const [whole, fraction2 = ""] = rounded.split(".");
  if (whole.replace("-", "").length > 15) return rounded;
  return new Intl.NumberFormat(locale, { maximumFractionDigits: Math.min(6, fraction2.length) }).format(Number(rounded));
}
function currencyConversion(value, rate) {
  return { amount: amount(value), from: rate.base, to: rate.quote, result: multiply(amount(value), rate.rate) };
}
var dimensions = ["length", "mass", "temperature", "volume", "area", "speed", "duration", "data"];
var exact = (scale) => ({ numerator: scale, denominator: "1" });
var fraction = (numerator, denominator) => ({ numerator, denominator });
var units = [
  { dimension: "length", symbol: "mm", ...fraction("1", "1000"), aliases: ["millimeter", "millimeters", "\u6BEB\u7C73"] },
  { dimension: "length", symbol: "cm", ...fraction("1", "100"), aliases: ["centimeter", "centimeters", "\u5398\u7C73"] },
  { dimension: "length", symbol: "m", ...exact("1"), aliases: ["meter", "meters", "metre", "\u7C73"] },
  { dimension: "length", symbol: "km", ...exact("1000"), aliases: ["kilometer", "kilometers", "\u5343\u7C73", "\u516C\u91CC"] },
  { dimension: "length", symbol: "in", ...fraction("254", "10000"), aliases: ["inch", "inches", "\u82F1\u5BF8"] },
  { dimension: "length", symbol: "ft", ...fraction("3048", "10000"), aliases: ["foot", "feet", "\u82F1\u5C3A"] },
  { dimension: "length", symbol: "yd", ...fraction("9144", "10000"), aliases: ["yard", "yards", "\u7801"] },
  { dimension: "length", symbol: "mi", ...fraction("1609344", "1000"), aliases: ["mile", "miles", "\u82F1\u91CC"] },
  { dimension: "mass", symbol: "mg", ...fraction("1", "1000000"), aliases: ["milligram", "milligrams", "\u6BEB\u514B"] },
  { dimension: "mass", symbol: "g", ...fraction("1", "1000"), aliases: ["gram", "grams", "\u514B"] },
  { dimension: "mass", symbol: "kg", ...exact("1"), aliases: ["kilogram", "kilograms", "\u5343\u514B", "\u516C\u65A4"] },
  { dimension: "mass", symbol: "t", ...exact("1000"), aliases: ["tonne", "tonnes", "metric ton", "\u5428"] },
  { dimension: "mass", symbol: "oz", ...fraction("28349523125", "1000000000000"), aliases: ["ounce", "ounces", "\u76CE\u53F8"] },
  { dimension: "mass", symbol: "lb", ...fraction("45359237", "100000000"), aliases: ["lbs", "pound", "pounds", "\u78C5"] },
  { dimension: "temperature", symbol: "\xB0C", ...exact("1"), aliases: ["c", "celsius", "centigrade", "\u6444\u6C0F\u5EA6"] },
  { dimension: "temperature", symbol: "\xB0F", ...fraction("5", "9"), aliases: ["f", "fahrenheit", "\u534E\u6C0F\u5EA6"] },
  { dimension: "temperature", symbol: "K", ...exact("1"), aliases: ["k", "kelvin", "\u5F00\u5C14\u6587"] },
  { dimension: "volume", symbol: "mL", ...fraction("1", "1000"), aliases: ["ml", "milliliter", "milliliters", "\u6BEB\u5347"] },
  { dimension: "volume", symbol: "L", ...exact("1"), aliases: ["l", "liter", "liters", "litre", "\u5347"] },
  { dimension: "volume", symbol: "m\xB3", ...exact("1000"), aliases: ["m3", "cubic meter", "cubic meters", "\u7ACB\u65B9\u7C73"] },
  { dimension: "volume", symbol: "US fl oz", ...fraction("295735295625", "10000000000000"), aliases: ["floz", "fluid ounce", "fluid ounces", "\u6DB2\u91CF\u76CE\u53F8"] },
  { dimension: "volume", symbol: "US cup", ...fraction("2365882365", "10000000000"), aliases: ["cup", "cups", "\u7F8E\u5236\u676F"] },
  { dimension: "volume", symbol: "US gal", ...fraction("3785411784", "1000000000"), aliases: ["gal", "gallon", "gallons", "\u52A0\u4ED1", "\u7F8E\u5236\u52A0\u4ED1"] },
  { dimension: "area", symbol: "m\xB2", ...exact("1"), aliases: ["m2", "square meter", "square meters", "\u5E73\u65B9\u7C73"] },
  { dimension: "area", symbol: "km\xB2", ...exact("1000000"), aliases: ["km2", "square kilometer", "square kilometers", "\u5E73\u65B9\u5343\u7C73"] },
  { dimension: "area", symbol: "ft\xB2", ...fraction("9290304", "100000000"), aliases: ["ft2", "sqft", "square foot", "square feet", "\u5E73\u65B9\u82F1\u5C3A"] },
  { dimension: "area", symbol: "acre", ...fraction("40468564224", "10000000"), aliases: ["acres", "\u82F1\u4EA9"] },
  { dimension: "area", symbol: "ha", ...exact("10000"), aliases: ["hectare", "hectares", "\u516C\u9877"] },
  { dimension: "speed", symbol: "m/s", ...exact("1"), aliases: ["mps", "meter per second", "meters per second", "\u7C73\u6BCF\u79D2"] },
  { dimension: "speed", symbol: "km/h", ...fraction("5", "18"), aliases: ["kmh", "kph", "\u516C\u91CC\u6BCF\u5C0F\u65F6", "\u5343\u7C73\u6BCF\u5C0F\u65F6"] },
  { dimension: "speed", symbol: "mph", ...fraction("1397", "3125"), aliases: ["miles per hour", "\u82F1\u91CC\u6BCF\u5C0F\u65F6"] },
  { dimension: "speed", symbol: "kn", ...fraction("463", "900"), aliases: ["knot", "knots", "\u8282"] },
  { dimension: "duration", symbol: "s", ...exact("1"), aliases: ["sec", "second", "seconds", "\u79D2"] },
  { dimension: "duration", symbol: "min", ...exact("60"), aliases: ["minute", "minutes", "\u5206\u949F"] },
  { dimension: "duration", symbol: "h", ...exact("3600"), aliases: ["hr", "hour", "hours", "\u5C0F\u65F6"] },
  { dimension: "duration", symbol: "d", ...exact("86400"), aliases: ["day", "days", "\u5929", "\u65E5"] },
  { dimension: "duration", symbol: "week", ...exact("604800"), aliases: ["weeks", "\u5468", "\u661F\u671F"] },
  { dimension: "data", symbol: "B", ...exact("1"), aliases: ["byte", "bytes", "\u5B57\u8282"] },
  { dimension: "data", symbol: "KB", ...exact("1000"), aliases: ["kilobyte", "kilobytes", "\u5343\u5B57\u8282"] },
  { dimension: "data", symbol: "MB", ...exact("1000000"), aliases: ["megabyte", "megabytes", "\u5146\u5B57\u8282"] },
  { dimension: "data", symbol: "GB", ...exact("1000000000"), aliases: ["gigabyte", "gigabytes", "\u5341\u4EBF\u5B57\u8282"] },
  { dimension: "data", symbol: "TB", ...exact("1000000000000"), aliases: ["terabyte", "terabytes"] },
  { dimension: "data", symbol: "KiB", ...exact("1024"), aliases: ["kibibyte", "kibibytes"] },
  { dimension: "data", symbol: "MiB", ...exact("1048576"), aliases: ["mebibyte", "mebibytes"] },
  { dimension: "data", symbol: "GiB", ...exact("1073741824"), aliases: ["gibibyte", "gibibytes"] },
  { dimension: "data", symbol: "TiB", ...exact("1099511627776"), aliases: ["tebibyte", "tebibytes"] }
];
function unit(value, dimension2) {
  const query = text(value, 40), folded = query.toLocaleLowerCase("en").replace(/[\s_°]/gu, "");
  const found = units.filter((unit2) => [unit2.symbol, ...unit2.aliases].some((alias) => alias.toLocaleLowerCase("en").replace(/[\s_°]/gu, "") === folded));
  if (!found.length || found.some((item) => item.dimension !== found[0].dimension) || dimension2 && found[0].dimension !== dimension2) throw new ReadError("unsupported");
  return found[0];
}
function dimension(value) {
  const query = text(value, 20).toLowerCase();
  if (!dimensions.includes(query)) throw new ReadError("unsupported");
  return query;
}
function unitConversion(value, from, to, wanted) {
  const input = amount(value), a = unit(from, wanted), b = unit(to, a.dimension);
  if (a.dimension !== b.dimension) throw new ReadError("unsupported");
  if (a.dimension === "temperature") {
    const celsius = a.symbol === "\xB0F" ? rationalTimes(rationalPlus(inputRational(input), "-32", "1"), "5", "9") : a.symbol === "K" ? rationalPlus(inputRational(input), "-27315", "100") : inputRational(input);
    const result2 = b.symbol === "\xB0F" ? rationalPlus(rationalTimes(celsius, "9", "5"), "32", "1") : b.symbol === "K" ? rationalPlus(celsius, "27315", "100") : celsius;
    return { amount: input, from: a.symbol, to: b.symbol, result: rationalText(result2), inverse: rationalText(rationalTimes(rationalTimes(inputRational("1"), a.numerator, a.denominator), b.denominator, b.numerator)) };
  }
  const absolute = rationalTimes(inputRational(input), a.numerator, a.denominator);
  const result = rationalTimes(absolute, b.denominator, b.numerator);
  const inverse = rationalTimes({ n: BigInt(a.numerator), d: BigInt(a.denominator) }, b.denominator, b.numerator);
  return { amount: input, from: a.symbol, to: b.symbol, result: rationalText(result), inverse: rationalText(inverse) };
}
function rationalTimes(value, numerator, denominator) {
  let n, d;
  if (typeof value === "string") {
    const input = decimalParts(value);
    n = input.coefficient;
    d = 10n ** BigInt(input.scale);
  } else {
    n = value.n;
    d = value.d;
  }
  return reduce(n * BigInt(numerator), d * BigInt(denominator));
}
function rationalPlus(value, numerator, denominator) {
  return reduce(value.n * BigInt(denominator) + BigInt(numerator) * value.d, value.d * BigInt(denominator));
}
function inputRational(value) {
  const parts = decimalParts(value);
  return { n: parts.coefficient, d: 10n ** BigInt(parts.scale) };
}
function reduce(n, d) {
  if (d <= 0n) throw new ReadError("bad_answer");
  let a = n < 0n ? -n : n, b = d;
  while (b) [a, b] = [b, a % b];
  const divisor = a || 1n;
  return { n: n / divisor, d: d / divisor };
}
function rationalText(value, maximumDigits = 18) {
  const negative = value.n < 0n, numerator = negative ? -value.n : value.n;
  const whole = numerator / value.d;
  let remainder = numerator % value.d, digits = "";
  const recurring = value.d / 2n ** countFactor(value.d, 2n) / 5n ** countFactor(value.d, 5n) !== 1n;
  for (let i = 0; i < maximumDigits && remainder; i++) {
    remainder *= 10n;
    const digit = remainder / value.d;
    digits += digit.toString();
    remainder %= value.d;
    if (recurring && i === maximumDigits - 1 && remainder * 2n >= value.d) {
      const rounded = (BigInt(digits) + 1n).toString().padStart(maximumDigits, "0");
      return (negative ? "-" : "") + formatRounded(whole, rounded, maximumDigits);
    }
  }
  digits = digits.replace(/0+$/, "");
  return (negative && (whole || digits) ? "-" : "") + whole + (digits ? `.${digits}` : "");
}
function formatRounded(whole, digits, maximumDigits) {
  if (digits.length > maximumDigits) return (whole + 1n).toString();
  const trimmed = digits.replace(/0+$/, "");
  return whole.toString() + (trimmed ? `.${trimmed}` : "");
}
function countFactor(value, factor) {
  let count = 0n, rest = value;
  while (rest % factor === 0n) {
    rest /= factor;
    count++;
  }
  return count;
}

// src/locales/en.json
var en_default = {
  title: "Currency and units",
  currency: "Currency",
  units: "Units",
  amount: "Amount",
  from: "From",
  to: "To",
  convert: "Convert",
  refresh: "Refresh rate",
  rate: "1 {from} = {rate} {to}",
  inverse: "1 {to} = {rate} {from}",
  rateDate: "ECB reference rate dated {date}",
  local: "Local conversion \xB7 no network request",
  welcome: "Convert common currencies and everyday units. Reference rates include their publication date.",
  dimension: "Unit type",
  choose: "Choose a unit",
  currencyHint: "Currency code, for example USD",
  unitHint: "Unit, for example km",
  attribution: "Currency data: Frankfurter, using European Central Bank reference rates. These are dated reference rates, not executable market quotes or financial advice.",
  unitAttribution: "Unit conversions run locally on the hub. SI and binary data units are distinct; no exchange-rate request is made.",
  account: "Public ECB reference rates and local unit conversion",
  toolCurrency: "Convert an amount between supported currencies using dated European Central Bank reference rates from Frankfurter",
  toolUnit: "Convert an amount between common length, mass, temperature, volume, area, speed, duration or data units locally",
  source: "About Frankfurter rates",
  invalid: "Enter a valid non-negative amount with at most 18 decimal places.",
  unsupported: "Unsupported currency or unit. Choose units of the same type.",
  bad_answer: "The exchange-rate service returned an unexpected response.",
  not_found: "This exchange-rate pair is not available.",
  rate_limited: "The exchange-rate service is rate-limiting requests. Try again later.",
  slow: "The exchange-rate service took too long to respond.",
  network: "The exchange-rate service could not be reached.",
  http: "The exchange-rate service returned HTTP {0}.",
  length: "Length",
  mass: "Mass",
  temperature: "Temperature",
  volume: "Volume",
  area: "Area",
  speed: "Speed",
  duration: "Duration",
  data: "Data"
};

// src/locales/zh-Hans.json
var zh_Hans_default = {
  title: "\u6C47\u7387\u548C\u5355\u4F4D\u6362\u7B97",
  currency: "\u8D27\u5E01",
  units: "\u5355\u4F4D",
  amount: "\u6570\u503C",
  from: "\u4ECE",
  to: "\u6362\u7B97\u4E3A",
  convert: "\u6362\u7B97",
  refresh: "\u5237\u65B0\u6C47\u7387",
  rate: "1 {from} = {rate} {to}",
  inverse: "1 {to} = {rate} {from}",
  rateDate: "\u6B27\u6D32\u592E\u884C\u53C2\u8003\u6C47\u7387\uFF0C\u65E5\u671F\uFF1A{date}",
  local: "\u672C\u5730\u6362\u7B97\uFF0C\u4E0D\u53D1\u9001\u7F51\u7EDC\u8BF7\u6C42",
  welcome: "\u6362\u7B97\u5E38\u7528\u8D27\u5E01\u548C\u65E5\u5E38\u5355\u4F4D\uFF0C\u53C2\u8003\u6C47\u7387\u4F1A\u6807\u660E\u53D1\u5E03\u65E5\u671F\u3002",
  dimension: "\u5355\u4F4D\u7C7B\u578B",
  choose: "\u9009\u62E9\u5355\u4F4D",
  currencyHint: "\u8D27\u5E01\u4EE3\u7801\uFF0C\u4F8B\u5982 USD",
  unitHint: "\u5355\u4F4D\uFF0C\u4F8B\u5982 km",
  attribution: "\u6C47\u7387\u6570\u636E\u6765\u81EA Frankfurter \u63D0\u4F9B\u7684\u6B27\u6D32\u592E\u884C\u53C2\u8003\u6C47\u7387\u3002\u8FD9\u662F\u6CE8\u660E\u65E5\u671F\u7684\u53C2\u8003\u6570\u636E\uFF0C\u4E0D\u662F\u53EF\u6210\u4EA4\u884C\u60C5\uFF0C\u4E5F\u4E0D\u6784\u6210\u8D22\u52A1\u5EFA\u8BAE\u3002",
  unitAttribution: "\u5355\u4F4D\u6362\u7B97\u5728 hub \u672C\u5730\u5B8C\u6210\uFF0C\u533A\u5206\u5341\u8FDB\u5236\u4E0E\u4E8C\u8FDB\u5236\u6570\u636E\u5355\u4F4D\uFF0C\u4E0D\u8BF7\u6C42\u6C47\u7387\u670D\u52A1\u3002",
  account: "\u516C\u5F00\u6B27\u6D32\u592E\u884C\u53C2\u8003\u6C47\u7387\u548C\u672C\u5730\u5355\u4F4D\u6362\u7B97",
  toolCurrency: "\u4F7F\u7528 Frankfurter \u63D0\u4F9B\u7684\u6CE8\u660E\u65E5\u671F\u7684\u6B27\u6D32\u592E\u884C\u53C2\u8003\u6C47\u7387\u6362\u7B97\u652F\u6301\u7684\u8D27\u5E01",
  toolUnit: "\u5728\u672C\u5730\u6362\u7B97\u957F\u5EA6\u3001\u8D28\u91CF\u3001\u6E29\u5EA6\u3001\u4F53\u79EF\u3001\u9762\u79EF\u3001\u901F\u5EA6\u3001\u65F6\u957F\u6216\u6570\u636E\u5355\u4F4D",
  source: "\u4E86\u89E3 Frankfurter \u6C47\u7387",
  invalid: "\u8BF7\u8F93\u5165\u6709\u6548\u7684\u975E\u8D1F\u6570\u503C\uFF0C\u6700\u591A 18 \u4F4D\u5C0F\u6570\u3002",
  unsupported: "\u4E0D\u652F\u6301\u6B64\u8D27\u5E01\u6216\u5355\u4F4D\uFF0C\u8BF7\u9009\u62E9\u540C\u4E00\u7C7B\u578B\u7684\u5355\u4F4D\u3002",
  bad_answer: "\u6C47\u7387\u670D\u52A1\u8FD4\u56DE\u4E86\u65E0\u6CD5\u8BC6\u522B\u7684\u54CD\u5E94\u3002",
  not_found: "\u6B64\u8D27\u5E01\u5BF9\u6CA1\u6709\u53EF\u7528\u6C47\u7387\u3002",
  rate_limited: "\u6C47\u7387\u670D\u52A1\u6B63\u5728\u9650\u5236\u8BF7\u6C42\uFF0C\u8BF7\u7A0D\u540E\u91CD\u8BD5\u3002",
  slow: "\u6C47\u7387\u670D\u52A1\u54CD\u5E94\u8D85\u65F6\u3002",
  network: "\u65E0\u6CD5\u8FDE\u63A5\u6C47\u7387\u670D\u52A1\u3002",
  http: "\u6C47\u7387\u670D\u52A1\u8FD4\u56DE HTTP {0}\u3002",
  length: "\u957F\u5EA6",
  mass: "\u8D28\u91CF",
  temperature: "\u6E29\u5EA6",
  volume: "\u4F53\u79EF",
  area: "\u9762\u79EF",
  speed: "\u901F\u5EA6",
  duration: "\u65F6\u957F",
  data: "\u6570\u636E"
};

// src/locales/ja.json
var ja_default = {
  title: "\u901A\u8CA8\u3068\u5358\u4F4D\u306E\u63DB\u7B97",
  currency: "\u901A\u8CA8",
  units: "\u5358\u4F4D",
  amount: "\u6570\u5024",
  from: "\u63DB\u7B97\u5143",
  to: "\u63DB\u7B97\u5148",
  convert: "\u63DB\u7B97",
  refresh: "\u30EC\u30FC\u30C8\u3092\u66F4\u65B0",
  rate: "1 {from} = {rate} {to}",
  inverse: "1 {to} = {rate} {from}",
  rateDate: "\u6B27\u5DDE\u4E2D\u592E\u9280\u884C\u306E\u53C2\u8003\u30EC\u30FC\u30C8\uFF08{date}\uFF09",
  local: "\u30ED\u30FC\u30AB\u30EB\u3067\u63DB\u7B97\u30FB\u901A\u4FE1\u306A\u3057",
  welcome: "\u901A\u8CA8\u3068\u65E5\u5E38\u306E\u5358\u4F4D\u3092\u63DB\u7B97\u3067\u304D\u307E\u3059\u3002\u53C2\u8003\u30EC\u30FC\u30C8\u306B\u306F\u767A\u8868\u65E5\u3092\u8868\u793A\u3057\u307E\u3059\u3002",
  dimension: "\u5358\u4F4D\u306E\u7A2E\u985E",
  choose: "\u5358\u4F4D\u3092\u9078\u629E",
  currencyHint: "\u901A\u8CA8\u30B3\u30FC\u30C9\uFF08\u4F8B\uFF1AUSD\uFF09",
  unitHint: "\u5358\u4F4D\uFF08\u4F8B\uFF1Akm\uFF09",
  attribution: "\u901A\u8CA8\u30C7\u30FC\u30BF\uFF1AFrankfurter \u304C\u63D0\u4F9B\u3059\u308B\u6B27\u5DDE\u4E2D\u592E\u9280\u884C\u306E\u53C2\u8003\u30EC\u30FC\u30C8\u3002\u65E5\u4ED8\u4ED8\u304D\u306E\u53C2\u8003\u5024\u3067\u3042\u308A\u3001\u7D04\u5B9A\u53EF\u80FD\u306A\u5E02\u5834\u30EC\u30FC\u30C8\u3084\u91D1\u878D\u52A9\u8A00\u3067\u306F\u3042\u308A\u307E\u305B\u3093\u3002",
  unitAttribution: "\u5358\u4F4D\u63DB\u7B97\u306F\u30CF\u30D6\u5185\u3067\u884C\u3044\u307E\u3059\u3002SI \u3068\u4E8C\u9032\u6CD5\u306E\u30C7\u30FC\u30BF\u5358\u4F4D\u3092\u533A\u5225\u3057\u3001\u30EC\u30FC\u30C8\u53D6\u5F97\u306E\u901A\u4FE1\u306F\u884C\u3044\u307E\u305B\u3093\u3002",
  account: "\u516C\u958B\u3055\u308C\u3066\u3044\u308B\u6B27\u5DDE\u4E2D\u592E\u9280\u884C\u306E\u53C2\u8003\u30EC\u30FC\u30C8\u3068\u30ED\u30FC\u30AB\u30EB\u5358\u4F4D\u63DB\u7B97",
  toolCurrency: "Frankfurter \u304C\u63D0\u4F9B\u3059\u308B\u65E5\u4ED8\u4ED8\u304D\u306E\u6B27\u5DDE\u4E2D\u592E\u9280\u884C\u53C2\u8003\u30EC\u30FC\u30C8\u3067\u5BFE\u5FDC\u901A\u8CA8\u3092\u63DB\u7B97\u3057\u307E\u3059",
  toolUnit: "\u9577\u3055\u3001\u8CEA\u91CF\u3001\u6E29\u5EA6\u3001\u4F53\u7A4D\u3001\u9762\u7A4D\u3001\u901F\u5EA6\u3001\u6642\u9593\u3001\u30C7\u30FC\u30BF\u91CF\u306E\u5358\u4F4D\u3092\u30ED\u30FC\u30AB\u30EB\u3067\u63DB\u7B97\u3057\u307E\u3059",
  source: "Frankfurter \u306E\u30EC\u30FC\u30C8\u306B\u3064\u3044\u3066",
  invalid: "\u5C0F\u6570\u70B9\u4EE5\u4E0B 18 \u6841\u307E\u3067\u306E\u6709\u52B9\u306A\u975E\u8CA0\u306E\u6570\u5024\u3092\u5165\u529B\u3057\u3066\u304F\u3060\u3055\u3044\u3002",
  unsupported: "\u5BFE\u5FDC\u3057\u3066\u3044\u306A\u3044\u901A\u8CA8\u307E\u305F\u306F\u5358\u4F4D\u3067\u3059\u3002\u540C\u3058\u7A2E\u985E\u306E\u5358\u4F4D\u3092\u9078\u629E\u3057\u3066\u304F\u3060\u3055\u3044\u3002",
  bad_answer: "\u70BA\u66FF\u30EC\u30FC\u30C8\u30B5\u30FC\u30D3\u30B9\u304B\u3089\u4E88\u671F\u3057\u306A\u3044\u5FDC\u7B54\u304C\u8FD4\u3055\u308C\u307E\u3057\u305F\u3002",
  not_found: "\u3053\u306E\u901A\u8CA8\u30DA\u30A2\u306E\u30EC\u30FC\u30C8\u306F\u3042\u308A\u307E\u305B\u3093\u3002",
  rate_limited: "\u70BA\u66FF\u30EC\u30FC\u30C8\u30B5\u30FC\u30D3\u30B9\u306E\u30EA\u30AF\u30A8\u30B9\u30C8\u5236\u9650\u4E2D\u3067\u3059\u3002\u3057\u3070\u3089\u304F\u3057\u3066\u304B\u3089\u518D\u8A66\u884C\u3057\u3066\u304F\u3060\u3055\u3044\u3002",
  slow: "\u70BA\u66FF\u30EC\u30FC\u30C8\u30B5\u30FC\u30D3\u30B9\u306E\u5FDC\u7B54\u304C\u30BF\u30A4\u30E0\u30A2\u30A6\u30C8\u3057\u307E\u3057\u305F\u3002",
  network: "\u70BA\u66FF\u30EC\u30FC\u30C8\u30B5\u30FC\u30D3\u30B9\u306B\u63A5\u7D9A\u3067\u304D\u307E\u305B\u3093\u3067\u3057\u305F\u3002",
  http: "\u70BA\u66FF\u30EC\u30FC\u30C8\u30B5\u30FC\u30D3\u30B9\u304C HTTP {0} \u3092\u8FD4\u3057\u307E\u3057\u305F\u3002",
  length: "\u9577\u3055",
  mass: "\u8CEA\u91CF",
  temperature: "\u6E29\u5EA6",
  volume: "\u4F53\u7A4D",
  area: "\u9762\u7A4D",
  speed: "\u901F\u5EA6",
  duration: "\u6642\u9593",
  data: "\u30C7\u30FC\u30BF\u91CF"
};

// src/i18n.ts
var textsFor = (locale = "") => locale.toLowerCase().startsWith("ja") ? ja_default : locale.toLowerCase().startsWith("zh") ? zh_Hans_default : en_default;
var fmt = (text2, values) => text2.replace(/\{(\w+)\}/g, (_, key) => String(values[key] ?? ""));
function errorText(error, locale = "") {
  const t = textsFor(locale);
  if (!(error instanceof ReadError)) return t.network;
  return error.code === "http" ? fmt(t.http, { 0: error.status ?? 0 }) : t[error.code];
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
var CurrencyService = class {
  constructor(fetch, _config = {}, options = {}) {
    this.fetch = fetch;
    this.locale = options.locale ?? "en";
    this.now = options.now ?? Date.now;
    this.monotonic = options.monotonic ?? (() => performance.now());
    this.limits = { ...limits, ...options.limits };
  }
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
      response = await this.fetch(`https://api.frankfurter.dev${path}`, {
        method: "GET",
        headers: { Accept: "application/json", "User-Agent": "persona-currency/0.1.0" },
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
    let text2;
    try {
      text2 = new TextDecoder("utf-8", { fatal: true }).decode(data);
    } catch {
      throw new ReadError("bad_answer");
    }
    if (response.status === 401 || response.status === 403 || response.status === 429) throw new ReadError("rate_limited");
    if (response.status === 404) throw new ReadError("not_found");
    if (response.status !== 200) throw new ReadError("http", response.status);
    try {
      return JSON.parse(text2);
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
  async rate(base, quote, refresh = false, signal) {
    const a = currency(base), b = currency(quote);
    const rd = await this.read(ratesPath(a, b), refresh ? 0 : this.limits.fresh, signal);
    return { ...parseRate(rd.raw, a, b, rd.at), stale: rd.stale };
  }
  cacheStats() {
    return { entries: this.cache.size, bytes: this.bytes };
  }
};

// src/tools.ts
function toolsFor(locale = "en") {
  const t = textsFor(locale);
  const value = { type: "string", description: t.amount };
  return [
    { name: "currency_convert", description: t.toolCurrency, input_schema: { type: "object", required: ["amount", "from", "to"], properties: { amount: value, from: { type: "string", description: t.from }, to: { type: "string", description: t.to } } } },
    { name: "unit_convert", description: t.toolUnit, input_schema: { type: "object", required: ["amount", "from", "to"], properties: { amount: value, from: { type: "string", description: t.from }, to: { type: "string", description: t.to }, dimension: { type: "string", enum: ["length", "mass", "temperature", "volume", "area", "speed", "duration", "data"], description: t.dimension } } } }
  ];
}
var tools = toolsFor();
function argsFrom(raw, exact2) {
  if (exact2) {
    const args = {};
    for (const field of ["amount", "from", "to", "dimension"]) {
      for (const token of exact2.fieldValues(field)) {
        const value2 = JSON.parse(token.raw);
        if (typeof value2 !== "string") throw new ReadError("invalid");
        args[field] = value2;
      }
    }
    return args;
  }
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = raw;
  for (const field of ["from", "to", "dimension"]) if (value[field] != null && typeof value[field] !== "string") throw new ReadError("invalid");
  if (value.amount != null && typeof value.amount !== "string" && typeof value.amount !== "number") throw new ReadError("invalid");
  return value;
}
async function callTool(service, name, raw = {}, signal, exact2) {
  const args = argsFrom(raw, exact2), t = textsFor(service.locale), request = budget(signal, service.limits.tool);
  try {
    if (name === "currency_convert") {
      const input = amount(args.amount), rate = await service.rate(args.from, args.to, false, request.signal), result = currencyConversion(input, rate);
      return clip(`${input} ${rate.base} = ${result.result} ${rate.quote}
${fmt(t.rateDate, { date: rate.date })}
${t.attribution}${rate.stale ? `
${errorText(rate.stale, service.locale)}` : ""}`, 29999);
    }
    if (name === "unit_convert") {
      const result = unitConversion(args.amount, args.from, args.to, args.dimension == null ? void 0 : dimension(args.dimension));
      return `${result.amount} ${result.from} = ${result.result} ${result.to}
${t.local}`;
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
    if (p.contribution && p.contribution !== "currency") throw noContribution(String(p.contribution));
    if (method === "status") return service.status();
    if (method === "tools.list") return { tools: toolsFor(service.locale) };
    if (method === "tools.call") {
      if (typeof p.name !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
      try {
        return { text: await callTool(service, p.name, p.args, void 0, call?.params?.field("args")) };
      } catch (error) {
        if (error instanceof ReadError) throw new ExtError(errorText(error, service.locale), error.code === "invalid" || error.code === "unsupported" ? "invalid" : void 0);
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
var initialState = { mode: "currency", amount: "1", from: "USD", to: "CNY", dimension: "length", converted: false };
function readState(raw) {
  if (typeof raw !== "string" || raw.length > 2e3) return initialState;
  try {
    const value = JSON.parse(raw);
    if (!value || Array.isArray(value) || value.mode !== "currency" && value.mode !== "units" || typeof value.converted !== "boolean") return initialState;
    if (value.mode === "currency") {
      return { mode: "currency", amount: amount(value.amount), from: currency(value.from), to: currency(value.to), dimension: "length", converted: value.converted };
    }
    const d = dimension(value.dimension);
    const parsed = unitConversion(value.amount, value.from, value.to, d);
    return { mode: "units", amount: parsed.amount, from: parsed.from, to: parsed.to, dimension: d, converted: value.converted };
  } catch {
    return initialState;
  }
}
var encodeState = (state) => JSON.stringify(state);
function fields2(values) {
  if (typeof values === "string") {
    try {
      values = JSON.parse(values);
    } catch {
      return {};
    }
  }
  if (!values || typeof values !== "object" || Array.isArray(values)) return {};
  return values;
}
function transition(state, id, values = {}) {
  const f = fields2(values);
  if (id.startsWith("mode:")) {
    const mode = id.slice(5) === "units" ? "units" : "currency";
    if (mode === "currency") return { mode, amount: state.amount, from: "USD", to: "CNY", dimension: "length", converted: false };
    return { mode, amount: state.amount, from: "km", to: "mi", dimension: "length", converted: false };
  }
  if (id === "tab") {
    const selected = f.tab === "units" ? "units" : f.tab === "currency" ? "currency" : state.mode;
    return transition(state, `mode:${selected}`);
  }
  if (id === "convert") {
    try {
      if (state.mode === "currency") return { ...state, amount: amount(f.amount), from: currency(f.from), to: currency(f.to), converted: true };
      const converted = unitConversion(f.amount, f.from, f.to);
      return { ...state, amount: converted.amount, from: converted.from, to: converted.to, dimension: unit(converted.to).dimension, converted: true };
    } catch {
      return state;
    }
  }
  if (id.startsWith("dimension:")) {
    try {
      const d = dimension(id.slice("dimension:".length)), options = { length: ["km", "mi"], mass: ["kg", "lb"], temperature: ["\xB0C", "\xB0F"], volume: ["L", "US gal"], area: ["m\xB2", "ft\xB2"], speed: ["km/h", "mph"], duration: ["min", "h"], data: ["MiB", "MB"] };
      return { mode: "units", amount: state.amount, from: options[d][0], to: options[d][1], dimension: d, converted: false };
    } catch {
      return state;
    }
  }
  return state;
}
async function loadPage(service, state, refresh, signal) {
  const request = budget(signal, service.limits.page);
  try {
    if (!state.converted) return { kind: "error", error: new ReadError("invalid") };
    if (state.mode === "units") return { kind: "local", result: unitConversion(state.amount, state.from, state.to, state.dimension) };
    const rate = await service.rate(state.from, state.to, refresh, request.signal);
    return { kind: "currency", rate, result: currencyConversion(state.amount, rate) };
  } catch (error) {
    checkSignal(signal);
    return { kind: "error", error };
  } finally {
    request.dispose();
  }
}
function page(props) {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale), locale = props.locale ?? service.locale;
  const act = (id, next, title) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "converter", state: encodeState(next), ...title ? { title } : {} } } : { id });
  const text2 = (key, value, style = "secondary") => /* @__PURE__ */ jsx(Text, { text: clip(value, 1900), style }, key);
  const modes = /* @__PURE__ */ jsx(Section, { children: uiVersion >= 2 ? /* @__PURE__ */ jsx(Segmented, { name: "tab", value: state.mode, choices: [{ value: "currency", title: t.currency }, { value: "units", title: t.units }], action: act("tab") }, "currency-mode") : ["currency", "units"].map((mode) => /* @__PURE__ */ jsx(Row, { title: t[mode], action: act(`mode:${mode}`), badge: state.mode === mode ? { text: t.choose, tone: "positive" } : void 0 }, `mode-${mode}`)) }, "currency-modes");
  const editor = /* @__PURE__ */ jsxs(Section, { title: state.mode === "currency" ? t.currency : t.units, children: [
    /* @__PURE__ */ jsx(Field, { name: "amount", label: t.amount, kind: "text", placeholder: "1", value: state.amount }, "currency-amount"),
    /* @__PURE__ */ jsx(Field, { name: "from", label: t.from, kind: "text", placeholder: state.mode === "currency" ? t.currencyHint : t.unitHint, value: state.from }, "currency-from"),
    /* @__PURE__ */ jsx(Field, { name: "to", label: t.to, kind: "text", placeholder: state.mode === "currency" ? t.currencyHint : t.unitHint, value: state.to }, "currency-to"),
    /* @__PURE__ */ jsx(Button, { title: t.convert, action: act("convert") }, "currency-convert")
  ] }, "currency-editor");
  const attribution = /* @__PURE__ */ jsxs(Section, { children: [
    text2("currency-license", state.mode === "currency" ? t.attribution : t.unitAttribution, "caption"),
    state.mode === "currency" ? /* @__PURE__ */ jsx(Link, { title: t.source, url: "https://frankfurter.dev/" }, "currency-source") : null
  ] }, "currency-attribution");
  if (!state.converted) return /* @__PURE__ */ jsxs(View, { title: t.title, state: encodeState(state), children: [
    modes,
    editor,
    /* @__PURE__ */ jsx(Section, { children: text2("currency-welcome-text", t.welcome, "body") }, "currency-welcome"),
    attribution
  ] }, "currency");
  if (loaded.kind === "loading") return /* @__PURE__ */ jsxs(View, { title: t.title, state: encodeState(state), children: [
    modes,
    editor,
    /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(Loading, { title: t.convert }, "currency-loading-indicator") }, "currency-loading"),
    attribution
  ] }, "currency");
  if (loaded.kind === "error") return /* @__PURE__ */ jsxs(View, { title: t.title, state: encodeState(state), children: [
    modes,
    editor,
    /* @__PURE__ */ jsxs(Section, { children: [
      text2("currency-error-text", errorText(loaded.error, locale)),
      /* @__PURE__ */ jsx(Button, { title: t.refresh, action: act("refresh") }, "currency-retry")
    ] }, "currency-error"),
    attribution
  ] }, "currency");
  const result = loaded.result;
  return /* @__PURE__ */ jsxs(View, { title: t.title, state: encodeState(state), children: [
    modes,
    editor,
    /* @__PURE__ */ jsxs(Section, { title: t.convert, children: [
      /* @__PURE__ */ jsx(Metric, { label: `${formatForDisplay(result.amount, locale)} ${result.from}`, value: `${formatForDisplay(result.result, locale)} ${result.to}` }, "currency-value"),
      text2("currency-rate", loaded.kind === "currency" ? fmt(t.rate, { from: result.from, to: result.to, rate: formatForDisplay(loaded.rate.rate, locale) }) : fmt(t.inverse, { from: result.to, to: result.from, rate: formatForDisplay(result.inverse ?? "1", locale) }), "caption"),
      loaded.kind === "currency" ? text2("currency-date", fmt(t.rateDate, { date: loaded.rate.date }) + (loaded.rate.stale ? ` \xB7 ${errorText(loaded.rate.stale, locale)}` : ""), "caption") : text2("currency-local", t.local, "caption")
    ] }, "currency-result"),
    state.mode === "units" ? /* @__PURE__ */ jsx(Section, { title: t.dimension, children: dimensions.map((d) => /* @__PURE__ */ jsx(Button, { title: t[d], action: act(`dimension:${d}`) }, `dimension-${d}`)) }, "currency-dimensions") : null,
    /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(Button, { title: t.refresh, action: act("refresh") }, "currency-refresh") }, "currency-footer"),
    attribution
  ] }, "currency");
}

// src/root.tsx
function Root(options) {
  const [state, setState] = useState(() => readState(options.state));
  const [refresh, setRefresh] = useState(0);
  const [result, setResult] = useState(void 0);
  const identity = `${state.mode}:${state.amount}:${state.from}:${state.to}:${state.dimension}:${state.converted}:${refresh}`;
  useLoadEffect(async (signal) => {
    const loaded = await loadPage(options.service, state, refresh > 0, signal);
    if (!signal.aborted) setResult((previous) => previous?.service === options.service && previous.identity === identity && JSON.stringify(previous.loaded) === JSON.stringify(loaded) ? previous : { service: options.service, identity, loaded });
  }, [options.service, identity]);
  return page({
    ...options,
    state,
    loaded: result?.service === options.service && result.identity === identity ? result.loaded : state.converted ? { kind: "loading" } : { kind: "error", error: new Error("not converted") },
    action: (id, next, title) => {
      if (next && (options.uiVersion ?? 0) >= 3) {
        const push = { ui: "converter", state: encodeState(next), ...title ? { title } : {} };
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
  return /* @__PURE__ */ jsx(Root, { ...options }, "currency-root");
}

// src/snapshot.ts
function snapshotPage(service) {
  return { render: async (req) => {
    const state = readState(req.state);
    if (!state.converted) return structuredClone(new Renderer({ authorize: () => false }).mount(page({ service, state, loaded: { kind: "error", error: new Error("not converted") }, locale: req.locale, uiVersion: req.ui_version })).view);
    const loaded = await loadPage(service, state, false, req.signal);
    const renderer = new Renderer({ authorize: () => false });
    try {
      return structuredClone(renderer.mount(page({ service, state, loaded, locale: req.locale, uiVersion: req.ui_version })).view);
    } finally {
      renderer.dispose();
    }
  } };
}

// src/index.ts
var sourceManifest = { ...persona_extension_default, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/currency.mjs", sha256: "" } } };
delete sourceManifest.icon;
delete sourceManifest.replaces;
function createProgram(fetch = (input, init) => globalThis.fetch(input, init), options = {}) {
  let service;
  const current = () => {
    if (!service) throw new Error("Currency conversion has not been initialized");
    return service;
  };
  return {
    id: persona_extension_default.id,
    version: persona_extension_default.version,
    manifest: sourceManifest,
    init: (init, _hub) => {
      service = new CurrencyService(fetch, init.config, { ...options, locale: init.hub?.locale ?? options.locale });
    },
    namespaces: { connector: connector(current) },
    ui: { converter: componentPage((req) => root({ service: current(), state: req.state, locale: req.locale, uiVersion: req.ui_version })) }
  };
}
var index_default = createProgram();
export {
  CurrencyService,
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
