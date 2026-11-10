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
      const key = JSON.parse(this.raw.slice(at, end));
      at = skipSpace(this.raw, skipSpace(this.raw, end) + 1);
      const value = valueEnd(this.raw, at);
      yield [key, new _ExactJSON(this.raw.slice(at, value), VALIDATED_JSON)];
      at = skipSpace(this.raw, value);
      if (this.raw[at] === ",") at = skipSpace(this.raw, at + 1);
    }
  }
  /** The final occurrence of an exact, JSON-decoded key; null/nonexistent objects have no members. */
  member(name) {
    let found;
    for (const [key, value] of this.entries()) if (key === name) found = value;
    return found;
  }
  /** Ordered raw struct-field occurrences for an extension's custom decoder, including
   * mixed string/integer IDs. The decoder must preserve its own null/type/error rules. */
  *fieldValues(name) {
    if (!name || /[^\x00-\x7f]/.test(name)) throw new TypeError("field name must be ASCII");
    const folded = foldASCII(name);
    for (const [key, value] of this.entries()) if (foldASCII(key) === folded) yield value;
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
    for (const [key, value] of this.entries()) {
      const name = exact.get(key) ?? folded.get(foldASCII(key));
      if (name !== void 0) values[name] = value.int64(values[name]);
    }
    return values;
  }
};

// ../../sdk/ts/src/components.ts
var callbackTag = Symbol("callback");
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

// ../../sdk/ts/src/renderer.ts
var encoder = new TextEncoder();

// ../../sdk/ts/src/page-bridge.ts
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
var unknownMethod = (method) => new ExtError(`unknown method ${method}`, "unknown_method");
var EXACT_PARAMS = Symbol.for("@persona/extension.exactParams");
function withExactParams(handler) {
  const wrapped = (method, params, call) => handler(method, params, call);
  Object.defineProperty(wrapped, EXACT_PARAMS, { value: true });
  return wrapped;
}
var EXACT_INIT_KEY = "@persona/extension.exactInitParams";
function withExactInit(handler) {
  const wrapped = function(init, hub, call) {
    return Reflect.apply(handler, this, [init, hub, call]);
  };
  Object.defineProperty(wrapped, Symbol.for(EXACT_INIT_KEY), { value: true });
  return wrapped;
}

// src/locales/en.json
var en_default = {
  invalidDSN: "The DSN must be https://<public key>@<host>/<project number>.",
  httpDisabled: "Enable allow_http before using an HTTP DSN. Reports and the public key travel unencrypted.",
  invalidConfig: "The Sentry configuration is invalid.",
  invalidReports: "The error report delivery is invalid.",
  notInitialized: "Sentry has not been initialized.",
  description: "Forward scrubbed Persona error and crash reports to your Sentry project. No DSN means no reports are collected or sent.",
  environmentTitle: "Environment",
  environmentDescription: "The Sentry event environment, such as production or staging.",
  sampleTitle: "Sample rate",
  sampleDescription: "Between 0 and 1: 1 sends all reports; 0.5 randomly sends half.",
  logsTitle: "Include error log lines",
  logsDescription: "When disabled, only exceptions, crashes and panics are sent.",
  httpTitle: "Allow an HTTP DSN",
  httpDescription: "For a trusted self-hosted instance only. HTTP sends reports and the public key unencrypted, allowing observers to read reports and forge events.",
  dsnDescription: "The project's Client Keys (DSN) value. Store it in the secret field. An HTTP DSN also requires allow_http."
};

