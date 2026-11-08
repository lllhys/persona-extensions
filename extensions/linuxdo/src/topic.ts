import { topicID, decimalInt64, type Topic, type Post, type Integer } from "./model.ts";
import { parse, isLosslessNumber } from "lossless-json";
import { plainText } from "./text.ts";
import { topicTime } from "./time.ts";
const str = (value: unknown) => {
  if (value === undefined || value === null) return "";
  if (typeof value !== "string") throw new Error("invalid topic string");
  return value;
};
const num = (value: unknown) => {
  if (value === undefined || value === null) return 0;
  if (!isLosslessNumber(value) || !Number.isFinite(Number(value.value))) throw new Error("invalid topic number");
  return Number(value.value);
};
const exactInteger = (value: bigint): Integer => Number.isSafeInteger(Number(value)) ? Number(value) : value.toString();
const integer = (value: unknown): Integer => {
  if (value === undefined || value === null) return 0;
  // Go integer fields reject decimal/exponent lexemes, even when JS rounds them
  // to an integer. Validate the original token before any numeric conversion.
  if (!isLosslessNumber(value) || !/^-?\d+$/u.test(value.value)) throw new Error("invalid topic integer");
  return exactInteger(decimalInt64(value.value));
};
const record = (value: unknown): Record<string, unknown> => {
  if (value === undefined || value === null) return {};
  if (typeof value !== "object" || Array.isArray(value) || isLosslessNumber(value)) throw new Error("invalid topic object");
  return value as Record<string, unknown>;
};
const arr = (value: unknown): unknown[] => {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new Error("invalid topic array");
  return value;
};
const id = (value: unknown) => {
  if (value === undefined || value === null) return "0";
  if (!isLosslessNumber(value) || !/^\d+$/u.test(value.value)) throw new Error("invalid topic identifier");
  return topicID(value.value);
};
/** Preserve signed-int64 identifiers without rounding; project only the public fields we consume. */
export function parseTopic(raw: string): Topic {
  JSON.parse(raw); // Reject invalid JSON before interpreting lossless numeric tokens.
  const doc = record(parse(raw));
  const posts: Post[] = arr(record(doc.post_stream).posts).map(value => {
    const p = record(value);
    let likes = BigInt(integer(p.reaction_users_count));
    for (const value of arr(p.actions_summary)) {
      const a = record(value), action = integer(a.id), count = BigInt(integer(a.count));
      if (action === 2 && count > likes) likes = count;
    }
    if (p.hidden !== undefined && p.hidden !== null && typeof p.hidden !== "boolean") throw new Error("invalid topic visibility");
    return { id: id(p.id), username: str(p.username), at: topicTime(p.created_at), cooked: str(p.cooked), number: integer(p.post_number), type: integer(p.post_type), hidden: p.hidden === true, score: num(p.score), likes: exactInteger(likes) };
  });
  // Tags were best-effort in Go; malformed tag metadata must not hide the body.
  const names = (Array.isArray(doc.tags) ? doc.tags : []).map(value => typeof value === "string" ? value : value && typeof value === "object" && typeof value.name === "string" ? value.name : "").filter(Boolean);
  return { id: id(doc.id), title: str(doc.title), postsCount: integer(doc.posts_count), likeCount: integer(doc.like_count), views: integer(doc.views), people: integer(doc.participant_count), at: topicTime(doc.created_at), tags: names, posts };
}
export function bestReplies(topic: Topic, n: number): Post[] {
  const seen = new Map<string, Set<Integer>>();
  return topic.posts.filter(p => BigInt(p.number) > 1n && p.type === 1 && !p.hidden && plainText(p.cooked).trim())
    .sort((a, b) => b.score - a.score || (BigInt(a.number) < BigInt(b.number) ? -1 : BigInt(a.number) > BigInt(b.number) ? 1 : 0)).filter(p => {
      // Keep the first ranked occurrence of each exact ID/floor pair. Separate
      // sets retain strict number/string equality without rescanning the array.
      let floors = seen.get(p.id);
      if (!floors) { floors = new Set<Integer>(); seen.set(p.id, floors); }
      if (floors.has(p.number)) return false;
      floors.add(p.number); return true;
    }).slice(0, n);
}
