// ../../sdk/ts/src/exact-json.ts
var EXT_ERROR = Symbol.for("@persona/extension.ExtError");
var JSONIntegerError = class extends TypeError {
  [EXT_ERROR] = true;
  code = "invalid_params";
};
var MAX_EXACT_LENGTH = 8 << 20;
var VALIDATED_JSON = Symbol("validated JSON slice");
var whitespace = (c) => c === " " || c === "	" || c === "\r" || c === "\n";
function skipSpace(text, at) {
  while (whitespace(text[at])) at++;
  return at;
}
function stringEnd(text, at) {
  for (at++; at < text.length; at++) {
    if (text[at] === "\\") at++;
    else if (text[at] === '"') return at + 1;
  }
  throw new SyntaxError("unterminated JSON string");
}
function valueEnd(text, at) {
  if (text[at] === '"') return stringEnd(text, at);
  if (text[at] === "{" || text[at] === "[") {
    let depth = 0;
    for (; at < text.length; at++) {
      const c = text[at];
      if (c === '"') at = stringEnd(text, at) - 1;
      else if (c === "{" || c === "[") depth++;
      else if ((c === "}" || c === "]") && --depth === 0) return at + 1;
    }
    throw new SyntaxError("unterminated JSON container");
  }
  while (at < text.length && !whitespace(text[at]) && !",]}".includes(text[at])) at++;
  return at;
}
function foldASCII(name) {
  return name.replace(/[a-zſK]/g, (c) => c === "\u212A" ? "K" : c === "\u017F" ? "S" : c.toUpperCase());
}
var ExactJSON = class _ExactJSON {
  constructor(raw, validated) {
    this.raw = raw;
    if (validated !== VALIDATED_JSON) throw new TypeError("use ExactJSON.parse to validate JSON");
    Object.freeze(this);
  }
  /** Validates JSON syntax, keeping its original tokens. The opt-in view is limited to 8 MiB
   * of UTF-16 text; transport byte/resource limits remain the host's responsibility. */
  static parse(raw) {
    if (typeof raw !== "string") throw new TypeError("exact JSON input must be text");
    if (raw.length > MAX_EXACT_LENGTH) throw new RangeError("exact JSON exceeds 8 MiB of text");
    JSON.parse(raw);
    return new _ExactJSON(raw.trim(), VALIDATED_JSON);
  }
  *entries() {
    if (this.raw === "null") return;
    if (this.raw[0] !== "{") throw new JSONIntegerError("expected a JSON object or null");
    let at = skipSpace(this.raw, 1);
    while (this.raw[at] !== "}") {
      const end = stringEnd(this.raw, at);
      const key2 = JSON.parse(this.raw.slice(at, end));
      at = skipSpace(this.raw, skipSpace(this.raw, end) + 1);
      const value = valueEnd(this.raw, at);
      yield [key2, new _ExactJSON(this.raw.slice(at, value), VALIDATED_JSON)];
      at = skipSpace(this.raw, value);
      if (this.raw[at] === ",") at = skipSpace(this.raw, at + 1);
    }
  }
  /** The final occurrence of an exact, JSON-decoded key; null/nonexistent objects have no members. */
  member(name) {
    let found;
    for (const [key2, value] of this.entries()) if (key2 === name) found = value;
    return found;
  }
  /** Ordered raw struct-field occurrences for an extension's custom decoder, including
   * mixed string/integer IDs. The decoder must preserve its own null/type/error rules. */
  *fieldValues(name) {
    if (!name || /[^\x00-\x7f]/.test(name)) throw new TypeError("field name must be ASCII");
    const folded = foldASCII(name);
    for (const [key2, value] of this.entries()) if (foldASCII(key2) === folded) yield value;
  }
  /** Last json.RawMessage struct field, including null, with Go matching for an ASCII
   * JSON field name. Unlike member(), this also matches case-folded aliases. This does
   * not merge repeated nested typed structs; decode the selected RawMessage separately. */
  field(name) {
    let found;
    for (const value of this.fieldValues(name)) found = value;
    return found;
  }
  /** Decode a Go int64 token: only -?(0|[1-9][0-9]*) within signed 64 bits.
   * null leaves an existing scalar unchanged; on a fresh field it is zero. */
  int64(previous = 0n) {
    if (this.raw === "null") return previous;
    if (!/^-?(?:0|[1-9][0-9]*)$/.test(this.raw)) throw new JSONIntegerError("expected a lexical JSON int64");
    const negative = this.raw[0] === "-";
    const digits = negative ? this.raw.slice(1) : this.raw;
    const limit = negative ? "9223372036854775808" : "9223372036854775807";
    if (digits.length > limit.length || digits.length === limit.length && digits > limit) {
      throw new JSONIntegerError("JSON integer is outside signed int64");
    }
    return BigInt(this.raw);
  }
  /** Decode a fresh Go struct of int64 fields (ASCII JSON field names, no embedding).
   * Missing/null fields are zero; duplicates are read in order, null does not overwrite,
   * and a wrong type/decimal/exponent/overflow is an error even if a later value is valid.
   * Unknown fields are ignored. Matching prefers exact names, then Go's ASCII folded names.
   * This is not JSON Schema's mathematical integer validation. */
  int64Fields(names) {
    const values = /* @__PURE__ */ Object.create(null);
    const exact = /* @__PURE__ */ new Map(), folded = /* @__PURE__ */ new Map();
    for (const name of names) {
      if (!name || /[^\x00-\x7f]/.test(name) || exact.has(name) || folded.has(foldASCII(name))) {
        throw new TypeError("int64 field names must be distinct ASCII names with distinct folds");
      }
      exact.set(name, name);
      folded.set(foldASCII(name), name);
      values[name] = 0n;
    }
    for (const [key2, value] of this.entries()) {
      const name = exact.get(key2) ?? folded.get(foldASCII(key2));
      if (name !== void 0) values[name] = value.int64(values[name]);
    }
    return values;
  }
};

