import { ReadError } from "./http.ts";
/** Domain failures stay distinct from provider reads and encrypted-state writes. */
export class MutationError extends Error {
  constructor(readonly code: "invalid" | "watchLimit" | "alertLimit" | "duplicateAlert" | "alertMet" | "saveError", message: string) { super(message); }
}
export function actionError(error: unknown): string {
  if (error instanceof MutationError) return error.code;
  if (error instanceof ReadError) return error.code === "not_found" ? "missing" : error.code === "rate_limited" ? "rateError" : "error";
  return "error";
}
