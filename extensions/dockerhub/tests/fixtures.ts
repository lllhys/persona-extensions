import type { FetchAdapter, Snapshot, UIEvent } from "@persona/extension/ui";
import { repositoryPath, searchPath, tagsPath } from "../src/model.ts";
export const nginx = { repo_name: "nginx", short_description: "Official build of Nginx.", star_count: 21419, pull_count: 13573379546, repo_owner: "", is_automated: false, is_official: true };
export const ingress = { ...nginx, repo_name: "nginx/nginx-ingress", short_description: "NGINX Ingress Controller", star_count: 124, pull_count: 1088577315, is_official: false };
export const searchResponse = { count: 295901, next: null, previous: null, results: [nginx, ingress] };
export const detailResponse = { user: "library", name: "nginx", namespace: "library", repository_type: "image", status: 1, description: "Official build of Nginx.", is_private: false, star_count: 21421, pull_count: 13579599047, last_updated: "2026-11-10T04:52:25.484949Z" };
export const tag = {
  name: "stable-alpine3.24-slim", digest: "sha256:e2355aa95529a7999dad94ddcb45a8768abf6fb0b204c93b1a3ffce477d7b893", last_updated: "2026-11-10T04:52:23.325515Z", tag_status: "active", content_type: "image",
  images: [
    { architecture: "amd64", variant: null, digest: "sha256:95af6eee6124ea2ebdee9a62c3de9684a6387a0d1531be13a6fd207c4a2fffaa", os: "linux", size: 5741494, status: "active" },
    { architecture: "arm64", variant: "v8", digest: "sha256:301e77a30f7f3ca9799b92bdb7bb3f1779303bc5b0b773bf2e48b597fea79dcd", os: "linux", size: 6096645, status: "active" },
    { architecture: "unknown", variant: null, digest: "sha256:b9050e5b5c3c7d6913be25a26b177fd77747dec89f96df6ba8ff7b5d7ea0fd3d", os: "unknown", size: 502372, status: "active" }
  ]
};
export const tagsResponse = { count: 120, next: null, previous: null, results: [tag] };
export const searchPathFixture = searchPath("nginx", 12);
export const detailPath = repositoryPath("library/nginx");
export const tagPath = tagsPath("library/nginx");
export class Fixture {
  clock = Date.UTC(2026, 10, 11);
  requests: { url: URL; init: RequestInit }[] = [];
  hits = new Map<string, number>();
  responses = new Map<string, { body: string; status?: number; delay?: number }>();
  cancelled = 0;
  constructor() {
    this.put(searchPathFixture, searchResponse); this.put(searchPath("nginx", 10), searchResponse); this.put(searchPath("nginx", 20), searchResponse); this.put(searchPath("nginx", 1), { ...searchResponse, results: [nginx] });
    this.put(detailPath, detailResponse); this.put(tagPath, tagsResponse);
    this.put(repositoryPath("nginx/nginx-ingress"), { ...detailResponse, namespace: "nginx", name: "nginx-ingress", pull_count: 1088577315 });
    this.put(tagsPath("nginx/nginx-ingress"), tagsResponse);
  }
  put(path: string, value: unknown, options: { status?: number; delay?: number } = {}) { this.responses.set(path, { body: JSON.stringify(value), ...options }); }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input), path = url.pathname + url.search;
    this.requests.push({ url, init }); this.hits.set(path, (this.hits.get(path) ?? 0) + 1);
    if (url.origin !== "https://hub.docker.com") throw new Error("unexpected origin");
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
