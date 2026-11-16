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
var defaultTimers = { setInterval: (fn, ms) => globalThis.setInterval(fn, ms), clearInterval: (id2) => globalThis.clearInterval(id2) };
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
      const id2 = this.nativeNodes.get(path) ?? globalThis.crypto.randomUUID();
      nativeNodes.set(path, id2);
      return id2;
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
        const id2 = previousActions.get(identity) ?? `cb.${globalThis.crypto.randomUUID()}`;
        const options = lower(cb.options, key, depth + 1, ancestors, pathPart(slot, "options"));
        if (Object.keys(options).some((name) => !["confirm", "submit", "push", "values"].includes(name))) throw new Error("invalid callback action options");
        if (options.push && (options.push.page !== void 0 || Object.keys(options.push).some((name) => !["ui", "state", "title"].includes(name)))) throw new Error("assistant-only action");
        const action = { ...options, id: id2 };
        registrations.set(id2, { identity, key, action, callback: cb });
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
      for (const [controller, id2] of this.activeEvents) if (!registrations.has(id2)) controller.abort();
      for (const instance of this.instances.values()) for (const hook2 of instance.hooks) {
        if (hook2.kind === "effect") {
          if (hook2.snapshot && hook2.changed) this.dirtyLoads.add(hook2);
          else this.dirtyLoads.delete(hook2);
          if (hook2.changed) hook2.stop?.();
        }
      }
      this.registrations = registrations;
      this.callbackIDs = new Map([...registrations].map(([id2, registration]) => [registration.identity, id2]));
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
var noContribution = (id2) => new ExtError(`no contribution ${id2}`, "no_contribution");
var unknownMethod = (method) => new ExtError(`unknown method ${method}`, "unknown_method");
var EXACT_PARAMS = Symbol.for("@persona/extension.exactParams");
function withExactParams(handler) {
  const wrapped = (method, params, call) => handler(method, params, call);
  Object.defineProperty(wrapped, EXACT_PARAMS, { value: true });
  return wrapped;
}

