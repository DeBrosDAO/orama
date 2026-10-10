import { HttpClient } from "../core/http";
import { SDKError } from "../errors";
import {
  type DirectFetch,
  type FetchCapability,
  type FetchOptions,
  type FetchTransport,
} from "./fetch-transport";
import {
  FetchCapsClient,
  type MintFetchCapsOptions,
  type MintFetchCapsResult,
} from "./fetch-caps-client";
import { PIN_PROPAGATION_ATTEMPTS, isNotFound, pinPropagationBackoff } from "./pin-propagation";

export {
  FETCH_CAP_MAX_COUNT,
  FETCH_CAP_MIN_TTL_SECONDS,
  FETCH_CAP_MAX_TTL_SECONDS,
} from "./fetch-caps-client";
export type { MintFetchCapsOptions, MintFetchCapsResult } from "./fetch-caps-client";

export interface StorageUploadResponse {
  cid: string;
  name: string;
  size: number;
}

export interface StoragePinRequest {
  cid: string;
  name?: string;
}

export interface StoragePinResponse {
  cid: string;
  name: string;
}

export interface StorageStatus {
  cid: string;
  name: string;
  status: string; // "pinned", "pinning", "queued", "unpinned", "error", "unknown"
  replication_min: number;
  replication_max: number;
  replication_factor: number;
  peers: string[];
  error?: string;
}

export class StorageClient {
  private httpClient: HttpClient;
  private fetchCaps: FetchCapsClient;

  constructor(httpClient: HttpClient) {
    this.httpClient = httpClient;
    this.fetchCaps = new FetchCapsClient(httpClient);
  }

  /**
   * Upload content to IPFS and optionally pin it.
   * Supports both File objects (browser) and Buffer/ReadableStream (Node.js).
   *
   * @param file - File to upload (File, Blob, or Buffer)
   * @param name - Optional filename
   * @param options - Optional upload options
   * @param options.pin - Whether to pin the content (default: true). Pinning happens asynchronously on the backend.
   * @returns Upload result with CID
   *
   * @example
   * ```ts
   * // Browser
   * const fileInput = document.querySelector('input[type="file"]');
   * const file = fileInput.files[0];
   * const result = await client.storage.upload(file, file.name);
   * console.log(result.cid);
   *
   * // Node.js
   * const fs = require('fs');
   * const fileBuffer = fs.readFileSync('image.jpg');
   * const result = await client.storage.upload(fileBuffer, 'image.jpg', { pin: true });
   * ```
   */
  async upload(
    file: File | Blob | ArrayBuffer | Uint8Array | ReadableStream<Uint8Array>,
    name?: string,
    options?: {
      pin?: boolean;
    }
  ): Promise<StorageUploadResponse> {
    // Create FormData for multipart upload
    const formData = new FormData();

    // Handle different input types
    if (file instanceof File) {
      formData.append("file", file);
    } else if (file instanceof Blob) {
      formData.append("file", file, name);
    } else if (file instanceof ArrayBuffer) {
      const blob = new Blob([file]);
      formData.append("file", blob, name);
    } else if (file instanceof Uint8Array) {
      // Convert Uint8Array to ArrayBuffer for Blob constructor
      const buffer = file.buffer.slice(
        file.byteOffset,
        file.byteOffset + file.byteLength
      ) as ArrayBuffer;
      const blob = new Blob([buffer], { type: "application/octet-stream" });
      formData.append("file", blob, name);
    } else if (file instanceof ReadableStream) {
      // For ReadableStream, we need to read it into a blob first
      // This is a limitation - in practice, pass File/Blob/Buffer
      const chunks: ArrayBuffer[] = [];
      const reader = file.getReader();
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        const buffer = value.buffer.slice(
          value.byteOffset,
          value.byteOffset + value.byteLength
        ) as ArrayBuffer;
        chunks.push(buffer);
      }
      const blob = new Blob(chunks);
      formData.append("file", blob, name);
    } else {
      throw new Error(
        "Unsupported file type. Use File, Blob, ArrayBuffer, Uint8Array, or ReadableStream."
      );
    }

    // Add pin flag (default: true)
    const shouldPin = options?.pin !== false; // Default to true
    formData.append("pin", shouldPin ? "true" : "false");

