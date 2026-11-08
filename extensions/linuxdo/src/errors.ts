/** Validation errors carry a resource key, never a supplied account/link/secret. */
export type InputCode = "invalidTopic" | "invalidList" | "configObject" | "configWords" | "configString" | "configBoolean" | "configZone" | "argsObject" | "argInteger" | "argBoolean" | "argString" | "argWords";
export class InputError extends Error {
  constructor(readonly code: InputCode, readonly field = "") { super(code); }
}
