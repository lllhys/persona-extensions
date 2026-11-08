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
  let url: URL;
  try { url = new URL(raw.trim()); } catch { throw publicError("invalidDSN", locale); }
  if (url.protocol === "http:" && !allowHTTP) throw publicError("httpDisabled", locale);
  if (!["https:", "http:"].includes(url.protocol) || !url.hostname || !url.username) throw publicError("invalidDSN", locale);
  const pathname = decodePath(url.pathname, locale).replace(/\/$/, "");
  const split = pathname.lastIndexOf("/");
  const project = pathname.slice(split + 1);
  if (!/^\d+$/.test(project) || BigInt(project) > 18446744073709551615n) throw publicError("invalidDSN", locale);
  const path = pathname.slice(0, split);
  let publicKey: string;
  try { publicKey = decodeURIComponent(url.username); } catch { throw publicError("invalidDSN", locale); }
  // Header injection is rejected rather than handed to a native error that might reveal the key.
  if (/[\s,\x00-\x1f\x7f]/.test(publicKey)) throw publicError("invalidDSN", locale);
  const safe = new URL(url.origin + "/");
  safe.pathname = `${path}/${project}`;
  const endpoint = new URL(url.origin + "/");
  endpoint.pathname = `${path}/api/${project}/envelope/`;
  return { origin: url.origin, path, project, publicKey, safe: safe.href, endpoint: endpoint.href };
}
function decodePath(value: string, locale?: string): string {
  try { return decodeURIComponent(value); } catch { throw publicError("invalidDSN", locale); }
}
