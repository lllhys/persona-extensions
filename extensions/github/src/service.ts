import type { GitHubClient } from "./client.ts";
import { GitHubError } from "./client.ts";
import { bounded, decimal, isInteger, type Integer, type ReadArgs } from "./integers.ts";
import { errorText, translations } from "./localization.ts";
import { abort, b64decode, cut, ref, type Check, type Comment, type File, type Issue, type PageState, type Pull, type Repository, type Search } from "./model.ts";
export interface Content { name: string; path: string; type: string; sha: string; size?: number; content?: string; encoding?: string; html_url?: string }
export interface ReviewThread { id: string; isResolved: boolean; isOutdated: boolean; path: string; line: number; startLine?: number; diffSide: string; comments: { nodes: { databaseId: Integer; body: string; createdAt: string; author?: { login: string; avatarUrl?: string } }[] } }
export interface Annotation { path: string; start_line: number; title?: string; message: string; annotation_level: string }
export interface Step { number: Integer; name: string; status: string; conclusion?: string }
export class GitHubService {
  constructor(readonly client: GitHubClient) {}
  async list(s: PageState, signal?: AbortSignal): Promise<{ items: (Issue | Repository | Record<string, unknown>)[]; more: boolean }> {
    const count = Math.max(1, Math.min(s.pg ?? 1, 10)), items: (Issue | Repository | Record<string, unknown>)[] = [];
    let more = false;
    for (let page = 1; page <= count; page++) {
      if (s.t === "repos") {
        const result = s.q ? (await this.client.json<Search<Repository>>(`/search/repositories?q=${encodeURIComponent(s.q + (s.f ? ` org:${s.f}` : ""))}&per_page=50&page=${page}`, "GET", undefined, signal)).items
          : await this.client.json<Repository[]>(`${s.f ? `/orgs/${encodeURIComponent(s.f)}/repos` : "/user/repos"}?sort=updated&per_page=50&page=${page}`, "GET", undefined, signal);
        items.push(...result); more = result.length === 50;
      } else if (s.f === "notify" || s.t === "notify") {
        const result = await this.client.json<Record<string, unknown>[]>(`/notifications?all=true&per_page=50&page=${page}`, "GET", undefined, signal); items.push(...result); more = result.length === 50;
      } else if (s.f === "activity" || s.t === "activity") {
        const user = await this.client.json<{ login: string }>("/user", "GET", undefined, signal);
        const result = await this.client.json<Record<string, unknown>[]>(`/users/${encodeURIComponent(user.login)}/received_events?per_page=50&page=${page}`, "GET", undefined, signal); items.push(...result); more = result.length === 50;
      } else {
        let query = s.t === "mine" ? "is:pr is:open author:@me" : s.t === "involved" ? "is:open involves:@me -author:@me" : "is:pr is:open review-requested:@me";
        if (s.f && s.f !== "involved") query += ` repo:${s.f}`; if (s.q) query += " " + s.q;
        const result = await this.client.json<Search<Issue>>(`/search/issues?q=${encodeURIComponent(query)}&sort=updated&per_page=50&page=${page}`, "GET", undefined, signal); items.push(...result.items); more = result.total_count > page * 50;
      }
      if (!more) break;
    }
    return { items, more };
  }
  async organizations(signal?: AbortSignal): Promise<string[]> { return (await this.client.pages<{ login: string }>("/user/orgs", signal)).map(o => o.login); }
  issue(repo: string, number: Integer, signal?: AbortSignal): Promise<Issue> { return this.client.json(`${ref(repo, number)}/issues/${number}`, "GET", undefined, signal); }
  pull(repo: string, number: Integer, signal?: AbortSignal, fresh = false): Promise<Pull> { return this.client.json(`${ref(repo, number)}/pulls/${number}`, "GET", undefined, signal, !fresh); }
  comments(repo: string, n: Integer, pr: boolean, signal?: AbortSignal): Promise<Comment[]> { return this.client.newest(`${ref(repo, n)}/${pr ? "pulls" : "issues"}/${n}/comments`, signal); }
  recentComments(repo: string, n: Integer, count: number, signal?: AbortSignal): Promise<Comment[]> { return this.client.json(`${ref(repo, n)}/issues/${n}/comments?per_page=100&page=${Math.max(1, Math.ceil(count / 100))}`, "GET", undefined, signal); }
  reviews(repo: string, n: Integer, signal?: AbortSignal): Promise<Comment[]> { return this.client.pages(`${ref(repo, n)}/pulls/${n}/reviews`, signal); }
  files(repo: string, n: Integer, signal?: AbortSignal): Promise<File[]> { return this.client.pages(`${ref(repo, n)}/pulls/${n}/files`, signal, 30); }
  async checks(repo: string, sha: string, signal?: AbortSignal): Promise<Check[]> {
    ref(repo, 1); const result: Check[] = [];
    for (let page = 1; page <= 10; page++) { const out = await this.client.json<{ total_count: number; check_runs: Check[] }>(`/repos/${repo}/commits/${encodeURIComponent(sha)}/check-runs?per_page=100&page=${page}`, "GET", undefined, signal); result.push(...out.check_runs); if (result.length >= out.total_count || out.check_runs.length < 100) break; }
    const statuses = await this.client.json<{ statuses: { id: Integer; context: string; state: string; target_url?: string; description?: string }[] }>(`/repos/${repo}/commits/${encodeURIComponent(sha)}/status`, "GET", undefined, signal);
    result.push(...statuses.statuses.map(s => ({ id: s.id, name: s.context, status: s.state === "pending" ? "in_progress" : "completed", conclusion: s.state, html_url: s.target_url, output: { summary: s.description } }))); return result;
  }
  async checkDetail(repo: string, id: Integer, signal?: AbortSignal): Promise<{ check: Check; annotations: Annotation[]; steps: Step[]; log: string; diagnostics: unknown[] }> {
    ref(repo, 1); const check = await this.client.json<Check>(`/repos/${repo}/check-runs/${id}`, "GET", undefined, signal);
    let annotations: Annotation[] = [], steps: Step[] = [], log = ""; const diagnostics: unknown[] = [];
    try { annotations = await this.client.pages<Annotation>(`/repos/${repo}/check-runs/${id}/annotations`, signal); } catch (error) { abort(signal); diagnostics.push(error); }
    if (check.app?.slug === "github-actions") {
      try { const job = await this.client.json<{ steps?: Step[] }>(`/repos/${repo}/actions/jobs/${id}`, "GET", undefined, signal); steps = job.steps ?? []; } catch (error) { abort(signal); diagnostics.push(error); }
      if (["failure", "timed_out", "action_required", "startup_failure"].includes(check.conclusion ?? "")) { try { log = await this.client.jobLog(repo, id, signal); } catch (error) { abort(signal); diagnostics.push(error); } }
    }
    return { check, annotations, steps, log, diagnostics };
  }
  async rerunFailed(repo: string, n: Integer, signal?: AbortSignal): Promise<number> {
    const epoch = this.client.epoch;
    const pull = await this.pull(repo, n, signal, true), checks = await this.checks(repo, pull.head.sha, signal), runs = new Set<Integer>();
    for (const check of checks) if (check.app?.slug === "github-actions" && ["failure", "timed_out", "action_required", "startup_failure"].includes(check.conclusion ?? "")) {
      const job = await this.client.json<{ run_id: Integer }>(`/repos/${repo}/actions/jobs/${check.id}`, "GET", undefined, signal); if (job.run_id) runs.add(job.run_id);
    }
    for (const id of runs) { if (epoch !== this.client.epoch) throw new GitHubError("Account changed during rerun preparation", "auth_changed"); await this.client.json(`/repos/${repo}/actions/runs/${id}/rerun-failed-jobs`, "POST", {}, signal); }
    this.client.invalidate(repo); return runs.size;
  }
  async content(repo: string, path: string, branch = "", signal?: AbortSignal): Promise<Content | Content[]> {
    ref(repo, 1); if (path.split("/").some(p => p === ".." || p === ".")) throw new GitHubError("Invalid file path", "invalid_params");
    return this.client.json(`/repos/${repo}/contents/${path.split("/").map(encodeURIComponent).join("/")}${branch ? `?ref=${encodeURIComponent(branch)}` : ""}`, "GET", undefined, signal);
  }
  decodeFile(content: Content): string { if (content.encoding !== "base64" || !content.content || (content.size ?? 0) > 1 << 20) throw new GitHubError("File is binary or too big", "response_too_large"); const text = b64decode(content.content.replace(/\s/g, "")); if (text.includes("\0")) throw new GitHubError("File is binary", "unsupported"); return text; }
  async context(repo: string, n: Integer, path: string, side: string, from: number, to: number, signal?: AbortSignal): Promise<{ path: string; side: string; start: number; lines: string[]; total: number }> {
    if (!Number.isSafeInteger(from) || from < 1 || !Number.isSafeInteger(to) || to < from || to - from >= 200 || !["old", "new"].includes(side)) throw new GitHubError("Invalid or oversized context range", "invalid_params");
    const pull = await this.pull(repo, n, signal), source = side === "old" ? pull.base : pull.head;
    const content = await this.content(source.repo?.full_name ?? repo, path, source.sha, signal);
    if (Array.isArray(content)) throw new GitHubError("Context requires a file", "invalid_params");
    const allLines = this.decodeFile(content).replace(/\n$/, "").split("\n"), lines = allLines.slice(from - 1, to);
    const out = { path, side, start: from, lines, total: allLines.length };
    if (new TextEncoder().encode(JSON.stringify(out)).length > 48 << 10) throw new GitHubError("Context exceeds callback budget; request a smaller range", "response_too_large"); return out;
  }
  async threads(repo: string, n: Integer, signal?: AbortSignal): Promise<ReviewThread[]> {
    const [owner, name] = repo.split("/"), result: ReviewThread[] = []; let after: string | null = null;
    const query = `query($owner: String!, $name: String!, $n: Int!, $after: String) { repository(owner: $owner, name: $name) { pullRequest(number: $n) { reviewThreads(first: 100, after: $after) { pageInfo { hasNextPage endCursor } nodes { id isResolved isOutdated path line startLine diffSide comments(first: 100) { nodes { databaseId body createdAt author { login avatarUrl } } } } } } } }`;
    for (let page = 0; page < 30; page++) {
      const data: { repository: { pullRequest: { reviewThreads: { pageInfo: { hasNextPage: boolean; endCursor: string }; nodes: ReviewThread[] } } } } = await this.client.graphQL(query, { owner, name, n, after }, signal);
      const threads = data.repository.pullRequest.reviewThreads; result.push(...threads.nodes); if (!threads.pageInfo.hasNextPage) break; after = threads.pageInfo.endCursor;
    }
    return result;
  }
  async viewed(repo: string, n: Integer, signal?: AbortSignal): Promise<Record<string, boolean>> {
    const [owner, name] = repo.split("/"), result: Record<string, boolean> = {}; let after: string | null = null;
    const query = `query($owner: String!, $name: String!, $n: Int!, $after: String) { repository(owner: $owner, name: $name) { pullRequest(number: $n) { files(first: 100, after: $after) { pageInfo { hasNextPage endCursor } nodes { path viewerViewedState } } } } }`;
    for (let page = 0; page < 30; page++) { const data: { repository: { pullRequest: { files: { pageInfo: { hasNextPage: boolean; endCursor: string }; nodes: { path: string; viewerViewedState: string }[] } } } } = await this.client.graphQL(query, { owner, name, n, after }, signal); const files = data.repository.pullRequest.files; for (const f of files.nodes) result[f.path] = f.viewerViewedState === "VIEWED"; if (!files.pageInfo.hasNextPage) break; after = files.pageInfo.endCursor; }
    return result;
  }
  async write(repo: string, n: Integer, kind: string, values: Record<string, string>, head?: string, signal?: AbortSignal): Promise<unknown> {
    const base = ref(repo, n), body = (values.body ?? "").trim(), epoch = this.client.epoch; abort(signal);
    const current = () => { abort(signal); if (epoch !== this.client.epoch) throw new GitHubError("Account changed during write preparation", "auth_changed"); };
    const writeJSON = (path: string, method: string, payload?: unknown) => { current(); return this.client.json(path, method, payload, signal); };
    const writeGraphQL = (query: string, variables: Record<string, unknown>) => { current(); return this.client.graphQL(query, variables, signal); };
    let result: unknown;
    if (["comment", "line", "reply", "draft"].includes(kind) && !body) throw new GitHubError("Comment body is required", "invalid_params");
    switch (kind) {
      case "comment": result = await writeJSON(`${base}/issues/${n}/comments`, "POST", { body }); break;
      case "review": case "submit_review": {
        const event = values.event || "COMMENT", pending = kind === "submit_review" ? await this.pending(repo, n, signal) : undefined;
        if (!["APPROVE", "REQUEST_CHANGES", "COMMENT"].includes(event) || (event !== "APPROVE" && !body && !pending)) throw new GitHubError("Invalid review event or empty body", "invalid_params");
        result = await writeJSON(`${base}/pulls/${n}/reviews${pending ? `/${pending.id}/events` : ""}`, "POST", { body, event }); break;
      }
      case "merge": {
        const pull = await this.pull(repo, n, signal, true); if (pull.state !== "open" || pull.draft || pull.merged) throw new GitHubError("The pull request is not open for merging", "invalid_params"); if (!head || pull.head.sha !== head) throw new GitHubError("Merge preview is stale", "head_changed");
        const merge_method = values.event || "merge"; if (!["merge", "squash", "rebase"].includes(merge_method)) throw new GitHubError("Invalid merge method", "invalid_params");
        result = await writeJSON(`${base}/pulls/${n}/merge`, "PUT", { sha: head, merge_method, ...(body ? { commit_message: body } : {}) }); break;
      }
      case "line": {
        const line = Number(values.line), start = Number(values.start_line), side = values.side === "old" || values.side === "LEFT" ? "LEFT" : values.side === "new" || values.side === "RIGHT" ? "RIGHT" : "";
        if (!values.path || line <= 0 || !Number.isSafeInteger(line) || !side || (start && (!Number.isSafeInteger(start) || start > line))) throw new GitHubError("Invalid line comment", "invalid_params");
        const pull = await this.pull(repo, n, signal, true);
        if (head !== undefined && pull.head.sha !== head) throw new GitHubError("Line comment preview is stale", "head_changed");
        result = await writeJSON(`${base}/pulls/${n}/comments`, "POST", { body, path: values.path, line, side, commit_id: head ?? pull.head.sha, ...(start > 0 && start < line ? { start_line: start, start_side: side } : {}) }); break;
      }
      case "reply": { const id = decimal(values.id ?? "0"); if (!isInteger(id) || id <= 0) throw new GitHubError("Invalid comment id", "invalid_params"); result = await writeJSON(`${base}/pulls/${n}/comments/${id}/replies`, "POST", { body }); break; }
      case "draft": {
        const pull = await this.pull(repo, n, signal, true), pending = await this.pending(repo, n, signal), line = Number(values.line), start = Number(values.start_line), side = values.side === "old" || values.side === "LEFT" ? "LEFT" : values.side === "new" || values.side === "RIGHT" ? "RIGHT" : "";
        if (!values.path || !side || !Number.isSafeInteger(line) || line <= 0 || (start && (!Number.isSafeInteger(start) || start > line))) throw new GitHubError("Invalid draft lines", "invalid_params");
        if (head !== undefined && pull.head.sha !== head) throw new GitHubError("Draft preview is stale", "head_changed");
        result = await writeGraphQL(`mutation($in: AddPullRequestReviewThreadInput!) { addPullRequestReviewThread(input: $in) { thread { id } } }`, { in: { pullRequestId: pull.node_id, ...(pending?.node_id ? { pullRequestReviewId: pending.node_id } : {}), path: values.path, line, side, body, ...(start > 0 && start < line ? { startLine: start, startSide: side } : {}) } }); break;
      }
      case "discard_review": { const pending = await this.pending(repo, n, signal); result = pending ? await writeJSON(`${base}/pulls/${n}/reviews/${pending.id}`, "DELETE") : {}; break; }
      case "resolve": case "unresolve": {
        const threads = await this.threads(repo, n, signal), thread = threads.find(t => t.id === values.thread || t.comments.nodes[0]?.databaseId === decimal(values.id ?? "0")); if (!thread) throw new GitHubError("Review conversation not found", "invalid_params");
        result = await writeGraphQL(`mutation($id: ID!) { ${kind === "resolve" ? "resolveReviewThread" : "unresolveReviewThread"}(input: {threadId: $id}) { thread { id } } }`, { id: thread.id }); break;
      }
      case "mark_viewed": { const pull = await this.pull(repo, n, signal); if (!values.path) throw new GitHubError("File path is required", "invalid_params"); result = await writeGraphQL(`mutation($id: ID!, $path: String!) { ${values.viewed === "true" ? "markFileAsViewed" : "unmarkFileAsViewed"}(input: {pullRequestId: $id, path: $path}) { clientMutationId } }`, { id: pull.node_id, path: values.path }); break; }
      case "rerun": { const id = decimal(values.id ?? "0"); if (!isInteger(id) || id <= 0) throw new GitHubError("Invalid workflow run", "invalid_params"); const job = await this.client.json<{ run_id: Integer }>(`${base}/actions/jobs/${id}`, "GET", undefined, signal); if (!job.run_id) throw new GitHubError("Workflow run not found", "invalid_response"); result = await writeJSON(`${base}/actions/runs/${job.run_id}/rerun-failed-jobs`, "POST", {}); break; }
      default: throw new GitHubError("Unknown write", "unknown_method");
    }
    current(); this.client.invalidate(repo); return result;
  }
  async pending(repo: string, n: Integer, signal?: AbortSignal): Promise<Comment | undefined> { return (await this.reviews(repo, n, signal)).find(r => r.state === "PENDING"); }
  async readTool(name: string, a: Partial<ReadArgs>, signal?: AbortSignal, locale = "en"): Promise<string> {
    if (name === "github_search") {
      if (!a.query?.trim()) throw new GitHubError("Search query required", "invalid_params");
      const count = a.limit && a.limit > 0 && a.limit <= 50 ? a.limit : 20;
      const result = await this.client.json<Search<Issue & { path?: string; repository?: { full_name: string } }>>(`/search/${a.kind === "code" ? "code" : "issues"}?q=${encodeURIComponent(a.query)}&sort=updated&per_page=${count}`, "GET", undefined, signal);
      return `${result.total_count} results\n` + result.items.map(i => a.kind === "code" ? `${i.repository?.full_name} ${i.path} ${i.html_url}` : `${i.repository_url?.split("/").slice(-2).join("/")}#${i.number} [${i.pull_request ? "PR" : "issue"} ${i.state}] ${i.title} — ${i.user?.login || translations(locale).deletedUser}, ${i.updated_at}, ${i.comments} comments ${i.html_url}`).join("\n");
    }
    const repo = a.repo ?? "", n = a.number ?? 0; ref(repo, name === "github_checks" ? n || 1 : n);
    if (name === "github_read") {
      const issue = await this.issue(repo, n, signal), count = bounded(a.comments ?? 15, 0, 50);
      const comments: Comment[] = [];
      for (let page = Math.max(1, Math.ceil((issue.comments ?? 0) / 100)); count > 0 && page >= 1 && comments.length < count; page--) { const batch = await this.client.json<Comment[]>(`/repos/${repo}/issues/${n}/comments?per_page=100&page=${page}`, "GET", undefined, signal); comments.unshift(...batch); if (!batch.length) break; }
      const reviews = count && issue.pull_request ? await this.reviews(repo, n, signal) : [];
      const pull = issue.pull_request ? await this.pull(repo, n, signal) : undefined;
      const entries = [...comments, ...reviews.filter(r => r.state && r.state !== "PENDING" && (r.state !== "COMMENTED" || (r.body ?? "").trim()))].sort((a, b) => (b.submitted_at || b.created_at).localeCompare(a.submitted_at || a.created_at)).slice(0, count);
      return `${issue.pull_request ? "Pull request" : "Issue"} ${repo}#${n}: ${issue.title}\nState: ${pull?.merged ? "merged" : issue.draft ? "draft" : issue.state} · by ${issue.user?.login || translations(locale).deletedUser} · opened ${issue.created_at} · updated ${issue.updated_at}\n${issue.html_url}\n${issue.labels?.length ? "Labels: " + issue.labels.map(l => l.name).join(", ") + "\n" : ""}${issue.assignees?.length ? "Assignees: " + issue.assignees.map(u => u.login).join(", ") + "\n" : ""}${pull ? `Branch: ${pull.head.ref} → ${pull.base.ref} (head ${pull.head.sha.slice(0, 8)})\nReview requested from: ${pull.requested_reviewers?.map(u => u.login).join(", ") ?? ""}\n` : ""}${cut((issue.body ?? "").trim() || "(no description)", 4000)}\n` + (count ? `--- ${entries.length} comments and reviews, newest first\n` + entries.map(c => `${c.submitted_at || c.created_at} ${c.user?.login || translations(locale).deletedUser} ${c.state ?? ""}:\n${cut((c.body ?? "").trim(), 1500)}`).join("\n\n") : "");
    }
    if (name === "github_diff") {
      const files = await this.files(repo, n, signal);
      if (a.path) { const file = files.find(f => f.filename === a.path); if (!file) throw new GitHubError("The requested path is not changed by this pull request", "invalid_params"); return `${file.filename} (${file.status}, +${file.additions} −${file.deletions})\n${cut(file.patch || "(no patch: binary, or too big for GitHub to show)", 20000)}`; }
      return `${files.length} files changed, +${files.reduce((count, f) => count + f.additions, 0)} −${files.reduce((count, f) => count + f.deletions, 0)}\n` + files.map(f => `${f.status} ${f.previous_filename ? f.previous_filename + " → " : ""}${f.filename} +${f.additions} −${f.deletions}`).join("\n");
    }
    if (name === "github_checks") {
      const sha = a.ref || (await this.pull(repo, n, signal)).head.sha, checks = await this.checks(repo, sha, signal); let logs = 0;
      const lines: string[] = [];
      for (const check of checks) {
        lines.push(`${check.name}: ${check.conclusion || check.status} ${check.html_url ?? check.details_url ?? ""}\n${cut(check.output?.summary, 4000)}`);
        if (["failure", "timed_out"].includes(check.conclusion ?? "") && check.app?.slug === "github-actions" && logs++ < 3) {
          try { lines.push(cut((await this.client.jobLog(repo, check.id, signal)).slice(-6000), 6000)); }
          catch (error) { abort(signal); if (error instanceof GitHubError && error.code === "auth_changed") throw error; lines.push(errorText(error, locale)); }
        }
      }
      return lines.join("\n");
    }
    throw new GitHubError("Unknown read tool", "unknown_method");
  }
}
