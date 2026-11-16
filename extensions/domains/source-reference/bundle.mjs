// ../../sdk/ts/src/exact-json.ts
var EXT_ERROR = Symbol.for("@persona/extension.ExtError");
var JSONIntegerError = class extends TypeError {
  [EXT_ERROR] = true;
  code = "invalid_params";
};
var MAX_EXACT_LENGTH = 8 << 20;
var VALIDATED_JSON = Symbol("validated JSON slice");

// ../../sdk/ts/src/storage.ts
var ExtensionStorage = class {
  constructor(hub) {
    this.hub = hub;
  }
  request() {
    return this.hub.call("storage.request", {});
  }
  get(key) {
    return this.hub.call("storage.get", { key });
  }
  list(params = {}) {
    return this.hub.call("storage.list", params);
  }
  transaction(params) {
    return this.hub.call("storage.transaction", params);
  }
  queueList(params) {
    return this.hub.call("storage.queue.list", params);
  }
  enqueue(params) {
    return this.hub.call("storage.queue.enqueue", params);
  }
  claim(params) {
    return this.hub.call("storage.queue.claim", params);
  }
  ack(params) {
    return this.hub.call("storage.queue.ack", params);
  }
  forget(params) {
    return this.hub.call("storage.queue.forget", params);
  }
  retry(params) {
    return this.hub.call("storage.queue.retry", params);
  }
  createBlob(params) {
    return this.hub.call("storage.blob.create", params);
  }
  writeBlob(params) {
    return this.hub.call("storage.blob.write", params);
  }
  finishBlob(params) {
    return this.hub.call("storage.blob.finish", params);
  }
  readBlob(params) {
    return this.hub.call("storage.blob.read", params);
  }
  deleteBlob(params) {
    return this.hub.call("storage.blob.delete", params);
  }
};
function storageBytes(bytes) {
  if (bytes.length > 65536) throw new RangeError("storage values and chunks are at most 64 KiB");
  let binary = "";
  for (let at = 0; at < bytes.length; at += 8192) binary += String.fromCharCode(...bytes.subarray(at, at + 8192));
  return btoa(binary);
}
function storageValue(value) {
  if (value.length > 87384) throw new RangeError("storage value exceeds the encoded bound");
  const binary = atob(value), bytes = Uint8Array.from(binary, (char) => char.charCodeAt(0));
  if (bytes.length > 65536 || storageBytes(bytes) !== value) throw new TypeError("noncanonical storage base64");
  return bytes;
}

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
var persona_extension_default = { manifest_version: 1, id: "domains", name: "Domains \u57DF\u540D", version: "0.1.1", icon: "assets/icon.png", description: "\u67E5\u8BE2\u5355\u4E2A .com \u6216 .net \u57DF\u540D\u7684\u516C\u5F00 RDAP \u6CE8\u518C\u4FE1\u606F\uFF0C\u67E5\u770B\u6CE8\u518C\u3001\u5230\u671F\u548C\u53D8\u66F4\u65E5\u671F\uFF0C\u4FDD\u7559\u672C\u5730\u6700\u8FD1\u67E5\u8BE2\u3002\u65E0\u9700\u8D26\u53F7\uFF1B\u4E0D\u505A\u6279\u91CF\u626B\u63CF\u3001DNS \u63A2\u6D4B\u6216\u8054\u7CFB\u4EBA\u6536\u96C6\u3002", publisher: "nwverse", homepage: "https://github.com/nwverse/persona-extensions/blob/main/extensions/domains/README.md", tags: ["\u57DF\u540D", "RDAP", "\u6CE8\u518C\u4FE1\u606F", "\u5230\u671F\u65E5\u671F"], engines: { persona: ">=202611101800", extension_protocol: 1 }, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/domains.mjs", sha256: "" } }, contributes: { connectors: [{ id: "domains", name: "Domains", capabilities: ["status", "tools"], hosts: ["data.iana.org", "rdap.verisign.com"], agent_tools: ["domains_*"] }], ui: [{ id: "lookup", title: "\u57DF\u540D\u67E5\u8BE2", placement: "nav_page", icon: "search" }] }, permissions: { network: ["data.iana.org", "rdap.verisign.com"], storage: { keys: ["history"] } } };

// src/model.ts
var limits = Object.freeze({ request: 12e3, page: 18e3, tool: 18e3, response: 1 << 20, bootstrap: 24 * 60 * 6e4, fresh: 15 * 6e4, cache: 48, cacheBytes: 2 << 20, gap: 350 });
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
function string(value) {
  if (typeof value !== "string") throw new ReadError("bad_answer");
  return value;
}
function domainName(raw) {
  if (typeof raw !== "string" || raw.length > 1024 || /[\u0000- \u007f\\/@#?&=]/u.test(raw)) throw new ReadError("invalid");
  let unicode;
  try {
    unicode = raw.trim().toLowerCase().replace(/\.$/u, "").normalize("NFC");
  } catch {
    throw new ReadError("invalid");
  }
  if (!unicode || unicode.length > 253 || /[_*:%]/u.test(unicode) || /^\d{1,3}(?:\.\d{1,3}){3}$/u.test(unicode)) throw new ReadError("invalid");
  const labels = unicode.split(".");
  if (labels.length < 2 || labels.some((label) => !label || label.length > 63)) throw new ReadError("invalid");
  const asciiLabels = labels.map((label) => {
    let ascii;
    try {
      ascii = new URL(`https://${label}.invalid`).hostname.slice(0, -8);
    } catch {
      throw new ReadError("invalid");
    }
    if (!ascii || ascii.length > 63 || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/u.test(ascii) || /^xn--/u.test(label) && !/^xn--[a-z0-9-]+$/u.test(label)) throw new ReadError("invalid");
    return ascii;
  });
  return { ascii: asciiLabels.join("."), unicode };
}
function rdapService(raw, domain) {
  if (!["com", "net"].includes(domainName(domain).ascii.split(".").at(-1))) throw new ReadError("unsupported");
  const root2 = object(raw), suffix = domainName(domain).ascii.split(".").at(-1);
  for (const entry of array(root2.services).slice(0, 4096)) {
    const pair = array(entry), zones = array(pair[0]).map(string).map((zone) => zone.toLowerCase()), urls = array(pair[1]);
    if (!zones.includes(suffix)) continue;
    const candidates = urls.map((value) => {
      try {
        const url = new URL(string(value));
        if (url.protocol !== "https:" || url.username || url.password || url.port || url.search || url.hash || !url.pathname.endsWith("/")) return "";
        if (url.origin !== "https://rdap.verisign.com" || url.pathname !== `/${suffix}/v1/`) return "";
        return url.toString();
      } catch {
        return "";
      }
    }).filter(Boolean);
    if (candidates.length !== 1) throw new ReadError("unsupported");
    return { origin: new URL(candidates[0]).origin, base: candidates[0] };
  }
  throw new ReadError("unsupported");
}
function lookupURL(service, domain) {
  return `${service.base}domain/${encodeURIComponent(domainName(domain).ascii)}`;
}
function publicLink(value, kind) {
  let url;
  try {
    url = new URL(string(value));
  } catch {
    throw new ReadError("bad_answer");
  }
  if (url.protocol !== "https:" || url.username || url.password || url.port) throw new ReadError("bad_answer");
  if (kind === "iana") {
    if (url.origin !== "https://www.iana.org") throw new ReadError("bad_answer");
  } else if (url.hostname === "localhost" || url.hostname === "127.0.0.1" || url.hostname === "::1" || !url.hostname.includes(".")) throw new ReadError("bad_answer");
  url.hash = "";
  return url.toString();
}
function registrar(raw) {
  if (!Array.isArray(raw)) return void 0;
  for (const entity of raw.slice(0, 32)) {
    const item = object(entity), roles = array(item.roles ?? []);
    if (!roles.includes("registrar")) continue;
    const fn = Array.isArray(item.vcardArray) && Array.isArray(item.vcardArray[1]) ? item.vcardArray[1].find((row) => Array.isArray(row) && row[0] === "fn" && typeof row[3] === "string") : void 0;
    if (fn) return clip(plain(fn[3]), 300);
  }
  return void 0;
}
function date(value) {
  const raw = string(value);
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/u.test(raw) || !Number.isFinite(Date.parse(raw))) throw new ReadError("bad_answer");
  const [y, m, d] = raw.slice(0, 10).split("-").map(Number), parsed = new Date(raw);
  if (parsed.getUTCFullYear() !== y || parsed.getUTCMonth() !== m - 1 || parsed.getUTCDate() !== d) throw new ReadError("bad_answer");
  return parsed.toISOString();
}
function parseRegistration(raw, domain, at) {
  const requested = domainName(domain), root2 = object(raw);
  if (root2.objectClassName !== "domain") throw new ReadError("bad_answer");
  const name = root2.ldhName == null ? requested.ascii : domainName(string(root2.ldhName)).ascii;
  if (name !== requested.ascii) throw new ReadError("bad_answer");
  const statuses = array(root2.status ?? []).slice(0, 32).map((value) => clip(plain(string(value)), 160)).filter(Boolean);
  const events = array(root2.events ?? []).slice(0, 32).map((value) => {
    const item = object(value);
    return { action: clip(plain(string(item.eventAction)), 80), at: date(item.eventDate) };
  });
  const self = array(root2.links ?? []).slice(0, 32).find((link) => object(link).rel === "self");
  const url = publicLink(object(self ?? {}).href, "self");
  return { domain: requested, name, statuses, events, registrar: registrar(root2.entities), url, source: new URL(url).origin, at };
}
var statusURL = "https://www.iana.org/assignments/rdap-status-values/rdap-status-values.xhtml";

// src/locales/en.json
var en_default = {
  account: "Public RDAP registration records \xB7 no account",
  lookup: "Domain registration",
  search: "Look up a domain",
  hint: "example.com or a domain in your language",
  action: "Look up",
  result: "Registration for {domain}",
  registeredName: "Registry name",
  statuses: "Registry status",
  statusHint: "Status names come from the registry. They do not by themselves prove availability or ownership.",
  statusGlossary: "Read the IANA status glossary",
  events: "Lifecycle dates",
  noEvents: "The registry did not supply lifecycle dates.",
  noStatus: "The registry did not supply status values.",
  registrar: "Registrar",
  source: "Registry source",
  openRecord: "Open the registry record",
  history: "Recent lookups",
  historyEmpty: "A successful lookup will appear here.",
  historyHint: "Only the domains you look up are kept in this Persona installation. No contact details are collected.",
  clearHistory: "Clear recent lookups",
  cleared: "Recent lookups cleared.",
  refresh: "Refresh",
  retry: "Try again",
  loading: "Reading the public registry\u2026",
  readAt: "Registry read {at}",
  noRecord: "No public registration record was found for this domain.",
  notFound: "The registry returned no public record for this domain.",
  attribution: "Registry selection: IANA RDAP bootstrap. Registration data comes from the named public registry; no WHOIS scraping, DNS probing or bulk lookup.",
  toolLookup: "Look up one explicitly supplied domain's public RDAP registration dates, lifecycle statuses, registrar and registry link.",
  domain: "One domain name, without a URL, path or credentials",
  invalid: "Enter one valid domain name, without a URL, path or credentials.",
  bad_answer: "The registry returned an incomplete or unexpected answer.",
  not_found: "No public registration record was found for this domain.",
  unsupported: "This version supports .com and .net through IANA's selected Verisign RDAP service.",
  rate_limited: "The registry is busy. Please try again later.",
  slow: "The registry took too long to answer.",
  network: "The public registry could not be reached.",
  http: "The registry returned HTTP {0}.",
  storage: "Recent-lookup storage is unavailable. The registration result is still shown.",
  storageUnavailable: "Recent lookups need a current Persona hub with installation-owned storage enabled."
};

// src/locales/zh-Hans.json
var zh_Hans_default = {
  account: "\u516C\u5F00 RDAP \u6CE8\u518C\u4FE1\u606F \xB7 \u65E0\u9700\u8D26\u53F7",
  lookup: "\u57DF\u540D\u6CE8\u518C\u4FE1\u606F",
  search: "\u67E5\u8BE2\u57DF\u540D",
  hint: "example.com \u6216\u4F60\u7684\u8BED\u8A00\u57DF\u540D",
  action: "\u67E5\u8BE2",
  result: "{domain} \u7684\u6CE8\u518C\u4FE1\u606F",
  registeredName: "\u6CE8\u518C\u5C40\u57DF\u540D",
  statuses: "\u6CE8\u518C\u5C40\u72B6\u6001",
  statusHint: "\u72B6\u6001\u540D\u79F0\u7531\u6CE8\u518C\u5C40\u8FD4\u56DE\uFF0C\u4E0D\u80FD\u4EC5\u51ED\u5B83\u5224\u65AD\u57DF\u540D\u53EF\u6CE8\u518C\u6216\u5F52\u5C5E\u3002",
  statusGlossary: "\u67E5\u770B IANA \u72B6\u6001\u8BF4\u660E",
  events: "\u751F\u547D\u5468\u671F\u65E5\u671F",
  noEvents: "\u6CE8\u518C\u5C40\u6CA1\u6709\u63D0\u4F9B\u751F\u547D\u5468\u671F\u65E5\u671F\u3002",
  noStatus: "\u6CE8\u518C\u5C40\u6CA1\u6709\u63D0\u4F9B\u72B6\u6001\u3002",
  registrar: "\u6CE8\u518C\u5546",
  source: "\u6CE8\u518C\u5C40\u6765\u6E90",
  openRecord: "\u6253\u5F00\u6CE8\u518C\u5C40\u8BB0\u5F55",
  history: "\u6700\u8FD1\u67E5\u8BE2",
  historyEmpty: "\u67E5\u8BE2\u6210\u529F\u540E\uFF0C\u57DF\u540D\u4F1A\u663E\u793A\u5728\u8FD9\u91CC\u3002",
  historyHint: "\u53EA\u4FDD\u5B58\u4F60\u67E5\u8FC7\u7684\u57DF\u540D\uFF0C\u4FDD\u5B58\u5728\u5F53\u524D Persona \u5B89\u88C5\u4E2D\uFF0C\u4E0D\u6536\u96C6\u8054\u7CFB\u4EBA\u4FE1\u606F\u3002",
  clearHistory: "\u6E05\u7A7A\u6700\u8FD1\u67E5\u8BE2",
  cleared: "\u5DF2\u6E05\u7A7A\u6700\u8FD1\u67E5\u8BE2\u3002",
  refresh: "\u5237\u65B0",
  retry: "\u91CD\u8BD5",
  loading: "\u6B63\u5728\u8BFB\u53D6\u516C\u5F00\u6CE8\u518C\u5C40\u2026",
  readAt: "\u6CE8\u518C\u5C40\u8BFB\u53D6\u4E8E {at}",
  noRecord: "\u6CA1\u6709\u627E\u5230\u8FD9\u4E2A\u57DF\u540D\u7684\u516C\u5F00\u6CE8\u518C\u4FE1\u606F\u3002",
  notFound: "\u6CE8\u518C\u5C40\u6CA1\u6709\u8FD9\u4E2A\u57DF\u540D\u7684\u516C\u5F00\u8BB0\u5F55\u3002",
  attribution: "\u6CE8\u518C\u5C40\u7531 IANA RDAP \u5F15\u5BFC\u76EE\u5F55\u9009\u62E9\uFF0C\u6CE8\u518C\u6570\u636E\u6765\u81EA\u6240\u5217\u516C\u5F00\u6CE8\u518C\u5C40\uFF1B\u4E0D\u6293\u53D6 WHOIS \u9875\u9762\uFF0C\u4E0D\u63A2\u6D4B DNS\uFF0C\u4E0D\u6279\u91CF\u67E5\u8BE2\u3002",
  toolLookup: "\u67E5\u8BE2\u4E00\u4E2A\u660E\u786E\u7ED9\u51FA\u7684\u57DF\u540D\u7684\u516C\u5F00 RDAP \u6CE8\u518C\u65E5\u671F\u3001\u751F\u547D\u5468\u671F\u72B6\u6001\u3001\u6CE8\u518C\u5546\u548C\u6CE8\u518C\u5C40\u94FE\u63A5\u3002",
  domain: "\u4E00\u4E2A\u57DF\u540D\uFF0C\u4E0D\u5E26\u7F51\u5740\u3001\u8DEF\u5F84\u6216\u51ED\u636E",
  invalid: "\u8BF7\u8F93\u5165\u4E00\u4E2A\u6709\u6548\u57DF\u540D\uFF0C\u4E0D\u8981\u5E26\u7F51\u5740\u3001\u8DEF\u5F84\u6216\u51ED\u636E\u3002",
  bad_answer: "\u6CE8\u518C\u5C40\u8FD4\u56DE\u4E86\u4E0D\u5B8C\u6574\u6216\u65E0\u6CD5\u8BC6\u522B\u7684\u5185\u5BB9\u3002",
  not_found: "\u6CA1\u6709\u627E\u5230\u8FD9\u4E2A\u57DF\u540D\u7684\u516C\u5F00\u6CE8\u518C\u4FE1\u606F\u3002",
  unsupported: "\u5F53\u524D\u7248\u672C\u901A\u8FC7 IANA \u9009\u5B9A\u7684 Verisign \u670D\u52A1\u652F\u6301 .com \u548C .net \u57DF\u540D\u3002",
  rate_limited: "\u6CE8\u518C\u5C40\u6682\u65F6\u7E41\u5FD9\uFF0C\u8BF7\u7A0D\u540E\u91CD\u8BD5\u3002",
  slow: "\u6CE8\u518C\u5C40\u54CD\u5E94\u8D85\u65F6\u3002",
  network: "\u6682\u65F6\u65E0\u6CD5\u8FDE\u63A5\u516C\u5F00\u6CE8\u518C\u5C40\u3002",
  http: "\u6CE8\u518C\u5C40\u8FD4\u56DE HTTP {0}\u3002",
  storage: "\u6700\u8FD1\u67E5\u8BE2\u5B58\u50A8\u6682\u65F6\u4E0D\u53EF\u7528\uFF0C\u6CE8\u518C\u4FE1\u606F\u4ECD\u53EF\u67E5\u770B\u3002",
  storageUnavailable: "\u6700\u8FD1\u67E5\u8BE2\u9700\u8981\u652F\u6301\u6269\u5C55\u79C1\u6709\u5B58\u50A8\u7684\u65B0\u7248 Persona hub\uFF0C\u5E76\u6388\u4E88\u76F8\u5E94\u5B58\u50A8\u6743\u9650\u3002"
};

// src/locales/ja.json
var ja_default = {
  account: "\u516C\u958B RDAP \u767B\u9332\u60C5\u5831 \xB7 \u30A2\u30AB\u30A6\u30F3\u30C8\u4E0D\u8981",
  lookup: "\u30C9\u30E1\u30A4\u30F3\u767B\u9332\u60C5\u5831",
  search: "\u30C9\u30E1\u30A4\u30F3\u3092\u8ABF\u3079\u308B",
  hint: "example.com \u307E\u305F\u306F\u56FD\u969B\u5316\u30C9\u30E1\u30A4\u30F3",
  action: "\u691C\u7D22",
  result: "{domain} \u306E\u767B\u9332\u60C5\u5831",
  registeredName: "\u30EC\u30B8\u30B9\u30C8\u30EA\u4E0A\u306E\u540D\u524D",
  statuses: "\u30EC\u30B8\u30B9\u30C8\u30EA\u30B9\u30C6\u30FC\u30BF\u30B9",
  statusHint: "\u30B9\u30C6\u30FC\u30BF\u30B9\u540D\u306F\u30EC\u30B8\u30B9\u30C8\u30EA\u306E\u5024\u3067\u3059\u3002\u3053\u308C\u3060\u3051\u3067\u767B\u9332\u53EF\u80FD\u6027\u3084\u6240\u6709\u8005\u3092\u5224\u65AD\u3057\u307E\u305B\u3093\u3002",
  statusGlossary: "IANA \u30B9\u30C6\u30FC\u30BF\u30B9\u7528\u8A9E\u96C6\u3092\u8AAD\u3080",
  events: "\u30E9\u30A4\u30D5\u30B5\u30A4\u30AF\u30EB\u306E\u65E5\u4ED8",
  noEvents: "\u30E9\u30A4\u30D5\u30B5\u30A4\u30AF\u30EB\u306E\u65E5\u4ED8\u306F\u63D0\u4F9B\u3055\u308C\u3066\u3044\u307E\u305B\u3093\u3002",
  noStatus: "\u30B9\u30C6\u30FC\u30BF\u30B9\u5024\u306F\u63D0\u4F9B\u3055\u308C\u3066\u3044\u307E\u305B\u3093\u3002",
  registrar: "\u30EC\u30B8\u30B9\u30C8\u30E9",
  source: "\u30EC\u30B8\u30B9\u30C8\u30EA\u306E\u63D0\u4F9B\u5143",
  openRecord: "\u30EC\u30B8\u30B9\u30C8\u30EA\u306E\u8A18\u9332\u3092\u958B\u304F",
  history: "\u6700\u8FD1\u306E\u691C\u7D22",
  historyEmpty: "\u691C\u7D22\u306B\u6210\u529F\u3059\u308B\u3068\u3001\u3053\u3053\u306B\u8868\u793A\u3055\u308C\u307E\u3059\u3002",
  historyHint: "\u691C\u7D22\u3057\u305F\u30C9\u30E1\u30A4\u30F3\u3060\u3051\u3092\u3053\u306E Persona \u30A4\u30F3\u30B9\u30C8\u30FC\u30EB\u306B\u4FDD\u5B58\u3057\u307E\u3059\u3002\u9023\u7D61\u5148\u306F\u53CE\u96C6\u3057\u307E\u305B\u3093\u3002",
  clearHistory: "\u6700\u8FD1\u306E\u691C\u7D22\u3092\u6D88\u53BB",
  cleared: "\u6700\u8FD1\u306E\u691C\u7D22\u3092\u6D88\u53BB\u3057\u307E\u3057\u305F\u3002",
  refresh: "\u66F4\u65B0",
  retry: "\u518D\u8A66\u884C",
  loading: "\u516C\u958B\u30EC\u30B8\u30B9\u30C8\u30EA\u3092\u8AAD\u307F\u8FBC\u3093\u3067\u3044\u307E\u3059\u2026",
  readAt: "\u30EC\u30B8\u30B9\u30C8\u30EA\u53D6\u5F97\u65E5\u6642 {at}",
  noRecord: "\u3053\u306E\u30C9\u30E1\u30A4\u30F3\u306E\u516C\u958B\u767B\u9332\u8A18\u9332\u306F\u898B\u3064\u304B\u308A\u307E\u305B\u3093\u3067\u3057\u305F\u3002",
  notFound: "\u3053\u306E\u30C9\u30E1\u30A4\u30F3\u306E\u516C\u958B\u8A18\u9332\u306F\u30EC\u30B8\u30B9\u30C8\u30EA\u306B\u3042\u308A\u307E\u305B\u3093\u3002",
  attribution: "\u30EC\u30B8\u30B9\u30C8\u30EA\u306E\u9078\u629E\u306B\u306F IANA RDAP \u30D6\u30FC\u30C8\u30B9\u30C8\u30E9\u30C3\u30D7\u3092\u4F7F\u7528\u3057\u307E\u3059\u3002\u767B\u9332\u60C5\u5831\u306F\u8A18\u8F09\u3055\u308C\u305F\u516C\u958B\u30EC\u30B8\u30B9\u30C8\u30EA\u304B\u3089\u53D6\u5F97\u3057\u3001WHOIS \u306E\u30B9\u30AF\u30EC\u30A4\u30D4\u30F3\u30B0\u3001DNS \u30D7\u30ED\u30FC\u30D6\u3001\u4E00\u62EC\u691C\u7D22\u306F\u884C\u3044\u307E\u305B\u3093\u3002",
  toolLookup: "\u660E\u793A\u3055\u308C\u305F1\u3064\u306E\u30C9\u30E1\u30A4\u30F3\u306B\u3064\u3044\u3066\u3001\u516C\u958B RDAP \u306E\u767B\u9332\u65E5\u3001\u30E9\u30A4\u30D5\u30B5\u30A4\u30AF\u30EB\u30B9\u30C6\u30FC\u30BF\u30B9\u3001\u30EC\u30B8\u30B9\u30C8\u30E9\u3001\u30EC\u30B8\u30B9\u30C8\u30EA\u30EA\u30F3\u30AF\u3092\u8ABF\u3079\u307E\u3059\u3002",
  domain: "URL\u3001\u30D1\u30B9\u3001\u8A8D\u8A3C\u60C5\u5831\u3092\u542B\u307E\u306A\u30441\u3064\u306E\u30C9\u30E1\u30A4\u30F3\u540D",
  invalid: "URL\u3001\u30D1\u30B9\u3001\u8A8D\u8A3C\u60C5\u5831\u3092\u542B\u307E\u306A\u3044\u6709\u52B9\u306A\u30C9\u30E1\u30A4\u30F3\u540D\u3092\u5165\u529B\u3057\u3066\u304F\u3060\u3055\u3044\u3002",
  bad_answer: "\u30EC\u30B8\u30B9\u30C8\u30EA\u306E\u5FDC\u7B54\u304C\u4E0D\u5B8C\u5168\u304B\u3001\u5F62\u5F0F\u304C\u4E0D\u6B63\u3067\u3059\u3002",
  not_found: "\u3053\u306E\u30C9\u30E1\u30A4\u30F3\u306E\u516C\u958B\u767B\u9332\u8A18\u9332\u306F\u898B\u3064\u304B\u308A\u307E\u305B\u3093\u3067\u3057\u305F\u3002",
  unsupported: "\u3053\u306E\u30D0\u30FC\u30B8\u30E7\u30F3\u306F IANA \u304C\u9078\u629E\u3059\u308B Verisign RDAP \u30B5\u30FC\u30D3\u30B9\u306B\u3088\u308B .com \u3068 .net \u306B\u5BFE\u5FDC\u3057\u3066\u3044\u307E\u3059\u3002",
  rate_limited: "\u30EC\u30B8\u30B9\u30C8\u30EA\u304C\u6DF7\u96D1\u3057\u3066\u3044\u307E\u3059\u3002\u3057\u3070\u3089\u304F\u3057\u3066\u304B\u3089\u304A\u8A66\u3057\u304F\u3060\u3055\u3044\u3002",
  slow: "\u30EC\u30B8\u30B9\u30C8\u30EA\u306E\u5FDC\u7B54\u304C\u30BF\u30A4\u30E0\u30A2\u30A6\u30C8\u3057\u307E\u3057\u305F\u3002",
  network: "\u516C\u958B\u30EC\u30B8\u30B9\u30C8\u30EA\u306B\u63A5\u7D9A\u3067\u304D\u307E\u305B\u3093\u3002",
  http: "\u30EC\u30B8\u30B9\u30C8\u30EA\u304C HTTP {0} \u3092\u8FD4\u3057\u307E\u3057\u305F\u3002",
  storage: "\u691C\u7D22\u5C65\u6B74\u3092\u4FDD\u5B58\u3067\u304D\u307E\u305B\u3093\u304C\u3001\u767B\u9332\u60C5\u5831\u306F\u8868\u793A\u3067\u304D\u307E\u3059\u3002",
  storageUnavailable: "\u691C\u7D22\u5C65\u6B74\u306B\u306F\u3001\u30A4\u30F3\u30B9\u30C8\u30FC\u30EB\u5C02\u7528\u30B9\u30C8\u30EC\u30FC\u30B8\u3092\u5099\u3048\u305F\u6700\u65B0\u306E Persona hub \u3068\u3001\u305D\u306E\u6A29\u9650\u304C\u5FC5\u8981\u3067\u3059\u3002"
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
  return new Intl.DateTimeFormat(locale, { year: "numeric", month: "short", day: "numeric", timeZone: timeZone === "Local" ? void 0 : timeZone }).format(new Date(at));
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
var Domains = class {
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
  async response(url, signal) {
    let response;
    try {
      response = await this.fetch(url, { method: "GET", headers: { Accept: "application/rdap+json, application/json", "User-Agent": "persona-domains/0.1.0" }, signal, redirect: "error" });
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
        validate?.(raw);
        const at = this.now();
        this.save(path, raw, at);
        this.lastOK = at;
        this.lastError = void 0;
        return { raw, at };
      } catch (error) {
        if (request.signal.aborted) {
          const reason = request.signal.reason;
          if (reason instanceof Error && reason.name === "AbortError") throw reason;
          throw new DOMException("Cancelled", "AbortError");
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
  async bootstrap(signal) {
    const rd = await this.read("https://data.iana.org/rdap/dns.json", this.limits.bootstrap, signal, (raw) => {
      rdapService(raw, "example.com");
    });
    return rdapService(rd.raw, "example.com");
  }
  async lookup(domain, refresh = false, signal) {
    const name = domainName(domain);
    const bootstrap = await this.read("https://data.iana.org/rdap/dns.json", this.limits.bootstrap, signal, (raw) => {
      rdapService(raw, name.ascii);
    });
    const service = rdapService(bootstrap.raw, name.ascii), url = lookupURL(service, name.ascii);
    const rd = await this.read(url, refresh ? 0 : this.limits.fresh, signal, (raw) => {
      parseRegistration(raw, name.ascii, this.now());
    });
    return { ...parseRegistration(rd.raw, name.ascii, rd.at), stale: rd.stale };
  }
  cacheStats() {
    return { entries: this.cache.size, bytes: this.bytes };
  }
};

// src/tools.ts
function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [{ name: "domains_lookup", description: t.toolLookup, input_schema: { type: "object", required: ["domain"], properties: { domain: { type: "string", description: t.domain } } } }];
}
var tools = toolsFor();
async function callTool(service, name, raw = {}, signal) {
  if (name !== "domains_lookup") throw unknownMethod(`connector.tools.${name}`);
  if (!raw || typeof raw !== "object" || Array.isArray(raw) || typeof raw.domain !== "string") throw new ReadError("invalid");
  const request = budget(signal, service.limits.tool), t = textsFor(service.locale);
  try {
    const result = await service.lookup(raw.domain, false, request.signal);
    return clip([
      `${result.domain.unicode} (${result.name})`,
      result.url,
      `${t.registrar}: ${result.registrar ?? "\u2014"}`,
      `${t.statuses}: ${result.statuses.join(", ") || t.noStatus}`,
      ...result.events.map((event) => `${event.action}: ${event.at}`),
      `${t.source}: ${result.source}`,
      t.attribution
    ].join("\n"), 29999);
  } finally {
    request.dispose();
  }
}
function connector(getService, getHistory) {
  return withExactParams(async (method, params, call) => {
    const service = getService();
    if (params != null && (typeof params !== "object" || Array.isArray(params))) throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
    const p = params ?? {};
    if (p.contribution != null && typeof p.contribution !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
    if (p.contribution && p.contribution !== "domains") throw noContribution(String(p.contribution));
    if (method === "status") return service.status();
    if (method === "tools.list") return { tools: toolsFor(service.locale) };
    if (method === "tools.call") {
      if (typeof p.name !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
      try {
        const exact = call?.params?.field("args"), domain = exact ? (() => {
          const fields2 = [...exact.fieldValues("domain")];
          if (fields2.length !== 1) throw new ReadError("invalid");
          return JSON.parse(fields2[0].raw);
        })() : p.args?.domain;
        const text = await callTool(service, p.name, { domain });
        if (p.name === "domains_lookup" && typeof domain === "string") {
          try {
            await getHistory().add(domain);
          } catch {
          }
        }
        return { text };
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
var initialState = { v: "lookup", q: "" };
function readState(raw) {
  if (typeof raw !== "string" || raw.length > 2e3) return initialState;
  try {
    const value = JSON.parse(raw);
    if (!value || Array.isArray(value)) return initialState;
    const q = typeof value.q === "string" ? clip(value.q.trim(), 253) : "";
    const v = ["result", "history"].includes(String(value.v)) ? value.v : "lookup";
    if (v === "result") domainName(q);
    return { v, q, ...value.message === "cleared" ? { message: "cleared" } : {} };
  } catch {
    return initialState;
  }
}
var encodeState = (state) => JSON.stringify(state);
function transition(state, id, values = {}) {
  if (id === "lookup") return { v: "lookup", q: "" };
  if (id === "history") return { v: "history", q: "" };
  if (id === "cleared") return { v: "history", q: "", message: "cleared" };
  if (id === "back") return { v: "lookup", q: state.q };
  if (id.startsWith("domain:")) {
    try {
      return { v: "result", q: domainName(id.slice(7)).ascii };
    } catch {
      return state;
    }
  }
  if (id === "search") {
    let fields2 = values;
    if (typeof values === "string") {
      try {
        fields2 = JSON.parse(values);
      } catch {
        return state;
      }
    }
    if (!fields2 || typeof fields2 !== "object" || Array.isArray(fields2) || typeof fields2.domain !== "string") return state;
    try {
      return { v: "result", q: domainName(fields2.domain).ascii };
    } catch {
      return state;
    }
  }
  return state;
}
async function loadPage(service, history, state, refresh, signal) {
  const request = budget(signal, service.limits.page);
  try {
    if (state.v === "result") {
      const result = await service.lookup(state.q, refresh, request.signal);
      try {
        await history.add(result.domain.ascii);
      } catch {
      }
      return { kind: "result", result };
    }
    if (state.v === "history") return { kind: "history", domains: (await history.load()).domains };
    return { kind: "empty" };
  } catch (error) {
    checkSignal(signal);
    return { kind: "error", error };
  } finally {
    request.dispose();
  }
}
function page(props) {
  const { service, state, loaded } = props, t = textsFor(props.locale ?? service.locale), tz = props.tz ?? "Local";
  const act = (id, next, title) => props.action?.(id, next, title) ?? (next && (props.uiVersion ?? 0) >= 3 ? { id, push: { ui: "lookup", state: encodeState(next), ...title ? { title } : {} } } : { id });
  const text = (key, value, style = "secondary") => /* @__PURE__ */ jsx(Text, { text: clip(value, 1800), style }, key);
  const form = /* @__PURE__ */ jsxs(Section, { title: t.search, children: [
    /* @__PURE__ */ jsx(Field, { name: "domain", label: t.search, kind: "text", placeholder: t.hint, value: state.q }, "domain-input"),
    /* @__PURE__ */ jsx(Button, { title: t.action, action: act("search") }, "domain-submit")
  ] }, "domain-form");
  const nav = /* @__PURE__ */ jsxs(Section, { children: [
    /* @__PURE__ */ jsx(Button, { title: t.lookup, action: act("lookup") }, "domain-home"),
    /* @__PURE__ */ jsx(Button, { title: t.history, action: act("history") }, "domain-history")
  ] }, "domain-navigation");
  if (loaded.kind === "loading") return /* @__PURE__ */ jsxs(View, { title: t.lookup, state: encodeState(state), children: [
    form,
    nav,
    /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(Loading, { title: t.loading }, "domain-loading-indicator") }, "domain-loading")
  ] }, "domains");
  if (loaded.kind === "error") return /* @__PURE__ */ jsxs(View, { title: t.lookup, state: encodeState(state), children: [
    form,
    nav,
    /* @__PURE__ */ jsxs(Section, { children: [
      text("domain-error-message", errorText(loaded.error, props.locale ?? service.locale)),
      /* @__PURE__ */ jsx(Button, { title: t.retry, action: act("refresh") }, "domain-retry")
    ] }, "domain-error")
  ] }, "domains");
  if (state.v === "result") {
    if (loaded.kind !== "result") throw new ReadError("invalid");
    const result = loaded.result;
    return /* @__PURE__ */ jsxs(View, { title: result.domain.unicode, state: encodeState(state), children: [
      form,
      nav,
      /* @__PURE__ */ jsxs(Section, { title: fmt(t.result, { domain: result.domain.unicode }), children: [
        text("registered-name", `${t.registeredName}: ${result.name}`, "body"),
        result.registrar ? text("registrar", `${t.registrar}: ${result.registrar}`, "body") : null
      ] }, "registration"),
      /* @__PURE__ */ jsxs(Section, { title: t.statuses, children: [
        text("registration-status-values", result.statuses.join(" \xB7 ") || t.noStatus, "body"),
        /* @__PURE__ */ jsx(Link, { title: t.statusGlossary, url: statusURL }, "status-glossary")
      ] }, "registration-status"),
      /* @__PURE__ */ jsx(Section, { title: t.events, children: result.events.length ? result.events.map((event, index) => /* @__PURE__ */ jsx(Row, { title: event.action, subtitle: readTime(event.at, tz, props.locale ?? service.locale) }, `event-${index}`)) : text("no-events", t.noEvents) }, "registration-events"),
      /* @__PURE__ */ jsxs(Section, { title: t.source, children: [
        text("source-name", result.source, "body"),
        /* @__PURE__ */ jsx(Link, { title: t.openRecord, url: result.url }, "registration-open"),
        /* @__PURE__ */ jsx(Button, { title: t.lookup, action: act("back") }, "registration-back")
      ] }, "registration-source"),
      /* @__PURE__ */ jsx(Section, { footer: `${fmt(t.readAt, { at: readTime(result.at, tz, props.locale ?? service.locale) })}${result.stale ? ` \xB7 ${errorText(result.stale, props.locale ?? service.locale)}` : ""}`, children: /* @__PURE__ */ jsx(Button, { title: t.refresh, action: act("refresh") }, "registration-refresh") }, "registration-footer"),
      /* @__PURE__ */ jsxs(Section, { children: [
        text("status-note", t.statusHint, "caption"),
        text("registration-attribution", t.attribution, "caption")
      ] }, "registration-notes")
    ] }, "domains");
  }
  if (state.v === "history") {
    if (loaded.kind !== "history") throw new ReadError("invalid");
    return /* @__PURE__ */ jsxs(View, { title: t.history, state: encodeState(state), children: [
      nav,
      state.message === "cleared" ? /* @__PURE__ */ jsx(Section, { children: text("history-cleared-text", t.cleared, "body") }, "history-cleared") : null,
      /* @__PURE__ */ jsxs(Section, { title: t.history, children: [
        loaded.domains.length ? loaded.domains.map((domain) => /* @__PURE__ */ jsx(Row, { title: domain, action: act(`domain:${domain}`, { v: "result", q: domain }, domain) }, `history-${domain}`)) : text("history-empty", t.historyEmpty),
        loaded.domains.length ? /* @__PURE__ */ jsx(Button, { title: t.clearHistory, action: act("clear") }, "history-clear") : null
      ] }, "history-list"),
      /* @__PURE__ */ jsx(Section, { children: text("history-note-text", t.historyHint, "caption") }, "history-note")
    ] }, "domains");
  }
  return /* @__PURE__ */ jsxs(View, { title: t.lookup, state: encodeState(state), children: [
    form,
    nav,
    /* @__PURE__ */ jsx(Section, { children: text("domain-welcome-text", t.attribution, "body") }, "domain-welcome")
  ] }, "domains");
}

// src/root.tsx
function Root(options) {
  const [state, setState] = useState(() => readState(options.state));
  const [refresh, setRefresh] = useState(0);
  const [result, setResult] = useState(void 0);
  const identity = `${state.v}:${state.q}:${refresh}`;
  useLoadEffect(async (signal) => {
    const loaded = await loadPage(options.service, options.history, state, refresh > 0, signal);
    if (!signal.aborted) setResult((previous) => previous?.service === options.service && previous.identity === identity && JSON.stringify(previous.loaded) === JSON.stringify(loaded) ? previous : { service: options.service, identity, loaded });
  }, [options.service, identity]);
  return page({
    ...options,
    state,
    loaded: result?.service === options.service && result.identity === identity ? result.loaded : { kind: "loading" },
    action: (id, next, title) => {
      if (next && (options.uiVersion ?? 0) >= 3) {
        const push = { ui: "lookup", state: encodeState(next), ...title ? { title } : {} };
        return callback(() => ({ push }), { push });
      }
      return callback(async (values) => {
        if (id === "refresh") {
          setRefresh((previous) => previous + 1);
          return;
        }
        if (id === "clear") {
          await options.history.clear();
          setState({ v: "history", q: "", message: "cleared" });
          return;
        }
        if (id.startsWith("domain:")) {
          const domain = id.slice(7);
          if (result?.loaded.kind === "history" && result.loaded.domains.includes(domain)) setState({ v: "result", q: domain });
          return;
        }
        setState((previous) => transition(previous, id, values));
      }, { submit: true });
    }
  });
}
function root(options) {
  return /* @__PURE__ */ jsx(Root, { ...options }, "domains-root");
}

// src/store.ts
var emptyHistory = () => ({ schema_version: 1, domains: [] });
function decodeHistory(value) {
  if (value.length > 87384) throw new ReadError("storage");
  let raw;
  try {
    raw = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(storageValue(value)));
  } catch {
    throw new ReadError("storage");
  }
  if (!raw || typeof raw !== "object" || Array.isArray(raw) || raw.schema_version !== 1 || !Array.isArray(raw.domains)) throw new ReadError("storage");
  const values = raw.domains;
  if (values.length > 20 || new Set(values).size !== values.length) throw new ReadError("storage");
  try {
    return { schema_version: 1, domains: values.map((value2) => domainName(value2).ascii) };
  } catch {
    throw new ReadError("storage");
  }
}
function encodeHistory(history) {
  if (history.schema_version !== 1 || history.domains.length > 20 || new Set(history.domains).size !== history.domains.length) throw new ReadError("storage");
  const value = storageBytes(new TextEncoder().encode(JSON.stringify(history)));
  decodeHistory(value);
  return value;
}
var DomainHistory = class {
  history = emptyHistory();
  loaded = false;
  loading;
  revision;
  serial = Promise.resolve();
  storage;
  constructor(hub) {
    this.storage = new ExtensionStorage(hub);
  }
  snapshot() {
    return structuredClone(this.history);
  }
  async load() {
    if (this.loaded) return this.snapshot();
    if (this.loading) return this.loading;
    this.loading = (async () => {
      try {
        const stored = await this.storage.get("history");
        this.history = decodeHistory(stored.data);
        this.revision = stored.revision;
      } catch (error) {
        if (error?.code !== "not_found") {
          this.loaded = false;
          throw new ReadError("storage");
        }
        this.history = emptyHistory();
        this.revision = void 0;
      }
      this.loaded = true;
      return this.snapshot();
    })();
    try {
      return await this.loading;
    } finally {
      this.loading = void 0;
    }
  }
  mutate(change) {
    const work = this.serial.then(async () => {
      if (!this.loaded) await this.load();
      for (let attempt = 0; attempt < 2; attempt++) {
        const next = { schema_version: 1, domains: change(this.snapshot().domains) };
        if (next.domains.length > 20) throw new ReadError("storage");
        const { request_id } = await this.storage.request();
        try {
          const result = await this.storage.transaction({ request_id, operations: [{ kind: "put", key: "history", revision: this.revision ?? "", data: encodeHistory(next) }] });
          const item = result.items.find((item2) => item2.key === "history");
          if (!item || item.deleted || typeof item.revision !== "string") throw new ReadError("storage");
          this.history = next;
          this.revision = item.revision;
          return;
        } catch (error) {
          if (error?.code !== "storage_conflict" || attempt === 1) throw error;
          this.loaded = false;
          await this.load();
        }
      }
    });
    this.serial = work.catch(() => {
    });
    return work;
  }
  add(domain) {
    const normalized = domainName(domain).ascii;
    return this.mutate((domains) => [normalized, ...domains.filter((item) => item !== normalized)].slice(0, 20));
  }
  clear() {
    return this.mutate(() => []);
  }
};
var MemoryHistory = class {
  history = emptyHistory();
  async load() {
    return structuredClone(this.history);
  }
  async add(domain) {
    const normalized = domainName(domain).ascii;
    this.history = { schema_version: 1, domains: [normalized, ...this.history.domains.filter((item) => item !== normalized)].slice(0, 20) };
  }
  async clear() {
    this.history = emptyHistory();
  }
};

// src/snapshot.ts
function snapshotPage(service, history) {
  const render = async (req, refresh = false) => {
    const state = readState(req.state), loaded = await loadPage(service, history, state, refresh);
    const renderer = new Renderer({ authorize: () => false });
    try {
      return structuredClone(renderer.mount(page({ service, history, state, loaded, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })).view);
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
var sourceManifest = { ...persona_extension_default, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/domains.mjs", sha256: "" } } };
delete sourceManifest.icon;
delete sourceManifest.replaces;
function createProgram(fetch = (input, init) => globalThis.fetch(input, init), options = {}, history) {
  let service;
  let recent;
  const current = () => {
    if (!service || !recent) throw new Error("Domains has not been initialized");
    return { service, history: recent };
  };
  return {
    id: persona_extension_default.id,
    version: persona_extension_default.version,
    manifest: sourceManifest,
    init: async (init, hub) => {
      service = new Domains(fetch, init.config, { ...options, locale: init.hub?.locale ?? options.locale });
      recent = history ?? new DomainHistory(hub);
      if (!history) await recent.load();
    },
    namespaces: { connector: connector(() => current().service, () => current().history) },
    ui: { lookup: componentPage((req) => root({ service: current().service, history: current().history, state: req.state, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })) }
  };
}
var index_default = createProgram();
export {
  DomainHistory,
  Domains,
  MemoryHistory,
  ReadError,
  Root,
  budget,
  callTool,
  connector,
  createProgram,
  decodeHistory,
  index_default as default,
  encodeHistory,
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
