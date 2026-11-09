import { ExtError, noContribution, unknownMethod, withExactParams, type ExactJSON, type Handler } from "@persona/extension";
import { clip, decodeStruct, filter, latestReplies, length, nodeName, nonEmpty, oneLine, parseJSON, ReadError, tidy, topicID, topicURL, trimSpace, type Config, type Listing } from "./model.ts";
import { describe, errorText, fmt, meta, missedText, textsFor, time, title, topicMeta } from "./i18n.ts";
import { budget, V2EX } from "./service.ts";
export function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [
    { name: "v2ex_hot", description: t.toolHotDescription, input_schema: { type: "object", properties: {
      source: { type: "string", description: t.argSource }, filtered: { type: "boolean", description: t.argFiltered },
      keywords: { type: "array", items: { type: "string" }, description: t.argKeywords }, limit: { type: "integer", minimum: 1, maximum: 30, description: t.argLimit } } } },
    { name: "v2ex_topic", description: t.toolTopicDescription, input_schema: { type: "object", required: ["topic"], properties: {
      topic: { type: "string", description: t.argTopic }, replies: { type: "integer", minimum: 0, maximum: 50, description: t.argReplies } } } },
  ];
}
export const tools = toolsFor();
function exactArgs(raw: ExactJSON): Record<string, unknown> {
  const a: Record<string, unknown> = { limit: raw.int64Fields(["limit"]).limit };
  for (const field of ["source", "filtered", "keywords"] as const) for (const token of raw.fieldValues(field)) {
    const value: unknown = JSON.parse(token.raw);
    if (value === null) { if (field !== "source") delete a[field]; continue; }
    if (field === "source" ? typeof value !== "string" : field === "filtered" ? typeof value !== "boolean"
      : !Array.isArray(value) || !value.every(v => v === null || typeof v === "string")) throw new ReadError("invalid");
    a[field] = field === "keywords" ? (value as (string | null)[]).map(v => v ?? "") : value;
  }
  // Pointer null resets the default; scalar int64 null retains the prior value.
  for (const token of raw.fieldValues("replies")) a.replies = token.raw === "null" ? undefined : token.int64(a.replies as bigint | undefined);
  const topic = raw.field("topic");
  if (topic) a.topic = topic.raw.startsWith('"') ? JSON.parse(topic.raw) : topic.raw;
  return a;
}
function argsFrom(raw: unknown, exact?: ExactJSON): Record<string, unknown> {
  if (exact) return exactArgs(exact);
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const a = raw as Record<string, unknown>;
  if (a.source != null && typeof a.source !== "string" || a.filtered != null && typeof a.filtered !== "boolean") throw new ReadError("invalid");
  if (a.keywords != null && (!Array.isArray(a.keywords) || !a.keywords.every(v => typeof v === "string"))) throw new ReadError("invalid");
  // Direct fixture calls have no original JSON tokens. Runtime namespace calls
  // use the officially branded exact handler above, never reconstructed numbers.
  for (const key of ["limit", "replies"]) if (a[key] != null) {
    const v = a[key];
    if ((typeof v !== "number" || !Number.isInteger(v)) && typeof v !== "bigint" || BigInt(v as number | bigint) < -9223372036854775808n || BigInt(v as number | bigint) > 9223372036854775807n) throw new ReadError("invalid");
  }
  return a;
}
const count = (value: unknown, fallback: number, most: number, positiveDefault = false) => {
  if (value == null) return fallback;
  const n = BigInt(value as number | bigint);
  return positiveDefault && n <= 0n ? fallback : Number(n < 0n ? 0n : n > BigInt(most) ? BigInt(most) : n);
};
export async function callTool(service: V2EX, name: string, raw: unknown = {}, signal?: AbortSignal, exact?: ExactJSON): Promise<string> {
  const args = argsFrom(raw, exact), locale = service.locale, t = textsFor(locale), tz = service.timezone;
  const request = budget(signal, service.limits.tool);
  const note = (list: { stale?: ReadError; at: number }) => list.stale ? fmt(t.stale, errorText(list.stale, locale), time(list.at, tz)) + "\n" : "";
  try {
    if (name === "v2ex_hot") {
      const source = typeof args.source === "string" && trimSpace(args.source) ? trimSpace(args.source) : "hot";
      const limit = count(args.limit, 15, 30, true);
      let list: Listing, heading: string;
      if (source === "hot" || source === "latest" || source === "nodes") {
        if (source === "nodes" && !service.nodes.length) throw new ExtError(t.noNodes, "invalid");
        list = await service.list(source, false, request.signal); heading = `V2EX ${t[source]}`;
        if (!list.topics.length && list.missed.length) throw new ExtError(fmt(t.missed, missedText(list.missed, locale)));
      } else {
        if (!nodeName.test(source)) throw new ReadError("invalid");
        list = await service.node(source, service.limits.list, request.signal); heading = fmt(t.toolNode, source);
      }
      let topics = list.topics; const conditions: string[] = [];
      if (args.filtered !== false) { topics = filter(topics, service.config); const condition = describe(t, service.config); if (condition) conditions.push(condition); }
      const extra: Config = { keywords: nonEmpty(args.keywords as string[] | undefined) };
      if (extra.keywords!.length) { topics = filter(topics, extra); conditions.push(describe(t, extra)); }
      let out = fmt(t.toolCount, heading, list.topics.length);
      if (conditions.length) out += fmt(t.toolFiltered, clip(conditions.join(" · "), 1200), topics.length);
      if (topics.length > limit) out += fmt(t.toolLimit, limit);
      out += "\n" + note(list);
      if (list.missed.length) out += fmt(t.missed, missedText(list.missed, locale)) + "\n";
      let posts = "";
      for (const [i, topic] of topics.slice(0, limit).entries()) {
        posts += `\n${i + 1}. ${clip(title(t, topic), 300)} (id ${topic.id})\n   ${clip(meta(t, topic, service.now()), 400)} · ${topicURL(topic.id)}\n`;
        if (oneLine(topic.content)) posts += "   " + clip(oneLine(topic.content), 160) + "\n";
      }
      // Annotations have their own ceiling and can use only space left by posts.
      // Never truncate a selected topic's identity or URL to make room for notes.
      out = clip(out, Math.min(1999, Math.max(0, 29_998 - length(posts))));
      return (out + posts).trim();
    }
    if (name === "v2ex_topic") {
      const id = topicID(args.topic), replies = count(args.replies, 10, 50);
      const thread = await service.thread(id, replies > 0, false, request.signal), topic = thread.topic;
      let out = `「${clip(title(t, topic), 300)}」 ${topicURL(topic.id)}\n${clip(topicMeta(t, topic, tz), 400)}\n${note(thread)}`;
      if (tidy(topic.content)) out += "\n" + clip(tidy(topic.content), 8000) + "\n";
      if (thread.repliesErr) out += "\n" + fmt(t.repliesMissed, errorText(thread.repliesErr, locale)) + "\n";
      const latest = latestReplies(thread.replies, replies);
      if (latest.length) {
        out += "\n" + fmt(t.toolReplies, latest.length, thread.replies.length) + "\n";
        const headers = latest.map(({ reply, floor }) => `#${floor} ${clip(reply.author, 40)} (${time(BigInt(reply.created) * 1000n, tz)}): `);
        // Reserve all floors/headings first, then share the exact remaining Unicode budget.
        const remaining = 29_999 - length(out) - headers.reduce((n, h) => n + length(h) + 1, 0);
        const each = Math.min(800, Math.max(0, Math.floor(remaining / latest.length) - 1));
        latest.forEach(({ reply }, i) => { out += headers[i] + clip(oneLine(reply.content), each) + "\n"; });
      }
      return clip(out.trim(), 29_999);
    }
    throw unknownMethod(`connector.tools.${name}`);
  } finally { request.dispose(); }
}
export function connector(getService: V2EX | (() => V2EX)): Handler { return withExactParams(async (method, params: unknown, call) => {
  const service = typeof getService === "function" ? getService() : getService;
  if (params != null && (typeof params !== "object" || Array.isArray(params))) throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  let p = (params ?? {}) as Record<string, unknown>;
  if (call?.params) {
    const decoded = decodeStruct(parseJSON(call.params.raw), { contribution: "string", name: "string", args: "raw" });
    if (decoded.errors) throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
    p = decoded.value;
  }
  if (p.contribution != null && typeof p.contribution !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  if (p.contribution && p.contribution !== "v2ex") throw noContribution(String(p.contribution));
  if (method === "status") return service.status();
  if (method === "tools.list") return { tools: toolsFor(service.locale) };
  if (method === "tools.call") {
    if (typeof p.name !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
    try { return { text: await callTool(service, p.name, call ? undefined : p.args, undefined, call?.params?.field("args")) }; }
    catch (error) { if (error instanceof ReadError) throw new ExtError(errorText(error, service.locale), error.code === "invalid" ? "invalid" : undefined); throw error; }
  }
  throw unknownMethod("connector." + method);
}); }
