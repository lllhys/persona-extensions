import { stringify } from "./integers.ts";
import { callback, useEffect, useLoadEffect, useState, type Node } from "@persona/extension/ui";
import { page } from "./page.tsx";
import { loadPage, readState, transition, hunks, type Loaded } from "./page-model.ts";
import { GitHubError } from "./client.ts";
import { type PageState } from "./model.ts";
import type { GitHubService } from "./service.ts";
import { WriteReceipts } from "./connector.ts";
export interface RootOptions { service: GitHubService; receipts: WriteReceipts; state?: unknown; locale?: string; uiVersion?: number }
export async function preview(service: GitHubService, s: PageState, values: Record<string, string>, signal?: AbortSignal): Promise<PageState> {
  const epoch = service.client.epoch;
  if (s.authEpoch !== undefined && s.authEpoch !== epoch) throw new GitHubError("Account changed since editing began", "auth_changed");
  const body = values.body ?? s.b ?? "", event = values.event ?? s.e ?? "COMMENT";
  const coordinates = ["line", "draft"].includes(s.w ?? "");
  const pull = s.w === "merge" || coordinates ? await service.pull(s.r!, s.n!, signal, true) : undefined;
  if (Array.from(body).length > 2000) throw new GitHubError("Write preview exceeds the original 2000-character limit", "invalid_params");
  if (!["comment", "review", "submit_review", "merge", "line", "reply", "draft"].includes(s.w ?? "")) throw new GitHubError("Unknown write", "invalid_params");
  if (["review", "submit_review"].includes(s.w ?? "") && !["APPROVE", "REQUEST_CHANGES", "COMMENT"].includes(event)) throw new GitHubError("Invalid review event", "invalid_params");
  if (s.w !== "merge" && event !== "APPROVE" && !body.trim() && !(s.w === "submit_review" && await service.pending(s.r!, s.n!, signal))) throw new GitHubError("A nonempty comment is required", "invalid_params");
  if (["line", "draft"].includes(s.w ?? "")) {
    if (!s.p || !Number.isSafeInteger(s.l) || !s.l || s.l < 1 || !["old", "new"].includes(s.s ?? "") || s.start && (!Number.isSafeInteger(s.start) || s.start < 1 || s.start > s.l)) throw new GitHubError("Invalid comment lines", "invalid_params");
    if (!s.head) throw new GitHubError("Open the diff before selecting comment lines", "invalid_params");
    if (s.head !== pull?.head.sha) throw new GitHubError("Displayed diff changed before preview", "head_changed");
    service.client.invalidate(s.r);
    const file = (await service.files(s.r!, s.n!, signal)).find(f => f.filename === s.p);
    if ((await service.pull(s.r!, s.n!, signal, true)).head.sha !== pull?.head.sha) throw new GitHubError("Diff changed during preview", "head_changed");
    const visible = new Set<number>();
    for (const hunk of hunks(file?.patch)) { let line = s.s === "old" ? hunk.old_start : hunk.new_start; for (const row of hunk.lines) if (row[0] !== "\\" && row[0] !== (s.s === "old" ? "+" : "-")) visible.add(line++); }
    if (!visible.has(s.l) || s.start && !visible.has(s.start)) throw new GitHubError("Comment lines are not in this diff", "invalid_params");
  }
  if (s.w === "merge" && pull && (pull.state !== "open" || pull.draft || pull.merged || !["merge", "squash", "rebase"].includes(event))) throw new GitHubError("This pull request cannot be merged", "invalid_params");
  if (epoch !== service.client.epoch) throw new GitHubError("Account changed during preview", "auth_changed");
  const head = pull?.head.sha;
  return { ...s, b: body, e: event, pv: true, head, authEpoch: epoch, o: crypto.randomUUID() };
}
export async function perform(service: GitHubService, receipts: WriteReceipts, s: PageState, action: string, values: Record<string, string>, signal?: AbortSignal): Promise<unknown> {
  if (action === "send") {
    if (!s.pv || !s.o || s.v !== "write") throw new GitHubError("Preview is required before sending", "permission_denied");
    if (s.authEpoch !== service.client.epoch) throw new GitHubError("Account changed since preview", "auth_changed");
    if (["line", "draft"].includes(s.w ?? "") && !s.head) throw new GitHubError("A commit-bound preview is required", "permission_denied");
    return receipts.run(s.o, s, () => service.write(s.r!, s.n!, s.w!, { body: s.b ?? "", event: s.e ?? "COMMENT", path: s.p ?? "", line: String(s.l ?? 0), start_line: String(s.start ?? 0), side: s.s ?? "new", id: String(s.id ?? 0) }, s.head, signal));
  }
  if (action === "rerun_all") return service.rerunFailed(s.r!, s.n!, signal).then(() => undefined);
  if (action.startsWith("commit:")) return { open_url: `https://github.com/${s.r}/commit/${encodeURIComponent(action.slice(7))}` };
  if (action === "context") return service.context(s.r!, s.n!, values.path ?? "", values.side ?? "new", Number(values.from), values.to === "end" ? Number(values.from) + 199 : Number(values.to), signal).then(lines => ({ lines }));
  return service.write(s.r!, s.n!, action, values, undefined, signal).then(() => undefined);
}
/** Page effects are cancellable; the connector's background poller is not owned by a view. */
export function Root(options: RootOptions): Node {
  const [state, setState] = useState(() => readState(options.state)), [revision, setRevision] = useState(0);
  const [result, setResult] = useState<{ identity: string; loaded: Loaded } | undefined>(undefined), [failure, setFailure] = useState<unknown>(undefined);
  const epoch = options.service.client.epoch;
  useEffect(() => options.service.client.onAuthenticationChange(() => { setResult(undefined); setFailure(undefined); setState({}); setRevision(r => r + 1); }), [options.service]);
  // Content-window navigation reuses the already loaded revision, without another request.
  const { cp: _sourcePage, lp: _logPage, ...requestState } = state;
  const identity = stringify(requestState) + ":" + revision + ":" + epoch;
  useLoadEffect(async signal => {
    const loaded = await loadPage(options.service, state, signal, options.locale);
    if (!signal.aborted) setResult(previous => previous?.identity === identity && stringify(previous.loaded) === stringify(loaded) ? previous : { identity, loaded });
  }, [options.service, identity]);
  const loaded: Loaded = failure ? { kind: "error", error: failure } : result?.identity === identity ? result.loaded : { kind: "loading" };
  return page({ ...options, state, loaded, action: (id, config = {}) => callback(async (values, context) => {
    context.signal.throwIfAborted(); if (epoch !== options.service.client.epoch) throw new GitHubError("Account changed since this page was drawn", "auth_changed"); setFailure(undefined);
    try {
      if (id === "preview") { const next = await preview(options.service, state, values, context.signal); if (!context.signal.aborted) setState(next); }
      else if (id === "send") { await perform(options.service, options.receipts, state, id, values, context.signal); if (!context.signal.aborted) setState(transition(state, "cancel")); }
      else if (id.startsWith("commit:") || ["context", "mark_viewed", "resolve", "unresolve", "discard_review", "rerun", "rerun_all"].includes(id)) {
        const response = await perform(options.service, options.receipts, state, id, values, context.signal); if (id !== "context" && !id.startsWith("commit:") && !context.signal.aborted) setRevision(r => r + 1); return response;
      } else if (id === "refresh") { options.service.client.invalidate(state.r); setRevision(r => r + 1); }
      else {
        const navigation = id === "file" && loaded.kind === "repo" ? { ...values, kind: String(loaded.items.find(i => i.path === values.path)?.type ?? "file") } : values;
        const editingDiff = ["edit:line", "edit:draft"].includes(id);
        if (editingDiff && (loaded.kind !== "detail" || !loaded.pull || !loaded.files.length)) throw new GitHubError("Open the diff before selecting comment lines", "invalid_params");
        const drawnState = editingDiff && loaded.kind === "detail" ? { ...state, head: loaded.pull!.head.sha, authEpoch: epoch } : id.startsWith("source:") && loaded.kind === "detail" && loaded.pull ? { ...state, ref: loaded.pull.head.sha } : state;
        setState(transition(drawnState, id, navigation));
      }
    } catch (error) { context.signal.throwIfAborted(); setFailure(error); }
  }, config) });
}
export const root = (options: RootOptions): Node => <Root key="github-root" {...options} />;
