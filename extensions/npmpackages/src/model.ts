export const limits = Object.freeze({ request: 12_000, page: 15_000, tool: 15_000, response: 1 << 20, fresh: 15 * 60_000, cache: 40, cacheBytes: 2 << 20, gap: 250 });
export type LimitOptions = { [K in keyof typeof limits]?: number };
export type ReadCode = "invalid" | "bad_answer" | "not_found" | "rate_limited" | "slow" | "network" | "http";
export class ReadError extends Error {
  constructor(readonly code: ReadCode, readonly status?: number) { super(code); }
}
export const clip = (text: string, most: number) => text.length <= most ? text : text.slice(0, most).trimEnd() + "…";
export const oneLine = (text: string) => text.replace(/\s+/gu, " ").trim();
export function packageName(value: unknown): string {
  if (typeof value !== "string" || /[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  const name = value.trim();
  // npm has refused new uppercase names since 2014, but the registry still serves the older ones (JSONStream, Base64), so accept them.
  if (name.length > 214 || !/^(?:@[a-zA-Z0-9][a-zA-Z0-9._-]*\/)?[a-zA-Z0-9][a-zA-Z0-9._-]*$/.test(name) || /(?:^|[.\/])(?:node_modules|favicon\.ico)(?:$|[.\/])/.test(name)) throw new ReadError("invalid");
  return name;
}
export function queryText(value: unknown): string {
  if (typeof value !== "string" || /[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  const query = oneLine(value);
  if (!query || Array.from(query).length > 200) throw new ReadError("invalid");
  return query;
}
export function versionText(value: unknown): string {
  if (typeof value !== "string" || !/^[0-9A-Za-z][0-9A-Za-z.+-]{0,99}$/.test(value) || /[\s\/\\]/.test(value)) throw new ReadError("bad_answer");
  return value;
}
export function searchPath(query: string, limit = 12): string {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
  return `/-/v1/search?${new URLSearchParams({ text: queryText(query), size: String(limit) })}`;
}
export function packagePath(name: string): string { return `/${packageName(name).replace("/", "%2F")}/latest`; }
export function downloadsPath(name: string): string { return `/downloads/point/last-week/${encodeURIComponent(packageName(name))}`; }
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
function httpsURL(value: unknown, hosts?: Set<string>): string | undefined {
  if (value == null || value === "") return undefined;
  const raw = string(value).trim().replace(/^git\+/, "").replace(/\.git(?:$|(?=[#?]))/, "");
  let url: URL;
  try { url = new URL(raw); } catch { throw new ReadError("bad_answer"); }
  if (url.protocol !== "https:" || url.username || url.password || url.port || url.pathname.includes("..") || (hosts && !hosts.has(url.hostname))) throw new ReadError("bad_answer");
  return url.href;
}
export interface PackageSummary {
  name: string; version: string; description: string; license: string; url: string;
  repository?: string; homepage?: string; published?: string; weeklyDownloads?: number; monthlyDownloads?: number;
}
export interface PackageDetail extends PackageSummary {
  install: string; engines: string; dependencies: number; unpackedSize?: number; types: boolean;
}
export interface SearchResult { packages: PackageSummary[]; total: number; at: number; stale?: ReadError }
export interface DetailResult { package: PackageDetail; at: number; downloadsStart: string; downloadsEnd: string; stale?: ReadError }
function license(value: unknown): string {
  if (value && typeof value === "object" && !Array.isArray(value)) value = (value as Record<string, unknown>).type;
  const license = clip(oneLine(string(value, "Unspecified")), 120);
  if (!license || /[\u0000-\u001f\u007f]/u.test(license)) throw new ReadError("bad_answer");
  return license;
}
// optionalLink is a repository or homepage link as https, or nothing. Package metadata often writes the repository as
// git+ssh://git@host/path, ssh://git@host/path, git://host/path or git@host:path, shown as https://host/path as on
// npmjs.com; a link that still cannot be read as https is left out rather than failing the search or the lookup.
function optionalLink(value: unknown): string | undefined {
  if (typeof value !== "string" || !value.trim()) return undefined;
  const text = value.trim().replace(/^(?:git\+)?(?:ssh:\/\/git@|git:\/\/)([^/:@]+)[:/]/, "https://$1/").replace(/^git@([^/:@]+):/, "https://$1/");
  try { return httpsURL(text); } catch { return undefined; }
}
function repository(value: unknown): string | undefined {
  if (value && typeof value === "object" && !Array.isArray(value)) value = (value as Record<string, unknown>).url;
  return optionalLink(value);
}
function summary(value: unknown): PackageSummary {
  const item = object(value), name = packageName(item.name), version = versionText(item.version);
  const links = item.links == null ? {} : object(item.links);
  const url = httpsURL(links.npm ?? `https://www.npmjs.com/package/${name}`, new Set(["www.npmjs.com"]));
  if (!url) throw new ReadError("bad_answer");
  const published = item.date == null ? undefined : item.date;
  if (published !== undefined && (typeof published !== "string" || !Number.isFinite(Date.parse(published)))) throw new ReadError("bad_answer");
  return { name, version, description: clip(oneLine(string(item.description, "")), 500), license: license(item.license), url, ...(repository(item.repository ?? links.repository) ? { repository: repository(item.repository ?? links.repository) } : {}), ...(optionalLink(item.homepage ?? links.homepage) ? { homepage: optionalLink(item.homepage ?? links.homepage) } : {}), ...(published ? { published: published as string } : {}) };
}
export function parseSearch(raw: unknown, at: number): SearchResult {
  const root = object(raw), objects = array(root.objects), total = integer(root.total, 1_000_000_000_000), seen = new Set<string>();
  const packages = objects.slice(0, 20).map(value => {
    const item = object(value), pkg = summary(object(item.package));
    if (seen.has(pkg.name)) throw new ReadError("bad_answer"); seen.add(pkg.name);
    const downloads = item.downloads == null ? {} : object(item.downloads);
    return { ...pkg, ...(downloads.weekly == null ? {} : { weeklyDownloads: integer(downloads.weekly) }), ...(downloads.monthly == null ? {} : { monthlyDownloads: integer(downloads.monthly) }) };
  });
  return { packages, total, at };
}
export function parsePackage(raw: unknown, at: number): PackageDetail {
  const item = object(raw), summaryValue = summary({ ...item, links: { npm: `https://www.npmjs.com/package/${packageName(item.name)}`, repository: item.repository, homepage: item.homepage } });
  const engines = item.engines == null ? {} : object(item.engines), parts: string[] = [];
  for (const key of ["node", "npm", "pnpm", "yarn", "bun"].filter(key => engines[key] != null).slice(0, 5)) parts.push(`${key} ${clip(oneLine(string(engines[key])), 100)}`);
  const dependencies = item.dependencies == null ? {} : object(item.dependencies), dist = item.dist == null ? {} : object(item.dist);
  const unpackedSize = dist.unpackedSize == null ? undefined : integer(dist.unpackedSize, 2_000_000_000_000);
  return { ...summaryValue, install: `npm install ${summaryValue.name}@${summaryValue.version}`, engines: parts.join(" · "), dependencies: Object.keys(dependencies).length, ...(unpackedSize === undefined ? {} : { unpackedSize }), types: typeof item.types === "string" || typeof item.typings === "string" };
}
export function parseDownloads(raw: unknown, name: string): { count: number; start: string; end: string } {
  const root = object(raw);
  if (root.package !== packageName(name)) throw new ReadError("bad_answer");
  const start = string(root.start), end = string(root.end);
  if (!/^\d{4}-\d{2}-\d{2}$/.test(start) || !/^\d{4}-\d{2}-\d{2}$/.test(end) || !Number.isFinite(Date.parse(start)) || !Number.isFinite(Date.parse(end))) throw new ReadError("bad_answer");
  return { count: integer(root.downloads), start, end };
}
