import { noContribution, unknownMethod, withExactParams, type ExactJSON, type Handler } from "@persona/extension";
import { clip, filter, oneLine, opening, topicID, topicURL, inInt64 } from "./model.ts";
import { date, describe, fmt, meta, textsFor, topicMeta } from "./i18n.ts";
import { plainText } from "./text.ts";
import { bestReplies } from "./topic.ts";
import { errorText, ReadError, type LinuxDo } from "./service.ts";
import { InputError } from "./errors.ts";

export function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [
    { name: "linuxdo_hot", description: t.hotDescription, input_schema: { type: "object", properties: {
      list: { type: "string", enum: ["hot", "daily", "weekly", "latest"], description: t.listArgument },
      filtered: { type: "boolean", description: t.filteredArgument },
      keywords: { type: "array", items: { type: "string" }, description: t.keywordsArgument },
      limit: { type: "integer", minimum: 1, maximum: 30, description: t.limitArgument },
    } } },
    { name: "linuxdo_topic", description: t.topicDescription, input_schema: { type: "object", required: ["topic"], properties: {
      topic: { type: "string", description: t.topicArgument },
      replies: { type: "integer", minimum: 0, maximum: 20, description: t.repliesArgument },
    } } },
  ];
}
export const tools = toolsFor();

function exactToolArgs(name: string, exact: ExactJSON): Record<string, unknown> {
  const parsed: unknown = JSON.parse(exact.raw);
  if (parsed === null) return {};
  if (typeof parsed !== "object" || Array.isArray(parsed)) throw new InputError("argsObject");
  const args = { ...parsed } as Record<string, unknown>;
  // Go decodes every typed integer field before selecting a tool. Only project
  // into Number after lexical/range checks and the original bounded defaults.
  let limit: bigint;
  try { limit = exact.int64Fields(["limit"]).limit; }
  catch { throw new InputError("argInteger", "limit"); }
  args.limit = Number(limit >= 1n && limit <= 30n ? limit : 15n);
  // Replies is *int, not int: null resets the pointer to the default, including
  // after an earlier duplicate. A previous invalid value must still fail.
  let replies: bigint | undefined;
  for (const value of exact.fieldValues("replies")) {
    if (value.raw === "null") replies = undefined;
    else {
      try { replies = value.int64(); }
      catch { throw new InputError("argInteger", "replies"); }
    }
  }
  args.replies = replies === undefined ? undefined : Number(replies < 0n ? 0n : replies > 20n ? 20n : replies);
  if (name === "linuxdo_topic") {
    // Topic is RawMessage: its last folded occurrence wins, including null.
    // Retain the string/link branch and the original numeric token (even -0).
    const topic = exact.field("topic");
    if (!topic || topic.raw === "null") args.topic = undefined;
    else if (topic.raw.startsWith('"')) args.topic = JSON.parse(topic.raw);
    else {
      try { topic.int64(); }
      catch { throw new InputError("invalidTopic"); }
      args.topic = topic.raw;
    }
  }
  return args;
}

export async function callTool(service: LinuxDo, name: string, raw: unknown = {}, signal?: AbortSignal, exact?: ExactJSON): Promise<string> {
  const a = exact ? exactToolArgs(name, exact) : raw === null ? {} : raw;
  if (typeof a !== "object" || Array.isArray(a)) throw new InputError("argsObject");
  // Go's optional JSON fields treat null as absent, not as an invalid value.
  const args = Object.fromEntries(Object.entries(a as Record<string, unknown>).filter(([, value]) => value !== null));
  // Direct callers may provide only decoded numbers. RPC calls opt into the
  // official raw view above; no schema change or unsafe-ID narrowing is needed.
  const integer = (key: string) => { if (args[key] !== undefined && (typeof args[key] !== "number" || !inInt64(args[key]))) throw new InputError("argInteger", key); };
  const t = textsFor(service.locale), zone = service.zone();
  if (name === "linuxdo_hot") {
    integer("limit");
    if (args.filtered !== undefined && typeof args.filtered !== "boolean") throw new InputError("argBoolean", "filtered");
    if (args.list !== undefined && typeof args.list !== "string") throw new InputError("argString", "list");
    if (args.keywords !== undefined && (!Array.isArray(args.keywords) || !args.keywords.every(x => typeof x === "string"))) throw new InputError("argWords", "keywords");
    const list = (args.list as string) || "hot", keywords = (args.keywords as string[] | undefined) ?? [];
    const limit = typeof args.limit === "number" && args.limit >= 1 && args.limit <= 30 ? args.limit : 15;
    const reading = await service.listing(list, false, signal), filtered = args.filtered !== false;
    const f = filtered ? service.config : { keywords }, narrow = filtered ? { keywords } : {};
    const topics = filter(filter(reading.value, f), narrow);
    const descriptions = [describe(t, f), filtered && keywords.some(x => x.trim()) ? fmt(t.narrow, keywords.filter(x => x.trim()).join("、")) : ""].filter(Boolean).join(" · ");
    let header = fmt(t.toolList, t.lists[list as keyof typeof t.lists], reading.value.length);
    if (descriptions) header += fmt(t.toolFiltered, descriptions, topics.length);
    if (topics.length > limit) header += fmt(t.toolLimit, limit);
    const lines = topics.slice(0, limit).map((topic, i) => `${i + 1}. ${topic.title} (id ${topic.id})\n   ${meta(t, topic, service.now())} · ${topic.url}${topic.excerpt ? "\n   " + clip(topic.excerpt, 160) : ""}`);
    return clip([header, reading.stale ? fmt(t.stale, errorText(reading.stale, service.locale)) : "", ...lines].filter(Boolean).join("\n\n"), 30000);
  }
  if (name === "linuxdo_topic") {
    integer("replies"); const id = topicID(args.topic), count = args.replies === undefined ? 10 : Math.max(0, Math.min(20, args.replies as number));
    const reading = await service.thread(id, signal), topic = reading.value, op = opening(topic), replies = bestReplies(topic, count);
    return clip([`「${oneLine(topic.title)}」 ${topicURL(topic.id)}\n${topicMeta(t, topic, zone)}`,
      reading.stale ? fmt(t.stale, errorText(reading.stale, service.locale)) : "", op ? clip(plainText(op.cooked, t), 8000) : "",
      replies.length ? fmt(t.toolReplies, topic.posts.length) + "\n" + replies.map(r => `#${r.number} ${r.username} (${date(r.at, zone)}, ${fmt(t.likes, r.likes)}): ${clip(oneLine(plainText(r.cooked, t)), 800)}`).join("\n") : ""].filter(Boolean).join("\n\n"), 30000);
  }
  throw unknownMethod(name);
}
export const connector = (source: LinuxDo | (() => LinuxDo)): Handler => withExactParams(async (method, params, call) => {
  const service = typeof source === "function" ? source() : source;
  if (params.contribution && params.contribution !== "linuxdo") throw noContribution(params.contribution);
  if (method === "status") return service.status();
  if (method === "tools.list") return { tools: toolsFor(service.locale) };
  if (method === "tools.call") {
    try { return { text: await callTool(service, params.name, params.args, undefined, call?.params?.field("args")) }; }
    catch (error) {
      if (error instanceof ReadError || error instanceof InputError) throw new Error(errorText(error, service.locale));
      throw error;
    }
  }
  throw unknownMethod(`connector.${method}`);
});
