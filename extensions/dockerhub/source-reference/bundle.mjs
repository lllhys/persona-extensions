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
var persona_extension_default = { manifest_version: 1, id: "dockerhub", name: "Docker Hub", version: "0.1.2", icon: "assets/icon.png", description: "Search public Docker Hub images. Check official status, publisher namespaces, pull activity, selected tag digests and supported platforms. Get an exact docker pull command as text without pulling or running images.", publisher: "nwverse", homepage: "https://github.com/nwverse/persona-extensions/blob/main/extensions/dockerhub/README.md", tags: ["Development", "Docker", "Containers", "Dependencies"], engines: { plugin_protocol: 1 }, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/dockerhub.mjs", sha256: "" } }, contributes: { connectors: [{ id: "dockerhub", name: "Docker Hub", capabilities: ["status", "tools"], hosts: ["hub.docker.com"], agent_tools: ["dockerhub_*"] }], ui: [{ id: "dockerhub", title: "Docker Hub", placement: "nav_page", icon: "code" }] }, permissions: { network: ["hub.docker.com"] } };

// src/model.ts
var limits = Object.freeze({ request: 12e3, page: 15e3, tool: 15e3, response: 1 << 20, fresh: 15 * 6e4, cache: 40, cacheBytes: 2 << 20, gap: 250 });
var ReadError = class extends Error {
  constructor(code, status) {
    super(code);
    this.code = code;
    this.status = status;
  }
};
var clip = (text, most) => text.length <= most ? text : text.slice(0, most).trimEnd() + "\u2026";
var oneLine = (text) => text.replace(/\s+/gu, " ").trim();
function repositoryName(value) {
  if (typeof value !== "string" || !/^(?:library|[a-z0-9][a-z0-9_-]{0,63})\/[a-z0-9][a-z0-9_.-]{0,99}$/.test(value)) throw new ReadError("invalid");
  return value;
}
function searchName(value) {
  if (typeof value !== "string" || !/^[a-z0-9][a-z0-9_.-]{0,99}$/.test(value)) throw new ReadError("bad_answer");
  return `library/${value}`;
}
function queryText(value) {
  if (typeof value !== "string" || /[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  const query = oneLine(value);
  if (!query || Array.from(query).length > 100) throw new ReadError("invalid");
  return query;
}
function searchPath(query, limit = 20) {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
  return `/v2/search/repositories/?${new URLSearchParams({ query: queryText(query), page_size: String(limit) })}`;
}
function repositoryPath(name) {
  return `/v2/repositories/${repositoryName(name)}/`;
}
function tagsPath(name) {
  return `/v2/repositories/${repositoryName(name)}/tags?page_size=10`;
}
function object(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  return value;
}
function array(value) {
  if (!Array.isArray(value)) throw new ReadError("bad_answer");
  return value;
}
function string(value, fallback) {
  if (value == null && fallback !== void 0) return fallback;
  if (typeof value !== "string") throw new ReadError("bad_answer");
  return value;
}
function integer(value, maximum = 1e14) {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0 || value > maximum) throw new ReadError("bad_answer");
  return value;
}
function boolean(value) {
  if (typeof value !== "boolean") throw new ReadError("bad_answer");
  return value;
}
function date(value) {
  const date2 = string(value);
  if (!Number.isFinite(Date.parse(date2))) throw new ReadError("bad_answer");
  return date2;
}
function summary(value) {
  const item = object(value);
  if (typeof item.repo_name !== "string") throw new ReadError("bad_answer");
  const name = repositoryName(item.repo_name.includes("/") ? item.repo_name : searchName(item.repo_name));
  return { name, description: clip(oneLine(string(item.short_description, "")), 500), pulls: integer(item.pull_count), stars: integer(item.star_count), official: boolean(item.is_official), url: `https://hub.docker.com/r/${name}` };
}
function parseSearch(raw, at) {
  const root2 = object(raw), seen = /* @__PURE__ */ new Set();
  const images = array(root2.results).slice(0, 20).map((value) => {
    const image = summary(value);
    if (seen.has(image.name)) throw new ReadError("bad_answer");
    seen.add(image.name);
    return image;
  });
  return { images, total: integer(root2.count, 1e9), at };
}
function parseRepository(raw, name) {
  const item = object(raw);
  if (repositoryName(`${item.namespace}/${string(item.name)}`) !== repositoryName(name)) throw new ReadError("bad_answer");
  return { name: repositoryName(name), namespace: string(item.namespace), description: clip(oneLine(string(item.description, "")), 500), pulls: integer(item.pull_count), stars: integer(item.star_count), official: item.namespace === "library", url: `https://hub.docker.com/r/${repositoryName(name)}`, updated: date(item.last_updated), pull: `docker pull ${repositoryName(name)}`, tags: [] };
}
function parseTags(raw, repository) {
  const root2 = object(raw);
  return array(root2.results).slice(0, 10).map((value) => {
    const item = object(value);
    if (item.content_type !== "image" || item.tag_status !== "active") return void 0;
    const name = string(item.name);
    if (!/^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$/.test(name)) throw new ReadError("bad_answer");
    const digest = string(item.digest);
    if (!/^sha256:[a-f0-9]{64}$/.test(digest)) throw new ReadError("bad_answer");
    const platforms = array(item.images).slice(0, 64).map((value2) => {
      const image = object(value2), os = string(image.os), architecture = string(image.architecture);
      if (!/^[a-z0-9][a-z0-9_-]{0,31}$/.test(os) || !/^[a-z0-9][a-z0-9_-]{0,31}$/.test(architecture)) throw new ReadError("bad_answer");
      const variant = image.variant == null ? "" : string(image.variant);
      if (variant && !/^[a-z0-9][a-z0-9_-]{0,31}$/.test(variant)) throw new ReadError("bad_answer");
      const platform = `${os}/${architecture}${variant ? `/${variant}` : ""}`;
      if (os === "unknown" || architecture === "unknown") return void 0;
      integer(image.size, 1e12);
      if (!/^sha256:[a-f0-9]{64}$/.test(string(image.digest))) throw new ReadError("bad_answer");
      return platform;
    }).filter((value2) => value2 !== void 0);
    if (repositoryName(repository).length > 130) throw new ReadError("bad_answer");
    return { name, digest, updated: date(item.last_updated), platforms: [...new Set(platforms)] };
  }).filter((tag) => tag !== void 0);
}

// src/locales/en.json
var en_default = {
  title: "Docker Hub",
  search: "Search Docker Hub",
  searchHint: "Image name or keyword",
  searchAction: "Search",
  loading: "Looking up Docker Hub",
  retry: "Try again",
  back: "Back to results",
  home: "New search",
  welcome: "Search public Docker Hub repositories. Check official status, publisher namespace, pull activity, selected tag digests and supported platforms before choosing an image.",
  empty: "No images found. Try a repository name or another keyword.",
  results: "{total} matching repositories",
  total: "{count} pulls",
  stars: "{count} stars",
  official: "Official image",
  community: "Community repository",
  namespace: "Namespace {namespace}",
  openImage: "View on Docker Hub",
  openTag: "View tag on Docker Hub",
  details: "Image and tag details",
  install: "Docker pull command",
  installHint: "Review the publisher, tag and image contents before running this command. This extension does not pull images, contact a container daemon or run containers.",
  tags: "{count} selected tags",
  noPlatforms: "No executable platform listed",
  attribution: "Metadata comes from public Docker Hub JSON endpoints. Official status and pull activity do not prove image safety. Tag digests refer to the reported tag manifest; platform-specific image digests are not shown as multi-platform pull references. Review publisher and image contents before executing.",
  account: "Public Docker Hub metadata",
  toolSearch: "Search public Docker Hub repositories for official status, pull activity and canonical links",
  toolRepository: "Read a Docker Hub repository's publisher, pull activity and selected tag digests and platforms, with a docker pull command as text only",
  query: "Image name or keyword",
  name: "Repository name, such as library/nginx",
  limit: "Maximum number of repositories",
  unknown: "Not supplied",
  invalid: "Enter a valid image name, keyword or result limit. Repository details use namespace/name; official images use library/name.",
  bad_answer: "Docker Hub returned an unexpected response.",
  not_found: "No matching Docker Hub repository was found.",
  rate_limited: "Docker Hub is rate-limiting requests. Try again later.",
  slow: "Docker Hub took too long to respond.",
  network: "Docker Hub could not be reached.",
  http: "Docker Hub returned HTTP {0}."
};

// src/locales/zh-Hans.json
var zh_Hans_default = {
  title: "Docker Hub",
  search: "\u641C\u7D22 Docker Hub",
  searchHint: "\u955C\u50CF\u540D\u79F0\u6216\u5173\u952E\u8BCD",
  searchAction: "\u641C\u7D22",
  loading: "\u6B63\u5728\u67E5\u8BE2 Docker Hub",
  retry: "\u91CD\u8BD5",
  back: "\u8FD4\u56DE\u7ED3\u679C",
  home: "\u65B0\u5EFA\u641C\u7D22",
  welcome: "\u641C\u7D22 Docker Hub \u516C\u5171\u955C\u50CF\u4ED3\u5E93\u3002\u9009\u62E9\u955C\u50CF\u524D\uFF0C\u67E5\u770B\u5B98\u65B9\u72B6\u6001\u3001\u53D1\u5E03\u65B9\u547D\u540D\u7A7A\u95F4\u3001\u62C9\u53D6\u91CF\u3001\u90E8\u5206\u6807\u7B7E\u7684 digest \u548C\u652F\u6301\u7684\u5E73\u53F0\u3002",
  empty: "\u6CA1\u6709\u627E\u5230\u955C\u50CF\u3002\u8BF7\u5C1D\u8BD5\u4ED3\u5E93\u540D\u79F0\u6216\u5176\u4ED6\u5173\u952E\u8BCD\u3002",
  results: "{total} \u4E2A\u5339\u914D\u7684\u4ED3\u5E93",
  total: "\u62C9\u53D6\u91CF {count}",
  stars: "{count} \u4E2A\u661F\u6807",
  official: "\u5B98\u65B9\u955C\u50CF",
  community: "\u793E\u533A\u4ED3\u5E93",
  namespace: "\u547D\u540D\u7A7A\u95F4 {namespace}",
  openImage: "\u5728 Docker Hub \u4E0A\u67E5\u770B",
  openTag: "\u5728 Docker Hub \u4E0A\u67E5\u770B\u6807\u7B7E",
  details: "\u955C\u50CF\u548C\u6807\u7B7E\u8BE6\u60C5",
  install: "Docker \u62C9\u53D6\u547D\u4EE4",
  installHint: "\u8FD0\u884C\u547D\u4EE4\u524D\u8BF7\u68C0\u67E5\u53D1\u5E03\u65B9\u3001\u6807\u7B7E\u548C\u955C\u50CF\u5185\u5BB9\u3002\u6B64\u6269\u5C55\u4E0D\u4F1A\u62C9\u53D6\u955C\u50CF\u3001\u8FDE\u63A5\u5BB9\u5668\u5B88\u62A4\u8FDB\u7A0B\u6216\u8FD0\u884C\u5BB9\u5668\u3002",
  tags: "{count} \u4E2A\u9009\u5B9A\u6807\u7B7E",
  noPlatforms: "\u672A\u5217\u51FA\u53EF\u6267\u884C\u5E73\u53F0",
  attribution: "\u5143\u6570\u636E\u6765\u81EA Docker Hub \u516C\u5171 JSON \u63A5\u53E3\u3002\u5B98\u65B9\u72B6\u6001\u548C\u62C9\u53D6\u91CF\u4E0D\u80FD\u8BC1\u660E\u955C\u50CF\u5B89\u5168\u3002\u6807\u7B7E digest \u5BF9\u5E94\u63A5\u53E3\u62A5\u544A\u7684\u6807\u7B7E manifest\uFF1B\u5404\u5E73\u53F0\u955C\u50CF digest \u4E0D\u4F1A\u88AB\u5F53\u4F5C\u591A\u5E73\u53F0\u62C9\u53D6\u5F15\u7528\u3002\u6267\u884C\u524D\u8BF7\u68C0\u67E5\u53D1\u5E03\u65B9\u548C\u955C\u50CF\u5185\u5BB9\u3002",
  account: "Docker Hub \u516C\u5171\u5143\u6570\u636E",
  toolSearch: "\u641C\u7D22 Docker Hub \u516C\u5171\u955C\u50CF\u4ED3\u5E93\uFF0C\u83B7\u53D6\u5B98\u65B9\u72B6\u6001\u3001\u62C9\u53D6\u91CF\u548C\u89C4\u8303\u94FE\u63A5",
  toolRepository: "\u8BFB\u53D6 Docker Hub \u4ED3\u5E93\u7684\u53D1\u5E03\u65B9\u3001\u62C9\u53D6\u91CF\u3001\u90E8\u5206\u6807\u7B7E\u7684 digest \u548C\u5E73\u53F0\uFF0C\u53EA\u63D0\u4F9B docker pull \u547D\u4EE4\u6587\u672C",
  query: "\u955C\u50CF\u540D\u79F0\u6216\u5173\u952E\u8BCD",
  name: "\u4ED3\u5E93\u540D\u79F0\uFF0C\u4F8B\u5982 library/nginx",
  limit: "\u8FD4\u56DE\u4ED3\u5E93\u7684\u6700\u5927\u6570\u91CF",
  unknown: "\u672A\u63D0\u4F9B",
  invalid: "\u8BF7\u8F93\u5165\u6709\u6548\u7684\u955C\u50CF\u540D\u79F0\u3001\u5173\u952E\u8BCD\u6216\u7ED3\u679C\u6570\u91CF\u3002\u4ED3\u5E93\u8BE6\u60C5\u4F7F\u7528 namespace/name\uFF1B\u5B98\u65B9\u955C\u50CF\u4F7F\u7528 library/name\u3002",
  bad_answer: "Docker Hub \u8FD4\u56DE\u4E86\u610F\u5916\u7684\u54CD\u5E94\u3002",
  not_found: "\u6CA1\u6709\u627E\u5230\u5339\u914D\u7684 Docker Hub \u4ED3\u5E93\u3002",
  rate_limited: "Docker Hub \u6B63\u5728\u9650\u5236\u8BF7\u6C42\u9891\u7387\uFF0C\u8BF7\u7A0D\u540E\u91CD\u8BD5\u3002",
  slow: "Docker Hub \u54CD\u5E94\u8D85\u65F6\u3002",
  network: "\u65E0\u6CD5\u8FDE\u63A5 Docker Hub\u3002",
  http: "Docker Hub \u8FD4\u56DE HTTP {0}\u3002"
};

// src/locales/ja.json
var ja_default = {
  title: "Docker Hub",
  search: "Docker Hub \u3092\u691C\u7D22",
  searchHint: "\u30A4\u30E1\u30FC\u30B8\u540D\u307E\u305F\u306F\u30AD\u30FC\u30EF\u30FC\u30C9",
  searchAction: "\u691C\u7D22",
  loading: "Docker Hub \u3092\u7167\u4F1A\u3057\u3066\u3044\u307E\u3059",
  retry: "\u518D\u8A66\u884C",
  back: "\u691C\u7D22\u7D50\u679C\u306B\u623B\u308B",
  home: "\u65B0\u3057\u3044\u691C\u7D22",
  welcome: "Docker Hub \u306E\u516C\u958B\u30EA\u30DD\u30B8\u30C8\u30EA\u3092\u691C\u7D22\u3057\u307E\u3059\u3002\u30A4\u30E1\u30FC\u30B8\u3092\u9078\u3076\u524D\u306B\u3001\u516C\u5F0F\u30B9\u30C6\u30FC\u30BF\u30B9\u3001\u516C\u958B\u5143\u306E\u540D\u524D\u7A7A\u9593\u3001\u30D7\u30EB\u6570\u3001\u30BF\u30B0\u306E\u30C0\u30A4\u30B8\u30A7\u30B9\u30C8\u3001\u5BFE\u5FDC\u30D7\u30E9\u30C3\u30C8\u30D5\u30A9\u30FC\u30E0\u3092\u78BA\u8A8D\u3067\u304D\u307E\u3059\u3002",
  empty: "\u30A4\u30E1\u30FC\u30B8\u304C\u898B\u3064\u304B\u308A\u307E\u305B\u3093\u3002\u30EA\u30DD\u30B8\u30C8\u30EA\u540D\u3084\u5225\u306E\u30AD\u30FC\u30EF\u30FC\u30C9\u3092\u304A\u8A66\u3057\u304F\u3060\u3055\u3044\u3002",
  results: "{total} \u4EF6\u306E\u30EA\u30DD\u30B8\u30C8\u30EA",
  total: "\u30D7\u30EB\u6570 {count}",
  stars: "\u30B9\u30BF\u30FC {count}",
  official: "\u516C\u5F0F\u30A4\u30E1\u30FC\u30B8",
  community: "\u30B3\u30DF\u30E5\u30CB\u30C6\u30A3\u30EA\u30DD\u30B8\u30C8\u30EA",
  namespace: "\u540D\u524D\u7A7A\u9593 {namespace}",
  openImage: "Docker Hub \u3067\u8868\u793A",
  openTag: "Docker Hub \u3067\u30BF\u30B0\u3092\u8868\u793A",
  details: "\u30A4\u30E1\u30FC\u30B8\u3068\u30BF\u30B0\u306E\u8A73\u7D30",
  install: "Docker pull \u30B3\u30DE\u30F3\u30C9",
  installHint: "\u5B9F\u884C\u524D\u306B\u516C\u958B\u5143\u3001\u30BF\u30B0\u3001\u30A4\u30E1\u30FC\u30B8\u306E\u5185\u5BB9\u3092\u78BA\u8A8D\u3057\u3066\u304F\u3060\u3055\u3044\u3002\u3053\u306E\u62E1\u5F35\u6A5F\u80FD\u306F\u30A4\u30E1\u30FC\u30B8\u306E\u30D7\u30EB\u3001\u30B3\u30F3\u30C6\u30CA\u30C7\u30FC\u30E2\u30F3\u3078\u306E\u63A5\u7D9A\u3001\u30B3\u30F3\u30C6\u30CA\u306E\u5B9F\u884C\u3092\u884C\u3044\u307E\u305B\u3093\u3002",
  tags: "\u9078\u629E\u3057\u305F\u30BF\u30B0 {count} \u4EF6",
  noPlatforms: "\u5B9F\u884C\u53EF\u80FD\u306A\u30D7\u30E9\u30C3\u30C8\u30D5\u30A9\u30FC\u30E0\u306F\u3042\u308A\u307E\u305B\u3093",
  attribution: "\u30E1\u30BF\u30C7\u30FC\u30BF\u306F Docker Hub \u306E\u516C\u958B JSON \u30A8\u30F3\u30C9\u30DD\u30A4\u30F3\u30C8\u304B\u3089\u53D6\u5F97\u3057\u307E\u3059\u3002\u516C\u5F0F\u30B9\u30C6\u30FC\u30BF\u30B9\u3084\u30D7\u30EB\u6570\u306F\u5B89\u5168\u6027\u306E\u8A3C\u660E\u3067\u306F\u3042\u308A\u307E\u305B\u3093\u3002\u30BF\u30B0\u306E\u30C0\u30A4\u30B8\u30A7\u30B9\u30C8\u306F API \u304C\u5831\u544A\u3057\u305F\u30BF\u30B0\u30DE\u30CB\u30D5\u30A7\u30B9\u30C8\u3092\u6307\u3057\u3001\u30D7\u30E9\u30C3\u30C8\u30D5\u30A9\u30FC\u30E0\u5225\u30A4\u30E1\u30FC\u30B8\u306E\u30C0\u30A4\u30B8\u30A7\u30B9\u30C8\u3092\u30DE\u30EB\u30C1\u30D7\u30E9\u30C3\u30C8\u30D5\u30A9\u30FC\u30E0\u306E\u30D7\u30EB\u53C2\u7167\u3068\u3057\u3066\u8868\u793A\u3057\u307E\u305B\u3093\u3002\u5B9F\u884C\u524D\u306B\u516C\u958B\u5143\u3068\u5185\u5BB9\u3092\u78BA\u8A8D\u3057\u3066\u304F\u3060\u3055\u3044\u3002",
  account: "Docker Hub \u306E\u516C\u958B\u30E1\u30BF\u30C7\u30FC\u30BF",
  toolSearch: "Docker Hub \u306E\u516C\u958B\u30EA\u30DD\u30B8\u30C8\u30EA\u3092\u691C\u7D22\u3057\u3001\u516C\u5F0F\u30B9\u30C6\u30FC\u30BF\u30B9\u3001\u30D7\u30EB\u6570\u3001\u6B63\u898F\u30EA\u30F3\u30AF\u3092\u53D6\u5F97\u3057\u307E\u3059",
  toolRepository: "Docker Hub \u30EA\u30DD\u30B8\u30C8\u30EA\u306E\u516C\u958B\u5143\u3001\u30D7\u30EB\u6570\u3001\u30BF\u30B0\u306E\u30C0\u30A4\u30B8\u30A7\u30B9\u30C8\u3068\u30D7\u30E9\u30C3\u30C8\u30D5\u30A9\u30FC\u30E0\u3092\u53D6\u5F97\u3057\u3001docker pull \u30B3\u30DE\u30F3\u30C9\u3092\u30C6\u30AD\u30B9\u30C8\u3068\u3057\u3066\u8868\u793A\u3057\u307E\u3059",
  query: "\u30A4\u30E1\u30FC\u30B8\u540D\u307E\u305F\u306F\u30AD\u30FC\u30EF\u30FC\u30C9",
  name: "\u30EA\u30DD\u30B8\u30C8\u30EA\u540D\uFF08\u4F8B: library/nginx\uFF09",
  limit: "\u8FD4\u3059\u30EA\u30DD\u30B8\u30C8\u30EA\u306E\u6700\u5927\u6570",
  unknown: "\u672A\u6307\u5B9A",
  invalid: "\u6709\u52B9\u306A\u30A4\u30E1\u30FC\u30B8\u540D\u3001\u30AD\u30FC\u30EF\u30FC\u30C9\u3001\u7D50\u679C\u6570\u3092\u5165\u529B\u3057\u3066\u304F\u3060\u3055\u3044\u3002\u8A73\u7D30\u306F namespace/name\u3001\u516C\u5F0F\u30A4\u30E1\u30FC\u30B8\u306F library/name \u3092\u6307\u5B9A\u3057\u307E\u3059\u3002",
  bad_answer: "Docker Hub \u304B\u3089\u4E88\u671F\u3057\u306A\u3044\u5FDC\u7B54\u304C\u8FD4\u3055\u308C\u307E\u3057\u305F\u3002",
  not_found: "\u4E00\u81F4\u3059\u308B Docker Hub \u30EA\u30DD\u30B8\u30C8\u30EA\u304C\u898B\u3064\u304B\u308A\u307E\u305B\u3093\u3002",
  rate_limited: "Docker Hub \u304C\u30EA\u30AF\u30A8\u30B9\u30C8\u3092\u5236\u9650\u3057\u3066\u3044\u307E\u3059\u3002\u5F8C\u3067\u3082\u3046\u4E00\u5EA6\u304A\u8A66\u3057\u304F\u3060\u3055\u3044\u3002",
  slow: "Docker Hub \u306E\u5FDC\u7B54\u304C\u30BF\u30A4\u30E0\u30A2\u30A6\u30C8\u3057\u307E\u3057\u305F\u3002",
  network: "Docker Hub \u306B\u63A5\u7D9A\u3067\u304D\u307E\u305B\u3093\u3002",
  http: "Docker Hub \u304C HTTP {0} \u3092\u8FD4\u3057\u307E\u3057\u305F\u3002"
};

// src/i18n.ts
var textsFor = (locale = "") => locale.toLowerCase().startsWith("ja") ? ja_default : locale.toLowerCase().startsWith("zh") ? zh_Hans_default : en_default;
var fmt = (text, values) => text.replace(/\{(\w+)\}/g, (_, key) => String(values[key] ?? ""));
var number = (value) => new Intl.NumberFormat("en-US").format(value);
var dateText = (value) => value ? new Intl.DateTimeFormat("en", { dateStyle: "medium", timeZone: "UTC" }).format(new Date(value)) : "";
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
var DockerHub = class {
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
    if (item && this.monotonic() - item.used < fresh) {
      item.used = this.monotonic();
      return item;
    }
    return void 0;
  }
  save(path, raw, at) {
    const bytes = new TextEncoder().encode(JSON.stringify(raw)).byteLength;
    if (bytes > this.limits.cacheBytes) return;
    const old = this.cache.get(path);
    if (old) this.bytes -= old.bytes;
    this.cache.set(path, { raw, at, used: this.monotonic(), bytes });
    this.bytes += bytes;
    while (this.cache.size > this.limits.cache || this.bytes > this.limits.cacheBytes) {
      let oldest;
      for (const [key, value] of this.cache) if (!oldest || value.used < this.cache.get(oldest).used) oldest = key;
      if (oldest === void 0) break;
      this.bytes -= this.cache.get(oldest).bytes;
      this.cache.delete(oldest);
    }
  }
  async response(path, signal) {
    let response;
    try {
      response = await this.fetch(`https://hub.docker.com${path}`, { method: "GET", headers: { Accept: "application/json", "User-Agent": "persona-dockerhub/0.1.0 (https://github.com/nwverse/persona-extensions)" }, signal, redirect: "error" });
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
  async read(path, fresh, signal, validate) {
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
        validate(raw, at);
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
      const old = this.cache.get(path);
      if (old && (!(error instanceof ReadError) || error.code !== "not_found")) {
        old.used = this.monotonic();
        return { ...old, stale: error instanceof ReadError ? error : new ReadError("network") };
      }
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
  async search(query, limit = 12, refresh = false, signal) {
    if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
    const path = searchPath(query, limit);
    const rd = await this.read(path, refresh ? 0 : this.limits.fresh, signal, (raw, at) => {
      parseSearch(raw, at);
    });
    return { ...parseSearch(rd.raw, rd.at), images: parseSearch(rd.raw, rd.at).images.slice(0, limit), stale: rd.stale };
  }
  async repository(name, refresh = false, signal) {
    name = repositoryName(name);
    const rd = await this.read(repositoryPath(name), refresh ? 0 : this.limits.fresh, signal, (raw) => {
      parseRepository(raw, name);
    });
    const image = parseRepository(rd.raw, name);
    const tagRead = await this.read(tagsPath(name), refresh ? 0 : this.limits.fresh, signal, (raw) => {
      parseTags(raw, name);
    });
    image.tags = parseTags(tagRead.raw, name);
    return { image, at: tagRead.at, ...rd.stale ?? tagRead.stale ? { stale: rd.stale ?? tagRead.stale } : {} };
  }
  cacheStats() {
    return { entries: this.cache.size, bytes: this.bytes };
  }
};

// src/tools.ts
function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [
    { name: "dockerhub_search", description: t.toolSearch, input_schema: { type: "object", required: ["query"], properties: { query: { type: "string", description: t.query }, limit: { type: "integer", minimum: 1, maximum: 20, description: t.limit } } } },
    { name: "dockerhub_repository", description: t.toolRepository, input_schema: { type: "object", required: ["name"], properties: { name: { type: "string", description: t.name } } } }
  ];
}
var tools = toolsFor();
function summaryLine(image, index, locale) {
  const t = textsFor(locale);
  return `${index + 1}. ${image.name}${image.official ? ` (${t.official})` : ""}
   ${image.description}
   ${fmt(t.total, { count: number(image.pulls) })} \xB7 ${fmt(t.stars, { count: number(image.stars) })}
   ${image.url}`;
}
function detailText(image, locale) {
  const t = textsFor(locale);
  return `${image.name}
${image.description}
${image.official ? t.official : fmt(t.namespace, { namespace: image.namespace })} \xB7 ${dateText(image.updated)}
${fmt(t.total, { count: number(image.pulls) })}
${t.install}: ${image.pull}
${image.tags.map((tag) => `${tag.name}
  ${tag.digest}
  ${tag.platforms.join(", ") || t.noPlatforms}`).join("\n")}
${image.url}`;
}
function argsFrom(raw, exact) {
  if (exact) {
    const args = {};
    for (const field of ["query", "name"]) for (const token of exact.fieldValues(field)) {
      const value2 = JSON.parse(token.raw);
      if (typeof value2 !== "string") throw new ReadError("invalid");
      args[field] = value2;
    }
    const limit = exact.int64Fields(["limit"]).limit;
    if (limit !== 0n) args.limit = Number(limit);
    return args;
  }
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = raw;
  for (const field of ["query", "name"]) if (value[field] != null && typeof value[field] !== "string") throw new ReadError("invalid");
  if (value.limit != null && (typeof value.limit !== "number" || !Number.isSafeInteger(value.limit))) throw new ReadError("invalid");
  return value;
}
async function callTool(service, name, raw = {}, signal, exact) {
  const args = argsFrom(raw, exact), t = textsFor(service.locale), request = budget(signal, service.limits.tool);
  try {
    if (name === "dockerhub_search") {
      const query = queryText(args.query), limit = args.limit == null ? 10 : Number(args.limit);
      if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
      const result = await service.search(query, limit, false, request.signal);
      return clip((result.images.length ? result.images.map((image, index) => summaryLine(image, index, service.locale)).join("\n") : t.empty) + `
${t.attribution}${result.stale ? `
${errorText(result.stale, service.locale)}` : ""}`, 29999);
    }
    if (name === "dockerhub_repository") {
      const result = await service.repository(repositoryName(args.name), false, request.signal);
      return clip(detailText(result.image, service.locale) + `
${t.attribution}${result.stale ? `
${errorText(result.stale, service.locale)}` : ""}`, 29999);
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
    if (p.contribution && p.contribution !== "dockerhub") throw noContribution(String(p.contribution));
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
function summaryState(value) {
  if (value == null) return void 0;
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("invalid");
  const raw = value;
  if (typeof raw.description !== "string" || typeof raw.official !== "boolean" || typeof raw.pulls !== "number" || !Number.isSafeInteger(raw.pulls) || raw.pulls < 0 || raw.pulls > 1e14 || typeof raw.stars !== "number" || !Number.isSafeInteger(raw.stars) || raw.stars < 0 || raw.stars > 1e14) throw new ReadError("invalid");
  return { name: repositoryName(raw.name), description: clip(raw.description, 500), official: raw.official, pulls: raw.pulls, stars: raw.stars, url: `https://hub.docker.com/r/${repositoryName(raw.name)}` };
}
function readState(raw) {
  if (typeof raw !== "string" || raw.length > 1e4) return initialState;
  try {
    const value = JSON.parse(raw);
    if (!value || Array.isArray(value) || typeof value.found !== "boolean") return initialState;
    return { q: queryText(value.q), found: value.found, image: summaryState(value.image) };
  } catch {
    return initialState;
  }
}
var encodeState = (state) => JSON.stringify(state);
function imageTransition(state, result, id) {
  if (!id.startsWith("image:")) return state;
  const image = result.images.find((candidate) => id === `image:${candidate.name}`);
  return image ? { q: state.q, found: true, image } : state;
}
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
    try {
      const next = { q: queryText(fields2.query), found: true };
      return next.q === state.q && state.found ? { ...next, image: state.image } : next;
    } catch {
      return state;
    }
  }
  if (id === "home") return initialState;
  if (id === "back" && state.found) return { q: state.q, found: true };
  return state;
}
async function loadPage(service, state, refresh, signal) {
  const request = budget(signal, service.limits.page);
  try {
    if (!state.found) return { kind: "empty" };
    if (state.image) return { kind: "detail", result: await service.repository(state.image.name, refresh, request.signal) };
    return { kind: "results", result: await service.search(state.q, 12, refresh, request.signal) };
  } catch (error) {
    checkSignal(signal);
    return { kind: "error", error };
  } finally {
    request.dispose();
  }
}
function page(props) {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale);
  const act = (id, next, title) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "dockerhub", state: encodeState(next), ...title ? { title } : {} } } : { id });
  const text = (key, value, style = "secondary") => /* @__PURE__ */ jsx(Text, { text: clip(value || t.unknown, 2800), style }, key);
  const search = /* @__PURE__ */ jsxs(Section, { title: t.search, children: [
    /* @__PURE__ */ jsx(Field, { name: "query", label: t.search, kind: "text", placeholder: t.searchHint, value: state.q }, "dockerhub-query"),
    /* @__PURE__ */ jsx(Button, { title: t.searchAction, action: act("search") }, "dockerhub-submit")
  ] }, "dockerhub-search");
  const attribution = /* @__PURE__ */ jsx(Section, { children: text("dockerhub-license", t.attribution, "caption") }, "dockerhub-attribution");
  const footer = /* @__PURE__ */ jsxs(Section, { children: [
    /* @__PURE__ */ jsx(Button, { title: t.retry, action: act("refresh") }, "dockerhub-refresh"),
    state.image ? /* @__PURE__ */ jsx(Button, { title: t.back, action: act("back") }, "dockerhub-back") : null,
    /* @__PURE__ */ jsx(Button, { title: t.home, action: act("home") }, "dockerhub-home")
  ] }, "dockerhub-footer");
  const stale = (error) => error ? /* @__PURE__ */ jsx(Section, { children: text("dockerhub-stale-text", errorText(error, props.locale ?? service.locale)) }, "dockerhub-stale") : null;
  if (loaded.kind === "loading") return /* @__PURE__ */ jsxs(View, { title: t.search, state: encodeState(state), children: [
    search,
    /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(Loading, { title: t.loading }, "dockerhub-loading-indicator") }, "dockerhub-loading"),
    attribution
  ] }, "dockerhub");
  if (loaded.kind === "error") return /* @__PURE__ */ jsxs(View, { title: t.search, state: encodeState(state), children: [
    search,
    /* @__PURE__ */ jsxs(Section, { children: [
      text("dockerhub-error-message", errorText(loaded.error, props.locale ?? service.locale)),
      /* @__PURE__ */ jsx(Button, { title: t.retry, action: act("refresh") }, "dockerhub-retry")
    ] }, "dockerhub-error"),
    attribution
  ] }, "dockerhub");
  if (loaded.kind === "empty") return /* @__PURE__ */ jsxs(View, { title: t.search, state: encodeState(state), children: [
    search,
    /* @__PURE__ */ jsx(Section, { children: text("dockerhub-welcome-text", t.welcome, "body") }, "dockerhub-welcome"),
    attribution
  ] }, "dockerhub");
  if (loaded.kind === "detail") {
    const image = loaded.result.image;
    return /* @__PURE__ */ jsxs(View, { title: image.name, state: encodeState(state), children: [
      search,
      /* @__PURE__ */ jsxs(Section, { title: image.name, footer: [image.official ? t.official : fmt(t.namespace, { namespace: image.namespace }), dateText(image.updated)].join(" \xB7 "), children: [
        text("dockerhub-description", image.description, "body"),
        text("dockerhub-total", fmt(t.total, { count: number(image.pulls) })),
        text("dockerhub-stars", fmt(t.stars, { count: number(image.stars) }))
      ] }, "dockerhub-detail"),
      /* @__PURE__ */ jsxs(Section, { title: t.install, children: [
        text("dockerhub-install-command", image.pull, "body"),
        text("dockerhub-install-hint", t.installHint, "caption")
      ] }, "dockerhub-install"),
      /* @__PURE__ */ jsx(Section, { title: fmt(t.tags, { count: number(image.tags.length) }) }, "dockerhub-tags"),
      image.tags.map((tag) => /* @__PURE__ */ jsxs(Section, { title: tag.name, footer: dateText(tag.updated), children: [
        text(`digest-${tag.name}`, tag.digest),
        text(`platforms-${tag.name}`, tag.platforms.join(" \xB7 ") || t.noPlatforms),
        /* @__PURE__ */ jsx(Link, { title: t.openTag, url: `${image.url}/tags?name=${encodeURIComponent(tag.name)}` }, `tag-link-${tag.name}`)
      ] }, `tag-${tag.name}`)),
      /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(Link, { title: t.openImage, url: image.url }, "dockerhub-image-link") }, "dockerhub-links"),
      stale(loaded.result.stale),
      footer,
      attribution
    ] }, "dockerhub");
  }
  return /* @__PURE__ */ jsxs(View, { title: `${t.title}: ${clip(state.q, 100)}`, state: encodeState(state), children: [
    search,
    /* @__PURE__ */ jsx(Section, { children: text("dockerhub-total-results", fmt(t.results, { total: number(loaded.result.total) }), "caption") }, "dockerhub-result-count"),
    loaded.result.images.length ? loaded.result.images.map((image) => /* @__PURE__ */ jsxs(Section, { title: image.name, footer: [image.official ? t.official : t.community, fmt(t.total, { count: number(image.pulls) })].join(" \xB7 "), children: [
      text(`description-${image.name}`, image.description, "body"),
      /* @__PURE__ */ jsx(Button, { title: t.details, action: act(`image:${image.name}`, { ...state, image }, image.name) }, `open-${image.name}`),
      /* @__PURE__ */ jsx(Link, { title: t.openImage, url: image.url }, `link-${image.name}`)
    ] }, `image-${image.name}`)) : /* @__PURE__ */ jsx(Section, { children: text("dockerhub-empty", t.empty) }, "dockerhub-empty-results"),
    stale(loaded.result.stale),
    footer,
    attribution
  ] }, "dockerhub");
}

