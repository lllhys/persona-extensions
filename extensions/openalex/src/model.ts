export const limits = Object.freeze({ request: 12_000, page: 15_000, tool: 15_000, response: 2 << 20, fresh: 60 * 60_000, cache: 40, cacheBytes: 2 << 20, gap: 200 });
export type LimitOptions = { [K in keyof typeof limits]?: number };
export type ReadCode = "invalid" | "bad_answer" | "not_found" | "rate_limited" | "slow" | "network" | "http";
export class ReadError extends Error {
  constructor(readonly code: ReadCode, readonly status?: number) { super(code); }
}
export const clip = (text: string, most: number) => text.length <= most ? text : text.slice(0, most).trimEnd() + "…";
export const oneLine = (text: string) => text.replace(/\s+/gu, " ").trim();
export function queryText(value: unknown): string {
  if (typeof value !== "string") throw new ReadError("invalid");
  if (/[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  const query = oneLine(value);
  if (!query || Array.from(query).length > 200) throw new ReadError("invalid");
  return query;
}
export function searchPath(query: string, limit = 20): string {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
  return `/works?${new URLSearchParams({ search: queryText(query), "per-page": String(limit), select: "id,display_name,publication_year,doi,primary_location,open_access,cited_by_count" })}`;
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
function boolean(value: unknown): boolean {
  if (typeof value !== "boolean") throw new ReadError("bad_answer");
  return value;
}
function workID(value: unknown): string {
  const id = string(value);
  if (!/^https:\/\/openalex\.org\/W[1-9][0-9]{0,15}$/.test(id)) throw new ReadError("bad_answer");
  return id;
}
function httpsURL(value: unknown): string | undefined {
  if (value == null) return undefined;
  const raw = string(value);
  let url: URL;
  try { url = new URL(raw); } catch { throw new ReadError("bad_answer"); }
  if (url.protocol !== "https:" || url.username || url.password) throw new ReadError("bad_answer");
  return url.href;
}
// openAccessURL is a work's open-access link when it is https. Publishers' links are often plain http (cell.com, for one):
// such a link is left out, as no insecure link is shown, rather than failing the whole search.
function openAccessURL(value: unknown): string | undefined {
  if (typeof value === "string" && /^http:\/\//i.test(value)) return undefined;
  return httpsURL(value);
}
export interface Work {
  id: string;
  title: string;
  year?: number;
  citations: number;
  openAccess: boolean;
  oaStatus: string;
  doi?: string;
  oaURL?: string;
  source: string;
  license: string;
}
export interface SearchResult { works: Work[]; total: number; at: number; stale?: ReadError }
export function parseWorks(raw: unknown, at: number): SearchResult {
  const root = object(raw), meta = object(root.meta), total = integer(meta.count, 1_000_000_000);
  const seen = new Set<string>();
  const works = array(root.results).slice(0, 20).map(rawWork => {
    const work = object(rawWork), id = workID(work.id);
    if (seen.has(id)) throw new ReadError("bad_answer");
    seen.add(id);
    const title = clip(oneLine(string(work.display_name)), 600);
    if (!title) throw new ReadError("bad_answer");
    const year = work.publication_year == null ? undefined : integer(work.publication_year, 2200);
    const access = object(work.open_access ?? {}), isOA = boolean(access.is_oa ?? false);
    const location = work.primary_location == null ? {} : object(work.primary_location);
    const source = location.source == null ? "" : clip(oneLine(string(object(location.source).display_name)), 300);
    const license = location.license == null ? "" : clip(oneLine(string(location.license)), 100);
    const oaURL = openAccessURL(access.oa_url);
    return { id, title, ...(year === undefined ? {} : { year }), citations: integer(work.cited_by_count ?? 0), openAccess: isOA,
      oaStatus: clip(oneLine(string(access.oa_status, isOA ? "open" : "closed")), 50), doi: httpsURL(work.doi), ...(oaURL ? { oaURL } : {}), source, license };
  });
  return { works, total, at };
}
