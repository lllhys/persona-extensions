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
var persona_extension_default = { manifest_version: 1, id: "openlibrary", name: "Open Library \u56FE\u4E66\u53D1\u73B0", version: "0.1.0", description: "\u6309\u4E66\u540D\u3001\u4F5C\u8005\u6216 ISBN \u641C\u7D22\u56FE\u4E66\uFF0C\u67E5\u770B\u51FA\u7248\u4FE1\u606F\u3001\u4F5C\u54C1\u7B80\u4ECB\u548C Open Library \u9605\u8BFB\u5165\u53E3\u3002\u5185\u5BB9\u7531 hub \u901A\u8FC7\u516C\u5F00 API \u83B7\u53D6\uFF0C\u65E0\u9700\u8D26\u53F7\u3002", publisher: "nwverse", homepage: "https://github.com/nwverse/persona-extensions/blob/main/extensions/openlibrary/README.md", tags: ["\u56FE\u4E66", "\u9605\u8BFB", "Open Library"], engines: { plugin_protocol: 1 }, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/openlibrary.mjs", sha256: "" } }, contributes: { connectors: [{ id: "openlibrary", name: "Open Library", capabilities: ["status", "tools"], hosts: ["openlibrary.org"], agent_tools: ["openlibrary_*"] }], ui: [{ id: "discover", title: "\u53D1\u73B0\u56FE\u4E66", placement: "nav_page", icon: "doc" }] }, permissions: { network: ["openlibrary.org"] } };

// src/model.ts
var limits = Object.freeze({ request: 12e3, page: 15e3, tool: 15e3, response: 2 << 20, fresh: 60 * 6e4, cache: 40, cacheBytes: 2 << 20, gap: 250 });
var ReadError = class extends Error {
  constructor(code, status) {
    super(code);
    this.code = code;
    this.status = status;
  }
};
var clip = (text, most) => Array.from(text).length <= most ? text : Array.from(text).slice(0, most).join("").trimEnd() + "\u2026";
var plain = (text) => text.replace(/\s+/gu, " ").trim();
function object(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  return value;
}
function array(value) {
  if (!Array.isArray(value)) throw new ReadError("bad_answer");
  return value;
}
function string(value, fallback = "") {
  if (value == null) return fallback;
  if (typeof value !== "string") throw new ReadError("bad_answer");
  return value;
}
function integer(value, maximum = 1e6) {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0 || value > maximum) throw new ReadError("bad_answer");
  return value;
}
function workKey(value) {
  if (typeof value !== "string" || !/^OL[1-9][0-9]{0,9}W$/.test(value)) throw new ReadError("invalid");
  return value;
}
function queryText(value) {
  if (typeof value !== "string") throw new ReadError("invalid");
  const text = plain(value);
  if (!text || Array.from(text).length > 200 || /[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  return text;
}
function searchPath(query, limit = 20) {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 30) throw new ReadError("invalid");
  return `/search.json?${new URLSearchParams({
    q: queryText(query),
    limit: String(limit),
    fields: "key,title,author_name,first_publish_year,edition_count,ebook_access,public_scan_b,ia"
  })}`;
}
function workPath(key) {
  return `/works/${workKey(key)}.json`;
}
function workURL(key) {
  return `https://openlibrary.org/works/${workKey(key)}`;
}
function access(value) {
  return value === "public" || value === "borrowable" || value === "printdisabled" || value === "no_ebook" ? value : "no_ebook";
}
function identifier(value) {
  const id = string(value);
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(id)) throw new ReadError("bad_answer");
  return id;
}
function parseBook(raw) {
  const value = object(raw), key = workKey(string(value.key).replace(/^\/works\//, ""));
  const title = clip(plain(string(value.title)), 500);
  if (!title) throw new ReadError("bad_answer");
  const authors = array(value.author_name ?? []).slice(0, 8).map((name) => clip(plain(string(name)), 200)).filter(Boolean);
  const year = value.first_publish_year == null ? void 0 : integer(value.first_publish_year, 2200);
  const editions = value.edition_count == null ? void 0 : integer(value.edition_count);
  const availability = access(value.ebook_access);
  let readingURL;
  if (availability === "public" && value.public_scan_b === true) {
    const ids = array(value.ia ?? []).slice(0, 100).map((value2) => {
      try {
        return identifier(value2);
      } catch {
        return "";
      }
    });
    const publicID = ids.find((id) => /gutenberg|projectgutenberg/i.test(id));
    if (publicID) readingURL = `https://openlibrary.org/books/ia:${encodeURIComponent(publicID)}`;
  }
  return {
    key,
    title,
    authors,
    ...year === void 0 ? {} : { year },
    ...editions === void 0 ? {} : { editions },
    access: availability,
    url: workURL(key),
    ...readingURL ? { readingURL } : {}
  };
}
function parseSearch(raw, at) {
  const value = object(raw), total = integer(value.numFound ?? value.num_found, 1e9);
  const seen = /* @__PURE__ */ new Set();
  const books = array(value.docs).slice(0, 30).map(parseBook).filter((book2) => {
    if (seen.has(book2.key)) return false;
    seen.add(book2.key);
    return true;
  });
  return { books, total, at };
}
function description(value) {
  if (value == null) return "";
  if (typeof value === "string") return clip(plain(value), 8e3);
  const item = object(value);
  if (item.type != null && item.type !== "/type/text") throw new ReadError("bad_answer");
  return clip(plain(string(item.value)), 8e3);
}
function parseWork(raw, base, at) {
  const value = object(raw), key = workKey(string(value.key).replace(/^\/works\//, ""));
  const title = clip(plain(string(value.title)), 500);
  if (key !== base.key || !title || title !== base.title) throw new ReadError("bad_answer");
  const subjects = array(value.subjects ?? []).slice(0, 12).map((item) => clip(plain(string(item)), 100)).filter(Boolean);
  return { ...base, title, description: description(value.description), subjects, at };
}

// src/locales/en.json
var en_default = {
  discover: "Discover books",
  search: "Search Open Library",
  searchHint: "Title, author or ISBN",
  searchAction: "Search",
  results: "Books matching {query}",
  empty: "No books found. Try a title, author or ISBN.",
  account: "Public Open Library catalog",
  loading: "Searching the catalog",
  retry: "Try again",
  back: "Back to results",
  backDiscover: "Discover books",
  year: "First published {year}",
  editions: "{count} editions",
  unknownYear: "Publication year unavailable",
  public: "Public edition listed",
  borrowable: "Borrowing may require an account",
  printdisabled: "Restricted edition; no open reading link",
  no_ebook: "No public ebook listed",
  read: "Open public edition",
  open: "View on Open Library",
  subjects: "Subjects",
  noDescription: "No description is available for this work.",
  description: "About this work",
  refresh: "Refresh",
  attribution: "Catalog data and reading availability are provided by Open Library. Availability may change.",
  readAt: "Catalog read {at}",
  toolSearch: "Search Open Library by title, author or ISBN and return canonical book links",
  toolWork: "Read an Open Library work's description, subjects and reading availability",
  query: "Book title, author or ISBN",
  work: "Open Library work identifier, for example OL59800W",
  limit: "Maximum number of results",
  invalid: "Enter a valid search or work identifier.",
  bad_answer: "The catalog returned an unexpected response.",
  not_found: "The requested work was not found.",
  rate_limited: "The catalog is rate-limiting requests. Try again later.",
  slow: "The catalog took too long to respond.",
  network: "The catalog could not be reached.",
  http: "The catalog returned HTTP {0}."
};

// src/locales/zh-Hans.json
var zh_Hans_default = {
  discover: "\u53D1\u73B0\u56FE\u4E66",
  search: "\u641C\u7D22 Open Library",
  searchHint: "\u4E66\u540D\u3001\u4F5C\u8005\u6216 ISBN",
  searchAction: "\u641C\u7D22",
  results: "\u4E0E\u201C{query}\u201D\u76F8\u5173\u7684\u56FE\u4E66",
  empty: "\u6CA1\u6709\u627E\u5230\u56FE\u4E66\uFF0C\u53EF\u4EE5\u6362\u4E66\u540D\u3001\u4F5C\u8005\u6216 ISBN \u518D\u8BD5\u3002",
  account: "Open Library \u516C\u5171\u76EE\u5F55",
  loading: "\u6B63\u5728\u68C0\u7D22\u76EE\u5F55",
  retry: "\u91CD\u8BD5",
  back: "\u8FD4\u56DE\u7ED3\u679C",
  backDiscover: "\u53D1\u73B0\u56FE\u4E66",
  year: "\u9996\u6B21\u51FA\u7248\u4E8E {year} \u5E74",
  editions: "{count} \u4E2A\u7248\u672C",
  unknownYear: "\u51FA\u7248\u5E74\u4EFD\u672A\u77E5",
  public: "\u76EE\u5F55\u5217\u51FA\u516C\u5171\u7248\u672C",
  borrowable: "\u501F\u9605\u53EF\u80FD\u9700\u8981\u8D26\u53F7",
  printdisabled: "\u53D7\u9650\u7248\u672C\uFF0C\u6CA1\u6709\u516C\u5F00\u9605\u8BFB\u94FE\u63A5",
  no_ebook: "\u76EE\u5F55\u6CA1\u6709\u516C\u5F00\u7535\u5B50\u7248",
  read: "\u6253\u5F00\u516C\u5171\u7248\u672C",
  open: "\u5728 Open Library \u67E5\u770B",
  subjects: "\u4E3B\u9898",
  noDescription: "\u8FD9\u90E8\u4F5C\u54C1\u6682\u65F6\u6CA1\u6709\u7B80\u4ECB\u3002",
  description: "\u4F5C\u54C1\u7B80\u4ECB",
  refresh: "\u5237\u65B0",
  attribution: "\u76EE\u5F55\u548C\u9605\u8BFB\u53EF\u7528\u6027\u7531 Open Library \u63D0\u4F9B\uFF0C\u53EF\u80FD\u968F\u65F6\u53D8\u5316\u3002",
  readAt: "\u8BFB\u53D6\u76EE\u5F55\u65F6\u95F4\uFF1A{at}",
  toolSearch: "\u6309\u4E66\u540D\u3001\u4F5C\u8005\u6216 ISBN \u641C\u7D22 Open Library\uFF0C\u8FD4\u56DE\u539F\u59CB\u4F5C\u54C1\u94FE\u63A5",
  toolWork: "\u8BFB\u53D6 Open Library \u4F5C\u54C1\u7B80\u4ECB\u3001\u4E3B\u9898\u548C\u9605\u8BFB\u53EF\u7528\u6027",
  query: "\u4E66\u540D\u3001\u4F5C\u8005\u6216 ISBN",
  work: "Open Library \u4F5C\u54C1\u6807\u8BC6\uFF0C\u4F8B\u5982 OL59800W",
  limit: "\u6700\u591A\u8FD4\u56DE\u591A\u5C11\u6761\u7ED3\u679C",
  invalid: "\u8BF7\u8F93\u5165\u6709\u6548\u7684\u641C\u7D22\u5185\u5BB9\u6216\u4F5C\u54C1\u6807\u8BC6\u3002",
  bad_answer: "\u76EE\u5F55\u8FD4\u56DE\u4E86\u65E0\u6CD5\u8BC6\u522B\u7684\u54CD\u5E94\u3002",
  not_found: "\u6CA1\u6709\u627E\u5230\u8FD9\u90E8\u4F5C\u54C1\u3002",
  rate_limited: "\u76EE\u5F55\u6B63\u5728\u9650\u5236\u8BF7\u6C42\uFF0C\u8BF7\u7A0D\u540E\u91CD\u8BD5\u3002",
  slow: "\u76EE\u5F55\u54CD\u5E94\u8D85\u65F6\u3002",
  network: "\u65E0\u6CD5\u8FDE\u63A5\u76EE\u5F55\u3002",
  http: "\u76EE\u5F55\u8FD4\u56DE HTTP {0}\u3002"
};

// src/locales/ja.json
var ja_default = {
  discover: "\u672C\u3092\u63A2\u3059",
  search: "Open Library \u3092\u691C\u7D22",
  searchHint: "\u66F8\u540D\u3001\u8457\u8005\u3001ISBN",
  searchAction: "\u691C\u7D22",
  results: "\u300C{query}\u300D\u306E\u691C\u7D22\u7D50\u679C",
  empty: "\u672C\u304C\u898B\u3064\u304B\u308A\u307E\u305B\u3093\u3002\u66F8\u540D\u3001\u8457\u8005\u3001ISBN \u3092\u304A\u8A66\u3057\u304F\u3060\u3055\u3044\u3002",
  account: "Open Library \u516C\u958B\u30AB\u30BF\u30ED\u30B0",
  loading: "\u30AB\u30BF\u30ED\u30B0\u3092\u691C\u7D22\u3057\u3066\u3044\u307E\u3059",
  retry: "\u518D\u8A66\u884C",
  back: "\u691C\u7D22\u7D50\u679C\u306B\u623B\u308B",
  backDiscover: "\u672C\u3092\u63A2\u3059",
  year: "\u521D\u7248 {year} \u5E74",
  editions: "{count} \u7248",
  unknownYear: "\u51FA\u7248\u5E74\u4E0D\u660E",
  public: "\u516C\u958B\u7248\u304C\u767B\u9332\u3055\u308C\u3066\u3044\u307E\u3059",
  borrowable: "\u8CB8\u51FA\u306B\u306F\u30A2\u30AB\u30A6\u30F3\u30C8\u304C\u5FC5\u8981\u306A\u5834\u5408\u304C\u3042\u308A\u307E\u3059",
  printdisabled: "\u5236\u9650\u4ED8\u304D\u306E\u7248\u3002\u516C\u958B\u8AAD\u66F8\u30EA\u30F3\u30AF\u306F\u3042\u308A\u307E\u305B\u3093",
  no_ebook: "\u516C\u958B\u96FB\u5B50\u66F8\u7C4D\u306F\u767B\u9332\u3055\u308C\u3066\u3044\u307E\u305B\u3093",
  read: "\u516C\u958B\u7248\u3092\u958B\u304F",
  open: "Open Library \u3067\u8868\u793A",
  subjects: "\u4E3B\u984C",
  noDescription: "\u3053\u306E\u4F5C\u54C1\u306E\u8AAC\u660E\u306F\u3042\u308A\u307E\u305B\u3093\u3002",
  description: "\u4F5C\u54C1\u306B\u3064\u3044\u3066",
  refresh: "\u66F4\u65B0",
  attribution: "\u30AB\u30BF\u30ED\u30B0\u3068\u8AAD\u66F8\u306E\u53EF\u5426\u306F Open Library \u304B\u3089\u63D0\u4F9B\u3055\u308C\u307E\u3059\u3002",
  readAt: "\u30AB\u30BF\u30ED\u30B0\u53D6\u5F97\u6642\u523B\uFF1A{at}",
  toolSearch: "\u66F8\u540D\u3001\u8457\u8005\u3001ISBN \u3067 Open Library \u3092\u691C\u7D22\u3057\u3001\u4F5C\u54C1\u3078\u306E\u30EA\u30F3\u30AF\u3092\u8FD4\u3057\u307E\u3059",
  toolWork: "Open Library \u306E\u4F5C\u54C1\u8AAC\u660E\u3001\u4E3B\u984C\u3001\u8AAD\u66F8\u306E\u53EF\u5426\u3092\u8AAD\u307F\u307E\u3059",
  query: "\u66F8\u540D\u3001\u8457\u8005\u3001ISBN",
  work: "Open Library \u306E\u4F5C\u54C1 ID\uFF08\u4F8B\uFF1AOL59800W\uFF09",
  limit: "\u6700\u5927\u7D50\u679C\u6570",
  invalid: "\u6709\u52B9\u306A\u691C\u7D22\u8A9E\u307E\u305F\u306F\u4F5C\u54C1 ID \u3092\u5165\u529B\u3057\u3066\u304F\u3060\u3055\u3044\u3002",
  bad_answer: "\u30AB\u30BF\u30ED\u30B0\u304B\u3089\u4E88\u671F\u3057\u306A\u3044\u5FDC\u7B54\u304C\u3042\u308A\u307E\u3057\u305F\u3002",
  not_found: "\u4F5C\u54C1\u304C\u898B\u3064\u304B\u308A\u307E\u305B\u3093\u3002",
  rate_limited: "\u30EA\u30AF\u30A8\u30B9\u30C8\u304C\u5236\u9650\u3055\u308C\u3066\u3044\u307E\u3059\u3002\u5F8C\u3067\u3082\u3046\u4E00\u5EA6\u304A\u8A66\u3057\u304F\u3060\u3055\u3044\u3002",
  slow: "\u30AB\u30BF\u30ED\u30B0\u306E\u5FDC\u7B54\u304C\u30BF\u30A4\u30E0\u30A2\u30A6\u30C8\u3057\u307E\u3057\u305F\u3002",
  network: "\u30AB\u30BF\u30ED\u30B0\u306B\u63A5\u7D9A\u3067\u304D\u307E\u305B\u3093\u3002",
  http: "\u30AB\u30BF\u30ED\u30B0\u304C HTTP {0} \u3092\u8FD4\u3057\u307E\u3057\u305F\u3002"
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
var OpenLibrary = class {
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
      response = await this.fetch(`https://openlibrary.org${path}`, {
        method: "GET",
        headers: { Accept: "application/json", "User-Agent": "persona-openlibrary/0.1.0" },
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
        if (request.signal.aborted) {
          throw error instanceof Error && error.name === "AbortError" ? error : new DOMException("Cancelled", "AbortError");
        }
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
    const rd = await this.read(searchPath(query, limit), refresh ? 0 : this.limits.fresh, signal);
    return { ...parseSearch(rd.raw, rd.at), stale: rd.stale };
  }
  async work(key, base, refresh = false, signal) {
    const rd = await this.read(workPath(key), refresh ? 0 : this.limits.fresh, signal);
    return { ...parseWork(rd.raw, base, rd.at), stale: rd.stale };
  }
  cacheStats() {
    return { entries: this.cache.size, bytes: this.bytes };
  }
};

// src/tools.ts
function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [
    { name: "openlibrary_search", description: t.toolSearch, input_schema: { type: "object", required: ["query"], properties: { query: { type: "string", description: t.query }, limit: { type: "integer", minimum: 1, maximum: 30, description: t.limit } } } },
    { name: "openlibrary_work", description: t.toolWork, input_schema: { type: "object", required: ["work"], properties: { work: { type: "string", description: t.work } } } }
  ];
}
var tools = toolsFor();
function line(book2, index) {
  return `${index + 1}. ${clip(book2.title, 300)}
   ${[book2.authors.join(", "), book2.year, book2.editions ? `${book2.editions} editions` : "", book2.access].filter(Boolean).join(" \xB7 ")}
   ${book2.url}
   Work ID: ${book2.key}${book2.readingURL ? `
   Public edition: ${book2.readingURL}` : ""}`;
}
function detail(work) {
  return `${work.title}
${work.authors.join(", ")}
${work.url}
${work.readingURL ? `Public edition: ${work.readingURL}` : `Availability: ${work.access}`}

${work.description || "No description available."}

Subjects: ${work.subjects.join(", ")}`;
}
function argsFrom(raw, exact) {
  if (exact) {
    const args = {};
    for (const key of ["query", "work"]) for (const token of exact.fieldValues(key)) {
      const value2 = JSON.parse(token.raw);
      if (typeof value2 !== "string") throw new ReadError("invalid");
      args[key] = value2;
    }
    const parsed = exact.int64Fields(["limit"]).limit;
    if (parsed !== 0n) args.limit = parsed;
    return args;
  }
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = raw;
  if (value.query != null && typeof value.query !== "string" || value.work != null && typeof value.work !== "string") throw new ReadError("invalid");
  if (value.limit != null && (typeof value.limit !== "number" || !Number.isSafeInteger(value.limit))) throw new ReadError("invalid");
  return value;
}
async function callTool(service, name, raw = {}, signal, exact) {
  const args = argsFrom(raw, exact), t = textsFor(service.locale), request = budget(signal, service.limits.tool);
  try {
    if (name === "openlibrary_search") {
      if (typeof args.query !== "string") throw new ReadError("invalid");
      const limit = args.limit == null ? 10 : Number(args.limit);
      if (!Number.isSafeInteger(limit) || limit < 1 || limit > 30) throw new ReadError("invalid");
      const result = await service.search(args.query, limit, false, request.signal);
      return clip(result.books.length ? result.books.map(line).join("\n").trim() : fmt(t.results, { query: args.query }) + "\n" + t.empty, 29999);
    }
    if (name === "openlibrary_work") {
      if (typeof args.work !== "string") throw new ReadError("invalid");
      const search = await service.search(args.work, 30, false, request.signal);
      const base = search.books.find((book2) => book2.key === args.work);
      if (!base) throw new ReadError("not_found");
      return clip(detail(await service.work(base.key, base, false, request.signal)), 29999);
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
    if (p.contribution && p.contribution !== "openlibrary") throw noContribution(String(p.contribution));
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
var initialState = { v: "discover", q: "", k: "" };
function book(raw) {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return void 0;
  const b = raw;
  if (typeof b.key !== "string" || typeof b.title !== "string" || !Array.isArray(b.authors) || b.authors.some((x) => typeof x !== "string") || typeof b.url !== "string" || b.url !== `https://openlibrary.org/works/${b.key}` || typeof b.access !== "string" || !["public", "borrowable", "printdisabled", "no_ebook"].includes(b.access)) return void 0;
  if (b.year != null && (typeof b.year !== "number" || !Number.isSafeInteger(b.year) || b.year < 0 || b.year > 2200)) return void 0;
  if (b.editions != null && (typeof b.editions !== "number" || !Number.isSafeInteger(b.editions) || b.editions < 0)) return void 0;
  if (b.readingURL != null && (typeof b.readingURL !== "string" || !/^https:\/\/openlibrary\.org\/books\/ia:[A-Za-z0-9][A-Za-z0-9._%-]{0,150}$/.test(b.readingURL))) return void 0;
  try {
    workKey(b.key);
  } catch {
    return void 0;
  }
  return {
    key: b.key,
    title: clip(b.title, 500),
    authors: b.authors.slice(0, 8).map((x) => clip(x, 200)),
    ...b.year == null ? {} : { year: b.year },
    ...b.editions == null ? {} : { editions: b.editions },
    access: b.access,
    url: b.url,
    ...typeof b.readingURL === "string" ? { readingURL: b.readingURL } : {}
  };
}
function readState(raw) {
  if (typeof raw !== "string" || raw.length > 8e3) return initialState;
  try {
    const value = JSON.parse(raw);
    if (!value || Array.isArray(value)) return initialState;
    const state = {
      v: value.v === "search" || value.v === "work" ? value.v : "discover",
      q: typeof value.q === "string" ? clip(value.q.trim(), 200) : "",
      k: typeof value.k === "string" && value.k ? workKey(value.k) : "",
      ...value.b === void 0 ? {} : { b: book(value.b) }
    };
    if (state.v === "work" && (!state.k || !state.b) || state.v === "search" && !state.q) return initialState;
    return state;
  } catch {
    return initialState;
  }
}
var encodeState = (state) => JSON.stringify(state);
function transition(state, id, values = {}) {
  const [kind, argument] = id.split(":", 2);
  if (kind === "work" && argument && state.b?.key === argument) return { ...state, v: "work", k: argument };
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
  const string2 = (key) => typeof fields2[key] === "string" ? fields2[key] : void 0;
  if (kind === "search") {
    const raw = string2("query") ?? "";
    const query = clip(raw.trim(), 200);
    return query ? { v: "search", q: query, k: "" } : state;
  }
  if (kind === "back") return state.q ? { v: "search", q: state.q, k: "" } : initialState;
  if (kind === "home") return initialState;
  return state;
}
async function loadPage(service, state, refresh, signal) {
  const request = budget(signal, service.limits.page);
  try {
    if (state.v === "work") {
      if (!state.b) throw new ReadError("invalid");
      return { kind: "work", work: await service.work(state.k, state.b, refresh, request.signal) };
    }
    if (state.v === "search") return { kind: "search", result: await service.search(state.q, 20, refresh, request.signal) };
    return { kind: "empty" };
  } catch (error) {
    checkSignal(signal);
    return { kind: "error", error };
  } finally {
    request.dispose();
  }
}
function page(props) {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale), tz = props.tz ?? "Local";
  const act = (id, next, title) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "discover", state: encodeState(next), ...title ? { title } : {} } } : { id });
  const text = (key, value, style = "secondary") => /* @__PURE__ */ jsx(Text, { text: clip(value, 8e3), style }, key);
  const search = /* @__PURE__ */ jsxs(Section, { title: t.search, children: [
    /* @__PURE__ */ jsx(Field, { name: "query", label: t.search, kind: "text", placeholder: t.searchHint, value: state.q }, "book-query"),
    /* @__PURE__ */ jsx(Button, { title: t.searchAction, action: act("search") }, "book-search-submit")
  ] }, "book-search");
  const navigation = /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(Button, { title: state.q ? t.back : t.backDiscover, action: act("back") }, "book-back") }, "book-navigation");
  if (loaded.kind === "loading") return /* @__PURE__ */ jsxs(View, { title: t.discover, state: encodeState(state), children: [
    search,
    state.v !== "discover" ? navigation : null,
    /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(Loading, { title: t.loading }, "book-loading-indicator") }, "book-loading")
  ] }, "openlibrary");
  if (loaded.kind === "error") return /* @__PURE__ */ jsxs(View, { title: t.discover, state: encodeState(state), children: [
    search,
    state.v !== "discover" ? navigation : null,
    /* @__PURE__ */ jsxs(Section, { children: [
      text("book-error-message", errorText(loaded.error, props.locale ?? service.locale)),
      /* @__PURE__ */ jsx(Button, { title: t.retry, action: act("refresh") }, "book-retry")
    ] }, "book-error")
  ] }, "openlibrary");
  if (state.v === "work") {
    if (loaded.kind !== "work") throw new ReadError("invalid");
    const work = loaded.work;
    return /* @__PURE__ */ jsxs(View, { title: work.title, state: encodeState(state), children: [
      /* @__PURE__ */ jsxs(Section, { title: work.title, footer: [work.year ? fmt(t.year, { year: work.year }) : t.unknownYear, work.editions ? fmt(t.editions, { count: work.editions }) : "", t[work.access]].filter(Boolean).join(" \xB7 "), children: [
        text("work-authors", work.authors.join(" \xB7 "), "body"),
        text("work-description", work.description || t.noDescription, "body")
      ] }, "work-details"),
      work.subjects.length ? /* @__PURE__ */ jsx(Section, { title: t.subjects, children: text("work-subject-list", work.subjects.join(" \xB7 ")) }, "work-subjects") : null,
      /* @__PURE__ */ jsxs(Section, { children: [
        work.readingURL ? /* @__PURE__ */ jsx(Link, { title: t.read, url: work.readingURL }, "work-read") : null,
        /* @__PURE__ */ jsx(Link, { title: t.open, url: work.url }, "work-open"),
        /* @__PURE__ */ jsx(Button, { title: t.back, action: act("back") }, "work-back")
      ] }, "work-actions"),
      /* @__PURE__ */ jsx(Section, { footer: `${fmt(t.readAt, { at: readTime(work.at, tz, props.locale ?? service.locale) })}${work.stale ? ` \xB7 ${errorText(work.stale, props.locale ?? service.locale)}` : ""}`, children: /* @__PURE__ */ jsx(Button, { title: t.refresh, action: act("refresh") }, "work-refresh") }, "work-footer"),
      /* @__PURE__ */ jsx(Section, { children: text("work-license", t.attribution, "caption") }, "work-attribution")
    ] }, "openlibrary");
  }
  const row = (book2) => /* @__PURE__ */ jsx(
    Row,
    {
      title: book2.title,
      subtitle: [book2.authors.join(" \xB7 "), book2.year ? fmt(t.year, { year: book2.year }) : "", t[book2.access]].filter(Boolean).join(" \xB7 "),
      action: act(`work:${book2.key}`, { v: "work", q: state.q, k: book2.key, b: book2 }, book2.title)
    },
    `book-${book2.key}`
  );
  return /* @__PURE__ */ jsxs(View, { title: state.v === "search" ? fmt(t.results, { query: clip(state.q, 100) }) : t.discover, state: encodeState(state), children: [
    search,
    state.v === "search" ? /* @__PURE__ */ jsxs(Section, { title: fmt(t.results, { query: clip(state.q, 100) }), children: [
      loaded.kind === "search" && loaded.result.books.length ? loaded.result.books.map(row) : text("book-empty", t.empty),
      /* @__PURE__ */ jsx(Button, { title: t.backDiscover, action: act("home") }, "book-home")
    ] }, "book-results") : /* @__PURE__ */ jsx(Section, { children: text("book-welcome-text", t.searchHint, "body") }, "book-welcome"),
    /* @__PURE__ */ jsx(Section, { children: text("book-license", t.attribution, "caption") }, "book-attribution")
  ] }, "openlibrary");
}