// src/root.tsx
function Root(options) {
  const [state, setState] = useState(() => readState(options.state));
  const [refresh, setRefresh] = useState(0);
  const [result, setResult] = useState(void 0);
  const identity = JSON.stringify(state) + ":" + refresh;
  useLoadEffect(async (signal) => {
    const loaded2 = await loadPage(options.service, state, refresh > 0, signal);
    if (!signal.aborted) setResult((previous) => previous?.service === options.service && previous.identity === identity && JSON.stringify(previous.loaded) === JSON.stringify(loaded2) ? previous : { service: options.service, identity, loaded: loaded2 });
  }, [options.service, identity]);
  const loaded = result?.service === options.service && result.identity === identity ? result.loaded : { kind: "loading" };
  return page({
    ...options,
    state,
    loaded,
    action: (id, next, title) => {
      if (next && (options.uiVersion ?? 0) >= 3) {
        const push = { ui: "dockerhub", state: encodeState(next), ...title ? { title } : {} };
        return callback(() => ({ push }), { push });
      }
      return callback((values) => {
        if (id === "refresh") setRefresh((previous) => previous + 1);
        else if (id.startsWith("image:")) {
          if (loaded.kind !== "results") return;
          setState((previous) => imageTransition(previous, loaded.result, id));
        } else setState((previous) => transition(previous, id, values));
      }, { submit: true });
    }
  });
}
function root(options) {
  return /* @__PURE__ */ jsx(Root, { ...options }, "dockerhub-root");
}

