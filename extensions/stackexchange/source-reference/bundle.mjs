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
var persona_extension_default = { manifest_version: 1, id: "stackexchange", name: "Stack Exchange", version: "0.1.1", icon: "assets/icon.png", description: "Search developer questions on Stack Overflow and selected Stack Exchange sites. Read accepted or highly voted answers with code, attribution and original links. Anonymous, read-only public API access.", publisher: "nwverse", homepage: "https://github.com/nwverse/persona-extensions/blob/main/extensions/stackexchange/README.md", tags: ["Development", "Questions", "Answers", "Stack Overflow"], engines: { plugin_protocol: 1 }, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/stackexchange.mjs", sha256: "" } }, contributes: { connectors: [{ id: "stackexchange", name: "Stack Exchange", capabilities: ["status", "tools"], hosts: ["api.stackexchange.com"], agent_tools: ["stackexchange_*"] }], ui: [{ id: "answers", title: "Stack Exchange", placement: "nav_page", icon: "search" }] }, permissions: { network: ["api.stackexchange.com"] } };

// src/model.ts
var limits = Object.freeze({ request: 12e3, page: 15e3, tool: 15e3, response: 2 << 20, fresh: 60 * 6e4, cache: 40, cacheBytes: 2 << 20, gap: 1e3 });
var ReadError = class extends Error {
  constructor(code, status) {
    super(code);
    this.code = code;
    this.status = status;
  }
};
var sites = Object.freeze({
  stackoverflow: { name: "Stack Overflow", host: "stackoverflow.com" },
  superuser: { name: "Super User", host: "superuser.com" },
  serverfault: { name: "Server Fault", host: "serverfault.com" },
  unix: { name: "Unix & Linux", host: "unix.stackexchange.com" },
  askubuntu: { name: "Ask Ubuntu", host: "askubuntu.com" },
  apple: { name: "Ask Different", host: "apple.stackexchange.com" },
  softwareengineering: { name: "Software Engineering", host: "softwareengineering.stackexchange.com" },
  codereview: { name: "Code Review", host: "codereview.stackexchange.com" },
  webapps: { name: "Web Applications", host: "webapps.stackexchange.com" },
  dba: { name: "Database Administrators", host: "dba.stackexchange.com" },
  devops: { name: "DevOps", host: "devops.stackexchange.com" },
  security: { name: "Information Security", host: "security.stackexchange.com" }
});
var siteIDs = Object.keys(sites);
var clip = (text, most) => text.length <= most ? text : text.slice(0, most).trimEnd() + "\u2026";
var oneLine = (text) => text.replace(/\s+/gu, " ").trim();
function queryText(value) {
  if (typeof value !== "string" || /[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  const query = oneLine(value);
  if (!query || Array.from(query).length > 200) throw new ReadError("invalid");
  return query;
}
function siteID(value) {
  if (value == null || value === "") return "stackoverflow";
  if (typeof value !== "string" || !Object.hasOwn(sites, value)) throw new ReadError("invalid");
  return value;
}
function tagText(value) {
  if (value == null || value === "") return "";
  if (typeof value !== "string" || !/^[a-z0-9][a-z0-9+#.-]{0,59}$/.test(value) || /[.-]$/.test(value)) throw new ReadError("invalid");
  return value;
}
function questionID(value) {
  if (typeof value === "string" && /^[1-9][0-9]{0,9}$/.test(value)) value = Number(value);
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 1 || value > 9999999999) throw new ReadError("invalid");
  return value;
}
var questionFilter = "!*10eWkBRxImNF3.k5tm)KG1";
var answerFilter = "!)bSBcEtaH)4v4wIT.S(z)a7Ht5P";
function searchPath(query, site, tag = "", limit = 12) {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
  const params = new URLSearchParams({ site: siteID(site), q: queryText(query), pagesize: String(limit), order: "desc", sort: "relevance", filter: questionFilter });
  if (tagText(tag)) params.set("tagged", tagText(tag));
  return `/2.3/search/advanced?${params}`;
}
function answersPath(id, site) {
  return `/2.3/questions/${questionID(id)}/answers?${new URLSearchParams({ site: siteID(site), pagesize: "10", order: "desc", sort: "votes", filter: answerFilter })}`;
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
function integer(value, minimum = 0, maximum = 1e9) {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < minimum || value > maximum) throw new ReadError("bad_answer");
  return value;
}
function boolean(value) {
  if (typeof value !== "boolean") throw new ReadError("bad_answer");
  return value;
}
var entities = { amp: "&", lt: "<", gt: ">", quot: '"', apos: "'", nbsp: " ", ndash: "\u2013", mdash: "\u2014", hellip: "\u2026", rsquo: "\u2019", lsquo: "\u2018", ldquo: "\u201C", rdquo: "\u201D", times: "\xD7", copy: "\xA9", reg: "\xAE", trade: "\u2122", eacute: "\xE9", egrave: "\xE8", agrave: "\xE0", ccedil: "\xE7", ouml: "\xF6", uuml: "\xFC", auml: "\xE4", szlig: "\xDF" };
function decodeEntities(value) {
  return value.replace(/&(?:#(\d{1,7})|#x([a-fA-F0-9]{1,6})|([a-zA-Z][a-zA-Z0-9]{1,30}));/g, (raw, decimal, hex, name) => {
    if (name) return entities[name] ?? entities[name.toLowerCase()] ?? raw;
    const point = Number.parseInt(decimal ?? hex, decimal ? 10 : 16);
    if (point > 1114111 || point >= 55296 && point <= 57343 || point < 32 && point !== 9 && point !== 10 && point !== 13) return raw;
    return String.fromCodePoint(point);
  });
}
function canonicalURL(raw, host, path) {
  let url;
  try {
    url = new URL(decodeEntities(raw));
  } catch {
    throw new ReadError("bad_answer");
  }
  if (url.protocol !== "https:" || url.hostname !== host || url.port || url.username || url.password || !path.test(url.pathname)) throw new ReadError("bad_answer");
  url.search = "";
  url.hash = "";
  return url.href;
}
function wrapper(raw) {
  const root2 = object(raw);
  if (root2.error_id != null || root2.error_message != null || root2.error_name != null) throw new ReadError("bad_answer");
  const backoff = root2.backoff == null ? 0 : integer(root2.backoff, 0, 3600);
  const quotaRemaining = root2.quota_remaining == null ? void 0 : integer(root2.quota_remaining, 0, 1e6);
  const items = array(root2.items);
  return { items, backoff, quotaRemaining };
}
function person(raw, host) {
  if (raw == null) return { author: "Anonymous" };
  const owner = object(raw), author = clip(oneLine(decodeEntities(string(owner.display_name))), 200);
  if (!author) throw new ReadError("bad_answer");
  const link = owner.link == null ? void 0 : canonicalURL(string(owner.link), host, /^\/users\/[1-9][0-9]*(?:\/|$)/);
  return { author, ...link ? { authorURL: link } : {} };
}
function license(raw) {
  const value = string(raw, "CC BY-SA");
  if (!/^CC BY-SA (?:2\.5|3\.0|4\.0)$/.test(value)) throw new ReadError("bad_answer");
  return value;
}
function parseQuestions(raw, site, at) {
  const { items, quotaRemaining } = wrapper(raw), { host } = sites[site], seen = /* @__PURE__ */ new Set();
  const questions = items.slice(0, 20).map((item) => {
    const question = object(item), id = integer(question.question_id, 1, 9999999999);
    if (seen.has(id)) throw new ReadError("bad_answer");
    seen.add(id);
    const title = clip(oneLine(decodeEntities(string(question.title))), 500);
    if (!title) throw new ReadError("bad_answer");
    const tags = array(question.tags).slice(0, 10).map((value) => tagText(string(value)));
    if (new Set(tags).size !== tags.length) throw new ReadError("bad_answer");
    const acceptedAnswerID = question.accepted_answer_id == null ? void 0 : integer(question.accepted_answer_id, 1, 9999999999);
    return { id, title, url: canonicalURL(string(question.link), host, new RegExp(`^/questions/${id}(?:/|$)`)), score: integer(question.score, -1e6), answerCount: integer(question.answer_count), ...acceptedAnswerID ? { acceptedAnswerID } : {}, tags, license: license(question.content_license), ...person(question.owner, host) };
  });
  return { questions, at, ...quotaRemaining === void 0 ? {} : { quotaRemaining } };
}
function markdownText(value) {
  return value.replace(/\\/g, "\\\\").replace(/([`*_])/g, "\\$1").replace(/\[/g, "\\[").replace(/^[ \t]*#{1,6}(?= )/gm, "\\#").replace(/^[ \t]*([+-]|\d+\.)(?= )/gm, "\\$1");
}
function answerMarkdown(raw) {
  if (raw.length > 2e5) throw new ReadError("bad_answer");
  const blocks = [];
  const saved = raw.replace(/<pre\b[^>]*>([\s\S]*?)<\/pre>/gi, (_, code) => {
    const text2 = decodeEntities(code.replace(/<code\b[^>]*>/gi, "").replace(/<\/code>/gi, "").replace(/<[^>]+>/g, ""));
    const content = text2.replace(/^\n+|\n+$/g, "");
    const fence = "`".repeat(Math.max(3, ...Array.from(content.matchAll(/`+/g), (match) => match[0].length + 1)));
    blocks.push(fence + "\n" + content + "\n" + fence);
    return `

@@CODE${blocks.length - 1}@@

`;
  });
  let text = saved.replace(/<script\b[\s\S]*?<\/script>/gi, "").replace(/<style\b[\s\S]*?<\/style>/gi, "");
  const links = [];
  text = text.replace(/<a\b[^>]*href\s*=\s*(?:"([^"]*)"|'([^']*)')[^>]*>([\s\S]*?)<\/a>/gi, (_, double, single, label) => {
    const href = decodeEntities(double ?? single ?? "");
    const text2 = markdownText(decodeEntities(label.replace(/<[^>]+>/g, "")));
    if (!/^https:\/\/[^\s<>"'()]+$/.test(href)) return text2;
    links.push(`[${text2}](${href.replace(/\)/g, "%29")})`);
    return `${links.length - 1}`;
  });
  text = text.replace(/<img\b[^>]*alt\s*=\s*(?:"([^"]*)"|'([^']*)')[^>]*>/gi, "$1$2");
  text = text.replace(/<li\b[^>]*>/gi, "\n- ").replace(/<\/(?:p|div|li|ul|ol|blockquote|h[1-6]|table|tr)>/gi, "\n\n").replace(/<br\s*\/?>/gi, "\n");
  text = text.replace(/<h[1-6]\b[^>]*>/gi, "\n\n### ").replace(/<(?:strong|b)\b[^>]*>/gi, "**").replace(/<\/(?:strong|b)>/gi, "**").replace(/<(?:em|i)\b[^>]*>/gi, "*").replace(/<\/(?:em|i)>/gi, "*");
  text = text.replace(/<code\b[^>]*>/gi, "").replace(/<\/code>/gi, "");
  const inlineCode = [];
  text = text.replace(/\u0001([^\u0001\u0002]*)\u0002/g, (_, code) => {
    const content = decodeEntities(code).replace(/\\/g, "\\\\").replace(/`/g, "'");
    inlineCode.push("`" + content + "`");
    return `${inlineCode.length - 1}`;
  });
  text = decodeEntities(text.replace(/<[^>]*>/g, ""));
  text = text.split(/\r?\n/).map((line) => markdownText(line)).join("\n").replace(/[ \t]+\n/g, "\n").replace(/\n{3,}/g, "\n\n").trim();
  text = text.replace(/\u0003(\d+)\u0004/g, (_, index) => inlineCode[Number(index)] ?? "");
  text = text.replace(/\u0005(\d+)\u0006/g, (_, index) => links[Number(index)] ?? "");
  text = text.replace(/@@CODE(\d+)@@/g, (_, index) => blocks[Number(index)] ?? "");
  if (!text || text.length > 3e4) throw new ReadError("bad_answer");
  return text;
}
function parseAnswers(raw, id, site, at) {
  const { items, quotaRemaining } = wrapper(raw), { host } = sites[site], seen = /* @__PURE__ */ new Set();
  const answers = items.slice(0, 10).map((item) => {
    const answer = object(item), answerID = integer(answer.answer_id, 1, 9999999999), question = integer(answer.question_id, 1, 9999999999);
    if (question !== questionID(id) || seen.has(answerID)) throw new ReadError("bad_answer");
    seen.add(answerID);
    return { id: answerID, questionID: question, body: answerMarkdown(string(answer.body)), score: integer(answer.score, -1e6), accepted: boolean(answer.is_accepted), url: canonicalURL(string(answer.link), host, new RegExp(`^/questions/${question}(?:/|$)`)), license: license(answer.content_license), ...person(answer.owner, host) };
  });
  return { answers, at, ...quotaRemaining === void 0 ? {} : { quotaRemaining } };
}
function selectedAnswer(answers) {
  const selected = answers.find((answer) => answer.accepted) ?? answers[0];
  if (!selected) throw new ReadError("not_found");
  return selected;
}

// src/locales/en.json
var en_default = {
  title: "Stack Exchange answers",
  search: "Search questions",
  searchHint: "Error message, API name or question",
  searchAction: "Search",
  site: "Site",
  tag: "Tag (optional)",
  tagHint: "typescript",
  loading: "Looking up questions and answers",
  retry: "Try again",
  back: "Back to results",
  home: "New search",
  welcome: "Search Stack Overflow or another developer community. Open a result to read its accepted or highest-voted answer.",
  empty: "No matching questions. Try fewer words or a different tag.",
  answers: "{count} answers",
  score: "{count} votes",
  accepted: "Accepted answer",
  highest: "Highest-voted answer",
  openQuestion: "View question on {site}",
  openAnswer: "View answer on {site}",
  readAnswer: "Read answer",
  noAnswer: "No answer has been posted for this question. Open the original question to follow its progress.",
  by: "By {author} \xB7 {license}",
  quota: "{count} anonymous API requests remaining today",
  attribution: "Questions and answers belong to their authors and are provided under the displayed CC BY-SA license. Review community answers before using them. Stack Exchange API; no account or write access.",
  account: "Public Stack Exchange questions and answers",
  toolSearch: "Search Stack Overflow and selected Stack Exchange sites for questions, scores, tags and links",
  toolAnswer: "Read the accepted or highest-voted answer for a Stack Exchange question, including code and attribution",
  query: "Question, error or API",
  limit: "Maximum number of questions",
  id: "Question ID",
  unknown: "Not supplied",
  invalid: "Enter a search query, a supported site, a valid tag and a positive question ID.",
  bad_answer: "Stack Exchange returned an unexpected response.",
  not_found: "No answer was found for this question.",
  rate_limited: "Stack Exchange is rate-limiting requests. Try again later.",
  slow: "Stack Exchange took too long to respond.",
  network: "Stack Exchange could not be reached.",
  http: "Stack Exchange returned HTTP {0}."
};

// src/locales/zh-Hans.json
var zh_Hans_default = {
  title: "Stack Exchange \u95EE\u7B54",
  search: "\u641C\u7D22\u95EE\u9898",
  searchHint: "\u62A5\u9519\u3001API \u540D\u79F0\u6216\u95EE\u9898",
  searchAction: "\u641C\u7D22",
  site: "\u7AD9\u70B9",
  tag: "\u6807\u7B7E\uFF08\u53EF\u9009\uFF09",
  tagHint: "typescript",
  loading: "\u6B63\u5728\u67E5\u627E\u95EE\u9898\u548C\u7B54\u6848",
  retry: "\u91CD\u8BD5",
  back: "\u8FD4\u56DE\u7ED3\u679C",
  home: "\u65B0\u641C\u7D22",
  welcome: "\u641C\u7D22 Stack Overflow \u6216\u5176\u4ED6\u5F00\u53D1\u8005\u793E\u533A\uFF0C\u6253\u5F00\u95EE\u9898\u5373\u53EF\u9605\u8BFB\u91C7\u7EB3\u7B54\u6848\u6216\u6700\u9AD8\u7968\u7B54\u6848\u3002",
  empty: "\u6CA1\u6709\u627E\u5230\u76F8\u5173\u95EE\u9898\uFF0C\u8BD5\u8BD5\u51CF\u5C11\u5173\u952E\u8BCD\u6216\u66F4\u6362\u6807\u7B7E\u3002",
  answers: "{count} \u4E2A\u56DE\u7B54",
  score: "{count} \u7968",
  accepted: "\u91C7\u7EB3\u7B54\u6848",
  highest: "\u6700\u9AD8\u7968\u7B54\u6848",
  openQuestion: "\u5728 {site} \u67E5\u770B\u95EE\u9898",
  openAnswer: "\u5728 {site} \u67E5\u770B\u7B54\u6848",
  readAnswer: "\u9605\u8BFB\u7B54\u6848",
  noAnswer: "\u8FD9\u4E2A\u95EE\u9898\u8FD8\u6CA1\u6709\u7B54\u6848\uFF0C\u53EF\u6253\u5F00\u539F\u95EE\u9898\u5173\u6CE8\u540E\u7EED\u8FDB\u5C55\u3002",
  by: "\u4F5C\u8005\uFF1A{author} \xB7 {license}",
  quota: "\u4ECA\u65E5\u5269\u4F59 {count} \u6B21\u533F\u540D API \u8BF7\u6C42",
  attribution: "\u95EE\u9898\u548C\u7B54\u6848\u5F52\u5404\u81EA\u4F5C\u8005\u6240\u6709\uFF0C\u6309\u6240\u6807\u6CE8\u7684 CC BY-SA \u8BB8\u53EF\u8BC1\u63D0\u4F9B\u3002\u91C7\u7528\u793E\u533A\u7B54\u6848\u524D\u8BF7\u81EA\u884C\u6838\u5B9E\u3002\u4F7F\u7528 Stack Exchange API\uFF0C\u65E0\u9700\u8D26\u53F7\uFF0C\u4E0D\u8FDB\u884C\u5199\u64CD\u4F5C\u3002",
  account: "Stack Exchange \u516C\u5F00\u95EE\u7B54",
  toolSearch: "\u641C\u7D22 Stack Overflow \u548C\u6307\u5B9A\u7684 Stack Exchange \u7AD9\u70B9\uFF0C\u8FD4\u56DE\u95EE\u9898\u3001\u7968\u6570\u3001\u6807\u7B7E\u548C\u94FE\u63A5",
  toolAnswer: "\u9605\u8BFB Stack Exchange \u95EE\u9898\u7684\u91C7\u7EB3\u7B54\u6848\u6216\u6700\u9AD8\u7968\u7B54\u6848\uFF0C\u5305\u542B\u4EE3\u7801\u548C\u7F72\u540D\u4FE1\u606F",
  query: "\u95EE\u9898\u3001\u62A5\u9519\u6216 API",
  limit: "\u6700\u591A\u8FD4\u56DE\u7684\u95EE\u9898\u6570",
  id: "\u95EE\u9898 ID",
  unknown: "\u672A\u63D0\u4F9B",
  invalid: "\u8BF7\u8F93\u5165\u641C\u7D22\u5185\u5BB9\uFF0C\u5E76\u9009\u62E9\u652F\u6301\u7684\u7AD9\u70B9\u3001\u6709\u6548\u6807\u7B7E\u548C\u6B63\u6574\u6570\u95EE\u9898 ID\u3002",
  bad_answer: "Stack Exchange \u8FD4\u56DE\u4E86\u65E0\u6CD5\u8BC6\u522B\u7684\u54CD\u5E94\u3002",
  not_found: "\u8FD9\u4E2A\u95EE\u9898\u8FD8\u6CA1\u6709\u7B54\u6848\u3002",
  rate_limited: "Stack Exchange \u6B63\u5728\u9650\u5236\u8BF7\u6C42\u9891\u7387\uFF0C\u8BF7\u7A0D\u540E\u91CD\u8BD5\u3002",
  slow: "Stack Exchange \u54CD\u5E94\u8D85\u65F6\u3002",
  network: "\u65E0\u6CD5\u8FDE\u63A5 Stack Exchange\u3002",
  http: "Stack Exchange \u8FD4\u56DE HTTP {0}\u3002"
};

// src/locales/ja.json
var ja_default = {
  title: "Stack Exchange \u56DE\u7B54",
  search: "\u8CEA\u554F\u3092\u691C\u7D22",
  searchHint: "\u30A8\u30E9\u30FC\u3001API \u540D\u3001\u8CEA\u554F",
  searchAction: "\u691C\u7D22",
  site: "\u30B5\u30A4\u30C8",
  tag: "\u30BF\u30B0\uFF08\u4EFB\u610F\uFF09",
  tagHint: "typescript",
  loading: "\u8CEA\u554F\u3068\u56DE\u7B54\u3092\u691C\u7D22\u3057\u3066\u3044\u307E\u3059",
  retry: "\u518D\u8A66\u884C",
  back: "\u7D50\u679C\u306B\u623B\u308B",
  home: "\u65B0\u3057\u3044\u691C\u7D22",
  welcome: "Stack Overflow \u306A\u3069\u306E\u958B\u767A\u8005\u30B3\u30DF\u30E5\u30CB\u30C6\u30A3\u3092\u691C\u7D22\u3057\u3001\u63A1\u7528\u56DE\u7B54\u307E\u305F\u306F\u6700\u591A\u7968\u306E\u56DE\u7B54\u3092\u8AAD\u3081\u307E\u3059\u3002",
  empty: "\u4E00\u81F4\u3059\u308B\u8CEA\u554F\u304C\u3042\u308A\u307E\u305B\u3093\u3002\u30AD\u30FC\u30EF\u30FC\u30C9\u3092\u6E1B\u3089\u3059\u304B\u3001\u30BF\u30B0\u3092\u5909\u66F4\u3057\u3066\u304F\u3060\u3055\u3044\u3002",
  answers: "\u56DE\u7B54 {count} \u4EF6",
  score: "{count} \u7968",
  accepted: "\u63A1\u7528\u56DE\u7B54",
  highest: "\u6700\u591A\u7968\u306E\u56DE\u7B54",
  openQuestion: "{site} \u3067\u8CEA\u554F\u3092\u958B\u304F",
  openAnswer: "{site} \u3067\u56DE\u7B54\u3092\u958B\u304F",
  readAnswer: "\u56DE\u7B54\u3092\u8AAD\u3080",
  noAnswer: "\u307E\u3060\u56DE\u7B54\u304C\u3042\u308A\u307E\u305B\u3093\u3002\u5143\u306E\u8CEA\u554F\u3092\u958B\u3044\u3066\u7D9A\u304D\u3092\u78BA\u8A8D\u3067\u304D\u307E\u3059\u3002",
  by: "\u6295\u7A3F\u8005: {author} \xB7 {license}",
  quota: "\u672C\u65E5\u306E\u533F\u540D API \u30EA\u30AF\u30A8\u30B9\u30C8\u6B8B\u308A {count} \u4EF6",
  attribution: "\u8CEA\u554F\u3068\u56DE\u7B54\u306F\u5404\u6295\u7A3F\u8005\u306B\u5E30\u5C5E\u3057\u3001\u8868\u793A\u3055\u308C\u305F CC BY-SA \u30E9\u30A4\u30BB\u30F3\u30B9\u3067\u63D0\u4F9B\u3055\u308C\u307E\u3059\u3002\u30B3\u30DF\u30E5\u30CB\u30C6\u30A3\u306E\u56DE\u7B54\u306F\u5229\u7528\u524D\u306B\u78BA\u8A8D\u3057\u3066\u304F\u3060\u3055\u3044\u3002Stack Exchange API \u3092\u4F7F\u7528\u3057\u3001\u30A2\u30AB\u30A6\u30F3\u30C8\u3084\u66F8\u304D\u8FBC\u307F\u6A29\u9650\u306F\u4E0D\u8981\u3067\u3059\u3002",
  account: "Stack Exchange \u306E\u516C\u958B\u8CEA\u554F\u3068\u56DE\u7B54",
  toolSearch: "Stack Overflow \u3068\u9078\u629E\u53EF\u80FD\u306A Stack Exchange \u30B5\u30A4\u30C8\u3067\u8CEA\u554F\u3001\u7968\u6570\u3001\u30BF\u30B0\u3001\u30EA\u30F3\u30AF\u3092\u691C\u7D22",
  toolAnswer: "Stack Exchange \u306E\u8CEA\u554F\u306E\u63A1\u7528\u56DE\u7B54\u307E\u305F\u306F\u6700\u591A\u7968\u306E\u56DE\u7B54\u3092\u3001\u30B3\u30FC\u30C9\u3068\u5E30\u5C5E\u60C5\u5831\u4ED8\u304D\u3067\u8AAD\u3080",
  query: "\u8CEA\u554F\u3001\u30A8\u30E9\u30FC\u3001API",
  limit: "\u8CEA\u554F\u306E\u6700\u5927\u4EF6\u6570",
  id: "\u8CEA\u554F ID",
  unknown: "\u672A\u63D0\u4F9B",
  invalid: "\u691C\u7D22\u8A9E\u3001\u5BFE\u5FDC\u30B5\u30A4\u30C8\u3001\u6709\u52B9\u306A\u30BF\u30B0\u3001\u6B63\u306E\u8CEA\u554F ID \u3092\u5165\u529B\u3057\u3066\u304F\u3060\u3055\u3044\u3002",
  bad_answer: "Stack Exchange \u304C\u4E88\u671F\u3057\u306A\u3044\u5FDC\u7B54\u3092\u8FD4\u3057\u307E\u3057\u305F\u3002",
  not_found: "\u3053\u306E\u8CEA\u554F\u306B\u306F\u56DE\u7B54\u304C\u3042\u308A\u307E\u305B\u3093\u3002",
  rate_limited: "Stack Exchange \u304C\u30EA\u30AF\u30A8\u30B9\u30C8\u3092\u5236\u9650\u3057\u3066\u3044\u307E\u3059\u3002\u3057\u3070\u3089\u304F\u3057\u3066\u304B\u3089\u518D\u8A66\u884C\u3057\u3066\u304F\u3060\u3055\u3044\u3002",
  slow: "Stack Exchange \u306E\u5FDC\u7B54\u304C\u30BF\u30A4\u30E0\u30A2\u30A6\u30C8\u3057\u307E\u3057\u305F\u3002",
  network: "Stack Exchange \u306B\u63A5\u7D9A\u3067\u304D\u307E\u305B\u3093\u3002",
  http: "Stack Exchange \u304C HTTP {0} \u3092\u8FD4\u3057\u307E\u3057\u305F\u3002"
};

// src/i18n.ts
var textsFor = (locale = "") => locale.toLowerCase().startsWith("ja") ? ja_default : locale.toLowerCase().startsWith("zh") ? zh_Hans_default : en_default;
var fmt = (text, values) => text.replace(/\{(\w+)\}/g, (_, key) => String(values[key] ?? ""));
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
var StackExchange = class {
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
  backoff = /* @__PURE__ */ new Map();
  queue = Promise.resolve();
  lastOK;
  lastError;
  cached(path, fresh) {
    const item = this.cache.get(path);
    if (item && this.monotonic() - item.fetched < Math.max(6e4, fresh)) {
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
    const atMonotonic = this.monotonic();
    this.cache.set(path, { raw, at, fetched: atMonotonic, used: atMonotonic, bytes });
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
      response = await this.fetch(`https://api.stackexchange.com${path}`, {
        method: "GET",
        headers: { Accept: "application/json", "Accept-Encoding": "gzip", "User-Agent": "persona-stackexchange/0.1.0" },
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
    let raw;
    try {
      raw = JSON.parse(text);
    } catch {
      throw new ReadError("bad_answer");
    }
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("bad_answer");
    const root2 = raw;
    if (root2.error_id != null || root2.error_name != null || root2.error_message != null) {
      throw new ReadError(root2.error_id === 502 || root2.error_id === 429 ? "rate_limited" : "bad_answer");
    }
    const backoff = root2.backoff == null ? 0 : root2.backoff;
    if (typeof backoff !== "number" || !Number.isSafeInteger(backoff) || backoff < 0 || backoff > 3600) throw new ReadError("bad_answer");
    if (backoff > 0) this.backoff.set(path.split("?")[0], this.monotonic() + backoff * 1e3);
    return raw;
  }
  async read(path, fresh, signal, validate) {
    checkSignal(signal);
    const cached = this.cached(path, fresh);
    if (cached) return cached;
    const run = this.queue.then(async () => {
      const again = this.cached(path, fresh);
      if (again) return again;
      const method = path.split("?")[0];
      const pause = Math.max(this.limits.gap - (this.monotonic() - this.lastRequest), (this.backoff.get(method) ?? 0) - this.monotonic(), 0);
      if (pause > 3e4) throw new ReadError("rate_limited");
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
  async search(query, site = "stackoverflow", tag = "", limit = 12, refresh = false, signal) {
    const rd = await this.read(searchPath(query, siteID(site), tagText(tag), limit), refresh ? 0 : this.limits.fresh, signal, (raw, at) => {
      parseQuestions(raw, site, at);
    });
    return { ...parseQuestions(rd.raw, site, rd.at), stale: rd.stale };
  }
  async answers(id, site = "stackoverflow", refresh = false, signal) {
    const rd = await this.read(answersPath(questionID(id), siteID(site)), refresh ? 0 : this.limits.fresh, signal, (raw, at) => {
      parseAnswers(raw, id, site, at);
    });
    return { ...parseAnswers(rd.raw, id, site, rd.at), stale: rd.stale };
  }
  cacheStats() {
    return { entries: this.cache.size, bytes: this.bytes };
  }
};

// src/tools.ts
function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [
    { name: "stackexchange_search", description: t.toolSearch, input_schema: { type: "object", required: ["query"], properties: { query: { type: "string", description: t.query }, site: { type: "string", enum: Object.keys(sites), description: t.site }, tag: { type: "string", description: t.tag }, limit: { type: "integer", minimum: 1, maximum: 20, description: t.limit } } } },
    { name: "stackexchange_answer", description: t.toolAnswer, input_schema: { type: "object", required: ["question_id"], properties: { question_id: { type: "integer", minimum: 1, description: t.id }, site: { type: "string", enum: Object.keys(sites), description: t.site } } } }
  ];
}
var tools = toolsFor();
function questionLine(question, index) {
  return `${index + 1}. ${clip(question.title, 500)} (${question.id})
   ${question.score} votes \xB7 ${question.answerCount} answers \xB7 ${question.tags.map((tag) => "#" + tag).join(" ")}
   ${question.url}`;
}
function answerText(answer) {
  return `${answer.accepted ? "Accepted" : "Highest-voted"} answer ${answer.id} \xB7 ${answer.score} votes
By ${answer.author} \xB7 ${answer.license}
${answer.url}

${answer.body}`;
}
function argsFrom(raw, exact) {
  if (exact) {
    const args = {};
    for (const field of ["query", "site", "tag"]) for (const token of exact.fieldValues(field)) {
      const value2 = JSON.parse(token.raw);
      if (typeof value2 !== "string") throw new ReadError("invalid");
      args[field] = value2;
    }
    const limit = exact.int64Fields(["limit", "question_id"]);
    if (limit.limit !== 0n) args.limit = Number(limit.limit);
    if (limit.question_id !== 0n) args.question_id = Number(limit.question_id);
    return args;
  }
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = raw;
  for (const field of ["query", "site", "tag"]) if (value[field] != null && typeof value[field] !== "string") throw new ReadError("invalid");
  for (const field of ["limit", "question_id"]) if (value[field] != null && (typeof value[field] !== "number" || !Number.isSafeInteger(value[field]))) throw new ReadError("invalid");
  return value;
}
async function callTool(service, name, raw = {}, signal, exact) {
  const args = argsFrom(raw, exact), t = textsFor(service.locale), request = budget(signal, service.limits.tool);
  try {
    const site = siteID(args.site);
    if (name === "stackexchange_search") {
      const query = queryText(args.query), limit = args.limit == null ? 10 : Number(args.limit), tag = tagText(args.tag);
      if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
      const result = await service.search(query, site, tag, limit, false, request.signal);
      return clip((result.questions.length ? result.questions.map(questionLine).join("\n") : t.empty) + `
${t.attribution}${result.stale ? `
${errorText(result.stale, service.locale)}` : ""}`, 29999);
    }
    if (name === "stackexchange_answer") {
      const id = questionID(args.question_id), result = await service.answers(id, site, false, request.signal);
      return clip(answerText(selectedAnswer(result.answers)) + `

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
    if (p.contribution && p.contribution !== "stackexchange") throw noContribution(String(p.contribution));
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
var initialState = { q: "", site: "stackoverflow", tag: "", found: false };
function questionState(value, site) {
  if (value == null) return void 0;
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("invalid");
  const raw = value, id = questionID(raw.id);
  const candidate = { id, title: queryText(raw.title), url: "", score: 0, answerCount: 0, tags: [], license: "CC BY-SA 4.0", author: "" };
  const url = new URL(candidate.url = typeof raw.url === "string" ? raw.url : "");
  if (url.protocol !== "https:" || url.hostname !== sites[site].host || url.username || url.password || url.port || url.pathname !== `/questions/${id}` && !url.pathname.startsWith(`/questions/${id}/`)) throw new ReadError("invalid");
  candidate.url = url.href;
  if (!Number.isSafeInteger(raw.score) || !Number.isSafeInteger(raw.answerCount) || raw.answerCount < 0 || raw.score < -1e6 || raw.score > 1e6 || raw.answerCount > 1e6) throw new ReadError("invalid");
  candidate.score = raw.score;
  candidate.answerCount = raw.answerCount;
  candidate.tags = Array.isArray(raw.tags) ? raw.tags.slice(0, 10).map(tagText) : [];
  if (new Set(candidate.tags).size !== candidate.tags.length) throw new ReadError("invalid");
  if (typeof raw.license !== "string" || !/^CC BY-SA (?:2\.5|3\.0|4\.0)$/.test(raw.license)) throw new ReadError("invalid");
  candidate.license = raw.license;
  candidate.author = clip(oneLine(typeof raw.author === "string" ? raw.author : ""), 200);
  if (!candidate.author) throw new ReadError("invalid");
  if (raw.authorURL != null) {
    if (typeof raw.authorURL !== "string") throw new ReadError("invalid");
    const link = new URL(raw.authorURL);
    if (link.protocol !== "https:" || link.hostname !== sites[site].host || !/^\/users\/[1-9][0-9]*(?:\/|$)/.test(link.pathname) || link.username || link.password || link.port) throw new ReadError("invalid");
    candidate.authorURL = link.href;
  }
  if (raw.acceptedAnswerID != null) candidate.acceptedAnswerID = questionID(raw.acceptedAnswerID);
  return candidate;
}
function readState(raw) {
  if (typeof raw !== "string" || raw.length > 16e3) return initialState;
  try {
    const value = JSON.parse(raw);
    if (!value || Array.isArray(value) || typeof value.found !== "boolean") return initialState;
    return { q: queryText(value.q), site: siteID(value.site), tag: tagText(value.tag), found: value.found, question: questionState(value.question, siteID(value.site)) };
  } catch {
    return initialState;
  }
}
var encodeState = (state) => JSON.stringify(state);
function questionTransition(state, results, id) {
  if (!id.startsWith("question:")) return state;
  let idNumber;
  try {
    idNumber = questionID(id.slice("question:".length));
  } catch {
    return state;
  }
  const question = results.questions.find((candidate) => candidate.id === idNumber);
  return question ? { q: state.q, site: state.site, tag: state.tag, found: true, question } : state;
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
      const next = { q: queryText(fields2.query), site: siteID(fields2.site ?? state.site), tag: tagText(fields2.tag ?? ""), found: true };
      return next.q === state.q && next.site === state.site && next.tag === state.tag && state.found ? { ...next, question: state.question } : next;
    } catch {
      return state;
    }
  }
  if (id === "home") return initialState;
  if (id === "back" && state.found) return { q: state.q, site: state.site, tag: state.tag, found: true };
  return state;
}
async function loadPage(service, state, refresh, signal) {
  const request = budget(signal, service.limits.page);
  try {
    if (!state.found) return { kind: "empty" };
    if (!state.question) return { kind: "results", result: await service.search(state.q, state.site, state.tag, 12, refresh, request.signal) };
    if (state.question.answerCount === 0) return { kind: "no-answer" };
    const result = await service.answers(state.question.id, state.site, refresh, request.signal), answer = selectedAnswer(result.answers);
    return { kind: "answer", question: state.question, body: answer.body, accepted: answer.accepted, id: answer.id, url: answer.url, author: answer.author, license: answer.license, stale: result.stale, quotaRemaining: result.quotaRemaining };
  } catch (error) {
    checkSignal(signal);
    return { kind: "error", error };
  } finally {
    request.dispose();
  }
}
function page(props) {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale), siteName = sites[state.site].name;
  const act = (id, next, title) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "answers", state: encodeState(next), ...title ? { title } : {} } } : { id });
  const text = (key, value, style = "secondary") => /* @__PURE__ */ jsx(Text, { text: clip(value || t.unknown, 2800), style }, key);
  const search = /* @__PURE__ */ jsxs(Section, { title: t.search, children: [
    /* @__PURE__ */ jsx(Field, { name: "query", label: t.search, kind: "text", placeholder: t.searchHint, value: state.q }, "stackexchange-query"),
    /* @__PURE__ */ jsx(Field, { name: "site", label: t.site, kind: "choice", value: state.site, choices: siteIDs.map((id) => ({ value: id, title: sites[id].name })) }, "stackexchange-site"),
    /* @__PURE__ */ jsx(Field, { name: "tag", label: t.tag, kind: "text", placeholder: t.tagHint, value: state.tag }, "stackexchange-tag"),
    /* @__PURE__ */ jsx(Button, { title: t.searchAction, action: act("search") }, "stackexchange-submit")
  ] }, "stackexchange-search");
  const attribution = /* @__PURE__ */ jsx(Section, { children: text("stackexchange-license", t.attribution, "caption") }, "stackexchange-attribution");
  if (loaded.kind === "loading") return /* @__PURE__ */ jsxs(View, { title: t.search, state: encodeState(state), children: [
    search,
    /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(Loading, { title: t.loading }, "stackexchange-loading-indicator") }, "stackexchange-loading"),
    attribution
  ] }, "stackexchange");
  if (loaded.kind === "error") return /* @__PURE__ */ jsxs(View, { title: t.search, state: encodeState(state), children: [
    search,
    /* @__PURE__ */ jsxs(Section, { children: [
      text("stackexchange-error-message", errorText(loaded.error, props.locale ?? service.locale)),
      /* @__PURE__ */ jsx(Button, { title: t.retry, action: act("refresh") }, "stackexchange-retry")
    ] }, "stackexchange-error"),
    attribution
  ] }, "stackexchange");
  if (loaded.kind === "empty") return /* @__PURE__ */ jsxs(View, { title: t.search, state: encodeState(state), children: [
    search,
    /* @__PURE__ */ jsx(Section, { children: text("stackexchange-welcome-text", t.welcome, "body") }, "stackexchange-welcome"),
    attribution
  ] }, "stackexchange");
  const footer = /* @__PURE__ */ jsxs(Section, { children: [
    /* @__PURE__ */ jsx(Button, { title: t.retry, action: act("refresh") }, "stackexchange-refresh"),
    state.question ? /* @__PURE__ */ jsx(Button, { title: t.back, action: act("back") }, "stackexchange-back") : null,
    /* @__PURE__ */ jsx(Button, { title: t.home, action: act("home") }, "stackexchange-home")
  ] }, "stackexchange-footer");
  if (loaded.kind === "no-answer") return /* @__PURE__ */ jsxs(View, { title: state.question?.title ?? t.title, state: encodeState(state), children: [
    search,
    /* @__PURE__ */ jsxs(Section, { children: [
      text("stackexchange-no-answer-text", t.noAnswer, "body"),
      state.question ? /* @__PURE__ */ jsx(Link, { title: fmt(t.openQuestion, { site: siteName }), url: state.question.url }, "stackexchange-question-link") : null
    ] }, "stackexchange-no-answer"),
    footer,
    attribution
  ] }, "stackexchange");
  if (loaded.kind === "answer") return /* @__PURE__ */ jsxs(View, { title: loaded.question.title, state: encodeState(state), children: [
    /* @__PURE__ */ jsxs(Section, { title: loaded.accepted ? t.accepted : t.highest, footer: [fmt(t.score, { count: loaded.question.score }), fmt(t.answers, { count: loaded.question.answerCount })].join(" \xB7 "), children: [
      /* @__PURE__ */ jsx(Markdown, { text: loaded.body }, "stackexchange-answer-body"),
      text("stackexchange-answer-author", fmt(t.by, { author: loaded.author, license: loaded.license }), "caption"),
      /* @__PURE__ */ jsx(Link, { title: fmt(t.openAnswer, { site: siteName }), url: loaded.url }, "stackexchange-answer-link")
    ] }, "stackexchange-answer-heading"),
    /* @__PURE__ */ jsxs(Section, { children: [
      text("stackexchange-question-title", loaded.question.title, "body"),
      /* @__PURE__ */ jsx(Link, { title: fmt(t.openQuestion, { site: siteName }), url: loaded.question.url }, "stackexchange-question-link"),
      text("stackexchange-question-tags", loaded.question.tags.map((tag) => "#" + tag).join(" \xB7 "), "caption")
    ] }, "stackexchange-question"),
    loaded.stale ? /* @__PURE__ */ jsx(Section, { children: text("stackexchange-stale-text", errorText(loaded.stale, props.locale ?? service.locale)) }, "stackexchange-stale") : null,
    loaded.quotaRemaining !== void 0 ? /* @__PURE__ */ jsx(Section, { children: text("stackexchange-quota-text", fmt(t.quota, { count: loaded.quotaRemaining }), "caption") }, "stackexchange-quota") : null,
    footer,
    attribution
  ] }, "stackexchange");
  return /* @__PURE__ */ jsxs(View, { title: `${t.title}: ${clip(state.q, 100)}`, state: encodeState(state), children: [
    search,
    loaded.result.questions.length ? loaded.result.questions.map((question) => /* @__PURE__ */ jsxs(Section, { title: question.title, footer: [fmt(t.score, { count: question.score }), fmt(t.answers, { count: question.answerCount }), question.tags.map((tag) => "#" + tag).join(" \xB7 ")].filter(Boolean).join(" \xB7 "), children: [
      /* @__PURE__ */ jsx(Button, { title: t.readAnswer, action: act(`question:${question.id}`, { ...state, question }, question.title) }, `open-${question.id}`),
      /* @__PURE__ */ jsx(Link, { title: fmt(t.openQuestion, { site: siteName }), url: question.url }, `link-${question.id}`)
    ] }, `question-${question.id}`)) : /* @__PURE__ */ jsx(Section, { children: text("stackexchange-empty", t.empty) }, "stackexchange-empty-results"),
    loaded.result.stale ? /* @__PURE__ */ jsx(Section, { children: text("stackexchange-stale-text", errorText(loaded.result.stale, props.locale ?? service.locale)) }, "stackexchange-stale") : null,
    loaded.result.quotaRemaining !== void 0 ? /* @__PURE__ */ jsx(Section, { children: text("stackexchange-quota-text", fmt(t.quota, { count: loaded.result.quotaRemaining }), "caption") }, "stackexchange-quota") : null,
    footer,
    attribution
  ] }, "stackexchange");
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
        const push = { ui: "answers", state: encodeState(next), ...title ? { title } : {} };
        return callback(() => ({ push }), { push });
      }
      return callback((values) => {
        if (id === "refresh") setRefresh((previous) => previous + 1);
        else if (id.startsWith("question:")) {
          if (loaded.kind !== "results") return;
          setState((previous) => questionTransition(previous, loaded.result, id));
        } else setState((previous) => transition(previous, id, values));
      }, { submit: true });
    }
  });
}
function root(options) {
  return /* @__PURE__ */ jsx(Root, { ...options }, "stackexchange-root");
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
var sourceManifest = { ...persona_extension_default, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/stackexchange.mjs", sha256: "" } } };
delete sourceManifest.icon;
delete sourceManifest.replaces;
function createProgram(fetch = (input, init) => globalThis.fetch(input, init), options = {}) {
  let service;
  const current = () => {
    if (!service) throw new Error("StackExchange has not been initialized");
    return service;
  };
  return {
    id: persona_extension_default.id,
    version: persona_extension_default.version,
    manifest: sourceManifest,
    init: (init, _hub) => {
      service = new StackExchange(fetch, init.config, { ...options, locale: init.hub?.locale ?? options.locale });
    },
    namespaces: { connector: connector(current) },
    ui: { answers: componentPage((req) => root({ service: current(), state: req.state, locale: req.locale, uiVersion: req.ui_version })) }
  };
}
var index_default = createProgram();
export {
  ReadError,
  Root,
  StackExchange,
  budget,
  callTool,
  connector,
  createProgram,
  index_default as default,
  encodeState,
  limits,
  loadPage,
  page,
  questionTransition,
  readState,
  root,
  snapshotPage,
  sourceManifest,
  tools,
  transition
};
