import en from "./locales/en.json";
import zh from "./locales/zh-Hans.json";
import ja from "./locales/ja.json";
import { clip, nonEmpty, oneLine, ReadError, type Config, type Topic } from "./model.ts";
export type Texts = typeof en;
export const textsFor = (locale = ""): Texts => locale.startsWith("ja") ? ja : locale.startsWith("en") ? en : zh;
export const fmt = (text: string, ...args: (string | number | bigint)[]) => text.replace(/\{(\d+)\}/g, (_, n) => String(args[Number(n)] ?? ""));
export function errorText(error: unknown, locale = ""): string {
  const t = textsFor(locale); return error instanceof ReadError ? error.code === "http" ? fmt(t.http, error.status ?? 0) : t[error.code] : t.network;
}
export function describe(t: Texts, config: Config): string {
  return ([ ["exclude_nodes", "excludeNodes"], ["keywords", "keywords"], ["exclude_keywords", "excludeKeywords"] ] as const)
    .map(([key, label]) => nonEmpty(config[key]).length ? fmt(t[label], nonEmpty(config[key]).join("、")) : "").filter(Boolean).join(" · ");
}
export function ago(t: Texts, duration: number) {
  return duration < 60_000 ? t.now : duration < 3_600_000 ? fmt(t.minutes, Math.floor(duration / 60_000))
    : duration < 86_400_000 ? fmt(t.hours, Math.floor(duration / 3_600_000)) : fmt(t.days, Math.floor(duration / 86_400_000));
}
export function time(at: number | bigint, tz: string, date = true): string {
  const ms = typeof at === "bigint" ? at : BigInt(Math.trunc(at));
  tz = tz === "Local" ? new Intl.DateTimeFormat().resolvedOptions().timeZone : tz;
  const bound = 8640000000000000n;
  let projected = ms, zone = tz;
  if (ms < -bound || ms > bound) {
    // Go 1.24 uses unsigned absolute seconds, including wrap at the int64 edges.
    // Merely reducing Unix seconds modulo 400 years gives the wrong calendar there.
    const sec = ms / 1000n, epoch = 9223372028741760000n, cycle = 146097n * 86400n;
    const anchor = BigInt(Date.UTC(2400, 2, 1));
    const absolute = BigInt.asUintN(64, sec + epoch);
    const calendar = anchor + absolute % cycle * 1000n;
    // tzset bases the year/day on absolute seconds but its clock on Unix seconds.
    const lookup = sec < 0n ? BigInt(Date.UTC(-800, 0, 1)) : calendar - calendar % 86400000n + sec % 86400n * 1000n;
    const name = new Intl.DateTimeFormat("en-US", { timeZone: tz, timeZoneName: "longOffset" }).formatToParts(Number(lookup)).find(p => p.type === "timeZoneName")!.value;
    const match = /^GMT([+-])(\d{2}):(\d{2})(?::(\d{2}))?$/.exec(name);
    if (!match && name !== "GMT") throw new Error("unsupported timezone offset format");
    const offset = match ? BigInt((Number(match[2]) * 3600 + Number(match[3]) * 60 + Number(match[4] ?? 0)) * (match[1] === "-" ? -1 : 1)) : 0n;
    projected = anchor + BigInt.asUintN(64, sec + offset + epoch) % cycle * 1000n;
    zone = "UTC";
  }
  const parts = new Intl.DateTimeFormat("en-US", { timeZone: zone, month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).formatToParts(Number(projected));
  const part = (key: string) => parts.find(p => p.type === key)?.value ?? "00";
  return (date ? `${part("month")}-${part("day")} ` : "") + `${part("hour")}:${part("minute")}`;
}
export const title = (t: Texts, topic: Topic) => oneLine(topic.title) || fmt(t.topicTitle, topic.id);
export const meta = (t: Texts, topic: Topic, now: number) => {
  const at = topic.lastTouched || topic.created;
  // time.Sub saturates a duration and compares Go's signed internal seconds.
  const unix = BigInt.asIntN(64, BigInt(at) + 62135596800n) - 62135596800n;
  const ns = BigInt(Math.trunc(now)) * 1000000n - unix * 1000000000n, most = 9223372036854775807n;
  const duration = Number(ns > most ? most : ns < -most - 1n ? -most - 1n : ns) / 1000000;
  return [topic.node.title || topic.node.name, topic.author, fmt(t.replyCount, topic.replies), at > 0 ? ago(t, duration) : ""].filter(Boolean).join(" · ");
};
export const topicMeta = (t: Texts, topic: Topic, tz: string) => [topic.node.title || topic.node.name, topic.author,
  topic.created > 0 ? time(BigInt(topic.created) * 1000n, tz) : "", fmt(t.replyCount, topic.replies)].filter(Boolean).join(" · ");
export const missedText = (missed: { name: string; error: ReadError }[], locale: string) => clip(missed.map(m => `${m.name}（${errorText(m.error, locale)}）`).join("、"), 1800);
