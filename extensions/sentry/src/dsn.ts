import { publicError } from "./i18n.ts";
export interface DSN {
  origin: string;
  path: string;
  publicKey: string;
  project: string;
  endpoint: string;
  safe: string;
}
/** Never include the input URL in errors: URL parsers can echo credentials. */
export function parseDSN(raw: string, allowHTTP = false, locale?: string): DSN {
  const input = raw.trim();
  let url: URL;
  try { url = new URL(input); } catch { throw publicError("invalidDSN", locale); }
  if (url.protocol === "http:" && !allowHTTP) throw publicError("httpDisabled", locale);
  if (!["https:", "http:"].includes(url.protocol) || !url.hostname || !url.username) throw publicError("invalidDSN", locale);
  // Go decodes u.Path without resolving dot segments. URL.pathname has already
  // normalized them, so read the original path before query/fragment instead.
  const rawPath = /^[^:]+:\/\/[^/?#]*(\/[^?#]*)?/.exec(input)?.[1] ?? "";
  const pathname = decodePath(rawPath, locale).replace(/\/$/, "");
  const split = pathname.lastIndexOf("/");
  const project = pathname.slice(split + 1);
  if (!/^\d+$/.test(project) || BigInt(project) > 18446744073709551615n) throw publicError("invalidDSN", locale);
  const path = pathname.slice(0, split);
  let publicKey: string;
  try { publicKey = decodeURIComponent(url.username); } catch { throw publicError("invalidDSN", locale); }
  // Header injection is rejected rather than handed to a native error that might reveal the key.
  if (/[\s,\x00-\x1f\x7f]/.test(publicKey)) throw publicError("invalidDSN", locale);
  // Match Go's literal path construction. The sanctioned fetch/Request layer may
  // still canonicalize this string; builder parity is not wire-path parity.
  return { origin: url.origin, path, project, publicKey,
    safe: `${url.origin}${path}/${project}`, endpoint: `${url.origin}${path}/api/${project}/envelope/` };
}
function decodePath(value: string, locale?: string): string {
  try { return decodeURIComponent(value); } catch { throw publicError("invalidDSN", locale); }
}
