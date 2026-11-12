import { ExtError, noContribution, unknownMethod, withExactParams, type ExactJSON, type Handler } from "@persona/extension";
import { clip, questionID, queryText, ReadError, selectedAnswer, siteID, sites, tagText, type Answer, type Question } from "./model.ts";
import { errorText, fmt, textsFor } from "./i18n.ts";
import { budget, type StackExchange } from "./service.ts";
export function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [
    { name: "stackexchange_search", description: t.toolSearch, input_schema: { type: "object", required: ["query"], properties: { query: { type: "string", description: t.query }, site: { type: "string", enum: Object.keys(sites), description: t.site }, tag: { type: "string", description: t.tag }, limit: { type: "integer", minimum: 1, maximum: 20, description: t.limit } } } },
    { name: "stackexchange_answer", description: t.toolAnswer, input_schema: { type: "object", required: ["question_id"], properties: { question_id: { type: "integer", minimum: 1, description: t.id }, site: { type: "string", enum: Object.keys(sites), description: t.site } } } },
  ];
}
export const tools = toolsFor();
function questionLine(question: Question, index: number): string {
  return `${index + 1}. ${clip(question.title, 500)} (${question.id})\n   ${question.score} votes · ${question.answerCount} answers · ${question.tags.map(tag => "#" + tag).join(" ")}\n   ${question.url}`;
}
function answerText(answer: Answer): string {
  return `${answer.accepted ? "Accepted" : "Highest-voted"} answer ${answer.id} · ${answer.score} votes\nBy ${answer.author} · ${answer.license}\n${answer.url}\n\n${answer.body}`;
}
function argsFrom(raw: unknown, exact?: ExactJSON): Record<string, unknown> {
  if (exact) {
    const args: Record<string, unknown> = {};
    for (const field of ["query", "site", "tag"]) for (const token of exact.fieldValues(field)) {
      const value: unknown = JSON.parse(token.raw); if (typeof value !== "string") throw new ReadError("invalid"); args[field] = value;
    }
    const limit = exact.int64Fields(["limit", "question_id"]);
    if (limit.limit !== 0n) args.limit = Number(limit.limit);
    if (limit.question_id !== 0n) args.question_id = Number(limit.question_id);
    return args;
  }
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = raw as Record<string, unknown>;
  for (const field of ["query", "site", "tag"]) if (value[field] != null && typeof value[field] !== "string") throw new ReadError("invalid");
  for (const field of ["limit", "question_id"]) if (value[field] != null && (typeof value[field] !== "number" || !Number.isSafeInteger(value[field]))) throw new ReadError("invalid");
  return value;
}
export async function callTool(service: StackExchange, name: string, raw: unknown = {}, signal?: AbortSignal, exact?: ExactJSON): Promise<string> {
  const args = argsFrom(raw, exact), t = textsFor(service.locale), request = budget(signal, service.limits.tool);
  try {
    const site = siteID(args.site);
    if (name === "stackexchange_search") {
      const query = queryText(args.query), limit = args.limit == null ? 10 : Number(args.limit), tag = tagText(args.tag);
      if (!Number.isSafeInteger(limit) || limit < 1 || limit > 20) throw new ReadError("invalid");
      const result = await service.search(query, site, tag, limit, false, request.signal);
      return clip((result.questions.length ? result.questions.map(questionLine).join("\n") : t.empty) + `\n${t.attribution}${result.stale ? `\n${errorText(result.stale, service.locale)}` : ""}`, 29_999);
    }
    if (name === "stackexchange_answer") {
      const id = questionID(args.question_id), result = await service.answers(id, site, false, request.signal);
      return clip(answerText(selectedAnswer(result.answers)) + `\n\n${t.attribution}${result.stale ? `\n${errorText(result.stale, service.locale)}` : ""}`, 29_999);
    }
    throw unknownMethod(`connector.tools.${name}`);
  } finally { request.dispose(); }
}
export function connector(getService: () => StackExchange): Handler { return withExactParams(async (method, params: unknown, call) => {
  const service = getService();
  if (params != null && (typeof params !== "object" || Array.isArray(params))) throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  const p = (params ?? {}) as Record<string, unknown>;
  if (p.contribution != null && typeof p.contribution !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  if (p.contribution && p.contribution !== "stackexchange") throw noContribution(String(p.contribution));
  if (method === "status") return service.status();
  if (method === "tools.list") return { tools: toolsFor(service.locale) };
  if (method === "tools.call") {
    if (typeof p.name !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
    try { return { text: await callTool(service, p.name, p.args, undefined, call?.params?.field("args")) }; }
    catch (error) { if (error instanceof ReadError) throw new ExtError(errorText(error, service.locale), error.code === "invalid" ? "invalid" : undefined); throw error; }
  }
  throw unknownMethod("connector." + method);
}); }
