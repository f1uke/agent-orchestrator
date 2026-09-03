import { useSyncExternalStore } from "react";
import { hasTrustedApiBaseUrl, subscribeApiBaseUrl } from "../lib/api-client";

/**
 * Whether the daemon's port is known yet.
 *
 * The renderer boots before the daemon reports its port, and until it does
 * `runtimeFetch` answers every request with a synthesized 503 rather than
 * reaching the network. That 503 is indistinguishable from a real server error
 * to a caller, so a query that fires on mount spends its whole retry budget
 * inside the boot window and settles into a permanent failure — the Wiki row
 * vanished from the sidebar for the entire life of the app exactly this way.
 *
 * "The daemon is not up yet" is a GATE, not a failure: gate a query on this and
 * React Query holds it until the port arrives, then runs it. The subscription
 * also fires when the daemon restarts on a new port, so a gated query re-runs
 * itself across a daemon bounce with no poll of its own.
 */
export function useApiReady(): boolean {
	return useSyncExternalStore(subscribeApiBaseUrl, hasTrustedApiBaseUrl, hasTrustedApiBaseUrl);
}
