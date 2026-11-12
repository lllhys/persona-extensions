export const limits = Object.freeze({ request: 12_000, page: 15_000, tool: 15_000, response: 1 << 20, fresh: 15 * 60_000, cache: 40, cacheBytes: 2 << 20, gap: 250 });
export type LimitOptions = { [K in keyof typeof limits]?: number };
export type ReadCode = "invalid" | "bad_answer" | "not_found" | "rate_limited" | "slow" | "network" | "http";
export class ReadError extends Error {
  constructor(readonly code: ReadCode, readonly status?: number) { super(code); }
}
export const clip = (text: string, most: number) => text.length <= most ? text : text.slice(0, most).trimEnd() + "…";
export const oneLine = (text: string) => text.replace(/\s+/gu, " ").trim();
export function gemName(value: unknown): string {
  if (typeof value !== "string" || !/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/.test(value)) throw new ReadError("invalid");
  return value;
}
export function queryText(value: unknown): string {
  if (typeof value !== "string" || /[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  const query = oneLine(value);
  if (!query || Array.from(query).length > 100) throw new ReadError("invalid");
  return query;
}
export function versionText(value: unknown): string {
  if (typeof value !== "string" || !/^[0-9A-Za-z][0-9A-Za-z.+-]{0,99}$/.test(value) || /[\s\/\\]/.test(value)) throw new ReadError("bad_answer");
  return value;
}
export function searchPath(query: string): string {
  return `/api/v1/search.json?${new URLSearchParams({ query: queryText(query) })}`;
}
export function gemPath(name: string): string { return `/api/v1/gems/${gemName(name)}.json`; }
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  return value as Record<string, unknown>;
}
function array(value: unknown): unknown[] { if (!Array.isArray(value)) throw new ReadError("bad_answer"); return value; }
function string(value: unknown, fallback?: string): string {
  if (value == null && fallback !== undefined) return fallback;
  if (typeof value !== "string") throw new ReadError("bad_answer");
  return value;
}
function integer(value: unknown, maximum = 2_000_000_000_000): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0 || value > maximum) throw new ReadError("bad_answer");
  return value;
}
function boolean(value: unknown): boolean { if (typeof value !== "boolean") throw new ReadError("bad_answer"); return value; }
export function httpsURL(value: unknown): string | undefined {
  if (value == null || value === "") return undefined;
  let url: URL;
  try { url = new URL(string(value)); } catch { return undefined; }
  // Optional third-party links are omitted, never upgraded to another origin.
  if (url.protocol !== "https:" || url.username || url.password || url.port) return undefined;
  return url.href;
}
function license(value: unknown): string {
  if (value == null || (Array.isArray(value) && value.length === 0)) return "Unspecified";
  const licenses = Array.isArray(value) ? value.slice(0, 10).map(item => string(item)) : [string(value)];
  const text = clip(oneLine(licenses.join(" OR ")), 120);
  if (!text || /[\u0000-\u001f\u007f]/u.test(text)) throw new ReadError("bad_answer");
  return text;
}
function date(value: unknown): string | undefined {
  if (value == null) return undefined;
  const date = string(value);
  if (!Number.isFinite(Date.parse(date))) throw new ReadError("bad_answer");
  return date;
}
export interface GemSummary {
  name: string; version: string; description: string; license: string; url: string;
  repository?: string; homepage?: string; documentation?: string;
  totalDownloads: number; versionDownloads?: number;
}
export interface GemDependency { name: string; requirements: string }
export interface GemDetail extends GemSummary {
  install: string; yanked: boolean; published?: string; dependencies: GemDependency[];
}
export interface SearchResult { gems: GemSummary[]; at: number; stale?: ReadError }
export interface DetailResult { gem: GemDetail; at: number; stale?: ReadError }
function summary(value: unknown): GemSummary {
  const item = object(value), name = gemName(item.name), version = versionText(item.version);
  const project = httpsURL(item.project_uri);
  if (project !== `https://rubygems.org/gems/${name}` && project !== `https://rubygems.org/gems/${encodeURIComponent(name)}`) throw new ReadError("bad_answer");
  return {
    name, version, description: clip(oneLine(string(item.info, "")), 500), license: license(item.licenses), url: project,
    ...(httpsURL(item.source_code_uri) ? { repository: httpsURL(item.source_code_uri) } : {}),
    ...(httpsURL(item.homepage_uri) ? { homepage: httpsURL(item.homepage_uri) } : {}),
    ...(httpsURL(item.documentation_uri) ? { documentation: httpsURL(item.documentation_uri) } : {}),
    totalDownloads: integer(item.downloads),
    ...(item.version_downloads == null ? {} : { versionDownloads: integer(item.version_downloads) }),
  };
}
export function parseSearch(raw: unknown, at: number): SearchResult {
  const seen = new Set<string>();
  const gems = array(raw).slice(0, 20).map(value => {
    const gem = summary(value);
    const normalized = gem.name.toLowerCase();
    if (seen.has(normalized)) throw new ReadError("bad_answer"); seen.add(normalized);
    return gem;
  });
  return { gems, at };
}
export function parseGem(raw: unknown, at: number): GemDetail {
  const root = object(raw), result = summary(root);
  const dependencies = object(root.dependencies), development = array(dependencies.development ?? []);
  for (const dependency of development) { object(dependency); }
  const runtime = array(dependencies.runtime ?? []).slice(0, 100).map(value => {
    const item = object(value);
    const name = gemName(item.name), requirements = clip(oneLine(string(item.requirements)), 100);
    if (!requirements) throw new ReadError("bad_answer");
    return { name, requirements };
  });
  return { ...result, install: `gem install ${result.name} -v ${result.version}`, yanked: boolean(root.yanked ?? false), ...(date(root.version_created_at) ? { published: date(root.version_created_at) } : {}), dependencies: runtime };
}
