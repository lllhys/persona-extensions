import { decodeHTML } from "entities";
import { parse, LosslessNumber } from "lossless-json";
import { InputError } from "./errors.ts";
export const lists = { hot: "/hot.rss", daily: "/top.rss?period=daily", weekly: "/top.rss?period=weekly", latest: "/latest.rss" } as const;
export type List = keyof typeof lists;
export const isList = (value: unknown): value is List => typeof value === "string" && Object.hasOwn(lists, value);
export interface Config { list?: string; categories?: string[]; exclude_categories?: string[]; keywords?: string[]; exclude_keywords?: string[]; hide_nsfw?: boolean; hide_pinned?: boolean; tz?: string }
export interface Settings extends Required<Config> { list: List }
export interface HotTopic { id: string; title: string; url: string; author: string; category: string; excerpt: string; posts: Integer; people: Integer; at: number; pinned: boolean }
export interface Segment { code: boolean; text: string }
// Safe integers stay numbers for existing consumers; larger signed-int64 values
// retain their exact decimal text for display and BigInt comparisons.
export type Integer = number | string;
export interface Post { id: string; username: string; at: number | null; cooked: string; number: Integer; type: Integer; hidden: boolean; score: number; likes: Integer }
export interface Topic { id: string; title: string; postsCount: Integer; likeCount: Integer; views: Integer; people: Integer; at: number | null; tags: string[]; posts: Post[] }
export const oneLine = (s: string) => s.replace(/\s+/gu, " ").trim();
export const decode = decodeHTML;
/** Go-compatible rune clipping, including the trailing ellipsis outside the budget. */
export function clip(s: string, n: number): string { const runes = Array.from(s); return runes.length <= n ? s : runes.slice(0, n).join("").trimEnd() + "…"; }
export function chunks(s: string, n = 1800): string[] {
  const out: string[] = []; let runes = Array.from(s);
  while (runes.length > n) {
    let cut = n;
    for (let i = n; i > n / 2; i--) if (runes[i - 1] === "\n") { cut = i; break; }
    const part = runes.slice(0, cut).join("").replace(/\n+$/u, ""); if (part.trim()) out.push(part);
    runes = runes.slice(cut);
  }
  const rest = runes.join("").replace(/^\n+|\n+$/gu, ""); if (rest.trim()) out.push(rest); return out;
}
/** Scan disjoint path segments, preserving the original slug/floor precedence without backtracking. */
function linkedID(input: string): string {
  const digitsAt = (start: number): string => {
    let end = start; while (end < input.length && input.charCodeAt(end) >= 48 && input.charCodeAt(end) <= 57) end++;
    return input.slice(start, end);
  };
  for (let from = 0; ; ) {
    const start = input.indexOf("/t/", from); if (start < 0) return "";
    const first = start + 3, slash = input.indexOf("/", first);
    const segment = input.slice(first, slash < 0 ? input.length : slash);
    if (slash >= 0 && /[^0-9]/u.test(segment)) {
      const digits = digitsAt(slash + 1);
      if (digits) return digits;
    }
    const digits = /^[0-9]+/u.exec(segment)?.[0]; if (digits) return digits;
    // No other /t/ can start within the segment already scanned.
    if (slash < 0) return "";
    from = slash;
  }
}
export const inInt64 = (n: number) => Number.isInteger(n) && BigInt(n) >= -9223372036854775808n && BigInt(n) <= 9223372036854775807n;
/** Bound decimal magnitude lexically before BigInt, including hostile long tokens. */
export function decimalInt64(token: string): bigint {
  if (!/^[+-]?[0-9]+$/u.test(token)) throw new Error("invalid integer token");
  const negative = token[0] === "-", unsigned = token.replace(/^[+-]?0*/u, "") || "0";
  const limit = negative ? "9223372036854775808" : "9223372036854775807";
  if (unsigned.length > limit.length || unsigned.length === limit.length && unsigned > limit) throw new Error("integer outside int64");
  return BigInt((negative ? "-" : "") + unsigned);
}
/** IDs stay decimal strings. SDK numbers expose only their already-decoded value;
 * original RPC numeric lexemes remain an upstream protocol gate. */
