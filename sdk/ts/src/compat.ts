function first(...names: string[]): string {
  for (const name of names) if (process.env[name]) return process.env[name]!;
  return "";
}

export function extensionEnv(name: string): string {
  return first(`PERSONA_EXTENSION_${name}`, `PERSONA_PLUGIN_${name}`, `JARVIS_EXTENSION_${name}`, `JARVIS_PLUGIN_${name}`);
}

export function hubEnv(name: string): string {
  return first(`PERSONA_HUB_${name}`, `JARVIS_HUB_${name}`);
}
