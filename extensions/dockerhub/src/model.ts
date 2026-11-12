export const limits = Object.freeze({ request: 12_000, page: 15_000, tool: 15_000, response: 1 << 20, fresh: 15 * 60_000, cache: 40, cacheBytes: 2 << 20, gap: 250 });
export type LimitOptions = { [K in keyof typeof limits]?: number };
export type ReadCode = "invalid" | "bad_answer" | "not_found" | "rate_limited" | "slow" | "network" | "http";
export class ReadError extends Error {
  constructor(readonly code: ReadCode, readonly status?: number) { super(code); }
}
export const clip = (text: string, most: number) => text.length <= most ? text : text.slice(0, most).trimEnd() + "…";
export const oneLine = (text: string) => text.replace(/\s+/gu, " ").trim();
export function repositoryName(value: unknown): string {
  if (typeof value !== "string" || !/^(?:library|[a-z0-9][a-z0-9_-]{0,63})\/[a-z0-9][a-z0-9_.-]{0,99}$/.test(value)) throw new ReadError("invalid");
  return value;
}
function searchName(value: unknown): string {
  if (typeof value !== "string" || !/^[a-z0-9][a-z0-9_.-]{0,99}$/.test(value)) throw new ReadError("bad_answer");
  return `library/${value}`;
}
export function queryText(value: unknown): string {
  if (typeof value !== "string" || /[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  const query = oneLine(value);
  if (!query || Array.from(query).length > 100) throw new ReadError("invalid");
  return query;
}
export function searchPath(query: string, limit = 20): string {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
  return `/v2/search/repositories/?${new URLSearchParams({ query: queryText(query), page_size: String(limit) })}`;
}
export function repositoryPath(name: string): string { return `/v2/repositories/${repositoryName(name)}/`; }
export function tagsPath(name: string): string { return `/v2/repositories/${repositoryName(name)}/tags?page_size=10`; }
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
function integer(value: unknown, maximum = 100_000_000_000_000): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0 || value > maximum) throw new ReadError("bad_answer");
  return value;
}
function boolean(value: unknown): boolean { if (typeof value !== "boolean") throw new ReadError("bad_answer"); return value; }
function date(value: unknown): string {
  const date = string(value);
  if (!Number.isFinite(Date.parse(date))) throw new ReadError("bad_answer");
  return date;
}
export interface ImageSummary {
  name: string; description: string; pulls: number; stars: number; official: boolean; url: string;
}
export interface ImageTag {
  name: string; digest: string; updated: string; platforms: string[];
}
export interface ImageDetail extends ImageSummary {
  namespace: string; updated: string; pull: string; tags: ImageTag[];
}
export interface SearchResult { images: ImageSummary[]; total: number; at: number; stale?: ReadError }
export interface DetailResult { image: ImageDetail; at: number; stale?: ReadError }
function summary(value: unknown): ImageSummary {
  const item = object(value);
  if (typeof item.repo_name !== "string") throw new ReadError("bad_answer");
  const name = repositoryName(item.repo_name.includes("/") ? item.repo_name : searchName(item.repo_name));
  return { name, description: clip(oneLine(string(item.short_description, "")), 500), pulls: integer(item.pull_count), stars: integer(item.star_count), official: boolean(item.is_official), url: `https://hub.docker.com/r/${name}` };
}
export function parseSearch(raw: unknown, at: number): SearchResult {
  const root = object(raw), seen = new Set<string>();
  const images = array(root.results).slice(0, 20).map(value => {
    const image = summary(value);
    if (seen.has(image.name)) throw new ReadError("bad_answer"); seen.add(image.name);
    return image;
  });
  return { images, total: integer(root.count, 1_000_000_000), at };
}
export function parseRepository(raw: unknown, name: string): ImageDetail {
  const item = object(raw);
  if (repositoryName(`${item.namespace}/${string(item.name)}`) !== repositoryName(name)) throw new ReadError("bad_answer");
  return { name: repositoryName(name), namespace: string(item.namespace), description: clip(oneLine(string(item.description, "")), 500), pulls: integer(item.pull_count), stars: integer(item.star_count), official: item.namespace === "library", url: `https://hub.docker.com/r/${repositoryName(name)}`, updated: date(item.last_updated), pull: `docker pull ${repositoryName(name)}`, tags: [] };
}
export function parseTags(raw: unknown, repository: string): ImageTag[] {
  const root = object(raw);
  return array(root.results).slice(0, 10).map(value => {
    const item = object(value);
    // Signatures, attestations and metadata are listed as tags too (content_type "unrecognized"): nothing to pull, so they
    // are left out, not a failure of the repository.
    if (item.content_type !== "image" || item.tag_status !== "active") return undefined;
    const name = string(item.name);
    if (!/^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$/.test(name)) throw new ReadError("bad_answer");
    const digest = string(item.digest);
    if (!/^sha256:[a-f0-9]{64}$/.test(digest)) throw new ReadError("bad_answer");
    const platforms = array(item.images).slice(0, 64).map(value => {
      const image = object(value), os = string(image.os), architecture = string(image.architecture);
      if (!/^[a-z0-9][a-z0-9_-]{0,31}$/.test(os) || !/^[a-z0-9][a-z0-9_-]{0,31}$/.test(architecture)) throw new ReadError("bad_answer");
      const variant = image.variant == null ? "" : string(image.variant);
      if (variant && !/^[a-z0-9][a-z0-9_-]{0,31}$/.test(variant)) throw new ReadError("bad_answer");
      const platform = `${os}/${architecture}${variant ? `/${variant}` : ""}`;
      if (os === "unknown" || architecture === "unknown") return undefined;
      integer(image.size, 1_000_000_000_000);
      if (!/^sha256:[a-f0-9]{64}$/.test(string(image.digest))) throw new ReadError("bad_answer");
      return platform;
    }).filter((value): value is string => value !== undefined);
    if (repositoryName(repository).length > 130) throw new ReadError("bad_answer");
    return { name, digest, updated: date(item.last_updated), platforms: [...new Set(platforms)] };
  }).filter((tag): tag is ImageTag => tag !== undefined);
}
