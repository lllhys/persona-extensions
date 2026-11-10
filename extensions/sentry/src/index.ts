import { ExtError, JSONIntegerError, unknownMethod, withExactInit, withExactParams, type Handler, type Program } from "@persona/extension";
import { publicError } from "./i18n.ts";
import { decodeConfig, decodeExactConfig, decodeDelivery, legacyDelivery } from "./params.ts";
import { SentryService, type Dependencies } from "./service.ts";
export { SentryService } from "./service.ts";
export { parseDSN } from "./dsn.ts";
export { eventID, event, envelope } from "./event.ts";
export { limitedUntil, send, bounds } from "./transport.ts";
export const sourceContributions = { error_reports: [{ id: "sentry" }] };
/** Go has only the error-report service, not a navigation/diagnostic page. */
export function createRuntime(deps: Dependencies = {
  fetch: (input, init) => globalThis.fetch(input, init),
  // stderr stays separate from JSON-lines stdout; messages contain no report data.
  log: message => console.error(message),
}) {
  const service = new SentryService(deps);
  let initialized = false, active = false, locale: string | undefined;
  let delivery: Handler | undefined;
  const program: Program = {
    id: "sentry", version: "0.1.2",
    init: withExactInit((init, _callHub, call) => {
      if (initialized) return active ? undefined : { error_reports: [] };
      locale = init.hub?.locale;
      active = init.active === undefined || !!init.active.error_reports?.includes("sentry");
      const dsn = init.secrets?.dsn ?? "";
      if (active) service.configure(call ? decodeExactConfig(call.params?.field("config")) : decodeConfig(init.config), dsn, locale);
      // Go retains the contribution without a DSN: reports are dropped locally,
      // but configuring the secret later must still start the report receiver.
      initialized = true;
      return active ? undefined : { error_reports: [] };
    }),
    namespaces: {
      // The SDK inspects the brand on the returned handler, not on this provider.
      get error_reports() {
        return delivery ??= withExactParams((method, params, call) => {
          if (method !== "deliver") throw unknownMethod(method);
          if (!initialized) throw new ExtError(publicError("notInitialized", locale).message, "not_initialized");
          // Go decodes the whole nullable wire struct. It does not validate the
          // contribution ID or impose another ingress/body/batch-count limit.
          try {
            const reports = call ? decodeDelivery(call.params) : legacyDelivery(params);
            if (active) service.deliver(reports);
          } catch (error) {
            if (error instanceof JSONIntegerError) throw publicError("invalidReports", locale);
            throw error;
          }
          return null;
        });
      },
    },
  };
  return { program, service };
}
export function createProgram(deps?: Dependencies): Program { return createRuntime(deps).program; }
export default createProgram();
