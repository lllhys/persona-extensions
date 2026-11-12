import type { FetchAdapter, Snapshot, UIEvent } from "@persona/extension/ui";
import { cratePath, searchPath } from "../src/model.ts";
export const serde = {
  id: "serde", name: "serde", updated_at: "2026-07-18T23:05:13.266456Z", created_at: "2014-12-05T20:20:39.487502Z",
  downloads: 1685619257, recent_downloads: 420047065, default_version: "1.0.229", newest_version: "1.0.229", max_version: "1.0.229",
  description: "A generic serialization/deserialization framework", homepage: "https://serde.rs", documentation: "https://docs.rs/serde", repository: "https://github.com/serde-rs/serde"
};
export const other = {
  ...serde, id: "serde-saphyr", name: "serde-saphyr", default_version: "2.1.1", newest_version: "2.1.1", max_version: "2.1.1",
  description: "YAML serializer for Serde", recent_downloads: 13802347, downloads: 18092766, repository: "https://github.com/example/serde-saphyr", homepage: null, documentation: "https://docs.rs/serde-saphyr"
};
export const searchResponse = { crates: [serde, other], meta: { total: 25327 } };
export const detailResponse = {
  crate: serde,
  versions: [
    { id: 1, crate: "serde", num: "1.0.229", license: "MIT OR Apache-2.0", created_at: "2026-07-18T23:05:13.266456Z", updated_at: "2026-07-18T23:05:13.266456Z", yanked: false, downloads: 236925653 },
    { id: 2, crate: "serde", num: "1.0.228", license: "MIT OR Apache-2.0", created_at: "2025-09-27T16:51:35.265429Z", updated_at: "2025-09-27T16:51:35.265429Z", yanked: false, downloads: 564775887 }
  ],
  keywords: [], categories: []
};
export const searchPathFixture = searchPath("serde", 12);
export const detailPath = cratePath("serde");
export class Fixture {
  clock = Date.UTC(2026, 10, 11);
  requests: { url: URL; init: RequestInit }[] = [];
  hits = new Map<string, number>();
  responses = new Map<string, { body: string; status?: number; delay?: number }>();
  cancelled = 0;
  constructor() {
    this.put(searchPathFixture, searchResponse);
    this.put(searchPath("serde", 10), searchResponse);
    this.put(detailPath, detailResponse);
  }
  put(path: string, value: unknown, options: { status?: number; delay?: number } = {}) { this.responses.set(path, { body: JSON.stringify(value), ...options }); }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input), path = url.pathname + url.search;
    this.requests.push({ url, init }); this.hits.set(path, (this.hits.get(path) ?? 0) + 1);
    if (url.origin !== "https://crates.io") throw new Error("unexpected origin");
    const answer = this.responses.get(path);
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
