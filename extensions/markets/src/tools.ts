import { noContribution, unknownMethod, ExtError, ExactJSON, withExactParams, type ExactCall } from "@persona/extension";
import { numericJSON } from "./integers.ts";
import { typedJSON } from "./typed-json.ts";
import type { Markets } from "./service.ts";
import { toolInteger } from "./integers.ts";
import { displayZone } from "./time.ts";
import { translator, localizeValue } from "./locale.ts";
import { bars, parseRef, key, clip, cleanQuery, change, price, compact, fmtFloat, fmtChange, stamp, sourceBars, zones, type Ref, type Quote } from "./model.ts";

export function tools(locale = "zh-Hans") {
  const t = translator(locale);
  return [
    { name: "markets_quote", description: t("toolsQuote"), input_schema: { type: "object", properties: { symbols: { type: "array", items: { type: "string" }, maxItems: 50 } } } },
    { name: "markets_search", description: t("toolsSearch"), input_schema: { type: "object", required: ["query"], properties: { query: { type: "string" }, limit: { type: "integer", minimum: 1, maximum: 30 } } } },
    { name: "markets_candles", description: t("toolsCandles"), input_schema: { type: "object", required: ["symbol"], properties: { symbol: { type: "string" }, bar: { type: "string", enum: bars }, limit: { type: "integer", minimum: 1, maximum: 100 } } } },
  ];
}
export function quoteLine(ref: Ref, q: Quote, locale: string) {
  const t = translator(locale), c = change(q);
  return `- ${key(ref)}${q.name ? ` (${q.name})` : ""}: ${t("latest")} ${price(q, q.last)} ${q.currency}`
    + (q.idle && q.at ? ` (${t("idle", { at: stamp(q.at, zones[ref.source]) })})` : "")
    + `, ${t(q.period)} ${c === undefined ? "—" : fmtChange(c)}, ${t("high")} ${price(q, q.high)}, ${t("low")} ${price(q, q.low)}`
    + (q.turnover > 0 ? `, ${t("turnover")} ${compact(q.turnover)} ${q.currency}` : "")
    + (q.facts.length ? "; " + q.facts.map(f => `${t(f.label)} ${localizeValue(f.value, locale)}`).join(", ") : "");
}
const bounded = (n: unknown, most: number, fallback: number) => (typeof n === "bigint" || typeof n === "number" && Number.isInteger(n)) && n > 0 && n <= most ? Number(n) : fallback;
const stringArg = (args: Record<string, unknown>, name: string) => args[name] === undefined || args[name] === null ? "" : typeof args[name] === "string" ? args[name] as string : (() => { throw new ExtError("Invalid market tool arguments", "invalid_params"); })();

