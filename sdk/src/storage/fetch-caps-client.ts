import { HttpClient } from "../core/http";
import { SDKError } from "../errors";
import {
  DirectFetch,
  type FetchCapability,
  type FetchOptions,
  type FetchTransport,
} from "./fetch-transport";
import { PIN_PROPAGATION_ATTEMPTS, isNotFound, pinPropagationBackoff } from "./pin-propagation";

/** Bounds the gateway enforces on `POST /v1/storage/fetch-caps`. */
export const FETCH_CAP_MAX_COUNT = 64;
export const FETCH_CAP_MIN_TTL_SECONDS = 3600;
export const FETCH_CAP_MAX_TTL_SECONDS = 604800;

export interface MintFetchCapsOptions {
  /** How many distinct tokens to mint for the CID, 1..64. Use one per fetch. */
  count: number;
  /** Lifetime in seconds, 3600 (1 h) .. 604800 (7 d). */
  ttlSeconds: number;
}

export interface MintFetchCapsResult {
  namespace: string;
  cid: string;
  caps: FetchCapability[];
}

interface MintFetchCapsWire {
  namespace: string;
  cid: string;
  caps: Array<{ id: string; token: string; revoke_key: string; expires_at: number | string }>;
}

/** The gateway's `expires_at` as Unix seconds, whether it sends a number or an RFC 3339 time. */
function toUnixSeconds(value: number | string): number {
  if (typeof value === "number") return value;
  const ms = Date.parse(value);
  return Number.isNaN(ms) ? NaN : Math.floor(ms / 1000);
}

/**
 * The fetch-capability half of `StorageClient`: minting, revoking and
 * downloading with a capability. `StorageClient` delegates to it.
 */
export class FetchCapsClient {
  constructor(private readonly httpClient: HttpClient) {}

  /**
   * Mint fetch capabilities for a CID you own: tokens that let a holder
   * download it through `fetchWith` without an identity. Needs a device-bound
   * session.
   *
   * @example
   * ```ts
   * const { caps } = await client.storage.mintFetchCaps(cid, { count: 3, ttlSeconds: 86400 });
   * ```
   */
  async mintFetchCaps(cid: string, options: MintFetchCapsOptions): Promise<MintFetchCapsResult> {
    const { count, ttlSeconds } = options;
    if (!Number.isInteger(count) || count < 1 || count > FETCH_CAP_MAX_COUNT) {
      throw new SDKError(
        `count must be an integer from 1 to ${FETCH_CAP_MAX_COUNT}, got ${count}`,
        400,
        "VALIDATION_FAILED"
      );
    }
    if (
      !Number.isInteger(ttlSeconds) ||
      ttlSeconds < FETCH_CAP_MIN_TTL_SECONDS ||
      ttlSeconds > FETCH_CAP_MAX_TTL_SECONDS
    ) {
      throw new SDKError(
        `ttlSeconds must be an integer from ${FETCH_CAP_MIN_TTL_SECONDS} to ${FETCH_CAP_MAX_TTL_SECONDS}, got ${ttlSeconds}`,
        400,
        "VALIDATION_FAILED"
      );
    }
    const wire = await this.httpClient.post<MintFetchCapsWire>("/v1/storage/fetch-caps", {
      cid,
      count,
      ttl_seconds: ttlSeconds,
    });
    return {
      namespace: wire.namespace,
      cid: wire.cid,
      caps: wire.caps.map((c) => ({
        id: c.id,
        token: c.token,
        revokeKey: c.revoke_key,
        expiresAt: toUnixSeconds(c.expires_at),
      })),
    };
  }

  /**
   * Revoke a fetch capability by its `id`, proving it was issued to you with the
   * `revokeKey` the mint returned beside it. Takes effect for every later use of the
   * token. An `id` with no matching key is refused (`FETCH_CAP_REVOKE_KEY_INVALID`).
   */
  async revokeFetchCap(id: string, revokeKey: string): Promise<void> {
    if (!revokeKey) {
      throw new SDKError("revokeKey is required to revoke a fetch capability", 400, "VALIDATION_FAILED");
    }
    await this.httpClient.delete(`/v1/storage/fetch-caps/${encodeURIComponent(id)}`, {
      headers: { "X-Orama-Revoke-Key": revokeKey },
    });
  }

  /** The non-private transport: downloads with this client's own credential. See `DirectFetch`. */
  directTransport(): DirectFetch {
    return new DirectFetch(this.httpClient);
  }

  /**
   * Download a CID with a fetch capability over the transport the caller chose.
   *
   * Re-asks while the pin propagates exactly as `get` does, through the same
   * transport: a relayed fetch stays relayed on every attempt and each attempt
   * picks its relay afresh. Any other failure, a relay failure included, is
   * thrown as it is; there is no fallback to another transport.
   */
  async fetchWith(
    transport: FetchTransport,
    cid: string,
    cap: FetchCapability,
    opts?: FetchOptions
  ): Promise<Uint8Array> {
    for (let attempt = 1; ; attempt++) {
      try {
        return await transport.fetch(cid, cap, opts);
      } catch (error) {
        if (attempt >= PIN_PROPAGATION_ATTEMPTS || !isNotFound(error)) {
          throw error;
        }
        await pinPropagationBackoff(attempt, opts?.signal);
      }
    }
  }
}
