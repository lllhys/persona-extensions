import { ExtError, noContribution, unknownMethod, withExactParams, type ExactJSON, type Handler } from "@persona/extension";
import { clip, gemName, queryText, ReadError, type GemDetail, type GemSummary } from "./model.ts";
import { dateText, errorText, fmt, number, textsFor } from "./i18n.ts";
import { budget, type RubyGems } from "./service.ts";
export function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [
    { name: "rubygems_search", description: t.toolSearch, input_schema: { type: "object", required: ["query"], properties: { query: { type: "string", description: t.query }, limit: { type: "integer", minimum: 1, maximum: 20, description: t.limit } } } },
    { name: "rubygems_gem", description: t.toolGem, input_schema: { type: "object", required: ["name"], properties: { name: { type: "string", description: t.name } } } },
  ];
}
export const tools = toolsFor();
function summaryLine(gem: GemSummary, index: number, locale: string): string {
  const t = textsFor(locale);
  return `${index + 1}. ${gem.name}@${gem.version}\n   ${gem.description}\n   ${fmt(t.license, { license: gem.license })} · ${fmt(t.total, { count: number(gem.totalDownloads) })}\n   ${gem.url}${gem.repository ? `\n   ${gem.repository}` : ""}`;
}
function detailText(gem: GemDetail, locale: string): string {
  const t = textsFor(locale);
  return `${gem.name}@${gem.version}\n${gem.description}\n${fmt(t.license, { license: gem.license })}${gem.published ? ` · ${dateText(gem.published)}` : ""}\n${fmt(t.total, { count: number(gem.totalDownloads) })}${gem.versionDownloads === undefined ? "" : `\n${fmt(t.versionDownloads, { count: number(gem.versionDownloads) })}`}\n${fmt(t.dependencies, { count: number(gem.dependencies.length) })}: ${gem.dependencies.map(item => `${item.name} (${item.requirements})`).join(", ") || t.noDependencies}${gem.yanked ? `\n${t.yanked}` : ""}\n${t.install}: ${gem.install}\n${gem.url}${gem.documentation ? `\n${gem.documentation}` : ""}${gem.repository ? `\n${gem.repository}` : ""}${gem.homepage ? `\n${gem.homepage}` : ""}`;
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
export async function callTool(service: RubyGems, name: string, raw: unknown = {}, signal?: AbortSignal, exact?: ExactJSON): Promise<string> {
  const args = argsFrom(raw, exact), t = textsFor(service.locale), request = budget(signal, service.limits.tool);
  try {
    if (name === "rubygems_search") {
      const query = queryText(args.query), limit = args.limit == null ? 10 : Number(args.limit);
      if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
      const result = await service.search(query, limit, false, request.signal);
      return clip((result.gems.length ? result.gems.map((gem, index) => summaryLine(gem, index, service.locale)).join("\n") : t.empty) + `\n${t.attribution}${result.stale ? `\n${errorText(result.stale, service.locale)}` : ""}`, 29_999);
    }
    if (name === "rubygems_gem") {
      const result = await service.gem(gemName(args.name), false, request.signal);
      return clip(detailText(result.gem, service.locale) + `\n${t.attribution}${result.stale ? `\n${errorText(result.stale, service.locale)}` : ""}`, 29_999);
    }
    throw unknownMethod(`connector.tools.${name}`);
  } finally { request.dispose(); }
}
export function connector(getService: () => RubyGems): Handler { return withExactParams(async (method, params: unknown, call) => {
  const service = getService();
  if (params != null && (typeof params !== "object" || Array.isArray(params))) throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  const p = (params ?? {}) as Record<string, unknown>;
  if (p.contribution != null && typeof p.contribution !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  if (p.contribution && p.contribution !== "rubygems") throw noContribution(String(p.contribution));
  if (method === "status") return service.status();
  if (method === "tools.list") return { tools: toolsFor(service.locale) };
  if (method === "tools.call") {
    if (typeof p.name !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
    try { return { text: await callTool(service, p.name, p.args, undefined, call?.params?.field("args")) }; }
    catch (error) { if (error instanceof ReadError) throw new ExtError(errorText(error, service.locale), error.code === "invalid" ? "invalid" : undefined); throw error; }
  }
  throw unknownMethod("connector." + method);
}); }
