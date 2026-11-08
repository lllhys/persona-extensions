import { test } from "node:test";
import assert from "node:assert/strict";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { Fixture, deferred, origin, sample, syntheticDSN } from "./fixtures.ts";

async function bounded<T>(work: Promise<T>, timeout = 1000): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([work, new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error(`local control exceeded ${timeout} ms`)), timeout);
    })]);
  } finally { clearTimeout(timer); }
}

// These exported local controls are not a real-hub shutdown or SIGTERM test.
for (const mode of ["deadline-resolve", "deadline-reject", "deadline-abort", "abort-alone", "abort-drain"] as const) {
  test(`round3: local stop fences a non-cooperative digest (${mode})`, { timeout: 3000 }, async () => {
    const imported = await import(pathToFileURL(resolve(".source-dist/sentry/bundle/main.mjs")).href);
    const started = deferred<void>(), resume = deferred<void>(), retired = deferred<void>();
    const oldDigest = crypto.subtle.digest.bind(crypto.subtle);
    let first = true;
    crypto.subtle.digest = (async (...args: Parameters<typeof oldDigest>) => {
      if (!first) return oldDigest(...args);
      first = false; started.resolve();
      try { await resume.promise; return await oldDigest(...args); }
      finally { retired.resolve(); }
    }) as typeof crypto.subtle.digest;
    const cancel = new AbortController(), oldFixture = new Fixture(), nextFixture = new Fixture();
    const logs: string[] = [], unhandled: unknown[] = [];
    const onUnhandled = (error: unknown) => { unhandled.push(error); };
    process.on("unhandledRejection", onUnhandled);
    const old = imported.createRuntime({ fetch: oldFixture.fetch, signal: cancel.signal, log: (message: string) => logs.push(message) }).service;
    const next = imported.createRuntime({ fetch: nextFixture.fetch }).service;
    try {
      old.configure({}, syntheticDSN);
      old.deliver([sample("hashing"), sample("queued")]); await bounded(started.promise);
      assert.equal(old.state().inFlight, true); assert.equal(old.state().queued, 1);
      let stop: Promise<boolean>;
      if (mode === "abort-alone") { cancel.abort(); stop = old.stop(1000); }
      else {
        stop = old.stop(mode === "abort-drain" ? 1000 : 5);
        if (mode === "abort-drain" || mode === "deadline-abort") cancel.abort();
      }
      assert.equal(old.stop(0), stop, "stop remains idempotent");
      assert.equal(await bounded(stop, 40), false, "must stop without waiting for digest settlement");
      assert.deepEqual(old.state(), { phase: "stopped", queued: 0, inFlight: false, sent: 0, dropped: 2, pausedUntil: 0 });
      assert.equal(oldFixture.hits.length, 0);
      assert.throws(() => old.configure({}, syntheticDSN), "a stopped service is terminal");
      old.deliver([sample("after-stop")]);
      const stoppedState = old.state();

      // A separate Program lifetime can start and drain while the old hash is
      // still pending; terminal stop does not add a new restart API.
      next.configure({ destination: origin, environment: "next" }, syntheticDSN);
      next.deliver([sample("next-before-settlement")]);
      assert.equal(await bounded(next.flush(1000)), true);
      assert.equal(nextFixture.hits.length, 1); assert.equal(next.state().sent, 1);
      const nextState = next.state();
      if (mode === "deadline-reject") resume.reject(new Error("synthetic detached digest rejection"));
      else resume.resolve();
      await bounded(retired.promise); await new Promise(resolve => setTimeout(resolve, 10));
      assert.deepEqual(old.state(), stoppedState, "late digest cannot fetch, log or count a second drop");
      assert.deepEqual(next.state(), nextState, "retired generation cannot alter the new lifetime");
      assert.equal(oldFixture.hits.length, 0); assert.deepEqual(logs, []); assert.deepEqual(unhandled, []);
      next.deliver([sample("next-after-settlement")]);
      assert.equal(await bounded(next.flush(1000)), true); assert.equal(next.state().sent, 2);
      assert.equal(nextFixture.hits.length, 2);
    } finally {
      resume.resolve(); await bounded(retired.promise);
      crypto.subtle.digest = oldDigest;
      await bounded(old.stop(0)); await bounded(next.stop(0));
      process.removeListener("unhandledRejection", onUnhandled);
    }
  });
}
