import type { HttpClient } from "../core/http";
import { SDKError } from "../errors";

/**
 * Permission to download one CID without presenting an identity: minted by an
 * owner with `storage.mintFetchCaps`, handed to whoever should fetch.
 */
export interface FetchCapability {
  /** Revocation handle: pass to `storage.revokeFetchCap` with `revokeKey`. */
  id: string;
  /**
   * Proof that `id` was issued to you, returned by the mint and required to
   * revoke by id. Keep it with the owner; it is not needed to fetch and should not
   * travel with the token.
   */
  revokeKey: string;
  /** Opaque bearer token, sent as `X-Orama-Fetch-Cap`. */
  token: string;
  /** Expiry as Unix seconds. */
  expiresAt: number;
}

export interface FetchOptions {
  signal?: AbortSignal;
}

/**
 * How a CID is downloaded with a fetch capability. The caller picks the
 * transport; the SDK never switches between them on failure, because a silent
 * fall back from a relayed fetch to a direct one would reveal the address the
 * relay exists to hide.
 */
export interface FetchTransport {
  fetch(cid: string, cap: FetchCapability, opts?: FetchOptions): Promise<Uint8Array>;
}

/**
 * Downloads through the gateway the client is configured for, with the
 * client's own credential (`GET /v1/storage/get/<cid>`); the capability is not
 * used. This is the non-private baseline: the storage node sees the caller's
 * address and identity. Get one from `client.storage.directTransport()`.
 */
export class DirectFetch implements FetchTransport {
  constructor(private readonly http: Pick<HttpClient, "getBinary">) {}

  async fetch(cid: string, _cap: FetchCapability, opts?: FetchOptions): Promise<Uint8Array> {
    const response = await this.http.getBinary(`/v1/storage/get/${cid}`, { signal: opts?.signal });
    if (!response.body) {
      throw new SDKError(`storage returned no body for ${cid}`, response.status, "EMPTY_BODY");
    }
    return new Uint8Array(await response.arrayBuffer());
  }
}