// src/locales/zh-Hans.json
var zh_Hans_default = {
  invalidDSN: "DSN \u5FC5\u987B\u4E3A https://<\u516C\u94A5>@<\u4E3B\u673A>/<\u9879\u76EE\u53F7>\u3002",
  httpDisabled: "\u4F7F\u7528 HTTP DSN \u524D\u8BF7\u5F00\u542F allow_http\u3002\u62A5\u544A\u548C\u516C\u94A5\u4F1A\u660E\u6587\u4F20\u8F93\u3002",
  invalidConfig: "Sentry \u914D\u7F6E\u65E0\u6548\u3002",
  invalidReports: "\u9519\u8BEF\u62A5\u544A\u6279\u6B21\u65E0\u6548\u3002",
  notInitialized: "Sentry \u5C1A\u672A\u521D\u59CB\u5316\u3002",
  description: "\u5C06 Persona \u7ECF\u8131\u654F\u7684\u9519\u8BEF\u548C\u5D29\u6E83\u62A5\u544A\u53D1\u9001\u5230\u4F60\u7684 Sentry \u9879\u76EE\u3002\u672A\u586B\u5199 DSN \u65F6\u4E0D\u4F1A\u6536\u96C6\u6216\u53D1\u9001\u62A5\u544A\u3002",
  environmentTitle: "\u73AF\u5883",
  environmentDescription: "Sentry \u4E8B\u4EF6\u7684\u73AF\u5883\uFF0C\u4F8B\u5982 production \u6216 staging\u3002",
  sampleTitle: "\u91C7\u6837\u7387",
  sampleDescription: "\u8303\u56F4\u4E3A 0 \u5230 1\uFF1A1 \u53D1\u9001\u5168\u90E8\u62A5\u544A\uFF1B0.5 \u968F\u673A\u53D1\u9001\u4E00\u534A\u3002",
  logsTitle: "\u5305\u542B\u9519\u8BEF\u65E5\u5FD7\u884C",
  logsDescription: "\u5173\u95ED\u540E\u4EC5\u53D1\u9001\u5F02\u5E38\u3001\u5D29\u6E83\u548C panic\u3002",
  httpTitle: "\u5141\u8BB8 HTTP DSN",
  httpDescription: "\u4EC5\u7528\u4E8E\u53EF\u4FE1\u7684\u81EA\u5EFA\u5B9E\u4F8B\u3002HTTP \u4F1A\u660E\u6587\u4F20\u8F93\u62A5\u544A\u548C\u516C\u94A5\uFF0C\u65C1\u89C2\u8005\u53EF\u8BFB\u53D6\u62A5\u544A\u5E76\u4F2A\u9020\u4E8B\u4EF6\u3002",
  dsnDescription: "\u9879\u76EE Client Keys (DSN) \u4E2D\u7684\u503C\uFF0C\u8BF7\u5B58\u5165\u79D8\u5BC6\u5B57\u6BB5\u3002HTTP DSN \u8FD8\u9700\u8981\u5F00\u542F allow_http\u3002"
};

