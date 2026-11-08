import type * as Wire from "@persona/extension/wire";
import { GitHubError } from "./client.ts";
import { cut, repoOf, type Check, type Comment, type File, type Issue, type PageState, type Pull, type Repository } from "./model.ts";
import type { Annotation, Step, Content, GitHubService, ReviewThread } from "./service.ts";
import { eventText, type TimelineEvent } from "./thread.ts";
import { parseJSON, decimal, isInteger } from "./integers.ts";
export type Loaded = { kind: "loading" } | { kind: "error"; error: unknown } | { kind: "list"; items: (Issue | Repository | Record<string, unknown>)[]; more: boolean; organizations: string[] }
 | { kind: "detail"; issue: Issue; pull?: Pull; comments: Comment[]; reviews: Comment[]; files: File[]; checks: Check[]; threads: ReviewThread[]; viewed: Record<string, boolean>; pending?: Comment; diagnostics?: unknown[]; draftComments?: Comment[]; commits: { sha: string; commit: { message: string }; html_url?: string }[] }
 | { kind: "repo"; repository: Repository; items: Record<string, unknown>[]; readme: string; more: boolean }
 | { kind: "file"; text: string; sha: string; repo?: string; ref?: string } | { kind: "check"; check: Check; log: string; annotations?: Annotation[]; steps?: Step[]; diagnostics?: unknown[] } | { kind: "write" };
