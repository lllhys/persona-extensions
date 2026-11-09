import simpleFold from "./simple-fold.json";

/** Go Unicode 17.0.0 simple folding: no normalization or multi-rune expansion. */
export const foldName = (s: string) => Array.from(s, rune => {
  const cp = rune.codePointAt(0)!;
  return String.fromCodePoint((simpleFold as Record<string, number>)[String(cp)] ?? cp);
}).join("");
export const equalFold = (a: string, b: string) => foldName(a) === foldName(b);
/** strings.ToLower maps each rune independently, without final-sigma context. */
export const lower = (s: string) => Array.from(s, rune => rune === "İ" ? "i" : rune.toLowerCase()).join("");
/** Go's White_Space includes NEL, but not ECMAScript's BOM whitespace. */
export const trimSpace = (s: string) => s.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, "");
export const webBase = "https://www.v2ex.com";
export const nodeName = /^[A-Za-z0-9_-]{1,64}$/;
export const limits = Object.freeze({ gap: 1000, refresh: 30_000, list: 300_000, topic: 600_000,
  request: 20_000, page: 15_000, tool: 25_000, pause: 600_000, pauseMost: 3_600_000,
  entries: 200, bytes: 32 << 20, response: 8 << 20, quota: 20 });
export type Limits = typeof limits;
export type LimitOptions = { [K in keyof Limits]?: number };
export interface Config { list?: string; nodes?: string[]; exclude_nodes?: string[]; keywords?: string[]; exclude_keywords?: string[]; tz?: string }
export type Integer = number | bigint;
export interface Topic { id: string; title: string; content: string; replies: Integer; created: Integer; lastTouched: Integer; node: { name: string; title: string }; author: string }
export interface Reply { id: string; content: string; created: Integer; author: string }
export type ErrorCode = "rate_limited" | "challenged" | "slow" | "bad_answer" | "not_found" | "no_node" | "no_topic" | "network" | "http" | "invalid";
export class ReadError extends Error { constructor(readonly code: ErrorCode, readonly status?: number) { super(code); } }
export interface Reading { body: string; at: number; live: boolean; stale?: ReadError }
export interface Listing { topics: Topic[]; at: number; stale?: ReadError; missed: { name: string; error: ReadError }[] }
export interface Thread { topic: Topic; replies: Reply[]; at: number; stale?: ReadError; repliesErr?: ReadError }
export const oneLine = (s: string) => s.replace(/\s+/gu, " ").trim();
export const tidy = (s: string) => s.replace(/\r\n/g, "\n").trim();
export const length = (s: string) => Array.from(s).length;
export const clip = (s: string, n: number) => length(s) <= n ? s : Array.from(s).slice(0, n).join("").trimEnd() + "…";
export const nonEmpty = (xs: readonly string[] = []) => xs.map(trimSpace).filter(Boolean);
export function chunks(s: string, n = 1800): string[] {
  const out: string[] = [], runes = Array.from(s);
  while (runes.length > n) {
    let cut = n;
    for (let i = n; i > n / 2; i--) if (runes[i - 1] === "\n") { cut = i; break; }
    const part = runes.splice(0, cut).join("").replace(/\n+$/g, "");
    if (part.trim()) out.push(part);
  }
  const rest = runes.join("").trim(); if (rest) out.push(rest);
  return out;
}
export function cleanNodes(names: readonly string[] = []) {
  const good: string[] = [], bad: string[] = [];
  for (const name of nonEmpty(names)) {
    if (!nodeName.test(name)) bad.push(name);
    else if (good.length < 5 && !good.includes(name)) good.push(name);
  }
  return { good, bad };
}
export function validTimezone(tz: string): boolean { if (tz === "Local") return true; try { new Intl.DateTimeFormat("en", { timeZone: tz }); return true; } catch { return false; } }
export function configFrom(raw: unknown): Config {
  if (raw === undefined || raw === null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const c = raw as Record<string, unknown>, out: Config = {};
  const keys = ["list", "tz", "nodes", "exclude_nodes", "keywords", "exclude_keywords"] as const;
  // Raw initialization and HTTP input retain ordered occurrences via parseJSON.
  // Direct authoring calls without a raw RPC frame use the supplied object.
  const fields = occurrences.get(c) ?? Object.entries(c).map(([key, value]) => ({ key, value }));
  for (const { key: name, value } of fields) {
    const key = keys.find(key => key === name) ?? keys.find(key => equalFold(key, name));
    if (key === undefined) continue;
    if (key === "list" || key === "tz") {
      if (value == null) continue;
      if (typeof value !== "string") throw new ReadError("invalid");
      out[key] = value;
    } else if (value == null) delete out[key];
    else {
      if (!Array.isArray(value) || !value.every(v => v == null || typeof v === "string")) throw new ReadError("invalid");
      out[key] = value.map(v => v ?? "");
    }
  }
  if (out.tz && !validTimezone(out.tz)) throw new ReadError("invalid");
  return out;
}
export function filter(topics: Topic[], cfg: Config): Topic[] {
  const excluded = nonEmpty(cfg.exclude_nodes).map(foldName);
  const include = nonEmpty(cfg.keywords).map(lower), exclude = nonEmpty(cfg.exclude_keywords).map(lower);
  return topics.filter(t => {
    const text = lower(t.title + "\n" + t.content);
    return !excluded.some(n => n === foldName(t.node.name) || n === foldName(t.node.title))
      && !exclude.some(k => text.includes(k)) && (!include.length || include.some(k => text.includes(k)));
  });
}
export function topicID(value: unknown): string {
  let raw: string;
  if (typeof value === "number" && Number.isSafeInteger(value)) raw = String(value);
  else if (typeof value === "bigint") raw = value.toString();
  else if (typeof value === "string") raw = value.trim();
  else throw new ReadError("invalid");
  const link = /^(?:(?:https?:\/\/)?(?:www\.)?v2ex\.com)?\/t\/(\d+)(?:[/?#].*)?$/i.exec(raw);
  const digits = link?.[1] ?? (/^\d+$/.test(raw) ? raw : "");
  if (!digits || BigInt(digits) <= 0n || BigInt(digits) > 9223372036854775807n) throw new ReadError("invalid");
  return BigInt(digits).toString();
}
export const topicURL = (id: string) => `${webBase}/t/${id}`;
export const topicPath = (id: string) => `/topics/show.json?id=${topicID(id)}`;
export const repliesPath = (id: string) => `/replies/show.json?topic_id=${topicID(id)}`;
export const nodePath = (name: string) => { if (!nodeName.test(name)) throw new ReadError("no_node"); return `/topics/show.json?node_name=${encodeURIComponent(name)}`; };
export function latestReplies(replies: Reply[], n: number) { return replies.slice(Math.max(0, replies.length - n)).map((reply, i) => ({ reply, floor: Math.max(0, replies.length - n) + i + 1 })); }

/** Validate the unmodified JSON first, then retain number and RawMessage lexemes.
 * RPC numbers already decoded by the SDK cannot recover this information here. */
class FractionToken { constructor(readonly raw: string) {} }
interface Occurrence { key: string; value: unknown; raw: string }
const occurrences = new WeakMap<object, Occurrence[]>();
export function parseJSON(body: string): unknown {
  try {
    JSON.parse(body); // Syntax validation must precede any lossless interpretation.
    let i = 0;
    const space = () => { while (/^[\t\n\r ]$/.test(body[i] ?? "")) i++; };
    const string = (): string => {
      const start = i++;
      while (i < body.length) { if (body[i] === "\\") i += 2; else if (body[i++] === '"') break; }
      return JSON.parse(body.slice(start, i));
    };
    // Containers use an explicit stack: an ignored deep field must not exhaust
    // the JavaScript call stack. Object occurrences keep their original slices.
    interface Frame { out: unknown[] | Record<string, unknown>; start: number; next: "value" | "separator"; occurrence?: Occurrence }
    const stack: Frame[] = [];
    const value = (parent?: Frame, key?: string): unknown => {
      space(); const start = i, c = body[i]; let out: unknown, frame: Frame | undefined;
      if (c === "[" || c === "{") {
        out = c === "[" ? [] : {};
        if (c === "{") occurrences.set(out as object, []);
        frame = { out: out as Frame["out"], start, next: "value" };
        if (stack.length >= 10_000) throw new ReadError("bad_answer");
        stack.push(frame); i++;
      } else if (c === '"') out = string();
      else {
        while (i < body.length && !/^[\t\n\r ,\]}]$/.test(body[i])) i++;
        const token = body.slice(start, i);
        if (token === "null") out = null;
        else if (token === "true") out = true;
        else if (token === "false") out = false;
        else if (!/^-?(0|[1-9][0-9]*)$/.test(token)) out = new FractionToken(token);
        else {
          const n = BigInt(token);
          out = n > BigInt(Number.MAX_SAFE_INTEGER) || n < BigInt(Number.MIN_SAFE_INTEGER) ? n : Number(token);
        }
      }
      if (parent) {
        if (Array.isArray(parent.out)) parent.out.push(out);
        else {
          const occurrence = { key: key!, value: out, raw: frame ? "" : body.slice(start, i) };
          occurrences.get(parent.out)!.push(occurrence);
          Object.defineProperty(parent.out, key!, { value: out, enumerable: true, configurable: true, writable: true });
          if (frame) frame.occurrence = occurrence;
        }
      }
      return out;
    };
    const root = value();
    while (stack.length) {
      const frame = stack[stack.length - 1]; space();
      if (body[i] === "]" || body[i] === "}") {
        i++; stack.pop();
        if (frame.occurrence) frame.occurrence.raw = body.slice(frame.start, i);
      } else if (frame.next === "separator") { i++; frame.next = "value"; }
      else {
        let key: string | undefined;
        if (!Array.isArray(frame.out)) { key = string(); space(); i++; }
        frame.next = "separator"; value(frame, key);
      }
    }
    return root;
  } catch { throw new ReadError("bad_answer"); }
}
export function parseArray(body: string): unknown[] {
  const value = parseJSON(body);
  if (!Array.isArray(value)) throw new ReadError("bad_answer");
  return value;
}
const object = (v: unknown): Record<string, unknown> => { if (v == null) return {}; if (typeof v !== "object" || Array.isArray(v) || v instanceof FractionToken) throw new ReadError("bad_answer"); return v as Record<string, unknown>; };
const str = (v: unknown) => { if (v == null) return ""; if (typeof v !== "string") throw new ReadError("bad_answer"); return v; };
export const int64 = (v: unknown): Integer => {
  if (v == null) return 0;
  if ((typeof v !== "number" || !Number.isSafeInteger(v)) && typeof v !== "bigint") throw new ReadError("bad_answer");
  const n = BigInt(v);
  if (n < -9223372036854775808n || n > 9223372036854775807n) throw new ReadError("bad_answer");
  return v as Integer;
};
export type Field = "string" | "integer" | "boolean" | "raw" | Schema;
export interface Schema { [key: string]: Field }
export interface Decoded { [key: string]: string | Integer | boolean | Decoded }
/** Decode each occurrence like encoding/json: errors persist, structs merge,
 * null scalars leave prior values intact, and field names use simple folding.
 * Refusals deliberately consume partial values even when decoding reports errors. */
export function decodeStruct(raw: unknown, schema: Schema): { value: Decoded; errors: number } {
  let errors = 0;
  const zero = (spec: Schema): Decoded => Object.fromEntries(Object.entries(spec).map(([key, field]) =>
    [key, typeof field === "object" ? zero(field) : field === "integer" ? 0 : field === "boolean" ? false : ""]));
  const apply = (input: unknown, spec: Schema, out: Decoded) => {
    if (input == null) return;
    let obj: Record<string, unknown>;
    try { obj = object(input); } catch { errors++; return; }
    const fields = occurrences.get(obj) ?? Object.entries(obj).map(([key, value]) => ({ key, value, raw: JSON.stringify(value) ?? "" }));
    for (const occurrence of fields) {
      const key = Object.hasOwn(spec, occurrence.key) ? occurrence.key : Object.keys(spec).find(key => equalFold(key, occurrence.key));
      if (key === undefined) continue;
      const field = spec[key], v = occurrence.value;
      if (typeof field === "object") { apply(v, field, out[key] as Decoded); continue; }
      if (field === "raw") { out[key] = occurrence.raw; continue; }
      if (v == null) continue;
      try {
        if (field === "integer") out[key] = int64(v);
        else if (field === "string") out[key] = str(v);
        else if (typeof v === "boolean") out[key] = v;
        else errors++;
      } catch { errors++; }
    }
  };
  const value = zero(schema); apply(raw, schema, value); return { value, errors };
}
const memberSchema: Schema = { username: "string" };
const topicSchema: Schema = { id: "integer", title: "string", content: "string", replies: "integer", created: "integer", last_touched: "integer",
  deleted: "raw", node: { name: "string", title: "string" }, member: memberSchema };
export function parseTopics(body: string): Topic[] {
  return parseArray(body).map(raw => {
    const decoded = decodeStruct(raw, topicSchema);
    if (decoded.errors) throw new ReadError("bad_answer");
    const t = decoded.value, n = t.node as Decoded, m = t.member as Decoded;
    const topic: Topic = { id: String(t.id), title: t.title as string, content: t.content as string, replies: t.replies as Integer,
      created: t.created as Integer, lastTouched: t.last_touched as Integer, node: { name: n.name as string, title: n.title as string }, author: m.username as string };
    return { topic, deleted: !["", "0", "null", "false", '"0"', '""'].includes(t.deleted as string) };
  }).filter(t => !t.deleted).map(t => t.topic);
}
export function parseReplies(body: string): Reply[] { return parseArray(body).map(raw => {
  const decoded = decodeStruct(raw, { id: "integer", content: "string", created: "integer", member: memberSchema });
  if (decoded.errors) throw new ReadError("bad_answer");
  const r = decoded.value, m = r.member as Decoded;
  return { id: String(r.id), content: r.content as string, created: r.created as Integer, author: m.username as string };
}); }
