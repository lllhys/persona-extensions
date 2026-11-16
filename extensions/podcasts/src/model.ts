export const limits = Object.freeze({ request: 12_000, page: 18_000, tool: 18_000, response: 2 << 20, fresh: 15 * 60_000, cache: 48, cacheBytes: 2 << 20, gap: 350 });
export type LimitOptions = { [K in keyof typeof limits]?: number };
export type ReadCode = "invalid" | "bad_answer" | "not_found" | "rate_limited" | "slow" | "network" | "http" | "storage";
export class ReadError extends Error {
  constructor(readonly code: ReadCode, readonly status?: number) { super(code); }
}
export const clip = (text: string, most: number) => Array.from(text).length <= most ? text : Array.from(text).slice(0, most).join("").trimEnd() + "…";
export const plain = (text: string) => text.replace(/\s+/gu, " ").trim();
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer");
  return value as Record<string, unknown>;
}
function array(value: unknown): unknown[] { if (!Array.isArray(value)) throw new ReadError("bad_answer"); return value; }
function string(value: unknown, fallback = "") {
  if (value == null) return fallback;
  if (typeof value !== "string") throw new ReadError("bad_answer");
  return value;
}
export function id(value: unknown): number {
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 1 || value > 2_000_000_000_000) throw new ReadError("bad_answer");
  return value;
}
export function podcastID(value: unknown): number {
  const text = typeof value === "number" ? value : typeof value === "string" && /^[1-9][0-9]{0,12}$/.test(value) ? Number(value) : NaN;
  if (!Number.isSafeInteger(text) || text < 1 || text > 2_000_000_000_000) throw new ReadError("invalid");
  return text;
}
function optionalID(value: unknown): number | undefined {
  if (value == null) return undefined;
  if (typeof value !== "number" || !Number.isSafeInteger(value) || value < 1 || value > 2_000_000_000_000) throw new ReadError("bad_answer");
  return value;
}
export function publicURL(value: unknown, allowed: "apple" | "feed"): string | undefined {
  if (value == null) return undefined;
  if (typeof value !== "string" || value.length > 2048 || /[\u0000- \u007f]/u.test(value)) throw new ReadError("bad_answer");
  let url: URL;
  try { url = new URL(value); } catch { throw new ReadError("bad_answer"); }
  if (url.protocol !== "https:" || url.username || url.password || url.port) throw new ReadError("bad_answer");
  const host = url.hostname.toLowerCase();
  if (allowed === "apple") {
    if (host !== "podcasts.apple.com" || !/^\/[a-z]{2}(?:-[a-z]{2})?\/podcast\//i.test(url.pathname)) throw new ReadError("bad_answer");
    url.search = ""; url.hash = "";
  }
  return url.toString();
}
function text(value: unknown, most: number): string { return clip(plain(string(value)), most); }
function date(value: unknown): string | undefined {
  if (value == null) return undefined;
  const raw = string(value);
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,3})?Z$/.test(raw) || !Number.isFinite(Date.parse(raw))) throw new ReadError("bad_answer");
  // Date.parse rolls an impossible calendar day into the next month; that is not a valid episode date.
  const [y, m, d] = raw.slice(0, 10).split("-").map(Number), parsed = new Date(raw);
  if (parsed.getUTCFullYear() !== y || parsed.getUTCMonth() !== m - 1 || parsed.getUTCDate() !== d) throw new ReadError("bad_answer");
  return parsed.toISOString();
}
export interface Show {
  id: number; title: string; publisher: string; genre: string; episodes?: number;
  url: string; feedURL?: string;
}
export interface Episode {
  id: number; title: string; at?: string; minutes?: number; description: string;
  url: string; showURL: string;
}
export interface SearchResult { shows: Show[]; at: number; stale?: ReadError }
export interface EpisodesResult { show: Show; episodes: Episode[]; at: number; stale?: ReadError }
export function queryText(value: unknown): string {
  if (typeof value !== "string") throw new ReadError("invalid");
  const text = plain(value);
  if (!text || Array.from(text).length > 200 || /[\u0000-\u001f\u007f]/u.test(value)) throw new ReadError("invalid");
  return text;
}
export function searchPath(query: string, limit = 20): string {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 30) throw new ReadError("invalid");
  return `/search?${new URLSearchParams({ term: queryText(query), media: "podcast", entity: "podcast", limit: String(limit) })}`;
}
export function lookupPath(show: unknown, limit = 20): string {
  if (!Number.isSafeInteger(limit) || limit < 1 || limit > 30) throw new ReadError("invalid");
  return `/lookup?${new URLSearchParams({ id: String(podcastID(show)), entity: "podcastEpisode", limit: String(limit), sort: "recent" })}`;
}
export function showPath(show: unknown): string {
  return `/lookup?${new URLSearchParams({ id: String(podcastID(show)), entity: "podcast", limit: "1" })}`;
}
export function canonicalShowURL(show: unknown): string { return `https://podcasts.apple.com/us/podcast/id${podcastID(show)}`; }
export function canonicalEpisodeURL(show: unknown, episode: unknown): string {
  return `${canonicalShowURL(show)}?i=${podcastID(episode)}`;
}
export function parseShow(raw: unknown): Show {
  const value = object(raw);
  if (value.wrapperType !== "track" || value.kind !== "podcast") throw new ReadError("bad_answer");
  const show = id(value.collectionId), title = text(value.collectionName, 500);
  if (!title) throw new ReadError("bad_answer");
  // Apple owns the show identity; the catalog's arbitrary host/country/query is
  // not propagated into a link. Publisher feeds are data for the user's player.
  const url = canonicalShowURL(show);
  if (value.collectionViewUrl != null) publicURL(value.collectionViewUrl, "apple");
  const episodes: number | undefined = value.trackCount == null ? undefined : value.trackCount as number;
  if (episodes !== undefined && (typeof value.trackCount !== "number" || !Number.isSafeInteger(episodes) || episodes < 0 || episodes > 1_000_000)) throw new ReadError("bad_answer");
  return { id: show, title, publisher: text(value.artistName, 300), genre: text(value.primaryGenreName, 100),
    ...(episodes === undefined ? {} : { episodes }), url, feedURL: publicURL(value.feedUrl, "feed") };
}
function parseEpisode(raw: unknown, show: Show): Episode {
  const value = object(raw);
  if (value.wrapperType !== "podcastEpisode" || value.kind !== "podcast-episode" || id(value.collectionId) !== show.id) throw new ReadError("bad_answer");
  const episode = id(value.trackId), title = text(value.trackName, 500);
  if (!title) throw new ReadError("bad_answer");
  const milliseconds: unknown = value.trackTimeMillis;
  if (milliseconds != null && (typeof milliseconds !== "number" || !Number.isSafeInteger(milliseconds) || milliseconds < 0 || milliseconds > 7 * 24 * 60 * 60_000)) throw new ReadError("bad_answer");
  return { id: episode, title, ...(value.releaseDate == null ? {} : { at: date(value.releaseDate) }),
    ...(milliseconds == null ? {} : { minutes: Math.round(milliseconds / 60_000) }),
    description: text(value.description, 8000), url: canonicalEpisodeURL(show.id, episode), showURL: show.url };
}
export function parseSearch(raw: unknown, at: number): SearchResult {
  const value = object(raw), count = value.resultCount;
  if (typeof count !== "number" || !Number.isSafeInteger(count) || count < 0 || count > 200) throw new ReadError("bad_answer");
  const shows = array(value.results).slice(0, 30).filter(item => object(item).wrapperType === "track").map(parseShow);
  const unique = shows.filter((show, index) => shows.findIndex(other => other.id === show.id) === index);
  return { shows: unique, at };
}
export function parseEpisodes(raw: unknown, expected: unknown, at: number): EpisodesResult {
  const showID = podcastID(expected), value = object(raw), results = array(value.results).slice(0, 50);
  const shows = results.filter(item => object(item).wrapperType === "track").map(parseShow).filter(show => show.id === showID);
  if (shows.length !== 1) throw new ReadError("not_found");
  const episodes = results.filter(item => object(item).wrapperType === "podcastEpisode").map(item => parseEpisode(item, shows[0]));
  const unique = episodes.filter((episode, index) => episodes.findIndex(other => other.id === episode.id) === index);
  return { show: shows[0], episodes: unique, at };
}
export function parseStoredShow(raw: unknown): Show | undefined {
  try {
    const value = object(raw), show = parseShow({ wrapperType: "track", kind: "podcast", collectionId: value.id,
      collectionName: value.title, artistName: value.publisher, primaryGenreName: value.genre, trackCount: value.episodes, feedUrl: value.feedURL });
    if (show.title !== value.title || show.publisher !== value.publisher || show.genre !== value.genre || show.feedURL !== value.feedURL || show.episodes !== value.episodes || show.url !== value.url || show.id !== value.id) return undefined;
    return show;
  } catch { return undefined; }
}
