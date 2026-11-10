export const limits = Object.freeze({ request: 20_000, page: 25_000, tool: 25_000, response: 4 << 20, feedFresh: 15 * 60_000, articleFresh: 60 * 60_000, cache: 60, cacheBytes: 4 << 20, gap: 100 });
export type LimitOptions = { [K in keyof typeof limits]?: number };
export type Language = "en" | "zh" | "ja";
export type ReadCode = "invalid" | "bad_answer" | "not_found" | "rate_limited" | "slow" | "network" | "http";
export class ReadError extends Error {
  constructor(readonly code: ReadCode, readonly status?: number) { super(code); }
}
export interface Config { language?: string }
export function configFrom(raw: unknown): Config {
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = (raw as Record<string, unknown>).language;
  if (value == null || value === "") return {};
  if (value !== "auto" && value !== "en" && value !== "zh" && value !== "ja") throw new ReadError("invalid");
  return { language: value };
}
export function languageFor(config: Config, locale = ""): Language {
  if (config.language && config.language !== "auto") return config.language as Language;
  return locale.toLowerCase().startsWith("ja") ? "ja" : locale.toLowerCase().startsWith("zh") ? "zh" : "en";
}
export const clip = (text: string, most: number) => Array.from(text).length <= most ? text : Array.from(text).slice(0, most).join("").trimEnd() + "…";
export const oneLine = (text: string) => text.replace(/\s+/gu, " ").trim();
/** API snippets may contain presentation markup. Treat it as data, never JSX or HTML. */
export function plain(text: string): string {
  let out = "";
  for (let i = 0; i < text.length;) {
    if (text[i] === "<") { const end = text.indexOf(">", i + 1); if (end < 0) { out += " "; break; } i = end + 1; continue; }
    if (text[i] === "&") {
      const match = /^&(amp|lt|gt|quot|apos|#39|nbsp|#x[0-9a-fA-F]{1,6}|#[0-9]{1,7});/.exec(text.slice(i));
      if (match) {
        const name = match[1], cp = name.startsWith("#x") ? Number.parseInt(name.slice(2), 16) : name.startsWith("#") ? Number(name.slice(1)) : 0;
        out += name === "amp" ? "&" : name === "lt" ? "<" : name === "gt" ? ">" : name === "quot" ? '"' : name === "apos" || name === "#39" ? "'" : name === "nbsp" ? " " : validCodepoint(cp) ? String.fromCodePoint(cp) : "�";
        i += match[0].length; continue;
      }
    }
    out += text[i++];
  }
  return oneLine(out);
}
function validCodepoint(n: number) { return n >= 32 && n <= 0x10ffff && !(n >= 0xd800 && n <= 0xdfff) && n !== 0xfffe && n !== 0xffff; }
export function titleKey(value: unknown): string {
  if (typeof value !== "string") throw new ReadError("invalid");
  const title = plain(value).trim();
  if (/[<>&]/.test(value)) throw new ReadError("invalid");
  if (!title || title.length > 255 || /[\u0000-\u001f\u007f#?<>[\]{}|]/.test(title) || Array.from(title).length > 255) throw new ReadError("invalid");
  return title.replaceAll(" ", "_");
}
export function articleURL(language: Language, title: string) {
  return `https://${language}.wikipedia.org/wiki/${encodeURIComponent(titleKey(title)).replaceAll("%2F", "/")}`;
}
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  return value as Record<string, unknown>;
}
function array(value: unknown): unknown[] { if (!Array.isArray(value)) throw new ReadError("bad_answer"); return value; }
function string(value: unknown, fallback = "") { return value == null ? fallback : typeof value === "string" ? value : (() => { throw new ReadError("bad_answer"); })(); }
function integer(value: unknown): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0) throw new ReadError("bad_answer");
  return value;
}
export interface Article {
  title: string;
  heading: string;
  description: string;
  extract: string;
  url: string;
  views?: number;
}
export interface Event { year: number; text: string; pages: Article[] }
export interface Feed {
  date: string;
  featured?: Article;
  popular: Article[];
  events: Event[];
  at: number;
  stale?: ReadError;
}
export function parseArticle(raw: unknown, language: Language): Article {
  const value = object(raw), canonical = titleKey(value.titles && typeof value.titles === "object" && !Array.isArray(value.titles)
    ? (value.titles as Record<string, unknown>).canonical : value.title);
  const title = plain(string(value.normalizedtitle || value.title));
  return {
    title: canonical,
    heading: clip(title || canonical.replaceAll("_", " "), 300),
    description: clip(plain(string(value.description)), 500),
    extract: clip(plain(string(value.extract)), 12000),
    url: articleURL(language, canonical),
    ...(value.views !== undefined ? { views: integer(value.views) } : {}),
  };
}
export function validDate(value: string): boolean {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!m) return false;
  const [year, month, day] = m.slice(1).map(Number);
  if (year < 2016 || year > 2200 || month < 1 || month > 12 || day < 1 || day > 31) return false;
  const parsed = new Date(`${value}T00:00:00Z`);
  return parsed.getUTCFullYear() === year && parsed.getUTCMonth() + 1 === month && parsed.getUTCDate() === day;
}
export function dateAt(now: number): string {
  if (!Number.isFinite(now) || now < 0 || now > Date.UTC(2200, 0, 1)) throw new ReadError("invalid");
  return new Date(now).toISOString().slice(0, 10);
}
export function feedPath(date: string): string {
  if (!validDate(date)) throw new ReadError("invalid");
  return `/api/rest_v1/feed/featured/${date.replaceAll("-", "/")}`;
}
export function summaryPath(title: string): string { return `/api/rest_v1/page/summary/${encodeURIComponent(titleKey(title))}`; }
export function searchPath(query: string, limit = 30): string {
  if (typeof query !== "string" || !query.trim() || Array.from(query).length > 200 || /[\u0000-\u001f\u007f]/.test(query)) throw new ReadError("invalid");
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 30) throw new ReadError("invalid");
  return `/w/rest.php/v1/search/page?${new URLSearchParams({ q: query.trim(), limit: String(limit) })}`;
}
export function parseFeed(raw: unknown, language: Language, date: string, at: number): Feed {
  const value = object(raw), popular = array(object(value.mostread).articles).slice(0, 30).map(article => parseArticle(article, language));
  const events = array(value.onthisday).slice(0, 30).map(rawEvent => {
    const event = object(rawEvent);
    const year = integer(event.year);
    if (year > 2200) throw new ReadError("bad_answer");
    const text = clip(plain(string(event.text)), 1500);
    if (!text) throw new ReadError("bad_answer");
    const pages = array(event.pages).slice(0, 5).map(page => parseArticle(page, language));
    return { year, text, pages };
  });
  const seen = new Set<string>();
  for (const article of popular) {
    if (seen.has(article.title)) throw new ReadError("bad_answer");
    seen.add(article.title);
  }
  return { date, ...(value.tfa != null ? { featured: parseArticle(value.tfa, language) } : {}), popular, events, at };
}
export function parseSearch(raw: unknown, language: Language): Article[] {
  const seen = new Set<string>();
  return array(object(raw).pages).slice(0, 30).map(page => {
    const value = object(page), result = parseArticle({ title: value.key, normalizedtitle: value.title, description: value.description, extract: value.excerpt }, language);
    if (seen.has(result.title)) throw new ReadError("bad_answer");
    seen.add(result.title);
    return result;
  });
}
