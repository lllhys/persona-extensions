import type { FetchAdapter, Snapshot, UIEvent } from "@persona/extension/ui";
export const response = {
  meta: { count: 2 },
  results: [{
    id: "https://openalex.org/W4362515116", display_name: "A Survey of Large Language Models", publication_year: 2026,
    doi: "https://doi.org/10.1007/s11704-026-60308-3", cited_by_count: 8095,
    primary_location: { source: { display_name: "Frontiers of Computer Science" }, license: "cc-by" },
    open_access: { is_oa: true, oa_status: "hybrid", oa_url: "https://link.springer.com/content/pdf/10.1007/s11704-026-60308-3.pdf" },
  }, {
    id: "https://openalex.org/W1234567890", display_name: "Closed metadata example", publication_year: 2020,
    doi: "https://doi.org/10.1000/example", cited_by_count: 12,
    primary_location: { source: { display_name: "Example Journal" } }, open_access: { is_oa: false, oa_status: "closed" },
  }],
};
export class Fixture {
  clock = Date.UTC(2026, 10, 10);
  requests: { url: URL; init: RequestInit }[] = [];
  hits = new Map<string, number>();
  answers = new Map<string, { body: string; status?: number; delay?: number }>();
  cancelled = 0;
  constructor() {
    this.put("/works?search=synthetic&per-page=20&select=id%2Cdisplay_name%2Cpublication_year%2Cdoi%2Cprimary_location%2Copen_access%2Ccited_by_count", response);
    this.put("/works?search=synthetic&per-page=10&select=id%2Cdisplay_name%2Cpublication_year%2Cdoi%2Cprimary_location%2Copen_access%2Ccited_by_count", response);
  }
  put(path: string, value: unknown, options: { status?: number; delay?: number } = {}) { this.answers.set(path, { body: JSON.stringify(value), ...options }); }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input), path = url.pathname + url.search;
    this.requests.push({ url, init }); this.hits.set(path, (this.hits.get(path) ?? 0) + 1);
    if (url.origin !== "https://api.openalex.org") throw new Error("unexpected origin");
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
