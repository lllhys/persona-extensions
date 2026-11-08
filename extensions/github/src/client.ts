import { ExtError } from "@persona/extension";
import type { Config, Fetch } from "./model.ts";
import { abort, bytes } from "./model.ts";
import { parseJSON, stringify, type Integer } from "./integers.ts";
export const bounds = { broker: 8 << 20, response: 7 << 20, logChunk: 1 << 20, log: 32 << 20, callback: 64 << 10, rpc: 7 << 20 } as const;
export class GitHubError extends ExtError { constructor(message: string, code = "error", readonly status = 0) { super(message, code); } }
export const scrub = (text: string): string => text.replace(/(?:gh[pousr]_[A-Za-z0-9_]+|github_pat_[A-Za-z0-9_]+|Bearer\s+\S+|https?:\/\/[^\s/@]+:[^\s/@]+@[^\s/]+)/gi, "[redacted]");
export interface TokenAccess { token(signal?: AbortSignal): Promise<string>; refresh(signal?: AbortSignal): Promise<string>; principal?(): string; }
/** Every request uses the immutable host fetch; there is no direct-network fallback. */
export class GitHubClient {
  readonly api: string; readonly web: string;
  private cache = new Map<string, { etag: string; raw: string; at: number; link: string }>();
  private logs = new Map<string, string>(); private generation = 0; private cacheRevision = 0;
  private repoRevisions = new Map<string, number>(); private authListeners = new Set<() => void>();
  get epoch(): number { return this.generation; }
  onAuthenticationChange(listener: () => void): () => void { this.authListeners.add(listener); return () => { this.authListeners.delete(listener); }; }
  resetAuthentication(): void { this.generation++; this.invalidate(); this.rateLow = false; this.rateReset = 0; for (const listener of this.authListeners) listener(); }
  private stamp(path: string): string { const repo = /\/repos\/([^/]+\/[^/?]+)(?:\/|\?|$)/.exec(path)?.[1]?.toLowerCase(); return `${this.cacheRevision}:${repo ? this.repoRevisions.get(repo) ?? 0 : 0}`; }
  rateLow = false; rateReset = 0;
  constructor(readonly fetch: Fetch, readonly config: Config, readonly access: TokenAccess, readonly now = Date.now) {
    const host = config.host || "github.com";
    if (host !== "github.com") throw new GitHubError("GitHub Enterprise is not supported by the existing public destination grants", "unsupported");
    this.web = `https://${host}`; this.api = "https://api.github.com";
  }
  private policy(): void { if (this.config.proxy) throw new GitHubError("The extension proxy requires a controlled broker proxy interface; requests are blocked, never sent directly", "proxy_unsupported"); }
  private url(path: string, external = false): URL {
    const url = new URL(path, this.api);
    if (url.protocol !== "https:" || url.username || url.password || (!external && url.origin !== this.api)) throw new GitHubError("Untrusted GitHub destination", "permission_denied");
    if (external && !["api.github.com", "avatars.githubusercontent.com"].includes(url.hostname) && !url.hostname.endsWith(".actions.githubusercontent.com") && !url.hostname.endsWith(".blob.core.windows.net")) throw new GitHubError("Untrusted media or log destination", "permission_denied");
    return url;
  }
  async send(path: string, method = "GET", body?: unknown, extra: HeadersInit = {}, signal?: AbortSignal, external = false): Promise<Response> {
    this.policy(); abort(signal);
    const generation = this.generation, principal = this.access.principal?.() ?? "";
    let url = this.url(path, external), token = await this.access.token(signal), refreshed = false;
    if (generation !== this.generation) throw new GitHubError("Authentication or request state changed before dispatch", "auth_changed");
    const headers = new Headers(extra);
    headers.set("Accept", "application/vnd.github+json"); headers.set("X-GitHub-Api-Version", "2022-11-28");
    const raw = body === undefined ? undefined : stringify(body);
    if (raw && bytes(raw) > bounds.response) throw new GitHubError("Request exceeds broker limit", "response_too_large");
    if (raw) headers.set("Content-Type", "application/json");
    for (let redirect = 0; redirect < 6; redirect++) {
      abort(signal); if (generation !== this.generation) throw new GitHubError("Authentication or request state changed before dispatch", "auth_changed");
      // An off-origin redirect is deliberately unauthenticated. Tokens never reach blob hosts or avatars.
      if (url.origin === this.api) headers.set("Authorization", `Bearer ${token}`); else headers.delete("Authorization");
      let response: Response;
      try { response = await this.fetch(url.href, { method, headers, body: raw, redirect: "manual", signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(30_000)]) : AbortSignal.timeout(30_000) }); }
      catch (e) { abort(signal); throw new GitHubError("Controlled GitHub request failed", "transport_error"); }
      if (generation !== this.generation) { await response.body?.cancel(); throw new GitHubError("Authentication changed during the request", "auth_changed"); }
      this.noteRate(response.headers);
      if (response.status === 401 && url.origin === this.api && !refreshed) {
        await response.body?.cancel(); token = await this.access.refresh(signal); refreshed = true;
        if (generation !== this.generation) throw new GitHubError("Account changed during credential refresh", "auth_changed");
        // Go also retried across accounts. This narrow safety guard only permits a confirmed 401 retry
        // when the refreshed credential is verified as the same principal; unknown effects never replay.
        if (method !== "GET" && method !== "HEAD") {
          if (!principal) throw new GitHubError("Cannot verify the approved write principal", "auth_changed");
          const identity = await this.fetch(this.api + "/user", { method: "GET", redirect: "error", headers: { Accept: "application/vnd.github+json", Authorization: `Bearer ${token}` }, signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(15_000)]) : AbortSignal.timeout(15_000) });
          if (!identity.ok) { await identity.body?.cancel(); throw new GitHubError("Cannot verify the refreshed write principal", "auth_changed"); }
          const user = parseJSON(await this.text(identity, 1 << 20)) as { login?: string };
          if (generation !== this.generation || typeof user.login !== "string" || user.login.toLowerCase() !== principal.toLowerCase()) throw new GitHubError("Refreshed credential belongs to a different write principal", "auth_changed");
        }
        redirect--; continue;
      }
      if ([301, 302, 303, 307, 308].includes(response.status)) {
        const next = response.headers.get("location"); await response.body?.cancel();
        if (!next || method !== "GET") throw new GitHubError("Unsupported GitHub redirect", "permission_denied");
        url = this.url(new URL(next, url).href, true); continue;
      }
      return response;
    }
    throw new GitHubError("Too many GitHub redirects", "transport_error");
  }
  noteRate(headers: Headers): void {
    if (!headers.has("x-ratelimit-limit") || !headers.has("x-ratelimit-remaining")) return;
    const limit = Number(headers.get("x-ratelimit-limit")), left = Number(headers.get("x-ratelimit-remaining"));
    if (limit > 0 && Number.isFinite(left)) { this.rateLow = left * 10 < limit; this.rateReset = Number(headers.get("x-ratelimit-reset")) * 1000; }
  }
  low(): boolean { return this.rateLow && this.now() < this.rateReset; }
  async text(response: Response, budget: number = bounds.response): Promise<string> { return new TextDecoder().decode(await this.readBytes(response, budget)); }
  async readBytes(response: Response, budget: number = bounds.response): Promise<Uint8Array> {
    if (Number(response.headers.get("content-length")) > budget) { await response.body?.cancel(); throw new GitHubError("GitHub response exceeds the bounded source runtime response budget", "response_too_large"); }
    if (!response.body) return new Uint8Array();
    const reader = response.body.getReader(), parts: Uint8Array[] = []; let length = 0;
    try { while (true) { const read = await reader.read(); if (read.done) break; length += read.value.length; if (length > budget) throw new GitHubError("GitHub response exceeds the bounded source runtime response budget", "response_too_large"); parts.push(read.value); } }
    finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
    const all = new Uint8Array(length); let offset = 0; for (const part of parts) { all.set(part, offset); offset += part.length; }
    return all;
  }
  async json<T>(path: string, method = "GET", body?: unknown, signal?: AbortSignal, conditional = method === "GET"): Promise<T> { return (await this.jsonResult<T>(path, method, body, signal, conditional)).value; }
  async jsonResult<T>(path: string, method = "GET", body?: unknown, signal?: AbortSignal, conditional = method === "GET"): Promise<{ value: T; link: string }> {
    this.policy(); abort(signal);
    const generation = this.generation, stamp = this.stamp(path);
    const old = conditional ? this.cache.get(path) : undefined;
    if (old && this.now() - old.at < 15_000) return { value: parseJSON(old.raw) as T, link: old.link };
    const response = await this.send(path, method, body, old ? { "If-None-Match": old.etag } : {}, signal);
    if (generation !== this.generation) { await response.body?.cancel(); throw new GitHubError("Request cache was invalidated; retry", "auth_changed"); }
    if (response.status === 304 && old) { old.at = this.now(); old.link = response.headers.get("link") || old.link; await response.body?.cancel(); return { value: parseJSON(old.raw) as T, link: old.link }; }
    if (!response.ok) { await response.body?.cancel(); throw new GitHubError(`GitHub returned HTTP ${response.status}`, response.status === 401 ? "not_signed_in" : response.status === 403 ? "no_permission" : "http_error", response.status); }
    const raw = await this.text(response); abort(signal); const link = response.headers.get("link") ?? "";
    if (generation !== this.generation) throw new GitHubError("Request cache was invalidated; retry", "auth_changed");
    const value = (response.status === 204 ? {} : parseJSON(raw)) as T; // Empty HTTP 200 JSON is invalid too; 204 intentionally has no representation.
    if (conditional && response.status !== 204 && response.headers.get("etag") && stamp === this.stamp(path)) {
      if (this.cache.size >= 400 || [...this.cache.values()].reduce((size, item) => size + bytes(item.raw), 0) + bytes(raw) > (16 << 20)) this.cache.clear();
      this.cache.set(path, { etag: response.headers.get("etag")!, raw, at: this.now(), link });
    }
    return { value, link };
  }
  async newest<T>(path: string, signal?: AbortSignal): Promise<T[]> {
    const page = (n: number) => this.jsonResult<T[]>(`${path}${path.includes("?") ? "&" : "?"}per_page=100&page=${n}`, "GET", undefined, signal);
    const first = await page(1); if (first.value.length < 100) return first.value;
    const lastLink = /<([^>]+)>;\s*rel="last"/.exec(first.link), url = lastLink ? new URL(lastLink[1]!) : undefined;
    const last = url?.origin === this.api ? Number(url.searchParams.get("page")) : 0;
    const result: T[] = last > 5 ? [] : [...first.value]; let size = bytes(stringify(result));
    const start = last > 5 ? Math.max(2, last - 4) : 2, end = last > 5 ? last : 5;
    for (let n = start; n <= end; n++) { const next = await page(n); size += bytes(stringify(next.value)); if (size > bounds.rpc) throw new GitHubError("Thread list exceeds source result budget", "response_too_large"); result.push(...next.value); if (next.value.length < 100) break; }
    return result.slice(-500);
  }
  invalidate(repo?: string): void {
    if (!repo) { this.cacheRevision++; this.cache.clear(); this.logs.clear(); this.repoRevisions.clear(); return; }
    const name = repo.toLowerCase(); this.repoRevisions.set(name, (this.repoRevisions.get(name) ?? 0) + 1);
    for (const key of this.cache.keys()) if (key.toLowerCase().includes(`/repos/${name}/`) || key.toLowerCase() === `/repos/${name}`) this.cache.delete(key);
    for (const key of this.logs.keys()) if (key.toLowerCase().startsWith(name + ":")) this.logs.delete(key);
  }
  async pages<T>(path: string, signal?: AbortSignal, max = 10): Promise<T[]> {
    const result: T[] = []; let budget = 0;
    for (let page = 1; page <= max; page++) {
      const items = await this.json<T[]>(`${path}${path.includes("?") ? "&" : "?"}per_page=100&page=${page}`, "GET", undefined, signal);
      if (!Array.isArray(items)) throw new GitHubError("Expected a GitHub list", "invalid_response");
      budget += bytes(stringify(items)); if (budget > bounds.rpc) throw new GitHubError("Paginated result requires a smaller page or cursor", "response_too_large");
      result.push(...items); if (items.length < 100) return result;
    }
    return result;
  }
  async graphQL<T>(query: string, variables: Record<string, unknown>, signal?: AbortSignal): Promise<T> {
    const out = await this.json<{ data: T; errors?: unknown[] }>("/graphql", "POST", { query, variables }, signal, false);
    if (out.errors?.length) throw new GitHubError("GitHub GraphQL request failed", "graphql_error"); return out.data;
  }
  async form(path: string, fields: Record<string, string>, signal?: AbortSignal): Promise<Record<string, unknown>> {
    this.policy(); abort(signal);
    let response: Response;
    try { response = await this.fetch(this.web + path, { method: "POST", redirect: "error", headers: { Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams(fields), signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(30_000)]) : AbortSignal.timeout(30_000) }); }
    catch { abort(signal); throw new GitHubError("Controlled GitHub device request failed", "transport_error"); }
    if (response.status >= 300 && ![400, 401].includes(response.status)) throw new GitHubError(`GitHub device request returned HTTP ${response.status}`);
    return parseJSON(await this.text(response, 1 << 20)) as Record<string, unknown>;
  }
  async jobLog(repo: string, job: Integer, signal?: AbortSignal): Promise<string> {
    abort(signal); const generation = this.generation, stamp = this.stamp(`/repos/${repo}/`), key = `${repo}:${job}`, old = this.logs.get(key); if (old !== undefined) return old;
    const log = await this.log(repo, job, signal), tail = log.slice(-64 * 1024); abort(signal);
    if (generation !== this.generation) throw new GitHubError("Log cache was invalidated; retry", "auth_changed");
    if (stamp === this.stamp(`/repos/${repo}/`)) { if (this.logs.size >= 400) this.logs.clear(); this.logs.set(key, tail); } return tail;
  }
  /** Range-capable logs retain the old 32 MiB read cap without any broker response exceeding 1 MiB.
   * Non-Range upstreams are an explicit capability failure, not a silently truncated successful log.
   */
  async log(repo: string, job: Integer, signal?: AbortSignal): Promise<string> {
    signal = signal ? AbortSignal.any([signal, AbortSignal.timeout(180_000)]) : AbortSignal.timeout(180_000);
    let all = "", total = 0, validator = ""; const decoder = new TextDecoder();
    for (let start = 0; start < bounds.log; start += bounds.logChunk) {
      const headers: Record<string, string> = { Range: `bytes=${start}-${start + bounds.logChunk - 1}`, "Accept-Encoding": "identity" };
      if (validator) headers["If-Range"] = validator;
      let response: Response;
      try { response = await this.send(`/repos/${repo}/actions/jobs/${job}/logs`, "GET", undefined, headers, signal); }
      catch (e) { abort(signal); throw new GitHubError("CI logs need controlled bounded Range reads; the broker/upstream did not complete one", "bounded_log_unsupported"); }
      if (response.status === 416 && start === total) return all;
      if (response.status === 200 && start === 0) {
        try { return await this.text(response, bounds.response); } catch { throw new GitHubError("The log server ignores Range; a controlled bounded log reader is required", "bounded_log_unsupported"); }
      }
      if (response.status !== 206) { await response.body?.cancel(); throw new GitHubError("The log server does not support safe bounded Range reads", "bounded_log_unsupported"); }
      const match = /^bytes (\d+)-(\d+)\/(\d+)$/.exec(response.headers.get("content-range") ?? "");
      if (!match || Number(match[1]) !== start || Number(match[2]) - start + 1 > bounds.logChunk) { await response.body?.cancel(); throw new GitHubError("Invalid bounded log range", "bounded_log_unsupported"); }
      const etag = response.headers.get("etag") ?? "";
      if (start > 0 && (!etag || etag !== validator)) { await response.body?.cancel(); throw new GitHubError("Log changed during bounded reads", "bounded_log_unsupported"); }
      validator = etag; total = Number(match[3]);
      if (total > bounds.log) { await response.body?.cancel(); throw new GitHubError("CI log exceeds the previous 32 MiB limit", "response_too_large"); }
      const chunk = await this.readBytes(response, bounds.logChunk);
      if (chunk.length !== Number(match[2]) - start + 1) throw new GitHubError("Incomplete bounded log range", "bounded_log_unsupported");
      all += decoder.decode(chunk, { stream: true });
      if (Number(match[2]) + 1 >= total) return all + decoder.decode();
      if (!validator) throw new GitHubError("Multi-range log reads need a stable validator", "bounded_log_unsupported");
    }
    return all;
  }
}
