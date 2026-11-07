import { decodeHTML } from "entities";
export const feeds = { top: "topstories", new: "newstories", best: "beststories", ask: "askstories", show: "showstories" } as const;
export type Feed = keyof typeof feeds;
export interface Item {
  id: number; type: string; by: string; time: number; title: string; url: string;
  text: string; score: number; descendants: number; kids: number[]; dead: boolean; deleted: boolean;
}
export interface Config { list?: string; keywords?: string[]; exclude_keywords?: string[]; min_score?: number }
export interface Filter { keywords: string[]; exclude_keywords: string[]; min_score: number }
export const isFeed = (value: unknown): value is Feed => typeof value === "string" && Object.hasOwn(feeds, value);
export const words = (values: string[] = []) => values.map(v => v.trim()).filter(Boolean);
export function normalize(config: Config): Required<Config> {
  return { list: isFeed(config.list) ? config.list : "top", keywords: words(config.keywords),
    exclude_keywords: words(config.exclude_keywords), min_score: Math.max(0, Math.trunc(config.min_score ?? 0)) };
}
export const gone = (item: Item) => item.deleted || item.dead || !item.id;
export function domain(item: Item): string {
  try { return new URL(item.url).hostname.replace(/^www\./, ""); } catch { return ""; }
}
export function safeLink(item: Item): string {
  try { const u = new URL(item.url); return u.protocol === "https:" && u.hostname && !u.username && !u.password ? u.href : ""; } catch { return ""; }
}
export const itemURL = (id: number) => `https://news.ycombinator.com/item?id=${id}`;
export function filter(items: Item[], f: Filter): Item[] {
  return items.filter(it => {
    const hay = `${it.title} ${domain(it)}`.toLowerCase();
    const has = (ws: string[]) => words(ws).some(w => hay.includes(w.toLowerCase()));
    return (!words(f.keywords).length || has(f.keywords)) && !has(f.exclude_keywords) && it.score >= f.min_score;
  });
}
export const active = (f: Filter) => !!(words(f.keywords).length || words(f.exclude_keywords).length || f.min_score > 0);
export const oneLine = (s: string) => s.trim().replace(/\s+/g, " ");
export function clip(s: string, n: number): string {
  const chars = Array.from(s); return chars.length <= n ? s : chars.slice(0, n - 1).join("").trim() + "…";
}
export function chunks(s: string, n = 1800): string[] {
  const chars = Array.from(s), out: string[] = [];
  while (chars.length > n) {
    let cut = n;
    for (let i = n; i > n / 2; i--) if (chars[i - 1] === "\n") { cut = i; break; }
    const piece = chars.splice(0, cut).join("").replace(/\n+$/, "");
    if (piece.trim()) out.push(piece);
  }
  const rest = chars.join("").trim(); if (rest) out.push(rest); return out;
}
// Decode after stripping tags: escaped markup stays literal native text, never HTML.
export function plain(s: string): string {
  return decodeHTML(s.replace(/<pre><code>(.*?)<\/code><\/pre>/gis, (_, code: string) => `\n\n${code.replace(/\n+$/, "")}\n\n`)
    .replace(/<a\s[^>]*href="([^"]*)"[^>]*>.*?<\/a>/gis, "$1")
    .replace(/<p>/gi, "\n\n").replace(/<[^>]*>/gs, ""))
    .replace(/\n{3,}/g, "\n\n").trim();
}
export function itemID(value: unknown): number {
  const s = typeof value === "string" || typeof value === "number" ? String(value).trim() : "";
  const digits = /^\d+$/.test(s) ? s : /^(?:https?:\/\/)?news\.ycombinator\.com\/item\?(?:.*&)?id=(\d+)(?:[&#].*)?$/i.exec(s)?.[1];
  const id = Number(digits); if (!Number.isSafeInteger(id) || id <= 0) throw new Error("item must be a Hacker News story's id or link"); return id;
}