// src/locales/ja.json
var ja_default = {
  invalidDSN: "DSN \u306F https://<\u516C\u958B\u30AD\u30FC>@<\u30DB\u30B9\u30C8>/<\u30D7\u30ED\u30B8\u30A7\u30AF\u30C8\u756A\u53F7> \u306E\u5F62\u5F0F\u3067\u6307\u5B9A\u3057\u3066\u304F\u3060\u3055\u3044\u3002",
  httpDisabled: "HTTP DSN \u3092\u4F7F\u3046\u524D\u306B allow_http \u3092\u6709\u52B9\u306B\u3057\u3066\u304F\u3060\u3055\u3044\u3002\u30EC\u30DD\u30FC\u30C8\u3068\u516C\u958B\u30AD\u30FC\u306F\u6697\u53F7\u5316\u3055\u308C\u305A\u306B\u9001\u4FE1\u3055\u308C\u307E\u3059\u3002",
  invalidConfig: "Sentry \u306E\u8A2D\u5B9A\u304C\u7121\u52B9\u3067\u3059\u3002",
  invalidReports: "\u30A8\u30E9\u30FC\u30EC\u30DD\u30FC\u30C8\u306E\u30D0\u30C3\u30C1\u304C\u7121\u52B9\u3067\u3059\u3002",
  notInitialized: "Sentry \u306F\u307E\u3060\u521D\u671F\u5316\u3055\u308C\u3066\u3044\u307E\u305B\u3093\u3002",
  description: "\u500B\u4EBA\u60C5\u5831\u3092\u9664\u53BB\u3057\u305F Persona \u306E\u30A8\u30E9\u30FC\u3068\u30AF\u30E9\u30C3\u30B7\u30E5\u30EC\u30DD\u30FC\u30C8\u3092 Sentry \u30D7\u30ED\u30B8\u30A7\u30AF\u30C8\u3078\u9001\u4FE1\u3057\u307E\u3059\u3002DSN \u304C\u306A\u3044\u5834\u5408\u3001\u30EC\u30DD\u30FC\u30C8\u306F\u53CE\u96C6\u3082\u9001\u4FE1\u3082\u3055\u308C\u307E\u305B\u3093\u3002",
  environmentTitle: "\u74B0\u5883",
  environmentDescription: "production \u3084 staging \u306A\u3069\u306E Sentry \u30A4\u30D9\u30F3\u30C8\u74B0\u5883\u3002",
  sampleTitle: "\u30B5\u30F3\u30D7\u30EA\u30F3\u30B0\u7387",
  sampleDescription: "0\u301C1 \u306E\u5024\u30021 \u306F\u5168\u4EF6\u30010.5 \u306F\u7121\u4F5C\u70BA\u306B\u534A\u5206\u3092\u9001\u4FE1\u3057\u307E\u3059\u3002",
  logsTitle: "\u30A8\u30E9\u30FC\u30ED\u30B0\u884C\u3092\u542B\u3081\u308B",
  logsDescription: "\u7121\u52B9\u306E\u5834\u5408\u3001\u4F8B\u5916\u3001\u30AF\u30E9\u30C3\u30B7\u30E5\u3001panic \u306E\u307F\u3092\u9001\u4FE1\u3057\u307E\u3059\u3002",
  httpTitle: "HTTP DSN \u3092\u8A31\u53EF",
  httpDescription: "\u4FE1\u983C\u3067\u304D\u308B\u30BB\u30EB\u30D5\u30DB\u30B9\u30C8\u74B0\u5883\u5C02\u7528\u3067\u3059\u3002HTTP \u306F\u30EC\u30DD\u30FC\u30C8\u3068\u516C\u958B\u30AD\u30FC\u3092\u6697\u53F7\u5316\u3057\u306A\u3044\u305F\u3081\u3001\u7B2C\u4E09\u8005\u306B\u3088\u308B\u95B2\u89A7\u3084\u30A4\u30D9\u30F3\u30C8\u306E\u507D\u9020\u304C\u53EF\u80FD\u3067\u3059\u3002",
  dsnDescription: "\u30D7\u30ED\u30B8\u30A7\u30AF\u30C8\u306E Client Keys (DSN) \u306E\u5024\u3002\u79D8\u5BC6\u30D5\u30A3\u30FC\u30EB\u30C9\u306B\u4FDD\u5B58\u3057\u3066\u304F\u3060\u3055\u3044\u3002HTTP DSN \u306B\u306F allow_http \u3082\u5FC5\u8981\u3067\u3059\u3002"
};

// src/i18n.ts
var locales = { en: en_default, "zh-Hans": zh_Hans_default, ja: ja_default };
function messages(locale = "zh-Hans") {
  return Object.hasOwn(locales, locale) ? locales[locale] : en_default;
}
function publicError(key, locale) {
  return new ExtError(messages(locale)[key], "invalid_params");
}

