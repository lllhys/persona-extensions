import type * as Wire from "@persona/extension/wire";
import type { GitHubService } from "./service.ts";
import type { GitHubAuth } from "./auth.ts";
import { GitHubError } from "./client.ts";
import { cut, validRepo, type Comment, type Check, type User } from "./model.ts";
import { translations, errorText, type Key } from "./localization.ts";
import { decimal, type Integer } from "./integers.ts";
export interface TimelineEvent { id: Integer; event: string; actor: User | null; created_at: string; label?: { name: string }; assignee?: User; requested_reviewer?: User; requested_team?: { name: string }; rename?: { from: string; to: string } }
export function eventText(event: TimelineEvent, locale?: string): string {
  const t = translations(locale), key = event.event === "assigned" && event.actor?.login === event.assignee?.login ? "event_selfAssigned" : "event_" + event.event;
  if (!Object.hasOwn(t, key)) return "";
  const values: Record<string, string> = { actor: event.actor?.login || t.unknownActor, target: event.assignee?.login || event.requested_reviewer?.login || event.requested_team?.name || "", label: event.label?.name || "", from: event.rename?.from || "", to: event.rename?.to || "" };
  return t[key as Key].replace(/\{(\w+)\}/g, (_, name: string) => values[name] || "");
}
export function checkState(check: Check): string { return check.status !== "completed" ? "pending" : ["success", "neutral"].includes(check.conclusion ?? "") ? "success" : ["failure", "error", "timed_out", "action_required", "startup_failure"].includes(check.conclusion ?? "") ? "failure" : check.conclusion === "cancelled" ? "cancelled" : "skipped"; }
/** Same legacy timeline IDs and cursor semantics; avatar refs remain cache-compatible. */
export class ThreadReader {
  private avatars = new Map<string, string>();
  constructor(readonly service: GitHubService, readonly auth: GitHubAuth) { service.client.onAuthenticationChange(() => this.avatars.clear()); }
  person(value: User | null): Wire.ConnectorThreadPerson {
    const user = value ?? { login: "" };
    const person: Wire.ConnectorThreadPerson = { id: user.login, name: user.login || translations(this.auth.locale).deletedUser };
    if (user.login && user.avatar_url) {
      const url = new URL(user.avatar_url), version = url.searchParams.get("v") || "0", reference = `avatar:${user.login}:${version}`;
      if (this.avatars.size >= 400) this.avatars.clear(); this.avatars.set(reference, user.avatar_url); person.avatar = reference;
    }
    return person;
  }
  async avatar(reference: string, signal?: AbortSignal): Promise<string> {
    const match = /^avatar:([A-Za-z0-9_.\[\]-]+):(\d+)$/.exec(reference); if (!match) throw new GitHubError("Unknown avatar reference", "invalid_params");
    let url = this.avatars.get(reference);
    if (!url) { const user = await this.service.client.json<User>(`/users/${encodeURIComponent(match[1]!)}`, "GET", undefined, signal); if (!user.avatar_url) throw new GitHubError("Avatar is not available", "not_found"); const parsed = new URL(user.avatar_url); parsed.searchParams.set("v", match[2]!); url = parsed.href; }
    const parsed = new URL(url); if (parsed.hostname !== "avatars.githubusercontent.com" || parsed.protocol !== "https:") throw new GitHubError("Untrusted avatar destination", "permission_denied"); parsed.searchParams.set("s", "96"); return parsed.href;
  }
  async timeline(repo: string, n: Integer, signal?: AbortSignal): Promise<{ chat: Wire.ConnectorThreadChat; messages: Wire.ConnectorThreadMessage[] }> {
    const issue = await this.service.issue(repo, n, signal), c = this.service.client, comments = await c.newest<Comment>(`/repos/${repo}/issues/${n}/comments`, signal), events = await c.newest<TimelineEvent>(`/repos/${repo}/issues/${n}/events`, signal);
    const mine = (u: User | null): boolean => !!u?.login && u.login.toLowerCase() === this.auth.status.account?.toLowerCase();
    const chat: Wire.ConnectorThreadChat = { id: `${repo}#${n}`, name: issue.title, kind: issue.pull_request ? "pr" : "issue", url: issue.html_url, state: issue.state, author: this.person(issue.user), labels: issue.labels, body: (issue.body ?? "").trim(), body_format: "markdown", ...(!issue.locked ? { reply: "github_comment" } : {}) };
    const messages: Wire.ConnectorThreadMessage[] = comments.map(cm => ({ id: "c" + cm.id, at: cm.created_at, sender: this.person(cm.user), mine: mine(cm.user), text: cm.body ?? "", format: "markdown", edited: !!cm.updated_at && cm.updated_at !== cm.created_at, url: cm.html_url }));
    const merged = events.filter(e => e.event === "merged").map(e => Date.parse(e.created_at));
    for (const event of events) {
      if (event.event === "closed" && merged.some(at => Math.abs(at - Date.parse(event.created_at)) <= 1000)) continue;
      const text = eventText(event, this.auth.locale); if (text) messages.push({ id: "e" + event.id, at: event.created_at, sender: this.person(event.actor), mine: mine(event.actor), event: true, text });
    }
    if (issue.pull_request) {
      const pull = await this.service.pull(repo, n, signal); chat.branch = { head: pull.head.ref, base: pull.base.ref }; chat.state = pull.merged ? "merged" : pull.draft && issue.state === "open" ? "draft" : issue.state;
      const reviews = await c.newest<Comment>(`/repos/${repo}/pulls/${n}/reviews`, signal), lines = await c.newest<Comment>(`/repos/${repo}/pulls/${n}/comments`, signal), byID = new Map(lines.map(cm => [cm.id, cm]));
      for (const review of reviews) { if (!review.state || review.state === "PENDING" || review.state === "COMMENTED" && !(review.body ?? "").trim()) continue; messages.push({ id: "r" + review.id, at: review.submitted_at || review.created_at, sender: this.person(review.user), mine: mine(review.user), kind: "review", review: review.state.toLowerCase(), text: review.body ?? "", format: "markdown", url: review.html_url }); }
      for (const cm of lines) {
        const parent = cm.in_reply_to_id ? byID.get(cm.in_reply_to_id) : undefined;
        messages.push({ id: "rc" + cm.id, at: cm.created_at, sender: this.person(cm.user), mine: mine(cm.user), kind: "code", text: cm.body ?? "", format: "markdown", edited: !!cm.updated_at && cm.updated_at !== cm.created_at, url: cm.html_url,
          code: { path: cm.path || "", line: cm.line || cm.original_line, ...(!parent ? { hunk: cm.diff_hunk?.split("\n").slice(-6).join("\n") } : {}) }, ...(parent ? { reply_to: { id: "rc" + parent.id, from: parent.user?.login ?? "", text: cut((parent.body ?? "").trim().split("\n")[0], 140) } } : {}) });
      }
      if (issue.state === "open" && pull.head.sha) {
        try {
          const checks = await this.service.checks(repo, pull.head.sha, signal); let logs = 0;
          for (const check of checks) {
            const state = checkState(check), extra: Wire.ConnectorThreadCheck = { name: check.name, state, url: check.html_url || check.details_url };
            let logFailure: string | undefined;
            if (state === "failure" && check.app?.slug === "github-actions" && logs++ < 3) { try { extra.log = (await c.jobLog(repo, check.id, signal)).slice(-8192).trim(); } catch (error) { signal?.throwIfAborted(); logFailure = errorText(error, this.auth.locale); } }
            messages.push({ id: "ck" + check.id, at: check.completed_at || check.started_at || issue.created_at, sender: { name: check.app?.slug || "checks" }, kind: "check", check: extra, ...(logFailure ? { text: logFailure } : {}), url: check.html_url || check.details_url });
          }
        } catch (error) { signal?.throwIfAborted(); messages.push({ id: "checks-unavailable", at: issue.updated_at, sender: { name: "GitHub" }, event: true, text: errorText(error, this.auth.locale) }); }
      }
    }
    return { chat, messages: messages.sort((a, b) => a.at.localeCompare(b.at)) };
  }
  async page(params: Wire.ConnectorThreadParams, signal?: AbortSignal): Promise<Wire.ConnectorThreadPage> {
    const epoch = this.auth.epoch;
    const match = /^([^#]+)#(\d+)$/.exec(params.thread); if (!match || !validRepo(match[1]!)) throw new GitHubError("Invalid thread reference", "invalid_params");
    const { chat, messages } = await this.timeline(match[1]!, decimal(match[2]!), signal);
    if (epoch !== this.auth.epoch) throw new GitHubError("Account changed during thread read", "auth_changed");
    let end = messages.length;
    if (params.before) { end = messages.findIndex(m => m.id === params.before); if (end < 0) throw new GitHubError("The thread cursor no longer exists; refresh the thread", "invalid_params"); }
    const start = Math.max(0, end - Math.max(1, Math.min(params.limit ?? 50, 100)));
    return { chat, messages: messages.slice(start, end), ...(start > 0 ? { before: messages[start]!.id } : {}) };
  }
}
