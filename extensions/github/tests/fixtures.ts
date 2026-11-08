import { GitHubAuth } from "../src/auth.ts";
import { GitHubClient } from "../src/client.ts";
import { GitHubService } from "../src/service.ts";
import { InboxPoller } from "../src/inbox.ts";
import { ArchiveReader } from "../src/archive.ts";
import { GitHubConnector } from "../src/connector.ts";
import { b64encode, type Config, type Fetch, type Host, type Issue, type Pull, type Comment, type Notification } from "../src/model.ts";
export const issue: Issue = { number: 12, title: "Synthetic pull request", body: "Synthetic description", state: "open", user: { login: "author" }, html_url: "https://github.com/example/repo/pull/12", repository_url: "https://api.github.com/repos/example/repo", created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-03T00:00:00Z", comments: 1, pull_request: {} };
export const pull: Pull = { ...issue, node_id: "synthetic-pr", head: { sha: "head-one", ref: "feature", repo: { full_name: "example/repo" } }, base: { sha: "base-one", ref: "main", repo: { full_name: "example/repo" } }, requested_reviewers: [{ login: "reader" }] };
export const comment: Comment = { id: 31, user: { login: "commenter" }, body: "Synthetic comment", created_at: "2026-01-02T00:00:00Z", html_url: "https://github.com/example/repo/pull/12#issuecomment-31" };
export const notification: Notification = { id: "n1", reason: "review_requested", updated_at: "2026-01-03T00:00:00Z", subject: { title: issue.title, type: "PullRequest", url: "https://api.github.com/repos/example/repo/pulls/12" }, repository: { full_name: "example/repo", html_url: "https://github.com/example/repo" } };
export class Fixture {
  now = Date.parse("2026-01-04T00:00:00Z");
  hits: { url: string; method: string; headers: Headers; body: string }[] = [];
  calls: { method: string; params: unknown }[] = []; events: { event: string; params: unknown }[] = [];
  secret = b64encode(JSON.stringify({ token: "synthetic-device-token" }));
  apiToken = "synthetic-device-token"; head = "head-one";
  overrides?: (url: URL, init: RequestInit) => Response | Promise<Response> | undefined;
  deviceResponses: Record<string, unknown>[] = [];
  notifications: Notification[] = [notification];
  host: Host = {
    call: async <T,>(method: string, params?: unknown): Promise<T> => { this.calls.push({ method, params }); if (method === "connector.secrets.load") return { data: this.secret } as T; if (method === "connector.secrets.save") { this.secret = (params as { data: string }).data; return {} as T; } if (method === "connector.credentials.gh_token") return { token: "synthetic-machine-token" } as T; throw new Error("Unexpected host method"); },
    notify: (event, params) => { this.events.push({ event, params }); }, badge: () => {}, invalidate: () => {},
  };
  fetch: Fetch = async (input, init) => {
    const url = new URL(input), headers = new Headers(init.headers); init.signal?.throwIfAborted();
    this.hits.push({ url: url.href, method: init.method || "GET", headers, body: typeof init.body === "string" ? init.body : String(init.body ?? "") });
    const overridden = this.overrides?.(url, init); if (overridden) return overridden;
    const json = (value: unknown, extra: HeadersInit = {}) => Response.json(value, { headers: extra });
    const path = url.pathname, method = init.method || "GET", page = Number(url.searchParams.get("page") || 1);
    if (url.hostname === "github.com") {
      if (path === "/login/device/code") return json({ device_code: "synthetic-code", user_code: "ABCD-EFGH", verification_uri: "https://github.com/login/device", interval: 5, expires_in: 60 });
      if (path === "/login/oauth/access_token") return json(this.deviceResponses.shift() || { error: "authorization_pending" });
    }
    if (method !== "GET" && path !== "/graphql") return json({ html_url: "https://github.com/example/repo/pull/12#synthetic" });
    if (path === "/user") return json({ login: "reader" }, { "X-OAuth-Scopes": "repo, read:org" });
    if (path === "/user/orgs") return json([{ login: "example" }]);
    if (path === "/graphql") {
      const query = JSON.parse(String(init.body)).query as string;
      if (query.includes("reviewThreads")) return json({ data: { repository: { pullRequest: { reviewThreads: { pageInfo: { hasNextPage: false, endCursor: "" }, nodes: [{ id: "synthetic-thread", isResolved: false, isOutdated: false, path: "file.ts", line: 1, diffSide: "RIGHT", comments: { nodes: [{ databaseId: 91, body: "Synthetic inline thread", createdAt: comment.created_at, author: { login: "commenter" } }] } }] } } } } });
      if (query.includes("viewerViewedState")) return json({ data: { repository: { pullRequest: { files: { pageInfo: { hasNextPage: false, endCursor: "" }, nodes: [{ path: "file.ts", viewerViewedState: "VIEWED" }] } } } } });
      return json({ data: {} });
    }
    if (path === "/notifications") return json(this.notifications, { "Last-Modified": "synthetic-date", "X-Poll-Interval": "90" });
    if (path === "/search/issues") return json({ total_count: 1, items: [issue] });
    if (path === "/user/repos" || path === "/orgs/example/repos" || path === "/search/repositories") { const repos = [{ full_name: "example/repo", name: "repo", html_url: "https://github.com/example/repo", default_branch: "main", owner: { login: "example" } }]; return json(path === "/search/repositories" ? { total_count: 1, items: repos } : repos); }
    if (path === "/repos/example/repo") return json({ full_name: "example/repo", name: "repo", default_branch: "main", html_url: "https://github.com/example/repo", owner: { login: "example" } });
    if (/\/issues\/12$/.test(path)) return json(issue);
    if (/\/pulls\/12$/.test(path)) return json({ ...pull, head: { ...pull.head, sha: this.head } });
    if (/\/events$/.test(path)) return json([]);
    if (/\/comments$/.test(path)) return json(page === 1 ? [comment] : []);
    if (/\/reviews$/.test(path)) return json([]);
    if (/\/files$/.test(path)) return json([{ filename: "file.ts", sha: "synthetic-blob", status: "modified", additions: 1, deletions: 1, patch: "@@ -1 +1 @@\n-old\n+new" }]);
    if (/\/check-runs$/.test(path)) return json({ total_count: 1, check_runs: [{ id: 71, name: "Synthetic CI", status: "completed", conclusion: "failure" }] });
    if (/\/status$/.test(path)) return json({ statuses: [] });
    if (/\/check-runs\/71$/.test(path)) return json({ id: 71, name: "Synthetic CI", status: "completed", conclusion: "failure" });
    if (/\/logs$/.test(path)) return new Response("Synthetic CI error\n");
    if (/\/actions\/jobs\/71$/.test(path)) return json({ run_id: 81 });
    if (/\/actions\/runs$/.test(path)) return json({ workflow_runs: [{ conclusion: "failure" }] });
    if (/\/branches\//.test(path)) return json({ name: "feature" });
    if (/\/readme$/.test(path)) return json({ name: "README.md", path: "README.md", type: "file", sha: "readme-sha", size: 10, content: b64encode("# Synthetic README"), encoding: "base64" });
    if (/\/contents\/?$/.test(path)) return json([{ name: "file.ts", path: "file.ts", type: "file", sha: "file-sha" }, { name: "src", path: "src", type: "dir", sha: "dir-sha" }]);
    if (/\/contents\//.test(path)) return json({ name: "file.ts", path: "file.ts", type: "file", sha: "file-sha", size: 8, content: b64encode("old\nnew\n"), encoding: "base64" });
    if (/\/commits$/.test(path)) return json([{ sha: "head-one", commit: { message: "Synthetic commit" } }]);
    if (/\/branches$/.test(path)) return json([{ name: "main" }]);
    if (/\/issues$/.test(path) || /\/pulls$/.test(path)) return json([issue]);
    if (/\/received_events$/.test(path)) return json([{ id: "event-one", type: "PushEvent", repo: { name: "example/repo" } }]);
    if (url.hostname === "avatars.githubusercontent.com") return new Response(new Uint8Array([1, 2, 3]), { headers: { "content-type": "image/png" } });
    throw new Error(`Unimplemented synthetic request ${method} ${path}`);
  };
  async setup(config: Config = {}) {
    const auth = new GitHubAuth(this.host, config, () => this.now), client = new GitHubClient(this.fetch, config, auth, () => this.now); auth.client = client;
    const service = new GitHubService(client), inbox = new InboxPoller(service, auth, config, () => this.now), archive = new ArchiveReader(service, auth, () => this.now), connector = new GitHubConnector(auth, service, inbox, archive);
    await auth.check(); return { auth, client, service, inbox, archive, connector };
  }
}
