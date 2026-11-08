// Synthetic source checks do not settle shared prerequisites or installed-hub acceptance.
const gates = [
  { id: "provisional-sdk-165-layout-164", status: "BLOCKED", reason: "The official SDK authoring branch is provisional; its unmodified Go oracle has a known repository-layout failure. SDK merge/resynchronization belongs to the coordinator." },
  { id: "cold-installed-http", status: "BLOCKED", reason: "Cold native snapshot fixtures pass only the guest-side boundary. No isolated installed-hub HTTP acceptance has been performed." },
  { id: "controlled-proxy-range-1181", status: "BLOCKED", reason: "No controlled per-extension proxy interface or real upstream large-log Range/ETag evidence." },
  { id: "equal-timestamp-discovery", status: "FAIL", reason: "Inherited Go search cursor repeats a full equal-timestamp page; the KNOWN characterization fixture is not a completeness pass." },
  { id: "native-ui-parity", status: "BLOCKED", reason: "Commit detail, log-location selection and large-source truncation/load-more navigation retain explicit gaps documented in README.md." },
  { id: "migration-hn", status: "BLOCKED", reason: "The migration-wide HN gate is not established by GitHub synthetic fixtures." },
  { id: "migration-sigterm", status: "BLOCKED", reason: "The migration-wide SIGTERM gate is not established by GitHub synthetic fixtures." },
  { id: "native-visual-hosted-ci", status: "NOT_RUN", reason: "No installed-hub, native dark/light, production or persistent queue/restart acceptance. Required hosted CI must pass before merge." },
];
console.log(JSON.stringify({ verdict: "BLOCKED", profile: "local-draft", gates }));
process.exitCode = 1;
