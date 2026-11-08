import { expect, test } from 'bun:test';
import { env } from './index';

test('compat extension environment reads old names and prefers current names', () => {
  const names = ['PERSONA_EXTENSION_ID','PERSONA_PLUGIN_ID','JARVIS_EXTENSION_ID','JARVIS_PLUGIN_ID','PERSONA_HUB_URL','JARVIS_HUB_URL','PERSONA_HUB_TOKEN','JARVIS_HUB_TOKEN'];
  const saved = names.map((name) => [name, process.env[name]] as const);
  try {
    for (const name of names) delete process.env[name];
    process.env.JARVIS_PLUGIN_ID = 'old';
    process.env.JARVIS_HUB_URL = 'https://hub.example.com';
    process.env.JARVIS_HUB_TOKEN = 'fixture';
    expect(env().id).toBe('old');
    expect(env().hubURL).toBe('https://hub.example.com');
    expect(env().hubToken).toBe('fixture');
    process.env.PERSONA_PLUGIN_ID = 'transitional';
    expect(env().id).toBe('transitional');
    process.env.PERSONA_EXTENSION_ID = 'current';
    expect(env().id).toBe('current');
  } finally {
    for (const [name, value] of saved) { if (value === undefined) delete process.env[name]; else process.env[name] = value; }
  }
});
