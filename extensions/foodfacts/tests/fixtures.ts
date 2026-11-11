import type { FetchAdapter, Snapshot, UIEvent } from "@persona/extension/ui";
export const searchResponse = {
  count: 2, page: 1, page_count: 2, page_size: 12, products: [{
    code: "3017620422003", product_name: "Nutella", brands: "Ferrero, Nutella", quantity: "400 g",
    ingredients_text: "sugar, palm oil", allergens: "milk, nuts, soybeans", nutriscore_grade: "e", nova_group: 3,
    url: "https://world.openfoodfacts.org/product/3017620422003/nutella-ferrero",
  }, {
    code: "8052575090230", product_name: "Nocciolata", brands: "Rigoni di Asiago", quantity: "250 g",
    ingredients_text: "raw sugar, hazelnut paste", allergens: "milk, nuts, soybeans", nutriscore_grade: "e", nova_group: 4,
    url: "https://world.openfoodfacts.org/product/8052575090230/nocciolata-rigoni-di-asiago",
  }],
};
export const productResponse = { code: "3017620422003", product: searchResponse.products[0], status: 1, status_verbose: "product found" };
export const searchPath = "/cgi/search.pl?search_terms=chocolate&search_simple=1&action=process&json=1&page_size=12&fields=code%2Cproduct_name%2Cbrands%2Cquantity%2Cnutriscore_grade%2Cnova_group%2Callergens%2Cingredients_text%2Curl";
export const productPath = "/api/v2/product/3017620422003?fields=code%2Cproduct_name%2Cbrands%2Cquantity%2Cnutriscore_grade%2Cnova_group%2Callergens%2Cingredients_text%2Curl";
export class Fixture {
  clock = Date.UTC(2026, 10, 10);
  requests: { url: URL; init: RequestInit }[] = [];
  hits = new Map<string, number>();
  answers = new Map<string, { body: string; status?: number; delay?: number }>();
  cancelled = 0;
  constructor() {
    this.put(searchPath, searchResponse);
    this.put(searchPath.replace("page_size=12", "page_size=10"), searchResponse);
    this.put(productPath, productResponse);
  }
  put(path: string, value: unknown, options: { status?: number; delay?: number } = {}) { this.answers.set(path, { body: JSON.stringify(value), ...options }); }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input), path = url.pathname + url.search;
    this.requests.push({ url, init }); this.hits.set(path, (this.hits.get(path) ?? 0) + 1);
    if (url.origin !== "https://world.openfoodfacts.org") throw new Error("unexpected origin");
    const answer = this.answers.get(path);
    if (!answer) return new Response("not found", { status: 404 });
    const signal = init.signal;
    if (answer.delay || signal.aborted) {
      await new Promise<void>((resolve, reject) => {
        const cancel = () => { clearTimeout(timer); this.cancelled++; reject(signal.reason); };
        const timer = setTimeout(resolve, answer.delay ?? 20);
        if (signal.aborted) cancel(); else signal.addEventListener("abort", cancel, { once: true });
      });
    }
    return new Response(answer.body, { status: answer.status ?? 200, headers: { "Content-Type": "application/json" } });
  };
}
export const until = async <T>(read: () => Promise<T>, ready: (value: T) => boolean): Promise<T> => {
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) { const value = await read(); if (ready(value)) return value; await new Promise(resolve => setTimeout(resolve, 10)); }
  throw new Error("condition did not settle");
};
export function interaction(snapshot: Snapshot, key: string, values: Record<string, string> = {}): UIEvent {
  const item = snapshot.view.sections.flatMap(section => section.items).find(item => item.key === key);
  if (!item || !("action" in item) || !item.action) throw new Error("missing action " + key);
  return { generation: snapshot.generation, version: snapshot.version, key, actionId: item.action.id, eventId: crypto.randomUUID(), values };
}
