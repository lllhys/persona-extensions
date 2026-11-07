import en from "./locales/en.json";
import zh from "./locales/zh-Hans.json";
import ja from "./locales/ja.json";
import { domain, words, type Item, type Filter } from "./model.ts";
export const resources = { en, "zh-Hans": zh, ja };
export type Texts = typeof en;
export function textsFor(locale = ""): Texts {
  if (/^en(?:[-_]|$)/i.test(locale)) return en;
  if (/^ja(?:[-_]|$)/i.test(locale)) return ja;
  return zh;
}
export function fmt(template: string, ...values: (string | number)[]): string {
  let i = 0; return template.replace(/%[sd]/g, () => String(values[i++]));
}
export function ago(t: Texts, milliseconds: number): string {
  if (milliseconds < 60_000) return t.now;
  if (milliseconds < 3_600_000) return fmt(t.minutes, Math.floor(milliseconds / 60_000));
  if (milliseconds < 86_400_000) return fmt(t.hours, Math.floor(milliseconds / 3_600_000));
  return fmt(t.days, Math.floor(milliseconds / 86_400_000));
}
export function meta(t: Texts, it: Item, now: number): string {
  return [fmt(t.points, it.score), fmt(t.nComments, it.descendants), domain(it), it.by, ago(t, now - it.time * 1000)].filter(Boolean).join(" · ");
}
export function describe(t: Texts, f: Filter): string {
  return [words(f.keywords).length ? fmt(t.keywords, words(f.keywords).join(t.listSep)) : "",
    words(f.exclude_keywords).length ? fmt(t.blocked, words(f.exclude_keywords).join(t.listSep)) : "",
    f.min_score > 0 ? fmt(t.minScore, f.min_score) : ""].filter(Boolean).join(" · ");
}
