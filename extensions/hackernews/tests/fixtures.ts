import type { FetchAdapter } from "@persona/extension/ui";
import { feeds } from "../src/model.ts";
export const epoch = 1_800_000_000_000;
export class Fixture {
  time = epoch;
  bodies = new Map<string, unknown>();
  hits = new Map<string, number>();
  active = 0;
  peak = 0;
  delay = 0;
  constructor() {
    for (const endpoint of Object.values(feeds)) this.bodies.set(`/${endpoint}.json`, [1, 2, 3, 4]);
    this.item({ id: 1, type: "story", by: "fixture-a", time: epoch / 1000 - 7200, title: "Synthetic Rust article", url: "https://www.example.com/rust", score: 250, descendants: 3, kids: [11, 12, 13] });
    this.item({ id: 11, type: "comment", by: "fixture-b", time: epoch / 1000 - 600, text: 'Synthetic <i>comment</i>.<p>See <a href="https://example.org/full">short</a> &amp; more' });
    this.item({ id: 12, type: "comment", deleted: true });
    this.item({ id: 13, type: "comment", by: "fixture-c", time: epoch / 1000 - 60, text: '<pre><code>  sample()\n</code></pre>' });
    this.item({ id: 2, type: "story", by: "fixture-d", time: epoch / 1000 - 172800, title: "Ask HN: Synthetic question", text: "Synthetic &#x2F; text<p>Another paragraph", score: 40 });
    this.item({ id: 3, type: "story", title: "Synthetic old site", url: "http://plain.example.net/x", score: 5 });
    this.item({ id: 4, type: "story", dead: true });
  }
  item(value: { id: number } & Record<string, unknown>) { this.bodies.set(`/item/${value.id}.json`, value); }
  fetch: FetchAdapter = async (input, init) => {
    const url = new URL(input);
    if (url.origin !== "https://hacker-news.firebaseio.com" || init.method !== "GET" || init.redirect !== "error") throw new Error("unexpected egress");
    const path = url.pathname.replace(/^\/v0/, "");
    this.hits.set(path, (this.hits.get(path) ?? 0) + 1);
    this.peak = Math.max(this.peak, ++this.active);
    try {
      init.signal.throwIfAborted();
      if (this.delay) await new Promise<void>((resolve, reject) => {
        const done = () => { init.signal.removeEventListener("abort", abort); resolve(); };
        const timer = setTimeout(done, this.delay);
        const abort = () => { clearTimeout(timer); reject(init.signal.reason); };
        init.signal.addEventListener("abort", abort, { once: true });
      });
      const value = this.bodies.get(path) ?? null;
      return value instanceof Response ? value.clone() : new Response(JSON.stringify(value), { status: 200 });
    } finally { this.active--; }
  };
}