export function topicID(value: unknown): string {
  // Keep the existing conservative guard for already-decoded numeric IDs:
  // dispatching an unsafe value could request a different topic after SDK rounding.
  // Exact raw state/HTTP integers and string IDs do not have this restriction.
  if (typeof value === "number" && (!Number.isSafeInteger(value) || value < 0)) throw new InputError("invalidTopic");
  if (typeof value !== "string" && typeof value !== "number") throw new InputError("invalidTopic");
  const input = (typeof value === "number" ? BigInt(value).toString() : value).trim();
  const digits = linkedID(input) || (/^[0-9]+$/u.test(input) ? input : "");
  if (!digits) throw new InputError("invalidTopic");
  try { return decimalInt64(digits).toString(); } catch { throw new InputError("invalidTopic"); }
}
/** Validate JSON syntax before lossless parsing. Non-integer tokens retain their
 * type and lexeme, so saved navigation cannot turn 1.0 or 1e0 into topic 1. */
export function parseJSON(raw: string): unknown {
  JSON.parse(raw);
  return parse(raw, undefined, token => /^-?[0-9]+$/u.test(token)
    ? Number.isSafeInteger(Number(token)) ? Number(token) : token
    : new LosslessNumber(token));
}
export const topicURL = (id: string) => `https://linux.do/t/topic/${id}`;
export const localZone = () => new Intl.DateTimeFormat().resolvedOptions().timeZone;
export const resolveZone = (zone?: string) => !zone || zone === "Local" ? localZone() : zone;
export function settings(raw: unknown = {}, timezone = "Local"): Settings {
  if (raw === null) raw = {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new InputError("configObject");
  const c = raw as Record<string, unknown>;
  const words = (key: string): string[] => {
    if (c[key] === undefined || c[key] === null) return [];
    if (!Array.isArray(c[key]) || !(c[key] as unknown[]).every(x => typeof x === "string")) throw new InputError("configWords", key);
    return (c[key] as string[]).map(x => x.trim()).filter(Boolean);
  };
  for (const key of ["list", "tz"]) if (c[key] !== undefined && c[key] !== null && typeof c[key] !== "string") throw new InputError("configString", key);
  for (const key of ["hide_nsfw", "hide_pinned"]) if (c[key] !== undefined && c[key] !== null && typeof c[key] !== "boolean") throw new InputError("configBoolean", key);
  const tz = (c.tz as string | undefined) ?? "";
  // Validate explicitly configured zones rather than silently changing their meaning.
  try { new Intl.DateTimeFormat("en", { timeZone: resolveZone(tz || timezone) }); }
  catch { throw new InputError("configZone"); }
  return { list: isList(c.list) ? c.list : "hot", tz, categories: words("categories"), exclude_categories: words("exclude_categories"), keywords: words("keywords"), exclude_keywords: words("exclude_keywords"), hide_nsfw: c.hide_nsfw !== false, hide_pinned: c.hide_pinned !== false };
}
export function filter(topics: HotTopic[], f: Config): HotTopic[] {
  const words = (xs?: string[]) => xs?.map(x => x.trim().toLowerCase()).filter(Boolean) ?? [];
  const any = (xs: string[], s: string) => xs.some(x => s.includes(x));
  const cats = words(f.categories), excludeCats = words(f.exclude_categories), keys = words(f.keywords), excludeKeys = words(f.exclude_keywords);
  return topics.filter(t => {
    const text = (t.title + "\n" + t.excerpt).toLowerCase(), category = t.category.toLowerCase();
    return !(f.hide_nsfw && t.title.toUpperCase().includes("NSFW")) && !(f.hide_pinned && t.pinned)
      && (!cats.length || any(cats, category)) && !any(excludeCats, category) && !any(excludeKeys, text) && (!keys.length || any(keys, text));
  });
}
export const opening = (t: Topic) => t.posts.find(p => p.number === 1);