// persona-extension.json
var persona_extension_default = { manifest_version: 1, id: "podcasts", name: "Podcasts \u64AD\u5BA2", version: "0.1.2", icon: "assets/icon.png", description: "\u641C\u7D22\u516C\u5F00\u64AD\u5BA2\uFF0C\u67E5\u770B\u8282\u76EE\u548C\u8FD1\u671F\u5355\u96C6\uFF0C\u628A\u60F3\u542C\u7684\u8282\u76EE\u4FDD\u5B58\u5230\u672C\u5730\u6536\u542C\u6E05\u5355\u3002\u901A\u8FC7 Apple \u516C\u5F00\u76EE\u5F55\u83B7\u53D6\u5143\u6570\u636E\uFF0C\u65E0\u9700\u8D26\u53F7\uFF1B\u4E0D\u4E0B\u8F7D\u5A92\u4F53\uFF0C\u4E0D\u63A7\u5236\u64AD\u653E\u3002", publisher: "nwverse", homepage: "https://github.com/nwverse/persona-extensions/blob/main/extensions/podcasts/README.md", tags: ["\u64AD\u5BA2", "\u5355\u96C6", "\u6536\u542C\u6E05\u5355", "Apple Podcasts"], engines: { persona: ">=202611101800", extension_protocol: 1 }, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/podcasts.mjs", sha256: "" } }, contributes: { connectors: [{ id: "podcasts", name: "Podcasts", capabilities: ["status", "tools"], hosts: ["itunes.apple.com"], agent_tools: ["podcasts_*"] }], ui: [{ id: "discover", title: "\u53D1\u73B0\u64AD\u5BA2", placement: "nav_page", icon: "music" }] }, permissions: { network: ["itunes.apple.com"], storage: { keys: ["subscriptions"] } } };

// src/model.ts
var limits = Object.freeze({ request: 12e3, page: 18e3, tool: 18e3, response: 2 << 20, fresh: 15 * 6e4, cache: 48, cacheBytes: 2 << 20, gap: 350 });
var ReadError = class extends Error {
  constructor(code, status) {
    super(code);
    this.code = code;
    this.status = status;
  }
};
var clip = (text2, most) => Array.from(text2).length <= most ? text2 : Array.from(text2).slice(0, most).join("").trimEnd() + "\u2026";
var plain = (text2) => text2.replace(/\s+/gu, " ").trim();
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
function id(value) {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 1 || value > 2e12) throw new ReadError("bad_answer");
  return value;
}
function podcastID(value) {
  const text2 = typeof value === "number" ? value : typeof value === "string" && /^[1-9][0-9]{0,12}$/.test(value) ? Number(value) : NaN;
  if (!Number.isSafeInteger(text2) || text2 < 1 || text2 > 2e12) throw new ReadError("invalid");
  return text2;
}
function publicURL(value, allowed) {
  if (value == null) return void 0;
  if (typeof value !== "string" || value.length > 2048 || /[\u0000- \u007f]/u.test(value)) throw new ReadError("bad_answer");
  let url;
  try {
    url = new URL(value);
  } catch {
    throw new ReadError("bad_answer");
  }
  if (url.protocol !== "https:" || url.username || url.password || url.port) throw new ReadError("bad_answer");
  const host = url.hostname.toLowerCase();
  if (allowed === "apple") {
    if (host !== "podcasts.apple.com" || !/^\/[a-z]{2}(?:-[a-z]{2})?\/podcast\//i.test(url.pathname)) throw new ReadError("bad_answer");
    url.search = "";
    url.hash = "";
  }
  return url.toString();
}
function text(value, most) {
  return clip(plain(string(value)), most);
}
function date(value) {
  if (value == null) return void 0;
  const raw = string(value);
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,3})?Z$/.test(raw) || !Number.isFinite(Date.parse(raw))) throw new ReadError("bad_answer");
  const [y, m, d] = raw.slice(0, 10).split("-").map(Number), parsed = new Date(raw);
  if (parsed.getUTCFullYear() !== y || parsed.getUTCMonth() !== m - 1 || parsed.getUTCDate() !== d) throw new ReadError("bad_answer");
  return parsed.toISOString();
}
function queryText(value) {
  if (typeof value !== "string") throw new ReadError("invalid");
  const text2 = plain(value);
  if (!text2 || Array.from(text2).length > 200 || /[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  return text2;
}
function searchPath(query, limit2 = 20) {
  if (!Number.isSafeInteger(limit2) || limit2 < 1 || limit2 > 30) throw new ReadError("invalid");
  return `/search?${new URLSearchParams({ term: queryText(query), media: "podcast", entity: "podcast", limit: String(limit2) })}`;
}
function lookupPath(show, limit2 = 20) {
  if (!Number.isSafeInteger(limit2) || limit2 < 1 || limit2 > 30) throw new ReadError("invalid");
  return `/lookup?${new URLSearchParams({ id: String(podcastID(show)), entity: "podcastEpisode", limit: String(limit2), sort: "recent" })}`;
}
function showPath(show) {
  return `/lookup?${new URLSearchParams({ id: String(podcastID(show)), entity: "podcast", limit: "1" })}`;
}
function canonicalShowURL(show) {
  return `https://podcasts.apple.com/us/podcast/id${podcastID(show)}`;
}
function canonicalEpisodeURL(show, episode) {
  return `${canonicalShowURL(show)}?i=${podcastID(episode)}`;
}
function parseShow(raw) {
  const value = object(raw);
  if (value.wrapperType !== "track" || value.kind !== "podcast") throw new ReadError("bad_answer");
  const show = id(value.collectionId), title = text(value.collectionName, 500);
  if (!title) throw new ReadError("bad_answer");
  const url = canonicalShowURL(show);
  if (value.collectionViewUrl != null) publicURL(value.collectionViewUrl, "apple");
  const episodes = value.trackCount == null ? void 0 : value.trackCount;
  if (episodes !== void 0 && (typeof value.trackCount !== "number" || !Number.isSafeInteger(episodes) || episodes < 0 || episodes > 1e6)) throw new ReadError("bad_answer");
  return {
    id: show,
    title,
    publisher: text(value.artistName, 300),
    genre: text(value.primaryGenreName, 100),
    ...episodes === void 0 ? {} : { episodes },
    url,
    feedURL: publicURL(value.feedUrl, "feed")
  };
}
function parseEpisode(raw, show) {
  const value = object(raw);
  if (value.wrapperType !== "podcastEpisode" || value.kind !== "podcast-episode" || id(value.collectionId) !== show.id) throw new ReadError("bad_answer");
  const episode = id(value.trackId), title = text(value.trackName, 500);
  if (!title) throw new ReadError("bad_answer");
  const milliseconds = value.trackTimeMillis;
  if (milliseconds != null && (typeof milliseconds !== "number" || !Number.isSafeInteger(milliseconds) || milliseconds < 0 || milliseconds > 7 * 24 * 60 * 6e4)) throw new ReadError("bad_answer");
  return {
    id: episode,
    title,
    ...value.releaseDate == null ? {} : { at: date(value.releaseDate) },
    ...milliseconds == null ? {} : { minutes: Math.round(milliseconds / 6e4) },
    description: text(value.description, 8e3),
    url: canonicalEpisodeURL(show.id, episode),
    showURL: show.url
  };
}
function parseSearch(raw, at) {
  const value = object(raw), count = value.resultCount;
  if (typeof count !== "number" || !Number.isSafeInteger(count) || count < 0 || count > 200) throw new ReadError("bad_answer");
  const shows = array(value.results).slice(0, 30).filter((item) => object(item).wrapperType === "track").map(parseShow);
  const unique = shows.filter((show, index) => shows.findIndex((other) => other.id === show.id) === index);
  return { shows: unique, at };
}
function parseEpisodes(raw, expected, at) {
  const showID = podcastID(expected), value = object(raw), results = array(value.results).slice(0, 50);
  const shows = results.filter((item) => object(item).wrapperType === "track").map(parseShow).filter((show) => show.id === showID);
  if (shows.length !== 1) throw new ReadError("not_found");
  const episodes = results.filter((item) => object(item).wrapperType === "podcastEpisode").map((item) => parseEpisode(item, shows[0]));
  const unique = episodes.filter((episode, index) => episodes.findIndex((other) => other.id === episode.id) === index);
  return { show: shows[0], episodes: unique, at };
}
function parseStoredShow(raw) {
  try {
    const value = object(raw), show = parseShow({
      wrapperType: "track",
      kind: "podcast",
      collectionId: value.id,
      collectionName: value.title,
      artistName: value.publisher,
      primaryGenreName: value.genre,
      trackCount: value.episodes,
      feedUrl: value.feedURL
    });
    if (show.title !== value.title || show.publisher !== value.publisher || show.genre !== value.genre || show.feedURL !== value.feedURL || show.episodes !== value.episodes || show.url !== value.url || show.id !== value.id) return void 0;
    return show;
  } catch {
    return void 0;
  }
}

// src/locales/en.json
var en_default = {
  account: "Apple public podcast directory \xB7 no account",
  discover: "Discover podcasts",
  library: "Listening list",
  search: "Find a podcast",
  searchHint: "Show, publisher or topic",
  searchAction: "Search",
  results: "Results for \u201C{query}\u201D",
  empty: "No shows found. Try another name or topic.",
  episodes: "Recent episodes",
  episodeCount: "{count} episodes in the directory",
  noEpisodes: "No recent episodes are listed.",
  back: "Back to results",
  backDiscover: "Discover",
  save: "Save to listening list",
  saved: "On listening list",
  remove: "Remove from listening list",
  savedMessage: "Saved to your listening list.",
  removedMessage: "Removed from your listening list.",
  libraryEmpty: "Save a show to find it here later.",
  libraryHint: "Your list stays in this Persona installation. It does not subscribe in Apple Podcasts or download episodes.",
  openShow: "Open in Apple Podcasts",
  openFeed: "Open publisher RSS feed",
  feedHint: "The feed is supplied by the publisher. Add it to your preferred podcast player if you trust it.",
  refresh: "Refresh",
  retry: "Try again",
  loading: "Loading the podcast directory\u2026",
  unknownDate: "Date not supplied",
  minutes: "{count} min",
  readAt: "Directory read {at}",
  attribution: "Show and episode metadata: Apple public podcast directory. Persona does not host or download the audio.",
  toolSearch: "Search the public Apple podcast directory for shows by name, publisher or topic.",
  toolEpisodes: "Read recent episode titles, dates, durations and descriptions for a podcast directory ID.",
  query: "Show, publisher or topic",
  limit: "Maximum results, from 1 to 30",
  show: "Numeric Apple podcast collection ID from podcasts_search",
  invalid: "Check the search or podcast ID and try again.",
  bad_answer: "The directory returned an incomplete or unexpected answer.",
  not_found: "That podcast is no longer in the directory.",
  rate_limited: "The directory is busy. Please try again shortly.",
  slow: "The directory took too long to answer.",
  network: "The podcast directory could not be reached.",
  http: "The directory returned HTTP {0}.",
  storage: "Your listening list is unavailable. No change was made.",
  storageUnavailable: "Listening-list storage needs a current Persona hub with installation-owned storage enabled."
};

// src/locales/zh-Hans.json
var zh_Hans_default = {
  account: "Apple \u516C\u5F00\u64AD\u5BA2\u76EE\u5F55 \xB7 \u65E0\u9700\u8D26\u53F7",
  discover: "\u53D1\u73B0\u64AD\u5BA2",
  library: "\u6536\u542C\u6E05\u5355",
  search: "\u641C\u7D22\u64AD\u5BA2",
  searchHint: "\u8282\u76EE\u3001\u4E3B\u64AD\u6216\u8BDD\u9898",
  searchAction: "\u641C\u7D22",
  results: "\u201C{query}\u201D\u7684\u641C\u7D22\u7ED3\u679C",
  empty: "\u6CA1\u6709\u627E\u5230\u8282\u76EE\uFF0C\u6362\u4E2A\u540D\u5B57\u6216\u8BDD\u9898\u8BD5\u8BD5\u3002",
  episodes: "\u8FD1\u671F\u5355\u96C6",
  episodeCount: "\u76EE\u5F55\u6536\u5F55 {count} \u96C6",
  noEpisodes: "\u76EE\u5F55\u8FD8\u6CA1\u6709\u5217\u51FA\u8FD1\u671F\u5355\u96C6\u3002",
  back: "\u8FD4\u56DE\u641C\u7D22\u7ED3\u679C",
  backDiscover: "\u53D1\u73B0",
  save: "\u4FDD\u5B58\u5230\u6536\u542C\u6E05\u5355",
  saved: "\u5DF2\u5728\u6536\u542C\u6E05\u5355",
  remove: "\u4ECE\u6536\u542C\u6E05\u5355\u79FB\u9664",
  savedMessage: "\u5DF2\u4FDD\u5B58\u5230\u6536\u542C\u6E05\u5355\u3002",
  removedMessage: "\u5DF2\u4ECE\u6536\u542C\u6E05\u5355\u79FB\u9664\u3002",
  libraryEmpty: "\u4FDD\u5B58\u60F3\u542C\u7684\u8282\u76EE\uFF0C\u4E0B\u6B21\u4ECE\u8FD9\u91CC\u7EE7\u7EED\u3002",
  libraryHint: "\u6E05\u5355\u53EA\u4FDD\u5B58\u5728\u5F53\u524D Persona \u5B89\u88C5\u4E2D\uFF0C\u4E0D\u4F1A\u66FF\u4F60\u8BA2\u9605 Apple Podcasts\uFF0C\u4E5F\u4E0D\u4F1A\u4E0B\u8F7D\u5355\u96C6\u3002",
  openShow: "\u5728 Apple Podcasts \u4E2D\u6253\u5F00",
  openFeed: "\u6253\u5F00\u53D1\u5E03\u8005 RSS \u8BA2\u9605\u6E90",
  feedHint: "\u8BA2\u9605\u6E90\u7531\u53D1\u5E03\u8005\u63D0\u4F9B\u3002\u786E\u8BA4\u53EF\u4FE1\u540E\uFF0C\u53EF\u6DFB\u52A0\u5230\u4F60\u5E38\u7528\u7684\u64AD\u5BA2\u64AD\u653E\u5668\u3002",
  refresh: "\u5237\u65B0",
  retry: "\u91CD\u8BD5",
  loading: "\u6B63\u5728\u8BFB\u53D6\u64AD\u5BA2\u76EE\u5F55\u2026",
  unknownDate: "\u672A\u63D0\u4F9B\u65E5\u671F",
  minutes: "{count} \u5206\u949F",
  readAt: "\u76EE\u5F55\u8BFB\u53D6\u4E8E {at}",
  attribution: "\u8282\u76EE\u548C\u5355\u96C6\u5143\u6570\u636E\u6765\u81EA Apple \u516C\u5F00\u64AD\u5BA2\u76EE\u5F55\u3002Persona \u4E0D\u6258\u7BA1\u6216\u4E0B\u8F7D\u97F3\u9891\u3002",
  toolSearch: "\u901A\u8FC7 Apple \u516C\u5F00\u64AD\u5BA2\u76EE\u5F55\uFF0C\u6309\u8282\u76EE\u540D\u3001\u4E3B\u64AD\u6216\u8BDD\u9898\u641C\u7D22\u64AD\u5BA2\u3002",
  toolEpisodes: "\u6309\u64AD\u5BA2\u76EE\u5F55 ID \u67E5\u770B\u8FD1\u671F\u5355\u96C6\u7684\u6807\u9898\u3001\u65E5\u671F\u3001\u65F6\u957F\u548C\u7B80\u4ECB\u3002",
  query: "\u8282\u76EE\u3001\u4E3B\u64AD\u6216\u8BDD\u9898",
  limit: "\u6700\u591A\u8FD4\u56DE\u591A\u5C11\u6761\uFF0C1 \u5230 30",
  show: "podcasts_search \u8FD4\u56DE\u7684\u6570\u5B57 Apple \u64AD\u5BA2\u76EE\u5F55 ID",
  invalid: "\u8BF7\u68C0\u67E5\u641C\u7D22\u5185\u5BB9\u6216\u64AD\u5BA2 ID\u3002",
  bad_answer: "\u76EE\u5F55\u8FD4\u56DE\u4E86\u4E0D\u5B8C\u6574\u6216\u65E0\u6CD5\u8BC6\u522B\u7684\u5185\u5BB9\u3002",
  not_found: "\u76EE\u5F55\u91CC\u627E\u4E0D\u5230\u8FD9\u4E2A\u64AD\u5BA2\u3002",
  rate_limited: "\u76EE\u5F55\u6682\u65F6\u7E41\u5FD9\uFF0C\u8BF7\u7A0D\u540E\u91CD\u8BD5\u3002",
  slow: "\u76EE\u5F55\u54CD\u5E94\u8D85\u65F6\u3002",
  network: "\u6682\u65F6\u65E0\u6CD5\u8FDE\u63A5\u64AD\u5BA2\u76EE\u5F55\u3002",
  http: "\u76EE\u5F55\u8FD4\u56DE HTTP {0}\u3002",
  storage: "\u6536\u542C\u6E05\u5355\u6682\u65F6\u4E0D\u53EF\u7528\uFF0C\u6CA1\u6709\u4F5C\u51FA\u4FEE\u6539\u3002",
  storageUnavailable: "\u6536\u542C\u6E05\u5355\u9700\u8981\u652F\u6301\u6269\u5C55\u79C1\u6709\u5B58\u50A8\u7684\u65B0\u7248 Persona hub\uFF0C\u5E76\u6388\u4E88\u76F8\u5E94\u5B58\u50A8\u6743\u9650\u3002"
};

// src/locales/ja.json
var ja_default = {
  account: "Apple \u516C\u958B\u30DD\u30C3\u30C9\u30AD\u30E3\u30B9\u30C8\u30C7\u30A3\u30EC\u30AF\u30C8\u30EA \xB7 \u30A2\u30AB\u30A6\u30F3\u30C8\u4E0D\u8981",
  discover: "\u30DD\u30C3\u30C9\u30AD\u30E3\u30B9\u30C8\u3092\u63A2\u3059",
  library: "\u518D\u751F\u30EA\u30B9\u30C8",
  search: "\u756A\u7D44\u3092\u691C\u7D22",
  searchHint: "\u756A\u7D44\u3001\u914D\u4FE1\u8005\u3001\u8A71\u984C",
  searchAction: "\u691C\u7D22",
  results: "\u300C{query}\u300D\u306E\u691C\u7D22\u7D50\u679C",
  empty: "\u756A\u7D44\u304C\u898B\u3064\u304B\u308A\u307E\u305B\u3093\u3002\u5225\u306E\u540D\u524D\u3084\u8A71\u984C\u3092\u304A\u8A66\u3057\u304F\u3060\u3055\u3044\u3002",
  episodes: "\u6700\u8FD1\u306E\u30A8\u30D4\u30BD\u30FC\u30C9",
  episodeCount: "\u30C7\u30A3\u30EC\u30AF\u30C8\u30EA\u306B {count} \u4EF6\u306E\u30A8\u30D4\u30BD\u30FC\u30C9",
  noEpisodes: "\u6700\u8FD1\u306E\u30A8\u30D4\u30BD\u30FC\u30C9\u306F\u307E\u3060\u3042\u308A\u307E\u305B\u3093\u3002",
  back: "\u691C\u7D22\u7D50\u679C\u306B\u623B\u308B",
  backDiscover: "\u898B\u3064\u3051\u308B",
  save: "\u518D\u751F\u30EA\u30B9\u30C8\u306B\u4FDD\u5B58",
  saved: "\u518D\u751F\u30EA\u30B9\u30C8\u306B\u4FDD\u5B58\u6E08\u307F",
  remove: "\u518D\u751F\u30EA\u30B9\u30C8\u304B\u3089\u524A\u9664",
  savedMessage: "\u518D\u751F\u30EA\u30B9\u30C8\u306B\u4FDD\u5B58\u3057\u307E\u3057\u305F\u3002",
  removedMessage: "\u518D\u751F\u30EA\u30B9\u30C8\u304B\u3089\u524A\u9664\u3057\u307E\u3057\u305F\u3002",
  libraryEmpty: "\u6C17\u306B\u306A\u308B\u756A\u7D44\u3092\u4FDD\u5B58\u3059\u308B\u3068\u3001\u3053\u3053\u304B\u3089\u518D\u958B\u3067\u304D\u307E\u3059\u3002",
  libraryHint: "\u30EA\u30B9\u30C8\u306F\u3053\u306E Persona \u30A4\u30F3\u30B9\u30C8\u30FC\u30EB\u306B\u306E\u307F\u4FDD\u5B58\u3055\u308C\u307E\u3059\u3002Apple Podcasts \u3067\u8CFC\u8AAD\u3057\u305F\u308A\u3001\u30A8\u30D4\u30BD\u30FC\u30C9\u3092\u30C0\u30A6\u30F3\u30ED\u30FC\u30C9\u3057\u305F\u308A\u3057\u307E\u305B\u3093\u3002",
  openShow: "Apple Podcasts \u3067\u958B\u304F",
  openFeed: "\u914D\u4FE1\u8005\u306E RSS \u30D5\u30A3\u30FC\u30C9\u3092\u958B\u304F",
  feedHint: "\u30D5\u30A3\u30FC\u30C9\u306F\u914D\u4FE1\u8005\u304C\u63D0\u4F9B\u3057\u3066\u3044\u307E\u3059\u3002\u4FE1\u983C\u3067\u304D\u308B\u3053\u3068\u3092\u78BA\u8A8D\u3057\u3001\u666E\u6BB5\u306E\u30DD\u30C3\u30C9\u30AD\u30E3\u30B9\u30C8\u30D7\u30EC\u30FC\u30E4\u30FC\u306B\u8FFD\u52A0\u3057\u3066\u304F\u3060\u3055\u3044\u3002",
  refresh: "\u66F4\u65B0",
  retry: "\u518D\u8A66\u884C",
  loading: "\u30DD\u30C3\u30C9\u30AD\u30E3\u30B9\u30C8\u30C7\u30A3\u30EC\u30AF\u30C8\u30EA\u3092\u8AAD\u307F\u8FBC\u3093\u3067\u3044\u307E\u3059\u2026",
  unknownDate: "\u65E5\u4ED8\u306A\u3057",
  minutes: "{count} \u5206",
  readAt: "\u30C7\u30A3\u30EC\u30AF\u30C8\u30EA\u53D6\u5F97\u65E5\u6642 {at}",
  attribution: "\u756A\u7D44\u3068\u30A8\u30D4\u30BD\u30FC\u30C9\u306E\u30E1\u30BF\u30C7\u30FC\u30BF\u306F Apple \u516C\u958B\u30DD\u30C3\u30C9\u30AD\u30E3\u30B9\u30C8\u30C7\u30A3\u30EC\u30AF\u30C8\u30EA\u304B\u3089\u53D6\u5F97\u3057\u307E\u3059\u3002Persona \u306F\u97F3\u58F0\u3092\u30DB\u30B9\u30C8\u307E\u305F\u306F\u30C0\u30A6\u30F3\u30ED\u30FC\u30C9\u3057\u307E\u305B\u3093\u3002",
  toolSearch: "Apple \u516C\u958B\u30DD\u30C3\u30C9\u30AD\u30E3\u30B9\u30C8\u30C7\u30A3\u30EC\u30AF\u30C8\u30EA\u3067\u3001\u756A\u7D44\u540D\u3001\u914D\u4FE1\u8005\u3001\u8A71\u984C\u304B\u3089\u756A\u7D44\u3092\u691C\u7D22\u3057\u307E\u3059\u3002",
  toolEpisodes: "\u30DD\u30C3\u30C9\u30AD\u30E3\u30B9\u30C8\u306E\u30C7\u30A3\u30EC\u30AF\u30C8\u30EA ID \u3092\u4F7F\u3044\u3001\u6700\u8FD1\u306E\u30A8\u30D4\u30BD\u30FC\u30C9\u306E\u30BF\u30A4\u30C8\u30EB\u3001\u65E5\u4ED8\u3001\u9577\u3055\u3001\u8AAC\u660E\u3092\u8AAD\u307F\u307E\u3059\u3002",
  query: "\u756A\u7D44\u3001\u914D\u4FE1\u8005\u3001\u8A71\u984C",
  limit: "\u6700\u5927\u7D50\u679C\u6570\uFF081\uFF5E30\uFF09",
  show: "podcasts_search \u304C\u8FD4\u3059 Apple \u30DD\u30C3\u30C9\u30AD\u30E3\u30B9\u30C8\u306E\u6570\u5024\u30B3\u30EC\u30AF\u30B7\u30E7\u30F3 ID",
  invalid: "\u691C\u7D22\u8A9E\u307E\u305F\u306F\u30DD\u30C3\u30C9\u30AD\u30E3\u30B9\u30C8 ID \u3092\u78BA\u8A8D\u3057\u3066\u304F\u3060\u3055\u3044\u3002",
  bad_answer: "\u30C7\u30A3\u30EC\u30AF\u30C8\u30EA\u306E\u5FDC\u7B54\u304C\u4E0D\u5B8C\u5168\u304B\u3001\u5F62\u5F0F\u304C\u4E0D\u6B63\u3067\u3059\u3002",
  not_found: "\u3053\u306E\u30DD\u30C3\u30C9\u30AD\u30E3\u30B9\u30C8\u306F\u30C7\u30A3\u30EC\u30AF\u30C8\u30EA\u306B\u3042\u308A\u307E\u305B\u3093\u3002",
  rate_limited: "\u30C7\u30A3\u30EC\u30AF\u30C8\u30EA\u304C\u6DF7\u96D1\u3057\u3066\u3044\u307E\u3059\u3002\u3057\u3070\u3089\u304F\u3057\u3066\u304B\u3089\u304A\u8A66\u3057\u304F\u3060\u3055\u3044\u3002",
  slow: "\u30C7\u30A3\u30EC\u30AF\u30C8\u30EA\u306E\u5FDC\u7B54\u304C\u30BF\u30A4\u30E0\u30A2\u30A6\u30C8\u3057\u307E\u3057\u305F\u3002",
  network: "\u30DD\u30C3\u30C9\u30AD\u30E3\u30B9\u30C8\u30C7\u30A3\u30EC\u30AF\u30C8\u30EA\u306B\u63A5\u7D9A\u3067\u304D\u307E\u305B\u3093\u3002",
  http: "\u30C7\u30A3\u30EC\u30AF\u30C8\u30EA\u304C HTTP {0} \u3092\u8FD4\u3057\u307E\u3057\u305F\u3002",
  storage: "\u518D\u751F\u30EA\u30B9\u30C8\u3092\u5229\u7528\u3067\u304D\u306A\u3044\u305F\u3081\u3001\u5909\u66F4\u3057\u307E\u305B\u3093\u3067\u3057\u305F\u3002",
  storageUnavailable: "\u518D\u751F\u30EA\u30B9\u30C8\u306B\u306F\u3001\u30A4\u30F3\u30B9\u30C8\u30FC\u30EB\u5C02\u7528\u30B9\u30C8\u30EC\u30FC\u30B8\u3092\u5099\u3048\u305F\u6700\u65B0\u306E Persona hub \u3068\u3001\u305D\u306E\u6A29\u9650\u304C\u5FC5\u8981\u3067\u3059\u3002"
};

// src/i18n.ts
var textsFor = (locale = "") => locale.toLowerCase().startsWith("ja") ? ja_default : locale.toLowerCase().startsWith("zh") ? zh_Hans_default : en_default;
var fmt = (text2, values) => text2.replace(/\{(\w+)\}/g, (_, key) => String(values[key] ?? ""));
function errorText(error, locale = "") {
  const t = textsFor(locale);
  if (!(error instanceof ReadError)) return t.network;
  return error.code === "http" ? fmt(t.http, { 0: error.status ?? 0 }) : t[error.code];
}
function readTime(at, timeZone = "Local", locale = "en") {
  return new Intl.DateTimeFormat(locale, { year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit", timeZone: timeZone === "Local" ? void 0 : timeZone }).format(new Date(at));
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
var Podcasts = class {
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
    if (item && this.monotonic() - item.at < fresh) {
      this.cache.delete(path);
      item.used = this.monotonic();
      this.cache.set(path, item);
      return { raw: item.raw, at: item.at };
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
      const oldest = this.cache.keys().next().value;
      if (oldest === void 0) break;
      this.bytes -= this.cache.get(oldest).bytes;
      this.cache.delete(oldest);
    }
  }
  async response(path, signal) {
    let response;
    try {
      response = await this.fetch(`https://itunes.apple.com${path}`, {
        method: "GET",
        headers: { Accept: "application/json", "User-Agent": "persona-podcasts/0.1.0" },
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
        validate(raw);
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
  async search(query, limit2 = 20, refresh = false, signal) {
    const rd = await this.read(searchPath(query, limit2), refresh ? 0 : this.limits.fresh, signal, (raw) => {
      parseSearch(raw, 0);
    });
    return { ...parseSearch(rd.raw, rd.at), stale: rd.stale };
  }
  async episodes(show, limit2 = 20, refresh = false, signal) {
    const rd = await this.read(lookupPath(show, limit2), refresh ? 0 : this.limits.fresh, signal, (raw) => {
      parseEpisodes(raw, show, 0);
    });
    return { ...parseEpisodes(rd.raw, show, rd.at), stale: rd.stale };
  }
  async show(show, signal) {
    const path = showPath(show), rd = await this.read(path, this.limits.fresh, signal, (raw) => {
      const results2 = raw.results;
      const item2 = results2?.find((raw2) => !!raw2 && typeof raw2 === "object" && raw2.wrapperType === "track");
      if (!item2) throw new ReadError("not_found");
      if (id(item2.collectionId) !== podcastID(show)) throw new ReadError("not_found");
      parseShow(item2);
    });
    const results = rd.raw.results;
    const item = results?.find((raw) => !!raw && typeof raw === "object" && raw.wrapperType === "track");
    if (!item) throw new ReadError("not_found");
    return parseShow(item);
  }
  cacheStats() {
    return { entries: this.cache.size, bytes: this.bytes };
  }
};

// src/tools.ts
function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [
    { name: "podcasts_search", description: t.toolSearch, input_schema: { type: "object", required: ["query"], properties: { query: { type: "string", description: t.query }, limit: { type: "integer", minimum: 1, maximum: 30, description: t.limit } } } },
    { name: "podcasts_episodes", description: t.toolEpisodes, input_schema: { type: "object", required: ["show"], properties: { show: { type: "integer", minimum: 1, maximum: 2e12, description: t.show }, limit: { type: "integer", minimum: 1, maximum: 30, description: t.limit } } } }
  ];
}
var tools = toolsFor();
function showLine(show, index) {
  return `${index + 1}. ${clip(show.title, 300)}
   ${[show.publisher, show.genre, show.episodes === void 0 ? "" : `${show.episodes} episodes`].filter(Boolean).join(" \xB7 ")}
   ${show.url}
   Podcast ID: ${show.id}${show.feedURL ? `
   Publisher RSS: ${show.feedURL}` : ""}`;
}
function episodeLine(episode, index) {
  return `${index + 1}. ${clip(episode.title, 300)}
   ${[episode.at ?? "Date not supplied", episode.minutes === void 0 ? "" : `${episode.minutes} min`].filter(Boolean).join(" \xB7 ")}
   ${episode.url}
   Episode ID: ${episode.id}${episode.description ? `
   ${clip(episode.description, 2e3)}` : ""}`;
}
function argsFrom(raw, exact) {
  if (exact) {
    const args = {};
    for (const token of exact.fieldValues("query")) {
      const value2 = JSON.parse(token.raw);
      if (typeof value2 !== "string") throw new ReadError("invalid");
      args.query = value2;
    }
    const parsed = exact.int64Fields(["limit", "show"]);
    if (parsed.limit !== 0n) {
      if (parsed.limit < 1n || parsed.limit > 30n) throw new ReadError("invalid");
      args.limit = Number(parsed.limit);
    }
    if (parsed.show !== 0n) {
      if (parsed.show < 1n || parsed.show > 2000000000000n) throw new ReadError("invalid");
      args.show = Number(parsed.show);
    }
    return args;
  }
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = raw;
  if (value.query != null && typeof value.query !== "string") throw new ReadError("invalid");
  for (const key of ["limit", "show"]) if (value[key] != null && (typeof value[key] !== "number" || !Number.isSafeInteger(value[key]))) throw new ReadError("invalid");
  return value;
}
function limit(value) {
  const result = value == null ? 10 : Number(value);
  if (!Number.isSafeInteger(result) || result < 1 || result > 30) throw new ReadError("invalid");
  return result;
}
async function callTool(service, name, raw = {}, signal, exact) {
  const args = argsFrom(raw, exact), t = textsFor(service.locale), request = budget(signal, service.limits.tool);
  try {
    if (name === "podcasts_search") {
      if (typeof args.query !== "string") throw new ReadError("invalid");
      const result = await service.search(args.query, limit(args.limit), false, request.signal);
      const stale = result.stale ? `
${errorText(result.stale, service.locale)} \xB7 ${fmt(t.readAt, { at: readTime(result.at, "Local", service.locale) })}` : "";
      return clip((result.shows.length ? result.shows.map(showLine).join("\n").trim() : fmt(t.results, { query: args.query }) + "\n" + t.empty) + stale, 29999);
    }
    if (name === "podcasts_episodes") {
      const show = podcastID(args.show), result = await service.episodes(show, limit(args.limit), false, request.signal);
      const prefix = `${result.show.title}
${result.show.publisher}
${result.show.url}

`;
      return clip(prefix + (result.episodes.length ? result.episodes.map(episodeLine).join("\n") : t.noEpisodes) + (result.stale ? `
${errorText(result.stale, service.locale)} \xB7 ${fmt(t.readAt, { at: readTime(result.at, "Local", service.locale) })}` : ""), 29999);
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
    if (p.contribution && p.contribution !== "podcasts") throw noContribution(String(p.contribution));
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
var initialState = { v: "discover", q: "", k: 0 };
function readState(raw) {
  if (typeof raw !== "string" || raw.length > 8e3) return initialState;
  try {
    const value = JSON.parse(raw);
    if (!value || Array.isArray(value)) return initialState;
    const k = value.k === 0 ? 0 : podcastID(value.k);
    const state = {
      v: ["search", "show", "library"].includes(String(value.v)) ? value.v : "discover",
      q: typeof value.q === "string" ? clip(value.q.trim(), 200) : "",
      k,
      ...value.from === "library" ? { from: "library" } : {},
      ...value.b === void 0 ? {} : { b: parseStoredShow(value.b) },
      ...typeof value.message === "string" && ["saved", "removed"].includes(value.message) ? { message: value.message } : {}
    };
    if (state.v === "show" && (!state.k || !state.b) || state.v === "search" && !state.q) return initialState;
    return state;
  } catch {
    return initialState;
  }
}
var encodeState = (state) => JSON.stringify(state);
function transition(state, id2, values = {}) {
  const [kind, argument] = id2.split(":", 2);
  if (kind === "show" && argument && state.b?.id === Number(argument)) return { ...state, v: "show", k: state.b.id, message: void 0 };
  if (kind === "saved" && argument && state.b?.id === Number(argument)) return { ...state, message: "saved" };
  if (kind === "removed" && argument && state.b?.id === Number(argument)) return { ...state, message: "removed" };
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
  if (kind === "search") {
    const raw = typeof fields2.query === "string" ? fields2.query : "";
    const query = clip(raw.trim(), 200);
    return query ? { v: "search", q: query, k: 0 } : state;
  }
  if (kind === "library") return { ...state, v: "library", k: 0, from: void 0, b: void 0 };
  if (kind === "back") return state.from === "library" ? { v: "library", q: "", k: 0 } : state.q ? { v: "search", q: state.q, k: 0 } : initialState;
  if (kind === "home") return initialState;
  return state;
}
async function loadPage(service, store, state, refresh, signal) {
  const request = budget(signal, service.limits.page);
  try {
    if (state.v === "show") return { kind: "show", result: await service.episodes(state.k, 20, refresh, request.signal) };
    if (state.v === "search") return { kind: "search", result: await service.search(state.q, 20, refresh, request.signal) };
    if (state.v === "library") return { kind: "library", shows: (await store.load()).shows };
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
  const act = (id2, next, title) => props.action?.(id2, next, title) ?? (next && uiVersion >= 3 ? { id: id2, push: { ui: "discover", state: encodeState(next), ...title ? { title } : {} } } : { id: id2 });
  const text2 = (key, value, style = "secondary") => /* @__PURE__ */ jsx(Text, { text: clip(value, 1800), style }, key);
  const search = /* @__PURE__ */ jsxs(Section, { title: t.search, children: [
    /* @__PURE__ */ jsx(Field, { name: "query", label: t.search, kind: "text", placeholder: t.searchHint, value: state.q }, "podcast-query"),
    /* @__PURE__ */ jsx(Button, { title: t.searchAction, action: act("search") }, "podcast-search-submit")
  ] }, "podcast-search");
  const navigation = /* @__PURE__ */ jsxs(Section, { children: [
    /* @__PURE__ */ jsx(Button, { title: t.discover, action: act("home") }, "podcast-home"),
    /* @__PURE__ */ jsx(Button, { title: t.library, action: act("library") }, "podcast-library")
  ] }, "podcast-navigation");
  const message = state.message ? /* @__PURE__ */ jsx(Section, { children: text2("podcast-message-text", state.message === "saved" ? t.savedMessage : t.removedMessage, "body") }, "podcast-message") : null;
  if (loaded.kind === "loading") return /* @__PURE__ */ jsxs(View, { title: t.discover, state: encodeState(state), children: [
    search,
    navigation,
    /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(Loading, { title: t.loading }, "podcast-loading-indicator") }, "podcast-loading")
  ] }, "podcasts");
  if (loaded.kind === "error") return /* @__PURE__ */ jsxs(View, { title: t.discover, state: encodeState(state), children: [
    search,
    navigation,
    /* @__PURE__ */ jsxs(Section, { children: [
      text2("podcast-error-message", errorText(loaded.error, props.locale ?? service.locale)),
      /* @__PURE__ */ jsx(Button, { title: t.retry, action: act("refresh") }, "podcast-retry")
    ] }, "podcast-error")
  ] }, "podcasts");
  if (state.v === "show") {
    if (loaded.kind !== "show") throw new ReadError("invalid");
    const result = loaded.result, show = result.show, saved = props.store.has(show.id);
    return /* @__PURE__ */ jsxs(View, { title: show.title, state: encodeState(state), children: [
      /* @__PURE__ */ jsx(Section, { title: show.title, footer: [show.publisher, show.genre, show.episodes === void 0 ? "" : fmt(t.episodeCount, { count: show.episodes })].filter(Boolean).join(" \xB7 "), children: text2("show-publisher", show.publisher || t.account, "body") }, "show-details"),
      /* @__PURE__ */ jsxs(Section, { children: [
        /* @__PURE__ */ jsx(Button, { title: saved ? t.saved : t.save, action: act(`save:${show.id}`) }, "show-save"),
        saved ? /* @__PURE__ */ jsx(Button, { title: t.remove, action: act(`remove:${show.id}`) }, "show-remove") : null,
        /* @__PURE__ */ jsx(Link, { title: t.openShow, url: show.url }, "show-open"),
        show.feedURL ? /* @__PURE__ */ jsx(Link, { title: t.openFeed, url: show.feedURL }, "show-feed") : null,
        /* @__PURE__ */ jsx(Button, { title: t.back, action: act("back") }, "show-back")
      ] }, "show-actions"),
      message,
      result.episodes.length ? /* @__PURE__ */ jsx(Section, { title: t.episodes, children: result.episodes.map((episode) => /* @__PURE__ */ jsx(Link, { title: episode.title, url: episode.url }, `episode-${episode.id}`)) }, "show-episodes") : /* @__PURE__ */ jsx(Section, { children: text2("show-no-episodes-text", t.noEpisodes) }, "show-no-episodes"),
      /* @__PURE__ */ jsx(Section, { title: t.episodes, children: result.episodes.map((episode) => text2(`episode-note-${episode.id}`, `${episode.title}
${[episode.at ? readTime(episode.at, tz, props.locale ?? service.locale) : t.unknownDate, episode.minutes === void 0 ? "" : fmt(t.minutes, { count: episode.minutes })].filter(Boolean).join(" \xB7 ")}
${episode.description}`, "body")) }, "show-episode-notes"),
      /* @__PURE__ */ jsx(Section, { footer: `${fmt(t.readAt, { at: readTime(result.at, tz, props.locale ?? service.locale) })}${result.stale ? ` \xB7 ${errorText(result.stale, props.locale ?? service.locale)}` : ""}`, children: /* @__PURE__ */ jsx(Button, { title: t.refresh, action: act("refresh") }, "show-refresh") }, "show-footer"),
      /* @__PURE__ */ jsx(Section, { children: text2("show-feed-note-text", t.feedHint, "caption") }, "show-feed-note"),
      /* @__PURE__ */ jsx(Section, { children: text2("show-license", t.attribution, "caption") }, "show-attribution")
    ] }, "podcasts");
  }
  if (state.v === "library") {
    if (loaded.kind !== "library") throw new ReadError("invalid");
    return /* @__PURE__ */ jsxs(View, { title: t.library, state: encodeState(state), children: [
      navigation,
      /* @__PURE__ */ jsx(Section, { title: t.library, children: loaded.shows.length ? loaded.shows.map((show) => /* @__PURE__ */ jsx(Row, { title: show.title, subtitle: [show.publisher, show.genre].filter(Boolean).join(" \xB7 "), action: act(`show:${show.id}`, { v: "show", q: "", k: show.id, from: "library", b: show }, show.title) }, `library-${show.id}`)) : text2("library-empty", t.libraryEmpty) }, "library-shows"),
      /* @__PURE__ */ jsx(Section, { children: text2("library-note-text", t.libraryHint, "caption") }, "library-note")
    ] }, "podcasts");
  }
  const row = (show) => /* @__PURE__ */ jsx(
    Row,
    {
      title: show.title,
      subtitle: [show.publisher, show.genre, show.episodes === void 0 ? "" : fmt(t.episodeCount, { count: show.episodes })].filter(Boolean).join(" \xB7 "),
      action: act(`show:${show.id}`, { v: "show", q: state.q, k: show.id, b: show }, show.title)
    },
    `show-${show.id}`
  );
  return /* @__PURE__ */ jsxs(View, { title: state.v === "search" ? fmt(t.results, { query: clip(state.q, 100) }) : t.discover, state: encodeState(state), children: [
    search,
    navigation,
    state.v === "search" ? /* @__PURE__ */ jsx(Section, { title: fmt(t.results, { query: clip(state.q, 100) }), children: loaded.kind === "search" && loaded.result.shows.length ? loaded.result.shows.map(row) : text2("podcast-empty", t.empty) }, "podcast-results") : /* @__PURE__ */ jsx(Section, { children: text2("podcast-welcome-text", t.searchHint, "body") }, "podcast-welcome"),
    /* @__PURE__ */ jsx(Section, { children: text2("podcast-license", t.attribution, "caption") }, "podcast-attribution")
  ] }, "podcasts");
}

// src/root.tsx
function Root(options) {
  const [state, setState] = useState(() => readState(options.state));
  const [refresh, setRefresh] = useState(0);
  const [result, setResult] = useState(void 0);
  const identity = `${state.v}:${state.q}:${state.k}:${refresh}`;
  useLoadEffect(async (signal) => {
    const loaded = await loadPage(options.service, options.store, state, refresh > 0, signal);
    if (!signal.aborted) setResult((previous) => previous?.service === options.service && previous.identity === identity && JSON.stringify(previous.loaded) === JSON.stringify(loaded) ? previous : { service: options.service, identity, loaded });
  }, [options.service, identity]);
  return page({
    ...options,
    state,
    loaded: result?.service === options.service && result.identity === identity ? result.loaded : { kind: "loading" },
    action: (id2, next, title) => {
      if (next && (options.uiVersion ?? 0) >= 3) {
        const push = { ui: "discover", state: encodeState(next), ...title ? { title } : {} };
        return callback(() => ({ push }), { push });
      }
      return callback(async (values) => {
        if (id2 === "refresh") {
          setRefresh((previous) => previous + 1);
          return;
        }
        if (id2 === "back" && state.v === "show" && (options.uiVersion ?? 0) >= 3) return { pop: true };
        if (id2.startsWith("show:")) {
          const show = result?.loaded.kind === "search" ? result.loaded.result.shows.find((show2) => show2.id === Number(id2.slice(5))) : result?.loaded.kind === "library" ? result.loaded.shows.find((show2) => show2.id === Number(id2.slice(5))) : void 0;
          if (show) setState({ v: "show", q: state.q, k: show.id, from: state.v === "library" ? "library" : void 0, b: show });
          return;
        }
        if (id2.startsWith("save:") || id2.startsWith("remove:")) {
          const show = result?.loaded.kind === "show" ? result.loaded.result.show : state.b;
          if (!show || show.id !== Number(id2.slice(id2.indexOf(":") + 1))) return;
          if (id2.startsWith("save:")) await options.store.save(show);
          else await options.store.remove(show.id);
          setState((previous) => ({ ...previous, message: id2.startsWith("save:") ? "saved" : "removed" }));
          return;
        }
        setState((previous) => transition(previous, id2, values));
      }, { submit: true });
    }
  });
}
function root(options) {
  return /* @__PURE__ */ jsx(Root, { ...options }, "podcasts-root");
}

// src/store.ts
var emptyList = () => ({ schema_version: 1, shows: [] });
function decodeList(value) {
  if (value.length > 87384) throw new ReadError("storage");
  let raw;
  try {
    raw = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(storageValue(value)));
  } catch {
    throw new ReadError("storage");
  }
  if (!raw || typeof raw !== "object" || Array.isArray(raw) || raw.schema_version !== 1 || !Array.isArray(raw.shows)) throw new ReadError("storage");
  const values = raw.shows;
  if (values.length > 50) throw new ReadError("storage");
  const shows = values.map(parseStoredShow);
  if (shows.some((show) => !show) || new Set(shows.map((show) => show.id)).size !== shows.length) throw new ReadError("storage");
  return { schema_version: 1, shows };
}
function encodeList(list) {
  if (list.schema_version !== 1 || list.shows.length > 50 || new Set(list.shows.map((show) => show.id)).size !== list.shows.length) throw new ReadError("storage");
  const value = storageBytes(new TextEncoder().encode(JSON.stringify(list)));
  decodeList(value);
  return value;
}
var ListeningStore = class {
  constructor(hub, now = Date.now) {
    this.now = now;
    this.storage = new ExtensionStorage(hub);
  }
  list = emptyList();
  loaded = false;
  loading;
  revision;
  serial = Promise.resolve();
  pending;
  storage;
  snapshot() {
    return structuredClone(this.list);
  }
  has(id2) {
    return this.list.shows.some((show) => show.id === id2);
  }
  async load() {
    if (this.loaded) return this.snapshot();
    if (this.loading) return this.loading;
    this.loading = (async () => {
      try {
        const stored = await this.storage.get("subscriptions");
        this.list = decodeList(stored.data);
        this.revision = stored.revision;
      } catch (error) {
        if (error?.code !== "not_found") {
          this.loaded = false;
          throw new ReadError("storage");
        }
        this.list = emptyList();
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
      for (let attempt = 0; attempt < 3; attempt++) {
        if (this.pending) {
          const pending = this.pending;
          if (this.now() - pending.at > 5 * 6e4) {
            this.pending = void 0;
            this.loaded = false;
            await this.load();
            continue;
          }
          try {
            const result = await this.storage.transaction(pending.request);
            const item = result.items.find((item2) => item2.key === "subscriptions");
            if (!item || item.deleted || typeof item.revision !== "string") throw new ReadError("storage");
            this.pending = void 0;
            this.list = decodeList(pending.request.operations[0].data ?? "");
            this.revision = item.revision;
          } catch (error) {
            if (["storage_unknown", "storage_timeout", "storage_canceled"].includes(error?.code ?? "")) throw new ReadError("storage");
            if (error?.code === "storage_expired") {
              this.pending = void 0;
              this.loaded = false;
              await this.load();
              continue;
            }
            this.pending = void 0;
            if (error?.code !== "storage_conflict") throw error;
            this.loaded = false;
            await this.load();
          }
          continue;
        }
        const next = { schema_version: 1, shows: change(this.snapshot()) };
        if (next.shows.length > 50) throw new ReadError("storage");
        const { request_id } = await this.storage.request();
        const request = { request_id, operations: [{ kind: "put", key: "subscriptions", revision: this.revision ?? "", data: encodeList(next) }] };
        try {
          const result = await this.storage.transaction(request);
          const item = result.items.find((item2) => item2.key === "subscriptions");
          if (!item || item.deleted || typeof item.revision !== "string") throw new ReadError("storage");
          this.list = next;
          this.revision = item.revision;
          return;
        } catch (error) {
          const code = error?.code;
          if (["storage_unknown", "storage_timeout", "storage_canceled"].includes(code ?? "")) {
            this.pending = { request, at: this.now(), change };
            throw new ReadError("storage");
          }
          if (code !== "storage_conflict" || attempt === 2) throw error;
          this.loaded = false;
          await this.load();
        }
      }
      throw new ReadError("storage");
    });
    this.serial = work.catch(() => {
    });
    return work;
  }
  save(show) {
    return this.mutate((list) => [show, ...list.shows.filter((item) => item.id !== show.id)]);
  }
  remove(id2) {
    return this.mutate((list) => list.shows.filter((show) => show.id !== id2));
  }
};
var MemoryStore = class {
  list = emptyList();
  async load() {
    return this.snapshot();
  }
  snapshot() {
    return structuredClone(this.list);
  }
  has(id2) {
    return this.list.shows.some((show) => show.id === id2);
  }
  async save(show) {
    this.list = { schema_version: 1, shows: [show, ...this.list.shows.filter((item) => item.id !== show.id)] };
  }
  async remove(id2) {
    this.list = { schema_version: 1, shows: this.list.shows.filter((show) => show.id !== id2) };
  }
};

// src/snapshot.ts
function snapshotPage(service, store) {
  const render = async (req, refresh = false) => {
    const state = readState(req.state), loaded = await loadPage(service, store, state, refresh);
    const renderer = new Renderer({ authorize: () => false });
    try {
      return structuredClone(renderer.mount(page({ service, store, state, loaded, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })).view);
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
var sourceManifest = { ...persona_extension_default, runtime: { mode: "deno", source: { entry: "src/index.ts", bundle: "build/podcasts.mjs", sha256: "" } } };
delete sourceManifest.icon;
delete sourceManifest.replaces;
function createProgram(fetch = (input, init) => globalThis.fetch(input, init), options = {}, store) {
  let service;
  let listening;
  const current = () => {
    if (!service || !listening) throw new Error("Podcasts has not been initialized");
    return { service, store: listening };
  };
  return {
    id: persona_extension_default.id,
    version: persona_extension_default.version,
    manifest: sourceManifest,
    init: async (init, hub) => {
      service = new Podcasts(fetch, init.config, { ...options, locale: init.hub?.locale ?? options.locale });
      listening = store ?? new ListeningStore(hub);
      if (!store) await listening.load();
    },
    namespaces: { connector: connector(() => current().service) },
    ui: { discover: componentPage((req) => root({ service: current().service, store: current().store, state: req.state, locale: req.locale, tz: req.tz, uiVersion: req.ui_version })) }
  };
}
var index_default = createProgram();
export {
  ListeningStore,
  MemoryStore,
  Podcasts,
  ReadError,
  Root,
  budget,
  callTool,
  connector,
  createProgram,
  decodeList,
  index_default as default,
  encodeList,
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
