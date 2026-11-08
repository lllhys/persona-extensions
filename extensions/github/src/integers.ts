import { ExactJSON, JSONIntegerError, type ExactCall } from "@persona/extension";

/** Keep unsafe Go integers as bigint internally, never as a rounded Number. */
export type Integer = number | bigint;
export const integer = (value: bigint): Integer => value >= BigInt(Number.MIN_SAFE_INTEGER) && value <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(value) : value;
export const isInteger = (value: unknown): value is Integer => typeof value === "bigint" ? value >= -9223372036854775808n && value <= 9223372036854775807n : typeof value === "number" && Number.isSafeInteger(value);
/** Go strconv.Atoi spelling for string-carried IDs; JSON numeric tokens use int64() directly. */
export function decimal(text: string): Integer {
  if (!/^[+-]?[0-9]+$/.test(text)) throw new JSONIntegerError("expected a decimal integer string");
  const digits = text.replace(/^[+-]/, "").replace(/^0+(?=\d)/, "");
  return integer(ExactJSON.parse((text[0] === "-" ? "-" : "") + digits).int64());
}
export const absolute = (value: Integer): Integer => value < 0 ? typeof value === "bigint" ? -value : -value : value;
export const bounded = (value: Integer, low: number, high: number): number => value < low ? low : value > high ? high : Number(value);

/** JSON.stringify with exact bigint tokens; only source-owned data reaches this writer. */
export function stringify(value: unknown): string {
  const markers: string[] = [];
  const raw = JSON.stringify(value, (_key, entry: unknown) => {
    if (typeof entry === "number" && !Number.isFinite(entry)) throw new JSONIntegerError("nonfinite value is not JSON");
    if (typeof entry !== "bigint") return entry;
    if (!isInteger(entry)) throw new JSONIntegerError("integer outside signed int64");
    markers.push(String(entry));
    return `\u0000persona-int64-${markers.length - 1}\u0000`;
  });
  if (raw === undefined) throw new TypeError("value is not JSON");
  // Never replace user strings which resemble markers: allocate a disjoint prefix first.
  if (markers.length && /\\u0000persona-int64-/.test(JSON.stringify(value, (_key, entry: unknown) => typeof entry === "bigint" ? null : entry))) {
    return writeJSON(value);
  }
  return raw.replace(/"\\u0000persona-int64-(\d+)\\u0000"/g, (_match, index: string) => markers[Number(index)]!);
}
function writeJSON(value: unknown): string {
  if (typeof value === "bigint") { if (!isInteger(value)) throw new JSONIntegerError("integer outside signed int64"); return String(value); }
  if (Array.isArray(value)) return "[" + value.map(entry => entry === undefined ? "null" : writeJSON(entry)).join(",") + "]";
  if (value && typeof value === "object") return "{" + Object.entries(value).filter(([, entry]) => entry !== undefined).map(([key, entry]) => JSON.stringify(key) + ":" + writeJSON(entry)).join(",") + "}";
  return JSON.stringify(value) ?? "null";
}

/** Original Go struct decoding, including ordered duplicates, folded keys and scalar nulls. */
export function strings(raw: ExactJSON, names: readonly string[]): Record<string, string> {
  const result: Record<string, string> = {};
  for (const name of names) {
    let value = "";
    for (const field of raw.fieldValues(name)) {
      const decoded: unknown = JSON.parse(field.raw);
      if (decoded === null) continue;
      if (typeof decoded !== "string") throw new JSONIntegerError("expected a string field");
      value = decoded;
    }
    result[name] = value;
  }
  return result;
}
export function pointerInteger(raw: ExactJSON, name: string): Integer | undefined {
  let value: bigint | undefined;
  for (const field of raw.fieldValues(name)) value = field.raw === "null" ? undefined : field.int64(value);
  return value === undefined ? undefined : integer(value);
}
export function exactParams(params: unknown, call?: ExactCall): ExactJSON {
  // Events/fixture calls have no raw metadata. Never synthesize lexemes for a real RPC.
  return call ? call.params ?? ExactJSON.parse("null") : ExactJSON.parse(stringify(params ?? null));
}
export interface ReadArgs { query: string; kind: string; limit: Integer; repo: string; number: Integer; comments?: Integer; path: string; ref: string }
export function readArgs(raw: ExactJSON): ReadArgs {
  const text = strings(raw, ["query", "kind", "repo", "path", "ref"]);
  const ints = raw.int64Fields(["limit", "number"]);
  return { ...text, query: text.query!, kind: text.kind!, repo: text.repo!, path: text.path!, ref: text.ref!, limit: integer(ints.limit), number: integer(ints.number), comments: pointerInteger(raw, "comments") };
}