// src/snapshot.ts
function snapshotPage(service) {
  return { render: async (req) => {
    const state = readState(req.state), loaded = await loadPage(service, state, false, req.signal);
    const renderer = new Renderer({ authorize: () => false });
    try {
      return structuredClone(renderer.mount(page({ service, state, loaded, locale: req.locale, uiVersion: req.ui_version })).view);
    } finally {
      renderer.dispose();
    }
  } };
}

// src/index.ts
var sourceManifest = { ...persona_extension_default, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/dockerhub.mjs", sha256: "" } } };
delete sourceManifest.icon;
delete sourceManifest.replaces;
function createProgram(fetch = (input, init) => globalThis.fetch(input, init), options = {}) {
  let service;
  const current = () => {
    if (!service) throw new Error("DockerHub has not been initialized");
    return service;
  };
  return {
    id: persona_extension_default.id,
    version: persona_extension_default.version,
    manifest: sourceManifest,
    init: (init, _hub) => {
      service = new DockerHub(fetch, init.config, { ...options, locale: init.hub?.locale ?? options.locale });
    },
    namespaces: { connector: connector(current) },
    ui: { dockerhub: componentPage((req) => root({ service: current(), state: req.state, locale: req.locale, uiVersion: req.ui_version })) }
  };
}
var index_default = createProgram();
export {
  DockerHub,
  ReadError,
  Root,
  budget,
  callTool,
  connector,
  createProgram,
  index_default as default,
  encodeState,
  imageTransition,
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
