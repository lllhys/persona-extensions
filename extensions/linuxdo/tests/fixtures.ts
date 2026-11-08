import type { FetchAdapter } from "@persona/extension/ui";
export const epoch = Date.parse("2026-11-03T12:37:02Z");
export interface Item { id: string | number; title?: string; category?: string; excerpt?: string; author?: string; pinned?: boolean; link?: string; english?: boolean }
const xml = (value: string) => value.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");
export function rss(items: Item[] = []): string {
  return `<?xml version="1.0"?><rss xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:discourse="http://www.discourse.org/"><channel>${items.map(it => `<item><title>${xml(it.title ?? "Synthetic topic")}</title><link>${xml(it.link ?? `https://linux.do/t/topic/${it.id}`)}</link><dc:creator><![CDATA[${it.author ?? "fixture-author"}]]></dc:creator><category>${xml(it.category ?? "Dev")}</category><discourse:topicPinned>${it.pinned ? "Yes" : "No"}</discourse:topicPinned><pubDate>Tue, 03 Nov 2026 09:37:02 +0000</pubDate><description><![CDATA[<p>${it.excerpt ?? "Synthetic excerpt"}</p><p>75 ${it.english ? "posts - 59 participants" : "个帖子 - 59 位参与者"}</p><p>${it.english ? "Read full topic" : "阅读完整话题"}</p>]]></description></item>`).join("")}</channel></rss>`;
}
export function topic(id = 1, cooked = "<p>Synthetic opening</p>", replies: string[] = ["<p>Low score</p>", "<p>High score</p>"]): Record<string, any> {
  return { id, title: "Synthetic Rust topic", posts_count: replies.length + 1, like_count: 110, views: 1098, participant_count: 13, created_at: "2026-11-03T09:37:02Z", tags: [{ name: "synthetic-tag" }], post_stream: { posts: [
    { id: id * 100, username: "fixture-op", created_at: "2026-11-03T09:37:02Z", cooked, post_number: 1, post_type: 1, score: 500, actions_summary: [{ id: 2, count: 89 }] },
    ...replies.map((cooked, i) => ({ id: id * 100 + i + 1, username: `fixture-${i + 2}`, created_at: "2026-11-03T10:00:00Z", cooked, post_number: i + 2, post_type: 1, score: 10 * (i + 1), reaction_users_count: i }))
  ] } };
}
export class Fixture {
  time = epoch; delay = 0; active = 0; peak = 0; cancelled = 0;
  bodies = new Map<string, string | object | Response>(); hits = new Map<string, number>();
  calls: { url: string; init?: RequestInit; at: number }[] = [];
  constructor() {
    const items = [
      { id: 10, title: "Synthetic pinned announcement", pinned: true, category: "Meta" },
      { id: 1, title: "Synthetic Rust topic", category: "Dev", excerpt: "Rust benchmark" },
      { id: 2, title: "[NSFW] synthetic", category: "Other" },
      { id: 3, title: "Synthetic news", category: "News" },
      { id: 4, title: "Synthetic GPU topic", category: "Resources", excerpt: "AI training" }
    ];
    this.bodies.set("/hot.rss", rss(items)); this.bodies.set("/latest.rss", rss(items)); this.bodies.set("/top.rss?period=daily", rss([items[3]])); this.bodies.set("/top.rss?period=weekly", rss()); this.bodies.set("/t/topic/1.json", topic());
  }
  fetch: FetchAdapter = async (input, init) => {
    init?.signal?.throwIfAborted();
    const url = new URL(input);
    if (url.origin !== "https://linux.do") throw new Error("fixture refuses external origin");
    const path = url.pathname + url.search; this.hits.set(path, (this.hits.get(path) ?? 0) + 1);
    this.calls.push({ url: url.href, init, at: Date.now() }); this.active++; this.peak = Math.max(this.peak, this.active);
    try {
      if (this.delay) await new Promise<void>((resolve, reject) => {
        const done = () => { clearTimeout(timer); init?.signal?.removeEventListener("abort", abort); };
        const abort = () => { done(); this.cancelled++; reject(init?.signal?.reason); };
        const timer = setTimeout(() => { done(); resolve(); }, this.delay); init?.signal?.addEventListener("abort", abort, { once: true });
      });
      const value = this.bodies.get(path);
      return value instanceof Response ? value.clone() : value === undefined ? new Response("missing", { status: 404 }) : new Response(typeof value === "string" ? value : JSON.stringify(value));
    } finally { this.active--; }
  };
}
