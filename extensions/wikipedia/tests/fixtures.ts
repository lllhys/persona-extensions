import type { FetchAdapter, Snapshot, UIEvent } from "@persona/extension/ui";
export const syntheticArticle = (title = "Example_article", overrides: Record<string, unknown> = {}) => ({
  type: "standard", title, normalizedtitle: title.replaceAll("_", " "),
  titles: { canonical: title }, description: "A synthetic subject", extract: "A synthetic introduction.",
  content_urls: { desktop: { page: "https://attacker.invalid/never-use-this" } }, ...overrides,
});
export const feed = {
  tfa: syntheticArticle("Featured_article"),
  mostread: { date: "2026-11-09Z", articles: [syntheticArticle("Popular_article", { views: 1234 })] },
  onthisday: [{ year: 1969, text: "A synthetic historical event.", pages: [syntheticArticle("Historical_article")] }],
  image: { title: "File:Synthetic.jpg", description: { text: "A synthetic picture" } },
};
export class Fixture {
  clock = Date.UTC(2026, 10, 9, 12);
  answers = new Map<string, { body: string; status?: number; delay?: number }>();
  hits = new Map<string, number>();
  requests: { url: URL; init: RequestInit; start: number; end?: number }[] = [];
  cancelled = 0;
  constructor() {
    this.put("/api/rest_v1/feed/featured/2026/11/09", feed);
    this.put("/api/rest_v1/page/summary/Featured_article", syntheticArticle("Featured_article", { extract: "A detailed synthetic introduction." }));
    this.put("/api/rest_v1/page/summary/Found_article", syntheticArticle("Found_article", { extract: "A found synthetic introduction." }));
    this.put("/w/rest.php/v1/search/page?q=synthetic&limit=30", { pages: [{ key: "Found_article", title: "Found article", description: "A result", excerpt: "A <span class=\"searchmatch\">synthetic</span> result &amp; summary" }] });
    this.put("/w/rest.php/v1/search/page?q=synthetic&limit=10", { pages: [{ key: "Found_article", title: "Found article", description: "A result", excerpt: "A synthetic result" }] });
  }
  put(path: string, value: unknown, options: { status?: number; delay?: number } = {}) { this.answers.set(path, { body: JSON.stringify(value), ...options }); }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input), path = url.pathname + url.search;
    if (!["en.wikipedia.org", "zh.wikipedia.org", "ja.wikipedia.org"].includes(url.hostname) || url.username || url.password) throw new Error("unexpected fixture target");
    this.requests.push({ url, init, start: Date.now() }); this.hits.set(path, (this.hits.get(path) ?? 0) + 1);
    const answer = this.answers.get(path) ?? { status: 404, body: "{}" };
    try {
      await new Promise<void>((resolve, reject) => {
        const timer = setTimeout(() => { init.signal.removeEventListener("abort", cancel); resolve(); }, answer.delay ?? 0);
        const cancel = () => { clearTimeout(timer); this.cancelled++; reject(init.signal.reason); };
        if (init.signal.aborted) cancel(); else init.signal.addEventListener("abort", cancel, { once: true });
      });
      return new Response(answer.body, { status: answer.status ?? 200 });
    } finally { this.requests[this.requests.length - 1].end = Date.now(); }
  };
}
export const tick = (ms = 2) => new Promise<void>(resolve => setTimeout(resolve, ms));
export async function until<T>(read: () => T | Promise<T>, predicate: (value: T) => boolean): Promise<T> {
  const deadline = Date.now() + 4000;
  while (Date.now() < deadline) { const value = await read(); if (predicate(value)) return value; await tick(); }
  throw new Error("fixture did not settle");
}
export function interaction(snapshot: Snapshot, key: string, values: Record<string, string> = {}): UIEvent {
  const item = snapshot.view.sections.flatMap(section => section.items).find(item => item.key === key);
  if (!item || !("action" in item) || !item.action) throw new Error(`missing action ${key}`);
  return { generation: snapshot.generation, version: snapshot.version, key, actionId: item.action.id, eventId: crypto.randomUUID(), values };
}