// src/root.tsx
function Root(options) {
  const [state, setState] = useState(() => readState(options.state));
  const [refresh, setRefresh] = useState(0);
  const [result, setResult] = useState(void 0);
  const identity = `${state.v}:${state.q}:${state.k}:${refresh}`;
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
        const push = { ui: "discover", state: encodeState(next), ...title ? { title } : {} };
        return callback(() => ({ push }), { push });
      }
      return callback((values) => {
        if (id === "refresh") setRefresh((previous) => previous + 1);
        else if (id.startsWith("work:")) {
          const key = id.slice("work:".length);
          const base = result?.loaded.kind === "search" ? result.loaded.result.books.find((book2) => book2.key === key) : void 0;
          if (base) setState({ v: "work", q: state.q, k: key, b: base });
        } else setState((previous) => transition(previous, id, values));
      }, { submit: true });
    }
  });
}
function root(options) {
  return /* @__PURE__ */ jsx(Root, { ...options }, "openlibrary-root");
}

// src/snapshot.ts
function snapshotPage(service) {
  const render = async (req, refresh = false) => {
    const state = readState(req.state), loaded = await loadPage(service, state, refresh);
    const renderer = new Renderer({ authorize: () => false });
    try {
      return structuredClone(renderer.mount(page({ service, state, loaded, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })).view);
    } finally {
      renderer.dispose();
    }
  };
  return { render, action: async (req) => {
    const state = transition(readState(req.state), req.action ?? "", req.values);
    return { view: await render({ ...req, state: encodeState(state) }, req.action === "refresh") };
  } };
}

// src/index.ts
var sourceManifest = { ...persona_extension_default, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/openlibrary.mjs", sha256: "" } } };
function createProgram(fetch = (input, init) => globalThis.fetch(input, init), options = {}) {
  let service;
  const current = () => {
    if (!service) throw new Error("Open Library has not been initialized");
    return service;
  };
  return {
    id: persona_extension_default.id,
    version: persona_extension_default.version,
    manifest: sourceManifest,
    init: (init, _hub) => {
      service = new OpenLibrary(fetch, init.config, { ...options, locale: init.hub?.locale ?? options.locale });
    },
    namespaces: { connector: connector(current) },
    ui: { discover: componentPage((req) => root({ service: current(), state: req.state, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })) }
  };
}
var index_default = createProgram();
export {
  OpenLibrary,
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