    return this.httpClient.uploadFile<StorageUploadResponse>(
      "/v1/storage/upload",
      formData,
      { timeout: 300000 } // 5 minute timeout for large files
    );
  }

  /**
   * Pin an existing CID
   *
   * @param cid - Content ID to pin
   * @param name - Optional name for the pin
   * @returns Pin result
   */
  async pin(cid: string, name?: string): Promise<StoragePinResponse> {
    return this.httpClient.post<StoragePinResponse>("/v1/storage/pin", {
      cid,
      name,
    });
  }

  /**
   * Get the pin status for a CID
   *
   * @param cid - Content ID to check
   * @returns Pin status information
   */
  async status(cid: string): Promise<StorageStatus> {
    return this.httpClient.get<StorageStatus>(`/v1/storage/status/${cid}`);
  }

  /**
   * Retrieve content from IPFS by CID
   *
   * @param cid - Content ID to retrieve
   * @returns ReadableStream of the content
   *
   * @example
   * ```ts
   * const stream = await client.storage.get(cid);
   * const reader = stream.getReader();
   * while (true) {
   *   const { done, value } = await reader.read();
   *   if (done) break;
   *   // Process chunk
   * }
   * ```
   */
  async get(cid: string): Promise<ReadableStream<Uint8Array>> {
    const response = await this.fetchWhilePinPropagates(cid);
    if (!response.body) {
      throw new SDKError(
        `storage returned no body for ${cid}`,
        response.status,
        "EMPTY_BODY"
      );
    }
    return response.body;
  }

  /**
   * Retrieve content from IPFS by CID and return the full Response object
   * Useful when you need access to response headers (e.g., content-length)
   *
   * @param cid - Content ID to retrieve
   * @returns Response object with body stream and headers
   *
   * @example
   * ```ts
   * const response = await client.storage.getBinary(cid);
   * const contentLength = response.headers.get('content-length');
   * const reader = response.body.getReader();
   * // ... read stream
   * ```
   */
  async getBinary(cid: string): Promise<Response> {
    return this.fetchWhilePinPropagates(cid);
  }

  /**
   * Fetch a CID, re-asking while the cluster still answers 404.
   *
   * A CID is addressable the moment the upload returns, but the pin has to
   * propagate across the IPFS Cluster peers before every node can serve it, so
   * a read that closely follows a write can legitimately 404 for a few seconds.
   * Only a 404 is retried: any other failure is the caller's answer straight
   * away.
   *
   * Both `get` and `getBinary` come through here. They used to carry a
   * character-for-character copy of this loop each, which is two places to fix
   * when the propagation window changes and two places to get it wrong.
   */
  private async fetchWhilePinPropagates(cid: string): Promise<Response> {
    for (let attempt = 1; ; attempt++) {
      try {
        return await this.httpClient.getBinary(`/v1/storage/get/${cid}`);
      } catch (error) {
        if (attempt >= PIN_PROPAGATION_ATTEMPTS || !isNotFound(error)) {
          throw error;
        }
        await pinPropagationBackoff(attempt);
      }
    }
  }

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
  mintFetchCaps(cid: string, options: MintFetchCapsOptions): Promise<MintFetchCapsResult> {
    return this.fetchCaps.mintFetchCaps(cid, options);
  }

  /**
   * Revoke a fetch capability by its `id`, proving it was issued to you with the
   * `revokeKey` the mint returned beside it. Takes effect for every later use of the
   * token. An `id` with no matching key is refused (`FETCH_CAP_REVOKE_KEY_INVALID`).
   */
  revokeFetchCap(id: string, revokeKey: string): Promise<void> {
    return this.fetchCaps.revokeFetchCap(id, revokeKey);
  }

  /** The non-private transport: downloads with this client's own credential. See `DirectFetch`. */
  directTransport(): DirectFetch {
    return this.fetchCaps.directTransport();
  }

  /**
   * Download a CID with a fetch capability over the transport the caller chose,
   * re-asking while the pin propagates exactly as `get` does. See
   * `FetchCapsClient.fetchWith`.
   */
  fetchWith(
    transport: FetchTransport,
    cid: string,
    cap: FetchCapability,
    opts?: FetchOptions
  ): Promise<Uint8Array> {
    return this.fetchCaps.fetchWith(transport, cid, cap, opts);
  }

  /**
   * Unpin a CID
   *
   * @param cid - Content ID to unpin
   */
  async unpin(cid: string): Promise<void> {
    await this.httpClient.delete(`/v1/storage/unpin/${cid}`);
  }
}
