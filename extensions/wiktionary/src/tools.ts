import { ExtError, noContribution, unknownMethod, withExactParams, type ExactJSON, type Handler } from "@persona/extension";
import { clip, ReadError, type Entry } from "./model.ts";
import { errorText, textsFor } from "./i18n.ts";
import { budget, Wiktionary } from "./service.ts";
export function toolsFor(locale = "en") {
  const t = textsFor(locale);
  return [
    { name: "wiktionary_lookup", description: t.toolLookup, input_schema: { type: "object", required: ["word"], properties: { word: { type: "string", description: t.word } } } },
  ];
}
export const tools = toolsFor();
function text(entry: Entry): string {
  return `${entry.word}\n${entry.url}\n\n${entry.groups.map(group => `${group.partOfSpeech}${group.language ? ` (${group.language})` : ""}\n${group.definitions.map((definition, index) => `${index + 1}. ${clip(definition.text, 1600)}${definition.examples.length ? `\n   ${definition.examples.map(example => `Example: ${clip(example.text, 400)}`).join("\n   ")}` : ""}`).join("\n")}`).join("\n\n")}`;
}
function argsFrom(raw: unknown, exact?: ExactJSON): Record<string, unknown> {
  if (exact) {
    const args: Record<string, unknown> = {};
    for (const token of exact.fieldValues("word")) {
      const value: unknown = JSON.parse(token.raw); if (typeof value !== "string") throw new ReadError("invalid"); args.word = value;
    }
    return args;
  }
  if (raw == null) return {};
  if (typeof raw !== "object" || Array.isArray(raw)) throw new ReadError("invalid");
  const value = raw as Record<string, unknown>;
  if (value.word != null && typeof value.word !== "string") throw new ReadError("invalid");
  return value;
}
export async function callTool(service: Wiktionary, name: string, raw: unknown = {}, signal?: AbortSignal, exact?: ExactJSON): Promise<string> {
  const args = argsFrom(raw, exact), request = budget(signal, service.limits.tool);
  try {
    if (name === "wiktionary_lookup") {
      if (typeof args.word !== "string") throw new ReadError("invalid");
      return clip(text(await service.lookup(args.word, false, request.signal)), 29_999);
    }
    throw unknownMethod(`connector.tools.${name}`);
  } finally { request.dispose(); }
}
export function connector(getService: () => Wiktionary): Handler { return withExactParams(async (method, params: unknown, call) => {
  const service = getService();
  if (params != null && (typeof params !== "object" || Array.isArray(params))) throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  const p = (params ?? {}) as Record<string, unknown>;
  if (p.contribution != null && typeof p.contribution !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
  if (p.contribution && p.contribution !== "wiktionary") throw noContribution(String(p.contribution));
  if (method === "status") return service.status();
  if (method === "tools.list") return { tools: toolsFor(service.locale) };
  if (method === "tools.call") {
    if (typeof p.name !== "string") throw new ExtError(errorText(new ReadError("invalid"), service.locale), "invalid");
    try { return { text: await callTool(service, p.name, p.args, undefined, call?.params?.field("args")) }; }
    catch (error) { if (error instanceof ReadError) throw new ExtError(errorText(error, service.locale), error.code === "invalid" ? "invalid" : undefined); throw error; }
  }
  throw unknownMethod("connector." + method);
}); }