// src/params.ts
var frame = { function: "string", module: "string", file: "string", line: "int", column: "int", in_app: "bool" };
var exception = { type: "string", value: "string", frames: { array: { struct: frame } } };
var breadcrumb = { time: "string", category: "string", message: "string", level: "string" };
var report = {
  id: "string",
  source: "string",
  kind: "string",
  level: "string",
  time: "string",
  release: "string",
  build: "string",
  commit: "string",
  os: "string",
  os_version: "string",
  device_model: "string",
  message: "string",
  exceptions: { array: { struct: exception } },
  breadcrumbs: { array: { struct: breadcrumb } },
  tags: "strings",
  previous_launch: "bool",
  fingerprint: "string",
  count: "int",
  received_at: "string"
};
var delivery = { contribution: "string", reports: { array: { struct: report } } };
var config = {
  environment: "string",
  sample_rate: { pointer: "float" },
  include_log_lines: { pointer: "bool" },
  allow_http: "bool"
};
var fail = () => {
  throw new JSONIntegerError("invalid typed Sentry parameters");
};
function zero(kind) {
  if (typeof kind === "object") return "struct" in kind ? struct(void 0, kind.struct) : null;
  return kind === "string" ? "" : kind === "bool" ? false : kind === "int" ? 0n : kind === "float" ? 0 : null;
}
function elements(value) {
  if (value.raw[0] !== "[") return fail();
  const raw = value.raw, values = [];
  let start = 1, depth = 0, quoted = false;
  for (let at = 1; at < raw.length; at++) {
    const char = raw[at];
    if (quoted) {
      if (char === "\\") at++;
      else if (char === '"') quoted = false;
      continue;
    }
    if (char === '"') quoted = true;
    else if (char === "[" || char === "{") depth++;
    else if ((char === "}" || char === "]") && depth > 0) depth--;
    else if ((char === "," || char === "]") && depth === 0) {
      const part = raw.slice(start, at).trim();
      if (part) values.push(ExactJSON.parse(part));
      start = at + 1;
    }
  }
  return values;
}
var sliceStorage = /* @__PURE__ */ new WeakMap();
function decode(value, kind, previous) {
  if (value.raw === "null") {
    if (typeof kind === "object" && ("array" in kind || "pointer" in kind) || kind === "strings") return null;
    return previous;
  }
  if (typeof kind === "object") {
    if ("struct" in kind) return struct(value, kind.struct, previous);
    if ("pointer" in kind) return decode(value, kind.pointer, previous ?? zero(kind.pointer));
    const tokens = elements(value);
    if (!tokens.length) return [];
    const storage = previous ? sliceStorage.get(previous) ?? previous : [];
    const array = tokens.map((entry, index) => {
      const decoded = decode(entry, kind.array, storage[index] ?? zero(kind.array));
      storage[index] = decoded;
      return decoded;
    });
    sliceStorage.set(array, storage);
    return array;
  }
  if (kind === "int") return value.int64(previous);
  if (kind === "strings") {
    if (value.raw[0] !== "{") return fail();
    const out = previous ?? /* @__PURE__ */ Object.create(null);
    for (const [name, entry] of members(value)) out[name] = decode(entry, "string", "");
    return out;
  }
  const parsed = JSON.parse(value.raw);
  if (kind === "float" ? typeof parsed !== "number" || !Number.isFinite(parsed) : typeof parsed !== (kind === "bool" ? "boolean" : "string")) return fail();
  return parsed;
}
function* members(value) {
  const raw = value.raw;
  let at = 1;
  const space = () => {
    while (at < raw.length && /\s/.test(raw[at])) at++;
  };
  while (at < raw.length - 1) {
    space();
    if (raw[at] === "}") break;
    const keyStart = at++;
    while (at < raw.length) {
      if (raw[at] === "\\") at += 2;
      else if (raw[at++] === '"') break;
    }
    const key = JSON.parse(raw.slice(keyStart, at));
    space();
    at++;
    space();
    const start = at;
    let depth = 0, quoted = false;
    for (; at < raw.length; at++) {
      const char = raw[at];
      if (quoted) {
        if (char === "\\") at++;
        else if (char === '"') quoted = false;
        continue;
      }
      if (char === '"') quoted = true;
      else if (char === "[" || char === "{") depth++;
      else if ((char === "]" || char === "}") && depth > 0) depth--;
      else if ((char === "," || char === "}") && depth === 0) break;
    }
    yield [key, ExactJSON.parse(raw.slice(start, at))];
    if (raw[at] === ",") at++;
  }
}
function struct(value, fields, previous) {
  const out = previous ?? /* @__PURE__ */ Object.create(null);
  for (const [name, kind] of Object.entries(fields)) {
    if (!(name in out)) out[name] = zero(kind);
    for (const entry of value?.fieldValues(name) ?? []) out[name] = decode(entry, kind, out[name]);
  }
  return out;
}
function decodeDelivery(value) {
  if (!value) return fail();
  return struct(value, delivery).reports ?? [];
}
function decodeConfig(value) {
  if (value && typeof value === "object") for (const [key, entry] of Object.entries(value)) {
    if (key.replace(/ſ/g, "s").toLowerCase() === "sample_rate" && typeof entry === "number" && !Number.isFinite(entry)) fail();
  }
  return decodeExactConfig(ExactJSON.parse(JSON.stringify(value ?? null)));
}
function decodeExactConfig(value) {
  if (value === void 0) return struct(ExactJSON.parse("null"), config);
  return struct(value, config);
}
function legacyDelivery(value) {
  const safe = (input) => {
    if (!input || typeof input !== "object") return;
    for (const [key, entry] of Object.entries(input)) {
      const folded = key.replace(/ſ/g, "s").toLowerCase();
      if (["count", "line", "column"].includes(folded) && typeof entry === "number" && !Number.isSafeInteger(entry)) fail();
      safe(entry);
    }
  };
  safe(value);
  return decodeDelivery(value === void 0 ? void 0 : ExactJSON.parse(JSON.stringify(value)));
}
function stringifyExact(value) {
  if (typeof value === "bigint") return value.toString();
  if (value === null || typeof value !== "object") return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map((item) => item === void 0 ? "null" : stringifyExact(item)).join(",")}]`;
  return `{${Object.entries(value).filter(([, entry]) => entry !== void 0).map(([key, entry]) => `${JSON.stringify(key)}:${stringifyExact(entry)}`).join(",")}}`;
}

// src/dsn.ts
function parseDSN(raw, allowHTTP = false, locale) {
  const input = raw.trim();
  let url;
  try {
    url = new URL(input);
  } catch {
    throw publicError("invalidDSN", locale);
  }
  if (url.protocol === "http:" && !allowHTTP) throw publicError("httpDisabled", locale);
  if (!["https:", "http:"].includes(url.protocol) || !url.hostname || !url.username) throw publicError("invalidDSN", locale);
  const rawPath = /^[^:]+:\/\/[^/?#]*(\/[^?#]*)?/.exec(input)?.[1] ?? "";
  const pathname = decodePath(rawPath, locale).replace(/\/$/, "");
  const split = pathname.lastIndexOf("/");
  const project = pathname.slice(split + 1);
  if (!/^\d+$/.test(project) || BigInt(project) > 18446744073709551615n) throw publicError("invalidDSN", locale);
  const path = pathname.slice(0, split);
  let publicKey;
  try {
    publicKey = decodeURIComponent(url.username);
  } catch {
    throw publicError("invalidDSN", locale);
  }
  if (/[\s,\x00-\x1f\x7f]/.test(publicKey)) throw publicError("invalidDSN", locale);
  return {
    origin: url.origin,
    path,
    project,
    publicKey,
    safe: `${url.origin}${path}/${project}`,
    endpoint: `${url.origin}${path}/api/${project}/envelope/`
  };
}
function decodePath(value, locale) {
  try {
    return decodeURIComponent(value);
  } catch {
    throw publicError("invalidDSN", locale);
  }
}

// src/event.ts
var utf8 = new TextEncoder();
var platforms = {
  hub: "go",
  manager: "go",
  web: "javascript",
  android: "java",
  ios: "cocoa",
  mac: "cocoa",
  watch: "cocoa",
  vision: "cocoa"
};
function breadcrumbMillis(time) {
  if (!time) return;
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{1,2}):(\d{2}):(\d{2})(?:[.,](\d+))?(Z|([+-])(\d{2}):(\d{2}))$/.exec(time);
  if (!match) return;
  const [, y, m, d, h, minute, second, fraction, zone, sign, oh, om] = match;
  const year = Number(y), month = Number(m), day = Number(d), hour = Number(h);
  const leap = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  if (month < 1 || month > 12 || day < 1 || day > days[month - 1] || hour > 23 || Number(minute) > 59 || Number(second) > 59) return;
  if (zone !== "Z" && (Number(oh) > 24 || Number(om) > 60)) return;
  const date = /* @__PURE__ */ new Date(0);
  date.setUTCFullYear(year, month - 1, day);
  date.setUTCHours(hour, Number(minute), Number(second), Number((fraction ?? "").slice(0, 3).padEnd(3, "0")));
  const offset = zone === "Z" ? 0 : (Number(oh) * 60 + Number(om)) * (sign === "-" ? -1 : 1);
  return date.getTime() - offset * 6e4;
}
async function eventID(report2) {
  const sum = await crypto.subtle.digest("SHA-256", utf8.encode(`${report2.source}\0${report2.id}`));
  return [...new Uint8Array(sum).slice(0, 16)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
}
async function event(report2, options) {
  const tags = { ...report2.tags, source: report2.source, kind: report2.kind };
  if (report2.previous_launch) tags.previous_launch = "true";
  const out = {
    event_id: await eventID(report2),
    timestamp: report2.time,
    platform: Object.hasOwn(platforms, report2.source) ? platforms[report2.source] : "",
    level: report2.level,
    logger: `persona.${report2.source}`,
    environment: options.environment,
    tags,
    extra: { count: report2.count ?? 0, previous_launch: report2.previous_launch ?? false, received_at: report2.received_at ?? "" },
    sdk: { name: "persona.sentry", version: options.client.replace(/^persona-sentry\//, ""), settings: { infer_ip: "never" } }
  };
  if (report2.release) out.release = `persona-${report2.source}@${report2.release}${report2.build ? `+${report2.build}` : ""}`;
  if (report2.build) out.dist = report2.build;
  if (report2.fingerprint) out.fingerprint = [report2.fingerprint];
  if (report2.message) out.message = { formatted: report2.message };
  const contexts = {};
  if (report2.os) contexts.os = { name: report2.os, version: report2.os_version ?? "" };
  if (report2.device_model) contexts.device = { model: report2.device_model };
  if (Object.keys(contexts).length) out.contexts = contexts;
  if (report2.exceptions?.length) out.exception = { values: [...report2.exceptions].reverse().map((exception2) => ({
    type: exception2.type,
    value: exception2.value ?? "",
    mechanism: { type: report2.kind, handled: report2.kind === "exception" || report2.kind === "log" },
    ...exception2.frames?.length ? { stacktrace: { frames: exception2.frames.map((frame2) => ({
      function: frame2.function ?? "",
      in_app: frame2.in_app ?? false,
      ...frame2.module ? { module: frame2.module } : {},
      ...frame2.file ? { filename: frame2.file } : {},
      ...(frame2.line ?? 0) > 0 ? { lineno: frame2.line } : {},
      ...(frame2.column ?? 0) > 0 ? { colno: frame2.column } : {}
    })) } } : {}
  })) };
  if (report2.breadcrumbs?.length) out.breadcrumbs = { values: report2.breadcrumbs.map((crumb) => {
    const millis = breadcrumbMillis(crumb.time);
    return {
      category: crumb.category,
      message: crumb.message ?? "",
      ...crumb.level ? { level: crumb.level } : {},
      ...millis !== void 0 ? { timestamp: millis / 1e3 } : {}
    };
  }) };
  return out;
}
function envelope(dsn, value, now) {
  const body = stringifyExact(value);
  const header = JSON.stringify({ event_id: value.event_id, dsn: dsn.safe, sent_at: new Date(now).toISOString().replace(/\.\d{3}Z$/, "Z") });
  const item = JSON.stringify({ type: "event", length: utf8.encode(body).length, content_type: "application/json" });
  return utf8.encode(`${header}
