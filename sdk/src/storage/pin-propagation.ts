import { NetworkError, SDKError } from "../errors";

/**
 * How long a read will keep re-asking while an upload's pin propagates across
 * the IPFS Cluster peers: 1s + 2s + 3s + 3s + 3s + 3s + 3s = 18s of waiting
 * over 8 attempts.
 */
export const PIN_PROPAGATION_ATTEMPTS = 8;
const PIN_PROPAGATION_BACKOFF_STEP_MS = 1000;
const PIN_PROPAGATION_BACKOFF_CAP_MS = 3000;

/**
 * Whether a failure means "the cluster does not have this CID yet".
 *
 * `httpClient.getBinary` throws an `SDKError` carrying the HTTP status, which
 * is the reliable signal. A gateway that says whether a 404 is worth retrying
 * is believed: it marks a 404 retryable only while a fresh upload's pin is
 * still propagating, and final once the content is gone, so a read of a gone
 * object stops at once instead of retrying for eighteen seconds. An older
 * gateway says nothing, and every 404 is retried as before. The message check
 * covers a transport that reports the status only in text.
 */
export function isNotFound(error: unknown): boolean {
  if (error instanceof SDKError) {
    return error.httpStatus === 404 && error.retryable !== false;
  }
  const message = error instanceof Error ? error.message : String(error);
  return message.includes("not found") || message.includes("404");
}

/** Waits the pin-propagation backoff for `attempt`; rejects early if `signal` aborts. */
export function pinPropagationBackoff(attempt: number, signal?: AbortSignal): Promise<void> {
  const backoffMs = Math.min(
    attempt * PIN_PROPAGATION_BACKOFF_STEP_MS,
    PIN_PROPAGATION_BACKOFF_CAP_MS
  );
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new NetworkError("request aborted by caller", "ABORTED", { cause: "caller-abort" }));
      return;
    }
    const onAbort = () => {
      clearTimeout(timer);
      reject(new NetworkError("request aborted by caller", "ABORTED", { cause: "caller-abort" }));
    };
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, backoffMs);
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}
