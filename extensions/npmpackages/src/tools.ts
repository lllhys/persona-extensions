import { ExtError, noContribution, unknownMethod, withExactParams, type ExactJSON, type Handler } from "@persona/extension";
import { clip, packageName, queryText, ReadError, type PackageDetail, type PackageSummary } from "./model.ts";
import { bytes, dateText, errorText, fmt, number, textsFor } from "./i18n.ts";
import { budget, type NpmPackages } from "./service.ts";
export function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [
    { name: "npmpackages_search", description: t.toolSearch, input_schema: { type: "object", required: ["query"], properties: { query: { type: "string", description: t.query }, limit: { type: "integer", minimum: 1, maximum: 20, description: t.limit } } } },
    { name: "npmpackages_package", description: t.toolPackage, input_schema: { type: "object", required: ["name"], properties: { name: { type: "string", description: t.name } } } },
  ];
}
export const tools = toolsFor();
function summaryLine(pkg: PackageSummary, index: number, locale: string): string {
  const t = textsFor(locale);
  return `${index + 1}. ${pkg.name}@${pkg.version}\n   ${pkg.description}\n   ${fmt(t.license, { license: pkg.license })}${pkg.weeklyDownloads === undefined ? "" : ` · ${fmt(t.weekly, { count: number(pkg.weeklyDownloads) })}`}\n   ${pkg.url}${pkg.repository ? `\n   ${pkg.repository}` : ""}`;
}
function detailText(pkg: PackageDetail, locale: string): string {
  const t = textsFor(locale);
  return `${pkg.name}@${pkg.version}\n${pkg.description}\n${fmt(t.license, { license: pkg.license })}${pkg.published ? ` · ${dateText(pkg.published)}` : ""}\n${pkg.weeklyDownloads === undefined ? t.noDownloads : fmt(t.weekly, { count: number(pkg.weeklyDownloads) })}\n${fmt(t.dependencies, { count: pkg.dependencies })} · ${t.engines}: ${pkg.engines || t.unknown}${pkg.unpackedSize === undefined ? "" : ` · ${fmt(t.size, { count: bytes(pkg.unpackedSize) })}`}\n${pkg.types ? t.types : t.typesNone}\n${t.install}: ${pkg.install}\n${pkg.url}${pkg.repository ? `\n${pkg.repository}` : ""}${pkg.homepage ? `\n${pkg.homepage}` : ""}`;
}
function argsFrom(raw: unknown, exact?: ExactJSON): Record<string, unknown> {
  if (exact) {
    const args: Record<string, unknown> = {};
    for (const field of ["query", "name"]) for (const token of exact.fieldValues(field)) {
      const value: unknown = JSON.parse(token.raw); if (typeof value !== "string") throw new ReadError("invalid"); args[field] = value;
    }
    const limit = exact.int64Fields(["limit"]).limit;
    if (limit !== 0n) args.limit = Number(limit);
    return args;
  }
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = raw as Record<string, unknown>;
  for (const field of ["query", "name"]) if (value[field] != null && typeof value[field] !== "string") throw new ReadError("invalid");
  if (value.limit != null && (typeof value.limit !== "number" || !Number.isSafeInteger(value.limit))) throw new ReadError("invalid");
  return value;
}
export async function callTool(service: NpmPackages, name: string, raw: unknown = {}, signal?: AbortSignal, exact?: ExactJSON): Promise<string> {
  const args = argsFrom(raw, exact), t = textsFor(service.locale), request = budget(signal, service.limits.tool);
  try {
    if (name === "npmpackages_search") {
      const query = queryText(args.query), limit = args.limit == null ? 10 : Number(args.limit);
      if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
      const result = await service.search(query, limit, false, request.signal);
      return clip((result.packages.length ? result.packages.map((pkg, index) => summaryLine(pkg, index, service.locale)).join("\n") : t.empty) + `\n${t.attribution}${result.stale ? `\n${errorText(result.stale, service.locale)}` : ""}`, 29_999);
    }
    if (name === "npmpackages_package") {
      const result = await service.package(packageName(args.name), false, request.signal);
      return clip(detailText(result.package, service.locale) + `\n${t.attribution}${result.stale ? `\n${errorText(result.stale, service.locale)}` : ""}`, 29_999);
    }
    throw unknownMethod(`connector.tools.${name}`);
  } finally { request.dispose(); }
}
export function connector(getService: () => NpmPackages): Handler { return withExactParams(async (method, params: unknown, call) => {
  const service = getService();
  if (params != null && (typeof params !== "object" || Array.isArray(params))) throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  const p = (params ?? {}) as Record<string, unknown>;
  if (p.contribution != null && typeof p.contribution !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  if (p.contribution && p.contribution !== "npmpackages") throw noContribution(String(p.contribution));
  if (method === "status") return service.status();
  if (method === "tools.list") return { tools: toolsFor(service.locale) };
  if (method === "tools.call") {
    if (typeof p.name !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
    try { return { text: await callTool(service, p.name, p.args, undefined, call?.params?.field("args")) }; }
    catch (error) { if (error instanceof ReadError) throw new ExtError(errorText(error, service.locale), error.code === "invalid" ? "invalid" : undefined); throw error; }
  }
  throw unknownMethod("connector." + method);
}); }