${item}
${body}
`);
}

// src/transport.ts
var bounds = { queue: 200, perMinute: 60, body: 8 << 20, response: 4 << 10, timeout: 2e4 };
function seconds(raw) {
  if (!/^[+-]?\d+$/.test(raw)) return;
  const digits = raw.replace(/^[+-]?0*/, "");
  if (digits.length > 19) return;
  const value = BigInt((raw.startsWith("-") ? "-" : "") + (digits || "0"));
  if (value < -9223372036854775808n || value > 9223372036854775807n) return;
  return value;
}
var after = (now, value) => {
  const duration = BigInt.asIntN(64, value * 1000000000n);
  const milliseconds = duration / 1000000n - (duration < 0n && duration % 1000000n !== 0n ? 1n : 0n);
  return Math.floor(now) + Number(milliseconds);
};
function limitedUntil(status, headers, now) {
  let until;
  for (const part of (headers.get("X-Sentry-Rate-Limits") ?? "").split(",")) {
    const [raw, categories = ""] = part.trim().split(":");
    const value2 = seconds(raw);
    if (value2 === void 0) continue;
    if (!categories || categories.split(";").some((cat) => cat === "error" || cat === "default")) {
      const time = after(now, value2);
      if (time > (until ?? -621355968e5)) until = time;
    }
  }
  if (until !== void 0) return until;
  if (status !== 429) return void 0;
  const value = seconds(headers.get("Retry-After") ?? "");
  return after(now, value !== void 0 && value > 0n ? value : 60n);
}
async function send(fetch, dsn, client, bytes, now, signal, timeout = bounds.timeout) {
  if (signal.aborted) return { status: "cancelled" };
  if (bytes.byteLength > bounds.body) return { status: "failed" };
  const controller = new AbortController();
  let finish;
  const interrupted = new Promise((resolve) => {
    finish = resolve;
  });
  const cancel = () => {
    controller.abort();
    finish({ status: "cancelled" });
  };
  signal.addEventListener("abort", cancel, { once: true });
  const timer = setTimeout(() => {
    controller.abort();
    finish({ status: "failed" });
  }, timeout);
  const work = (async () => {
    try {
      const response = await fetch(dsn.endpoint, {
        method: "POST",
        redirect: "error",
        signal: controller.signal,
        headers: { "Content-Type": "application/x-sentry-envelope", "X-Sentry-Auth": `Sentry sentry_version=7, sentry_client=${client}, sentry_key=${dsn.publicKey}` },
        body: bytes
      });
      if (controller.signal.aborted) {
        void response.body?.cancel().catch(() => {
        });
        return { status: "cancelled" };
      }
      const reader = response.body?.getReader();
      if (reader) {
        const abortRead = () => {
          void reader.cancel().catch(() => {
          });
        };
        controller.signal.addEventListener("abort", abortRead, { once: true });
        try {
          let count = 0;
          while (count < bounds.response && !controller.signal.aborted) {
            const chunk = await reader.read();
            if (chunk.done) break;
            count += chunk.value.byteLength;
          }
        } catch {
        } finally {
          controller.signal.removeEventListener("abort", abortRead);
          void reader.cancel().catch(() => {
          });
        }
      }
      if (controller.signal.aborted) return { status: "cancelled" };
      const until = limitedUntil(response.status, response.headers, now);
      if (until !== void 0) return { status: "limited", until };
      return { status: response.status >= 200 && response.status < 300 ? "sent" : "failed" };
    } catch {
      return { status: controller.signal.aborted && signal.aborted ? "cancelled" : "failed" };
    }
  })();
  try {
    return await Promise.race([work, interrupted]);
  } finally {
    clearTimeout(timer);
    signal.removeEventListener("abort", cancel);
  }
}

// src/service.ts
var SentryService = class {
  constructor(deps, version = "0.1.2") {
    this.deps = deps;
    this.now = deps.now ?? Date.now;
    this.random = deps.random ?? Math.random;
    this.options = { environment: "production", client: `persona-sentry/${version}` };
    deps.signal?.addEventListener("abort", this.onAbort, { once: true });
    if (deps.signal?.aborted) this.onAbort();
  }
  dsn;
  options;
  sample = 1;
  logs = true;
  queue = [];
  running;
  generation = 0;
  processing = false;
  minute;
  inMinute = 0;
  sent = 0;
  dropped = 0;
  pausedUntil = 0;
  phase = "unconfigured";
  cancellation = new AbortController();
  stopWork;
  now;
  random;
  onAbort = () => {
    this.cancelPending();
    void this.stop(0).catch(() => {
    });
  };
  configure(config2, rawDSN, locale) {
    if (this.phase === "stopping" || this.phase === "stopped") throw publicError("invalidConfig", locale);
    let cfg;
    try {
      cfg = decodeConfig(config2);
    } catch (error) {
      if (error instanceof JSONIntegerError) throw publicError("invalidConfig", locale);
      throw error;
    }
    if (!rawDSN) return;
    const dsn = parseDSN(rawDSN, cfg.allow_http, locale);
    this.dsn = dsn;
    this.options = { ...this.options, environment: cfg.environment || "production" };
    this.sample = Math.min(1, Math.max(0, cfg.sample_rate ?? 1));
    this.logs = cfg.include_log_lines ?? true;
    this.phase = "running";
  }
  deliver(reports) {
    if (this.phase !== "running" || !this.dsn) return;
    for (const report2 of reports) {
      if (report2.kind === "log" && !this.logs || this.sample < 1 && !(this.random() < this.sample)) continue;
      if (this.queue.length >= bounds.queue) {
        this.dropped++;
        continue;
      }
      this.queue.push(structuredClone(report2));
    }
    this.kick();
  }
  state() {
    return { phase: this.phase, queued: this.queue.length, inFlight: !!this.running, sent: this.sent, dropped: this.dropped, pausedUntil: this.pausedUntil };
  }
  /** A timeout does not cancel delivery; stop performs cancellation explicitly. */
  async flush(timeout = bounds.timeout) {
    const deadline = Date.now() + Math.max(0, timeout);
    while (this.running || this.queue.length) {
      this.kick();
      const remaining = deadline - Date.now();
      if (remaining <= 0) return false;
      let timer;
      let interrupted;
      const signal = this.cancellation.signal;
      try {
        const finished = await Promise.race([this.running.then(() => true), new Promise((resolve) => {
          interrupted = () => resolve(false);
          signal.addEventListener("abort", interrupted, { once: true });
          if (signal.aborted) interrupted();
          timer = setTimeout(interrupted, remaining);
        })]);
        if (!finished) return false;
      } finally {
        clearTimeout(timer);
        signal.removeEventListener("abort", interrupted);
      }
    }
    return true;
  }
  stop(timeout = bounds.timeout) {
    if (this.stopWork) return this.stopWork;
    this.phase = "stopping";
    this.stopWork = (async () => {
      const drained = await this.flush(timeout) && !this.cancellation.signal.aborted;
      if (!drained) this.cancelPending();
      this.phase = "stopped";
      this.dsn = void 0;
      this.deps.signal?.removeEventListener("abort", this.onAbort);
      return drained;
    })();
    return this.stopWork;
  }
  cancelPending() {
    if (this.cancellation.signal.aborted) return;
    this.generation++;
    this.dropped += this.queue.length + (this.processing ? 1 : 0);
    this.queue = [];
    this.processing = false;
    this.running = void 0;
    this.cancellation.abort();
  }
  kick() {
    if (this.running || !this.queue.length || this.phase === "stopped" || this.cancellation.signal.aborted) return;
    const generation = this.generation;
    const current = () => generation === this.generation;
    this.running = Promise.resolve().then(async () => {
      while (current() && this.queue.length && !this.cancellation.signal.aborted) {
        const report2 = this.queue.shift();
        const now = this.now();
        if (this.minute === void 0 || now - this.minute >= 6e4) {
          this.minute = now;
          this.inMinute = 0;
        }
        if (this.inMinute >= bounds.perMinute || now < this.pausedUntil) {
          this.dropped++;
          continue;
        }
        this.inMinute++;
        this.processing = true;
        try {
          const dsn = this.dsn, options = { ...this.options };
          const value = await event(report2, options);
          if (!current()) return;
          const result = await send(
            this.deps.fetch,
            dsn,
            options.client,
            envelope(dsn, value, this.now()),
            this.now(),
            this.cancellation.signal,
            this.deps.timeout
          );
          if (!current()) return;
          this.processing = false;
          if (result.status === "sent") {
            this.sent++;
            this.log("sentry: sent event");
          } else {
            this.dropped++;
            if (result.status === "limited") this.pausedUntil = result.until;
          }
        } catch {
          if (current()) this.dropped++;
        } finally {
          if (current()) this.processing = false;
        }
      }
    }).finally(() => {
      if (current()) {
        this.running = void 0;
        this.kick();
      }
    }).catch(() => {
    });
  }
  log(message) {
    try {
      this.deps.log?.(message);
    } catch {
    }
  }
};

// src/index.ts
var sourceContributions = { error_reports: [{ id: "sentry" }] };
function createRuntime(deps = {
  fetch: (input, init) => globalThis.fetch(input, init),
  // stderr stays separate from JSON-lines stdout; messages contain no report data.
  log: (message) => console.error(message)
}) {
  const service = new SentryService(deps);
  let initialized = false, active = false, locale;
  let delivery2;
  const program = {
    id: "sentry",
    version: "0.1.2",
    init: withExactInit((init, _callHub, call) => {
      if (initialized) return active ? void 0 : { error_reports: [] };
      locale = init.hub?.locale;
      active = init.active === void 0 || !!init.active.error_reports?.includes("sentry");
      const dsn = init.secrets?.dsn ?? "";
      if (active) service.configure(call ? decodeExactConfig(call.params?.field("config")) : decodeConfig(init.config), dsn, locale);
      initialized = true;
      return active ? void 0 : { error_reports: [] };
    }),
    namespaces: {
      // The SDK inspects the brand on the returned handler, not on this provider.
      get error_reports() {
        return delivery2 ??= withExactParams((method, params, call) => {
          if (method !== "deliver") throw unknownMethod(method);
          if (!initialized) throw new ExtError(publicError("notInitialized", locale).message, "not_initialized");
          try {
            const reports = call ? decodeDelivery(call.params) : legacyDelivery(params);
            if (active) service.deliver(reports);
          } catch (error) {
            if (error instanceof JSONIntegerError) throw publicError("invalidReports", locale);
            throw error;
          }
          return null;
        });
      }
    }
  };
  return { program, service };
}
function createProgram(deps) {
  return createRuntime(deps).program;
}
var index_default = createProgram();
export {
  SentryService,
  bounds,
  createProgram,
  createRuntime,
  index_default as default,
  envelope,
  event,
  eventID,
  limitedUntil,
  parseDSN,
  send,
  sourceContributions
};
