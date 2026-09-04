import { HttpClient, HttpClientConfig, NetworkErrorCallback } from "./core/http";
import { AuthClient } from "./auth/client";
import { DBClient } from "./db/client";
import { PubSubClient } from "./pubsub/client";
import { NetworkClient } from "./network/client";
import { CacheClient } from "./cache/client";
import { StorageClient } from "./storage/client";
import { FunctionsClient, FunctionsClientConfig } from "./functions/client";
import { WSClientConfig } from "./core/ws";
import { StorageAdapter } from "./auth/types";

export interface ClientConfig extends Omit<HttpClientConfig, "fetch"> {
  apiKey?: string;
  jwt?: string;
  storage?: StorageAdapter;
  wsConfig?: Partial<Omit<WSClientConfig, "wsURL">>;
  functionsConfig?: FunctionsClientConfig;
  fetch?: typeof fetch;
  /**
   * Callback invoked on network errors (HTTP and WebSocket).
   * Use this to trigger gateway failover at the application layer.
   */
  onNetworkError?: NetworkErrorCallback;
}

export interface Client {
  auth: AuthClient;
  db: DBClient;
  pubsub: PubSubClient;
  network: NetworkClient;
  cache: CacheClient;
  storage: StorageClient;
  functions: FunctionsClient;
}

export function createClient(config: ClientConfig): Client {
  const httpClient = new HttpClient({
    baseURL: config.baseURL,
    timeout: config.timeout,
    maxRetries: config.maxRetries,
    retryDelayMs: config.retryDelayMs,
    debug: config.debug,
    fetch: config.fetch,
    onNetworkError: config.onNetworkError,
  });

  const auth = new AuthClient({
    httpClient,
    storage: config.storage,
    apiKey: config.apiKey,
    jwt: config.jwt,
  });

  // Derive WebSocket URL from baseURL
  const wsURL = config.baseURL.replace(/^http/, "ws").replace(/\/$/, "");

  const db = new DBClient(httpClient);
  const pubsub = new PubSubClient(httpClient, {
    ...config.wsConfig,
    wsURL,
    onNetworkError: config.onNetworkError,
  });
  const network = new NetworkClient(httpClient);
  const cache = new CacheClient(httpClient);
  const storage = new StorageClient(httpClient);
  const functions = new FunctionsClient(httpClient, config.functionsConfig);

  return {
    auth,
    db,
    pubsub,
    network,
    cache,
    storage,
    functions,
  };
}

export { HttpClient } from "./core/http";
export type { NetworkErrorCallback, NetworkErrorContext } from "./core/http";
export { WSClient } from "./core/ws";
export { Logger } from "./core/logger";
export type { LogSink } from "./core/logger";
export { AuthClient } from "./auth/client";
export { DBClient } from "./db/client";
export { QueryBuilder } from "./db/qb";
export { Repository } from "./db/repository";
export { PubSubClient, Subscription } from "./pubsub/client";
export { NetworkClient } from "./network/client";
export { CacheClient } from "./cache/client";
export { StorageClient } from "./storage/client";
export { FunctionsClient } from "./functions/client";
export {
  SDKError,
  AuthError,
  ScopeError,
  NotFoundError,
  NetworkError,
  RevokedCredentialError,
  AuthCode,
} from "./errors";
export { SCOPES, DATA_PLANE_SCOPES, KEY_PROFILES, PROFILE_SCOPES, isScope, satisfiesScope } from "./scopes";
export type { Scope, KeyProfile } from "./scopes";
export type { RequestOptions, TokenRefresher } from "./core/http";
export type { LogoutOptions } from "./auth/client";
export type {
  WSClientConfig,
  ReconnectConfig,
  WSReconnectingHandler,
  WSReconnectedHandler,
} from "./core/ws";
export { MemoryStorage, LocalStorageAdapter } from "./auth/types";
export type { StorageAdapter, AuthConfig, WhoAmI } from "./auth/types";
export type * from "./db/types";
export type {
  MessageHandler,
  ErrorHandler,
  CloseHandler,
  PresenceMember,
  PresenceResponse,
  PresenceOptions,
  SubscribeOptions,
} from "./pubsub/types";
export { type PubSubMessage } from "./pubsub/types";
export type {
  PeerInfo,
  NetworkStatus,
  ProxyRequest,
  ProxyResponse,
} from "./network/client";
export type {
  CacheGetRequest,
  CacheGetResponse,
  CachePutRequest,
  CachePutResponse,
  CacheDeleteRequest,
  CacheDeleteResponse,
  CacheMultiGetRequest,
  CacheMultiGetResponse,
  CacheScanRequest,
  CacheScanResponse,
  CacheHealthResponse,
} from "./cache/client";
export type {
  StorageUploadResponse,
  StoragePinRequest,
  StoragePinResponse,
  StorageStatus,
} from "./storage/client";
export type { FunctionsClientConfig } from "./functions/client";
export type * from "./functions/types";
