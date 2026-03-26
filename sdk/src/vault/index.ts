// High-level vault client
export { VaultClient } from './client';
export { adaptiveThreshold, writeQuorum } from './quorum';
export type {
  VaultConfig,
  SecretMeta,
  StoreResult,
  RetrieveResult,
  ListResult,
  DeleteResult,
  GuardianResult,
} from './types';

// Vault auth (renamed to avoid collision with top-level AuthClient)
export { AuthClient as VaultAuthClient } from './auth';

// Transport (guardian communication)
export { GuardianClient, GuardianError } from './transport';
export { fanOut, fanOutIndexed, withTimeout, withRetry } from './transport';
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
} from './transport';

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
} from './crypto';
export type { EncryptedData, SerializedEncryptedData } from './crypto';

export { deriveKeyHKDF } from './crypto';

export { shamirSplit, shamirCombine } from './crypto';
export type { ShamirShare } from './crypto';
