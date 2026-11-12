import type { FetchAdapter, Snapshot, UIEvent } from "@persona/extension/ui";
import { gemPath, searchPath } from "../src/model.ts";
export const rails = {
  name: "rails", version: "8.1.4.1", info: "Ruby on Rails is a full-stack web framework.", licenses: ["MIT"],
  project_uri: "https://rubygems.org/gems/rails", source_code_uri: "https://github.com/rails/rails/tree/v8.1.4.1",
  homepage_uri: "https://rubyonrails.org", documentation_uri: "https://api.rubyonrails.org/v8.1.4.1/",
  downloads: 815065467, version_downloads: 1388621
};
export const rack = { ...rails, name: "rack", version: "3.2.4", info: "A modular Ruby webserver interface.", licenses: ["MIT"], project_uri: "https://rubygems.org/gems/rack", downloads: 1500000, version_downloads: 90000, source_code_uri: "http://github.com/rack/rack" };
export const searchResponse = [rails, rack];
export const detailResponse = {
  ...rails, version_created_at: "2026-11-04T02:47:52.512Z", yanked: false,
  dependencies: { development: [], runtime: [{ name: "actionpack", requirements: "= 8.1.4.1" }, { name: "activesupport", requirements: "= 8.1.4.1" }, { name: "railties", requirements: "= 8.1.4.1" }] }
};
export const searchPathFixture = searchPath("rails");
export const detailPath = gemPath("rails");
export class Fixture {
  clock = Date.UTC(2026, 10, 11);
  requests: { url: URL; init: RequestInit }[] = [];
  hits = new Map<string, number>();
  responses = new Map<string, { body: string; status?: number; delay?: number }>();
  cancelled = 0;
  constructor() { this.put(searchPathFixture, searchResponse); this.put(detailPath, detailResponse); this.put(gemPath("rack"), { ...detailResponse, ...rack, version_created_at: "2026-10-04T02:47:52.512Z", dependencies: { development: [], runtime: [{ name: "base64", requirements: ">= 0.2" }] } }); }
  put(path: string, value: unknown, options: { status?: number; delay?: number } = {}) { this.responses.set(path, { body: JSON.stringify(value), ...options }); }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input), path = url.pathname + url.search;
    this.requests.push({ url, init }); this.hits.set(path, (this.hits.get(path) ?? 0) + 1);
    if (url.origin !== "https://rubygems.org") throw new Error("unexpected origin");
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
