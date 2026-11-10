import en from "./locales/en.json";
import zh from "./locales/zh-Hans.json";
import ja from "./locales/ja.json";
import { ReadError } from "./model.ts";
export type Texts = typeof en;
export const textsFor = (locale = ""): Texts => locale.toLowerCase().startsWith("ja") ? ja : locale.toLowerCase().startsWith("zh") ? zh : en;
export const fmt = (text: string, values: Record<string, string | number> | (string | number)[], ...args: (string | number)[]) => text.replace(/\{(\w+)\}/g, (_, key) => String(Array.isArray(values) ? values[Number(key)] ?? "" : values[key] ?? ""));
export function errorText(error: unknown, locale = ""): string {
  const t = textsFor(locale);
  if (!(error instanceof ReadError)) return t.network;
  return error.code === "http" ? fmt(t.http, { 0: error.status ?? 0 }) : t[error.code];
}
export function readTime(at: number, timeZone = "Local", locale = "en"): string {
  const zone = timeZone === "Local" ? undefined : timeZone;
  return new Intl.DateTimeFormat(locale, { year: "numeric", month: "short", day: "numeric", timeZone: zone }).format(at);
}
