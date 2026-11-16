import { ExtError, noContribution, unknownMethod, withExactParams, type Handler } from "@persona/extension";
import { clip, ReadError } from "./model.ts";
import { errorText, textsFor } from "./i18n.ts";
import { budget, Domains } from "./service.ts";
import type { HistoryStorage } from "./store.ts";
export function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [{ name: "domains_lookup", description: t.toolLookup, input_schema: { type: "object", required: ["domain"], properties: { domain: { type: "string", description: t.domain } } } }];
}
export const tools = toolsFor();
export async function callTool(service: Domains, name: string, raw: unknown = {}, signal?: AbortSignal): Promise<string> {
  if (name !== "domains_lookup") throw unknownMethod(`connector.tools.${name}`);
  if (!raw || typeof raw !== "object" || Array.isArray(raw) || typeof (raw as { domain?: unknown }).domain !== "string") throw new ReadError("invalid");
  const request = budget(signal, service.limits.tool), t = textsFor(service.locale);
  try {
    const result = await service.lookup((raw as { domain: string }).domain, false, request.signal);
    return clip([`${result.domain.unicode} (${result.name})`, result.url,
      `${t.registrar}: ${result.registrar ?? "—"}`, `${t.statuses}: ${result.statuses.join(", ") || t.noStatus}`,
      ...result.events.map(event => `${event.action}: ${event.at}`), `${t.source}: ${result.source}`, t.attribution].join("\n"), 29_999);
  } finally { request.dispose(); }
}
export function connector(getService: () => Domains, getHistory: () => HistoryStorage): Handler { return withExactParams(async (method, params: unknown, call) => {
  const service = getService();
  if (params != null && (typeof params !== "object" || Array.isArray(params))) throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  const p = (params ?? {}) as Record<string, unknown>;
  if (p.contribution != null && typeof p.contribution !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  if (p.contribution && p.contribution !== "domains") throw noContribution(String(p.contribution));
  if (method === "status") return service.status();
  if (method === "tools.list") return { tools: toolsFor(service.locale) };
  if (method === "tools.call") {
    if (typeof p.name !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
    try {
      const exact = call?.params?.field("args"), domain = exact ? (() => {
        const fields = [...exact.fieldValues("domain")];
        if (fields.length !== 1) throw new ReadError("invalid");
        return JSON.parse(fields[0].raw) as unknown;
      })() : (p.args as { domain?: unknown } | undefined)?.domain;
      const text = await callTool(service, p.name, { domain });
      if (p.name === "domains_lookup" && typeof domain === "string") {
        try { await getHistory().add(domain); } catch { /* A history failure cannot falsify the read result. */ }
      }
      return { text };
    } catch (error) { if (error instanceof ReadError) throw new ExtError(errorText(error, service.locale), error.code === "invalid" ? "invalid" : undefined); throw error; }
  }
  throw unknownMethod("connector." + method);
}); }
