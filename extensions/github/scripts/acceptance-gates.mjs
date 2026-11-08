// Synthetic source checks do not settle shared prerequisites or installed-hub acceptance.
const gates = [
  { id: "provisional-sdk-165", status: "BLOCKED", reason: "The unchanged provisional SDK payload includes the merged layout guard. Official-main synchronization still waits for main readiness #1570; notification-only #1657 is not initializer config raw-token support." },
  { id: "cold-installed-http", status: "BLOCKED", reason: "Cold native snapshot fixtures pass only the guest-side boundary. No isolated installed-hub HTTP acceptance has been performed." },
  { id: "controlled-proxy-range-1181", status: "BLOCKED", reason: "No controlled per-extension proxy interface or real upstream large-log Range/ETag evidence." },
  { id: "equal-timestamp-discovery", status: "FAIL", reason: "Inherited Go search cursor repeats a full equal-timestamp page; the KNOWN characterization fixture is not a completeness pass." },
  { id: "native-ui-parity", status: "PASS", reason: "Synthetic bundled callbacks prove original Go commit-link navigation, last-error log location, and bounded Unicode source truncation/load-more. This is not installed or visual acceptance." },
  { id: "migration-hn", status: "BLOCKED", reason: "The migration-wide HN gate is not established by GitHub synthetic fixtures." },
  { id: "migration-sigterm", status: "PASS", reason: "Retained coordinator evidence: shared SIGTERM Linux 20/20 PASS. Not rerun or established by this source writer." },
  { id: "native-visual-hosted-ci", status: "NOT_RUN", reason: "No installed-hub, native dark/light, production or persistent queue/restart acceptance. Required hosted CI must pass before merge." },
];
console.log(JSON.stringify({ verdict: "BLOCKED", profile: "local-draft", gates }));
process.exitCode = 1;
