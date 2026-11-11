import { ExtError, noContribution, unknownMethod, withExactParams, type ExactJSON, type Handler } from "@persona/extension";
import { amount, clip, currency, currencyConversion, dimension, ReadError, unitConversion } from "./model.ts";
import { errorText, fmt, textsFor } from "./i18n.ts";
import { budget, type CurrencyService } from "./service.ts";
export function toolsFor(locale = "en") {
  const t = textsFor(locale);
  const value = { type: "string", description: t.amount };
  return [
    { name: "currency_convert", description: t.toolCurrency, input_schema: { type: "object", required: ["amount", "from", "to"], properties: { amount: value, from: { type: "string", description: t.from }, to: { type: "string", description: t.to } } } },
    { name: "unit_convert", description: t.toolUnit, input_schema: { type: "object", required: ["amount", "from", "to"], properties: { amount: value, from: { type: "string", description: t.from }, to: { type: "string", description: t.to }, dimension: { type: "string", enum: ["length", "mass", "temperature", "volume", "area", "speed", "duration", "data"], description: t.dimension } } } },
  ];
}
export const tools = toolsFor();
function argsFrom(raw: unknown, exact?: ExactJSON): Record<string, unknown> {
  if (exact) {
    const args: Record<string, unknown> = {};
    for (const field of ["amount", "from", "to", "dimension"]) {
      for (const token of exact.fieldValues(field)) {
        const value: unknown = JSON.parse(token.raw); if (typeof value !== "string") throw new ReadError("invalid"); args[field] = value;
      }
    }
    return args;
  }
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = raw as Record<string, unknown>;
  for (const field of ["from", "to", "dimension"]) if (value[field] != null && typeof value[field] !== "string") throw new ReadError("invalid");
  if (value.amount != null && typeof value.amount !== "string" && typeof value.amount !== "number") throw new ReadError("invalid");
  return value;
}
export async function callTool(service: CurrencyService, name: string, raw: unknown = {}, signal?: AbortSignal, exact?: ExactJSON): Promise<string> {
  const args = argsFrom(raw, exact), t = textsFor(service.locale), request = budget(signal, service.limits.tool);
  try {
    if (name === "currency_convert") {
      const input = amount(args.amount), rate = await service.rate(args.from, args.to, false, request.signal), result = currencyConversion(input, rate);
      return clip(`${input} ${rate.base} = ${result.result} ${rate.quote}\n${fmt(t.rateDate, { date: rate.date })}\n${t.attribution}${rate.stale ? `\n${errorText(rate.stale, service.locale)}` : ""}`, 29_999);
    }
    if (name === "unit_convert") {
      const result = unitConversion(args.amount, args.from, args.to, args.dimension == null ? undefined : dimension(args.dimension));
      return `${result.amount} ${result.from} = ${result.result} ${result.to}\n${t.local}`;
    }
    throw unknownMethod(`connector.tools.${name}`);
  } finally { request.dispose(); }
}
export function connector(getService: () => CurrencyService): Handler { return withExactParams(async (method, params: unknown, call) => {
  const service = getService();
  if (params != null && (typeof params !== "object" || Array.isArray(params))) throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  const p = (params ?? {}) as Record<string, unknown>;
  if (p.contribution != null && typeof p.contribution !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  if (p.contribution && p.contribution !== "currency") throw noContribution(String(p.contribution));
  if (method === "status") return service.status();
  if (method === "tools.list") return { tools: toolsFor(service.locale) };
  if (method === "tools.call") {
    if (typeof p.name !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
    try { return { text: await callTool(service, p.name, p.args, undefined, call?.params?.field("args")) }; }
    catch (error) { if (error instanceof ReadError) throw new ExtError(errorText(error, service.locale), error.code === "invalid" || error.code === "unsupported" ? "invalid" : undefined); throw error; }
  }
  throw unknownMethod("connector." + method);
}); }
