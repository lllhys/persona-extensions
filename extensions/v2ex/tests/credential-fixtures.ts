/** The three original synthetic credential-negative URLs, using explicit public origins and URL fields. */
const topic = new URL("https://v2ex.com/t/123");
topic.username = "user";
const archive = new URL("https://registry.npmjs.org/example/-/example-1.0.0.tgz");
archive.username = "fixture";
archive.password = "synthetic";
const query = new URL("https://registry.npmjs.org/example/-/example-1.0.0.tgz");
query.searchParams.set("token", "synthetic");
export const credentialFixtures = Object.freeze({ topic: topic.href, archive: archive.href, query: query.href });
