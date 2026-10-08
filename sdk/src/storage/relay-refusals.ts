import { FetchCapError, RelayCode, RelayError, SDKError } from "../errors";
import type { ParsedResponse } from "./http-response";

/** Map a relay's pre-upgrade refusal (400/429/503/...) to a typed error. */
export function relayRefusal(status: number, retryAfter: string | undefined, bodyText: string): RelayError {
  let body: { error?: unknown; code?: unknown } = {};
  try {
    body = JSON.parse(bodyText);
  } catch {
    // Not JSON: the status alone decides the code.
  }
  const fallback =
    status === 400
      ? RelayCode.DestinationNotAllowed
      : status === 429
        ? RelayCode.RateLimited
        : status === 503
          ? RelayCode.Unavailable
          : RelayCode.Refused;
  const code = typeof body.code === "string" ? body.code : fallback;
  const message = typeof body.error === "string" ? body.error : `relay refused the tunnel (HTTP ${status})`;
  const seconds = retryAfter !== undefined && /^\d+$/.test(retryAfter) ? Number(retryAfter) : undefined;
  return new RelayError(message, status, code, seconds === undefined ? {} : { retry_after_seconds: seconds });
}

/** Map the storage node's non-200 answer, read through the tunnel, to a typed error. */
export function storageRefusal(response: ParsedResponse): SDKError {
  let body: any = {};
  try {
    body = JSON.parse(Buffer.from(response.body).toString("utf8"));
  } catch {
    // Not JSON: fromResponse falls back to the HTTP status.
  }
  const code = body?.code;
  if (typeof code === "string" && code.startsWith("FETCH_CAP_")) {
    const message = typeof body.error === "string" ? body.error : code;
    return new FetchCapError(message, response.status, code, body);
  }
  return SDKError.fromResponse(response.status, body);
}
