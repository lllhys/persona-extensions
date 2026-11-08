import { absolute, stringify } from "./integers.ts";
import { View, Section, Text, Row, Button, Empty, Loading, Segmented, Markdown, Field, Header, FileTree, Code, Diff, CommentThread, Composer, Breadcrumb, StatGrid, Link, type Node, type NativeAction, type CallbackAction, type ActionOptions, type UIProps } from "@persona/extension/ui";
import type * as Wire from "@persona/extension/wire";
import { translations, errorText } from "./localization.ts";
import { clipBytes, repoOf, type Comment, type Issue, type PageState, type Pull, type Repository } from "./model.ts";
import { patchPages, keyFor, type Loaded } from "./page-model.ts";
import { checkState } from "./thread.ts";
export type ActionFactory = (id: string, options?: ActionOptions) => NativeAction | CallbackAction;
export interface PageProps { state: PageState; loaded: Loaded; locale?: string; uiVersion?: number; action: ActionFactory }
export function mergeLabel(p: Pull, locale?: string): string {
  const t = translations(locale);
  if (p.merged) return t.merged;
  if (p.draft) return t.draftState;
  if (p.mergeable === false || p.mergeable_state === "dirty") return t.conflict;
  return ({ clean: t.ready, has_hooks: t.ready, unstable: t.checksFailed, blocked: t.blockedMerge, behind: t.behind, draft: t.draftState } as Record<string, string>)[p.mergeable_state ?? ""] ?? t.unknownMerge;
}
/** Diff and file pagination bound the whole frame, not each of hundreds of patches. */
export function page(props: PageProps): Node {
  const { state: s, loaded: data, action: a } = props, t = translations(props.locale), version = props.uiVersion ?? 7;
  const tabs = data.kind === "repo" ? ["code", "pulls", "issues", "commits", "branches"] : s.n ? ["overview", "discussion", "files", "commits", "checks"] : ["review", "mine", "involved", "repos"];
  const label = (name: string): string => t[name as keyof typeof t] ?? name;
  const selected = s.t || tabs[0]!, title = s.n ? `${s.r}#${s.n}` : s.r || "GitHub", tabItems = tabs.map(value => ({ value, title: label(value) }));
  const stateLabel = (issue: Issue, pull?: Pull): string => pull?.merged ? t.merged : pull?.draft || issue.draft ? t.draftState : issue.state === "open" ? t.statusOpen : t.statusClosed;
  const text = (value: string | null | undefined, budget = 60000): string => clipBytes(value, budget);
  const diagnostics = (errors: unknown[] = []): Node => errors.map((error, i) => <Text key={`diagnostic-${i}`} text={errorText(error, props.locale)} style="secondary" />);
  let body: Node;
  if (data.kind === "loading") body = version >= 3 ? <Loading key="loading" title={t.loading} /> : <Text key="loading" text={t.loading} style="secondary" />;
  else if (data.kind === "error") body = <Text key="error" text={errorText(data.error, props.locale)} style="secondary" />;
  else if (data.kind === "list") body = <>
    {data.items.length === 0 ? version >= 3 ? <Empty key="empty" title={t.empty} /> : <Text key="empty" text={t.empty} /> : data.items.map((raw, index) => {
      if ("full_name" in raw) { const repo = raw as Repository; return <Row key={keyFor("repo", repo.full_name)} title={repo.full_name} subtitle={repo.description} icon="folder" action={a("repo:" + repo.full_name)} />; }
      if ("number" in raw) { const issue = raw as Issue, repo = repoOf(issue); return <Row key={keyFor("thread", repo + "#" + issue.number)} title={issue.title} subtitle={`${repo}#${issue.number} · ${issue.user?.login || t.deletedUser}`} badge={{ text: stateLabel(issue) }} icon={issue.pull_request ? "pull_request" : "doc"} action={a(`thread:${repo}#${issue.number}`)} />; }
      const item = raw as Record<string, any>, subject = item.subject, repo = item.repository?.full_name ?? item.repo?.name ?? "", match = /\/(?:issues|pulls)\/(\d+)$/.exec(subject?.url ?? "");
      return <Row key={keyFor("event", String(item.id ?? index))} title={subject?.title ?? `${item.type ?? ""} ${repo}`} subtitle={item.reason ?? item.actor?.login ?? ""} action={match ? a(`thread:${repo}#${match[1]}`) : repo ? a("repo:" + repo) : undefined} />;
    })}
  </>;
  else if (data.kind === "file") {
    const markdown = /\.(?:md|markdown)$/i.test(s.p ?? "") && !s.src;
    body = <>
      {version >= 4 ? <Breadcrumb key="file-path" crumbs={(s.p ?? "").split("/").map((name, i, parts) => ({ title: name, action: i < parts.length - 1 ? a("directory:" + parts.slice(0, i + 1).join("/")) : undefined }))} /> : null}
      {markdown && version >= 3 ? <Markdown key="file-markdown" text={text(data.text, 100000)} /> : version >= 4 ? <Code key={keyFor("code", s.r + ":" + s.p)} path={s.p} text={text(data.text, 100000)} line_url={`https://github.com/${data.repo ?? s.r}/blob/${data.ref || s.ref || "HEAD"}/${s.p}#L{start}-L{end}`} /> : <Text key="file" text={text(data.text, 100000)} style="code" />}
      {/\.(?:md|markdown)$/i.test(s.p ?? "") ? <Button key="raw" title={s.src ? t.preview : t.raw} action={a("src")} /> : null}
      <Link key="file-web" title={t.openWeb} url={`https://github.com/${data.repo ?? s.r}/blob/${data.ref || s.ref || "HEAD"}/${s.p}`} />
    </>;
  } else if (data.kind === "repo") body = <>
    {version >= 4 ? <Header key="repo-header" title={data.repository.full_name} subtitle={data.repository.description} /> : <Text key="repo-header" text={data.repository.full_name} />}
    {s.p ? <Button key="parent-directory" title={t.previous} action={a("directory:" + s.p.split("/").slice(0, -1).join("/"))} /> : null}
    {selected === "code" && version >= 4 ? <FileTree key="tree" entries={data.items.map(i => ({ path: String(i.path), kind: i.type === "dir" ? "dir" : "file" }))} selected={s.p} action={a("file")} expand={a("directory")} /> : data.items.map((i, index) => {
      const commit = i.commit as { message?: string } | undefined;
      return <Row key={keyFor("repo-item", String(i.sha ?? i.number ?? i.path ?? i.name ?? index))} title={String(i.title ?? i.path ?? commit?.message?.split("\n")[0] ?? i.name ?? i.sha ?? "")} subtitle={String(i.sha ?? "").slice(0, 12)} action={selected === "branches" ? a("branch:" + i.name) : i.number ? a(`thread:${s.r}#${i.number}`) : i.path ? a("file:" + i.path, { values: { path: String(i.path), kind: String(i.type) } }) : i.sha ? a("commit:" + i.sha) : undefined} />;
    })}
    {data.readme ? version >= 3 ? <Markdown key="readme" text={text(data.readme)} /> : <Text key="readme" text={text(data.readme)} /> : null}
    <Link key="repo-web" title={t.openWeb} url={data.repository.html_url} />
  </>;
  else if (data.kind === "detail") {
    const issue = data.issue, pull = data.pull;
    const comments = [...data.comments, ...data.reviews.filter(r => r.state !== "PENDING").map(r => ({ ...r, kind: "review" as const }))].sort((a, b) => (a.submitted_at || a.created_at).localeCompare(b.submitted_at || b.created_at));
    const discussionID = (c: Comment): string => (c.state === "event" ? "e" : c.kind === "code" ? "rc" : c.kind === "review" ? "r" : "c") + absolute(c.id);
    const visibleComments = comments.slice(-100 * (s.pg ?? 1));
    const canReview = !!pull && issue.state === "open", canMerge = canReview && !pull.draft;
    const buttons = [...(!issue.locked ? [{ title: t.comment, action: a("edit:comment", { submit: true }) }] : []), ...(canReview ? [{ title: t.approve, action: a("edit:review", { submit: true, values: { event: "APPROVE" } }) }, { title: t.changes, action: a("edit:review", { submit: true, values: { event: "REQUEST_CHANGES" } }) }, { title: t.reviewComment, action: a("edit:review", { submit: true, values: { event: "COMMENT" } }) }] : []), ...(canMerge ? [{ title: t.merge, action: a("edit:merge", { submit: true, values: { event: "merge" } }) }] : [])];
    const fileOffset = Math.min(s.fp ?? 0, Math.max(0, Math.ceil(data.files.length / 100) - 1)) * 100;
    const visibleFiles = s.v === "diff" ? data.files.filter(f => f.filename === s.p) : data.files.slice(fileOffset, fileOffset + 100);
    body = <>
      {version >= 4 ? <Header key="thread-header" title={issue.title} subtitle={`${issue.user?.login || t.deletedUser} · ${stateLabel(issue, pull)}`} facts={pull ? [{ value: `${pull.head.ref} → ${pull.base.ref}`, icon: "branch" }] : undefined} /> : <Text key="thread-header" text={`${issue.title}\n${stateLabel(issue, pull)} · ${issue.user?.login || t.deletedUser}`} />}
      {diagnostics(data.diagnostics)}
      {["overview", "discussion"].includes(selected) && s.v !== "diff" ? <>
        {selected === "overview" ? <>
          {pull && version >= 2 ? <StatGrid key="size" stats={[{ label: t.files, value: String(pull.changed_files ?? 0), tone: "neutral" }, { label: t.additions, value: "+" + (pull.additions ?? 0), tone: "positive" }, { label: t.deletions, value: "−" + (pull.deletions ?? 0), tone: "danger" }, { label: t.commits, value: String(pull.commits ?? 0), tone: "neutral" }]} /> : null}
          {pull ? <Row key="mergeable" title={t.mergeStatus} value={mergeLabel(pull, props.locale)} /> : null}
          {issue.labels?.length ? <Row key="labels" title={t.labels} value={issue.labels.map(l => l.name).join(", ")} /> : null}
          {issue.assignees?.length ? <Row key="assignees" title={t.assignees} value={issue.assignees.map(u => u.login).join(", ")} /> : null}
          {pull?.milestone ? <Row key="milestone" title={t.milestone} value={pull.milestone.title} /> : null}
          <Row key="updated" title={t.updated} value={issue.updated_at} />
          {pull?.requested_reviewers?.length ? <Row key="requested" title={t.requested} value={pull.requested_reviewers.map(u => u.login).join(", ")} /> : null}
          {data.checks.filter(c => checkState(c) === "failure").map(check => <Row key={keyFor("overview-failed", String(check.id))} title={check.name} value={check.conclusion} action={a("check:" + check.id)} />)}
          {data.checks.some(c => checkState(c) === "failure" && c.app?.slug === "github-actions") ? <Button key="rerun-all" title={t.rerun} action={a("rerun_all", { confirm: t.rerunConfirm })} /> : null}
          {data.reviews.filter(r => r.state !== "PENDING").slice(-20).map(r => <Row key={keyFor("review-status", String(r.id))} title={r.user?.login || t.deletedUser} value={r.state === "APPROVED" ? t.reviewApproved : r.state === "CHANGES_REQUESTED" ? t.reviewChanges : t.reviewed} />)}
        </> : null}
        {version >= 3 ? <Markdown key="description" text={text(issue.body || t.noDescription)} /> : <Text key="description" text={text(issue.body || t.noDescription)} />}
        {version >= 4 ? <CommentThread key="discussion" title={t.discussion} comments={visibleComments.map(c => ({ id: discussionID(c), author: c.user?.login || t.deletedUser, body: text(c.body, Math.max(256, Math.floor(180000 / Math.max(1, visibleComments.length)))), at: c.submitted_at || c.created_at, event: c.state === "event", avatar: c.user?.avatar_url ? { url: c.user?.avatar_url } : undefined, badge: c.state && c.state !== "event" ? { text: c.state } : undefined }))} reply={!issue.locked ? a("edit:comment", { submit: true }) : undefined} /> : visibleComments.slice(-50).map(c => <Text key={keyFor("comment", discussionID(c))} text={`${c.user?.login || t.deletedUser}: ${text(c.body, 2400)}`} />)}
        {comments.length > visibleComments.length && (s.pg ?? 1) < 10 ? <Button key="more-discussion" title={t.more} action={a("more")} /> : null}
        {version >= 4 && buttons.length ? <Composer key="composer" name="body" placeholder={t.body} buttons={buttons} /> : <>{!issue.locked ? <Field key="body" name="body" label={t.body} kind="multiline" /> : <Text key="locked" text={t.locked} />}{buttons.map((button, i) => <Button key={`write-${i}`} {...button} />)}</>}
      </> : null}
      {selected === "commits" ? data.commits.map(commit => <Row key={keyFor("pr-commit", commit.sha)} title={commit.commit.message.split("\n")[0]!} subtitle={commit.sha.slice(0, 12)} icon="commit" action={a("commit:" + commit.sha)} />) : null}
      {selected === "checks" ? data.checks.map(check => <Row key={keyFor("check", String(check.id))} title={check.name} value={check.conclusion || check.status} icon="check" action={check.app?.slug === "github-actions" || check.output ? a("check:" + check.id) : undefined} />) : null}
      {selected === "files" || s.v === "diff" ? <>
        {data.pending ? <><Text key="pending" text={`${t.pendingReview}: ${data.pending.id}`} /><Button key="submit-pending" title={t.submitReview} action={a("edit:submit_review", { submit: true })} /><Button key="discard-pending" title={t.discardReview} role="destructive" action={a("discard_review", { confirm: t.discardConfirm })} />{(data.draftComments ?? []).map(c => <Row key={keyFor("draft-comment", String(c.id))} title={`${c.path}:${c.line}`} subtitle={text(c.body, 1000)} badge={{ text: t.draftState }} />)}</> : null}
        {version >= 4 ? <FileTree key="files-tree" entries={visibleFiles.map(f => ({ path: f.filename, status: f.status, additions: f.additions, deletions: f.deletions, viewed: data.viewed[f.filename] }))} selected={s.o} action={a("load_diff")} /> : null}
        {visibleFiles.map((f, index) => {
          const threads: UIProps<Wire.Thread>[] = data.threads.filter(thread => thread.path === f.filename).map(thread => ({ id: thread.id, line: thread.line || 1, start_line: thread.startLine, side: thread.diffSide === "LEFT" ? "old" : "new", resolved: thread.isResolved, outdated: thread.isOutdated, comments: thread.comments.nodes.map(c => ({ id: String(c.databaseId), author: c.author?.login || "", at: c.createdAt, body: text(c.body, Math.max(128, Math.floor(80000 / Math.max(1, data.threads.filter(th => th.path === f.filename).reduce((count, th) => count + th.comments.nodes.length, 0))))), avatar: c.author?.avatarUrl ? { url: c.author.avatarUrl } : undefined })), reply: a("edit:reply", { submit: true, values: { id: String(thread.comments.nodes[0]?.databaseId ?? 0) } }), resolve: a(thread.isResolved ? "unresolve" : "resolve", { confirm: t.resolveConfirm, values: { thread: thread.id } }) }));
          const loaded = f.filename === s.o || s.v === "diff" || !s.o && index === 0;
          let pages: Wire.Hunk[][] = [], problem = "";
          if (loaded) { try { pages = patchPages(f.patch); } catch (error) { problem = errorText(error, props.locale); } }
          const part = s.v === "diff" ? Math.min(s.dp ?? 0, Math.max(0, pages.length - 1)) : 0, shown = pages[part];
          const patch = shown?.map(h => h.header + "\n" + h.lines.join("\n")).join("\n") ?? "";
          return <>
            {version >= 4 ? <Diff key={keyFor("diff", f.filename)} path={f.filename} old_path={f.previous_filename} status={f.status} additions={f.additions} deletions={f.deletions} revision={f.sha} collapsed={!loaded} hunks={loaded ? shown : undefined} threads={loaded ? threads : undefined} binary={!f.patch} load={a("load_diff")} expand={a("diff:" + f.filename)} comment={canReview ? a("edit:line", { submit: true }) : undefined} draft={canReview ? a("edit:draft", { submit: true }) : undefined} context={version >= 5 ? a("context") : undefined} viewed={data.viewed[f.filename]} mark_viewed={a("mark_viewed", { confirm: t.resolveConfirm })} suggestions={true} /> : <><Row key={keyFor("diff-name", f.filename)} title={f.filename} subtitle={`+${f.additions} -${f.deletions}`} action={a("diff:" + f.filename)} />{loaded ? <Text key={keyFor("patch", f.filename)} text={patch || t.binary} style="code" /> : null}{loaded ? threads.map(thread => <><Row key={keyFor("legacy-thread", thread.id ?? "")} title={`${f.filename}:${thread.line}`} value={thread.resolved ? t.resolve : t.reply} action={thread.resolve} /><Text key={keyFor("legacy-thread-body", thread.id ?? "")} text={thread.comments.map(c => `${c.author}: ${text(c.body, 1000)}`).join("\n")} /><Button key={keyFor("legacy-reply", thread.id ?? "")} title={t.reply} action={thread.reply!} /></>) : null}</>}
            {problem ? <Text key={keyFor("diff-problem", f.filename)} text={problem} /> : null}
            {s.v !== "diff" && pages.length > 1 ? <Button key={keyFor("full-diff", f.filename)} title={t.fullDiff} action={a("diff:" + f.filename)} /> : null}
            {s.v === "diff" && pages.length > 1 ? <><Text key="diff-page" text={`${part + 1} / ${pages.length}`} />{part > 0 ? <Button key="diff-previous" title={t.previous} action={a("diff_previous")} /> : null}{part + 1 < pages.length ? <Button key="diff-next" title={t.next} action={a("diff_next")} /> : null}</> : null}
            {loaded ? <><Button key={keyFor("source", f.filename)} title={t.source} action={a("source:" + f.filename)} /><Link key={keyFor("diff-web", f.filename)} title={t.openWeb} url={`https://github.com/${s.r}/pull/${s.n}/files`} /></> : null}
          </>;
        })}
        {s.v !== "diff" && fileOffset > 0 ? <Button key="files-previous" title={t.previous} action={a("files_previous")} /> : null}
        {s.v !== "diff" && fileOffset + 100 < data.files.length ? <Button key="files-next" title={t.next} action={a("files_next")} /> : null}
      </> : null}
      <Link key="thread-web" title={t.openWeb} url={issue.html_url} />
    </>;
  } else if (data.kind === "check") body = <>
    <Text key="check-summary" text={`${data.check.name}: ${data.check.conclusion || data.check.status}\n${text(data.check.output?.title, 2000)}`} />
    {diagnostics(data.diagnostics)}
    {(data.annotations ?? []).slice(0, 100).map((annotation, i) => <><Row key={`annotation-${i}`} title={text(annotation.title || annotation.message, 1000)} subtitle={`${annotation.path}:${annotation.start_line}`} badge={{ text: annotation.annotation_level }} /><Text key={`annotation-message-${i}`} text={text(annotation.message, 2000)} /></>)}
    {(data.steps ?? []).map(step => <Row key={`step-${step.number}`} title={`${step.number}. ${step.name}`} value={step.conclusion || step.status} />)}
    {data.log ? <Text key="log" text={text(data.log.slice(-100000), 100000)} style="code" /> : null}
    {data.check.output?.summary ? version >= 3 ? <Markdown key="summary" text={text(data.check.output.summary, 12000)} /> : <Text key="summary" text={text(data.check.output.summary, 12000)} /> : null}
    {data.check.app?.slug === "github-actions" && checkState(data.check) === "failure" ? <Button key="rerun" title={t.rerun} action={a("rerun", { confirm: t.rerunConfirm, values: { id: String(s.id) } })} /> : null}
    {data.check.html_url || data.check.details_url ? <Link key="check-web" title={t.openWeb} url={(data.check.html_url || data.check.details_url)!} /> : null}
  </>;
  else body = <>
    <Text key="write-target" text={`${s.w}: ${s.r}#${s.n}${s.p ? " " + s.p + ":" + s.l : ""}`} />
    {s.pv ? <>{version >= 3 ? <Markdown key="preview" text={s.b ?? ""} /> : <Text key="preview" text={s.b ?? ""} />}<Text key="write-choice" text={s.e ?? ""} /><Button key="send" title={t.send} action={a("send", { confirm: t.confirm })} /><Button key="edit" title={t.raw} action={a("edit_preview")} /></> : <><Field key="write-body" name="body" label={t.body} kind="multiline" value={s.b ?? ""} />{["review", "submit_review", "merge"].includes(s.w ?? "") ? <Field key="event" name="event" label={s.w === "merge" ? t.merge : t.reviewComment} kind="choice" value={s.e} choices={(s.w === "merge" ? ["merge", "squash", "rebase"] : ["COMMENT", "APPROVE", "REQUEST_CHANGES"]).map(value => ({ value, title: value }))} /> : null}<Button key="preview" title={t.preview} action={a("preview", { submit: true })} /></>}
    <Button key="cancel" title={t.cancel} action={a("cancel")} />
  </>;
  const hasMore = (data.kind === "list" || data.kind === "repo") && data.more;
  return <View key="github" title={title} state={stringify(s)} refresh_after={300} tabs={version >= 4 && !["write", "file", "source", "check"].includes(s.v ?? "") ? { name: "t", value: selected, items: tabItems, action: a("tab") } : undefined} search={!s.n && s.v !== "file" ? { placeholder: t.search, value: s.q ?? "", action: a("search") } : undefined} filter={data.kind === "list" && (s.t === "involved" || s.t === "repos") ? { name: "f", title: s.t === "repos" ? t.organization : t.involved, value: s.f ?? "", choices: s.t === "repos" ? [{ value: "", title: t.all }, ...data.organizations.map(o => ({ value: o, title: o }))] : [{ value: "involved", title: t.assigned }, { value: "notify", title: t.notify }, { value: "activity", title: t.activity }], action: a("filter") } : data.kind === "repo" && ["pulls", "issues"].includes(selected) ? { name: "f", value: s.f ?? "open", choices: [{ value: "open", title: t.open }, { value: "closed", title: t.closed }], action: a("filter") } : undefined}>
    <Section key="navigation">
      {s.r || s.n ? <Button key="back" title="GitHub" action={a("back")} /> : null}
      {version < 4 && s.v !== "write" ? version >= 2 ? <Segmented key="tabs" name="t" choices={tabItems} value={selected} action={a("tab")} /> : tabs.map(tab => <Button key={"tab-" + tab} title={label(tab)} action={a("tab:" + tab)} />) : null}
    </Section>
    <Section key="content" more={hasMore ? { title: t.more, action: a("more") } : undefined}>{body}</Section>
    <Section key="refresh-section"><Button key="refresh" title={t.refresh} action={a("refresh")} /></Section>
  </View>;
}
