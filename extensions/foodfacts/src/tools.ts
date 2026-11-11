import { ExtError, noContribution, unknownMethod, withExactParams, type ExactJSON, type Handler } from "@persona/extension";
import { barcode, clip, queryText, ReadError, type Product } from "./model.ts";
import { errorText, fmt, textsFor } from "./i18n.ts";
import { budget, type FoodFacts } from "./service.ts";
export function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [
    { name: "foodfacts_search", description: t.toolSearch, input_schema: { type: "object", required: ["query"], properties: { query: { type: "string", description: t.query }, limit: { type: "integer", minimum: 1, maximum: 20, description: t.limit } } } },
    { name: "foodfacts_product", description: t.toolProduct, input_schema: { type: "object", required: ["code"], properties: { code: { type: "string", description: t.code } } } },
  ];
}
export const tools = toolsFor();
function line(product: Product, index: number, locale: string): string {
  const t = textsFor(locale);
  return `${index + 1}. ${clip(product.name, 300)}\n   ${[product.brands, product.quantity, product.nutriscore ? fmt(t.nutriscore, { grade: product.nutriscore.toUpperCase() }) : "", product.nova ? fmt(t.nova, { group: product.nova }) : ""].filter(Boolean).join(" · ")}\n   ${t.ingredients}: ${product.ingredients || t.unknown}\n   ${t.allergens}: ${product.allergens || t.unknown}\n   ${product.url}`;
}
function argsFrom(raw: unknown, exact?: ExactJSON): Record<string, unknown> {
  if (exact) {
    const args: Record<string, unknown> = {};
    for (const field of ["query", "code"]) for (const token of exact.fieldValues(field)) {
      const value: unknown = JSON.parse(token.raw); if (typeof value !== "string") throw new ReadError("invalid"); args[field] = value;
    }
    const limit = exact.int64Fields(["limit"]).limit;
    if (limit !== 0n) args.limit = limit;
    return args;
  }
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = raw as Record<string, unknown>;
  if (value.query != null && typeof value.query !== "string") throw new ReadError("invalid");
  if (value.code != null && typeof value.code !== "string") throw new ReadError("invalid");
  if (value.limit != null && (typeof value.limit !== "number" || !Number.isSafeInteger(value.limit))) throw new ReadError("invalid");
  return value;
}
export async function callTool(service: FoodFacts, name: string, raw: unknown = {}, signal?: AbortSignal, exact?: ExactJSON): Promise<string> {
  const args = argsFrom(raw, exact), t = textsFor(service.locale), request = budget(signal, service.limits.tool);
  try {
    if (name === "foodfacts_search") {
      const query = queryText(args.query), limit = args.limit == null ? 10 : Number(args.limit);
      if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
      const result = await service.search(query, limit, false, request.signal);
      return clip((result.products.length ? result.products.map((product, index) => line(product, index, service.locale)).join("\n").trim() : fmt(t.results, { query }) + "\n" + t.empty) + `\n${t.attribution}${result.stale ? `\n${errorText(result.stale, service.locale)}` : ""}`, 29_999);
    }
    if (name === "foodfacts_product") {
      const product = await service.product(barcode(args.code), false, request.signal);
      return clip(line(product, 0, service.locale) + `\n${t.attribution}`, 29_999);
    }
    throw unknownMethod(`connector.tools.${name}`);
  } finally { request.dispose(); }
}
export function connector(getService: () => FoodFacts): Handler { return withExactParams(async (method, params: unknown, call) => {
  const service = getService();
  if (params != null && (typeof params !== "object" || Array.isArray(params))) throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  const p = (params ?? {}) as Record<string, unknown>;
  if (p.contribution != null && typeof p.contribution !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  if (p.contribution && p.contribution !== "foodfacts") throw noContribution(String(p.contribution));
  if (method === "status") return service.status();
  if (method === "tools.list") return { tools: toolsFor(service.locale) };
  if (method === "tools.call") {
    if (typeof p.name !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
    try { return { text: await callTool(service, p.name, p.args, undefined, call?.params?.field("args")) }; }
    catch (error) { if (error instanceof ReadError) throw new ExtError(errorText(error, service.locale), error.code === "invalid" ? "invalid" : undefined); throw error; }
  }
  throw unknownMethod("connector." + method);
}); }
