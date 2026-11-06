import { existsSync } from "node:fs";

// Distribution fixtures come from the canonical schema, not separate handwritten copies.
const packaged = new URL("../fixtures/", import.meta.url);
export const testFixtures = existsSync(packaged)
  ? packaged
  : new URL("../../../schema/extension/examples/", import.meta.url);
