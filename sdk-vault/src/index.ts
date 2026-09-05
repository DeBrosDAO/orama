/**
 * @debros/orama-vault — client for the Orama Network vault guardians.
 *
 * This is operator and RootWallet tooling, not application tooling. The
 * guardians listen on the WireGuard overlay (`10.0.0.x`), so an application
 * running outside the cluster cannot reach them at all. Applications that need
 * secrets should use the gateway's `/v1/vault/{push,pull}` endpoints, which are
 * reachable over HTTPS and do the same Shamir split server-side.
 *
 * It used to ship inside `@debros/orama`, where it added two cryptography
 * dependencies and twenty top-level primitives to every application's bundle
 * for an API none of them could call.
 */

// High-level vault client
export { VaultClient } from "./client";
export { adaptiveThreshold, writeQuorum } from "./quorum";
export { QuorumError } from "./errors";
export type {
  VaultConfig,
  SecretMeta,
  StoreResult,
  RetrieveResult,
  ListResult,
  DeleteResult,
  GuardianResult,
} from "./types";

// Guardian challenge-response authentication.
export { AuthClient } from "./auth";

// Transport (guardian communication)
export { GuardianClient, GuardianError } from "./transport";
export { fanOut, fanOutIndexed, withTimeout, withRetry } from "./transport";
export type {
  GuardianEndpoint,
  GuardianErrorCode,
  GuardianInfo,
  HealthResponse as GuardianHealthResponse,
  StatusResponse as GuardianStatusResponse,
  PushResponse,
  PullResponse,
  StoreSecretResponse,
  GetSecretResponse,
  DeleteSecretResponse,
  ListSecretsResponse,
  SecretEntry,
  ChallengeResponse as GuardianChallengeResponse,
  SessionResponse as GuardianSessionResponse,
  FanOutResult,
} from "./transport";

// Crypto primitives
export {
  encrypt,
  decrypt,
  encryptString,
  decryptString,
  serialize as serializeEncrypted,
  deserialize as deserializeEncrypted,
  encryptAndSerialize,
  deserializeAndDecrypt,
  toHex as encryptedToHex,
  fromHex as encryptedFromHex,
  toBase64 as encryptedToBase64,
  fromBase64 as encryptedFromBase64,
  generateKey,
  generateNonce,
  clearKey,
  isValidEncryptedData,
  KEY_SIZE,
  NONCE_SIZE,
  TAG_SIZE,
  deriveKeyHKDF,
  shamirSplit,
  shamirCombine,
} from "./crypto";
export type {
  EncryptedData,
  SerializedEncryptedData,
  ShamirShare,
} from "./crypto";
