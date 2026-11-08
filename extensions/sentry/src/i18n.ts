import { ExtError } from "@persona/extension";
import en from "./locales/en.json";
import zh from "./locales/zh-Hans.json";
import ja from "./locales/ja.json";
export const locales = { en, "zh-Hans": zh, ja };
export type MessageKey = keyof typeof en;
export function messages(locale = "zh-Hans"): typeof en {
  return Object.hasOwn(locales, locale) ? locales[locale as keyof typeof locales] : en;
}
export function publicError(key: MessageKey, locale?: string): ExtError {
  return new ExtError(messages(locale)[key], "invalid_params");
}