export function readState(raw?: unknown): PageState {
  let input: Record<string, unknown>; try { const parsed = typeof raw === "string" ? parseJSON(raw) : raw; input = parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed as Record<string, unknown> : {}; } catch { input = {}; }
  const state: PageState = {};
  for (const name of ["v", "t", "q", "f", "r", "ref", "p", "o", "w", "b", "e", "s", "head"] as const) if (typeof input[name] === "string") state[name] = input[name].slice(0, name === "b" ? 8000 : 2000);
  for (const name of ["n", "id"] as const) if (isInteger(input[name]) && input[name] >= 0) state[name] = input[name];
  for (const name of ["pg", "l", "start", "dp", "fp", "authEpoch"] as const) if (typeof input[name] === "number" && Number.isSafeInteger(input[name]) && input[name] >= 0) state[name] = input[name];
  for (const name of ["pr", "src", "pv"] as const) if (typeof input[name] === "boolean") state[name] = input[name];
  state.pg = Math.max(1, Math.min(state.pg ?? 1, 10)); return state;
}
export function transition(state: PageState, action: string, values: Record<string, string> = {}): PageState {
  const [kind, ...parts] = action.split(":"), arg = parts.join(":");
  switch (kind) {
    case "tab": return { ...state, ...(state.v === "diff" ? { v: undefined, p: undefined } : {}), t: values.t ?? arg, q: undefined, f: undefined, pg: 1, dp: 0, fp: 0, o: undefined };
    case "search": return { ...state, q: values.q ?? "", pg: 1 };
    case "filter": return { ...state, f: values.f ?? "", pg: 1 };
    case "more": return { ...state, pg: Math.min(10, (state.pg ?? 1) + 1) };
    case "refresh": return state;
    case "repo": return { v: "repo", r: arg, t: "code", pg: 1 };
    case "thread": { const m = /^([^#]+)#(\d+)$/.exec(arg); return m ? { r: m[1], n: decimal(m[2]!), t: "overview", pg: 1 } : state; }
    case "src": return { ...state, src: !state.src };
    case "edit_preview": return { ...state, pv: false, o: undefined };
    case "directory": return { ...state, v: "repo", n: undefined, p: values.path ?? arg, t: "code" };
    case "source": return { ...state, v: "source", p: arg || values.path };
    case "file": return { ...state, v: values.kind === "dir" ? "repo" : "file", p: values.path ?? arg };
    case "branch": return { ...state, v: "repo", t: "code", ref: arg || values.ref, p: "" };
    case "load_diff": return { ...state, o: values.path ?? arg };
    case "diff": return { ...state, v: "diff", p: arg || values.path, dp: 0 };
    case "diff_next": return { ...state, dp: (state.dp ?? 0) + 1 };
    case "diff_previous": return { ...state, dp: Math.max(0, (state.dp ?? 0) - 1) };
    case "files_next": return { ...state, fp: (state.fp ?? 0) + 1, o: undefined };
    case "files_previous": return { ...state, fp: Math.max(0, (state.fp ?? 0) - 1), o: undefined };
    case "check": return { ...state, v: "check", id: decimal(arg) };
    case "edit": return { ...state, v: "write", w: arg, b: values.body ?? "", e: values.event ?? "COMMENT", p: values.path ?? state.p, l: Number(values.line ?? 0), start: Number(values.start_line ?? 0), s: values.side ?? "new", id: decimal(values.id ?? "0"), pv: false, head: ["line", "draft"].includes(arg) ? state.head : undefined };
    case "cancel": return { r: state.r, n: state.n, t: "discussion", pg: 1 };
    case "back": return {};
    default: return state;
  }
}
export async function loadPage(service: GitHubService, state: PageState, signal?: AbortSignal, locale = "en"): Promise<Loaded> {
  try {
    if (state.v === "write") return { kind: "write" };
    if (state.v === "source") {
      const pull = await service.pull(state.r!, state.n!, signal), content = await service.content(pull.head.repo?.full_name ?? state.r!, state.p!, pull.head.sha, signal);
      if (Array.isArray(content)) throw new GitHubError("Expected file source", "invalid_params");
      return { kind: "file", text: service.decodeFile(content), sha: pull.head.sha, repo: pull.head.repo?.full_name ?? state.r, ref: pull.head.sha };
    }
    if (state.v === "file") { const c = await service.content(state.r!, state.p!, state.ref, signal); if (Array.isArray(c)) throw new GitHubError("Expected file", "invalid_params"); return { kind: "file", text: service.decodeFile(c), sha: c.sha }; }
    if (state.v === "repo") {
      const repo = state.r!, repository = await service.client.json<Repository>(`/repos/${repo}`, "GET", undefined, signal), tab = state.t ?? "code";
      if (tab === "code") { const items = await service.content(repo, state.p ?? "", state.ref, signal); let readme = ""; if (!state.p) { try { const r = await service.client.json<Content>(`/repos/${repo}/readme${state.ref ? `?ref=${encodeURIComponent(state.ref)}` : ""}`, "GET", undefined, signal); readme = service.decodeFile(r); } catch {} } return { kind: "repo", repository, items: (Array.isArray(items) ? items : [items]) as unknown as Record<string, unknown>[], readme, more: false }; }
      const endpoint = tab === "pulls" ? "pulls" : tab === "issues" ? "issues" : tab === "branches" ? "branches" : "commits";
      const items: Record<string, unknown>[] = []; let more = false;
      for (let page = 1; page <= (state.pg ?? 1); page++) { const out = await service.client.json<Record<string, unknown>[]>(`/repos/${repo}/${endpoint}?per_page=50&page=${page}${["pulls", "issues"].includes(tab) ? "&state=" + (state.f === "closed" ? "closed" : "open") : state.ref ? "&sha=" + encodeURIComponent(state.ref) : ""}`, "GET", undefined, signal); items.push(...out); more = out.length === 50; if (!more) break; }
      return { kind: "repo", repository, items, readme: "", more };
    }
    if (state.v === "check") return { kind: "check", ...await service.checkDetail(state.r!, state.id!, signal) };
    if (state.n) {
      const repo = state.r!, n = state.n, issue = await service.issue(repo, n, signal), pr = !!issue.pull_request;
      const wantsFiles = pr && (state.t === "files" || state.v === "diff");
      if (wantsFiles) service.client.invalidate(repo);
      const pull = pr ? await service.pull(repo, n, signal, wantsFiles) : undefined;
      const diagnostics: unknown[] = [];
      const optional = async <T,>(request: () => Promise<T>, fallback: T): Promise<T> => { try { return await request(); } catch (error) { signal?.throwIfAborted(); diagnostics.push(error); return fallback; } };
      const discussion = [undefined, "overview", "discussion"].includes(state.t);
      const comments = discussion ? await service.comments(repo, n, false, signal) : [], reviews = pr && discussion ? await service.reviews(repo, n, signal) : [];
      if (discussion) {
        if (pr) comments.push(...(await optional(() => service.comments(repo, n, true, signal), [])).map(c => ({ ...c, kind: "code" as const })));
        const events = await optional(() => service.client.newest<TimelineEvent>(`/repos/${repo}/issues/${n}/events`, signal), []);
        for (const event of events) {
          if (event.event === "closed" && events.some(e => e.event === "merged" && Math.abs(Date.parse(e.created_at) - Date.parse(event.created_at)) <= 1000)) continue;
          const body = eventText(event, locale); if (body) comments.push({ id: -event.id, user: event.actor ?? { login: "" }, body, created_at: event.created_at, state: "event" });
        }
      }
      const files = wantsFiles ? await service.files(repo, n, signal) : [], checks = pull && (state.t === "checks" || discussion) ? await optional(() => service.checks(repo, pull.head.sha, signal), []) : [];
      if (wantsFiles && (await service.pull(repo, n, signal, true)).head.sha !== pull?.head.sha) throw new GitHubError("Diff changed while loading", "head_changed");
      const threads = files.length ? await optional(() => service.threads(repo, n, signal), []) : [], viewed = files.length ? await optional(() => service.viewed(repo, n, signal), {}) : {}, pending = files.length ? await optional(() => service.pending(repo, n, signal), undefined) : undefined;
      const draftComments = pending ? await optional(() => service.client.pages<Comment>(`/repos/${repo}/pulls/${n}/reviews/${pending.id}/comments`, signal), []) : [];
      const commits = pr && state.t === "commits" ? await service.client.pages<{ sha: string; commit: { message: string }; html_url?: string }>(`/repos/${repo}/pulls/${n}/commits`, signal) : [];
      return { kind: "detail", issue, pull, comments, reviews, files, checks, threads, viewed, pending, draftComments, commits, diagnostics };
    }
    const result = await service.list({ ...state, t: state.t || "review" }, signal), organizations = state.t === "repos" ? await service.organizations(signal) : [];
    return { kind: "list", ...result, organizations };
  } catch (error) { signal?.throwIfAborted(); return { kind: "error", error }; }
}
export function hunks(patch = ""): Wire.Hunk[] {
  const result: Wire.Hunk[] = []; let current: Wire.Hunk | undefined;
  for (const line of patch.split("\n")) { const match = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/.exec(line); if (match) { current = { header: line, old_start: Number(match[1]), new_start: Number(match[2]), lines: [] }; result.push(current); } else if (current && /^[ +\\-]/.test(line)) current.lines.push(line); }
  return result;
}
export function patchPages(patch = "", budget = 24000): Wire.Hunk[][] {
  const pages: Wire.Hunk[][] = []; let page: Wire.Hunk[] = [], size = 0;
  for (const hunk of hunks(patch)) {
    let old = hunk.old_start, next = hunk.new_start, current: Wire.Hunk | undefined;
    for (const line of hunk.lines) {
      const length = new TextEncoder().encode(JSON.stringify(line)).length + 1;
      if (length > budget - 256) throw new GitHubError("A diff line exceeds the page budget; open the source file", "response_too_large");
      if (size + length > budget - 256 && page.length) { pages.push(page); page = []; size = 0; current = undefined; }
      if (!current) { current = { header: `@@ -${old} +${next} @@`, old_start: old, new_start: next, lines: [] }; page.push(current); size += 128; }
      current.lines.push(line); size += length;
      if (line[0] !== "+" && line[0] !== "\\") old++;
      if (line[0] !== "-" && line[0] !== "\\") next++;
    }
  }
  if (page.length) pages.push(page); return pages;
}
export const keyFor = (kind: string, value: string): string => { let hash = 2166136261; for (const c of value) hash = Math.imul(hash ^ c.charCodeAt(0), 16777619) >>> 0; return `${kind}-${hash.toString(16)}-${value.slice(-80)}`; };
