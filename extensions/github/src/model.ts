import type { Hub } from "@persona/extension";
import { isInteger, type Integer } from "./integers.ts";
import type * as wire from "@persona/extension/wire";
export type Fetch = import("@persona/extension/ui").FetchAdapter;
export type Host = Pick<Hub, "call" | "notify" | "badge" | "invalidate">;
export interface Config { host?: string; proxy?: string; triage_rules?: string; auth?: { from_machine?: string }; inbox?: { repos?: string[]; exclude_repos?: string[]; skip_kinds?: string[] } }
export interface Options { ci?: "mine" | "default_branch" | "all" | "off"; muted_repos?: string[]; ignore_branches?: string[] }
export interface User { login: string; type?: string; avatar_url?: string }
export interface Issue { number: Integer; node_id?: string; title: string; state: string; body: string | null; user: User | null; html_url: string; repository_url?: string; created_at: string; updated_at: string; comments?: number; draft?: boolean; labels?: { name: string; color?: string }[]; locked?: boolean; assignees?: User[]; pull_request?: { merged_at?: string }; requested_reviewers?: User[] }
export interface Pull extends Issue { changed_files?: number; additions?: number; deletions?: number; commits?: number; mergeable_state?: string; milestone?: { title: string }; merged_by?: User; head: { sha: string; ref: string; repo?: { full_name: string } }; base: { sha: string; ref: string; repo?: { full_name: string } }; merged?: boolean; mergeable?: boolean; requested_reviewers?: User[] }
export interface Comment { kind?: "code" | "event" | "review"; id: Integer; node_id?: string; user: User | null; body: string | null; state?: string; created_at: string; submitted_at?: string; html_url?: string; path?: string; line?: number; original_line?: number; start_line?: number; side?: string; in_reply_to_id?: Integer; updated_at?: string; diff_hunk?: string }
export interface File { filename: string; previous_filename?: string; sha: string; status: string; patch?: string; additions: number; deletions: number }
export interface Check { id: Integer; name: string; status: string; conclusion?: string; html_url?: string; details_url?: string; app?: { slug: string }; started_at?: string; completed_at?: string; output?: { title?: string; summary?: string; text?: string } }
export interface Repository { full_name: string; name: string; html_url: string; description?: string; default_branch: string; owner: User; private?: boolean }
export interface Notification { id: string; reason: string; updated_at: string; subject: { title: string; url: string; type: string }; repository: { full_name: string; html_url: string } }
export interface Search<T> { total_count: number; items: T[]; incomplete_results?: boolean }
export interface PageState { authEpoch?: number; dp?: number; fp?: number; v?: string; t?: string; q?: string; f?: string; r?: string; n?: Integer; ref?: string; p?: string; pg?: number; o?: string; pr?: boolean; id?: Integer; src?: boolean; w?: string; b?: string; e?: string; l?: number; s?: string; pv?: boolean; head?: string; start?: number }
export type Thing = wire.ConnectorInboxThing;
export type Batch = wire.ConnectorChatBatch;
export function validRepo(repo: string): boolean { return /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repo) && repo.split("/").every(p => p !== "." && p !== ".."); }
export function repoOf(issue: Issue): string { return issue.repository_url?.split("/").slice(-2).join("/") ?? ""; }
export function ref(repo: string, n: Integer): string { if (!validRepo(repo) || !isInteger(n)) throw new Error("invalid repository or thread number"); return `/repos/${repo}`; }
export function cut(text: string | null | undefined, count: number): string { const chars = Array.from(text ?? ""); return chars.length <= count ? chars.join("") : chars.slice(0, count).join("") + "…"; }
export function clipBytes(text: string | null | undefined, budget: number): string {
  const value = text ?? "", encoded = new TextEncoder().encode(value); if (encoded.length <= budget) return value;
  let end = Math.max(0, budget - 3); while (end > 0 && (encoded[end]! & 0xc0) === 0x80) end--;
  return new TextDecoder().decode(encoded.slice(0, end)) + "…";
}
export function bytes(text: string): number { return new TextEncoder().encode(text).length; }
export const b64encode = (text: string): string => { let binary = ""; for (const b of new TextEncoder().encode(text)) binary += String.fromCharCode(b); return btoa(binary); };
export const b64decode = (text: string): string => new TextDecoder().decode(Uint8Array.from(atob(text), c => c.charCodeAt(0)));
export function abort(signal?: AbortSignal): void { signal?.throwIfAborted(); }
type GlobToken = { kind: "star" } | { kind: "any" } | { kind: "literal"; value: string } | { kind: "class"; negate: boolean; ranges: [number, number][] };
/** Go path.Match syntax; branch matching is case-sensitive. Repository callers fold explicitly. */
function globTokens(pattern: string): GlobToken[] | undefined {
  const chars = Array.from(pattern), tokens: GlobToken[] = []; let i = 0;
  const classChar = (): number | undefined => {
    let c = chars[i++]; if (c === undefined || c === "-" || c === "]") return undefined;
    if (c === "\\") c = chars[i++];
    return c !== undefined && i < chars.length ? c.codePointAt(0) : undefined;
  };
  while (i < chars.length) {
    const c = chars[i++]!;
    if (c === "*" || c === "?") tokens.push({ kind: c === "*" ? "star" : "any" });
    else if (c === "\\") { const literal = chars[i++]; if (literal === undefined) return undefined; tokens.push({ kind: "literal", value: literal }); }
    else if (c === "[") {
      const negate = chars[i] === "^"; if (negate) i++;
      const ranges: [number, number][] = [];
      while (!(chars[i] === "]" && ranges.length)) {
        const lo = classChar(); if (lo === undefined) return undefined;
        let hi = lo; if (chars[i] === "-") { i++; const end = classChar(); if (end === undefined) return undefined; hi = end; }
        ranges.push([lo, hi]);
      }
      i++; tokens.push({ kind: "class", negate, ranges });
    } else tokens.push({ kind: "literal", value: c });
  }
  return tokens;
}
export const validGlob = (pattern: string): boolean => globTokens(pattern) !== undefined;
export function glob(pattern: string, value: string): boolean {
  const tokens = globTokens(pattern); if (!tokens) return false;
  const chars = Array.from(value); let matches = Array<boolean>(chars.length + 1).fill(false); matches[0] = true;
  for (const token of tokens) {
    const next = Array<boolean>(chars.length + 1).fill(false);
    if (token.kind === "star") next[0] = matches[0]!;
    for (let j = 1; j <= chars.length; j++) {
      const c = chars[j - 1]!, cp = c.codePointAt(0)!;
      next[j] = token.kind === "star" ? matches[j]! || c !== "/" && next[j - 1]! : matches[j - 1]! && (token.kind === "any" ? c !== "/" : token.kind === "literal" ? c === token.value : token.ranges.some(([lo, hi]) => lo <= cp && cp <= hi) !== token.negate);
    }
    matches = next;
  }
  return matches[chars.length]!;
}
