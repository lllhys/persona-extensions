import { ExtError, noContribution, unknownMethod, withExactParams, type ExactJSON, type Handler } from "@persona/extension";
import { clip, podcastID, ReadError, type Episode, type Show } from "./model.ts";
import { errorText, fmt, readTime, textsFor } from "./i18n.ts";
import { budget, Podcasts } from "./service.ts";
export function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [
    { name: "podcasts_search", description: t.toolSearch, input_schema: { type: "object", required: ["query"], properties: { query: { type: "string", description: t.query }, limit: { type: "integer", minimum: 1, maximum: 30, description: t.limit } } } },
    { name: "podcasts_episodes", description: t.toolEpisodes, input_schema: { type: "object", required: ["show"], properties: { show: { type: "integer", minimum: 1, maximum: 2_000_000_000_000, description: t.show }, limit: { type: "integer", minimum: 1, maximum: 30, description: t.limit } } } },
  ];
}
export const tools = toolsFor();
function showLine(show: Show, index: number): string {
  return `${index + 1}. ${clip(show.title, 300)}\n   ${[show.publisher, show.genre, show.episodes === undefined ? "" : `${show.episodes} episodes`].filter(Boolean).join(" · ")}\n   ${show.url}\n   Podcast ID: ${show.id}${show.feedURL ? `\n   Publisher RSS: ${show.feedURL}` : ""}`;
}
function episodeLine(episode: Episode, index: number): string {
  return `${index + 1}. ${clip(episode.title, 300)}\n   ${[episode.at ?? "Date not supplied", episode.minutes === undefined ? "" : `${episode.minutes} min`].filter(Boolean).join(" · ")}\n   ${episode.url}\n   Episode ID: ${episode.id}${episode.description ? `\n   ${clip(episode.description, 2000)}` : ""}`;
}
function argsFrom(raw: unknown, exact?: ExactJSON): Record<string, unknown> {
  if (exact) {
    const args: Record<string, unknown> = {};
    for (const token of exact.fieldValues("query")) {
      const value: unknown = JSON.parse(token.raw); if (typeof value !== "string") throw new ReadError("invalid"); args.query = value;
    }
    const parsed = exact.int64Fields(["limit", "show"]);
    if (parsed.limit !== 0n) {
      if (parsed.limit < 1n || parsed.limit > 30n) throw new ReadError("invalid");
      args.limit = Number(parsed.limit);
    }
    if (parsed.show !== 0n) {
      if (parsed.show < 1n || parsed.show > 2_000_000_000_000n) throw new ReadError("invalid");
      args.show = Number(parsed.show);
    }
    return args;
  }
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = raw as Record<string, unknown>;
  if (value.query != null && typeof value.query !== "string") throw new ReadError("invalid");
  for (const key of ["limit", "show"] as const) if (value[key] != null && (typeof value[key] !== "number" || !Number.isSafeInteger(value[key]))) throw new ReadError("invalid");
  return value;
}
function limit(value: unknown): number {
  const result = value == null ? 10 : Number(value);
  if (!Number.isSafeInteger(result) || result < 1 || result > 30) throw new ReadError("invalid");
  return result;
}
export async function callTool(service: Podcasts, name: string, raw: unknown = {}, signal?: AbortSignal, exact?: ExactJSON): Promise<string> {
  const args = argsFrom(raw, exact), t = textsFor(service.locale), request = budget(signal, service.limits.tool);
  try {
    if (name === "podcasts_search") {
      if (typeof args.query !== "string") throw new ReadError("invalid");
      const result = await service.search(args.query, limit(args.limit), false, request.signal);
      const stale = result.stale ? `\n${errorText(result.stale, service.locale)} · ${fmt(t.readAt, { at: readTime(result.at, "Local", service.locale) })}` : "";
      return clip((result.shows.length ? result.shows.map(showLine).join("\n").trim() : fmt(t.results, { query: args.query }) + "\n" + t.empty) + stale, 29_999);
    }
    if (name === "podcasts_episodes") {
      const show = podcastID(args.show), result = await service.episodes(show, limit(args.limit), false, request.signal);
      const prefix = `${result.show.title}\n${result.show.publisher}\n${result.show.url}\n\n`;
      return clip(prefix + (result.episodes.length ? result.episodes.map(episodeLine).join("\n") : t.noEpisodes) + (result.stale ? `\n${errorText(result.stale, service.locale)} · ${fmt(t.readAt, { at: readTime(result.at, "Local", service.locale) })}` : ""), 29_999);
    }
    throw unknownMethod(`connector.tools.${name}`);
  } finally { request.dispose(); }
}
export function connector(getService: () => Podcasts): Handler { return withExactParams(async (method, params: unknown, call) => {
  const service = getService();
  if (params != null && (typeof params !== "object" || Array.isArray(params))) throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  const p = (params ?? {}) as Record<string, unknown>;
  if (p.contribution != null && typeof p.contribution !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  if (p.contribution && p.contribution !== "podcasts") throw noContribution(String(p.contribution));
  if (method === "status") return service.status();
  if (method === "tools.list") return { tools: toolsFor(service.locale) };
  if (method === "tools.call") {
    if (typeof p.name !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
    try { return { text: await callTool(service, p.name, p.args, undefined, call?.params?.field("args")) }; }
    catch (error) { if (error instanceof ReadError) throw new ExtError(errorText(error, service.locale), error.code === "invalid" ? "invalid" : undefined); throw error; }
  }
  throw unknownMethod("connector." + method);
}); }
