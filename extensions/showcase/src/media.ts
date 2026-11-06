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