export function stringArray(raw: ExactJSON): string[] | undefined {
  const value: unknown = JSON.parse(raw.raw);
  if (value === null) return undefined;
  if (!Array.isArray(value)) throw new JSONIntegerError("expected a string array");
  return value.map(entry => {
    if (entry === null) return "";
    if (typeof entry !== "string") throw new JSONIntegerError("expected a string array element");
    return entry;
  });
}
/** Map keys are case-sensitive, unlike struct fields; repeated maps merge unless null clears them. */
export function stringMapFields(raw: ExactJSON, name: string): Record<string, string> | undefined {
  let result: Record<string, string> | undefined;
  for (const field of raw.fieldValues(name)) {
    if (field.raw === "null") { result = undefined; continue; }
    result ??= Object.create(null) as Record<string, string>;
    for (const [key, value] of objectEntries(field)) {
      const decoded: unknown = JSON.parse(value.raw);
      if (decoded !== null && typeof decoded !== "string") throw new JSONIntegerError("expected a string map value");
      Object.defineProperty(result, key, { value: decoded ?? "", writable: true, enumerable: true, configurable: true });
    }
  }
  return result;
}
function* objectEntries(raw: ExactJSON): Generator<[string, ExactJSON]> {
  if (raw.raw === "null") return;
  if (raw.raw[0] !== "{") throw new JSONIntegerError("expected an object or null");
  const text = raw.raw; let at = 1;
  const space = () => { while (at < text.length && /[ \t\r\n]/.test(text[at]!)) at++; };
  const quoted = () => { const start = at++; while (at < text.length) { const c = text[at++]!; if (c === "\\") at++; else if (c === '"') break; } return text.slice(start, at); };
  space();
  while (text[at] !== "}") {
    const key = JSON.parse(quoted()) as string; space(); at++; space(); const start = at;
    if (text[at] === '"') quoted();
    else if (text[at] === "{" || text[at] === "[") {
      let depth = 0;
      do { const c = text[at]; if (c === '"') { quoted(); continue; } if (c === "{" || c === "[") depth++; else if (c === "}" || c === "]") depth--; at++; } while (depth);
    } else while (at < text.length && !/[ \t\r\n,}\]]/.test(text[at]!)) at++;
    yield [key, ExactJSON.parse(text.slice(start, at))];
    space(); if (text[at] === ",") { at++; space(); }
  }
}
export function statusChanges(raw: ExactJSON): { ref: string; field: string; value: string }[] | undefined {
  if (raw.raw === "null") return undefined;
  if (raw.raw[0] !== "[") throw new JSONIntegerError("expected a status-change array");
  const text = raw.raw; let at = 1; const result: { ref: string; field: string; value: string }[] = [];
  const space = () => { while (at < text.length && /[ \t\r\n]/.test(text[at]!)) at++; };
  space();
  while (text[at] !== "]") {
    const start = at; let depth = 0, quoted = false;
    do {
      const c = text[at]!;
      if (quoted && c === "\\") { at += 2; continue; }
      if (c === '"') quoted = !quoted;
      if (!quoted) { if (c === "{" || c === "[") depth++; else if (c === "}" || c === "]") depth--; }
      at++;
    } while (at < text.length && (quoted || depth > 0 || !/[,\]]/.test(text[at]!)));
    const decoded = strings(ExactJSON.parse(text.slice(start, at)), ["ref", "field", "value"]);
    result.push({ ref: decoded.ref!, field: decoded.field!, value: decoded.value! });
    space(); if (text[at] === ",") { at++; space(); }
  }
  return result;
}
export function optionArgs(raw: ExactJSON): { ci: string; muted_repos?: string[]; ignore_branches?: string[] } {
  const result: { ci: string; muted_repos?: string[]; ignore_branches?: string[] } = { ci: strings(raw, ["ci"]).ci! };
  for (const name of ["muted_repos", "ignore_branches"] as const) for (const field of raw.fieldValues(name)) result[name] = stringArray(field);
  return result;
}

const numericFields = new Set(["id", "number", "databaseId", "run_id", "check_suite_id", "in_reply_to_id", "line", "start_line", "original_line", "startLine", "total_count", "comments", "changed_files", "additions", "deletions", "commits", "size", "n", "l", "start"]);
/** Parse bounded API/state JSON before rounding typed numeric identities. Unknown fields retain JSON semantics.
 * String IDs (notifications and GraphQL node IDs) stay strings; numeric IDs obey signed int64. */
export function parseJSON(text: string): unknown {
  JSON.parse(text); // Validate the entire document before scanning tokens.
  let at = 0;
  const space = () => { while (/\s/.test(text[at] ?? "") && at < text.length) at++; };
  const quoted = (): string => {
    const start = at++;
    while (at < text.length) { const c = text[at++]!; if (c === "\\") at++; else if (c === '"') break; }
    return JSON.parse(text.slice(start, at)) as string;
  };
  const value = (key = "", depth = 0): unknown => {
    if (depth > 128) throw new JSONIntegerError("JSON nesting exceeds source budget");
    space();
    if (text[at] === '"') return quoted();
    if (text[at] === "{") {
      at++; space(); const result: Record<string, unknown> = {};
      while (text[at] !== "}") {
        const name = quoted(); space(); at++;
        const entry = value(name, depth + 1);
        // Define rather than assign: __proto__ is an ordinary JSON member.
        Object.defineProperty(result, name, { value: entry, enumerable: true, writable: true, configurable: true });
        space(); if (text[at] === ",") { at++; space(); } else break;
      }
      at++; return result;
    }
    if (text[at] === "[") {
      at++; space(); const result: unknown[] = [];
      while (text[at] !== "]") { result.push(value("", depth + 1)); space(); if (text[at] === ",") { at++; space(); } else break; }
      at++; return result;
    }
    const start = at; while (at < text.length && !/[\s,}\]]/.test(text[at]!)) at++;
    const token = text.slice(start, at);
    if (numericFields.has(key) && token !== "null" && /^[-\d]/.test(token)) return integer(ExactJSON.parse(token).int64());
    return JSON.parse(token) as unknown;
  };
  return value();
}
