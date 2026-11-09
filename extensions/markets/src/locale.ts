import en from "./locales/en.json";
import zh from "./locales/zh-Hans.json";
import ja from "./locales/ja.json";
export type LocaleKey = keyof typeof en;
export const locales = { en, "zh-Hans": zh, ja };
/** Source facts are cached once; translate their magnitude suffix only at the presentation boundary. */
export function localizeValue(value: string, locale?: string): string {
  const keys = ["unitTrillion", "unitHundredMillion", "unitTenThousand"] as const;
  const matched = keys.find(key => value.includes(zh[key]));
  return matched ? value.replace(zh[matched], translator(locale)(matched)) : value;
}
export function translator(locale = "zh-Hans") { const dict: Record<string, string> = locale.startsWith("ja") ? ja : locale.startsWith("zh") ? zh : en; return (key: LocaleKey | string, values: Record<string, string | number> = {}) => (dict[key] ?? key).replace(/\{(\w+)\}/g, (_, k) => String(values[k] ?? "")); }
