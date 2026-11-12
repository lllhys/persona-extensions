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
var persona_extension_default = { manifest_version: 1, id: "wikipedia", name: "Wikipedia \u6BCF\u65E5\u9605\u8BFB", version: "0.1.3", icon: "assets/icon.png", description: "\u5728 Persona \u4E2D\u8BFB Wikipedia \u4ECA\u65E5\u7CBE\u9009\u3001\u70ED\u95E8\u6761\u76EE\u548C\u5386\u53F2\u4E0A\u7684\u4ECA\u5929\uFF0C\u641C\u7D22\u5E76\u9605\u8BFB\u6761\u76EE\u6458\u8981\u3002\u5185\u5BB9\u7531 hub \u901A\u8FC7\u516C\u5F00 API \u83B7\u53D6\uFF0C\u65E0\u9700\u8D26\u53F7\u3002", publisher: "nwverse", homepage: "https://github.com/nwverse/persona-extensions/blob/main/extensions/wikipedia/README.md", tags: ["Wikipedia", "\u9605\u8BFB", "\u77E5\u8BC6", "\u6BCF\u65E5\u7CBE\u9009"], engines: { plugin_protocol: 1 }, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/wikipedia.mjs", sha256: "" } }, contributes: { connectors: [{ id: "wikipedia", name: "Wikipedia", capabilities: ["status", "tools"], hosts: ["en.wikipedia.org", "zh.wikipedia.org", "ja.wikipedia.org"], agent_tools: ["wikipedia_*"] }], ui: [{ id: "today", title: "\u6BCF\u65E5\u9605\u8BFB", placement: "nav_page", icon: "doc" }] }, configuration: { type: "object", properties: { language: { type: "string", title: "Wikipedia \u8BED\u8A00", default: "auto", description: "auto \u8DDF\u968F Persona \u754C\u9762\u8BED\u8A00\uFF1B\u82F1\u6587\u9009 en\uFF0C\u4E2D\u6587\u9009 zh\uFF0C\u65E5\u6587\u9009 ja", oneOf: [{ const: "auto", title: "\u8DDF\u968F\u754C\u9762\u8BED\u8A00" }, { const: "en", title: "English" }, { const: "zh", title: "\u4E2D\u6587" }, { const: "ja", title: "\u65E5\u672C\u8A9E" }] } } }, permissions: { network: ["en.wikipedia.org", "zh.wikipedia.org", "ja.wikipedia.org"] } };

// src/model.ts
var limits = Object.freeze({ request: 2e4, page: 25e3, tool: 25e3, response: 4 << 20, feedFresh: 15 * 6e4, articleFresh: 60 * 6e4, cache: 60, cacheBytes: 4 << 20, gap: 100 });
var ReadError = class extends Error {
  constructor(code, status) {
    super(code);
    this.code = code;
    this.status = status;
  }
};
function configFrom(raw) {
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = raw.language;
  if (value == null || value === "") return {};
  if (value !== "auto" && value !== "en" && value !== "zh" && value !== "ja") throw new ReadError("invalid");
  return { language: value };
}
function languageFor(config, locale = "") {
  if (config.language && config.language !== "auto") return config.language;
  return locale.toLowerCase().startsWith("ja") ? "ja" : locale.toLowerCase().startsWith("zh") ? "zh" : "en";
}
var clip = (text, most) => Array.from(text).length <= most ? text : Array.from(text).slice(0, most).join("").trimEnd() + "\u2026";
var oneLine = (text) => text.replace(/\s+/gu, " ").trim();
function plain(text) {
  let out = "";
  for (let i = 0; i < text.length; ) {
    if (text[i] === "<") {
      const end = text.indexOf(">", i + 1);
      if (end < 0) {
        out += " ";
        break;
      }
      i = end + 1;
      continue;
    }
    if (text[i] === "&") {
      const match = /^&(amp|lt|gt|quot|apos|#39|nbsp|#x[0-9a-fA-F]{1,6}|#[0-9]{1,7});/.exec(text.slice(i));
      if (match) {
        const name = match[1], cp = name.startsWith("#x") ? Number.parseInt(name.slice(2), 16) : name.startsWith("#") ? Number(name.slice(1)) : 0;
        out += name === "amp" ? "&" : name === "lt" ? "<" : name === "gt" ? ">" : name === "quot" ? '"' : name === "apos" || name === "#39" ? "'" : name === "nbsp" ? " " : validCodepoint(cp) ? String.fromCodePoint(cp) : "\uFFFD";
        i += match[0].length;
        continue;
      }
    }
    out += text[i++];
  }
  return oneLine(out);
}
function validCodepoint(n) {
  return n >= 32 && n <= 1114111 && !(n >= 55296 && n <= 57343) && n !== 65534 && n !== 65535;
}
function titleKey(value) {
  if (typeof value !== "string") throw new ReadError("invalid");
  const title = plain(value).trim();
  if (/[<>]|&(#[0-9]+|#x[0-9a-fA-F]+|[A-Za-z][A-Za-z0-9]*);/.test(value)) throw new ReadError("invalid");
  if (!title || title.length > 255 || /[\u0000-\u001f\u007f#<>[\]{}|]/.test(title) || Array.from(title).length > 255) throw new ReadError("invalid");
  return title.replaceAll(" ", "_");
}
function articleURL(language, title) {
  return `https://${language}.wikipedia.org/wiki/${encodeURIComponent(titleKey(title)).replaceAll("%2F", "/")}`;
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
function integer(value) {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) throw new ReadError("bad_answer");
  return value;
}
function parseArticle(raw, language) {
  const value = object(raw), canonical = titleKey(value.titles && typeof value.titles === "object" && !Array.isArray(value.titles) ? value.titles.canonical : value.title);
  const title = plain(string(value.normalizedtitle || value.title));
  return {
    title: canonical,
    heading: clip(title || canonical.replaceAll("_", " "), 300),
    description: clip(plain(string(value.description)), 500),
    extract: clip(plain(string(value.extract)), 12e3),
    url: articleURL(language, canonical),
    ...value.views !== void 0 ? { views: integer(value.views) } : {}
  };
}
function validDate(value) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!m) return false;
  const [year, month, day] = m.slice(1).map(Number);
  if (year < 2016 || year > 2200 || month < 1 || month > 12 || day < 1 || day > 31) return false;
  const parsed = /* @__PURE__ */ new Date(`${value}T00:00:00Z`);
  return parsed.getUTCFullYear() === year && parsed.getUTCMonth() + 1 === month && parsed.getUTCDate() === day;
}
function dateAt(now) {
  if (!Number.isFinite(now) || now < 0 || now > Date.UTC(2200, 0, 1)) throw new ReadError("invalid");
  return new Date(now).toISOString().slice(0, 10);
}
function feedPath(date) {
  if (!validDate(date)) throw new ReadError("invalid");
  return `/api/rest_v1/feed/featured/${date.replaceAll("-", "/")}`;
}
function summaryPath(title) {
  return `/api/rest_v1/page/summary/${encodeURIComponent(titleKey(title))}`;
}
function searchPath(query, limit = 30) {
  if (typeof query !== "string" || !query.trim() || Array.from(query).length > 200 || /[\u0000-\u001f\u007f]/.test(query)) throw new ReadError("invalid");
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 30) throw new ReadError("invalid");
  return `/w/rest.php/v1/search/page?${new URLSearchParams({ q: query.trim(), limit: String(limit) })}`;
}
function parseFeed(raw, language, date, at) {
  const value = object(raw);
  if (value.tfa == null && value.mostread === void 0 && value.onthisday === void 0) throw new ReadError("bad_answer");
  const popular = value.mostread === void 0 ? [] : array(object(value.mostread).articles).slice(0, 30).map((article) => parseArticle(article, language));
  const events = value.onthisday === void 0 ? [] : array(value.onthisday).slice(0, 30).map((rawEvent) => {
    const event = object(rawEvent);
    const year = integer(event.year);
    if (year > 2200) throw new ReadError("bad_answer");
    const text = clip(plain(string(event.text)), 1500);
    if (!text) throw new ReadError("bad_answer");
    const pages = array(event.pages).slice(0, 5).map((page2) => parseArticle(page2, language));
    return { year, text, pages };
  });
  const seen = /* @__PURE__ */ new Set();
  for (const article of popular) {
    if (seen.has(article.title)) throw new ReadError("bad_answer");
    seen.add(article.title);
  }
  return { date, ...value.tfa != null ? { featured: parseArticle(value.tfa, language) } : {}, popular, events, at };
}
function parseSearch(raw, language) {
  const seen = /* @__PURE__ */ new Set();
  return array(object(raw).pages).slice(0, 30).map((page2) => {
    const value = object(page2), result = parseArticle({ title: value.key, normalizedtitle: value.title, description: value.description, extract: value.excerpt }, language);
    if (seen.has(result.title)) throw new ReadError("bad_answer");
    seen.add(result.title);
    return result;
  });
}

// src/locales/en.json
var en_default = { featuredShort: "Featured", popularShort: "Popular", historyShort: "Today", today: "Wikipedia", featured: "Featured today", popular: "Most read", history: "On this day", search: "Search articles", searchHint: "Title or topic", searchAction: "Search", results: "Results for \u201C{query}\u201D", empty: "No articles found", noFeatured: "Today's featured article is unavailable in this language.", views: "{views} views", read: "Read summary", open: "Open on Wikipedia", back: "Back to results", backToday: "Back to daily reading", refresh: "Refresh", retry: "Retry", loading: "Loading articles", readAt: "Public Wikipedia content \xB7 read {at}", summaryHint: "A short introduction. Open the full article on Wikipedia for the complete text and references.", attribution: "Text from Wikipedia, available under CC BY-SA; images retain their individual licenses.", account: "Public Wikipedia; no account required", toolToday: "Read today's featured and most-read Wikipedia articles and on-this-day events.", toolSearch: "Search public Wikipedia articles by title or topic.", toolArticle: "Read a Wikipedia article's short introduction and canonical link. Use search first if the exact title is unknown.", query: "Search query", article: "Exact article title or canonical key", limit: "Maximum results, from 1 to 30", invalid: "Enter a valid article title or search query.", bad_answer: "Wikipedia returned an invalid response.", not_found: "That article was not found.", rate_limited: "Wikipedia is receiving too many requests; try again later.", slow: "Wikipedia took too long to respond.", network: "Wikipedia could not be reached.", http: "Wikipedia returned HTTP {0}." };

// src/locales/zh-Hans.json
var zh_Hans_default = { featuredShort: "\u7CBE\u9009", popularShort: "\u70ED\u95E8", historyShort: "\u4ECA\u65E5", today: "Wikipedia \u6BCF\u65E5\u9605\u8BFB", featured: "\u4ECA\u65E5\u7CBE\u9009", popular: "\u70ED\u95E8\u6761\u76EE", history: "\u5386\u53F2\u4E0A\u7684\u4ECA\u5929", search: "\u641C\u7D22\u6761\u76EE", searchHint: "\u6807\u9898\u6216\u4E3B\u9898", searchAction: "\u641C\u7D22", results: "\u201C{query}\u201D\u7684\u641C\u7D22\u7ED3\u679C", empty: "\u6CA1\u6709\u627E\u5230\u6761\u76EE", noFeatured: "\u8FD9\u4E2A\u8BED\u8A00\u7248\u672C\u6CA1\u6709\u63D0\u4F9B\u4ECA\u65E5\u7CBE\u9009\u6761\u76EE\u3002", views: "{views} \u6B21\u9605\u8BFB", read: "\u9605\u8BFB\u6458\u8981", open: "\u5728 Wikipedia \u6253\u5F00", back: "\u8FD4\u56DE\u641C\u7D22\u7ED3\u679C", backToday: "\u8FD4\u56DE\u6BCF\u65E5\u9605\u8BFB", refresh: "\u5237\u65B0", retry: "\u91CD\u8BD5", loading: "\u6B63\u5728\u8BFB\u53D6\u6761\u76EE", readAt: "Wikipedia \u516C\u5F00\u5185\u5BB9 \xB7 \u8BFB\u4E8E {at}", summaryHint: "\u8FD9\u91CC\u5C55\u793A\u7B80\u77ED\u4ECB\u7ECD\uFF0C\u5168\u6587\u548C\u53C2\u8003\u8D44\u6599\u8BF7\u5728 Wikipedia \u67E5\u770B\u3002", attribution: "\u6587\u5B57\u6765\u81EA Wikipedia\uFF0C\u6309 CC BY-SA \u63D0\u4F9B\uFF1B\u56FE\u7247\u9075\u5FAA\u5404\u81EA\u7684\u8BB8\u53EF\u3002", account: "Wikipedia \u516C\u5F00\u5185\u5BB9\uFF0C\u65E0\u9700\u8D26\u53F7", toolToday: "\u8BFB\u53D6 Wikipedia \u4ECA\u65E5\u7CBE\u9009\u3001\u70ED\u95E8\u6761\u76EE\u548C\u5386\u53F2\u4E0A\u7684\u4ECA\u5929\u3002", toolSearch: "\u6309\u6807\u9898\u6216\u4E3B\u9898\u641C\u7D22 Wikipedia \u516C\u5F00\u6761\u76EE\u3002", toolArticle: "\u8BFB\u53D6 Wikipedia \u6761\u76EE\u7684\u7B80\u77ED\u4ECB\u7ECD\u548C\u6B63\u5F0F\u94FE\u63A5\uFF1B\u4E0D\u77E5\u9053\u51C6\u786E\u6807\u9898\u65F6\u5148\u641C\u7D22\u3002", query: "\u641C\u7D22\u5173\u952E\u8BCD", article: "\u51C6\u786E\u7684\u6761\u76EE\u6807\u9898\u6216 canonical key", limit: "\u6700\u591A\u8FD4\u56DE\u51E0\u6761\uFF0C1 \u5230 30", invalid: "\u8BF7\u8F93\u5165\u6709\u6548\u7684\u6761\u76EE\u6807\u9898\u6216\u641C\u7D22\u5173\u952E\u8BCD\u3002", bad_answer: "Wikipedia \u8FD4\u56DE\u4E86\u65E0\u6CD5\u8BC6\u522B\u7684\u5185\u5BB9\u3002", not_found: "\u6CA1\u6709\u627E\u5230\u8FD9\u4E2A\u6761\u76EE\u3002", rate_limited: "Wikipedia \u8BF7\u6C42\u8FC7\u591A\uFF0C\u8BF7\u7A0D\u540E\u518D\u8BD5\u3002", slow: "Wikipedia \u54CD\u5E94\u8D85\u65F6\u3002", network: "\u6682\u65F6\u65E0\u6CD5\u8FDE\u63A5 Wikipedia\u3002", http: "Wikipedia \u8FD4\u56DE HTTP {0}\u3002" };

// src/locales/ja.json
var ja_default = { featuredShort: "\u79C0\u9038", popularShort: "\u4EBA\u6C17", historyShort: "\u4ECA\u65E5", today: "Wikipedia \u4ECA\u65E5\u306E\u8AAD\u307F\u7269", featured: "\u4ECA\u65E5\u306E\u79C0\u9038\u306A\u8A18\u4E8B", popular: "\u3088\u304F\u8AAD\u307E\u308C\u3066\u3044\u308B\u8A18\u4E8B", history: "\u4ECA\u65E5\u306F\u4F55\u306E\u65E5", search: "\u8A18\u4E8B\u3092\u691C\u7D22", searchHint: "\u984C\u540D\u307E\u305F\u306F\u8A71\u984C", searchAction: "\u691C\u7D22", results: "\u300C{query}\u300D\u306E\u691C\u7D22\u7D50\u679C", empty: "\u8A18\u4E8B\u304C\u898B\u3064\u304B\u308A\u307E\u305B\u3093", noFeatured: "\u3053\u306E\u8A00\u8A9E\u7248\u3067\u306F\u4ECA\u65E5\u306E\u79C0\u9038\u306A\u8A18\u4E8B\u3092\u63D0\u4F9B\u3057\u3066\u3044\u307E\u305B\u3093\u3002", views: "{views} \u56DE\u95B2\u89A7", read: "\u8981\u7D04\u3092\u8AAD\u3080", open: "Wikipedia \u3067\u958B\u304F", back: "\u691C\u7D22\u7D50\u679C\u306B\u623B\u308B", backToday: "\u4ECA\u65E5\u306E\u8AAD\u307F\u7269\u306B\u623B\u308B", refresh: "\u66F4\u65B0", retry: "\u518D\u8A66\u884C", loading: "\u8A18\u4E8B\u3092\u8AAD\u307F\u8FBC\u307F\u4E2D", readAt: "Wikipedia \u306E\u516C\u958B\u30B3\u30F3\u30C6\u30F3\u30C4 \xB7 {at} \u306B\u53D6\u5F97", summaryHint: "\u77ED\u3044\u7D39\u4ECB\u3067\u3059\u3002\u5168\u6587\u3068\u53C2\u8003\u6587\u732E\u306F Wikipedia \u3067\u3054\u89A7\u304F\u3060\u3055\u3044\u3002", attribution: "\u6587\u7AE0\u306F Wikipedia \u3088\u308A\u3001CC BY-SA \u3067\u63D0\u4F9B\u3055\u308C\u3066\u3044\u307E\u3059\u3002\u753B\u50CF\u306B\u306F\u305D\u308C\u305E\u308C\u306E\u30E9\u30A4\u30BB\u30F3\u30B9\u304C\u9069\u7528\u3055\u308C\u307E\u3059\u3002", account: "Wikipedia \u306E\u516C\u958B\u30B3\u30F3\u30C6\u30F3\u30C4\u3002\u30A2\u30AB\u30A6\u30F3\u30C8\u4E0D\u8981", toolToday: "Wikipedia \u306E\u4ECA\u65E5\u306E\u79C0\u9038\u306A\u8A18\u4E8B\u3001\u3088\u304F\u8AAD\u307E\u308C\u3066\u3044\u308B\u8A18\u4E8B\u3001\u4ECA\u65E5\u306F\u4F55\u306E\u65E5\u3092\u8AAD\u307F\u307E\u3059\u3002", toolSearch: "\u984C\u540D\u3084\u8A71\u984C\u3067 Wikipedia \u306E\u516C\u958B\u8A18\u4E8B\u3092\u691C\u7D22\u3057\u307E\u3059\u3002", toolArticle: "Wikipedia \u8A18\u4E8B\u306E\u77ED\u3044\u7D39\u4ECB\u3068\u6B63\u898F\u30EA\u30F3\u30AF\u3092\u8AAD\u307F\u307E\u3059\u3002\u6B63\u78BA\u306A\u984C\u540D\u304C\u4E0D\u660E\u306A\u5834\u5408\u306F\u5148\u306B\u691C\u7D22\u3057\u3066\u304F\u3060\u3055\u3044\u3002", query: "\u691C\u7D22\u30AD\u30FC\u30EF\u30FC\u30C9", article: "\u6B63\u78BA\u306A\u8A18\u4E8B\u306E\u984C\u540D\u307E\u305F\u306F canonical key", limit: "\u6700\u5927\u4EF6\u6570\uFF081\u301C30\uFF09", invalid: "\u6709\u52B9\u306A\u8A18\u4E8B\u306E\u984C\u540D\u307E\u305F\u306F\u691C\u7D22\u30AD\u30FC\u30EF\u30FC\u30C9\u3092\u5165\u529B\u3057\u3066\u304F\u3060\u3055\u3044\u3002", bad_answer: "Wikipedia \u304C\u7121\u52B9\u306A\u5FDC\u7B54\u3092\u8FD4\u3057\u307E\u3057\u305F\u3002", not_found: "\u305D\u306E\u8A18\u4E8B\u306F\u898B\u3064\u304B\u308A\u307E\u305B\u3093\u3002", rate_limited: "Wikipedia \u3078\u306E\u30EA\u30AF\u30A8\u30B9\u30C8\u304C\u591A\u3059\u304E\u307E\u3059\u3002\u3057\u3070\u3089\u304F\u3057\u3066\u304B\u3089\u518D\u8A66\u884C\u3057\u3066\u304F\u3060\u3055\u3044\u3002", slow: "Wikipedia \u306E\u5FDC\u7B54\u304C\u30BF\u30A4\u30E0\u30A2\u30A6\u30C8\u3057\u307E\u3057\u305F\u3002", network: "Wikipedia \u306B\u63A5\u7D9A\u3067\u304D\u307E\u305B\u3093\u3002", http: "Wikipedia \u304C HTTP {0} \u3092\u8FD4\u3057\u307E\u3057\u305F\u3002" };

// src/i18n.ts
var textsFor = (locale = "") => locale.toLowerCase().startsWith("ja") ? ja_default : locale.toLowerCase().startsWith("zh") ? zh_Hans_default : en_default;
var fmt = (text, values, ...args) => text.replace(/\{(\w+)\}/g, (_, key) => String(Array.isArray(values) ? values[Number(key)] ?? "" : values[key] ?? ""));
function errorText(error, locale = "") {
  const t = textsFor(locale);
  if (!(error instanceof ReadError)) return t.network;
  return error.code === "http" ? fmt(t.http, { 0: error.status ?? 0 }) : t[error.code];
}
function readTime(at, timeZone = "Local", locale = "en") {
  const zone = timeZone === "Local" ? void 0 : timeZone;
  return new Intl.DateTimeFormat(locale, { year: "numeric", month: "short", day: "numeric", timeZone: zone }).format(at);
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
var Wikipedia = class {
  constructor(fetch, config = {}, options = {}) {
    this.fetch = fetch;
    this.config = configFrom(config);
    this.locale = options.locale ?? "en";
    this.language = languageFor(this.config, this.locale);
    this.timezone = options.timezone || "Local";
    if (this.timezone !== "Local") new Intl.DateTimeFormat("en", { timeZone: this.timezone });
    this.now = options.now ?? Date.now;
    this.monotonic = options.monotonic ?? (() => performance.now());
    this.limits = { ...limits, ...options.limits };
  }
  config;
  locale;
  language;
  timezone;
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
      response = await this.fetch(`https://${this.language}.wikipedia.org${path}`, {
        method: "GET",
        headers: { Accept: "application/json", "User-Agent": "persona-wikipedia/0.1.0" },
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
  async today(date = dateAt(this.now()), refresh = false, signal) {
    const path = feedPath(date), rd = await this.read(path, refresh ? 0 : this.limits.feedFresh, signal);
    return { ...parseFeed(rd.raw, this.language, date, rd.at), stale: rd.stale };
  }
  async article(title, refresh = false, signal) {
    const rd = await this.read(summaryPath(title), refresh ? 0 : this.limits.articleFresh, signal);
    return parseArticle(rd.raw, this.language);
  }
  async search(query, limit = 30, signal) {
    const rd = await this.read(searchPath(query, limit), this.limits.feedFresh, signal);
    return parseSearch(rd.raw, this.language);
  }
  cacheStats() {
    return { entries: this.cache.size, bytes: this.bytes };
  }
};

// src/tools.ts
function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [
    { name: "wikipedia_today", description: t.toolToday, input_schema: { type: "object", properties: {} } },
    { name: "wikipedia_search", description: t.toolSearch, input_schema: { type: "object", required: ["query"], properties: { query: { type: "string", description: t.query }, limit: { type: "integer", minimum: 1, maximum: 30, description: t.limit } } } },
    { name: "wikipedia_article", description: t.toolArticle, input_schema: { type: "object", required: ["title"], properties: { title: { type: "string", description: t.article } } } }
  ];
}
var tools = toolsFor();
function line(article, index) {
  return `${index + 1}. ${clip(article.heading, 300)}
   ${[clip(article.description, 300), article.views === void 0 ? "" : `${article.views} views`].filter(Boolean).join(" \xB7 ")}
   ${article.url}
${article.extract ? "   " + clip(article.extract, 600) + "\n" : ""}`;
}
function argsFrom(raw, exact) {
  if (exact) {
    const args = {};
    for (const key of ["query", "title"]) for (const token of exact.fieldValues(key)) {
      const value2 = JSON.parse(token.raw);
      if (typeof value2 !== "string") throw new ReadError("invalid");
      args[key] = value2;
    }
    args.limit = exact.int64Fields(["limit"]).limit;
    return args;
  }
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = raw;
  if (value.query != null && typeof value.query !== "string" || value.title != null && typeof value.title !== "string") throw new ReadError("invalid");
  if (value.limit != null && (typeof value.limit !== "number" || !Number.isSafeInteger(value.limit))) throw new ReadError("invalid");
  return value;
}
async function callTool(service, name, raw = {}, signal, exact) {
  const args = argsFrom(raw, exact), t = textsFor(service.locale), request = budget(signal, service.limits.tool);
  try {
    if (name === "wikipedia_today") {
      const feed = await service.today(dateAt(service.now()), false, request.signal);
      let out = `${t.featured}
${feed.featured ? line(feed.featured, 0) : t.noFeatured + "\n"}
${t.popular}
`;
      out += feed.popular.slice(0, 15).map(line).join("\n");
      out += `
${t.history}
` + feed.events.slice(0, 8).map((event) => `${event.year}. ${clip(event.text, 500)}${event.pages[0] ? `
   ${event.pages[0].url}` : ""}`).join("\n");
      return clip(out.trim(), 29999);
    }
    if (name === "wikipedia_search") {
      if (typeof args.query !== "string" || !args.query.trim() || Array.from(args.query).length > 200) throw new ReadError("invalid");
      const limit = args.limit == null ? 10 : Number(args.limit);
      if (!Number.isSafeInteger(limit) || limit < 1 || limit > 30) throw new ReadError("invalid");
      const articles = await service.search(args.query, limit, request.signal);
      return clip(articles.length ? articles.map(line).join("\n").trim() : fmt(t.results, { query: args.query }) + "\n" + t.empty, 29999);
    }
    if (name === "wikipedia_article") {
      if (typeof args.title !== "string") throw new ReadError("invalid");
      const article = await service.article(args.title, false, request.signal);
      return clip(`${article.heading}
${article.description}
${article.url}

${article.extract}`.trim(), 29999);
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
    if (p.contribution && p.contribution !== "wikipedia") throw noContribution(String(p.contribution));
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
var initialState = { v: "today", t: "featured", a: "", q: "", d: "" };
function readState(raw) {
  if (typeof raw !== "string" || raw.length > 4e3) return initialState;
  try {
    const value = JSON.parse(raw);
    if (!value || Array.isArray(value)) return initialState;
    const state = {
      v: value.v === "search" || value.v === "article" ? value.v : "today",
      t: value.t === "popular" || value.t === "history" ? value.t : "featured",
      a: typeof value.a === "string" && value.a ? titleKey(value.a) : "",
      q: typeof value.q === "string" ? clip(value.q.trim(), 200) : "",
      d: typeof value.d === "string" && /^\d{4}-\d{2}-\d{2}$/.test(value.d) ? value.d : ""
    };
    if (state.v === "article" && !state.a || state.v === "search" && !state.q) state.v = "today";
    return state;
  } catch {
    return initialState;
  }
}
var encodeState = (state) => JSON.stringify(state);
function transition(state, id, values = {}) {
  const [kind, argument] = id.split(":", 2);
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
  try {
    if (kind === "tab" && (argument === "featured" || argument === "popular" || argument === "history")) return { ...state, t: argument };
    if (kind === "tab" && (string2("tab") === "featured" || string2("tab") === "popular" || string2("tab") === "history")) return { ...state, t: string2("tab") };
    if (kind === "search") {
      const query = clip((string2("query") ?? "").trim(), 200);
      return query ? { ...state, v: "search", q: query, a: "" } : state;
    }
    if (kind === "article" && argument) return { ...state, v: "article", a: titleKey(argument) };
    if (kind === "back") return { ...state, v: state.q ? "search" : "today", a: "" };
    if (kind === "home") return { ...initialState, d: state.d };
  } catch {
  }
  return state;
}
async function loadPage(service, state, refresh, signal) {
  const request = budget(signal, service.limits.page);
  try {
    if (state.v === "article") return { kind: "article", article: await service.article(state.a, refresh, request.signal) };
    if (state.v === "search") return { kind: "search", articles: await service.search(state.q, 30, request.signal) };
    return { kind: "today", feed: await service.today(state.d || dateAt(service.now()), refresh, request.signal) };
  } catch (error) {
    checkSignal(signal);
    return { kind: "error", error };
  } finally {
    request.dispose();
  }
}
function page(props) {
  const { service, state, loaded, uiVersion = 0 } = props, t = textsFor(props.locale ?? service.locale), tz = props.tz ?? service.timezone;
  const act = (id, next, title) => props.action?.(id, next, title) ?? (next && uiVersion >= 3 ? { id, push: { ui: "today", state: encodeState(next), ...title ? { title } : {} } } : { id });
  const text = (key, value, style = "secondary") => /* @__PURE__ */ jsx(Text, { text: clip(value, 1900), style }, key);
  const search = /* @__PURE__ */ jsxs(Section, { title: t.search, children: [
    /* @__PURE__ */ jsx(Field, { name: "query", label: t.search, kind: "text", placeholder: t.searchHint, value: state.q }, "query-field"),
    /* @__PURE__ */ jsx(Button, { title: t.searchAction, action: act("search") }, "do-search")
  ] }, "search");
  const tabs = /* @__PURE__ */ jsx(Section, { children: uiVersion >= 2 ? /* @__PURE__ */ jsx(Segmented, { name: "tab", value: state.t, choices: [{ value: "featured", title: t.featuredShort }, { value: "popular", title: t.popularShort }, { value: "history", title: t.historyShort }], action: act("tab") }, "tab") : ["featured", "popular", "history"].map((tab) => /* @__PURE__ */ jsx(Row, { title: t[tab], action: act("tab:" + tab), badge: state.t === tab ? { text: t.read, tone: "positive" } : void 0 }, `tab-${tab}`)) }, "tabs");
  const back = /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(Button, { title: state.q ? t.back : t.backToday, action: act("back") }, "back-button") }, "navigation");
  if (loaded.kind === "loading") return /* @__PURE__ */ jsxs(View, { title: t.today, state: encodeState(state), children: [
    search,
    state.v === "today" ? tabs : back,
    /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(Loading, { title: t.loading }, "loading-indicator") }, "loading")
  ] }, "wikipedia");
  if (loaded.kind === "error") return /* @__PURE__ */ jsxs(View, { title: t.today, state: encodeState(state), children: [
    search,
    state.v === "today" ? tabs : back,
    /* @__PURE__ */ jsxs(Section, { children: [
      text("message", errorText(loaded.error, props.locale ?? service.locale)),
      /* @__PURE__ */ jsx(Button, { title: t.retry, action: act("refresh") }, "retry")
    ] }, "error")
  ] }, "wikipedia");
  if (state.v === "article") {
    if (loaded.kind !== "article") throw new ReadError("invalid");
    const article = loaded.article;
    return /* @__PURE__ */ jsxs(View, { title: article.heading, state: encodeState(state), children: [
      /* @__PURE__ */ jsxs(Section, { children: [
        article.description ? text("description", article.description, "caption") : null,
        article.extract ? text("extract", article.extract, "body") : text("no-summary", t.noFeatured)
      ] }, "article-summary"),
      /* @__PURE__ */ jsxs(Section, { footer: t.summaryHint, children: [
        /* @__PURE__ */ jsx(Link, { title: t.open, url: article.url }, "open"),
        /* @__PURE__ */ jsx(Button, { title: state.q ? t.back : t.backToday, action: act("back") }, "back-button")
      ] }, "article-actions"),
      /* @__PURE__ */ jsx(Section, { children: text("license", t.attribution, "caption") }, "attribution")
    ] }, "wikipedia");
  }
  const articleRow = (article, index) => /* @__PURE__ */ jsx(Row, { title: article.heading, subtitle: [article.description, article.views !== void 0 ? fmt(t.views, { views: article.views.toLocaleString("en-US") }) : ""].filter(Boolean).join(" \xB7 "), action: act(`article:${article.title}`, { ...state, v: "article", a: article.title }, article.heading) }, `article-${index}-${article.title}`);
  if (state.v === "search") {
    if (loaded.kind !== "search") throw new ReadError("invalid");
    return /* @__PURE__ */ jsxs(View, { title: fmt(t.results, { query: clip(state.q, 100) }), state: encodeState(state), children: [
      search,
      /* @__PURE__ */ jsxs(Section, { title: fmt(t.results, { query: clip(state.q, 100) }), children: [
        loaded.articles.length ? loaded.articles.map(articleRow) : text("empty", t.empty),
        /* @__PURE__ */ jsx(Button, { title: t.backToday, action: act("home") }, "home")
      ] }, "results")
    ] }, "wikipedia");
  }
  if (loaded.kind !== "today") throw new ReadError("invalid");
  const feed = loaded.feed;
  const footer = `${fmt(t.readAt, { at: readTime(feed.at, tz, props.locale ?? service.locale) })}${feed.stale ? ` \xB7 ${errorText(feed.stale, props.locale ?? service.locale)}` : ""}`;
  return /* @__PURE__ */ jsxs(View, { title: t.today, state: encodeState(state), children: [
    search,
    tabs,
    state.t === "featured" ? /* @__PURE__ */ jsx(Section, { title: t.featured, children: feed.featured ? articleRow(feed.featured, 0) : text("no-featured", t.noFeatured) }, "featured") : null,
    state.t === "popular" ? /* @__PURE__ */ jsx(Section, { title: t.popular, children: feed.popular.length ? feed.popular.slice(0, 20).map(articleRow) : text("empty", t.empty) }, "popular") : null,
    state.t === "history" ? /* @__PURE__ */ jsx(Section, { title: t.history, children: feed.events.length ? feed.events.slice(0, 12).map((event, i) => [
      text(`event-${i}-${event.year}`, `${event.year} \xB7 ${event.text}`, "body"),
      event.pages[0] ? articleRow(event.pages[0], i) : null
    ]) : text("empty", t.empty) }, "history") : null,
    /* @__PURE__ */ jsx(Section, { footer, children: /* @__PURE__ */ jsx(Button, { title: t.refresh, action: act("refresh") }, "refresh") }, "footer"),
    /* @__PURE__ */ jsx(Section, { children: text("license", t.attribution, "caption") }, "attribution")
  ] }, "wikipedia");
}

// src/root.tsx
function Root(options) {
  const [state, setState] = useState(() => readState(options.state));
  const [refresh, setRefresh] = useState(0);
  const [result, setResult] = useState(void 0);
  const identity = `${state.v}:${state.t}:${state.a}:${state.q}:${state.d}:${refresh}`;
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
        const push = { ui: "today", state: encodeState(next), ...title ? { title } : {} };
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
  return /* @__PURE__ */ jsx(Root, { ...options }, "wikipedia-root");
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
var sourceManifest = { ...persona_extension_default, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/wikipedia.mjs", sha256: "" } } };
delete sourceManifest.icon;
delete sourceManifest.replaces;
function createProgram(fetch = (input, init) => globalThis.fetch(input, init), options = {}) {
  let service;
  const current = () => {
    if (!service) throw new Error("Wikipedia has not been initialized");
    return service;
  };
  return {
    id: persona_extension_default.id,
    version: persona_extension_default.version,
    manifest: sourceManifest,
    init: (init, _hub) => {
      service = new Wikipedia(fetch, init.config, { ...options, locale: init.hub?.locale ?? options.locale, timezone: init.hub?.timezone ?? options.timezone });
    },
    namespaces: { connector: connector(current) },
    ui: { today: componentPage((req) => root({ service: current(), state: req.state, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })) }
  };
}
var index_default = createProgram();
export {
  ReadError,
  Root,
  Wikipedia,
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
