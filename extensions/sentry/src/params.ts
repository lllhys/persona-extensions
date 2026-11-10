import { ExactJSON, JSONIntegerError, type ErrorReport, type ErrorException, type ErrorFrame } from "@persona/extension";

export type Frame = Omit<ErrorFrame, "line" | "column"> & { line?: number | bigint; column?: number | bigint };
export type Exception = Omit<ErrorException, "frames"> & { frames?: Frame[] };
export type Report = Omit<ErrorReport, "count" | "exceptions"> & { count?: number | bigint; exceptions?: Exception[] };
type Fields = Record<string, Kind>;
type Kind = "string" | "bool" | "float" | "int" | "strings" | { array: Kind } | { struct: Fields } | { pointer: Kind };
const frame: Fields = { function: "string", module: "string", file: "string", line: "int", column: "int", in_app: "bool" };
const exception: Fields = { type: "string", value: "string", frames: { array: { struct: frame } } };
const breadcrumb: Fields = { time: "string", category: "string", message: "string", level: "string" };
const report: Fields = {
  id: "string", source: "string", kind: "string", level: "string", time: "string", release: "string", build: "string",
  commit: "string", os: "string", os_version: "string", device_model: "string", message: "string",
  exceptions: { array: { struct: exception } }, breadcrumbs: { array: { struct: breadcrumb } }, tags: "strings",
  previous_launch: "bool", fingerprint: "string", count: "int", received_at: "string",
};
const delivery: Fields = { contribution: "string", reports: { array: { struct: report } } };
const config: Fields = {
  environment: "string", sample_rate: { pointer: "float" }, include_log_lines: { pointer: "bool" }, allow_http: "bool",
};
const fail = (): never => { throw new JSONIntegerError("invalid typed Sentry parameters"); };
function zero(kind: Kind): any {
  if (typeof kind === "object") return "struct" in kind ? struct(undefined, kind.struct) : null;
  return kind === "string" ? "" : kind === "bool" ? false : kind === "int" ? 0n : kind === "float" ? 0 : null;
}
/** Split validated arrays without reconstructing numeric tokens. */
function elements(value: ExactJSON): ExactJSON[] {
  if (value.raw[0] !== "[") return fail();
  const raw = value.raw, values: ExactJSON[] = [];
  let start = 1, depth = 0, quoted = false;
  for (let at = 1; at < raw.length; at++) {
    const char = raw[at];
    if (quoted) { if (char === "\\") at++; else if (char === '"') quoted = false; continue; }
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
const sliceStorage = new WeakMap<any[], any[]>();
function decode(value: ExactJSON, kind: Kind, previous: any): any {
  if (value.raw === "null") {
    if (typeof kind === "object" && ("array" in kind || "pointer" in kind) || kind === "strings") return null;
    return previous;
  }
  if (typeof kind === "object") {
    if ("struct" in kind) return struct(value, kind.struct, previous);
    if ("pointer" in kind) return decode(value, kind.pointer, previous ?? zero(kind.pointer));
    // encoding/json reuses slice storage, even after a shorter duplicate array.
    // An empty array replaces storage; null clears it in the branch above.
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
    const out = previous ?? Object.create(null);
    // Go decodes each map value into a fresh string; null resets it to zero.
    for (const [name, entry] of members(value)) out[name] = decode(entry, "string", "");
    return out;
  }
  const parsed = JSON.parse(value.raw);
  if (kind === "float" ? typeof parsed !== "number" || !Number.isFinite(parsed) : typeof parsed !== (kind === "bool" ? "boolean" : "string")) return fail();
  return parsed;
}
function* members(value: ExactJSON): Generator<[string, ExactJSON]> {
  const raw = value.raw;
  let at = 1;
  const space = () => { while (at < raw.length && /\s/.test(raw[at])) at++; };
  while (at < raw.length - 1) {
    space(); if (raw[at] === "}") break;
    const keyStart = at++;
    while (at < raw.length) { if (raw[at] === "\\") at += 2; else if (raw[at++] === '"') break; }
    const key = JSON.parse(raw.slice(keyStart, at)); space(); at++; space();
    const start = at;
    let depth = 0, quoted = false;
    for (; at < raw.length; at++) {
      const char = raw[at];
      if (quoted) { if (char === "\\") at++; else if (char === '"') quoted = false; continue; }
      if (char === '"') quoted = true;
      else if (char === "[" || char === "{") depth++;
      else if ((char === "]" || char === "}") && depth > 0) depth--;
      else if ((char === "," || char === "}") && depth === 0) break;
    }
    yield [key, ExactJSON.parse(raw.slice(start, at))];
    if (raw[at] === ",") at++;
  }
}
function struct(value: ExactJSON | undefined, fields: Fields, previous?: any): any {
  const out = previous ?? Object.create(null);
  for (const [name, kind] of Object.entries(fields)) {
    if (!(name in out)) out[name] = zero(kind);
    for (const entry of value?.fieldValues(name) ?? []) out[name] = decode(entry, kind, out[name]);
  }
  return out;
}
export function decodeDelivery(value: ExactJSON | undefined): Report[] {
  // Missing parameters are an invalid empty RawMessage in Go; JSON null is valid.
  if (!value) return fail();
  return struct(value, delivery).reports ?? [];
}
export function decodeConfig(value: unknown): Record<string, any> {
  if (value && typeof value === "object") for (const [key, entry] of Object.entries(value)) {
    if (key.replace(/ſ/g, "s").toLowerCase() === "sample_rate" && typeof entry === "number" && !Number.isFinite(entry)) fail();
  }
  return decodeExactConfig(ExactJSON.parse(JSON.stringify(value ?? null)));
}
export function decodeExactConfig(value: ExactJSON | undefined): Record<string, any> {
  if (value === undefined) return struct(ExactJSON.parse("null"), config);
  return struct(value, config);
}
/** Notifications currently lack SDK raw metadata. Fail closed on visible unsafe
 * integers, without claiming to recover erased fractional tokens or duplicates. */
export function legacyDelivery(value: unknown): Report[] {
  const safe = (input: any): void => {
    if (!input || typeof input !== "object") return;
    for (const [key, entry] of Object.entries(input)) {
      const folded = key.replace(/ſ/g, "s").toLowerCase();
      if (["count", "line", "column"].includes(folded) && typeof entry === "number" && !Number.isSafeInteger(entry)) fail();
      safe(entry);
    }
  };
  safe(value);
  return decodeDelivery(value === undefined ? undefined : ExactJSON.parse(JSON.stringify(value)));
}
/** Keep integer tokens, not rounded Numbers or quoted decimal strings. */
export function stringifyExact(value: any): string {
  if (typeof value === "bigint") return value.toString();
  if (value === null || typeof value !== "object") return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(item => item === undefined ? "null" : stringifyExact(item)).join(",")}]`;
  return `{${Object.entries(value).filter(([, entry]) => entry !== undefined).map(([key, entry]) => `${JSON.stringify(key)}:${stringifyExact(entry)}`).join(",")}}`;
}
