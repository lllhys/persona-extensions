import type * as Wire from "@persona/extension/wire";
import { GitHubError, bounds } from "./client.ts";
import { bytes, cut, repoOf, type Issue, type Search, type User, type Comment } from "./model.ts";
import type { GitHubAuth } from "./auth.ts";
import type { GitHubService } from "./service.ts";
import { translations } from "./localization.ts";
interface Cursor { since: string; threads: Record<string, string>; positions?: Record<string, { at: string; ids: string[] }> }
/** Date.parse normalizes impossible days; reject them before any cursor can reach HTTP or comparisons. */
export function validTimestamp(value: unknown): value is string {
  if (typeof value !== "string") return false;
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(?:Z|([+-])(\d{2}):(\d{2}))$/.exec(value);
  if (!m) return false;
  const [year, month, day, hour, minute, second] = m.slice(1, 7).map(Number), leap = year! % 4 === 0 && (year! % 100 !== 0 || year! % 400 === 0);
  const days = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  return month! >= 1 && month! <= 12 && day! >= 1 && day! <= days[month! - 1]! && hour! < 24 && minute! < 60 && second! < 60 && (!m[7] || Number(m[8]) < 24 && Number(m[9]) < 60) && Number.isFinite(Date.parse(value));
}
const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && !Array.isArray(value);
export class ArchiveReader {
  constructor(readonly service: GitHubService, readonly auth: GitHubAuth, readonly now = Date.now, readonly locale = "en") {}
  async messages(issue: Issue, repo: string, after = "", signal?: AbortSignal, position?: { at: string; ids: string[] }): Promise<Wire.ConnectorArchiveMessage[]> {
    const result: Wire.ConnectorArchiveMessage[] = [], t = translations(this.locale);
    const add = (id: string, user: User | null, at: string, text: string | null) => { const login = user?.login ?? ""; if ((!after || at > after || position?.at === at && !position.ids.includes(id)) && (text ?? "").trim()) result.push({ id, sender_id: login, sender_name: login || t.deletedUser, mine: !!login && login.toLowerCase() === this.auth.status.account?.toLowerCase(), type: "TEXT", text: cut((text ?? "").trim(), 4000), at, sender_handles: [{ kind: "github", value: login }] }); };
    add("body", issue.user, issue.created_at, `【${issue.title}】\n${issue.body ?? ""}`);
    const read = async (path: string, accept: (comment: Comment) => void, supportsSince = true) => {
      // Re-read the cursor second to retain distinct comments sharing a timestamp.
      const since = supportsSince && after ? "&since=" + encodeURIComponent(new Date(Date.parse(after) - 1000).toISOString()) : "";
      for (let page = 1; page <= 200; page++) {
        const comments = await this.service.client.json<Comment[]>(`${path}?per_page=50&page=${page}${since}`, "GET", undefined, signal);
        for (const comment of comments) accept(comment);
        if (comments.length < 50) return;
      }
      throw new GitHubError("Archive pagination is incomplete; the cursor was not advanced", "response_too_large");
    };
    await read(`/repos/${repo}/issues/${issue.number}/comments`, comment => add(`c${comment.id}`, comment.user, comment.created_at, comment.body));
    if (issue.pull_request) {
      await read(`/repos/${repo}/pulls/${issue.number}/reviews`, review => { const verb = review.state === "APPROVED" ? t.reviewApproved : review.state === "CHANGES_REQUESTED" ? t.reviewChanges : review.state === "COMMENTED" && (review.body ?? "").trim() ? t.reviewed : ""; if (verb) add(`r${review.id}`, review.user, review.submitted_at || review.created_at, `(review) ${verb}${(review.body ?? "").trim() ? ": " + (review.body ?? "").trim() : ""}`); }, false);
      await read(`/repos/${repo}/pulls/${issue.number}/comments`, comment => add(`i${comment.id}`, comment.user, comment.created_at, `(${comment.path ?? ""}${comment.line ? ":" + comment.line : ""})${comment.body ?? ""}`));
    }
    return result.sort((a, b) => a.at!.localeCompare(b.at!));
  }
  async changes(cursor = "", limit = 50, signal?: AbortSignal): Promise<Wire.ConnectorChangesResult> {
    if (this.auth.status.state !== "ok" || this.service.client.low()) return { batches: [], next: cursor, more: false };
    let cur: Cursor;
    try { cur = cursor ? JSON.parse(cursor) as Cursor : { since: new Date(this.now() - 30 * 86400_000).toISOString(), threads: {} }; }
    catch { throw new GitHubError("Invalid archive cursor", "invalid_params"); }
    if (!record(cur) || !validTimestamp(cur.since) || cur.threads != null && (!record(cur.threads) || !Object.values(cur.threads).every(validTimestamp)) || cur.positions !== undefined && (!record(cur.positions) || !Object.values(cur.positions).every(p => record(p) && validTimestamp(p.at) && Array.isArray(p.ids) && p.ids.every(id => typeof id === "string")))) throw new GitHubError("Invalid archive cursor", "invalid_params");
    cur.threads ??= {};
    const result = await this.service.client.json<Search<Issue>>(`/search/issues?q=${encodeURIComponent("involves:@me updated:>=" + cur.since)}&sort=updated&order=asc&per_page=${Math.max(1, Math.min(limit, 100))}`, "GET", undefined, signal);
    const batches: Wire.ConnectorChatBatch[] = []; let size = 0, partial = false;
    for (const issue of result.items) {
      const repo = repoOf(issue), thread = `${repo}#${issue.number}`;
      try {
        const allMessages = await this.messages(issue, repo, cur.threads[thread] ?? "", signal, cur.positions?.[thread]), [owner, name] = repo.split("/");
        const chat: Wire.ConnectorArchiveChat = { id: `${owner}-${name}-${issue.number}`, name: `${name}#${issue.number} ${issue.title}`, kind: issue.pull_request ? "pr" : "issue", updated_at: issue.updated_at };
        const messages: Wire.ConnectorArchiveMessage[] = []; let encodedSize = bytes(JSON.stringify(chat)) + 256;
        for (const message of allMessages) { const length = bytes(JSON.stringify(message)) + 1; if (size + encodedSize + length > bounds.rpc - (64 << 10)) break; messages.push(message); encodedSize += length; }
        if (messages.length) {
          batches.push({ chat, messages }); size += encodedSize; cur.threads[thread] = messages.at(-1)!.at!;
          const at = messages.at(-1)!.at!, ids = [...(cur.positions?.[thread]?.at === at ? cur.positions[thread]!.ids : []), ...messages.filter(m => m.at === at).map(m => m.id)];
          (cur.positions ??= {})[thread] = { at, ids: [...new Set(ids)] };
          if (messages.length < allMessages.length) { partial = true; break; }
        } else if (allMessages.length) { partial = true; break; }
        if (issue.updated_at > cur.since) cur.since = issue.updated_at;
      } catch { signal?.throwIfAborted(); partial = true; break; }
    }
    for (const [thread, at] of Object.entries(cur.threads)) if (this.now() - Date.parse(at) > 90 * 86400_000) { delete cur.threads[thread]; if (cur.positions) delete cur.positions[thread]; }
    // A partial thread never advances the cursor. Completed earlier batches remain importable.
    return { batches, next: JSON.stringify(cur), more: partial || result.total_count > result.items.length };
  }
}
