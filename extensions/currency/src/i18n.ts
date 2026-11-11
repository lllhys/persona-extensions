import en from "./locales/en.json";
import zh from "./locales/zh-Hans.json";
import ja from "./locales/ja.json";
import { ReadError } from "./model.ts";
export type Texts = typeof en;
export const textsFor = (locale = ""): Texts => locale.toLowerCase().startsWith("ja") ? ja : locale.toLowerCase().startsWith("zh") ? zh : en;
export const fmt = (text: string, values: Record<string, string | number>) => text.replace(/\{(\w+)\}/g, (_, key) => String(values[key] ?? ""));
export function errorText(error: unknown, locale = ""): string {
  const t = textsFor(locale);
  if (!(error instanceof ReadError)) return t.network;
  return error.code === "http" ? fmt(t.http, { 0: error.status ?? 0 }) : t[error.code];
}
