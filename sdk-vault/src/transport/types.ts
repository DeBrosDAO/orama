/** A guardian node endpoint. */
export interface GuardianEndpoint {
  address: string;
  port: number;
}

/** V1 push response. */
export interface PushResponse {
  status: string;
}

/** V1 pull response. */
export interface PullResponse {
  share: string; // base64
}

/** V2 store response. */
export interface StoreSecretResponse {
  status: string;
  name: string;
  version: number;
}

/** V2 get response. */
export interface GetSecretResponse {
  share: string; // base64
  name: string;
  version: number;
  created_ns: number;
  updated_ns: number;
}

/** V2 delete response. */
export interface DeleteSecretResponse {
  status: string;
  name: string;
}

/** V2 list response. */
export interface ListSecretsResponse {
  secrets: SecretEntry[];
}

/** An entry in the list secrets response. */
export interface SecretEntry {
  name: string;
  version: number;
  size: number;
}

/** Health check response. */
export interface HealthResponse {
  status: string;
  version: string;
}

/** Status response. */
export interface StatusResponse {
  status: string;
  version: string;
  data_dir: string;
  client_port: number;
  peer_port: number;
}

/** Guardian info response. */
export interface GuardianInfo {
  guardians: Array<{ address: string; port: number }>;
  threshold: number;
  total: number;
}

/** Challenge response from auth endpoint. */
export interface ChallengeResponse {
  nonce: string;
  created_ns: number;
  tag: string;
}

/** Session token response from auth endpoint. */
export interface SessionResponse {
  identity: string;
  expiry_ns: number;
  tag: string;
}

/** Error body from guardian. */
export interface GuardianErrorBody {
  error: string;
}

/** Error classification codes. */
export type GuardianErrorCode = 'TIMEOUT' | 'NOT_FOUND' | 'AUTH' | 'SERVER_ERROR' | 'NETWORK' | 'CONFLICT';

/** Fan-out result for a single guardian. */
export interface FanOutResult<T> {
  endpoint: GuardianEndpoint;
  result: T | null;
  error: string | null;
  errorCode?: GuardianErrorCode;
}
