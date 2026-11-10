import type { FetchAdapter, Snapshot, UIEvent } from "@persona/extension/ui";
export const response = {
  response: { numFound: 2, docs: [
    { identifier: "prideprejudice_0000", title: "Pride and Prejudice", creator: "Jane Austen", year: 1813, mediatype: "texts", downloads: 12345, description: "A public-domain novel." },
    { identifier: "example.audio", title: "Example recording", creator: ["Example Artist", "Example Reader"], year: 1950, mediatype: "audio", downloads: 42, description: "An example audio item." },
  ] },
};
export class Fixture {
  clock = Date.UTC(2026, 10, 10);
  requests: { url: URL; init: RequestInit }[] = [];
  hits = new Map<string, number>();
  answers = new Map<string, { body: string; status?: number; delay?: number }>();
  cancelled = 0;
  constructor() {
    this.put("/advancedsearch.php?q=%28synthetic%29&fl=identifier%2Ctitle%2Ccreator%2Cyear%2Cmediatype%2Cdownloads%2Cdescription&rows=20&page=1&output=json", response);
    this.put("/advancedsearch.php?q=%28synthetic%29&fl=identifier%2Ctitle%2Ccreator%2Cyear%2Cmediatype%2Cdownloads%2Cdescription&rows=10&page=1&output=json", response);
    this.put("/advancedsearch.php?q=%28synthetic%29+AND+mediatype%3Atexts&fl=identifier%2Ctitle%2Ccreator%2Cyear%2Cmediatype%2Cdownloads%2Cdescription&rows=20&page=1&output=json", response);
  }
  put(path: string, value: unknown, options: { status?: number; delay?: number } = {}) { this.answers.set(path, { body: JSON.stringify(value), ...options }); }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input), path = url.pathname + url.search;
    this.requests.push({ url, init }); this.hits.set(path, (this.hits.get(path) ?? 0) + 1);
    if (url.origin !== "https://archive.org") throw new Error("unexpected origin");
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
