import type { FetchAdapter, Snapshot, UIEvent } from "@persona/extension/ui";
export const sample = {
  key: "/works/OL59800W", title: "The Left Hand of Darkness", author_name: ["Ursula K. Le Guin"],
  first_publish_year: 1969, edition_count: 92, ebook_access: "printdisabled", ia: ["restricted-item"],
};
export const publicBook = {
  key: "/works/OL66554W", title: "Pride and Prejudice", author_name: ["Jane Austen"],
  first_publish_year: 1813, edition_count: 132, ebook_access: "public", public_scan_b: true,
  ia: ["restricted-item", "synapseml_gutenberg_pride_and_prejudice_by_jane_austen"],
};
export const detail = {
  key: "/works/OL59800W", title: "The Left Hand of Darkness", description: { type: "/type/text", value: "A synthetic description of a novel." },
  subjects: ["Science fiction", "Human nature"],
};
export class Fixture {
  clock = Date.UTC(2026, 10, 10);
  requests: { url: URL; init: RequestInit }[] = [];
  hits = new Map<string, number>();
  answers = new Map<string, { body: string; status?: number; delay?: number }>();
  cancelled = 0;
  constructor() {
    this.put("/search.json?q=synthetic&limit=20&fields=key%2Ctitle%2Cauthor_name%2Cfirst_publish_year%2Cedition_count%2Cebook_access%2Cpublic_scan_b%2Cia", { numFound: 2, docs: [sample, publicBook] });
    this.put("/search.json?q=synthetic&limit=10&fields=key%2Ctitle%2Cauthor_name%2Cfirst_publish_year%2Cedition_count%2Cebook_access%2Cpublic_scan_b%2Cia", { numFound: 2, docs: [sample, publicBook] });
    this.put("/search.json?q=OL59800W&limit=30&fields=key%2Ctitle%2Cauthor_name%2Cfirst_publish_year%2Cedition_count%2Cebook_access%2Cpublic_scan_b%2Cia", { numFound: 1, docs: [sample] });
    this.put("/works/OL59800W.json", detail);
    this.put("/works/OL66554W.json", { key: "/works/OL66554W", title: "Pride and Prejudice", description: "A public-domain novel.", subjects: ["Courtship"] });
  }
  put(path: string, value: unknown, options: { status?: number; delay?: number } = {}) {
    this.answers.set(path, { body: JSON.stringify(value), ...options });
  }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input), path = url.pathname + url.search;
    this.requests.push({ url, init }); this.hits.set(path, (this.hits.get(path) ?? 0) + 1);
    if (url.origin !== "https://openlibrary.org") throw new Error("unexpected origin");
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
