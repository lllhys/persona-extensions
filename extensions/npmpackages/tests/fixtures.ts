import type { FetchAdapter, Snapshot, UIEvent } from "@persona/extension/ui";
import { downloadsPath, packagePath, searchPath } from "../src/model.ts";
export const typescript = {
  name: "typescript", version: "7.0.2", description: "TypeScript is a language for application scale JavaScript development",
  license: "Apache-2.0", homepage: "https://www.typescriptlang.org/", repository: { url: "git+https://github.com/microsoft/TypeScript.git", type: "git" },
  date: "2026-11-10T04:06:51.126Z", links: { npm: "https://www.npmjs.com/package/typescript", repository: "git+https://github.com/microsoft/TypeScript.git", homepage: "https://www.typescriptlang.org/" },
  engines: { node: ">=16.20.0" }, dependencies: { "example-dependency": "^1.0.0" },
  dist: { unpackedSize: 2497498 }, types: "./lib/typescript.d.ts"
};
export const scoped = {
  ...typescript, name: "@babel/preset-typescript", version: "8.0.7", description: "Babel preset for TypeScript.", license: "MIT",
  homepage: "https://babel.dev/docs/en/next/babel-preset-typescript", repository: { url: "https://github.com/babel/babel.git", type: "git", directory: "packages/babel-preset-typescript" },
  links: { npm: "https://www.npmjs.com/package/@babel/preset-typescript", repository: "https://github.com/babel/babel.git", homepage: "https://babel.dev/docs/en/next/babel-preset-typescript" }
};
export const searchResponse = { objects: [{ package: typescript, downloads: { weekly: 417015344, monthly: 1441813427 } }, { package: scoped, downloads: { weekly: 73675116, monthly: 256177168 } }], total: 473657, time: "2026-11-11T10:19:25.115Z" };
export const typescriptPath = searchPath("typescript", 12);
export const scopedPath = packagePath("@babel/preset-typescript");
export const downloadsScopedPath = downloadsPath("@babel/preset-typescript");
export class Fixture {
  clock = Date.UTC(2026, 10, 11);
  requests: { url: URL; init: RequestInit }[] = [];
  hits = new Map<string, number>();
  responses = new Map<string, { body: string; status?: number; delay?: number }>();
  cancelled = 0;
  constructor() {
    this.put(typescriptPath, searchResponse);
    this.put(searchPath("typescript", 10), searchResponse);
    this.put(scopedPath, scoped);
    this.put(downloadsScopedPath, { downloads: 73675116, start: "2026-11-03", end: "2026-11-09", package: "@babel/preset-typescript" });
  }
  put(path: string, value: unknown, options: { status?: number; delay?: number } = {}) { this.responses.set(path, { body: JSON.stringify(value), ...options }); }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input), path = url.pathname + url.search;
    this.requests.push({ url, init }); this.hits.set(url.origin + path, (this.hits.get(url.origin + path) ?? 0) + 1);
    if (!["https://registry.npmjs.org", "https://api.npmjs.org"].includes(url.origin)) throw new Error("unexpected origin");
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
