export const limits = Object.freeze({ request: 12_000, page: 15_000, tool: 15_000, response: 1 << 20, fresh: 24 * 60 * 60_000, cache: 24, cacheBytes: 1 << 20, gap: 1200 });
export type LimitOptions = { [K in keyof typeof limits]?: number };
export type ReadCode = "invalid" | "bad_answer" | "not_found" | "rate_limited" | "slow" | "network" | "http";
export class ReadError extends Error {
  constructor(readonly code: ReadCode, readonly status?: number) { super(code); }
}
export const clip = (text: string, most: number) => text.length <= most ? text : text.slice(0, most).trimEnd() + "…";
export const oneLine = (text: string) => text.replace(/\s+/gu, " ").trim();
export function queryText(value: unknown): string {
  if (typeof value !== "string" || /[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  const query = oneLine(value);
  if (!query || Array.from(query).length > 120) throw new ReadError("invalid");
  return query;
}
export function barcode(value: unknown): string {
  if (typeof value !== "string" || !/^[0-9]{8,14}$/.test(value)) throw new ReadError("invalid");
  return value;
}
export function lookupPath(query: string): string {
  if (/^[0-9]{8,14}$/.test(query)) return productPath(query);
  return searchPath(query);
}
export function searchPath(query: string, limit = 12): string {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
  return `/cgi/search.pl?${new URLSearchParams({ search_terms: queryText(query), search_simple: "1", action: "process", json: "1", page_size: String(limit), fields })}`;
}
export function productPath(code: string): string {
  return `/api/v2/product/${barcode(code)}?${new URLSearchParams({ fields })}`;
}
const fields = "code,product_name,brands,quantity,nutriscore_grade,nova_group,allergens,ingredients_text,url";
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  return value as Record<string, unknown>;
}
function string(value: unknown, most: number): string {
  if (value == null) return "";
  if (typeof value !== "string") throw new ReadError("bad_answer");
  return clip(oneLine(value), most);
}
function grade(value: unknown): "a" | "b" | "c" | "d" | "e" | undefined {
  if (value == null || value === "" || value === "unknown" || value === "not-applicable") return undefined;
  if (typeof value !== "string" || !/^[a-e]$/.test(value)) throw new ReadError("bad_answer");
  return value as "a" | "b" | "c" | "d" | "e";
}
function nova(value: unknown): number | undefined {
  if (value == null) return undefined;
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 1 || value > 4) throw new ReadError("bad_answer");
  return value;
}
function productURL(value: unknown, code: string): string {
  const canonical = `https://world.openfoodfacts.org/product/${code}`;
  if (value == null || value === "") return canonical;
  if (typeof value !== "string") throw new ReadError("bad_answer");
  let url: URL;
  try { url = new URL(value); } catch { throw new ReadError("bad_answer"); }
  if (url.protocol !== "https:" || url.hostname !== "world.openfoodfacts.org" || url.username || url.password || url.port || !url.pathname.startsWith(`/product/${code}`)) throw new ReadError("bad_answer");
  return url.href;
}
export interface Product {
  code: string; name: string; brands: string; quantity: string; ingredients: string; allergens: string;
  nutriscore?: "a" | "b" | "c" | "d" | "e"; nova?: number; url: string;
}
export interface SearchResult { products: Product[]; total: number; at: number; stale?: ReadError }
export function parseProduct(raw: unknown, expectedCode: string, at: number): Product {
  const root = object(raw);
  if (root.status === 0 || root.status_verbose === "product not found") throw new ReadError("not_found");
  if (root.status !== 1) throw new ReadError("bad_answer");
  return product(root.product, expectedCode);
}
function product(raw: unknown, expectedCode?: string): Product {
  const item = object(raw), code = barcode(item.code);
  // Open Food Facts answers a 12-digit UPC-A as its EAN-13, with a leading zero: the same product.
  if (expectedCode && code.replace(/^0+/, "") !== expectedCode.replace(/^0+/, "")) throw new ReadError("bad_answer");
  const name = string(item.product_name, 300);
  if (!name) throw new ReadError("bad_answer");
  return { code, name, brands: string(item.brands, 300), quantity: string(item.quantity, 100),
    ingredients: string(item.ingredients_text, 2500), allergens: string(item.allergens, 1000),
    ...(grade(item.nutriscore_grade) ? { nutriscore: grade(item.nutriscore_grade) } : {}),
    ...(nova(item.nova_group) ? { nova: nova(item.nova_group) } : {}), url: productURL(item.url, code) };
}
export function parseSearch(raw: unknown, at: number): SearchResult {
  const root = object(raw);
  if (!Array.isArray(root.products) || typeof root.count !== "number" || !Number.isSafeInteger(root.count) || root.count < 0 || root.count > 10_000_000) throw new ReadError("bad_answer");
  const seen = new Set<string>(), products: Product[] = [];
  for (const rawProduct of root.products.slice(0, 20)) {
    // A crowd-sourced entry may have no name yet (one of the twelve for 可口可乐 has none): it is left out of the search, not
    // a failure of it. A product looked up by its barcode still needs one.
    const name = object(rawProduct).product_name;
    if (name == null || (typeof name === "string" && !name.trim())) continue;
    const item = product(rawProduct);
    if (seen.has(item.code)) continue;
    seen.add(item.code); products.push(item);
  }
  return { products, total: root.count, at };
}
