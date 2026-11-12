export const limits = Object.freeze({ request: 12_000, page: 15_000, tool: 15_000, response: 1 << 20, fresh: 15 * 60_000, cache: 40, cacheBytes: 2 << 20, gap: 250 });
export type LimitOptions = { [K in keyof typeof limits]?: number };
export type ReadCode = "invalid" | "bad_answer" | "not_found" | "rate_limited" | "slow" | "network" | "http";
export class ReadError extends Error {
  constructor(readonly code: ReadCode, readonly status?: number) { super(code); }
}
export const clip = (text: string, most: number) => text.length <= most ? text : text.slice(0, most).trimEnd() + "…";
export const oneLine = (text: string) => text.replace(/\s+/gu, " ").trim();
export function packageID(value: unknown): string {
  if (typeof value !== "string" || !/^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/.test(value) || value.includes("..")) throw new ReadError("invalid");
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
export function searchPath(query: string, limit = 12): string {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
  return `/query?${new URLSearchParams({ q: queryText(query), take: String(limit) })}`;
}
export function registrationPath(id: string, version: string): string {
  return `/v3/registration5-semver1/${packageID(id).toLowerCase()}/${versionText(version).toLowerCase()}.json`;
}
export function nuspecPath(id: string, version: string): string {
  return `/v3-flatcontainer/${packageID(id).toLowerCase()}/${versionText(version).toLowerCase()}/${packageID(id).toLowerCase()}.nuspec`;
}
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
function integer(value: unknown, maximum = 20_000_000_000_000): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0 || value > maximum) throw new ReadError("bad_answer");
  return value;
}
function boolean(value: unknown): boolean { if (typeof value !== "boolean") throw new ReadError("bad_answer"); return value; }
function optionalHTTPSURL(value: unknown): string | undefined {
  if (value == null || value === "") return undefined;
  let url: URL;
  try { url = new URL(string(value)); } catch { throw new ReadError("bad_answer"); }
  if (url.protocol !== "https:") return undefined;
  if (url.username || url.password || url.port) throw new ReadError("bad_answer");
  return url.href;
}
function httpsURL(value: unknown): string | undefined {
  if (value == null || value === "") return undefined;
  let url: URL;
  try { url = new URL(string(value)); } catch { throw new ReadError("bad_answer"); }
  if (url.protocol !== "https:" || url.username || url.password || url.port) throw new ReadError("bad_answer");
  return url.href;
}
function apiURL(value: unknown, host: string, path: RegExp): string {
  const url = httpsURL(value);
  if (!url) throw new ReadError("bad_answer");
  const parsed = new URL(url);
  if (parsed.hostname !== host || !path.test(parsed.pathname)) throw new ReadError("bad_answer");
  return url;
}
function date(value: unknown): string | undefined {
  if (value == null) return undefined;
  const date = string(value);
  if (!Number.isFinite(Date.parse(date))) throw new ReadError("bad_answer");
  return date;
}
function license(value: unknown): string {
  const license = clip(oneLine(string(value, "See license link")), 120);
  if (!license || /[\u0000-\u001f\u007f]/u.test(license)) throw new ReadError("bad_answer");
  return license;
}
export interface PackageSummary {
  id: string; version: string; description: string; license: string; url: string;
  projectURL?: string; licenseURL?: string; totalDownloads?: number; published?: string;
}
export interface PackageDetail extends PackageSummary {
  install: string; listed: boolean; frameworks: string[]; dependencies: number;
}
export interface SearchResult { packages: PackageSummary[]; total: number; at: number; stale?: ReadError }
export interface DetailResult { package: PackageDetail; at: number; stale?: ReadError }
function summary(value: unknown): PackageSummary {
  const item = object(value), id = packageID(item.id), version = versionText(item.version);
  const licenseValue = item.licenseExpression ?? item.license ?? (item.licenseUrl ? "See license link" : undefined);
  return { id, version, description: clip(oneLine(string(item.description, "")), 500), license: license(licenseValue), url: `https://www.nuget.org/packages/${id}/${version}`, ...(optionalHTTPSURL(item.projectUrl) ? { projectURL: optionalHTTPSURL(item.projectUrl) } : {}), ...(optionalHTTPSURL(item.licenseUrl) ? { licenseURL: optionalHTTPSURL(item.licenseUrl) } : {}), ...(item.totalDownloads == null ? {} : { totalDownloads: integer(item.totalDownloads) }), ...(date(item.published) ? { published: date(item.published) } : {}) };
}
export function parseSearch(raw: unknown, at: number): SearchResult {
  const root = object(raw), total = integer(root.totalHits, 1_000_000_000_000), seen = new Set<string>();
  const packages = array(root.data).slice(0, 20).map(value => {
    const pkg = summary(value);
    if (seen.has(pkg.id.toLowerCase())) throw new ReadError("bad_answer"); seen.add(pkg.id.toLowerCase());
    return pkg;
  });
  return { packages, total, at };
}
export function parseLeaf(raw: unknown, id: string, version: string): { listed: boolean } {
  const root = object(raw);
  const listed = boolean(root.listed ?? true);
  const registration = httpsURL(root.registration);
  if (registration && new URL(registration).pathname !== `/v3/registration5-semver1/${packageID(id).toLowerCase()}/index.json`) throw new ReadError("bad_answer");
  return { listed };
}
export function parseNuspec(raw: string, id: string, version: string, listed: boolean): PackageDetail {
  if (raw.length > 200_000 || /<!DOCTYPE|<!ENTITY/i.test(raw)) throw new ReadError("bad_answer");
  const metadata = raw.match(/<metadata\b[^>]*>([\s\S]*?)<\/metadata>/i)?.[1];
  if (!metadata) throw new ReadError("bad_answer");
  const field = (name: string): string | undefined => {
    const match = metadata.match(new RegExp(`<${name}\\b[^>]*>([\\s\\S]*?)<\\/${name}>`, "i"));
    return match ? match[1].trim() : undefined;
  };
  if (field("id")?.toLowerCase() !== id.toLowerCase() || field("version")?.toLowerCase() !== version.toLowerCase()) throw new ReadError("bad_answer");
  const result = summary({ id, version, description: field("description"), projectUrl: field("projectUrl"), licenseUrl: field("licenseUrl"), licenseExpression: field("licenseExpression") ?? field("license"), published: field("published") });
  const dependenciesBlock = field("dependencies") ?? "";
  const groups = Array.from(dependenciesBlock.matchAll(/<group\b[^>]*targetFramework="([^"]+)"[^>]*>/gi)).map(match => {
    const start = match.index ?? 0, opening = match[0];
    if (opening.endsWith("/>")) return [match[1], ""] as const;
    const end = dependenciesBlock.indexOf("</group>", start + opening.length);
    if (end < 0) throw new ReadError("bad_answer");
    return [match[1], dependenciesBlock.slice(start + opening.length, end)] as const;
  });
  const frameworks: string[] = [];
  let dependencies = 0;
  for (const [rawFramework, body] of groups.slice(0, 40)) {
    const framework = clip(oneLine(rawFramework), 100);
    if (frameworks.includes(framework)) throw new ReadError("bad_answer");
    frameworks.push(framework);
    dependencies += Array.from((body ?? "").matchAll(/<dependency\b[^>]*\bid="[^"]+"/gi)).length;
    if (dependencies > 1_000) throw new ReadError("bad_answer");
  }
  return { ...result, install: `dotnet add package ${id} --version ${version}`, listed, frameworks, dependencies };
}
export function parseCatalog(raw: unknown, id: string, version: string, listed: boolean): PackageDetail {
  const item = object(raw);
  if (packageID(item.id) !== packageID(id) || versionText(item.version) !== versionText(version)) throw new ReadError("bad_answer");
  const result = summary({ ...item, id, version });
  const groups = item.dependencyGroups == null ? [] : array(item.dependencyGroups), frameworks: string[] = [];
  let dependencies = 0;
  for (const rawGroup of groups.slice(0, 40)) {
    const group = object(rawGroup), framework = clip(oneLine(string(group.targetFramework, "Unspecified")), 100);
    if (frameworks.includes(framework)) throw new ReadError("bad_answer");
    frameworks.push(framework);
    dependencies += array(group.dependencies ?? []).length;
    if (dependencies > 1_000) throw new ReadError("bad_answer");
  }
  return { ...result, install: `dotnet add package ${id} --version ${version}`, listed, frameworks, dependencies };
}
