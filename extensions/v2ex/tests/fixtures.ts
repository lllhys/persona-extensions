import type { FetchAdapter, Snapshot, UIEvent } from "@persona/extension/ui";
export const rawTopic = (id = 1, overrides: Record<string, unknown> = {}) => ({ id, title: `Synthetic topic ${id}`, content: "First paragraph\r\nSecond paragraph", replies: 25,
  created: 1700000000, last_touched: 1700000000 + id, node: { name: "go", title: "Go" }, member: { username: "fixture-author", bio: "DO-NOT-PROJECT-PROFILE" }, ...overrides });
export const rawReplies = (n = 25, size = 30) => Array.from({ length: n }, (_, i) => ({ id: i + 100, content: `Synthetic reply ${i + 1} ` + "😀".repeat(size),
  created: 1700000000 + i, member: { username: `fixture-user-${i + 1}`, location: "DO-NOT-PROJECT-PROFILE" } }));
export interface Answer { body: string; status?: number; headers?: Record<string, string>; delay?: number }
export const tick = (ms = 2) => new Promise<void>(resolve => setTimeout(resolve, ms));
export async function until<T>(read: () => T | Promise<T>, predicate: (value: T) => boolean): Promise<T> {
  const deadline = Date.now() + 3000;
  while (Date.now() < deadline) { const value = await read(); if (predicate(value)) return value; await tick(); }
  throw new Error("fixture did not settle before deadline");
}
export class Fixture {
  clock = 1700000000000;
  answers = new Map<string, Answer>();
  hits = new Map<string, number>();
  requests: { path: string; init: RequestInit; start: number; end?: number }[] = [];
  active = 0;
  mostActive = 0;
  cancelled = 0;
  constructor() {
    this.put("/topics/hot.json", [rawTopic(1), rawTopic(2, { title: "Synthetic Python article", node: { name: "python", title: "Python" } }), rawTopic(3, { title: "Synthetic job listing" })]);
    this.put("/topics/latest.json", [rawTopic(2), rawTopic(1)]);
    this.put("/topics/show.json?id=1", [rawTopic(1)]);
    this.put("/replies/show.json?topic_id=1", rawReplies());
    this.put("/topics/show.json?node_name=go", [rawTopic(1), rawTopic(4)]);
    this.put("/topics/show.json?node_name=python", [rawTopic(2), rawTopic(1)]);
  }
  put(path: string, value: unknown, options: Omit<Answer, "body"> = {}) { this.answers.set(path, { body: JSON.stringify(value), ...options }); }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input);
    if (url.origin !== "https://www.v2ex.com" || !url.pathname.startsWith("/api/")) throw new Error("unexpected fixture URL");
    const path = url.pathname.slice(4) + url.search;
    const record = { path, init, start: Date.now(), end: undefined as number | undefined }; this.requests.push(record);
    this.hits.set(path, (this.hits.get(path) ?? 0) + 1);
    const answer = this.answers.get(path) ?? { status: 404, body: "{}" };
    this.active++; this.mostActive = Math.max(this.active, this.mostActive);
    try {
      await new Promise<void>((resolve, reject) => {
        const done = () => { clearTimeout(timer); init.signal.removeEventListener("abort", cancel); };
        const cancel = () => { this.cancelled++; done(); reject(init.signal.reason); };
        const timer = setTimeout(() => { done(); resolve(); }, answer.delay ?? 0);
        if (init.signal.aborted) cancel(); else init.signal.addEventListener("abort", cancel, { once: true });
      });
      return new Response(answer.body, { status: answer.status ?? 200, headers: answer.headers });
    } finally { this.active--; record.end = Date.now(); }
  };
}
export function interaction(snapshot: Snapshot, key: string, values: Record<string, string> = {}): UIEvent {
  const item = snapshot.view.sections.flatMap(section => section.items).find(item => item.key === key);
  if (!item || !("action" in item) || !item.action) throw new Error(`missing action ${key}`);
  return { generation: snapshot.generation, version: snapshot.version, key, actionId: item.action.id, eventId: crypto.randomUUID(), values };
}