export async function callTool(service: Markets, name: string, raw: unknown = {}, locale = "zh-Hans", signal?: AbortSignal) {
  if (raw === null) raw = {};
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) throw new ExtError("Invalid market tool arguments", "invalid_params");
  const args = raw as Record<string, unknown>, t = translator(locale), lines: string[] = [];
  // Go decodes the entire shared argument struct before selecting a tool.
  for (const field of ["symbol", "bar", "query"]) stringArg(args, field);
  const limit = toolInteger(args.limit);
  if (args.symbols !== undefined && args.symbols !== null && (!Array.isArray(args.symbols) || args.symbols.some(s => s !== null && typeof s !== "string"))) throw new ExtError(t("invalid"), "invalid_params");
  if (name === "markets_quote") {
    const typed = Array.isArray(args.symbols) ? args.symbols.map(s => s ?? "") as string[] : undefined;
    const list = typed?.length ? typed : service.watchlist();
    const refs = list.map(parseRef).filter((r): r is Ref => !!r).slice(0, 50);
    const got = await service.quotes(refs.map(key), 10_000, false, signal);
    lines.push(t("quotesAt", { at: new Date(service.clock()).toISOString() }));
    if (!list.length) lines.push(t("empty"));
    for (const ref of refs) { const q = got.quotes.get(key(ref)); lines.push(q ? quoteLine(ref, q, locale) : `- ${key(ref)}: ${t("missing")}`); }
    for (const code of list.filter(s => !parseRef(s)).slice(0, 50)) lines.push(t("badCode", { code: clip(code, 40) }));
  } else if (name === "markets_search") {
    const query = cleanQuery(stringArg(args, "query"));
    if (!query) throw new ExtError(t("invalid"), "invalid_params");
    const hits = await service.search(query, bounded(limit, 30, 10), 10_000, signal);
    lines.push(t("searchTitle", { q: query }));
    if (!hits.length) lines.push(t("none"));
    for (const hit of hits) lines.push(quoteLine(hit.ref, hit.quote, locale) + (hit.quote.kind ? `, ${t(hit.quote.kind)}` : ""));
  } else if (name === "markets_candles") {
    const ref = parseRef(stringArg(args, "symbol"));
    if (!ref) throw new ExtError(t("invalid"), "invalid_params");
    const bar = stringArg(args, "bar") || service.defaultBar(ref.source);
    if (!sourceBars[ref.source].includes(bar)) throw new ExtError(t("noCandles"), "invalid_params");
    const candles = await service.candles(ref, bar, bounded(limit, 100, 24), signal), zone = service.cfg.tz || "Local";
    lines.push(t("candlesAt", { ref: key(ref), bar, n: candles.length, zone: displayZone(zone, service.clock()) }));
    if (!candles.length) lines.push(t(bar === "T" ? "noMinutes" : "noCandles"));
    for (const c of candles) lines.push([stamp(c.at, zone), fmtFloat(c.open), fmtFloat(c.high), fmtFloat(c.low), fmtFloat(c.close), compact(c.vol)].join(" "));
  } else throw new ExtError(t("invalid"), "unknown_method");
  return { text: clip(lines.join("\n") + "\n", 128_000) };
}

/** Decode Go's complete outer struct, then its final args RawMessage. Scalar null
 * preserves earlier values; slices and RawMessage null replace them. Unknown
 * fields stay ignored, but an earlier consumed type error cannot be overwritten. */
function toolCall(params: unknown, call: ExactCall | undefined) {
  try {
    // Direct fixture callers have no wire metadata. Production always uses the
    // SDK-branded lazy handler, retaining the original JSON before Number rounding.
    const exact = call?.params ?? ExactJSON.parse(JSON.stringify(params ?? null));
    const envelope = typedJSON(numericJSON(exact.raw), { fields: { name: "string", args: "raw" } }) ?? {};
    const args = exact.field("args");
    const decoded = args ? typedJSON(numericJSON(args.raw), { fields: {
      symbols: { array: "string" }, symbol: "string", bar: "string", query: "string", limit: "raw",
    } }) ?? {} : {};
    // Validate every signed int64 occurrence before applying the Go business clamp.
    decoded.limit = args?.int64Fields(["limit"]).limit ?? 0n;
    return { name: envelope.name ?? "", args: decoded };
  } catch {
    throw new ExtError("Invalid market tool arguments", "invalid_params");
  }
}

export function connector(service: Markets | (() => Markets)) {
  const handler = async (method: string, params: any = {}, call?: ExactCall) => {
    params ??= {};
    if (params.contribution && params.contribution !== "markets") throw noContribution(params.contribution);
    const current = typeof service === "function" ? service() : service;
    switch (method) {
      case "status": return current.status(params.locale);
      case "tools.list": return { tools: tools(params.locale) };
      case "tools.call": {
        const decoded = toolCall(params, call);
        return callTool(current, decoded.name, decoded.args, params.locale);
      }
      case "inbox.collect": return current.inbox(params.locale);
      default: throw unknownMethod("connector." + method);
    }
  };
  return withExactParams(handler) as typeof handler;
}
