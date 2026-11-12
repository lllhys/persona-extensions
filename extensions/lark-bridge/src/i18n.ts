import en from "./locales/en.json";
import zh from "./locales/zh-Hans.json";
import ja from "./locales/ja.json";
import { ReadError } from "./model.ts";
export type Texts = typeof en;
export const textsFor = (locale = ""): Texts => locale.toLowerCase().startsWith("ja") ? ja : locale.toLowerCase().startsWith("zh") ? zh : en;
export const fmt = (text: string, values: Record<string, string | number>) => text.replace(/\{(\w+)\}/g, (_, key) => String(values[key] ?? ""));
export function errorText(error: unknown, locale = ""): string {
  const t = textsFor(locale);
  return error instanceof ReadError ? t[error.code] : t.network;
}
export function readTime(value: string, locale = "en"): string {
  if (!value) return textsFor(locale).unknownTime;
  const milliseconds = /^\d+$/.test(value) ? Number(value) * (value.length <= 10 ? 1000 : 1) : Date.parse(value);
  if (!Number.isFinite(milliseconds) || milliseconds < 0 || milliseconds > 8.64e15) return textsFor(locale).unknownTime;
  try { return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(new Date(milliseconds)); }
  catch { return new Intl.DateTimeFormat(locale, { dateStyle: "medium" }).format(new Date(milliseconds)); }
}
