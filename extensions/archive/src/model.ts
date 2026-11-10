export const limits = Object.freeze({ request: 12_000, page: 15_000, tool: 15_000, response: 2 << 20, fresh: 60 * 60_000, cache: 40, cacheBytes: 2 << 20, gap: 200 });
export type LimitOptions = { [K in keyof typeof limits]?: number };
export type Media = "all" | "texts" | "audio" | "movies" | "image";
export type MediaKind = Exclude<Media, "all"> | "collection" | "software" | "etree" | "data" | "web";
export type ReadCode = "invalid" | "bad_answer" | "not_found" | "rate_limited" | "slow" | "network" | "http";
export class ReadError extends Error {
  constructor(readonly code: ReadCode, readonly status?: number) { super(code); }
}
export interface Config { media: Media }
export function configFrom(raw: unknown): Config {
  if (raw == null) return { media: "all" };
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = (raw as Record<string, unknown>).media;
  if (value == null || value === "") return { media: "all" };
  if (value !== "all" && value !== "texts" && value !== "audio" && value !== "movies" && value !== "image") throw new ReadError("invalid");
  return { media: value };
}
export const clip = (text: string, most: number) => text.length <= most ? text : text.slice(0, most).trimEnd() + "…";
export const oneLine = (text: string) => text.replace(/\s+/gu, " ").trim();
export function queryText(value: unknown): string {
  if (typeof value !== "string") throw new ReadError("invalid");
  if (/[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  const query = oneLine(value);
  if (!query || Array.from(query).length > 160) throw new ReadError("invalid");
  return query;
}
export function searchPath(query: string, media: Media, limit = 20): string {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
  const q = queryText(query);
  const filter = media === "all" ? "" : ` AND mediatype:${media}`;
  return `/advancedsearch.php?${new URLSearchParams({ q: `(${q})${filter}`, fl: "identifier,title,creator,year,mediatype,downloads,description", rows: String(limit), page: "1", output: "json" })}`;
}
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  return value as Record<string, unknown>;
}
function array(value: unknown): unknown[] { if (!Array.isArray(value)) throw new ReadError("bad_answer"); return value; }
function string(value: unknown, fallback = "") { return value == null ? fallback : typeof value === "string" ? value : (() => { throw new ReadError("bad_answer"); })(); }
function integer(value: unknown, maximum = 1_000_000_000): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0 || value > maximum) throw new ReadError("bad_answer");
  return value;
}
function strings(value: unknown): string[] {
  if (value == null) return [];
  return (Array.isArray(value) ? value : [value]).slice(0, 8).map(x => clip(oneLine(string(x)), 300)).filter(Boolean);
}
export interface Item {
  identifier: string;
  title: string;
  creators: string[];
  year?: number;
  media: MediaKind;
  downloads: number;
  description: string;
  url: string;
}
export interface SearchResult { items: Item[]; total: number; at: number; stale?: ReadError }
export function parseItems(raw: unknown, at: number): SearchResult {
  const response = object(object(raw).response), total = integer(response.numFound, 1_000_000_000);
  const seen = new Set<string>();
  const items = array(response.docs).slice(0, 20).map(rawItem => {
    const item = object(rawItem), identifier = string(item.identifier);
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$/.test(identifier)) throw new ReadError("bad_answer");
    if (seen.has(identifier)) throw new ReadError("bad_answer");
    seen.add(identifier);
    const title = clip(oneLine(string(item.title)), 500);
    if (!title) throw new ReadError("bad_answer");
    const media = item.mediatype;
    if (media !== "texts" && media !== "audio" && media !== "movies" && media !== "image" && media !== "collection" && media !== "software" && media !== "etree" && media !== "data" && media !== "web") throw new ReadError("bad_answer");
    const year = item.year == null ? undefined : integer(item.year, 2200);
    return { identifier, title, creators: strings(item.creator), ...(year === undefined ? {} : { year }), media: media as MediaKind,
      downloads: integer(item.downloads ?? 0), description: clip(strings(item.description).join(" · "), 1000), url: `https://archive.org/details/${encodeURIComponent(identifier)}` };
  });
  return { items, total, at };
}