// ../../sdk/ts/src/components.ts
var callbackTag = Symbol("callback");
function stringValues(value) {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new Error("invalid action values");
  const entries = Object.entries(value);
  if (entries.length > 128 || entries.some(([key2, item]) => key2.length > 256 || typeof item !== "string" || item.length > 16384)) {
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
function createElement(type, props, key2) {
  if (typeof type !== "function" && type !== Fragment) throw new Error("only registered native or function components are supported");
  const { key: propKey, ...rest } = props ?? {};
  const resolvedKey = key2 ?? propKey;
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
function useEffect(effect, dependencies) {
  effectHook(effect, dependencies, false);
}
function useLoadEffect(effect, dependencies) {
  effectHook(effect, dependencies, true);
}
function useLatest(value) {
  const [slot] = hook("latest", () => ({ kind: "latest", value }));
  slot.value = value;
  return slot;
}
function useInterval(fn, milliseconds2) {
  if (!context) throw new Error("hooks require a mounted function component");
  const owner = context.owner;
  const latest = useLatest(fn);
  const [work] = hook("interval", () => ({ kind: "interval", running: false }));
  useEffect((signal) => {
    if (milliseconds2 === null) return;
    if (!Number.isFinite(milliseconds2) || milliseconds2 <= 0) throw new Error("interval must be positive and finite");
    const handle = owner.timers.setInterval(() => {
      if (signal.aborted || work.running || owner.suspended || owner.disposed) return;
      work.running = true;
      Promise.resolve().then(() => {
        if (!signal.aborted) return latest.value(signal);
      }).catch((error) => owner.report(error)).finally(() => {
        work.running = false;
      });
    }, milliseconds2);
    return () => owner.timers.clearInterval(handle);
  }, [milliseconds2]);
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
    const addKey = (key2) => {
      if (typeof key2 !== "string" || !key2 || key2.length > 256 || keys.has(key2)) throw new Error("missing or duplicate UI key");
      if (++nodeCount > this.limits.nodes) throw new Error("UI node limit exceeded");
      keys.add(key2);
      return key2;
    };
    const lower = (value, key2, depth, ancestors, slot) => {
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
        const identity = JSON.stringify([key2, slot]);
        if (registrations.size >= this.limits.callbacks) throw new Error("UI callback limit exceeded");
        const id = previousActions.get(identity) ?? `cb.${globalThis.crypto.randomUUID()}`;
        const options = lower(cb.options, key2, depth + 1, ancestors, pathPart(slot, "options"));
        if (Object.keys(options).some((name) => !["confirm", "submit", "push", "values"].includes(name))) throw new Error("invalid callback action options");
        if (options.push && (options.push.page !== void 0 || Object.keys(options.push).some((name) => !["ui", "state", "title"].includes(name)))) throw new Error("assistant-only action");
        const action = { ...options, id };
        registrations.set(id, { identity, key: key2, action, callback: cb });
        return action;
      }
      if (ancestors.has(value)) throw new Error("cyclic UI props");
      ancestors.add(value);
      try {
        if (Array.isArray(value)) {
          if (value.length > this.limits.props - propCount) throw new Error("UI prop limit exceeded");
          return Array.from(value, (item, index) => lower(item, key2, depth + 1, ancestors, pathPart(slot, "array", String(index))));
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
            Object.defineProperty(out, name, { value: lower(item, key2, depth + 1, ancestors, pathPart(slot, "prop", name)), enumerable: true });
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
      const key2 = addKey(element.key);
      const identity = nativeIdentity(JSON.stringify([path, kind]));
      const { children, ...props } = element.props;
      const allowed = kind === "view" ? ["title", "state", "refresh_after", "search", "filter", "tabs", "layout", "review"] : kind === "section" ? ["title", "footer", "more", "pane"] : fields[kind];
      if (!allowed || Object.keys(props).some((name) => !allowed.includes(name))) throw new Error("unknown native component prop");
      if (kind === "view" || kind === "section") {
        const items = resolve(children, path, depth + 1);
        const expected = kind === "view" ? "section" : "component";
        if (items.some((item) => item.kind !== expected)) throw new Error("invalid native UI nesting");
        const out2 = lower(props, key2, depth + 1, /* @__PURE__ */ new Set(), JSON.stringify([identity, kind]));
        return [{ kind, key: key2, value: kind === "view" ? { ...out2, sections: items.map((item) => item.value) } : { ...out2, key: key2, items: items.map((item) => item.value) } }];
      }
      if (children !== void 0) throw new Error("native leaf components do not accept children");
      let out;
      if (kind === "grid" || kind === "carousel") {
        const { tiles, ...rest } = props;
        if (!Array.isArray(tiles)) throw new Error("tiles must be an array");
        const lowered = lower(rest, key2, depth + 1, /* @__PURE__ */ new Set(), JSON.stringify([identity, kind]));
        const tileCount = tiles.length;
        if (++propCount > this.limits.props || tileCount > this.limits.props - propCount) throw new Error("UI prop limit exceeded");
        if (tileCount > this.limits.nodes - nodeCount) throw new Error("UI node limit exceeded");
        out = { ...lowered, tiles: Array.from({ length: tileCount }, (_, index) => {
          const tile = tiles[index];
          const tileKey = addKey(tile?.key);
          return lower(tile, tileKey, depth + 1, /* @__PURE__ */ new Set(), nativeIdentity(JSON.stringify([identity, kind, tileKey])));
        }) };
      } else out = lower(props, key2, depth + 1, /* @__PURE__ */ new Set(), JSON.stringify([identity, kind]));
      return [{ kind: "component", key: key2, value: { ...out, type: kind, key: key2 } }];
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
      const fingerprint2 = JSON.stringify([saved.generation, saved.version, saved.key, saved.actionId, saved.values]);
      if (encoder.encode(fingerprint2).byteLength > this.limits.frameBytes) throw new Error("UI event frame limit exceeded");
      const receipt = this.receipts.get(saved.eventId);
      if (receipt) {
        if (receipt.fingerprint !== fingerprint2) throw new Error("event ID collision: body mismatch");
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
      this.receipts.set(saved.eventId, { fingerprint: fingerprint2, result });
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
        for (const key2 in item) {
          if (!Object.prototype.hasOwnProperty.call(item, key2)) continue;
          if (keys.length >= this.limits.props - count) throw new Error("UI event value limit exceeded");
          strings.add(key2);
          keys.push(key2);
        }
        return Object.fromEntries(keys.sort().map((key2) => [key2, copy(item[key2], depth + 1)]));
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
var unsupported = (why) => new ExtError(why, "unsupported");
var EXACT_PARAMS = Symbol.for("@persona/extension.exactParams");
function withExactParams(handler) {
  const wrapped = (method, params, call) => handler(method, params, call);
  Object.defineProperty(wrapped, EXACT_PARAMS, { value: true });
  return wrapped;
}

// src/http.ts
var ReadError = class extends Error {
  constructor(code, message, status) {
    super(message);
    this.code = code;
    this.status = status;
  }
};
var notFound = () => new ReadError("not_found", "Instrument not found");
var aborted = (signal) => {
  if (signal?.aborted) throw signal.reason ?? new DOMException("Cancelled", "AbortError");
};
var isCancelled = (err) => err instanceof Error && err.name === "AbortError";
var Queue = class {
  constructor(maximum2 = 4) {
    this.maximum = maximum2;
  }
  active = 0;
  waiting = [];
  async run(fn, signal) {
    aborted(signal);
    if (this.active >= this.maximum) await new Promise((resolve, reject) => {
      const start = () => {
        signal?.removeEventListener("abort", cancel);
        this.active++;
        resolve();
      };
      const cancel = () => {
        const i = this.waiting.indexOf(start);
        if (i >= 0) this.waiting.splice(i, 1);
        reject(signal?.reason ?? new DOMException("Cancelled", "AbortError"));
      };
      if (this.waiting.length >= 128) {
        reject(new ReadError("rate_limited", "Market request queue full"));
        return;
      }
      this.waiting.push(start);
      signal?.addEventListener("abort", cancel, { once: true });
    });
    else this.active++;
    try {
      aborted(signal);
      return await fn();
    } finally {
      this.active--;
      this.waiting.shift()?.();
    }
  }
};
var Cache = class {
  constructor(clock, maximum2 = 100) {
    this.clock = clock;
    this.maximum = maximum2;
  }
  entries = /* @__PURE__ */ new Map();
  get(k, fresh) {
    const c = this.entries.get(k);
    return c && fresh > 0 && this.clock() - c.at < fresh ? c.value : void 0;
  }
  put(k, value) {
    if (!this.entries.has(k) && this.entries.size >= this.maximum) {
      let oldest = "", at = Infinity;
      for (const [k2, c] of this.entries) if (c.at < at) {
        oldest = k2;
        at = c.at;
      }
      this.entries.delete(oldest);
    }
    this.entries.set(k, { value, at: this.clock() });
  }
};
var hosts = ["www.okx.com", "qt.gtimg.cn", "smartbox.gtimg.cn", "web.ifzq.gtimg.cn", "ifzq.gtimg.cn"];
var trustedFetch = (input, init) => globalThis.fetch(input, init);
var HTTP = class {
  constructor(fetch = trustedFetch, clock = Date.now) {
    this.fetch = fetch;
    this.clock = clock;
    this.cache = new Cache(clock);
  }
  queue = new Queue(4);
  cache;
  async get(url, fresh, limit = 4 * 1024 * 1024, signal, validate) {
    aborted(signal);
    const u = new URL(url);
    if (u.protocol !== "https:" || !hosts.includes(u.hostname) || u.username || u.password || u.port) throw new ReadError("invalid", "Unapproved market URL");
    if (!Number.isSafeInteger(limit) || limit < 0) throw new ReadError("invalid", "Invalid market response size limit");
    const bounded2 = (body) => {
      if (body.byteLength > limit) throw new ReadError("invalid", "Market response exceeds size limit");
      validate?.(body);
      return body;
    };
    const hit = this.cache.get(url, fresh);
    if (hit) return bounded2(hit);
    return this.queue.run(async () => {
      aborted(signal);
      const cached = this.cache.get(url, fresh);
      if (cached) return bounded2(cached);
      const control = new AbortController();
      const cancel = () => control.abort(signal?.reason);
      signal?.addEventListener("abort", cancel, { once: true });
      const timer = setTimeout(() => control.abort(new ReadError("timeout", "Market request timed out")), 15e3);
      try {
        const response = await this.fetch(url, { method: "GET", headers: { "User-Agent": "persona-markets/0.6.5", Accept: "application/json" }, signal: control.signal, redirect: "error" });
        if (response.status === 429) {
          await response.body?.cancel();
          throw new ReadError("rate_limited", "Too many requests; try later", 429);
        }
        if (!response.ok) {
          await response.body?.cancel();
          throw new ReadError("http", `Market returned HTTP ${response.status}`, response.status);
        }
        const reader = response.body?.getReader();
        if (!reader) throw new ReadError("invalid", "Empty market response");
        const chunks = [];
        let size = 0;
        try {
          for (; ; ) {
            aborted(control.signal);
            const { done, value } = await reader.read();
            if (done) break;
            size += value.byteLength;
            if (size > limit) throw new ReadError("invalid", "Market response exceeds size limit");
            chunks.push(value);
          }
        } catch (err) {
          await reader.cancel().catch(() => {
          });
          throw err;
        } finally {
          reader.releaseLock();
        }
        const body = new Uint8Array(size);
        let i = 0;
        for (const chunk of chunks) {
          body.set(chunk, i);
          i += chunk.length;
        }
        aborted(control.signal);
        bounded2(body);
        this.cache.put(url, body);
        return body;
      } catch (err) {
        if (control.signal.aborted) throw control.signal.reason;
        if (err instanceof ReadError) throw err;
        throw new ReadError("http", "Cannot read market response");
      } finally {
        clearTimeout(timer);
        signal?.removeEventListener("abort", cancel);
      }
    }, signal);
  }
  async json(url, fresh, limit, signal, validate, parse = JSON.parse) {
    let value;
    await this.get(url, fresh, limit, signal, (body) => {
      try {
        value = parse(new TextDecoder("utf-8", { fatal: true }).decode(body));
        validate?.(value);
      } catch (err) {
        if (err instanceof ReadError) throw err;
        throw new ReadError("invalid", "Invalid market JSON");
      }
    });
    return value;
  }
};

// src/integers.ts
var minimum = -(1n << 63n);
var maximum = (1n << 63n) - 1n;
function int64(value) {
  if (value.trim() !== value || !/^[+-]?\d+$/.test(value)) return;
  const digits = value.replace(/^[+-]?0*/, "");
  if (digits.length > 19) return;
  const n = BigInt((value.startsWith("-") ? "-" : "") + (digits || "0"));
  return n >= minimum && n <= maximum ? n : void 0;
}
function toolInteger(value) {
  if (value === void 0 || value === null) return 0;
  if (typeof value === "bigint") {
    if (value < minimum || value > maximum) throw new ExtError("Invalid market tool arguments", "invalid_params");
    return value;
  }
  if (typeof value !== "number" || !Number.isInteger(value) || !Number.isFinite(value) || BigInt(value) < minimum || BigInt(value) > maximum) throw new ExtError("Invalid market tool arguments", "invalid_params");
  return value;
}
var occurrences = Symbol("market JSON fields");
function jsonFields(value) {
  return value[occurrences] ?? Object.entries(value);
}
function jsonObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value) && !(value instanceof JSONNumber);
}
var JSONNumber = class {
  constructor(lexeme) {
    this.lexeme = lexeme;
  }
};
function numericJSON(text) {
  JSON.parse(text);
  let at = 0;
  const whitespace2 = () => {
    while (/[\t\r\n ]/.test(text[at] ?? "!")) at++;
  };
  const string = () => {
    const start = at++;
    while (text[at] !== '"') {
      if (text[at] === "\\") at++;
      at++;
    }
    at++;
    return JSON.parse(text.slice(start, at));
  };
  const value = (depth) => {
    if (depth > 128) throw new Error("Market JSON nesting exceeds limit");
    whitespace2();
    if (text[at] === '"') return string();
    if (text[at] === "{" || text[at] === "[") {
      const object = text[at++] === "{", end = object ? "}" : "]";
      const result = object ? /* @__PURE__ */ Object.create(null) : [];
      const fields2 = [];
      if (object) Object.defineProperty(result, occurrences, { value: fields2 });
      whitespace2();
      while (text[at] !== end) {
        if (object) {
          const key2 = string();
          whitespace2();
          at++;
          const item = value(depth + 1);
          fields2.push([key2, item]);
          result[key2] = item;
        } else result.push(value(depth + 1));
        whitespace2();
        if (text[at] !== ",") break;
        at++;
        whitespace2();
      }
      at++;
      return result;
    }
    const start = at;
    while (at < text.length && !/[\t\r\n ,}\]]/.test(text[at])) at++;
    const token = text.slice(start, at);
    return /^(?:true|false|null)$/.test(token) ? JSON.parse(token) : new JSONNumber(token);
  };
  return value(0);
}
function jsonInteger(value) {
  return value instanceof JSONNumber && /^-?\d+$/.test(value.lexeme) ? int64(value.lexeme) : void 0;
}

// src/time.ts
function validRFC3339(value) {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(Z|[+-]\d{2}:\d{2})$/.exec(value);
  if (!match || match[0].length !== value.length) return false;
  const [year, month, date, hour, minute, second] = match.slice(1, 7).map(Number);
  const days = [31, year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  const zone = match[7];
  return month >= 1 && month <= 12 && date >= 1 && date <= days[month - 1] && hour <= 23 && minute <= 59 && second <= 59 && (zone === "Z" || Number(zone.slice(1, 3)) <= 23 && Number(zone.slice(4)) <= 59);
}
var milliseconds = (n) => n === void 0 ? 0 : n >= BigInt(Number.MIN_SAFE_INTEGER) && n <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(n) : n;
var day = 86400000n;
var floor = (a, b) => {
  const q = a / b;
  return a % b < 0n ? q - 1n : q;
};
function civil(at) {
  const days = floor(at, day), within = at - days * day, z = days + 719468n;
  const era = floor(z, 146097n), doe = z - era * 146097n;
  const yoe = (doe - doe / 1460n + doe / 36524n - doe / 146096n) / 365n;
  const doy = doe - (365n * yoe + yoe / 4n - yoe / 100n), mp = (5n * doy + 2n) / 153n;
  const month = mp + (mp < 10n ? 3n : -9n), date = doy - (153n * mp + 2n) / 5n + 1n;
  return { year: Number(yoe + era * 400n + (month <= 2n ? 1n : 0n)), month: Number(month), day: Number(date), hour: Number(within / 3600000n), minute: Number(within / 60000n % 60n), second: Number(within / 1000n % 60n) };
}
function utcDate(y, m, d, h = 0, mi = 0, s = 0) {
  const date = /* @__PURE__ */ new Date(0);
  date.setUTCFullYear(y, m - 1, d);
  date.setUTCHours(h, mi, s, 0);
  return date.getTime();
}
var timeZone = (zone) => !zone || zone === "Local" ? void 0 : zone;
function calendarMilliseconds(year, month, date, hour, minute, second) {
  const y = BigInt(year - (month <= 2 ? 1 : 0)), era = floor(y, 400n), yoe = y - era * 400n;
  const mp = BigInt(month + (month > 2 ? -3 : 9));
  const doy = (153n * mp + 2n) / 5n + BigInt(date) - 1n;
  const days = era * 146097n + yoe * 365n + yoe / 4n - yoe / 100n + doy - 719468n;
  return days * day + BigInt(hour * 3600 + minute * 60 + second) * 1000n;
}
function offset(at, zone) {
  const p = Object.fromEntries(new Intl.DateTimeFormat("en-US", { timeZone: timeZone(zone), era: "short", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" }).formatToParts(at).map((p2) => [p2.type, p2.value]));
  const year = p.era === "BC" ? 1 - Number(p.year) : Number(p.year);
  return Number(calendarMilliseconds(year, +p.month, +p.day, +p.hour, +p.minute, +p.second) - BigInt(Math.floor(at / 1e3)) * 1000n);
}
function displayZone(zone, at) {
  if (timeZone(zone)) return zone;
  const minutes = Math.trunc(offset(at, zone) / 6e4), absolute = Math.abs(minutes);
  return `UTC${minutes < 0 ? "-" : "+"}${String(Math.floor(absolute / 60)).padStart(2, "0")}:${String(absolute % 60).padStart(2, "0")}`;
}
function timeParts(at, zone) {
  if (typeof at === "number" && (!Number.isFinite(at) || !Number.isInteger(at))) throw new Error("Invalid market timestamp");
  const exact = BigInt(at), utc = civil(exact);
  let delta = 0;
  if (zone !== "UTC" && zone !== "Etc/UTC") {
    const probe = exact >= -8640000000000000n && exact <= 8640000000000000n ? Number(exact) : utcDate(utc.year < 0 ? 0 : 2400 + utc.year % 400, utc.month, utc.day, utc.hour, utc.minute, utc.second);
    delta = offset(probe, zone);
  }
  const p = civil(exact + BigInt(delta));
  return Object.fromEntries(Object.entries(p).map(([key2, value]) => [key2, key2 === "year" ? (value < 0 ? "-" : "") + String(Math.abs(value)).padStart(4, "0") : String(value).padStart(2, "0")]));
}

// src/typed-json.ts
var fold = (s) => s.replace(/[a-zſK]/g, (c) => c === "\u017F" ? "S" : c === "\u212A" ? "K" : c.toUpperCase());
function typedJSON(value, type, previous) {
  if (type === "raw") return value;
  if (typeof type === "object" && "nullable" in type) return value === null ? void 0 : typedJSON(value, type.nullable, previous);
  if (value === null) {
    if (typeof type === "object" && ("map" in type || "array" in type)) return null;
    return previous ?? (type === "string" ? "" : type === "integer" || type === "float" ? new JSONNumber("0") : void 0);
  }
  if (type === "string") {
    if (typeof value !== "string") throw new Error("Invalid market string field");
    return value;
  }
  if (type === "integer") {
    if (jsonInteger(value) === void 0) throw new Error("Invalid market integer field");
    return value;
  }
  if (type === "float") {
    if (!(value instanceof JSONNumber) || !Number.isFinite(Number(value.lexeme))) throw new Error("Invalid market floating-point field");
    return value;
  }
  if (type === "time") {
    if (typeof value !== "string" || !validRFC3339(value)) throw new Error("Invalid stored RFC3339 time");
    return value;
  }
  if ("array" in type) {
    if (!Array.isArray(value)) throw new Error("Invalid market array field");
    return value.map((item) => typedJSON(item, type.array));
  }
  if (!jsonObject(value)) throw new Error("Invalid market object field");
  const result = previous ?? /* @__PURE__ */ Object.create(null);
  for (const [name, item] of jsonFields(value)) {
    if ("map" in type) {
      result[name] = typedJSON(item, type.map);
    } else {
      const field = Object.hasOwn(type.fields, name) ? name : Object.keys(type.fields).find((key2) => fold(key2) === fold(name));
      if (field !== void 0) result[field] = typedJSON(item, type.fields[field], result[field]);
    }
  }
  return result;
}
function ordinaryJSON(value) {
  if (value instanceof JSONNumber) return Number(value.lexeme);
  if (Array.isArray(value)) return value.map(ordinaryJSON);
  if (jsonObject(value)) return Object.fromEntries(Object.entries(value).filter(([, item]) => item !== void 0).map(([key2, item]) => [key2, ordinaryJSON(item)]));
  return value;
}

// src/floats.ts
function floatResult(raw) {
  const fail = { value: 0, valid: false };
  const special = /^[+-]?(?:inf|infinity)$/i.exec(raw);
  if (special && special[0].length === raw.length) return { value: raw.startsWith("-") ? -Infinity : Infinity, valid: true };
  if (/^nan$/i.test(raw) && raw.length === 3) return { value: NaN, valid: true };
  const hex = /^[+-]?0[xX]/.test(raw), digit = hex ? /[0-9a-fA-F]/ : /[0-9]/;
  for (let i = 0; i < raw.length; i++) if (raw[i] === "_") {
    const prefix = hex && i === (raw[0] === "+" || raw[0] === "-" ? 3 : 2);
    if (!prefix && !digit.test(raw[i - 1] ?? "") || !digit.test(raw[i + 1] ?? "")) return fail;
  }
  const text = raw.replace(/_/g, "");
  if (!hex) {
    const match2 = /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/.exec(text);
    if (!match2 || match2[0].length !== text.length) return fail;
    const value2 = Number(text);
    return { value: value2, valid: Number.isFinite(value2) };
  }
  const match = /^([+-]?)0[xX]([0-9a-fA-F]+(?:\.[0-9a-fA-F]*)?|\.[0-9a-fA-F]+)[pP]([+-]?\d+)$/.exec(text);
  if (!match || match[0].length !== text.length) return fail;
  const negative = match[1] === "-", fraction = match[2].split(".")[1]?.length ?? 0;
  const digits = match[2].replace(".", "").replace(/^0+/, "");
  if (!digits) return { value: negative ? -0 : 0, valid: true };
  const kept = digits.slice(0, 16), sticky = /[1-9a-fA-F]/.test(digits.slice(16));
  const mantissa = BigInt("0x" + kept), bits = mantissa.toString(2).length;
  const scale = Number(match[3]) - fraction * 4 + Math.max(digits.length - 16, 0) * 4;
  const exponent = bits - 1 + scale;
  if (exponent > 1023) return { value: negative ? -Infinity : Infinity, valid: false };
  if (exponent < -1075) return { value: negative ? -0 : 0, valid: true };
  const quantum = Math.max(exponent - 52, -1074), shift = quantum - scale;
  let rounded = mantissa;
  if (shift > 0) {
    const count = BigInt(shift), half = 1n << count - 1n, rest = mantissa & (1n << count) - 1n;
    rounded >>= count;
    if (rest > half || rest === half && (sticky || (rounded & 1n) !== 0n)) rounded++;
  } else rounded <<= BigInt(-shift);
  const value = (negative ? -1 : 1) * Number(rounded) * 2 ** quantum;
  return { value, valid: Number.isFinite(value) };
}
var parseFloat64 = (raw) => {
  const result = floatResult(raw);
  return result.valid ? result.value : void 0;
};

// src/locales/en.json
var en_default = { markets: "Markets", okx: "OKX", cn: "A-shares", hk: "Hong Kong", us: "US", spot: "Spot", swap: "Perpetual swap", stock: "Stock", fund: "Fund", index: "Index", ETF: "ETF", day: "Today", "24h": "24h", watch: "Watchlist", benchmarks: "Benchmarks", movers: "Largest moves", cardMovers: "Largest watchlist moves", empty: "Your watchlist is empty; search to add an instrument", missing: "No current quote for this instrument", noQuote: "No quote", retry: "Retry", refresh: "Refresh", refreshed: "Refreshed", search: "Search", searchHint: "Search code or name: BTC, stock name, AAPL", searchTitle: "Search: {q}", none: "No matching instruments", clear: "Back to watchlist", backSearch: "Back to search", add: "Add to watchlist", added: "Added {symbol}", remove: "Remove from watchlist", removed: "Removed {symbol}", removeConfirm: "Remove {symbol} from your watchlist?", watching: "In watchlist", code: "Code", addHint: "Use a source prefix for stocks: cn:600519, hk:00700, us:AAPL; a bare coin is an OKX USDT pair", look: "View", order: "Watchlist order", sort: "Sort", upSort: "Largest rise", downSort: "Largest fall", all: "All", collapse: "Collapse", latest: "Latest price", idle: "Market closed \xB7 as of {at}", turnover: "Turnover", details: "Details", high: "High", low: "Low", open: "Open", previous: "Previous close", volume: "Volume", range: "Range", turnover_rate: "Turnover rate", pe: "P/E", cap: "Market cap", limit_up: "Limit up", limit_down: "Limit down", high52: "52-week high", low52: "52-week low", kline: "Candles", T: "Intraday", "15m": "15 minutes", "1H": "1 hour", "4H": "4 hours", "1D": "1 day", "1W": "1 week", close: "Close", noCandles: "No candles for this period", noMinutes: "No intraday data; trading has not started", span: "Change over {n} bars", sinceOpen: "Since open", spanHigh: "Period high", spanLow: "Period low", alerts: "Price alerts", alertsHint: "Checked every minute; each alert enters the Inbox once", newAlert: "New alert", alert: "Set alert", alertKind: "Alert", alertValue: "Price or percentage", above: "Above", below: "Below", move: "Absolute move above", cancel: "Cancel", waiting: "Waiting", met: "Triggered", near: "Near target", alertConfirm: "Delete this alert?", alertAdded: "Alert set", alertRemoved: "Alert deleted", alertWhy: "User-created price alert", top: "Move to top", up: "Move up", down: "Move down", site: "Open on {site}", tencent: "Tencent Securities", readAt: "Public market data \xB7 read at {at}", periodHint: "Changes are relative to the previous close (24 hours earlier for crypto)", moreHits: "Only the first 30 results; refine the query for more", loading: "Loading markets", error: "Cannot load market data", invalid: "Invalid code or alert target", saveError: "Could not save the change", watchLimit: "Watchlist limit reached (50)", alertLimit: "Alert limit reached (50)", duplicateAlert: "This pending alert already exists", alertMet: "The alert target is already met", cardOff: "The overview card is disabled", toolsQuote: "Read quotes; omitted symbols use the user's watchlist. Use source:code (okx:BTC-USDT, cn:600519, hk:00700, us:AAPL).", toolsSearch: "Search public market instruments by name or code before quoting unknown symbols.", toolsCandles: "Read oldest-first OHLCV candles in the configured timezone. Unsupported source periods are rejected.", quotesAt: "Quotes at {at}", candlesAt: "{ref} {bar}, {n} candles, timezone {zone}: time open high low close volume", badCode: "Invalid code: {code}", table: "Candle values", priceAlert: "Price alert", publicAccount: "Public markets; no account required", rateError: "Too many market requests; try again later", unitTenThousand: "\xD710K", unitHundredMillion: "\xD7100M", unitTrillion: "T" };

// src/locales/zh-Hans.json
var zh_Hans_default = { markets: "\u884C\u60C5", okx: "OKX", cn: "A\u80A1", hk: "\u6E2F\u80A1", us: "\u7F8E\u80A1", spot: "\u73B0\u8D27", swap: "\u6C38\u7EED\u5408\u7EA6", stock: "\u80A1\u7968", fund: "\u57FA\u91D1", index: "\u6307\u6570", ETF: "ETF", day: "\u5F53\u65E5", "24h": "24h", watch: "\u81EA\u9009", benchmarks: "\u5927\u76D8", movers: "\u81EA\u9009 \xB7 \u52A8\u5F97\u6700\u591A\u7684", cardMovers: "\u81EA\u9009\u91CC\u52A8\u5F97\u6700\u591A\u7684", empty: "\u81EA\u9009\u662F\u7A7A\u7684\uFF1A\u641C\u4E00\u4E2A\u52A0\u8FDB\u6765", missing: "\u73B0\u5728\u67E5\u4E0D\u5230\u8FD9\u4E2A\u4EE3\u7801\u7684\u884C\u60C5", noQuote: "\u65E0\u884C\u60C5", retry: "\u91CD\u8BD5", refresh: "\u5237\u65B0", refreshed: "\u5DF2\u5237\u65B0", search: "\u641C\u7D22", searchHint: "\u641C\u4EE3\u7801\u6216\u540D\u79F0\uFF1ABTC\u3001\u8305\u53F0\u3001\u817E\u8BAF\u3001AAPL", searchTitle: "\u641C\u7D22\u300C{q}\u300D", none: "\u6CA1\u6709\u5BF9\u5F97\u4E0A\u7684\u4EE3\u7801", clear: "\u8FD4\u56DE\u81EA\u9009", backSearch: "\u8FD4\u56DE\u641C\u7D22", add: "\u52A0\u5165\u81EA\u9009", added: "\u5DF2\u52A0\u5165 {symbol}", remove: "\u79FB\u51FA\u81EA\u9009", removed: "\u5DF2\u79FB\u51FA {symbol}", removeConfirm: "\u628A {symbol} \u79FB\u51FA\u81EA\u9009\uFF1F", watching: "\u5DF2\u5728\u81EA\u9009", code: "\u4EE3\u7801", addHint: "\u80A1\u7968\u4EE3\u7801\u524D\u5199\u6765\u6E90\uFF1Acn:600519\u3001hk:00700\u3001us:AAPL\uFF1B\u53EA\u5199\u5E01\u540D\u5F53\u6210 OKX \u7684 USDT \u4EA4\u6613\u5BF9", look: "\u67E5\u770B", order: "\u81EA\u9009\u987A\u5E8F", sort: "\u6392\u5E8F", upSort: "\u6DA8\u5E45", downSort: "\u8DCC\u5E45", all: "\u5168\u90E8", collapse: "\u6536\u8D77", latest: "\u6700\u65B0\u4EF7", idle: "\u4F11\u5E02 \xB7 \u622A\u81F3 {at}", turnover: "\u6210\u4EA4\u989D", details: "\u8BE6\u60C5", high: "\u6700\u9AD8", low: "\u6700\u4F4E", open: "\u4ECA\u5F00", previous: "\u6628\u6536", volume: "\u6210\u4EA4\u91CF", range: "\u632F\u5E45", turnover_rate: "\u6362\u624B\u7387", pe: "\u5E02\u76C8\u7387", cap: "\u603B\u5E02\u503C", limit_up: "\u6DA8\u505C", limit_down: "\u8DCC\u505C", high52: "52 \u5468\u6700\u9AD8", low52: "52 \u5468\u6700\u4F4E", kline: "K \u7EBF", T: "\u4ECA\u65E5\u5206\u65F6", "15m": "15 \u5206\u949F", "1H": "1 \u5C0F\u65F6", "4H": "4 \u5C0F\u65F6", "1D": "1 \u5929", "1W": "1 \u5468", close: "\u6536\u76D8\u4EF7", noCandles: "\u6CA1\u6709\u8FD9\u4E2A\u5468\u671F\u7684 K \u7EBF", noMinutes: "\u8FD8\u6CA1\u6709\u5206\u65F6\uFF1A\u4ECA\u5929\u8FD8\u6CA1\u5F00\u76D8", span: "{n} \u6839\u6DA8\u8DCC", sinceOpen: "\u5F00\u76D8\u4EE5\u6765", spanHigh: "\u533A\u95F4\u6700\u9AD8", spanLow: "\u533A\u95F4\u6700\u4F4E", alerts: "\u4EF7\u683C\u63D0\u9192", alertsHint: "\u5230\u4E86\u8FDB\u6536\u4EF6\u7BB1\uFF0C\u6BCF\u6761\u53EA\u63D0\u9192\u4E00\u6B21\uFF1B\u6BCF\u5206\u949F\u67E5\u4E00\u6B21", newAlert: "\u65B0\u5EFA\u63D0\u9192", alert: "\u8BBE\u63D0\u9192", alertKind: "\u63D0\u9192", alertValue: "\u4EF7\u683C\uFF0C\u6216\u6DA8\u8DCC\u5E45\u7684\u767E\u5206\u6570", above: "\u6DA8\u5230", below: "\u8DCC\u5230", move: "\u6DA8\u8DCC\u5E45\u8D85", cancel: "\u53D6\u6D88", waiting: "\u7B49\u7740", met: "\u63D0\u9192\u5230\u4E86", near: "\u5FEB\u5230\u63D0\u9192", alertConfirm: "\u5220\u6389\u8FD9\u6761\u63D0\u9192\uFF1F", alertAdded: "\u5DF2\u8BBE\u63D0\u9192", alertRemoved: "\u5DF2\u5220\u6389\u63D0\u9192", alertWhy: "\u7528\u6237\u81EA\u5DF1\u8BBE\u7684\u4EF7\u683C\u63D0\u9192", top: "\u7F6E\u9876", up: "\u4E0A\u79FB", down: "\u4E0B\u79FB", site: "\u5728 {site} \u6253\u5F00", tencent: "\u817E\u8BAF\u8BC1\u5238", readAt: "\u516C\u5F00\u884C\u60C5 \xB7 \u8BFB\u4E8E {at}", periodHint: "\u6DA8\u8DCC\u6309\u4E0A\u4E2A\u4EA4\u6613\u65E5\u7684\u6536\u76D8\u4EF7\u7B97\uFF0C\u52A0\u5BC6\u8D27\u5E01\u6309 24 \u5C0F\u65F6\u524D\u7684\u4EF7\u683C\u7B97", moreHits: "\u53EA\u5217\u6700\u524D\u9762\u7684 30 \u4E2A\uFF0C\u5199\u5F97\u518D\u5177\u4F53\u4E9B\u80FD\u627E\u5230\u522B\u7684", loading: "\u6B63\u5728\u8BFB\u53D6\u884C\u60C5", error: "\u884C\u60C5\u8BFB\u53D6\u5931\u8D25", invalid: "\u4EE3\u7801\u6216\u63D0\u9192\u76EE\u6807\u4E0D\u6B63\u786E", saveError: "\u53D8\u66F4\u672A\u80FD\u4FDD\u5B58", watchLimit: "\u81EA\u9009\u6700\u591A 50 \u4E2A", alertLimit: "\u4EF7\u683C\u63D0\u9192\u6700\u591A 50 \u6761", duplicateAlert: "\u5DF2\u6709\u76F8\u540C\u7684\u5F85\u89E6\u53D1\u63D0\u9192", alertMet: "\u63D0\u9192\u76EE\u6807\u73B0\u5728\u5DF2\u7ECF\u8FBE\u5230", cardOff: "\u603B\u89C8\u5361\u7247\u5DF2\u5173\u95ED", toolsQuote: "\u67E5\u5B9E\u65F6\u884C\u60C5\uFF0Csymbols \u4E0D\u7ED9\u5C31\u662F\u7528\u6237\u81EA\u9009\u3002\u4EE3\u7801\u5199\u6210\u6765\u6E90:\u4EE3\u7801\uFF0C\u5982 okx:BTC-USDT\u3001cn:600519\u3001hk:00700\u3001us:AAPL\u3002", toolsSearch: "\u6309\u540D\u79F0\u6216\u4EE3\u7801\u641C\u516C\u5F00\u884C\u60C5\u54C1\u79CD\uFF0C\u4E0D\u77E5\u9053\u51C6\u786E\u4EE3\u7801\u65F6\u5148\u641C\u7D22\u3002", toolsCandles: "\u8BFB\u53D6\u65E7\u7684\u5728\u524D\u7684 K \u7EBF\uFF1A\u7528\u6237\u65F6\u533A\u7684\u5F00\u59CB\u65F6\u95F4\u3001\u5F00\u3001\u9AD8\u3001\u4F4E\u3001\u6536\u3001\u91CF\uFF1B\u6765\u6E90\u6CA1\u6709\u7684\u5468\u671F\u62A5\u9519\u3002", quotesAt: "\u884C\u60C5\uFF0C{at}", candlesAt: "{ref} \u7684 {bar} K \u7EBF\uFF0C\u6700\u8FD1 {n} \u6839\uFF08\u65F6\u95F4\u662F {zone}\uFF09\uFF1A\u65F6\u95F4 \u5F00 \u9AD8 \u4F4E \u6536 \u91CF", badCode: "\u300C{code}\u300D\u4E0D\u50CF\u80FD\u67E5\u7684\u4EE3\u7801", table: "K \u7EBF\u6570\u503C", priceAlert: "\u884C\u60C5\u63D0\u9192", publicAccount: "\u516C\u5F00\u884C\u60C5\uFF0C\u4E0D\u7528\u8D26\u53F7", rateError: "\u884C\u60C5\u8BF7\u6C42\u592A\u591A\uFF0C\u7A0D\u540E\u518D\u8BD5", unitTenThousand: "\u4E07", unitHundredMillion: "\u4EBF", unitTrillion: "\u4E07\u4EBF" };

// src/locales/ja.json
var ja_default = { markets: "\u5E02\u5834", okx: "OKX", cn: "\u4E2D\u56FDA\u682A", hk: "\u9999\u6E2F\u682A", us: "\u7C73\u56FD\u682A", spot: "\u73FE\u7269", swap: "\u7121\u671F\u9650\u5148\u7269", stock: "\u682A\u5F0F", fund: "\u30D5\u30A1\u30F3\u30C9", index: "\u6307\u6570", ETF: "ETF", day: "\u5F53\u65E5", "24h": "24h", watch: "\u30A6\u30A9\u30C3\u30C1\u30EA\u30B9\u30C8", benchmarks: "\u4E3B\u8981\u6307\u6570", movers: "\u5909\u52D5\u306E\u5927\u304D\u3044\u9298\u67C4", cardMovers: "\u30A6\u30A9\u30C3\u30C1\u30EA\u30B9\u30C8\u306E\u5927\u304D\u306A\u5909\u52D5", empty: "\u30EA\u30B9\u30C8\u306F\u7A7A\u3067\u3059\u3002\u691C\u7D22\u3057\u3066\u9298\u67C4\u3092\u8FFD\u52A0\u3057\u3066\u304F\u3060\u3055\u3044", missing: "\u3053\u306E\u9298\u67C4\u306E\u73FE\u5728\u306E\u4FA1\u683C\u306F\u3042\u308A\u307E\u305B\u3093", noQuote: "\u4FA1\u683C\u306A\u3057", retry: "\u518D\u8A66\u884C", refresh: "\u66F4\u65B0", refreshed: "\u66F4\u65B0\u3057\u307E\u3057\u305F", search: "\u691C\u7D22", searchHint: "\u30B3\u30FC\u30C9\u30FB\u540D\u79F0\u3092\u691C\u7D22\uFF1ABTC\u3001\u9298\u67C4\u540D\u3001AAPL", searchTitle: "\u691C\u7D22\uFF1A{q}", none: "\u4E00\u81F4\u3059\u308B\u9298\u67C4\u306F\u3042\u308A\u307E\u305B\u3093", clear: "\u30EA\u30B9\u30C8\u306B\u623B\u308B", backSearch: "\u691C\u7D22\u306B\u623B\u308B", add: "\u30EA\u30B9\u30C8\u306B\u8FFD\u52A0", added: "{symbol} \u3092\u8FFD\u52A0\u3057\u307E\u3057\u305F", remove: "\u30EA\u30B9\u30C8\u304B\u3089\u524A\u9664", removed: "{symbol} \u3092\u524A\u9664\u3057\u307E\u3057\u305F", removeConfirm: "{symbol} \u3092\u30EA\u30B9\u30C8\u304B\u3089\u524A\u9664\u3057\u307E\u3059\u304B\uFF1F", watching: "\u767B\u9332\u6E08\u307F", code: "\u30B3\u30FC\u30C9", addHint: "\u682A\u5F0F\u306B\u306F\u5E02\u5834\u3092\u6307\u5B9A\uFF1Acn:600519\u3001hk:00700\u3001us:AAPL\u3002\u901A\u8CA8\u540D\u306E\u307F\u306E\u5834\u5408\u306F OKX \u306E USDT \u30DA\u30A2", look: "\u8868\u793A", order: "\u767B\u9332\u9806", sort: "\u4E26\u3079\u66FF\u3048", upSort: "\u4E0A\u6607\u7387", downSort: "\u4E0B\u843D\u7387", all: "\u3059\u3079\u3066", collapse: "\u9589\u3058\u308B", latest: "\u6700\u65B0\u4FA1\u683C", idle: "\u4F11\u5834 \xB7 {at} \u6642\u70B9", turnover: "\u58F2\u8CB7\u4EE3\u91D1", details: "\u8A73\u7D30", high: "\u9AD8\u5024", low: "\u5B89\u5024", open: "\u59CB\u5024", previous: "\u524D\u65E5\u7D42\u5024", volume: "\u51FA\u6765\u9AD8", range: "\u5024\u5E45", turnover_rate: "\u56DE\u8EE2\u7387", pe: "PER", cap: "\u6642\u4FA1\u7DCF\u984D", limit_up: "\u4E0A\u9650\u4FA1\u683C", limit_down: "\u4E0B\u9650\u4FA1\u683C", high52: "52\u9031\u9AD8\u5024", low52: "52\u9031\u5B89\u5024", kline: "\u30ED\u30FC\u30BD\u30AF\u8DB3", T: "\u5F53\u65E5\u63A8\u79FB", "15m": "15\u5206", "1H": "1\u6642\u9593", "4H": "4\u6642\u9593", "1D": "1\u65E5", "1W": "1\u9031", close: "\u7D42\u5024", noCandles: "\u3053\u306E\u671F\u9593\u306E\u4FA1\u683C\u30C7\u30FC\u30BF\u306F\u3042\u308A\u307E\u305B\u3093", noMinutes: "\u53D6\u5F15\u958B\u59CB\u524D\u306E\u305F\u3081\u5F53\u65E5\u30C7\u30FC\u30BF\u306F\u3042\u308A\u307E\u305B\u3093", span: "{n} \u672C\u306E\u5909\u52D5", sinceOpen: "\u53D6\u5F15\u958B\u59CB\u4EE5\u6765", spanHigh: "\u671F\u9593\u9AD8\u5024", spanLow: "\u671F\u9593\u5B89\u5024", alerts: "\u4FA1\u683C\u30A2\u30E9\u30FC\u30C8", alertsHint: "\u6BCE\u5206\u78BA\u8A8D\u3057\u3001\u5404\u30A2\u30E9\u30FC\u30C8\u306F\u4E00\u5EA6\u3060\u3051\u53D7\u4FE1\u7BB1\u306B\u5C4A\u304D\u307E\u3059", newAlert: "\u65B0\u898F\u30A2\u30E9\u30FC\u30C8", alert: "\u30A2\u30E9\u30FC\u30C8\u3092\u8A2D\u5B9A", alertKind: "\u6761\u4EF6", alertValue: "\u4FA1\u683C\u307E\u305F\u306F\u5909\u52D5\u7387", above: "\u4EE5\u4E0A", below: "\u4EE5\u4E0B", move: "\u7D76\u5BFE\u5909\u52D5\u7387\u4EE5\u4E0A", cancel: "\u30AD\u30E3\u30F3\u30BB\u30EB", waiting: "\u5F85\u6A5F\u4E2D", met: "\u901A\u77E5\u6E08\u307F", near: "\u76EE\u6A19\u4ED8\u8FD1", alertConfirm: "\u3053\u306E\u30A2\u30E9\u30FC\u30C8\u3092\u524A\u9664\u3057\u307E\u3059\u304B\uFF1F", alertAdded: "\u30A2\u30E9\u30FC\u30C8\u3092\u8A2D\u5B9A\u3057\u307E\u3057\u305F", alertRemoved: "\u30A2\u30E9\u30FC\u30C8\u3092\u524A\u9664\u3057\u307E\u3057\u305F", alertWhy: "\u30E6\u30FC\u30B6\u30FC\u8A2D\u5B9A\u306E\u4FA1\u683C\u30A2\u30E9\u30FC\u30C8", top: "\u5148\u982D\u3078", up: "\u4E0A\u3078", down: "\u4E0B\u3078", site: "{site} \u3067\u958B\u304F", tencent: "Tencent Securities", readAt: "\u516C\u958B\u5E02\u5834\u30C7\u30FC\u30BF \xB7 {at} \u306B\u53D6\u5F97", periodHint: "\u5909\u52D5\u306F\u524D\u65E5\u7D42\u5024\u6BD4\u3001\u6697\u53F7\u8CC7\u7523\u306F24\u6642\u9593\u524D\u306E\u4FA1\u683C\u6BD4\u3067\u3059", moreHits: "\u5148\u982D30\u4EF6\u306E\u307F\u8868\u793A\u3002\u691C\u7D22\u3092\u7D5E\u308A\u8FBC\u3093\u3067\u304F\u3060\u3055\u3044", loading: "\u5E02\u5834\u30C7\u30FC\u30BF\u3092\u8AAD\u307F\u8FBC\u307F\u4E2D", error: "\u5E02\u5834\u30C7\u30FC\u30BF\u3092\u53D6\u5F97\u3067\u304D\u307E\u305B\u3093", invalid: "\u30B3\u30FC\u30C9\u307E\u305F\u306F\u76EE\u6A19\u304C\u7121\u52B9\u3067\u3059", saveError: "\u5909\u66F4\u3092\u4FDD\u5B58\u3067\u304D\u307E\u305B\u3093\u3067\u3057\u305F", watchLimit: "\u767B\u9332\u3067\u304D\u308B\u9298\u67C4\u306F50\u4EF6\u307E\u3067\u3067\u3059", alertLimit: "\u30A2\u30E9\u30FC\u30C8\u306F50\u4EF6\u307E\u3067\u3067\u3059", duplicateAlert: "\u540C\u3058\u5F85\u6A5F\u4E2D\u306E\u30A2\u30E9\u30FC\u30C8\u304C\u3042\u308A\u307E\u3059", alertMet: "\u30A2\u30E9\u30FC\u30C8\u306E\u76EE\u6A19\u306F\u3059\u3067\u306B\u9054\u6210\u3055\u308C\u3066\u3044\u307E\u3059", cardOff: "\u6982\u8981\u30AB\u30FC\u30C9\u306F\u7121\u52B9\u3067\u3059", toolsQuote: "\u73FE\u5728\u4FA1\u683C\u3092\u53D6\u5F97\u3002symbols \u7701\u7565\u6642\u306F\u767B\u9332\u30EA\u30B9\u30C8\u3002\u5E02\u5834:\u30B3\u30FC\u30C9\u5F62\u5F0F\uFF08okx:BTC-USDT\u3001cn:600519\u3001hk:00700\u3001us:AAPL\uFF09\u3002", toolsSearch: "\u540D\u79F0\u30FB\u30B3\u30FC\u30C9\u3067\u516C\u958B\u5E02\u5834\u306E\u9298\u67C4\u3092\u691C\u7D22\u3002\u6B63\u78BA\u306A\u30B3\u30FC\u30C9\u304C\u4E0D\u660E\u306A\u3089\u5148\u306B\u691C\u7D22\u3057\u3066\u304F\u3060\u3055\u3044\u3002", toolsCandles: "\u8A2D\u5B9A\u30BF\u30A4\u30E0\u30BE\u30FC\u30F3\u306E\u958B\u59CB\u6642\u523B\u3068 OHLCV \u3092\u53E4\u3044\u9806\u3067\u53D6\u5F97\u3002\u672A\u5BFE\u5FDC\u306E\u671F\u9593\u306F\u62D2\u5426\u3057\u307E\u3059\u3002", quotesAt: "\u4FA1\u683C\u53D6\u5F97\uFF1A{at}", candlesAt: "{ref} {bar}\u3001{n} \u672C\u3001\u6642\u9593\u5E2F {zone}\uFF1A\u6642\u523B \u59CB\u5024 \u9AD8\u5024 \u5B89\u5024 \u7D42\u5024 \u51FA\u6765\u9AD8", badCode: "\u7121\u52B9\u306A\u30B3\u30FC\u30C9\uFF1A{code}", table: "\u4FA1\u683C\u30C7\u30FC\u30BF", priceAlert: "\u4FA1\u683C\u30A2\u30E9\u30FC\u30C8", publicAccount: "\u516C\u958B\u5E02\u5834\u30C7\u30FC\u30BF\u30FB\u30A2\u30AB\u30A6\u30F3\u30C8\u4E0D\u8981", rateError: "\u5E02\u5834\u30EA\u30AF\u30A8\u30B9\u30C8\u304C\u591A\u3059\u304E\u307E\u3059\u3002\u5F8C\u3067\u518D\u8A66\u884C\u3057\u3066\u304F\u3060\u3055\u3044", unitTenThousand: "\u4E07", unitHundredMillion: "\u5104", unitTrillion: "\u5146" };

// src/locale.ts
function localizeValue(value, locale) {
  const keys = ["unitTrillion", "unitHundredMillion", "unitTenThousand"];
  const matched = keys.find((key2) => value.includes(zh_Hans_default[key2]));
  return matched ? value.replace(zh_Hans_default[matched], translator(locale)(matched)) : value;
}
function translator(locale = "zh-Hans") {
  const dict = locale.startsWith("ja") ? ja_default : locale.startsWith("zh") ? zh_Hans_default : en_default;
  return (key2, values = {}) => (dict[key2] ?? key2).replace(/\{(\w+)\}/g, (_, k) => String(values[k] ?? ""));
}

// src/model.ts
var sourceIDs = ["okx", "cn", "hk", "us"];
var bars = ["T", "15m", "1H", "4H", "1D", "1W"];
var sourceBars = { okx: bars.slice(1), cn: ["T", "15m", "1H", "1D", "1W"], hk: ["T", "1D", "1W"], us: ["T", "1D", "1W"] };
var zones = { okx: "Asia/Hong_Kong", cn: "Asia/Shanghai", hk: "Asia/Hong_Kong", us: "America/New_York" };
var benchmarks = ["okx:BTC-USDT", "cn:000001.SH", "hk:HSI", "us:.IXIC"];
var key = (r) => `${r.source}:${r.symbol}`;
var number = (s) => typeof s === "string" ? floatResult(s).value : 0;
var emptyQuote = (symbol) => ({ symbol, name: "", kind: "", currency: "", last: 0, base: 0, high: 0, low: 0, turnover: 0, period: "", at: 0, decimals: 0, idle: false, facts: [] });
function normalize(source, typed) {
  let s = typed.trim().toUpperCase();
  if (source === "okx") {
    s = s.replace(/[/_]/g, "-").replace(/ /g, "");
    if (s && !s.includes("-")) s += "-USDT";
    return /^[A-Z0-9]{1,20}-[A-Z0-9]{1,20}(-SWAP)?$/.test(s) ? s : void 0;
  }
  if (source === "cn") {
    const m = /^(?:(SH|SZ|BJ))?(\d{6})(?:\.(SH|SZ|BJ))?$/.exec(s);
    if (!m || m[1] && m[3]) return;
    const ex = m[1] || m[3] || (/^(92|4|8)/.test(m[2]) ? "BJ" : /^[659]/.test(m[2]) ? "SH" : "SZ");
    return m[2] + "." + ex;
  }
  if (source === "hk") {
    s = s.replace(/\.HK$/, "").replace(/^HK(?=\d)/, "");
    return /^\d{1,5}$/.test(s) ? s.padStart(5, "0") : /^[A-Z]{2,10}$/.test(s) ? s : void 0;
  }
  for (const suffix of [".OQ", ".N", ".AM", ".PS"]) if (s.endsWith(suffix) && s.slice(0, -suffix.length) && s.slice(0, -suffix.length) !== ".") {
    s = s.slice(0, -suffix.length);
    break;
  }
  s = s.replace(/(.+)-([A-Z])$/, "$1.$2");
  return /^\.?[A-Z][A-Z0-9]{0,9}(\.[A-Z])?$/.test(s) ? s : void 0;
}
function parseRef(typed) {
  const s = typed.trim(), i = s.indexOf(":"), id = i < 0 ? "okx" : s.slice(0, i).trim().toLowerCase();
  if (!sourceIDs.includes(id)) return;
  const symbol = normalize(id, i < 0 ? s : s.slice(i + 1));
  return symbol ? { source: id, symbol } : void 0;
}
function readRefs(list) {
  return [...new Set(list.map(parseRef).filter((r) => !!r).map(key))].slice(0, 50);
}
var change = (q) => q?.last && q.base ? (q.last - q.base) / q.base : void 0;
var meets = (a, q) => q.last !== 0 && (a.kind === "above" ? q.last >= a.value : a.kind === "below" ? q.last <= a.value : change(q) !== void 0 && Math.abs(change(q)) * 100 >= a.value);
var near = (a, q) => !a.met && !!q.last && !meets(a, q) && (a.kind === "move" ? change(q) !== void 0 && Math.abs(change(q)) * 100 >= a.value * 0.8 : Math.abs(a.value - q.last) <= a.value * 0.01);
function clip(s, n) {
  const chars = Array.from(s);
  return chars.length > n ? chars.slice(0, n - 1).join("") + "\u2026" : s;
}
var cleanQuery = (s) => clip(s.trim(), 30);
function fmtFloat(f) {
  const a = Math.abs(f);
  if (!a) return "0";
  if (a >= 1e3) return f.toFixed(2);
  let s = a >= 1 ? f.toFixed(4) : f.toPrecision(4);
  if (s.includes("e")) {
    const [mantissa, exponent] = s.split("e");
    const digits = mantissa.replace("-", "").replace(".", "");
    const point = 1 + Number(exponent);
    s = (f < 0 ? "-" : "") + (point <= 0 ? "0." + "0".repeat(-point) + digits : digits.slice(0, point) + "." + digits.slice(point));
  }
  return s.replace(/(\.\d*?)0+$/, "$1").replace(/\.$/, "");
}
var price = (q, f) => !f ? "\u2014" : q.decimals > 0 ? f.toFixed(q.decimals) : fmtFloat(f);
var priceLike = (q, f, like) => f.toFixed(q.decimals || (fmtFloat(like).split(".")[1]?.length ?? 0));
var fmtChange = (f) => `${f >= 0 ? "+" : ""}${(f * 100).toFixed(2)}%`;
function compact(f) {
  for (const [n, s] of [[1e12, "T"], [1e9, "B"], [1e6, "M"], [1e3, "K"]]) if (Math.abs(f) >= n) return (f / n).toFixed(2) + s;
  return f.toFixed(2);
}
function fmtCN(f, locale = "zh-Hans") {
  const t = translator(locale);
  for (const [n, s] of [[1e12, "unitTrillion"], [1e8, "unitHundredMillion"], [1e4, "unitTenThousand"]]) if (f >= n) return (f / n).toFixed(2) + t(s);
  return f.toFixed(0);
}
var tone = (cfg, c) => c === 0 ? "neutral" : c > 0 !== (cfg.colors === "green_up") ? "danger" : "positive";
var parts = timeParts;
function localTime(s, zone) {
  const m = /^(\d{4})[-/]?(\d{2})[-/]?(\d{2})(?:[ T]?(\d{2}):?(\d{2})(?::?(\d{2}))?)?$/.exec(s);
  if (!m || m[0].length !== s.length) return 0;
  const [y, mo, d, h, mi, sec] = m.slice(1).map((x) => Number(x || 0));
  const wanted = utcDate(y, mo, d, h, mi, sec);
  if (mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || sec > 59) return 0;
  let at = wanted;
  for (let i = 0; i < 3; i++) {
    const p2 = parts(at, zone);
    const wall = utcDate(+p2.year, +p2.month, +p2.day, +p2.hour, +p2.minute, +p2.second);
    at += wanted - wall;
  }
  const p = parts(at, zone);
  return +p.year === y && +p.month === mo && +p.day === d && +p.hour === h && +p.minute === mi && +p.second === sec ? at : 0;
}
function stamp(at, zone, style = "full") {
  const p = parts(at, zone);
  return style === "time" ? `${p.hour}:${p.minute}` : style === "day" ? `${+p.month}/${+p.day}` : style === "short" ? `${+p.month}/${+p.day} ${p.hour}:${p.minute}` : `${p.year}-${p.month}-${p.day} ${p.hour}:${p.minute}`;
}

// src/okx.ts
var tickerFields = ["instId", "instType", "last", "open24h", "high24h", "low24h", "vol24h", "volCcy24h", "ts"];
var tickerType = { fields: Object.fromEntries(tickerFields.map((field) => [field, "string"])) };
function validTicker(t) {
  if (!jsonObject(t)) throw new ReadError("invalid", "Invalid OKX ticker");
  typedJSON(t, tickerType);
}
function parseOKX(text, candles) {
  const raw = numericJSON(text);
  if (!jsonObject(raw)) throw new ReadError("invalid", "Invalid OKX response");
  const env = typedJSON(raw, { fields: { code: "string", msg: "string", data: "raw" } });
  if (env.code === "0") {
    if (!Array.isArray(env.data)) throw new ReadError("invalid", "Invalid OKX data");
    if (!candles && env.data.some((row) => !jsonObject(row))) throw new ReadError("invalid", "Invalid OKX ticker");
    env.data = typedJSON(env.data, { array: candles ? { array: "string" } : tickerType });
  }
  return env;
}
var instType = (id) => id.endsWith("-SWAP") ? "SWAP" : "SPOT";
function ticker(t) {
  validTicker(t);
  const id = String(t.instId ?? "");
  const q = { ...emptyQuote(id), kind: instType(id) === "SWAP" ? "swap" : "spot", currency: id.split("-")[1] ?? "", last: number(t.last), base: number(t.open24h), high: number(t.high24h), low: number(t.low24h), turnover: number(t.volCcy24h), period: "24h", at: milliseconds(typeof t.ts === "string" ? int64(t.ts) : void 0) };
  if (instType(id) === "SWAP") q.turnover *= q.last;
  return q;
}
var OKX = class {
  constructor(http) {
    this.http = http;
    this.missing = new Cache(http.clock);
  }
  id = "okx";
  bars = sourceBars.okx;
  missing;
  normalize = (s) => normalize(this.id, s);
  url = (s) => `https://www.okx.com/trade-${instType(s) === "SWAP" ? "swap" : "spot"}/${s.toLowerCase()}`;
  async get(path, fresh, signal) {
    const url = "https://www.okx.com/api/v5/market/" + path;
    const env = await this.http.json(url, fresh, 8 * 1024 * 1024, signal, (env2) => {
      if (env2?.code !== "0") {
        if (env2?.code === "51001") throw notFound();
        if (env2?.code === "50011") throw new ReadError("rate_limited", "OKX rate limit");
        throw new ReadError("invalid", "Invalid OKX response");
      }
      if (!Array.isArray(env2.data)) throw new ReadError("invalid", "Invalid OKX data");
      for (const row of env2.data) {
        if (!path.startsWith("candles?")) validTicker(row);
        else if (row !== null && (!Array.isArray(row) || row.some((x) => x !== null && typeof x !== "string"))) throw new ReadError("invalid", "Invalid OKX candle row");
      }
    }, (text) => parseOKX(text, path.startsWith("candles?")));
    return env.data;
  }
  async quote(id, fresh, signal) {
    if (this.missing.get(id, fresh)) throw notFound();
    try {
      const list = await this.get("ticker?" + new URLSearchParams({ instId: id }), fresh, signal);
      if (!list.length) throw notFound();
      return ticker(list[0]);
    } catch (err) {
      if (err instanceof ReadError && err.code === "not_found") this.missing.put(id, true);
      throw err;
    }
  }
  async quotes(ids, fresh, signal) {
    const out = /* @__PURE__ */ new Map();
    if (ids.length <= 5) {
      const answers = await Promise.allSettled(ids.map(async (id) => [id, await this.quote(id, fresh, signal)]));
      let first;
      for (const answer of answers) {
        if (answer.status === "fulfilled") out.set(...answer.value);
        else if (!(answer.reason instanceof ReadError && answer.reason.code === "not_found")) first ??= answer.reason;
      }
      if (first) throw first;
    } else {
      const want = new Set(ids);
      for (const type of ["SPOT", "SWAP"]) if (ids.some((id) => instType(id) === type)) {
        for (const t of await this.get("tickers?instType=" + type, fresh, signal)) if (want.has(t.instId)) out.set(t.instId, ticker(t));
      }
    }
    return out;
  }
  async search(query, n, fresh, signal) {
    const q = query.trim().toUpperCase().replace(/[/_]/g, "-").replace(/ /g, "");
    if (!q || n <= 0) return [];
    const hits = [];
    for (const type of ["SPOT", "SWAP"]) for (const t of await this.get("tickers?instType=" + type, fresh, signal)) {
      const id = t.instId;
      if (typeof id !== "string" || type === "SWAP" && !id.endsWith("-SWAP")) continue;
      const rank = id === q || id.split("-")[0] === q ? 0 : id.startsWith(q) ? 1 : id.includes(q) ? 2 : -1;
      if (rank >= 0) hits.push({ quote: ticker(t), rank });
    }
    return hits.sort((a, b) => a.rank - b.rank || b.quote.turnover - a.quote.turnover).slice(0, n).map((h) => h.quote);
  }
  async candles(id, bar, n, fresh, signal) {
    if (!this.bars.includes(bar)) throw new ReadError("invalid", "Unsupported OKX period");
    const rows = await this.get("candles?" + new URLSearchParams({ bar, instId: id, limit: String(n) }), fresh, signal);
    return rows.slice().reverse().flatMap((r) => {
      if (!Array.isArray(r) || r.length < 6 || typeof r[0] !== "string") return [];
      const exact = int64(r[0]), values = r.slice(1, 6).map((x) => typeof x === "string" ? parseFloat64(x) : void 0);
      if (exact === void 0 || values.some((x) => x === void 0)) return [];
      const [open, high, low, close, vol] = values;
      return [{ at: milliseconds(exact), open, high, low, close, vol }];
    });
  }
};

// src/tencent.ts
var candleData = { map: { map: "raw" } };
var minuteData = { map: { fields: { data: { fields: { date: "string", data: { array: "string" } } } } } };
function parseTencent(text, minutes) {
  return typedJSON(numericJSON(text), { fields: { code: "integer", msg: "string", data: minutes ? minuteData : candleData } });
}
var record = (value) => !!value && typeof value === "object" && !Array.isArray(value) && !(value instanceof JSONNumber);
function kindName(s) {
  return s.includes("ETF") ? "ETF" : s === "ZS" ? "index" : /^(GP-FUND|LOF|JJ)/.test(s) ? "fund" : s.startsWith("GP") ? "stock" : "";
}
var decimals = (m, kind, last) => m === "cn" && ["ETF", "fund"].includes(kind) || m === "hk" && last < 1 ? 3 : m === "us" && last < 1 ? 4 : 2;
var Tencent = class {
  constructor(http) {
    this.http = http;
    this.usCode = new Cache(http.clock);
  }
  usCode;
  market(id) {
    return new TxMarket(this, id);
  }
  async quoteLines(codes, fresh, signal) {
    const raw = await this.http.get("https://qt.gtimg.cn/q=" + codes.join(","), fresh, void 0, signal);
    const out = /* @__PURE__ */ new Map();
    for (const line of new TextDecoder("gbk").decode(raw).split(";")) {
      const i = line.indexOf("="), k = line.slice(0, i).trim();
      if (i < 0 || !k.startsWith("v_") || k === "v_pv_none_match") continue;
      out.set(k.slice(2), line.slice(i + 1).trim().replace(/^"|"$/g, "").split("~"));
    }
    return out;
  }
};
var TxMarket = class {
  constructor(tx, id) {
    this.tx = tx;
    this.id = id;
    this.bars = sourceBars[id];
  }
  bars;
  normalize = (s) => normalize(this.id, s);
  code(sym, bars2 = false) {
    if (this.id === "cn") {
      const [num, ex] = sym.split(".");
      return ex.toLowerCase() + num;
    }
    if (this.id === "hk") return (bars2 ? "hk" : "r_hk") + sym;
    return "us" + (bars2 ? this.tx.usCode.entries.get(sym)?.value ?? sym : sym);
  }
  url = (sym) => "https://gu.qq.com/" + this.code(sym, true);
  parseQuote(sym, fields2) {
    const at = (i) => fields2[i]?.trim() ?? "", num = (i) => number(at(i));
    if (fields2.length < 50 || !num(3)) return;
    const q = { ...emptyQuote(sym), name: at(1), last: num(3), base: num(4), high: num(33), low: num(34), period: "day" };
    let kind = "";
    if (this.id === "cn") {
      kind = at(61);
      q.currency = at(82);
      const p = at(35).split("/");
      if (p.length === 3) q.turnover = number(p[2]);
    } else if (this.id === "hk") {
      kind = at(63);
      q.currency = at(75);
      q.turnover = num(37);
    } else {
      kind = sym.startsWith(".") ? "ZS" : at(56);
      q.currency = at(35);
      q.turnover = num(37);
      if (at(2)) this.tx.usCode.put(sym, at(2));
    }
    if (kind === "ZS") q.turnover = this.id === "hk" ? q.turnover * 1e4 : this.id === "us" ? 0 : q.turnover;
    q.currency ||= { cn: "CNY", hk: "HKD", us: "USD" }[this.id];
    q.kind = kindName(kind);
    q.at = localTime(at(30), zones[this.id]);
    q.idle = !!q.at && this.tx.http.clock() - Number(q.at) > 20 * 6e4;
    q.decimals = decimals(this.id, q.kind, q.last);
    q.facts = this.facts(q, num);
    return q;
  }
  facts(q, num) {
    const facts = [], index = q.kind === "index";
    const add = (label, value, format = (v) => price(q, v)) => {
      if (value > 0) facts.push({ label, value: format(value) });
    }, pct = (v) => v.toFixed(2) + "%";
    add("open", num(5));
    add("previous", num(4));
    add("volume", this.id === "cn" ? num(6) * 100 : this.id === "hk" && index ? 0 : num(6), fmtCN);
    add("range", num(43), pct);
    if (!index) {
      add("turnover_rate", num(this.id === "hk" ? 59 : 38), pct);
      add("pe", num(39), (v) => v.toFixed(2));
      add("cap", num(45) * 1e8, (v) => fmtCN(v) + " " + q.currency);
      if (this.id === "cn") {
        add("limit_up", num(47));
        add("limit_down", num(48));
      }
    }
    add("high52", num(this.id === "cn" ? 67 : 48));
    add("low52", num(this.id === "cn" ? 68 : 49));
    return facts;
  }
  async quotes(syms, fresh, signal) {
    const out = /* @__PURE__ */ new Map();
    if (!syms.length) return out;
    const codes = syms.map((s) => this.code(s)), lines = await this.tx.quoteLines(codes, fresh, signal);
    syms.forEach((s, i) => {
      const f = lines.get(codes[i]), q = f && this.parseQuote(s, f);
      if (q) out.set(s, q);
    });
    return out;
  }
  async quote(sym, fresh, signal) {
    const q = (await this.quotes([sym], fresh, signal)).get(sym);
    if (!q) throw notFound();
    return q;
  }
  listed(market, type) {
    return (this.id === "cn" ? ["sh", "sz", "bj"].includes(market) : market === this.id) && !!kindName(type);
  }
  async search(query, n, fresh, signal) {
    if (!query.trim() || n <= 0) return [];
    const body = await this.tx.http.get("https://smartbox.gtimg.cn/s3/?" + new URLSearchParams({ c: "1", q: query.trim(), t: "all", v: "2" }), fresh, void 0, signal);
    const raw = new TextDecoder().decode(body).trim(), i = raw.indexOf("=");
    if (i < 0) throw new ReadError("invalid", "Invalid Tencent search");
    let v = raw.slice(i + 1).replace(/;$/, "");
    try {
      v = JSON.parse(v);
    } catch {
      v = v.replace(/^"|"$/g, "");
    }
    if (typeof v !== "string") throw new ReadError("invalid", "Invalid Tencent search");
    if (!v || v === "N") return [];
    const syms = [];
    for (const item of v.split("^")) {
      const f = item.split("~");
      if (f.length < 5 || !this.listed(f[0], f[4])) continue;
      const sym = this.normalize(f[1] + (this.id === "cn" ? "." + f[0] : ""));
      if (sym && !syms.includes(sym)) syms.push(sym);
      if (syms.length === n) break;
    }
    const quotes = await this.quotes(syms, fresh, signal);
    return syms.flatMap((s) => quotes.has(s) ? [quotes.get(s)] : []);
  }
  async envelope(url, code, fresh, signal, validate) {
    return this.tx.http.json(url, fresh, void 0, signal, (env) => {
      if (!record(env) || env.code != null && jsonInteger(env.code) !== 0n || env.msg != null && typeof env.msg !== "string") throw new ReadError("invalid", "Tencent provider error");
      if (env.data == null || Array.isArray(env.data) && !env.data.length) throw notFound();
      if (!record(env.data)) throw new ReadError("invalid", "Invalid Tencent data");
      for (const data of Object.values(env.data)) {
        if (data !== null && !record(data)) throw new ReadError("invalid", "Invalid Tencent instrument data");
        validate(data ?? {});
      }
      if (!Object.hasOwn(env.data, code)) throw notFound();
    }, (text) => parseTencent(text, url.includes("/minute/query?")));
  }
  async candles(sym, bar, n, fresh, signal) {
    if (!this.bars.includes(bar)) throw new ReadError("invalid", "Unsupported stock period");
    if (this.id === "us" && !this.tx.usCode.entries.has(sym)) await this.quote(sym, 36e5, signal);
    const code = this.code(sym, true);
    if (bar === "T") return this.minutes(code, fresh, signal);
    const minute = ["15m", "1H"].includes(bar), rowKey = minute ? bar === "15m" ? "m15" : "m60" : bar === "1D" ? "day" : "week";
    const url = minute ? `https://ifzq.gtimg.cn/appstock/app/kline/mkline?param=${code},${rowKey},,${n}` : `https://web.ifzq.gtimg.cn/appstock/app/fqkline/get?param=${code},${rowKey},,,${n},qfq`;
    const rowsFor = (d) => Object.hasOwn(d, "qfq" + rowKey) ? d["qfq" + rowKey] : d[rowKey];
    const env = await this.envelope(url, code, fresh, signal, (d) => {
      const rows2 = rowsFor(d);
      if (rows2 != null && (!Array.isArray(rows2) || rows2.some((r) => r !== null && !Array.isArray(r)))) throw new ReadError("invalid", "Invalid Tencent candles");
    });
    const rows = rowsFor(env.data[code] ?? {}) ?? [];
    return rows.flatMap((r) => {
      if (!Array.isArray(r) || r.length < 6 || r.slice(0, 6).some((x) => typeof x !== "string")) return [];
      const values = r.slice(1, 6).map((x) => parseFloat64(x));
      if (values.some((x) => x === void 0)) return [];
      let at = localTime(r[0], zones[this.id]);
      if (!at) return [];
      if (minute) at -= bar === "15m" ? 9e5 : 36e5;
      else if (bar === "1W") {
        const p = parts(at, zones[this.id]);
        const date = new Date(Date.UTC(+p.year, +p.month - 1, +p.day));
        date.setUTCDate(date.getUTCDate() - (date.getUTCDay() + 6) % 7);
        at = localTime(date.toISOString().slice(0, 10), zones[this.id]);
      }
      const [open, close, high, low, vol] = values;
      return [{ at, open, close, high, low, vol: vol * (this.id === "cn" ? 100 : 1) }];
    }).slice(-n);
  }
  async minutes(code, fresh, signal) {
    const env = await this.envelope("https://web.ifzq.gtimg.cn/appstock/app/minute/query?" + new URLSearchParams({ code }), code, fresh, signal, (d) => {
      if (d.data == null) return;
      if (!record(d.data) || d.data.date != null && typeof d.data.date !== "string" || d.data.data != null && (!Array.isArray(d.data.data) || d.data.data.some((line) => line !== null && typeof line !== "string"))) throw new ReadError("invalid", "Invalid minute response");
    });
    const data = env.data[code]?.data;
    if (!data || typeof data.date !== "string" || !localTime(data.date, zones[this.id])) return [];
    const minutes = [];
    for (const line of data.data ?? []) {
      if (typeof line !== "string") continue;
      const f = line.trim().split(/\s+/);
      if (f.length < 3 || !/^\d{4}$/.test(f[0])) continue;
      const price2 = parseFloat64(f[1]), cum = parseFloat64(f[2]);
      if (price2 === void 0 || cum === void 0 || price2 === 0) continue;
      const at = localTime(data.date + f[0], zones[this.id]);
      if (at) minutes.push({ at, price: price2, cum });
    }
    const per = Math.ceil(minutes.length / 120), out = [];
    let prev = 0;
    for (let i = 0; i < minutes.length; i += per) {
      const g = minutes.slice(i, i + per), last = g.at(-1);
      out.push({ at: g[0].at, open: g[0].price, close: last.price, high: Math.max(...g.map((m) => m.price)), low: Math.min(...g.map((m) => m.price)), vol: Math.max(last.cum - prev, 0) * (this.id === "cn" ? 100 : 1) });
      prev = last.cum;
    }
    return out;
  }
};

// src/errors.ts
var MutationError = class extends Error {
  constructor(code, message) {
    super(message);
    this.code = code;
  }
};
function actionError(error) {
  if (error instanceof MutationError) return error.code;
  if (error instanceof ReadError) return error.code === "not_found" ? "missing" : error.code === "rate_limited" ? "rateError" : "error";
  return "error";
}

// src/state.ts
var alertType = { fields: { id: "string", ref: "string", kind: "string", value: "float", created: "time", met: { nullable: "time" }, price: "float", change: "float", name: "string", decimals: "integer" } };
function parseStateJSON(text) {
  const value = numericJSON(text);
  if (value === null) return null;
  const type = Array.isArray(value) ? { array: value.some((item) => item !== null && typeof item === "object") ? alertType : "string" } : { fields: { schema_version: "integer", watchlist: { array: "string" }, alerts: { array: alertType } } };
  return ordinaryJSON(typedJSON(value, type));
}
function validateState(value) {
  const s = value;
  if (!s || s.schema_version !== 1 || !Array.isArray(s.watchlist) || s.watchlist.length > 50 || s.watchlist.some((x) => typeof x !== "string" || !parseRef(x)) || new Set(s.watchlist).size !== s.watchlist.length || !Array.isArray(s.alerts) || s.alerts.length > 50) throw new Error("Invalid stored market state; original blob retained");
  const ids = /* @__PURE__ */ new Set();
  for (const a of s.alerts) {
    if (!a || typeof a.id !== "string" || !/^[a-zA-Z0-9_-]{1,128}$/.test(a.id) || ids.has(a.id) || typeof a.ref !== "string" || !parseRef(a.ref) || !["above", "below", "move"].includes(a.kind) || !Number.isFinite(a.value) || a.value <= 0 || typeof a.created !== "string" || !validRFC3339(a.created) || a.met !== void 0 && (typeof a.met !== "string" || !validRFC3339(a.met)) || [a.price, a.change, a.decimals].some((x) => x !== void 0 && !Number.isFinite(x)) || a.decimals !== void 0 && (!Number.isInteger(a.decimals) || a.decimals < 0 || a.decimals > 12) || a.kind === "move" && a.value > 100 || a.name !== void 0 && (typeof a.name !== "string" || a.name.length > 512)) throw new Error("Invalid stored alerts; original blob retained");
    ids.add(a.id);
  }
  const normalized = structuredClone(s);
  normalized.watchlist = readRefs(s.watchlist);
  if (normalized.watchlist.length !== s.watchlist.length) throw new Error("Duplicate normalized watchlist references; original blob retained");
  normalized.alerts = normalized.alerts.map((a) => ({ ...a, ref: key(parseRef(a.ref)) }));
  return normalized;
}
function encodeState(state) {
  const bytes = new TextEncoder().encode(JSON.stringify(validateState(state)));
  if (bytes.length > 256 * 1024) throw new Error("Stored market state too large");
  let binary = "";
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary);
}
function decodeState(data) {
  if (data.length > 35e4) throw new Error("Stored market state too large");
  const bytes = Uint8Array.from(atob(data), (c) => c.charCodeAt(0));
  return validateState(parseStateJSON(new TextDecoder("utf-8", { fatal: true }).decode(bytes)));
}
function legacyState(watchlist, alerts) {
  if (watchlist === null) watchlist = [];
  if (alerts === null) alerts = [];
  if (!Array.isArray(watchlist) || watchlist.some((x) => typeof x !== "string" || !parseRef(x))) throw new Error("Invalid legacy watchlist; review before import");
  const refs = readRefs(watchlist);
  if (watchlist.length > 50 || refs.length !== new Set(watchlist.map((x) => {
    const r = parseRef(x);
    return r.source + ":" + r.symbol;
  })).size) throw new Error("Legacy watchlist exceeds 50 entries; review before import");
  return validateState({ schema_version: 1, watchlist: refs, alerts });
}
var StateStore = class {
  constructor(hub) {
    this.hub = hub;
  }
  state = { schema_version: 1, watchlist: [], alerts: [] };
  serial = Promise.resolve();
  async load(seed) {
    const stored = await this.hub.call("connector.secrets.load", { contribution: "markets" });
    if (stored.data) this.state = decodeState(stored.data);
    else {
      const initial = { schema_version: 1, watchlist: readRefs(seed), alerts: [] };
      await this.save(initial);
      this.state = initial;
    }
  }
  snapshot() {
    return structuredClone(this.state);
  }
  save(s) {
    return this.hub.call("connector.secrets.save", { contribution: "markets", data: encodeState(s) });
  }
  transaction(fn, keepOnFailure = false) {
    const work = this.serial.then(async () => {
      const draft = this.snapshot(), result = fn(draft), next = validateState(draft);
      if (JSON.stringify(next) === JSON.stringify(this.state)) return result;
      try {
        await this.save(next);
      } catch (err) {
        if (keepOnFailure) this.state = next;
        throw new MutationError("saveError", "Could not save encrypted market state");
      }
      this.state = next;
      return result;
    });
    this.serial = work.catch(() => {
    });
    return work;
  }
};

// src/service.ts
var Markets = class {
  constructor(hub, config = {}, options = {}) {
    this.hub = hub;
    if (!config || typeof config !== "object" || Array.isArray(config) || config.watchlist !== void 0 && (!Array.isArray(config.watchlist) || config.watchlist.some((x) => typeof x !== "string")) || [config.bar, config.colors, config.tz, config.triage_rules].some((x) => x !== void 0 && typeof x !== "string") || config.overview_card !== void 0 && typeof config.overview_card !== "boolean") throw new Error("Invalid markets configuration");
    this.cfg = { ...config, watchlist: config.watchlist?.slice() };
    if (config.tz && config.tz !== "Local") new Intl.DateTimeFormat("en", { timeZone: config.tz });
    this.clock = options.clock ?? Date.now;
    this.http = new HTTP(options.fetch ?? trustedFetch, this.clock);
    const tx = new Tencent(this.http);
    this.sources = { okx: new OKX(this.http), cn: tx.market("cn"), hk: tx.market("hk"), us: tx.market("us") };
    this.store = new StateStore(hub);
    this.id = options.id ?? (() => Array.from(crypto.getRandomValues(new Uint8Array(6)), (n) => n.toString(16).padStart(2, "0")).join(""));
    this.interval = options.interval ?? ((fn, ms) => {
      const timer = setInterval(fn, ms);
      return () => clearInterval(timer);
    });
  }
  http;
  sources;
  store;
  cfg;
  lastOK = 0;
  lastErr;
  lastErrAt = 0;
  stopLoop;
  loopController;
  alertCheck;
  listeners = /* @__PURE__ */ new Set();
  closed = false;
  id;
  interval;
  clock;
  async initialize() {
    await this.store.load(this.cfg.watchlist?.length ? this.cfg.watchlist : ["BTC-USDT", "ETH-USDT", "SOL-USDT"]);
    if (!this.closed && !this.stopLoop) {
      this.loopController = new AbortController();
      this.stopLoop = this.interval(() => {
        void this.checkAlerts(3e4, this.loopController?.signal).catch((err) => this.note(err));
      }, 6e4);
    }
  }
  dispose() {
    this.closed = true;
    this.stopLoop?.();
    this.stopLoop = void 0;
    this.loopController?.abort();
    this.listeners.clear();
  }
  subscribe(fn) {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  }
  changed() {
    if (this.closed) return;
    this.hub.invalidate("watchlist");
    this.hub.invalidate("card");
    for (const fn of this.listeners) fn();
  }
  watchlist() {
    return this.store.snapshot().watchlist;
  }
  alerts() {
    return this.store.snapshot().alerts;
  }
  defaultBar(id) {
    return [this.cfg.bar, "1H", "1D", sourceBars[id][0]].find((b) => b && sourceBars[id].includes(b));
  }
  note(err) {
    if (!err) {
      this.lastOK = this.clock();
      this.lastErr = void 0;
    } else if (!isCancelled(err) && !(err instanceof ReadError && err.code === "not_found")) {
      this.lastErr = err;
      this.lastErrAt = this.clock();
    }
  }
  status(locale = "zh-Hans") {
    const t = translator(locale), failed = !!this.lastErr && this.lastErrAt >= this.lastOK;
    return { state: failed ? this.lastErr instanceof ReadError && this.lastErr.code === "rate_limited" ? "rate_limited" : "error" : "ok", account: t("publicAccount"), ...this.lastOK ? { last_sync: new Date(this.lastOK).toISOString() } : {}, ...failed ? { error: t(this.lastErr instanceof ReadError && this.lastErr.code === "rate_limited" ? "rateError" : "error") } : {} };
  }
  async quote(ref, fresh = 1e4, signal) {
    try {
      const q = await this.sources[ref.source].quote(ref.symbol, fresh, signal);
      this.note();
      return q;
    } catch (err) {
      this.note(err);
      throw err;
    }
  }
  async quotes(refs, fresh = 1e4, partial = false, signal) {
    const quotes = /* @__PURE__ */ new Map(), errors = /* @__PURE__ */ new Map();
    for (const id of sourceIDs) {
      const group = refs.map(parseRef).filter((r) => r?.source === id);
      if (!group.length) continue;
      try {
        const got = await this.sources[id].quotes([...new Set(group.map((r) => r.symbol))], fresh, signal);
        this.note();
        for (const [symbol, q] of got) quotes.set(key({ source: id, symbol }), q);
      } catch (err) {
        this.note(err);
        if (isCancelled(err) || signal?.aborted || !partial) throw err;
        errors.set(id, err);
      }
    }
    return { quotes, errors };
  }
  async search(q, n = 30, fresh = 1e4, signal) {
    const lists = [];
    let first;
    for (const id of sourceIDs) {
      try {
        const got = await this.sources[id].search(q, n, fresh, signal);
        this.note();
        lists.push(got.map((quote) => ({ ref: { source: id, symbol: quote.symbol }, quote })));
      } catch (err) {
        this.note(err);
        if (signal?.aborted || isCancelled(err)) throw err;
        first ??= err;
      }
    }
    if (!lists.length && first) throw first;
    const out = [];
    for (let i = 0; out.length < n; i++) {
      let more = false;
      for (const list of lists) if (list[i] && out.length < n) {
        out.push(list[i]);
        more = true;
      }
      if (!more) break;
    }
    return out;
  }
  async candles(r, bar, n, signal) {
    try {
      const out = await this.sources[r.source].candles(r.symbol, bar, n, 3e4, signal);
      this.note();
      return out;
    } catch (err) {
      this.note(err);
      throw err;
    }
  }
  async addWatch(r, signal) {
    await this.quote(r, 1e4, signal);
    if (signal?.aborted) throw signal.reason;
    await this.store.transaction((next) => {
      const ref = key(r);
      if (next.watchlist.includes(ref)) return;
      if (next.watchlist.length >= 50) throw new MutationError("watchLimit", "Watchlist limit: 50");
      next.watchlist.push(ref);
    });
    this.changed();
  }
  async removeWatch(r) {
    await this.store.transaction((next) => {
      next.watchlist = next.watchlist.filter((s) => s !== key(r));
    });
    this.changed();
  }
  async moveWatch(r, by) {
    await this.store.transaction((next) => {
      const w = next.watchlist, i = w.indexOf(key(r));
      if (i < 0) return;
      if (by === "top") {
        const at = w.flatMap((s, i2) => parseRef(s)?.source === r.source ? [i2] : []), group = [key(r), ...at.map((i2) => w[i2]).filter((s) => s !== key(r))];
        at.forEach((i2, j) => {
          w[i2] = group[j];
        });
      } else for (let j = i + by; j >= 0 && j < w.length; j += by) if (parseRef(w[j])?.source === r.source) {
        [w[i], w[j]] = [w[j], w[i]];
        break;
      }
    });
    this.changed();
  }
  async addAlert(r, kind, raw, signal) {
    const value = typeof raw === "string" ? parseFloat64(raw.trim().replace(/%$/, "")) ?? NaN : NaN;
    if (!["above", "below", "move"].includes(kind) || !Number.isFinite(value) || value <= 0 || kind === "move" && value > 100) throw new MutationError("invalid", "Invalid alert target");
    const quote = await this.quote(r, 1e4, signal), alert = { id: this.id(), ref: key(r), kind, value, created: new Date(this.clock()).toISOString(), name: quote.name, decimals: quote.decimals };
    if (meets(alert, quote)) throw new MutationError("alertMet", "Alert target already met");
    if (signal?.aborted) throw signal.reason;
    await this.store.transaction((next) => {
      if (next.alerts.length >= 50) throw new MutationError("alertLimit", "Alert limit: 50");
      if (next.alerts.some((a) => !a.met && a.ref === alert.ref && a.kind === kind && a.value === value)) throw new MutationError("duplicateAlert", "Duplicate pending alert");
      next.alerts.push(alert);
    });
    this.changed();
    return alert;
  }
  async removeAlert(id) {
    await this.store.transaction((next) => {
      next.alerts = next.alerts.filter((a) => a.id !== id);
    });
    this.changed();
  }
  checkAlerts(fresh = 3e4, signal) {
    return this.alertCheck ??= this.performAlertCheck(fresh, signal).finally(() => {
      this.alertCheck = void 0;
    });
  }
  async performAlertCheck(fresh, signal) {
    const pending = this.alerts().filter((a) => !a.met);
    if (!pending.length) return;
    const { quotes } = await this.quotes([...new Set(pending.map((a) => a.ref))], fresh, true, signal);
    if (signal?.aborted) throw signal.reason;
    let met = false;
    try {
      await this.store.transaction((next) => {
        for (const a of next.alerts) {
          const q = quotes.get(a.ref);
          if (a.met || !q || !meets(a, q)) continue;
          a.met = new Date(this.clock()).toISOString();
          a.price = q.last;
          a.change = change(q) ?? 0;
          a.name = q.name;
          met = true;
        }
      }, true);
    } catch (err) {
      this.note(err);
    }
    if (met && !this.closed) {
      this.hub.notify("connector.changed", { contribution: "markets", scope: "inbox" });
      this.changed();
    }
  }
  inbox(locale = "zh-Hans") {
    const t = translator(locale), zone = this.cfg.tz || "Local", rules = Array.from((this.cfg.triage_rules ?? "").replace(/\\n/g, "\n").trim()).slice(0, 4e3).join("");
    return { things: this.alerts().filter((a) => a.met && this.clock() - Date.parse(a.met) < 7 * 864e5).map((a) => {
      const r = parseRef(a.ref), q = { decimals: a.decimals ?? 0 }, name = [a.name, r.symbol].filter(Boolean).join(" "), what = `${t(a.kind)} ${a.kind === "move" ? a.value + "%" : price(q, a.value)}`;
      return { thread: "alert:" + a.id, kind: "price_alert", label: t("priceAlert"), title: name + " " + what, preview: `${name} ${price(q, a.price ?? 0)}, ${fmtChange(a.change ?? 0)}; ${stamp(Date.parse(a.created), zone, "short")}`, sender: t("markets"), at: a.met, last: a.met, tier: "action", why: t("alertWhy"), tags: [{ key: "symbol", value: a.ref, name }], extra: { rule_tier: "action" }, ...rules ? { rules } : {}, url: this.sources[r.source].url(r.symbol) };
    }) };
  }
};

// src/tools.ts
function tools(locale = "zh-Hans") {
  const t = translator(locale);
  return [
    { name: "markets_quote", description: t("toolsQuote"), input_schema: { type: "object", properties: { symbols: { type: "array", items: { type: "string" }, maxItems: 50 } } } },
    { name: "markets_search", description: t("toolsSearch"), input_schema: { type: "object", required: ["query"], properties: { query: { type: "string" }, limit: { type: "integer", minimum: 1, maximum: 30 } } } },
    { name: "markets_candles", description: t("toolsCandles"), input_schema: { type: "object", required: ["symbol"], properties: { symbol: { type: "string" }, bar: { type: "string", enum: bars }, limit: { type: "integer", minimum: 1, maximum: 100 } } } }
  ];
}
function quoteLine(ref, q, locale) {
  const t = translator(locale), c = change(q);
  return `- ${key(ref)}${q.name ? ` (${q.name})` : ""}: ${t("latest")} ${price(q, q.last)} ${q.currency}` + (q.idle && q.at ? ` (${t("idle", { at: stamp(q.at, zones[ref.source]) })})` : "") + `, ${t(q.period)} ${c === void 0 ? "\u2014" : fmtChange(c)}, ${t("high")} ${price(q, q.high)}, ${t("low")} ${price(q, q.low)}` + (q.turnover > 0 ? `, ${t("turnover")} ${compact(q.turnover)} ${q.currency}` : "") + (q.facts.length ? "; " + q.facts.map((f) => `${t(f.label)} ${localizeValue(f.value, locale)}`).join(", ") : "");
}
var bounded = (n, most, fallback) => (typeof n === "bigint" || typeof n === "number" && Number.isInteger(n)) && n > 0 && n <= most ? Number(n) : fallback;
var stringArg = (args, name) => args[name] === void 0 || args[name] === null ? "" : typeof args[name] === "string" ? args[name] : (() => {
  throw new ExtError("Invalid market tool arguments", "invalid_params");
})();
async function callTool(service, name, raw = {}, locale = "zh-Hans", signal) {
  if (raw === null) raw = {};
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw new ExtError("Invalid market tool arguments", "invalid_params");
  const args = raw, t = translator(locale), lines = [];
  for (const field of ["symbol", "bar", "query"]) stringArg(args, field);
  const limit = toolInteger(args.limit);
  if (args.symbols !== void 0 && args.symbols !== null && (!Array.isArray(args.symbols) || args.symbols.some((s) => s !== null && typeof s !== "string"))) throw new ExtError(t("invalid"), "invalid_params");
  if (name === "markets_quote") {
    const typed = Array.isArray(args.symbols) ? args.symbols.map((s) => s ?? "") : void 0;
    const list = typed?.length ? typed : service.watchlist();
    const refs = list.map(parseRef).filter((r) => !!r).slice(0, 50);
    const got = await service.quotes(refs.map(key), 1e4, false, signal);
    lines.push(t("quotesAt", { at: new Date(service.clock()).toISOString() }));
    if (!list.length) lines.push(t("empty"));
    for (const ref of refs) {
      const q = got.quotes.get(key(ref));
      lines.push(q ? quoteLine(ref, q, locale) : `- ${key(ref)}: ${t("missing")}`);
    }
    for (const code of list.filter((s) => !parseRef(s)).slice(0, 50)) lines.push(t("badCode", { code: clip(code, 40) }));
  } else if (name === "markets_search") {
    const query = cleanQuery(stringArg(args, "query"));
    if (!query) throw new ExtError(t("invalid"), "invalid_params");
    const hits = await service.search(query, bounded(limit, 30, 10), 1e4, signal);
    lines.push(t("searchTitle", { q: query }));
    if (!hits.length) lines.push(t("none"));
    for (const hit of hits) lines.push(quoteLine(hit.ref, hit.quote, locale) + (hit.quote.kind ? `, ${t(hit.quote.kind)}` : ""));
  } else if (name === "markets_candles") {
    const ref = parseRef(stringArg(args, "symbol"));
    if (!ref) throw new ExtError(t("invalid"), "invalid_params");
    const bar = stringArg(args, "bar") || service.defaultBar(ref.source);
    if (!sourceBars[ref.source].includes(bar)) throw new ExtError(t("noCandles"), "invalid_params");
    const candles = await service.candles(ref, bar, bounded(limit, 100, 24), signal), zone = service.cfg.tz || "Local";
    lines.push(t("candlesAt", { ref: key(ref), bar, n: candles.length, zone: displayZone(zone, service.clock()) }));
    if (!candles.length) lines.push(t(bar === "T" ? "noMinutes" : "noCandles"));
    for (const c of candles) lines.push([stamp(c.at, zone), fmtFloat(c.open), fmtFloat(c.high), fmtFloat(c.low), fmtFloat(c.close), compact(c.vol)].join(" "));
  } else throw new ExtError(t("invalid"), "unknown_method");
  return { text: clip(lines.join("\n") + "\n", 128e3) };
}
function toolCall(params, call) {
  try {
    const exact = call?.params ?? ExactJSON.parse(JSON.stringify(params ?? null));
    const envelope = typedJSON(numericJSON(exact.raw), { fields: { name: "string", args: "raw" } }) ?? {};
    const args = exact.field("args");
    const decoded = args ? typedJSON(numericJSON(args.raw), { fields: {
      symbols: { array: "string" },
      symbol: "string",
      bar: "string",
      query: "string",
      limit: "raw"
    } }) ?? {} : {};
    decoded.limit = args?.int64Fields(["limit"]).limit ?? 0n;
    return { name: envelope.name ?? "", args: decoded };
  } catch {
    throw new ExtError("Invalid market tool arguments", "invalid_params");
  }
}
function connector(service) {
  const handler = async (method, params = {}, call) => {
    params ??= {};
    if (params.contribution && params.contribution !== "markets") throw noContribution(params.contribution);
    const current = typeof service === "function" ? service() : service;
    switch (method) {
      case "status":
        return current.status(params.locale);
      case "tools.list":
        return { tools: tools(params.locale) };
      case "tools.call": {
        const decoded = toolCall(params, call);
        return callTool(current, decoded.name, decoded.args, params.locale);
      }
      case "inbox.collect":
        return current.inbox(params.locale);
      default:
        throw unknownMethod("connector." + method);
    }
  };
  return withExactParams(handler);
}

// ../../sdk/ts/src/jsx-runtime.ts
var jsx = createElement;
var jsxs = createElement;

// src/page.tsx
function readPageState(raw, service) {
  let value = raw;
  if (typeof raw === "string") {
    try {
      value = JSON.parse(raw);
    } catch {
      value = {};
    }
  }
  if (!value || typeof value !== "object" || Array.isArray(value)) value = {};
  const r = typeof value.i === "string" ? parseRef(value.i) : void 0;
  const b = typeof value.b === "string" && bars.includes(value.b) ? value.b : "";
  return { i: r ? key(r) : "", b: r ? sourceBars[r.source].includes(b) ? b : service.defaultBar(r.source) : b, q: typeof value.q === "string" ? cleanQuery(value.q) : "", s: ["up", "down"].includes(value.s) ? value.s : "", m: sourceIDs.includes(value.m) ? value.m : "", o: value.o === true, f: value.f === true };
}
var loading = () => ({ kind: "loading", quotes: /* @__PURE__ */ new Map(), errors: /* @__PURE__ */ new Map(), hits: [], candles: [], at: 0 });
async function loadPage(service, state, refresh, card, uiVersion = 5, signal) {
  if (card && service.cfg.overview_card === false) throw unsupported(translator()("cardOff"));
  const loaded = loading();
  loaded.at = service.clock();
  const fresh = refresh ? 0 : 1e4;
  try {
    if (card) {
      loaded.kind = "card";
      const result = await service.quotes([...benchmarks, ...service.watchlist()], fresh, true, signal);
      loaded.quotes = result.quotes;
      loaded.errors = result.errors;
    } else if (state.i) {
      loaded.kind = "instrument";
      const ref = parseRef(state.i);
      loaded.quotes.set(state.i, await service.quote(ref, fresh, signal));
      try {
        loaded.candles = await service.candles(ref, state.b, 60, signal);
      } catch (err) {
        if (signal?.aborted || isCancelled(err)) throw err;
        loaded.candleError = err;
      }
    } else if (state.q) {
      loaded.kind = "search";
      let searchError;
      try {
        loaded.hits = await service.search(state.q, 30, fresh, signal);
      } catch (err) {
        if (signal?.aborted || isCancelled(err)) throw err;
        searchError = err;
      }
      const exact = uiVersion >= 3 && parseRef(state.q);
      if (exact && !loaded.hits.some((h) => key(h.ref) === key(exact))) {
        try {
          const quote = await service.quote(exact, fresh, signal);
          loaded.exact = { ref: exact, quote };
        } catch (err) {
          if (signal?.aborted || isCancelled(err)) throw err;
        }
      }
      if (searchError && !loaded.hits.length && !loaded.exact) throw searchError;
    } else {
      loaded.kind = "list";
      const refs = [...service.watchlist(), ...uiVersion >= 2 ? benchmarks : []], result = await service.quotes(refs, fresh, true, signal);
      loaded.quotes = result.quotes;
      loaded.errors = result.errors;
    }
  } catch (error) {
    if (signal?.aborted || isCancelled(error)) throw error;
    loaded.kind = "error";
    loaded.error = error;
  }
  return loaded;
}
function fingerprint(loaded) {
  const byKey = ([a], [b]) => a < b ? -1 : a > b ? 1 : 0;
  return JSON.stringify({
    kind: loaded.kind,
    quotes: [...loaded.quotes].sort(byKey),
    errors: [...loaded.errors].sort(byKey).map(([k, v]) => [k, v instanceof ReadError ? [v.code, v.status] : "error"]),
    hits: loaded.hits,
    exact: loaded.exact,
    candles: loaded.candles,
    candleError: loaded.candleError instanceof ReadError ? loaded.candleError.code : !!loaded.candleError,
    error: loaded.error instanceof ReadError ? loaded.error.code : !!loaded.error
  }, (_key, value) => typeof value === "bigint" ? value.toString() : value);
}
async function transition(service, old, id, values = {}, signal) {
  const state = { ...old };
  const i = id.indexOf(":"), action = i < 0 ? id : id.slice(0, i), arg = i < 0 ? "" : id.slice(i + 1), ref = parseRef(arg || values.inst || "");
  let toast;
  switch (action) {
    case "open":
      if (ref) {
        state.i = key(ref);
        state.b = sourceBars[ref.source].includes(state.b) ? state.b : service.defaultBar(ref.source);
        state.f = false;
      }
      break;
    case "look":
      if (!ref) throw new MutationError("invalid", "Invalid code");
      state.i = key(ref);
      state.b = service.defaultBar(ref.source);
      break;
    case "back":
      state.i = "";
      state.f = false;
      break;
    case "opts":
      state.o = !state.o;
      break;
    case "form":
      state.f = !state.f;
      break;
    case "search":
      state.q = cleanQuery(values.q ?? values.inst ?? "");
      state.i = "";
      break;
    case "clear":
      state.q = "";
      break;
    case "sort":
      state.s = ["up", "down"].includes(arg || values.sort) ? arg || values.sort : "";
      state.o = false;
      break;
    case "mkt":
      state.m = sourceIDs.includes(arg || values.mkt) ? arg || values.mkt : "";
      state.o = false;
      break;
    case "bar":
      if (bars.includes(arg || values.bar)) state.b = arg || values.bar;
      break;
    case "add":
      if (!ref) throw new MutationError("invalid", "Invalid code");
      await service.addWatch(ref, signal);
      toast = "added";
      if (state.q && state.i === key(ref)) state.i = "";
      break;
    case "remove":
      if (ref) {
        await service.removeWatch(ref);
        toast = "removed";
      }
      break;
    case "up":
    case "down":
    case "top":
      if (ref) await service.moveWatch(ref, action === "top" ? "top" : action === "up" ? -1 : 1);
      break;
    case "alert":
      if (ref) {
        await service.addAlert(ref, values.alert_kind, values.alert_value, signal);
        state.f = false;
        toast = "alertAdded";
      }
      break;
    case "unalert":
      await service.removeAlert(arg);
      toast = "alertRemoved";
      break;
    case "refresh":
      toast = "refreshed";
      break;
  }
  return { state: readPageState(state, service), refresh: action === "refresh", toast, ...ref ? { symbol: ref.symbol } : {} };
}
function sortByChange(refs, quotes, down = false) {
  return refs.slice().sort((a, b) => {
    const ca = change(quotes.get(a)), cb = change(quotes.get(b));
    if (ca === void 0 || cb === void 0) return ca === cb ? 0 : ca === void 0 ? 1 : -1;
    return down ? ca - cb : cb - ca;
  });
}
function matchRank(query, source, ref, q) {
  const name = q.name.toLowerCase(), typed = query.trim().toLowerCase();
  return source.normalize(query) === ref.symbol || name && name === typed ? 0 : name && name.startsWith(typed) ? 1 : !name && /\p{Script=Han}/u.test(query) ? 3 : 2;
}
function refreshDelay(service, state, loaded, card = false) {
  if (loaded.kind === "loading") return null;
  if (loaded.kind === "error" || loaded.kind === "search") return 3e4;
  if (loaded.kind === "instrument") return loaded.quotes.get(state.i)?.idle ? 3e5 : 3e4;
  const watch = service.watchlist();
  const active = card ? !loaded.quotes.size || [...loaded.quotes.values()].some((q) => !q.idle) : loaded.errors.size > 0 || !watch.length || watch.some((ref) => {
    const q = loaded.quotes.get(ref);
    return !!q && !q.idle;
  });
  return active ? card ? 6e4 : 3e4 : 3e5;
}
function page(props) {
  const { service, state: st, loaded: l, action: a, card = false } = props, t = translator(props.locale), v = props.uiVersion ?? 5, zone = service.cfg.tz || props.tz || "Local", watch = service.watchlist(), alerts = service.alerts();
  const b = (id, title, options = {}, uiKey = id) => {
    const { role, ...metadata } = options;
    return /* @__PURE__ */ jsx(Button, { title, action: a(id, metadata), role }, uiKey);
  };
  const textError = (err) => err instanceof ReadError ? err.code === "not_found" ? t("missing") : err.code === "rate_limited" ? t("retry") + " \xB7 " + t("error") : t("error") : t("error");
  const alertWhat = (alert, q) => `${t(alert.kind)} ${alert.kind === "move" ? alert.value + "%" : price({ decimals: Math.max(q?.decimals ?? 0, alert.decimals ?? 0) }, alert.value)}`;
  const row = (ref, q, search = false, action = true, summary = false) => {
    const r = parseRef(ref);
    const c = change(q);
    const mark = alerts.find((a2) => a2.ref === ref && a2.met) ?? alerts.find((a2) => a2.ref === ref && q && near(a2, q));
    const subtitle = q ? [mark ? `${t(mark.met ? "met" : "near")}: ${alertWhat(mark, q)}` : "", q.name ? r.symbol : "", search ? t(q.kind) : q.last && q.base ? `${q.last - q.base > 0 ? "+" : ""}${priceLike(q, q.last - q.base, q.last)}` : "", q.turnover > 0 ? `${t("turnover")} ${fmtCN(q.turnover, props.locale)} ${q.currency}` : "", search && watch.includes(ref) ? t("watching") : "", q.idle ? t("idle", { at: stamp(q.at, zones[r.source], "short") }) : ""].filter(Boolean).join(" \xB7 ") : t("missing");
    return /* @__PURE__ */ jsx(Row, { title: q?.name || r.symbol, subtitle: summary ? q?.name ? r.symbol : void 0 : subtitle, value: q ? price(q, q.last) : void 0, badge: c !== void 0 ? { text: fmtChange(c) + (mark?.met ? " \xB7 " + t("met") : ""), tone: tone(service.cfg, c) } : !q ? { text: t("noQuote"), tone: "caution" } : void 0, action: action ? a("open:" + ref) : void 0 }, (summary ? "mover-" : "quote-") + ref);
  };
  const stats = (qs) => benchmarks.flatMap((ref) => {
    const q = qs.get(ref);
    if (!q) return [];
    const c = change(q);
    return [{ label: q.name || parseRef(ref).symbol.split("-")[0], value: price(q, q.last), detail: c === void 0 ? void 0 : fmtChange(c), tone: c === void 0 ? void 0 : tone(service.cfg, c) }];
  });
  const alertRows = (list, detail) => list.map((alert) => {
    const q = l.quotes.get(alert.ref), title = alertWhat(alert, q);
    return /* @__PURE__ */ jsx(Row, { title: detail ? title : (alert.name || parseRef(alert.ref).symbol) + " " + title, subtitle: alert.met ? `${t("met")} \xB7 ${stamp(Date.parse(alert.met), zone, "short")} \xB7 ${price({ decimals: alert.decimals ?? 0 }, alert.price ?? 0)}` : t("waiting"), badge: alert.met ? { text: t("met"), tone: "caution" } : void 0, action: a(detail ? "unalert:" + alert.id : "open:" + alert.ref, detail ? { confirm: t("alertConfirm") } : {}) }, alert.id);
  });
  const readAt = /* @__PURE__ */ jsxs(Section, { children: [
    /* @__PURE__ */ jsx(Text, { text: t("readAt", { at: stamp(l.at || service.clock(), zone, "time") }), style: "caption" }, "at"),
    !card && b("refresh", t("refresh"))
  ] }, "section-read-at");
  const addFields = /* @__PURE__ */ jsxs(Section, { title: t("add"), footer: t("addHint"), children: [
    /* @__PURE__ */ jsx(Field, { name: "inst", label: t("code"), kind: "text", placeholder: "BTC-USDT" }, "inst"),
    b("add", t("add"), { submit: true }),
    v < 3 && b("search", t("search"), { submit: true })
  ] }, "section-add");
  if (l.kind === "loading") return /* @__PURE__ */ jsx(View, { title: card ? void 0 : t("markets"), state: JSON.stringify(st), children: /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(Loading, { title: t("loading") }, "loading") }, "section-loading") }, "markets-view");
  if (l.kind === "error") return /* @__PURE__ */ jsx(View, { title: t("markets"), state: JSON.stringify(st), children: /* @__PURE__ */ jsxs(Section, { children: [
    /* @__PURE__ */ jsx(Text, { text: textError(l.error), style: "secondary" }, "message"),
    !!st.i && watch.includes(st.i) && b("remove:" + st.i, t("remove"), { role: "destructive", confirm: t("removeConfirm", { symbol: parseRef(st.i).symbol }) }),
    b("refresh", t("retry")),
    b(st.i ? "back" : "clear", t(st.i && st.q ? "backSearch" : "clear"))
  ] }, "section-error") }, "markets-view");
  if (card) {
    const moves = watch.filter((ref) => change(l.quotes.get(ref)) !== void 0).sort((a2, b2) => Math.abs(change(l.quotes.get(b2))) - Math.abs(change(l.quotes.get(a2)))).slice(0, 5);
    return /* @__PURE__ */ jsxs(View, { children: [
      !!stats(l.quotes).length && /* @__PURE__ */ jsx(Section, { children: /* @__PURE__ */ jsx(StatGrid, { stats: stats(l.quotes) }, "stats") }, "section-benchmarks"),
      /* @__PURE__ */ jsxs(Section, { title: moves.length ? t("cardMovers") : void 0, children: [
        moves.map((ref) => row(ref, l.quotes.get(ref), false, false, true)),
        !moves.length && /* @__PURE__ */ jsx(Text, { text: !watch.length ? t("empty") : l.errors.size ? t("error") : "", style: "secondary" }, "empty")
      ] }, "section-movers")
    ] }, "markets-view");
  }
  if (st.i) {
    const ref = parseRef(st.i), q = l.quotes.get(st.i), cs = l.candles, first = cs[0], last = cs.at(-1), c = change(q), details = [{ label: `${t(q.period)} ${t("high")}`, value: price(q, q.high) }, { label: `${t(q.period)} ${t("low")}`, value: price(q, q.low) }, ...q.turnover > 0 ? [{ label: `${t(q.period)} ${t("turnover")}`, value: compact(q.turnover) + " " + q.currency }] : [], ...q.facts.map((f) => ({ label: t(f.label), value: localizeValue(f.value, props.locale) }))], span = first?.open ? (last.close - first.open) / first.open : void 0;
    const rangeStats = cs.length ? [{ label: st.b === "T" ? t("sinceOpen") : t("span", { n: cs.length }), value: span === void 0 ? "\u2014" : fmtChange(span), tone: span === void 0 ? void 0 : tone(service.cfg, span) }, { label: t("spanHigh"), value: price(q, Math.max(...cs.map((c2) => c2.high))) }, { label: t("spanLow"), value: price(q, Math.min(...cs.map((c2) => c2.low))) }] : [];
    const group = watch.filter((s) => parseRef(s)?.source === ref.source), i = group.indexOf(st.i);
    return /* @__PURE__ */ jsxs(View, { title: [ref.symbol, q.name].filter(Boolean).join(" "), state: JSON.stringify(st), children: [
      /* @__PURE__ */ jsxs(Section, { children: [
        b("back", t(st.q ? "backSearch" : "clear")),
        /* @__PURE__ */ jsx(Metric, { label: q.idle ? t("idle", { at: stamp(q.at, zones[ref.source], "short") }) : t("latest"), value: price(q, q.last), detail: c === void 0 ? void 0 : t(q.period) + " " + fmtChange(c), tone: c === void 0 ? void 0 : tone(service.cfg, c) }, "last")
      ] }, "section-price"),
      /* @__PURE__ */ jsxs(Section, { title: t("kline") + " \xB7 " + t(st.b), children: [
        v >= 2 ? /* @__PURE__ */ jsx(Segmented, { name: "bar", value: st.b, choices: sourceBars[ref.source].map((b2) => ({ value: b2, title: t(b2) })), action: a("bar") }, "bar") : sourceBars[ref.source].map((bar) => /* @__PURE__ */ jsx(Row, { title: t(bar), badge: bar === st.b ? { text: t("look"), tone: "positive" } : void 0, action: a("bar:" + bar) }, bar)),
        l.candleError ? /* @__PURE__ */ jsx(Text, { text: textError(l.candleError), style: "secondary" }, "error") : !cs.length ? /* @__PURE__ */ jsx(Text, { text: t(st.b === "T" ? "noMinutes" : "noCandles"), style: "secondary" }, "empty") : /* @__PURE__ */ jsxs(Fragment, { children: [
          v >= 2 && /* @__PURE__ */ jsxs(Fragment, { children: [
            /* @__PURE__ */ jsx(Chart, { kind: "line", title: t("close"), series: [{ name: t("close"), points: cs.map((c2) => ({ label: stamp(c2.at, zone, st.b === "T" ? "time" : ["1D", "1W"].includes(st.b) ? "day" : "short"), value: c2.close, display: clip(`${t("close")} ${price(q, c2.close)} ${t("high")} ${price(q, c2.high)} ${t("low")} ${price(q, c2.low)}`, 40) })) }] }, "close"),
            /* @__PURE__ */ jsx(Chart, { kind: "bar", title: t("volume"), series: [{ name: t("volume"), points: cs.map((c2) => ({ label: stamp(c2.at, zone, st.b === "T" ? "time" : ["1D", "1W"].includes(st.b) ? "day" : "short"), value: c2.vol, display: compact(c2.vol) })) }] }, "volume")
          ] }),
          v >= 2 ? /* @__PURE__ */ jsx(StatGrid, { stats: rangeStats }, "range") : rangeStats.map((s) => /* @__PURE__ */ jsx(Metric, { ...s }, s.label))
        ] })
      ] }, "section-candles"),
      /* @__PURE__ */ jsx(Section, { title: t("details"), children: v >= 2 ? [/* @__PURE__ */ jsx(StatGrid, { stats: details.slice(0, q.turnover > 0 ? 3 : 2) }, "figures"), ...Array.from({ length: Math.ceil(q.facts.length / 6) }, (_, i2) => /* @__PURE__ */ jsx(StatGrid, { stats: q.facts.slice(i2 * 6, i2 * 6 + 6).map((f) => ({ label: t(f.label), value: localizeValue(f.value, props.locale) })) }, "facts-" + i2))] : details.map((s) => /* @__PURE__ */ jsx(Metric, { ...s }, s.label)) }, "section-details"),
      /* @__PURE__ */ jsxs(Section, { title: t("alerts"), footer: t("alertsHint"), children: [
        alertRows(alerts.filter((a2) => a2.ref === st.i), true),
        st.f ? /* @__PURE__ */ jsxs(Fragment, { children: [
          /* @__PURE__ */ jsx(Field, { name: "alert_kind", label: t("alertKind"), kind: "choice", value: "above", choices: ["above", "below", "move"].map((value) => ({ value, title: t(value) })) }, "kind"),
          /* @__PURE__ */ jsx(Field, { name: "alert_value", label: t("alertValue"), kind: "number", placeholder: price(q, q.last) }, "value"),
          b("alert:" + st.i, t("alert"), { submit: true }),
          b("form", t("cancel"))
        ] }) : b("form", t("newAlert"))
      ] }, "section-alerts"),
      /* @__PURE__ */ jsxs(Section, { title: t("watch"), children: [
        i >= 0 ? /* @__PURE__ */ jsxs(Fragment, { children: [
          i > 1 && b("top:" + st.i, t("top")),
          i > 0 && b("up:" + st.i, t("up")),
          i < group.length - 1 && b("down:" + st.i, t("down")),
          b("remove:" + st.i, t("remove"), { role: "destructive", confirm: t("removeConfirm", { symbol: ref.symbol }) })
        ] }) : b("add:" + st.i, t("add")),
        /* @__PURE__ */ jsx(Link, { title: t("site", { site: ref.source === "okx" ? "OKX" : t("tencent") }), url: service.sources[ref.source].url(ref.symbol) }, "site"),
        /* @__PURE__ */ jsx(Text, { text: t("readAt", { at: stamp(l.at, zone, "time") }), style: "caption" }, "read")
      ] }, "section-watch")
    ] }, "markets-view");
  }
  if (st.q) {
    const groups = sourceIDs.map((id) => ({ id, hits: l.hits.filter((h) => h.ref.source === id) })).filter((g) => g.hits.length).sort((a2, b2) => Math.min(...a2.hits.map((h) => matchRank(st.q, service.sources[a2.id], h.ref, h.quote))) - Math.min(...b2.hits.map((h) => matchRank(st.q, service.sources[b2.id], h.ref, h.quote))));
    return /* @__PURE__ */ jsxs(View, { title: t("markets"), state: JSON.stringify(st), search: v >= 3 ? { placeholder: t("searchHint"), value: st.q, action: a("search") } : void 0, children: [
      l.exact && /* @__PURE__ */ jsxs(Section, { title: t("code") + " " + key(l.exact.ref), children: [
        row(key(l.exact.ref), l.exact.quote, true),
        !watch.includes(key(l.exact.ref)) && b("add:" + key(l.exact.ref), t("add") + " \xB7 " + clip(l.exact.quote.name || l.exact.ref.symbol, 20))
      ] }, "section-exact"),
      !l.hits.length && !l.exact && /* @__PURE__ */ jsx(Section, { title: t("searchTitle", { q: st.q }), children: /* @__PURE__ */ jsx(Text, { text: t("none"), style: "secondary" }, "none") }, "section-empty"),
      groups.map((g) => /* @__PURE__ */ jsx(Section, { title: `${t("searchTitle", { q: st.q })} \xB7 ${t(g.id)} \xB7 ${g.hits.length}`, footer: l.hits.length >= 30 ? t("moreHits") : void 0, children: g.hits.flatMap((h) => [row(key(h.ref), h.quote, true), !watch.includes(key(h.ref)) ? b("add:" + key(h.ref), t("add") + " \xB7 " + (h.quote.name || h.ref.symbol)) : null]) }, g.id)),
      /* @__PURE__ */ jsx(Section, { children: b("clear", t("clear")) }, "section-back"),
      v < 3 && addFields,
      readAt
    ] }, "markets-view");
  }
  const present = sourceIDs.filter((id) => watch.some((ref) => parseRef(ref)?.source === id)), movers = watch.filter((s) => change(l.quotes.get(s)) !== void 0).sort((a2, b2) => Math.abs(change(l.quotes.get(b2))) - Math.abs(change(l.quotes.get(a2))));
  return /* @__PURE__ */ jsxs(View, { title: t("markets"), state: JSON.stringify(st), search: v >= 3 ? { placeholder: t("searchHint"), value: st.q, action: a("search") } : void 0, children: [
    v >= 2 && stats(l.quotes).length > 1 && /* @__PURE__ */ jsx(Section, { title: t("benchmarks"), children: /* @__PURE__ */ jsx(StatGrid, { stats: stats(l.quotes) }, "stats") }, "section-benchmarks"),
    !watch.length && /* @__PURE__ */ jsx(Section, { title: t("watch") + " \xB7 0", children: /* @__PURE__ */ jsx(Text, { text: t("empty"), style: "secondary" }, "empty") }, "section-empty"),
    movers.length > 3 && /* @__PURE__ */ jsx(Section, { title: t("movers"), children: sortByChange(movers.slice(0, 3), l.quotes).map((ref) => row(ref, l.quotes.get(ref), false, true, true)) }, "section-movers"),
    v >= 2 && watch.length > 1 && /* @__PURE__ */ jsxs(Section, { children: [
      /* @__PURE__ */ jsx(Row, { title: `${t("sort")} \xB7 ${t(st.s === "up" ? "upSort" : st.s === "down" ? "downSort" : "order")} \xB7 ${t(st.m && present.includes(st.m) ? st.m : "all")}`, value: st.o ? t("collapse") : void 0, action: a("opts") }, "opts"),
      st.o && /* @__PURE__ */ jsxs(Fragment, { children: [
        /* @__PURE__ */ jsx(Segmented, { name: "sort", value: st.s || "order", choices: [{ value: "order", title: t("order") }, { value: "up", title: t("upSort") }, { value: "down", title: t("downSort") }], action: a("sort") }, "sort"),
        present.length > 1 && /* @__PURE__ */ jsx(Segmented, { name: "mkt", value: st.m || "all", choices: [{ value: "all", title: t("all") }, ...present.map((value) => ({ value, title: t(value) }))], action: a("mkt") }, "market")
      ] })
    ] }, "section-order"),
    present.filter((id) => !st.m || !present.includes(st.m) || id === st.m).map((id) => {
      const refs = watch.filter((s) => parseRef(s)?.source === id);
      return /* @__PURE__ */ jsx(Section, { title: `${t(id)} \xB7 ${refs.length}`, footer: t("periodHint"), children: l.errors.has(id) ? /* @__PURE__ */ jsxs(Fragment, { children: [
        /* @__PURE__ */ jsx(Text, { text: textError(l.errors.get(id)), style: "secondary" }, "error-" + id),
        b("refresh", t("retry"), {}, "retry-" + id)
      ] }) : (st.s ? sortByChange(refs, l.quotes, st.s === "down") : refs).map((ref) => row(ref, l.quotes.get(ref))) }, id);
    }),
    !!alerts.length && /* @__PURE__ */ jsx(Section, { title: `${t("alerts")} \xB7 ${alerts.length}`, children: alertRows(alerts, false) }, "section-alerts"),
    v < 3 && addFields,
    readAt
  ] }, "markets-view");
}

// src/root.tsx
function Root(options) {
  const [state, setState] = useState(() => readPageState(options.state, options.service));
  const [request, setRequest] = useState({ revision: 0, refresh: false });
  const [gate] = useState(() => ({ acting: false, dirty: false }));
  const [result, setResult] = useState(void 0);
  const identity = JSON.stringify([state, request.revision, options.uiVersion, options.card]);
  useEffect(() => options.service.subscribe(() => {
    if (gate.acting) gate.dirty = true;
    else setRequest((old) => ({ revision: old.revision + 1, refresh: false }));
  }), [options.service]);
  useLoadEffect(async (signal) => {
    try {
      const loaded2 = await loadPage(options.service, state, request.refresh, !!options.card, options.uiVersion, signal);
      if (!signal.aborted) setResult((old) => old?.service === options.service && old.identity === identity && fingerprint(old.loaded) === fingerprint(loaded2) ? old : { service: options.service, identity, loaded: loaded2 });
    } catch (error) {
      if (!signal.aborted && !isCancelled(error)) throw error;
    }
  }, [options.service, identity]);
  const loaded = result?.service === options.service && result.identity === identity ? result.loaded : loading();
  useInterval(async (signal) => {
    if (gate.acting) return;
    try {
      const next = await loadPage(options.service, state, false, !!options.card, options.uiVersion, signal);
      if (!signal.aborted) setResult((old) => old?.service === options.service && old.identity === identity && fingerprint(old.loaded) === fingerprint(next) ? old : { service: options.service, identity, loaded: next });
    } catch (error) {
      if (!signal.aborted && !isCancelled(error)) throw error;
    }
  }, refreshDelay(options.service, state, loaded, !!options.card));
  return page({
    ...options,
    state,
    loaded,
    action: (id, metadata) => callback(async (values, context2) => {
      const t = translator(options.locale);
      gate.acting = true;
      try {
        const next = await transition(options.service, state, id, values, context2.signal);
        if (context2.signal.aborted) return;
        setState((previous) => JSON.stringify(previous) === JSON.stringify(next.state) ? previous : next.state);
        if (next.refresh) setRequest((old) => ({ revision: old.revision + 1, refresh: true }));
        else setRequest((old) => old.refresh ? { ...old, refresh: false } : old);
        return next.toast ? { toast: t(next.toast, { symbol: next.symbol ?? "" }) } : void 0;
      } catch (error) {
        if (context2.signal.aborted || isCancelled(error)) throw error;
        return { toast: t(actionError(error)) };
      } finally {
        gate.acting = false;
        if (gate.dirty) {
          gate.dirty = false;
          setRequest((old) => ({ revision: old.revision + 1, refresh: false }));
        }
      }
    }, metadata)
  });
}
function root(options) {
  return /* @__PURE__ */ jsx(Root, { ...options }, "markets-root");
}

// src/snapshot.ts
function snapshotPage(service, card = false) {
  const render = async (req, refresh = false) => {
    const state = readPageState(req.state, service);
    const loaded = await loadPage(service, state, refresh, card, req.ui_version, AbortSignal.timeout(6e4));
    const renderer = new Renderer({ authorize: () => false });
    try {
      const view = structuredClone(renderer.mount(page({ service, state, loaded, card, locale: req.locale, tz: req.tz, uiVersion: req.ui_version, action: (id, metadata) => ({ id, ...metadata }) })).view);
      view.refresh_after = (refreshDelay(service, state, loaded, card) ?? 3e4) / 1e3;
      return view;
    } finally {
      renderer.dispose();
    }
  };
  return { render, action: async (req) => {
    const t = translator(req.locale);
    let next = { state: readPageState(req.state, service), refresh: false };
    try {
      if (!card) next = await transition(service, next.state, req.action ?? "", req.values);
    } catch (error) {
      if (isCancelled(error)) throw error;
      next.toast = actionError(error);
    }
    return { view: await render({ ...req, state: JSON.stringify(next.state) }, next.refresh), ...next.toast ? { toast: t(next.toast, { symbol: next.symbol ?? "" }) } : {} };
  } };
}

// persona-extension.json
var persona_extension_default = { id: "markets", version: "0.6.5", contributes: { connectors: [{ id: "markets", name: "\u884C\u60C5", capabilities: ["status", "tools", "inbox", "fallback"], hosts: ["www.okx.com", "qt.gtimg.cn", "smartbox.gtimg.cn", "web.ifzq.gtimg.cn", "ifzq.gtimg.cn"], kinds: ["price_alert"], kind_descriptions: { price_alert: "\u7528\u6237\u81EA\u5DF1\u5728\u300C\u884C\u60C5\u300D\u91CC\u8BBE\u7684\u4EF7\u683C\u63D0\u9192\u5230\u4E86\uFF08\u6DA8\u5230\u3001\u8DCC\u5230\u67D0\u4E2A\u4EF7\uFF0C\u6216\u6DA8\u8DCC\u5E45\u8D85\u8FC7\u8BBE\u7684\u767E\u5206\u6BD4\uFF09\uFF0C\u9700\u8981\u7528\u6237" } }], ui: [{ id: "watchlist", title: "\u884C\u60C5", placement: "nav_page", icon: "chart" }, { id: "card", title: "\u884C\u60C5", placement: "overview_card", opens: "watchlist" }] } };

// src/index.ts
function services(hub, config = {}, options = {}) {
  const service = new Markets(hub, config, options);
  return { service, namespaces: { connector: connector(service) }, ui: { watchlist: snapshotPage(service), card: snapshotPage(service, true) } };
}
function createProgram(options = {}) {
  let service;
  const current = () => {
    if (!service) throw new Error("Markets has not been initialized");
    return service;
  };
  return {
    id: persona_extension_default.id,
    version: persona_extension_default.version,
    init: async (init, hub) => {
      service?.dispose();
      const next = new Markets(hub, init.config ?? {}, options);
      try {
        await next.initialize();
      } catch (error) {
        next.dispose();
        throw error;
      }
      service = next;
      return persona_extension_default.contributes;
    },
    namespaces: { connector: connector(current) },
    ui: {
      watchlist: componentPage((req) => root({ service: current(), state: readPageState(req.state, current()), locale: req.locale, uiVersion: req.ui_version, tz: req.tz })),
      card: { render: (req) => snapshotPage(current(), true).render(req), action: (req) => snapshotPage(current(), true).action(req) }
    }
  };
}
var index_default = createProgram();
export {
  Markets,
  ReadError,
  Root,
  callTool,
  connector,
  createProgram,
  decodeState,
  index_default as default,
  encodeState,
  legacyState,
  loadPage,
  page,
  parseStateJSON,
  readPageState,
  root,
  services,
  snapshotPage,
  tools,
  transition
};
