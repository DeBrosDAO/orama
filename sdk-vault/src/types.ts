import type { GuardianEndpoint } from "./transport/types";

/** Configuration for VaultClient. */
export interface VaultConfig {
  /**
   * Guardian endpoints to connect to.
   *
   * This set is the unit of redundancy: a secret is split into one share per
   * guardian listed here, and the write quorum is computed from its size.
   */
  guardians: GuardianEndpoint[];
  /** Identity hash (hex string, 64 chars). */
  identityHex: string;
  /** Request timeout in ms (default: 10000). */
  timeoutMs?: number;
}

/** Metadata for a stored secret. */
export interface SecretMeta {
  name: string;
  version: number;
  size: number;
}

/**
 * Result of a successful store.
 *
 * There is no `quorumMet` field: a store that did not reach quorum throws a
 * `QuorumError` rather than returning, so anything returned here is durable.
 */
export interface StoreResult {
  /** Number of guardians that acknowledged. */
  ackCount: number;
  /** Number of guardians the write was addressed to. */
  totalContacted: number;
  /** Number of guardians that did not take their share. */
  failCount: number;
  /** Per-guardian results, one per configured guardian. */
  guardianResults: GuardianResult[];
}

/** Result of a retrieve operation. */
export interface RetrieveResult {
  /** The reconstructed secret data. */
  data: Uint8Array;
  /** Number of shares combined. */
  sharesCollected: number;
  /** The version the returned data belongs to. */
  version: number;
}

/** Result of a list operation. */
export interface ListResult {
  secrets: SecretMeta[];
}

/** Result of a successful delete. */
export interface DeleteResult {
  /** Number of guardians that dropped their share. */
  ackCount: number;
  /** Number of guardians the delete was addressed to. */
  totalContacted: number;
  /** Per-guardian results, one per configured guardian. */
  guardianResults: GuardianResult[];
}

/** Per-guardian operation result. */
export interface GuardianResult {
  /** `address:port` of the guardian. */
  endpoint: string;
  success: boolean;
  error?: string;
}
