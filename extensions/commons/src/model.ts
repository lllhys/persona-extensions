export const limits = Object.freeze({ request: 12_000, page: 15_000, tool: 15_000, response: 2 << 20, fresh: 60 * 60_000, cache: 30, cacheBytes: 2 << 20, gap: 200 });
export type LimitOptions = { [K in keyof typeof limits]?: number };
export type ReadCode = "invalid" | "bad_answer" | "not_found" | "rate_limited" | "slow" | "network" | "http";
export class ReadError extends Error {
  constructor(readonly code: ReadCode, readonly status?: number) { super(code); }
}
export const clip = (text: string, most: number) => text.length <= most ? text : text.slice(0, most).trimEnd() + "…";
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
export function queryText(value: unknown): string {
  if (typeof value !== "string") throw new ReadError("invalid");
  if (/[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  const query = oneLine(value);
  if (!query || Array.from(query).length > 160) throw new ReadError("invalid");
  return query;
}
export function searchPath(query: string, limit = 20): string {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
  return `/w/api.php?${new URLSearchParams({ action: "query", generator: "search", gsrsearch: `File:${queryText(query)}`,
    gsrnamespace: "6", gsrlimit: String(limit), prop: "imageinfo", iiprop: "url|extmetadata|size", iiurlwidth: "640", format: "json" })}`;
}
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  return value as Record<string, unknown>;
}
function array(value: unknown): unknown[] { if (!Array.isArray(value)) throw new ReadError("bad_answer"); return value; }
function string(value: unknown, fallback = "") { return value == null ? fallback : typeof value === "string" ? value : (() => { throw new ReadError("bad_answer"); })(); }
function integer(value: unknown, maximum = 100_000): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 0 || value > maximum) throw new ReadError("bad_answer");
  return value;
}
function uploadURL(value: unknown): string | undefined {
  if (value == null) return undefined;
  const raw = string(value);
  let url: URL;
  try { url = new URL(raw); } catch { throw new ReadError("bad_answer"); }
  if (url.protocol !== "https:" || !["upload.wikimedia.org", "thumb.wikimedia.org"].includes(url.hostname) || url.username || url.password || !url.pathname.startsWith("/wikipedia/commons/")) throw new ReadError("bad_answer");
  url.search = ""; url.hash = "";
  return url.href;
}
function commonsURL(value: unknown, title: string): string {
  const raw = value == null ? `https://commons.wikimedia.org/wiki/${title.replaceAll(" ", "_")}` : string(value);
  let url: URL;
  try { url = new URL(raw); } catch { throw new ReadError("bad_answer"); }
  if (url.protocol !== "https:" || url.hostname !== "commons.wikimedia.org" || url.username || url.password || !url.pathname.startsWith("/wiki/File:")) throw new ReadError("bad_answer");
  url.search = ""; url.hash = "";
  return url.href;
}
export interface ImageItem {
  id: number;
  title: string;
  description: string;
  artist: string;
  license: string;
  licenseURL?: string;
  pageURL: string;
  thumbnailURL?: string;
  width?: number;
  height?: number;
}
export interface SearchResult { images: ImageItem[]; at: number; stale?: ReadError }
function metadata(value: unknown, name: string): string {
  if (value == null) return "";
  const item = object(value);
  if (item.value == null) return "";
  return clip(plain(string(item.value)), 4000);
}
// licenseHosts serve their licenses over https too, so an http link to them is upgraded.
const licenseHosts = new Set(["creativecommons.org", "www.gnu.org", "gnu.org", "opensource.org"]);
// licenseLink is a file's license link as https. LicenseUrl comes from the file's page, and older files link their license
// over http (GFDL: http://www.gnu.org/copyleft/fdl.html); a link that cannot be made https is left out, the license name
// still shown, rather than failing the whole search.
function licenseLink(text: string): string | undefined {
  if (!text) return undefined;
  let url: URL;
  try { url = new URL(text); } catch { return undefined; }
  if (url.protocol === "http:" && licenseHosts.has(url.hostname)) url.protocol = "https:";
  return url.protocol === "https:" && !url.username && !url.password ? url.href : undefined;
}
export function parseImages(raw: unknown, at: number): SearchResult {
  const query = object(object(raw).query), pages = object(query.pages);
  const images = Object.entries(pages).slice(0, 20).map(([id, rawPage]) => {
    if (!/^[1-9][0-9]{0,9}$/.test(id)) throw new ReadError("bad_answer");
    const page = object(rawPage), title = string(page.title);
    if (!/^File:[^<>{}\[\]|#\u0000-\u001f]{1,255}$/.test(title)) throw new ReadError("bad_answer");
    const infos = array(page.imageinfo);
    if (!infos.length) throw new ReadError("bad_answer");
    const info = object(infos[0]), meta = object(info.extmetadata ?? {});
    const description = metadata(meta.ImageDescription, "description");
    const artist = metadata(meta.Artist, "artist");
    const license = metadata(meta.LicenseShortName, "license") || "See file page";
    const licenseURL = licenseLink(metadata(meta.LicenseUrl, "licenseURL"));
    const thumbnailURL = uploadURL(info.thumburl);
    const width = info.thumbwidth == null ? undefined : integer(info.thumbwidth, 640);
    const height = info.thumbheight == null ? undefined : integer(info.thumbheight, 100_000);
    return { id: integer(page.pageid ?? Number(id), 1_000_000_000), title: clip(title, 300), description, artist, license,
      ...(licenseURL ? { licenseURL } : {}), pageURL: commonsURL(info.descriptionurl, title),
      ...(thumbnailURL ? { thumbnailURL } : {}), ...(width === undefined ? {} : { width }), ...(height === undefined ? {} : { height }) };
  });
  return { images, at };
}
