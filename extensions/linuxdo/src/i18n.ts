import en from "./locales/en.json";
import zh from "./locales/zh-Hans.json";
import ja from "./locales/ja.json";
import { opening, oneLine, resolveZone, type HotTopic, type Topic, type Config } from "./model.ts";
export const resources = { en, "zh-Hans": zh, ja };
export type Texts = typeof en;
export const textsFor = (locale = ""): Texts => /^en(?:[-_]|$)/iu.test(locale) ? en : /^ja(?:[-_]|$)/iu.test(locale) ? ja : zh;
export const fmt = (template: string, ...args: (string | number)[]) => template.replace(/\{(\d+)\}/gu, (_, i: string) => String(args[Number(i)] ?? ""));
export function ago(t: Texts, ms: number): string { return ms < 60_000 ? t.justNow : ms < 3_600_000 ? fmt(t.minutes, Math.floor(ms / 60_000)) : ms < 86_400_000 ? fmt(t.hours, Math.floor(ms / 3_600_000)) : fmt(t.days, Math.floor(ms / 86_400_000)); }
export function date(at: number | null, zone: string, full = true): string {
  if (at === null) return "";
  if (!Number.isFinite(at) || Math.abs(at) > 8.64e15) throw new RangeError("timestamp outside Date formatter range");
  const options = { hourCycle: "h23", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" } as const;
  let formatter: Intl.DateTimeFormat;
  try { formatter = new Intl.DateTimeFormat("en", { ...options, timeZone: resolveZone(zone) }); }
  catch { formatter = new Intl.DateTimeFormat("en", { ...options, timeZone: "UTC" }); }
  const parts = formatter.formatToParts(at);
  const val = (type: string) => parts.find(p => p.type === type)?.value;
  return (full ? `${val("month")}-${val("day")} ` : "") + `${val("hour")}:${val("minute")}`;
}
export function meta(t: Texts, topic: HotTopic, now: number): string { return [topic.category, topic.author, topic.posts ? fmt(t.postsPeople, topic.posts, topic.people) : "", topic.at ? ago(t, now - topic.at) : ""].filter(Boolean).join(" · "); }
export function topicMeta(t: Texts, topic: Topic, zone: string): string { return [opening(topic)?.username, date(topic.at, zone), fmt(t.topicCounts, topic.views, topic.likeCount, topic.postsCount, topic.people), topic.tags.length ? fmt(t.tags, topic.tags.join("、")) : ""].filter(Boolean).join(" · "); }
export function describe(t: Texts, f: Config): string { return [[t.only, f.categories], [t.exclude, f.exclude_categories], [t.keywords, f.keywords], [t.blocked, f.exclude_keywords]].map(([template, values]) => Array.isArray(values) && values.length ? fmt(template as string, values.join("、")) : "").concat([f.hide_nsfw ? t.hideNSFW : "", f.hide_pinned ? t.hidePinned : ""]).filter(Boolean).join(" · "); }
export const safeErrorDetail = (value: string) => oneLine(value);
