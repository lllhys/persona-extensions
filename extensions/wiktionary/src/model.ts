export const limits = Object.freeze({ request: 12_000, page: 15_000, tool: 15_000, response: 2 << 20, fresh: 6 * 60 * 60_000, cache: 40, cacheBytes: 2 << 20, gap: 150 });
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
export function word(value: unknown): string {
  if (typeof value !== "string") throw new ReadError("invalid");
  const text = value.trim();
  if (/[\u0000-\u001f\u007f#?<>[\]{}|\\]/u.test(value) || text.split("/").includes("..")) throw new ReadError("invalid");
  if (!text || Array.from(text).length > 120) throw new ReadError("invalid");
  return text;
}
export function definitionPath(value: string): string { return `/api/rest_v1/page/definition/${encodeURIComponent(word(value)).replaceAll("%2F", "/")}`; }
export function entryURL(language: Language, value: string): string { return `https://${language}.wiktionary.org/wiki/${word(value).replaceAll(" ", "_")}`; }
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  return value as Record<string, unknown>;
}
function array(value: unknown): unknown[] { if (!Array.isArray(value)) throw new ReadError("bad_answer"); return value; }
function string(value: unknown, fallback = "") { return value == null ? fallback : typeof value === "string" ? value : (() => { throw new ReadError("bad_answer"); })(); }
export interface Example { text: string }
export interface Definition { text: string; examples: Example[] }
export interface SenseGroup { partOfSpeech: string; language: string; definitions: Definition[] }
export interface Entry {
  word: string;
  url: string;
  groups: SenseGroup[];
  at: number;
  stale?: ReadError;
}
export function parseEntry(raw: unknown, language: Language, value: string, at: number): Entry {
  const source = object(raw), groups: SenseGroup[] = [];
  for (const key of Object.keys(source).sort()) {
    const group = array(source[key]).slice(0, 20).map(rawGroup => {
      const item = object(rawGroup), partOfSpeech = clip(plain(string(item.partOfSpeech)), 100), label = clip(plain(string(item.language)), 100);
      if (!partOfSpeech) throw new ReadError("bad_answer");
      const definitions = array(item.definitions).slice(0, 12).map(rawDefinition => {
        const definition = object(rawDefinition), text = clip(plain(string(definition.definition)), 2000);
        if (!text) throw new ReadError("bad_answer");
        const examples = array(definition.examples ?? []).slice(0, 4).map(example => {
          const text = clip(plain(string(example)), 600);
          return text ? { text } : undefined;
        }).filter(example => example !== undefined) as Example[];
        return { text, examples };
      });
      return { partOfSpeech, language: label, definitions };
    });
    groups.push(...group);
  }
  if (!groups.length) throw new ReadError("not_found");
  return { word: word(value), url: entryURL(language, value), groups, at };
}
