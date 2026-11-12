import type { FetchAdapter, Snapshot, UIEvent } from "@persona/extension/ui";
import { answersPath, searchPath } from "../src/model.ts";
export const question = {
  tags: ["javascript", "typescript"], is_answered: true, answer_count: 2, score: 17,
  accepted_answer_id: 70794814, question_id: 70794241, content_license: "CC BY-SA 4.0",
  link: "https://stackoverflow.com/questions/70794241/optional-properties",
  title: "How do optional properties work in TypeScript?",
  owner: { display_name: "Example Author", link: "https://stackoverflow.com/users/123/example" }
};
export const unanswered = { ...question, question_id: 70794242, accepted_answer_id: undefined, answer_count: 0, link: "https://stackoverflow.com/questions/70794242/unanswered", title: "An unanswered question" };
export const answer = {
  owner: { display_name: "Example Answerer", link: "https://stackoverflow.com/users/456/answerer" },
  is_accepted: true, score: 21, answer_id: 70794814, question_id: 70794241,
  content_license: "CC BY-SA 4.0", link: "https://stackoverflow.com/questions/70794241/optional-properties/70794814#70794814",
  body: '<p>Use <code>&lt;T&gt;</code> &amp; a spread.</p>\n<pre><code>const value = { ...input };\nconsole.log(value);</code></pre>\n<p><a href="https://www.typescriptlang.org/docs/">TypeScript docs</a> &gt; guide</p>'
};
export const questionPath = searchPath("typescript", "stackoverflow", "", 12);
export const answerPath = answersPath(70794241, "stackoverflow");
export class Fixture {
  clock = Date.UTC(2026, 10, 11);
  requests: { url: URL; init: RequestInit }[] = [];
  hits = new Map<string, number>();
  responses = new Map<string, { body: string; status?: number; delay?: number }>();
  cancelled = 0;
  constructor() {
    this.put(questionPath, { items: [question, unanswered], has_more: false, quota_remaining: 299 });
    this.put(searchPath("typescript", "stackoverflow", "", 10), { items: [question], has_more: false, quota_remaining: 298 });
    this.put(answerPath, { items: [answer], has_more: false, quota_remaining: 297 });
  }
  put(path: string, value: unknown, options: { status?: number; delay?: number } = {}) { this.responses.set(path, { body: JSON.stringify(value), ...options }); }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input), path = url.pathname + url.search;
    this.requests.push({ url, init }); this.hits.set(path, (this.hits.get(path) ?? 0) + 1);
    if (url.origin !== "https://api.stackexchange.com") throw new Error("unexpected origin");
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
    return new Response(answer.body, { status: answer.status ?? 200, headers: { "Content-Type": "application/json", "Content-Encoding": "gzip" } });
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
