import type * as Wire from "@persona/extension/wire";
import { ExtError, unknownMethod, type ExactCall } from "@persona/extension";
import { exactParams, strings, stringArray, stringMapFields, optionArgs, statusChanges, integer, bounded, readArgs, stringify, isInteger, decimal, type Integer } from "./integers.ts";
import { ThreadReader } from "./thread.ts";
import originalManifest from "../persona-extension.json";
import tools from "./tools.json";
import { GitHubError, bounds } from "./client.ts";
import { b64encode, bytes, cut, validRepo } from "./model.ts";
import type { GitHubAuth } from "./auth.ts";
import type { GitHubService } from "./service.ts";
import type { InboxPoller } from "./inbox.ts";
import type { ArchiveReader } from "./archive.ts";
export const capabilities = ["status", "tools", "actions", "events", "inbox", "settle", "archive", "login", "options", "threads", "machines", "fallback"];
export const kinds = ["review_requested", "mention", "team_mention", "assign", "author", "ci_failed"];
/** Successful and ambiguous failed attempts both occupy a receipt; a retry never repeats a write. */
export class WriteReceipts {
  private generation = 0;
  private entries = new Map<string, { fingerprint: string; generation: number; result: Promise<unknown> }>();
  resetAuthentication(): void { this.generation++; }
  run<T>(id: string, args: unknown, action: () => Promise<T>): Promise<T> {
    if (!id || id.length > 256) throw new GitHubError("A bounded request_id is required", "invalid_params");
    const fingerprint = stringify(args), generation = this.generation, previous = this.entries.get(id);
    if (previous) { if (previous.generation !== generation) throw new GitHubError("Request id belongs to an earlier account", "auth_changed"); if (previous.fingerprint !== fingerprint) throw new GitHubError("Request id reused for another write", "invalid_params"); return previous.result as Promise<T>; }
    // Never evict a receipt and accidentally turn an old retry into a fresh public write.
    if (this.entries.size >= 5000) throw new GitHubError("Write receipt capacity reached; start a new authorized session", "rate_limited");
    const result = Promise.resolve().then(() => { if (generation !== this.generation) throw new GitHubError("Account changed before write dispatch", "auth_changed"); return action(); }); this.entries.set(id, { fingerprint, generation, result }); return result;
  }
}
export function writeArgs(name: string, args: Record<string, unknown>): { repo: string; number: Integer; body: string; event: string } {
  if (!["github_comment", "github_review"].includes(name)) throw unknownMethod(name);
  if (!args || typeof args !== "object" || Array.isArray(args)) throw new GitHubError("Invalid write arguments", "invalid_params");
  for (const key of ["repo", "chat", "body", "text", "event"]) if (Object.hasOwn(args, key) && args[key] != null && typeof args[key] !== "string") throw new GitHubError("Write fields must be strings", "invalid_params");
  if (Object.hasOwn(args, "number") && args.number != null && !isInteger(args.number)) throw new GitHubError("Thread number must be an exact visible integer", "invalid_params");
  const field = (key: string): string => Object.hasOwn(args, key) ? (args[key] ?? "") as string : "";
  const match = /^([^#]+)#(\d+)$/.exec(field("chat"));
  let repo = field("repo"), number = Object.hasOwn(args, "number") ? (args.number ?? 0) as Integer : 0;
  if (!repo && !number && match) { repo = match[1]!; number = decimal(match[2]!); }
  const body = (field("body") || field("text")).trim(), event = field("event");
  if (!validRepo(repo) || !isInteger(number) || number <= 0 || name === "github_comment" && !body || name === "github_review" && (!["APPROVE", "REQUEST_CHANGES", "COMMENT"].includes(event) || event !== "APPROVE" && !body)) throw new GitHubError("Invalid comment or review arguments", "invalid_params");
  return { repo, number, body, event };
}
export class GitHubConnector {
  readonly receipts = new WriteReceipts();
  readonly threadReader: ThreadReader;
  constructor(readonly auth: GitHubAuth, readonly service: GitHubService, readonly inbox: InboxPoller, readonly archive: ArchiveReader, readonly active = true) { this.threadReader = new ThreadReader(service, auth); service.client.onAuthenticationChange(() => this.receipts.resetAuthentication()); }
  async handle(method: string, params: Record<string, unknown> = {}, call?: ExactCall): Promise<unknown> {
    const raw = exactParams(params, call);
    const names: Record<string, string[]> = {
      "login.step": ["step"], "auth.set": ["source", "machine", "machine_name"], "machines.online": ["machine"],
      "inbox.reply": ["thread", "item_id", "last", "text", "client_id"], "archive.changes": ["cursor"],
      "tools.call": ["name"], "actions.describe": ["name"], "actions.do": ["name", "request_id"],
      "thread.page": ["thread", "before"], "thread.media": ["ref"],
    };
    params = { ...params, ...strings(raw, names[method] ?? []) };
    const contribution = strings(raw, ["contribution"]).contribution!;
    if (!this.active || contribution.trim() && contribution.trim() !== "github") throw new ExtError("No active GitHub connector", "no_contribution");
    if (["archive.changes", "thread.page"].includes(method)) {
      const limit = integer(raw.int64Fields(["limit"]).limit);
      params.limit = bounded(limit, 1, 100);
    }
    if (["tools.call", "actions.describe", "actions.do"].includes(method)) {
      const args = raw.field("args");
      if (!args) throw new GitHubError("Tool arguments are required", "invalid_params");
      if (method === "tools.call") params.args = readArgs(args);
      else params.args = { ...strings(args, ["repo", "body", "event", "chat", "text"]), number: integer(args.int64Fields(["number"]).number) };
    }
    if (method === "login.step") params.values = stringMapFields(raw, "values");
    if (method === "inbox.reply") for (const field of raw.fieldValues("changes")) params.changes = statusChanges(field);
    if (method === "options.set") {
      const options = raw.field("options");
      if (!options) throw new GitHubError("Options are required", "invalid_params");
      params.options = optionArgs(options);
    }
    if (method === "inbox.settle") for (const field of raw.fieldValues("threads")) params.threads = stringArray(field) ?? [];
    const epoch = this.auth.epoch;
    let result: unknown;
    switch (method) {
      case "manifest": result = { ...originalManifest.contributes.connectors[0], capabilities, hosts: ["github.com"], kinds }; break;
      case "status": result = this.service.client.low() && this.auth.status.state === "ok" ? { ...this.auth.status, state: "rate_limited" } : this.auth.status; break;
      case "login.step": result = await this.auth.step(String(params.step ?? "")); break;
      case "logout": await this.auth.logout(); result = {}; break;
      case "auth.get": result = this.auth.get(); break;
      case "auth.set": result = await this.auth.set(params as unknown as Wire.ConnectorAuthSource); break;
      case "machines.online": await this.auth.online(String(params.machine ?? "")); result = {}; break;
      case "options.get": result = { options: this.inbox.options }; break;
      case "options.set": this.inbox.setOptions(params.options as never); result = {}; break;
      case "inbox.collect": result = { things: this.inbox.collect() }; break;
      case "inbox.settle": result = { settled: this.inbox.settled((params.threads ?? []) as string[]) }; break;
      case "inbox.reply": {
        const a = writeArgs("github_comment", { chat: params.thread, text: params.text });
        const send = async () => { await this.service.write(a.repo, a.number, "comment", { body: a.body }); this.inbox.replied(String(params.thread)); return {}; };
        result = params.client_id ? await this.receipts.run(String(params.client_id), params, send) : await send(); break;
      }
      case "archive.changes": result = await this.archive.changes(String(params.cursor ?? ""), Number(params.limit ?? 50)); break;
      case "tools.list": result = { tools: tools.filter(t => !["github_comment", "github_review"].includes(t.name)) }; break;
      case "tools.call": result = { text: await this.service.readTool(String(params.name), (params.args ?? {}) as never, undefined, this.auth.locale) }; break;
      case "actions.list": result = { tools: tools.filter(t => ["github_comment", "github_review"].includes(t.name)) }; break;
      case "actions.describe": {
        const a = writeArgs(String(params.name), (params.args ?? {}) as Record<string, unknown>);
        const issue = await this.service.issue(a.repo, a.number, AbortSignal.timeout(5000)).catch(() => undefined);
        result = { action: { name: String(params.name), visible: a.repo }, summary: `${params.name} ${a.repo}#${a.number}${issue ? " " + issue.title : ""}${a.event ? " " + a.event : ""}\n\n${a.body}` }; break;
      }
      case "actions.do": {
        const a = writeArgs(String(params.name), (params.args ?? {}) as Record<string, unknown>);
        result = await this.receipts.run(String(params.request_id ?? ""), { name: params.name, args: a }, async () => {
          const response = await this.service.write(a.repo, a.number, params.name === "github_comment" ? "comment" : "review", { body: a.body, event: a.event }) as { html_url?: string };
          return { text: `${params.name} ${a.repo}#${a.number}: ${response.html_url ?? ""}` };
        }); break;
      }
      case "thread.page": result = await this.thread(params as unknown as Wire.ConnectorThreadParams); break;
      case "thread.media": result = await this.media(String(params.ref ?? "")); break;
      case "snapshot.get": case "items.list": case "items.doc_title": case "archive.media": throw new ExtError("The existing GitHub connector does not implement this capability", "no_capability");
      default: throw unknownMethod(`connector.${method}`);
    }
    if (!["auth.set", "logout", "login.step", "machines.online"].includes(method) && epoch !== this.auth.epoch) throw new GitHubError("Account changed during connector request", "auth_changed");
    if (bytes(stringify(result)) > bounds.rpc) throw new GitHubError("Connector result needs a smaller page or cursor", "response_too_large"); return result;
  }
  async thread(params: Wire.ConnectorThreadParams): Promise<Wire.ConnectorThreadPage> { return this.threadReader.page(params); }
  async media(reference: string): Promise<Wire.ConnectorMedia> {
    const url = reference.startsWith("avatar:https://") ? reference.slice(7) : reference.startsWith("avatar:") ? await this.threadReader.avatar(reference) : reference;
    if (!/^https:\/\/avatars\.githubusercontent\.com\//.test(url)) throw new GitHubError("Unknown avatar reference", "invalid_params");
    const response = await this.service.client.send(url, "GET", undefined, {}, undefined, true); if (!response.ok) throw new GitHubError("Avatar request failed");
    if (Number(response.headers.get("content-length")) > 4 << 20) { await response.body?.cancel(); throw new GitHubError("Avatar exceeds media budget", "response_too_large"); }
    const binary = await this.service.client.readBytes(response, 4 << 20);
    let data = ""; for (const byte of binary) data += String.fromCharCode(byte);
    return { mime: response.headers.get("content-type") || "image/png", data: btoa(data) };
  }
}
