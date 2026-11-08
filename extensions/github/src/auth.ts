import type * as Wire from "@persona/extension/wire";
import { ExtError } from "@persona/extension";
import { GitHubClient, GitHubError } from "./client.ts";
import { b64decode, b64encode, abort, type Config, type Host, type User } from "./model.ts";
import { translations, errorText } from "./localization.ts";
const clientID = "178c6fc778ccc68e1d6a"; // Public GitHub CLI OAuth application; not a credential.
export function missingScopes(scopes: string[], locale?: string): { scope: string; affects: string[] }[] {
  const t = translations(locale); if (scopes.includes("repo")) return [];
  const out = [{ scope: "repo", affects: [t.scopePrivate, scopes.includes("public_repo") ? t.scopePublic : t.scopeWrite] }];
  if (!scopes.includes("notifications")) out.push({ scope: "notifications", affects: [t.scopeNotify] }); return out;
}
/** Host APIs below are generated wire contracts and already implemented host methods. */
export class GitHubAuth {
  private value = ""; private device = ""; private loaded = false;
  private pending?: { code: string; user: string; uri: string; interval: number; expires: number };
  private acquiring?: Promise<string>; private generation = 0; private account = "";
  private saving: Promise<void> = Promise.resolve();
  get epoch(): number { return this.generation; }
  private changed(): void { this.generation++; this.acquiring = undefined; this.client.resetAuthentication(); }
  private current(generation: number): void { if (generation !== this.generation) throw new GitHubError("Authentication source changed; retry the request", "auth_changed"); }
  source: "machine" | "device"; machine: string; scopes: string[] = []; scopesKnown = false;
  status: Wire.ConnectorStatus = { state: "not_signed_in" };
  client!: GitHubClient;
  constructor(readonly hub: Host, config: Config, readonly now = Date.now, readonly locale = "en") { this.machine = config.auth?.from_machine ?? ""; this.source = this.machine ? "machine" : "device"; }
  async load(): Promise<void> {
    if (this.loaded) return;
    const generation = this.generation, params: Wire.ConnectorParams = { contribution: "github" };
    const secret = await this.hub.call<Wire.ConnectorSecret>("connector.secrets.load", params); this.current(generation);
    if (secret.data) { try { this.device = String(JSON.parse(b64decode(secret.data)).token ?? ""); } catch { throw new GitHubError("Invalid sealed connector state", "invalid_secret"); } }
    this.loaded = true;
  }
  async save(token: string, generation = this.generation): Promise<void> {
    const params: Wire.ConnectorSecretSaveParams = { contribution: "github", data: b64encode(JSON.stringify({ ...(token ? { token } : {}) })) };
    // Order durable mutations too: logout cannot finish while an older save could still overwrite its secret.
    const operation = this.saving.catch(() => {}).then(async () => {
      this.current(generation);
      await this.hub.call("connector.secrets.save", params); this.current(generation);
      this.device = token; this.loaded = true;
    });
    this.saving = operation; await operation;
  }
  principal(): string { return this.status.state === "ok" ? this.status.account ?? "" : ""; }
  async token(signal?: AbortSignal): Promise<string> { abort(signal); if (this.value) return this.value; return this.refresh(signal); }
  async refresh(signal?: AbortSignal): Promise<string> {
    abort(signal);
    const generation = this.generation, source = this.source, machine = this.machine;
    const request = this.acquiring ??= (async () => {
      await this.load(); this.current(generation);
      if (source === "device") { if (!this.device) throw new GitHubError("GitHub is not signed in", "not_signed_in"); return this.value = this.device; }
      const params: Wire.ConnectorGhTokenParams = { contribution: "github", machine, host: "github.com" };
      try { const answer = await this.hub.call<Wire.ConnectorGhToken>("connector.credentials.gh_token", params); this.current(generation); if (!answer.token) throw new GitHubError("GitHub is not signed in", "not_signed_in"); return this.value = answer.token; }
      catch (error) { this.current(generation); this.status = { state: "not_signed_in", error: errorText(error, this.locale) }; throw new GitHubError("Could not read the computer's gh login", error instanceof ExtError ? error.code : "error"); }
    })();
    try { const value = await request; this.current(generation); abort(signal); return value; } finally { if (this.acquiring === request) this.acquiring = undefined; }
  }
  async check(signal?: AbortSignal): Promise<Wire.ConnectorStatus> {
    const generation = this.generation;
    try {
      const response = await this.client.send("/user", "GET", undefined, {}, signal);
      if (!response.ok) { await response.body?.cancel(); throw new GitHubError(`GitHub HTTP ${response.status}`, response.status === 401 ? "not_signed_in" : "no_permission", response.status); }
      const user = JSON.parse(await this.client.text(response, 1 << 20)) as User; this.current(generation);
      if (this.account && this.account.toLowerCase() !== user.login.toLowerCase()) this.changed();
      this.account = user.login;
      this.scopesKnown = response.headers.has("x-oauth-scopes"); this.scopes = (response.headers.get("x-oauth-scopes") ?? "").split(",").map(v => v.trim()).filter(Boolean);
      this.status = { state: "ok", account: user.login, via: this.source === "device" ? translations(this.locale).deviceSource : translations(this.locale).machineSource };
    } catch (e) { abort(signal); if (generation !== this.generation) return this.status; this.status = { state: e instanceof GitHubError && e.code === "no_permission" ? "no_permission" : "not_signed_in", error: errorText(e, this.locale) }; }
    return this.status;
  }
  get(): Wire.ConnectorAuth { return { source: this.source, ...(this.source === "machine" ? { machine: this.machine } : {}), scopes: this.scopes, scopes_known: this.scopesKnown }; }
  async set(params: Wire.ConnectorAuthSource): Promise<Wire.ConnectorAuth> {
    if (params.source !== "device" && params.source !== "machine") throw new GitHubError("Unknown authentication source", "invalid_params");
    if (params.source === "machine" && !params.machine) throw new GitHubError("A computer is required", "invalid_params");
    this.source = params.source; this.machine = params.machine ?? ""; this.value = ""; this.pending = undefined; this.account = ""; this.scopesKnown = false; this.scopes = []; this.status = { state: "not_signed_in" }; this.changed();
    const generation = this.generation;
    if (params.source === "machine") await this.save("");
    this.current(generation); await this.check(); return this.get();
  }
  async online(machine: string): Promise<void> { if (this.source === "machine" && this.machine === machine) await this.check(); }
  private prompt(error?: string): Wire.ConnectorPrompt { const p = this.pending!; return { step: "device", kind: "device_code", url: p.uri, user_code: p.user, interval: p.interval, hint: translations(this.locale).deviceHint, ...(error ? { error } : {}) }; }
  async start(signal?: AbortSignal, error?: string): Promise<Wire.ConnectorPrompt> {
    if (this.source !== "device") throw new GitHubError("Select device login first", "invalid_params");
    const generation = this.generation;
    const response = await this.client.form("/login/device/code", { client_id: clientID, scope: "repo" }, signal); this.current(generation);
    if (typeof response.device_code !== "string" || typeof response.user_code !== "string" || typeof response.verification_uri !== "string") throw new GitHubError("GitHub did not return a device code", "invalid_response");
    this.pending = { code: response.device_code, user: response.user_code, uri: response.verification_uri, interval: Math.max(5, Number(response.interval) || 5), expires: this.now() + Number(response.expires_in) * 1000 };
    return this.prompt(error);
  }
  async step(step = "", signal?: AbortSignal): Promise<Wire.ConnectorPrompt> {
    if (step === "") return this.start(signal);
    if (step !== "device" || this.source !== "device") throw new GitHubError("Unknown login step or source", "invalid_params");
    if (!this.pending) return this.start(signal);
    const p = this.pending, t = translations(this.locale), generation = this.generation;
    if (this.now() >= p.expires) return this.start(signal, t.expired);
    const response = await this.client.form("/login/oauth/access_token", { client_id: clientID, device_code: p.code, grant_type: "urn:ietf:params:oauth:grant-type:device_code" }, signal);
    this.current(generation);
    if (response.access_token) { if (typeof response.access_token !== "string") throw new GitHubError("Invalid device token", "invalid_response"); await this.save(response.access_token, generation); this.current(generation); this.value = this.device; this.pending = undefined; const st = await this.check(signal); this.current(generation); return st.state === "ok" ? { done: true, account: st.account } : { step: "device", error: t.error }; }
    switch (response.error) { case "authorization_pending": return this.prompt(); case "slow_down": p.interval = Math.max(Number(response.interval) || 0, p.interval + 5); return this.prompt(); case "expired_token": return this.start(signal, t.expired); case "access_denied": return this.start(signal, t.denied); default: return this.start(signal, t.error); }
  }
  async logout(): Promise<void> {
    this.pending = undefined; this.value = ""; this.device = ""; this.loaded = true; this.account = ""; this.scopes = []; this.scopesKnown = false; this.status = { state: "not_signed_in" }; this.changed();
    const generation = this.generation; await this.save("", generation); this.current(generation); await this.check();
  }
}
