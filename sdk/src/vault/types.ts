import type { GuardianEndpoint } from './transport/types';

/** Configuration for VaultClient. */
export interface VaultConfig {
  /** Guardian endpoints to connect to. */
  guardians: GuardianEndpoint[];
  /** HMAC key for authentication (derived from user's secret). */
  hmacKey: Uint8Array;
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

/** Result of a store operation. */
export interface StoreResult {
  /** Number of guardians that acknowledged. */
  ackCount: number;
  /** Total guardians contacted. */
  totalContacted: number;
  /** Number of failures. */
  failCount: number;
  /** Whether write quorum was met. */
  quorumMet: boolean;
  /** Per-guardian results. */
  guardianResults: GuardianResult[];
}

/** Result of a retrieve operation. */
export interface RetrieveResult {
  /** The reconstructed secret data. */
  data: Uint8Array;
  /** Number of shares collected. */
  sharesCollected: number;
}

/** Result of a list operation. */
export interface ListResult {
  secrets: SecretMeta[];
}

/** Result of a delete operation. */
export interface DeleteResult {
  /** Number of guardians that acknowledged. */
  ackCount: number;
  totalContacted: number;
  quorumMet: boolean;
}

/** Per-guardian operation result. */
export interface GuardianResult {
  endpoint: string;
  success: boolean;
  error?: string;
}
