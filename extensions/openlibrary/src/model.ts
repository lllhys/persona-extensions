export const limits = Object.freeze({ request: 12_000, page: 15_000, tool: 15_000, response: 2 << 20, fresh: 60 * 60_000, cache: 40, cacheBytes: 2 << 20, gap: 250 });
export type LimitOptions = { [K in keyof typeof limits]?: number };
export type ReadCode = "invalid" | "bad_answer" | "not_found" | "rate_limited" | "slow" | "network" | "http";
export class ReadError extends Error {
  constructor(readonly code: ReadCode, readonly status?: number) { super(code); }
}
export const clip = (text: string, most: number) => Array.from(text).length <= most ? text : Array.from(text).slice(0, most).join("").trimEnd() + "…";
export const plain = (text: string) => text.replace(/\s+/gu, " ").trim();
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  return value as Record<string, unknown>;
}
function array(value: unknown): unknown[] { if (!Array.isArray(value)) throw new ReadError("bad_answer"); return value; }
function string(value: unknown, fallback = "") {
  if (value == null) return fallback;
  if (typeof value !== "string") throw new ReadError("bad_answer");
  return value;
}
function integer(value: unknown, maximum = 1_000_000): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0 || value > maximum) throw new ReadError("bad_answer");
  return value;
}
export type Access = "public" | "borrowable" | "printdisabled" | "no_ebook";
export interface Book {
  key: string;
  title: string;
  authors: string[];
  year?: number;
  editions?: number;
  access: Access;
  url: string;
  readingURL?: string;
}
export interface Work extends Book {
  description: string;
  subjects: string[];
  at: number;
  stale?: ReadError;
}
export interface SearchResult { books: Book[]; total: number; at: number; stale?: ReadError }
export function workKey(value: unknown): string {
  if (typeof value !== "string" || !/^OL[1-9][0-9]{0,9}W$/.test(value)) throw new ReadError("invalid");
  return value;
}
export function queryText(value: unknown): string {
  if (typeof value !== "string") throw new ReadError("invalid");
  const text = plain(value);
  if (!text || Array.from(text).length > 200 || /[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  return text;
}
export function searchPath(query: string, limit = 20): string {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 30) throw new ReadError("invalid");
  return `/search.json?${new URLSearchParams({ q: queryText(query), limit: String(limit),
    fields: "key,title,author_name,first_publish_year,edition_count,ebook_access,public_scan_b,ia" })}`;
}
export function workPath(key: string): string { return `/works/${workKey(key)}.json`; }
export function workURL(key: string): string { return `https://openlibrary.org/works/${workKey(key)}`; }
function access(value: unknown): Access {
  return value === "public" || value === "borrowable" || value === "printdisabled" || value === "no_ebook" ? value : "no_ebook";
}
function identifier(value: unknown): string {
  const id = string(value);
  // Metadata is an identifier, never a URL or a script. Do not permit a service
  // response to choose a scheme, path, credentials or an external host.
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(id)) throw new ReadError("bad_answer");
  return id;
}
export function parseBook(raw: unknown): Book {
  const value = object(raw), key = workKey(string(value.key).replace(/^\/works\//, ""));
  const title = clip(plain(string(value.title)), 500);
  if (!title) throw new ReadError("bad_answer");
  const authors = array(value.author_name ?? []).slice(0, 8).map(name => clip(plain(string(name)), 200)).filter(Boolean);
  const year = value.first_publish_year == null ? undefined : integer(value.first_publish_year, 2200);
  const editions = value.edition_count == null ? undefined : integer(value.edition_count);
  const availability = access(value.ebook_access);
  let readingURL: string | undefined;
  // A public catalog flag is not enough to identify a freely readable scan.
  // Only the explicit public Gutenberg collection identifies a public reading
  // target; restricted IA identifiers are never presented as open books.
  if (availability === "public" && value.public_scan_b === true) {
    const ids = array(value.ia ?? []).slice(0, 100).map(value => { try { return identifier(value); } catch { return ""; } });
    const publicID = ids.find(id => /gutenberg|projectgutenberg/i.test(id));
    if (publicID) readingURL = `https://openlibrary.org/books/ia:${encodeURIComponent(publicID)}`;
  }
  return { key, title, authors, ...(year === undefined ? {} : { year }), ...(editions === undefined ? {} : { editions }),
    access: availability, url: workURL(key), ...(readingURL ? { readingURL } : {}) };
}
export function parseSearch(raw: unknown, at: number): SearchResult {
  const value = object(raw), total = integer(value.numFound ?? value.num_found, 1_000_000_000);
  const seen = new Set<string>();
  const books = array(value.docs).slice(0, 30).map(parseBook).filter(book => {
    if (seen.has(book.key)) return false;
    seen.add(book.key); return true;
  });
  return { books, total, at };
}
function description(value: unknown): string {
  if (value == null) return "";
  if (typeof value === "string") return clip(plain(value), 8000);
  const item = object(value);
  if (item.type != null && item.type !== "/type/text") throw new ReadError("bad_answer");
  return clip(plain(string(item.value)), 8000);
}
export function parseWork(raw: unknown, base: Book, at: number): Work {
  const value = object(raw), key = workKey(string(value.key).replace(/^\/works\//, ""));
  const title = clip(plain(string(value.title)), 500);
  if (key !== base.key || !title || title !== base.title) throw new ReadError("bad_answer");
  const subjects = array(value.subjects ?? []).slice(0, 12).map(item => clip(plain(string(item)), 100)).filter(Boolean);
  return { ...base, title, description: description(value.description), subjects, at };
}
