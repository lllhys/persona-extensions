export interface DemoMedia {
  avatar: string;
  square: string;
  wide: string;
  broken: string;
}

// Public, repository-owned synthetic assets; no account or remote photo data.
const assets = "https://raw.githubusercontent.com/nwverse/persona-extensions/main/extensions/showcase/assets";
export const defaultMedia: Readonly<DemoMedia> = Object.freeze({
  avatar: `${assets}/avatar.png`,
  square: `${assets}/square.png`,
  wide: `${assets}/wide.png`,
  broken: `${assets}/intentionally-missing.png`,
});

/** Optional source-profile presentation URLs, never native or runtime fetch authority. */
export function configuredMedia(config: unknown): Readonly<DemoMedia> {
  const media = (config as { media?: Partial<DemoMedia> } | undefined)?.media;
  const out = { ...defaultMedia };
  for (const key of Object.keys(out) as (keyof DemoMedia)[]) {
    const value = media?.[key];
    if (typeof value !== "string" || value.length > 2048) continue;
    try {
      const url = new URL(value);
      if (url.protocol === "https:" && !url.username && !url.password) out[key] = value;
    } catch { /* Invalid optional presentation overrides keep the synthetic default. */ }
  }
  return Object.freeze(out);
}
