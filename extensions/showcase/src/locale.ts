import en from "./locales/en.json";
import zh from "./locales/zh-Hans.json";
import ja from "./locales/ja.json";

export const locales = { en, "zh-Hans": zh, ja };
export type Locale = keyof typeof locales;
export type Message = keyof typeof en;
export type Translate = (key: Message, values?: Record<string, string | number>) => string;
export function locale(value = "en"): Locale {
  if (value === "ja" || value.startsWith("ja-")) return "ja";
  if (value === "zh" || value === "zh-CN" || value === "zh-Hans" || value.startsWith("zh-Hans-")) return "zh-Hans";
  return "en";
}
export function translator(language: string = "en"): Translate {
  const messages = locales[locale(language)];
  return (key, values = {}) => messages[key].replace(/\{(\w+)\}/g, (match, name: string) => String(values[name] ?? match));
}
