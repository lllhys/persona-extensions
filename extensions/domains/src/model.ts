export const limits = Object.freeze({ request: 12_000, page: 18_000, tool: 18_000, response: 1 << 20, bootstrap: 24 * 60 * 60_000, fresh: 15 * 60_000, cache: 48, cacheBytes: 2 << 20, gap: 350 });
export type LimitOptions = { [K in keyof typeof limits]?: number };
export type ReadCode = "invalid" | "bad_answer" | "not_found" | "unsupported" | "rate_limited" | "slow" | "network" | "http" | "storage";
export class ReadError extends Error { constructor(readonly code: ReadCode, readonly status?: number) { super(code); } }
export const clip = (text: string, most: number) => Array.from(text).length <= most ? text : Array.from(text).slice(0, most).join("").trimEnd() + "…";
export const plain = (text: string) => text.replace(/\s+/gu, " ").trim();
function object(value: unknown): Record<string, unknown> { if (!value || typeof value !== "object" || Array.isArray(value)) throw new ReadError("bad_answer"); return value as Record<string, unknown>; }
function array(value: unknown): unknown[] { if (!Array.isArray(value)) throw new ReadError("bad_answer"); return value; }
function string(value: unknown): string { if (typeof value !== "string") throw new ReadError("bad_answer"); return value; }
export interface DomainName { ascii: string; unicode: string }
export function domainName(raw: unknown): DomainName {
  if (typeof raw !== "string" || raw.length > 1024 || /[\u0000- \u007f\\/@#?&=]/u.test(raw)) throw new ReadError("invalid");
  let unicode: string;
  try { unicode = raw.trim().toLowerCase().replace(/\.$/u, "").normalize("NFC"); } catch { throw new ReadError("invalid"); }
  if (!unicode || unicode.length > 253 || /[_*:%]/u.test(unicode) || /^\d{1,3}(?:\.\d{1,3}){3}$/u.test(unicode)) throw new ReadError("invalid");
  const labels = unicode.split(".");
  if (labels.length < 2 || labels.some(label => !label || label.length > 63)) throw new ReadError("invalid");
  const asciiLabels = labels.map(label => {
    let ascii: string;
    try { ascii = new URL(`https://${label}.invalid`).hostname.slice(0, -8); }
    catch { throw new ReadError("invalid"); }
    if (!ascii || ascii.length > 63 || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/u.test(ascii) || /^xn--/u.test(label) && !/^xn--[a-z0-9-]+$/u.test(label)) throw new ReadError("invalid");
    return ascii;
  });
  return { ascii: asciiLabels.join("."), unicode };
}
export interface RDAPService { origin: string; base: string }
export function rdapService(raw: unknown, domain: string): RDAPService {
  if (!["com", "net"].includes(domainName(domain).ascii.split(".").at(-1)!)) throw new ReadError("unsupported");
  const root = object(raw), suffix = domainName(domain).ascii.split(".").at(-1)!;
  for (const entry of array(root.services).slice(0, 4096)) {
    const pair = array(entry), zones = array(pair[0]).map(string).map(zone => zone.toLowerCase()), urls = array(pair[1]);
    if (!zones.includes(suffix)) continue;
    const candidates = urls.map(value => {
      try {
        const url = new URL(string(value));
        if (url.protocol !== "https:" || url.username || url.password || url.port || url.search || url.hash || !url.pathname.endsWith("/")) return "";
        if (url.origin !== "https://rdap.verisign.com" || url.pathname !== `/${suffix}/v1/`) return "";
        return url.toString();
      } catch { return ""; }
    }).filter(Boolean);
    if (candidates.length !== 1) throw new ReadError("unsupported");
    return { origin: new URL(candidates[0]).origin, base: candidates[0] };
  }
  throw new ReadError("unsupported");
}
export function lookupURL(service: RDAPService, domain: string): string {
  return `${service.base}domain/${encodeURIComponent(domainName(domain).ascii)}`;
}
export interface RegistrationEvent { action: string; at: string }
export interface Registration {
  domain: DomainName; name: string; statuses: string[]; events: RegistrationEvent[];
  registrar?: string; url: string; source: string; at: number; stale?: ReadError;
}
function publicLink(value: unknown, kind: "self" | "iana"): string {
  let url: URL;
  try { url = new URL(string(value)); } catch { throw new ReadError("bad_answer"); }
  if (url.protocol !== "https:" || url.username || url.password || url.port) throw new ReadError("bad_answer");
  if (kind === "iana") {
    if (url.origin !== "https://www.iana.org") throw new ReadError("bad_answer");
  } else if (url.hostname === "localhost" || url.hostname === "127.0.0.1" || url.hostname === "::1" || !url.hostname.includes(".")) throw new ReadError("bad_answer");
  url.hash = "";
  return url.toString();
}
function registrar(raw: unknown): string | undefined {
  if (!Array.isArray(raw)) return undefined;
  for (const entity of raw.slice(0, 32)) {
    const item = object(entity), roles = array(item.roles ?? []);
    if (!roles.includes("registrar")) continue;
    const fn = Array.isArray(item.vcardArray) && Array.isArray(item.vcardArray[1])
      ? (item.vcardArray[1] as unknown[]).find(row => Array.isArray(row) && row[0] === "fn" && typeof row[3] === "string") : undefined;
    if (fn) return clip(plain((fn as unknown[])[3] as string), 300);
  }
  return undefined;
}
function date(value: unknown): string {
  const raw = string(value);
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/u.test(raw) || !Number.isFinite(Date.parse(raw))) throw new ReadError("bad_answer");
  // Date.parse rolls an impossible calendar day into the next month; that is not a valid RDAP date.
  const [y, m, d] = raw.slice(0, 10).split("-").map(Number), parsed = new Date(raw);
  if (parsed.getUTCFullYear() !== y || parsed.getUTCMonth() !== m - 1 || parsed.getUTCDate() !== d) throw new ReadError("bad_answer");
  return parsed.toISOString();
}
export function parseRegistration(raw: unknown, domain: string, at: number): Registration {
  const requested = domainName(domain), root = object(raw);
  if (root.objectClassName !== "domain") throw new ReadError("bad_answer");
  const name = root.ldhName == null ? requested.ascii : domainName(string(root.ldhName)).ascii;
  if (name !== requested.ascii) throw new ReadError("bad_answer");
  const statuses = array(root.status ?? []).slice(0, 32).map(value => clip(plain(string(value)), 160)).filter(Boolean);
  const events = array(root.events ?? []).slice(0, 32).map(value => {
    const item = object(value);
    return { action: clip(plain(string(item.eventAction)), 80), at: date(item.eventDate) };
  });
  const self = array(root.links ?? []).slice(0, 32).find(link => object(link).rel === "self");
  const url = publicLink(object(self ?? {}).href, "self");
  return { domain: requested, name, statuses, events, registrar: registrar(root.entities), url, source: new URL(url).origin, at };
}
export const statusURL = "https://www.iana.org/assignments/rdap-status-values/rdap-status-values.xhtml";
