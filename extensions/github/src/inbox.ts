import { decimal, type Integer } from "./integers.ts";
import { GitHubError } from "./client.ts";
import { cut, glob, validGlob, validRepo, type Config, type Issue, type Notification, type Options, type Thing, type User } from "./model.ts";
import { translations } from "./localization.ts";
import type { GitHubService } from "./service.ts";
import type { GitHubAuth } from "./auth.ts";
export const classify = (n: Notification): string => ["review_requested", "mention", "team_mention", "assign"].includes(n.reason) ? n.reason : n.reason === "author" && ["PullRequest", "Issue"].includes(n.subject.type) ? "author" : n.reason === "ci_activity" && (n.subject.type === "CheckSuite" || !n.subject.url) ? "ci_failed" : "";
export const parseCI = (title: string): { workflow: string; status: string; branch: string } | undefined => { const m = /^(.+) workflow run(?:, Attempt #\d+)? (.+?) for (.+) branch$/.exec(title.trim()); return m ? { workflow: m[1]!, status: m[2]!.toLowerCase(), branch: m[3]! } : undefined; };
export const isBot = (user?: User | null): boolean => user?.type === "Bot" || !!user?.login.endsWith("[bot]");
interface CIMeta { repo: string; branch: string; workflows: Map<string, { at: string; failed: boolean }>; pr: Integer; checked: number; isDefault: boolean }
const pendingCI = (meta: CIMeta): boolean => [...meta.workflows.values()].some(w => w.failed);
export class InboxPoller {
  private things = new Map<string, Thing>(); private seen = new Map<string, string>();
  private lastModified = ""; private fullAt = 0; private active?: Promise<number>;
  private ci = new Map<string, CIMeta>();
  private current(epoch: number): void { if (epoch !== this.auth.epoch) throw new GitHubError("Account changed during polling", "auth_changed"); }
  private reset(): void { this.things.clear(); this.seen.clear(); this.ci.clear(); this.lastModified = ""; this.fullAt = 0; this.active = undefined; }
  options: Options = { ci: "mine", muted_repos: [], ignore_branches: ["train/*"] };
  constructor(readonly service: GitHubService, readonly auth: GitHubAuth, readonly config: Config, readonly now = Date.now, readonly locale = "en") { service.client.onAuthenticationChange(() => this.reset()); }
  lets(repo: string, kind: string): boolean { const f = this.config.inbox; return !(f?.skip_kinds ?? []).includes(kind) && (!(f?.repos?.length) || f.repos.some(p => glob(p.toLowerCase(), repo.toLowerCase()))) && !(f?.exclude_repos ?? []).some(p => glob(p.toLowerCase(), repo.toLowerCase())); }
  muted(repo: string): boolean { return (this.options.muted_repos ?? []).some(r => r.toLowerCase() === repo.toLowerCase()); }
  setOptions(value: Options): void {
    if (!value || ![undefined, "", "mine", "default_branch", "all", "off"].includes(value.ci) || ![value.muted_repos, value.ignore_branches].every(v => v === undefined || Array.isArray(v) && v.length <= 200 && v.every(i => typeof i === "string"))) throw new GitHubError("Invalid inbox options", "invalid_params");
    if (value.muted_repos?.some(r => !validRepo(r)) || value.ignore_branches?.some(p => !p || !validGlob(p))) throw new GitHubError("Invalid repository or branch pattern", "invalid_params");
    this.options = { ci: value.ci || "mine", muted_repos: value.muted_repos ?? [], ignore_branches: value.ignore_branches ?? ["train/*"] }; this.seen.clear(); this.fullAt = 0;
    for (const [id, thing] of this.things) { const c = this.ci.get(id); if (this.muted(thing.tags?.find(t => t.key === "repo")?.value ?? "") || c && (this.options.ci === "off" || this.options.ci === "default_branch" && !c.isDefault || this.options.ignore_branches!.some(p => glob(p, c.branch)))) { thing.done = "read"; thing.why = translations(this.locale).optionsMuted; } }
  }
  collect(): Thing[] { return [...this.things.values()].map(t => ({ ...t, rules: this.config.triage_rules, extra: { ...t.extra, rule_tier: t.tier && ["action", "fyi", "noise"].includes(t.tier) ? t.tier : "fyi" } })).sort((a, b) => (a.at ?? "").localeCompare(b.at ?? "")); }
  settled(threads: string[]): Record<string, string> {
    const out: Record<string, string> = {}, t = translations(this.locale);
    for (const id of threads) {
      const ci = /^ci:([^@]+)@(.+)$/.exec(id), subject = /^subject:([^@]+)@/.exec(id);
      const repo = ci?.[1] ?? subject?.[1] ?? id.split("#")[0] ?? id;
      const excluded = id.startsWith("notification:") || !!ci && (this.options.ci === "off" || this.options.ignore_branches?.some(p => glob(p, ci[2]!))) || this.muted(repo);
      const known = this.things.get(id);
      if (excluded) out[id] = id.startsWith("notification:") ? t.legacyNotice : ci && this.options.ignore_branches?.some(p => glob(p, ci[2]!)) ? t.ignoredCI : ci && this.options.ci === "off" ? t.ciDisabled : t.repoMuted;
      else if (known?.done || known?.read) out[id] = known.why || t.readElsewhere;
    }
    return out;
  }
  replied(thread: string): void { const t = this.things.get(thread); if (t) t.done = "replied"; }
  poll(signal?: AbortSignal): Promise<number> { if (this.active) return this.active; const request = this.read(signal).finally(() => { if (this.active === request) this.active = undefined; }); return this.active = request; }
  private async read(signal?: AbortSignal): Promise<number> {
    if (this.auth.status.state !== "ok") return 60_000;
    const epoch = this.auth.epoch, c = this.service.client, full = !this.fullAt || this.now() - this.fullAt >= 600_000;
    let response = await c.send("/notifications?per_page=50", "GET", undefined, !full && this.lastModified ? { "If-Modified-Since": this.lastModified } : {}, signal);
    const wait = Math.max(60, Number(response.headers.get("x-poll-interval")) || 60) * 1000;
    if (response.status === 304) { await response.body?.cancel(); return wait; }
    if (!response.ok) { await response.body?.cancel(); throw new GitHubError(`Notifications HTTP ${response.status}`, "http_error"); }
    this.current(epoch); const modified = response.headers.get("last-modified") ?? "";
    const notifications: Notification[] = []; let whole = true;
    for (let page = 1; page <= 10; page++) {
      const raw = await c.text(response); this.current(epoch); notifications.push(...JSON.parse(raw) as Notification[]);
      const next = /<([^>]+)>;\s*rel="next"/.exec(response.headers.get("link") ?? ""); if (!next) break;
      if (page === 10 || c.low() || !next[1]!.startsWith(c.api + "/")) { whole = false; break; }
      try { response = await c.send(next[1]!, "GET", undefined, {}, signal); if (!response.ok) { whole = false; await response.body?.cancel(); break; } } catch { whole = false; break; }
    }
    this.current(epoch); this.lastModified = modified; if (full) this.fullAt = this.now();
    const unread = new Set<string>(), t = translations(this.locale); let budget = c.low() ? 0 : 30, moved = false;
    for (const n of notifications) {
      const kind = classify(n), repo = n.repository.full_name;
      const match = /\/repos\/([^/]+\/[^/]+)\/(?:issues|pulls)\/(\d+)$/.exec(n.subject.url), run = kind === "ci_failed" ? parseCI(n.subject.title) : undefined;
      const thread = run ? `ci:${repo}@${run.branch}` : match ? `${match[1]}#${match[2]}` : `subject:${repo}@${n.id}`;
      // Configuration affects future collection, not whether existing host inbox rows are settled.
      unread.add(thread);
      if (!kind || this.muted(repo) || !this.lets(repo, kind)) continue;
      if (kind === "ci_failed" && (!run || this.options.ci === "off" || this.options.ignore_branches?.some(p => glob(p, run.branch)))) continue;
      if (this.seen.get(n.id) === n.updated_at || budget <= 0) continue; budget--;
      if (run && !["failed", "timed out", "failure"].includes(run.status)) {
        const meta = this.ci.get(thread), previous = meta?.workflows.get(run.workflow);
        if (run.status === "succeeded" && meta && previous && n.updated_at >= previous.at) {
          meta.workflows.set(run.workflow, { at: n.updated_at, failed: false });
          if (!pendingCI(meta) && this.things.has(thread)) { Object.assign(this.things.get(thread)!, { done: "read", why: t.laterSuccess }); moved = true; }
        }
        this.seen.set(n.id, n.updated_at); continue;
      }
      try {
        let thing: Thing = { thread, kind, title: n.subject.title, preview: n.subject.title, at: n.updated_at, last: n.updated_at, url: n.repository.html_url, label: kind === "review_requested" ? t.reviewRequested : kind === "mention" ? t.mention : kind === "team_mention" ? t.teamMention : kind === "assign" ? t.assign : kind === "ci_failed" ? t.ciFailed : t.author, tags: [{ key: "repo", value: repo.toLowerCase(), name: repo }] };
        if (run) {
          const repository = await c.json<{ default_branch: string }>(`/repos/${repo}`, "GET", undefined, signal); let pr: Integer = 0;
          if (run.branch !== repository.default_branch) {
            if (this.options.ci === "default_branch") { this.seen.set(n.id, n.updated_at); continue; }
            if (this.options.ci === "mine") { const pulls = await c.json<Issue[]>(`/repos/${repo}/pulls?head=${encodeURIComponent(repo.split("/")[0] + ":" + run.branch)}&state=open`, "GET", undefined, signal); pr = pulls.find(p => p.user?.login.toLowerCase() === this.auth.status.account?.toLowerCase())?.number ?? 0; }
          }
          thing = { ...thing, sender: "GitHub Actions", title: `${run.branch}: ${t.ciFailed}`, url: `${n.repository.html_url}/actions?query=${encodeURIComponent("branch:" + run.branch)}`, tier: run.branch === repository.default_branch || pr || this.options.ci === "all" ? "action" : "fyi", why: run.branch === repository.default_branch ? t.ciDefault : pr ? t.ciMine : this.options.ci === "all" ? t.ciAll : t.ciOther };
          this.current(epoch);
          const workflows = this.ci.get(thread)?.workflows ?? new Map<string, { at: string; failed: boolean }>(), previous = workflows.get(run.workflow);
          if (previous && previous.at > n.updated_at) { this.seen.set(n.id, n.updated_at); continue; }
          workflows.set(run.workflow, { at: n.updated_at, failed: true });
          thing.tags!.push({ key: "branch", value: run.branch, name: run.branch }); this.ci.set(thread, { repo, branch: run.branch, workflows, pr, checked: this.now(), isDefault: run.branch === repository.default_branch });
        } else if (match) {
          const number = decimal(match[2]!), isPR = n.subject.type === "PullRequest", issue = await this.service.issue(repo, number, signal), pull = isPR ? await this.service.pull(repo, number, signal) : undefined;
          const comments = await this.service.recentComments(repo, number, issue.comments ?? 0, signal), reviews = isPR ? await this.service.reviews(repo, number, signal) : [];
          const moves = [...comments, ...reviews.filter(r => r.state !== "PENDING" && (r.state !== "COMMENTED" || (r.body ?? "").trim()))].sort((a, b) => (a.submitted_at || a.created_at).localeCompare(b.submitted_at || b.created_at)), last = moves.at(-1), bot = isBot(last?.user ?? issue.user);
          thing.title = issue.title; thing.url = issue.html_url; thing.sender = last ? last.user?.login ?? "" : issue.user?.login ?? ""; thing.preview = cut((last?.body ?? issue.body ?? "").trim().split("\n")[0], 140);
          thing.context = `${repo}#${number} ${issue.title}\n${cut(issue.body, 1500)}\n${moves.slice(-8).map(m => `${m.submitted_at || m.created_at} ${m.user?.login ?? ""} ${m.state ?? ""}: ${cut(m.body, 600)}`).join("\n")}`;
          thing.can_reply = true; thing.reply_to = `${repo}#${number}`;
          if (issue.state === "closed" || pull?.merged) { thing.done = "read"; thing.why = t.prClosed; }
          else if (last?.user?.login.toLowerCase() === this.auth.status.account?.toLowerCase()) thing.done = "replied";
          else if (n.reason === "review_requested" && !(pull?.requested_reviewers ?? []).some(u => u.login.toLowerCase() === this.auth.status.account?.toLowerCase()) && !(pull as unknown as { requested_teams?: unknown[] })?.requested_teams?.length) { thing.done = reviews.some(r => r.user?.login === this.auth.status.account) ? "replied" : "read"; thing.why = t.reviewWithdrawn; }
          thing.tier = n.reason === "review_requested" ? isBot(issue.user) ? "fyi" : "action" : n.reason === "mention" ? bot ? "fyi" : "action" : n.reason === "assign" ? "action" : n.reason === "team_mention" || bot || last?.state === "APPROVED" ? "fyi" : last?.state === "CHANGES_REQUESTED" ? "action" : "";
          thing.why ||= bot ? t.bot : thing.tier === "action" ? t.needsYou : n.reason === "team_mention" ? t.teamFYI : "";
          thing.tags!.push({ key: "sender", value: thing.sender.toLowerCase(), name: thing.sender });
        } else { thing.tier = n.reason === "team_mention" ? "fyi" : "action"; thing.context = `${repo} ${n.subject.type}: ${n.subject.title}`; if (n.subject.type === "Discussion") thing.url += "/discussions"; else if (/\/commits\//.test(n.subject.url)) thing.url += "/commit/" + n.subject.url.split("/").at(-1); }
        this.current(epoch); this.things.set(thread, thing); this.seen.set(n.id, n.updated_at); moved = true;
      } catch { signal?.throwIfAborted(); this.current(epoch); }
    }
    for (const [thread, thing] of this.things) { if (this.now() - Date.parse(thing.at ?? "") > 8 * 86400_000) { this.things.delete(thread); this.ci.delete(thread); } else if (whole && !unread.has(thread) && !thing.done && !thing.read) { thing.read = true; moved = true; } }
    if (await this.followCI(signal, epoch)) moved = true; this.current(epoch);
    if (moved) this.auth.hub.notify("connector.changed", { contribution: "github", scope: "inbox" }); return wait;
  }
  private async followCI(signal?: AbortSignal, epoch = this.auth.epoch): Promise<boolean> {
    let count = 0, moved = false; const t = translations(this.locale), c = this.service.client;
    if (c.low()) return false;
    for (const [thread, meta] of this.ci) {
      const thing = this.things.get(thread); if (!thing || thing.done || this.now() - meta.checked < 600_000 || count++ >= 10) continue; meta.checked = this.now();
      try {
        if (meta.pr) { const pr = await this.service.pull(meta.repo, meta.pr, signal); this.current(epoch); if (pr.state === "closed" || pr.merged) { thing.done = "read"; thing.why = t.prClosed; moved = true; continue; } }
        const runs = await c.json<{ workflow_runs: { name: string; status: string; conclusion?: string }[] }>(`/repos/${meta.repo}/actions/runs?branch=${encodeURIComponent(meta.branch)}&per_page=20`, "GET", undefined, signal);
        this.current(epoch);
        for (const [workflow, state] of meta.workflows) {
          const latest = runs.workflow_runs.find(r => r.name === workflow);
          if (state.failed && latest?.status === "completed" && latest.conclusion === "success") state.failed = false;
        }
        if (!pendingCI(meta)) { thing.done = "read"; thing.why = t.laterSuccess; moved = true; continue; }
        await c.json(`/repos/${meta.repo}/branches/${encodeURIComponent(meta.branch)}`, "GET", undefined, signal);
      } catch (e) { signal?.throwIfAborted(); this.current(epoch); if (e instanceof GitHubError && e.status === 404) { thing!.done = "read"; thing!.why = t.branchDeleted; moved = true; } }
    }
    return moved;
  }
}
