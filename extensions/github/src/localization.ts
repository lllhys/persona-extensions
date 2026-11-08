import en from "./locales/en.json";
import zh from "./locales/zh-Hans.json";
import ja from "./locales/ja.json";
import { GitHubError } from "./client.ts";
export type Key = keyof typeof en;
export function translations(locale = "en"): typeof en { return locale.toLowerCase().startsWith("zh") ? zh : locale.toLowerCase().startsWith("ja") ? ja : en; }
export function errorText(error: unknown, locale?: string): string { const t = translations(locale); return error instanceof GitHubError ? error.code === "proxy_unsupported" ? t.proxyBlocked : error.code === "bounded_log_unsupported" ? t.logBlocked : error.code === "response_too_large" ? t.tooLarge : error.code === "head_changed" ? t.changedHead : error.code === "not_signed_in" ? t.signIn : t.error : t.error; }
