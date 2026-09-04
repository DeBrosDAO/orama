import { NetworkError, SDKError } from "../errors";
import { Logger } from "./logger";

/**
 * Context provided to the onNetworkError callback
 */
export interface NetworkErrorContext {
  method: "GET" | "POST" | "PUT" | "DELETE" | "WS";
  path: string;
  isRetry: boolean;
  attempt: number;
}

/**
 * Callback invoked when a network error occurs.
 * Use this to trigger gateway failover or other error handling.
 */
export type NetworkErrorCallback = (
  error: SDKError,
  context: NetworkErrorContext
) => void;

export interface HttpClientConfig {
  baseURL: string;
  timeout?: number;
  maxRetries?: number;
  retryDelayMs?: number;
  /**
   * Replaces the `fetch` used for every request. Defaults to the platform's
   * global `fetch`.
   *
   * This is also the supported way to reach a gateway presenting a certificate
   * your runtime does not trust (a Let's Encrypt staging certificate, or a
   * self-signed one in a test cluster). Build a fetch that relaxes
   * verification for that connection only — for example, in Node:
   *
   * ```ts
   * import { Agent, fetch as undiciFetch } from "undici";
   * const dispatcher = new Agent({ connect: { rejectUnauthorized: false } });
   * const client = createClient({
   *   baseURL,
   *   apiKey,
   *   fetch: (input, init) => undiciFetch(input, { ...init, dispatcher }) as unknown as Promise<Response>,
   * });
   * ```
   *
   * The SDK never changes the process's TLS settings on your behalf: doing so
   * would disable certificate verification for every other HTTPS client in the
   * same process, not just for Orama.
   */
  fetch?: typeof fetch;
  /**
   * Enable debug logging (includes full SQL queries and args). Default: false
   */
  debug?: boolean;
  /**
   * Callback invoked on network errors (after all retries exhausted).
   * Use this to trigger gateway failover at the application layer.
   */
  onNetworkError?: NetworkErrorCallback;
}

/**
 * Obtains a fresh access token. Returning null means the session cannot be
 * renewed, and the original 401 is raised unchanged.
 *
 * `AuthClient` installs one on the client it is built with, so an expired JWT
 * is renewed and the request retried once without the application seeing it.
 */
export type TokenRefresher = () => Promise<string | null>;

/** Per-request options shared by every verb. */
export interface RequestOptions {
  headers?: Record<string, string>;
  query?: Record<string, string | number | boolean>;
  /** Per-request timeout override, in milliseconds. */
  timeout?: number;
  /**
   * Caller's cancellation signal. When it fires the in-flight request is
   * terminated at the socket and the promise rejects with a `NetworkError`
   * whose code is "ABORTED" — never retried, and never reported through
   * `onNetworkError`, because a user pressing Cancel is not a network failure.
   */
  signal?: AbortSignal;
  /**
   * Send this one request to a different origin, with the same credentials.
   *
   * For a service that lives behind its own gateway. Passing an absolute URL as
   * the path does not work: the path is appended to the client's base URL, so
   * an absolute one produced `http://localhost:10104https://…`.
   */
  baseURL?: string;
}

/**
 * Longest a single retry will wait. A gateway that asks for an hour is asking
 * for more patience than a request can reasonably hold.
 */
const MAX_RETRY_DELAY_MS = 30_000;

/** Fraction of the computed delay added as random jitter. */
const RETRY_JITTER_RATIO = 0.25;

/**
 * Parse a `Retry-After` header, which is either a number of seconds or an
 * HTTP date. Returns undefined for anything else, including a date in the past.
 */
function parseRetryAfter(value: string | null): number | undefined {
  if (!value) return undefined;

  const seconds = Number(value.trim());
  if (Number.isFinite(seconds)) {
    return seconds > 0 ? seconds * 1000 : 0;
  }

  const when = Date.parse(value);
  if (Number.isNaN(when)) return undefined;
  return Math.max(0, when - Date.now());
}

/**
 * A one-line description of an rqlite request, for the debug log.
 *
 * Returns null for anything that is not an rqlite call, and for a body that
 * does not parse — a request is never failed because its log line could not be
 * assembled.
 */
function describeQuery(path: string, body: unknown): string | null {
  if (!path.includes("/v1/rqlite/") || body === undefined || body === null) {
    return null;
  }

  let parsed: any;
  try {
    parsed = typeof body === "string" ? JSON.parse(body) : body;
  } catch {
    return null;
  }

  if (parsed?.sql) {
    let details = `SQL: ${parsed.sql}`;
    if (parsed.args?.length > 0) {
      const args = parsed.args
        .map((a: unknown) => (typeof a === "string" ? `"${a}"` : a))
        .join(", ");
      details += ` | Args: [${args}]`;
    }
    return details;
  }

  if (parsed?.table) {
    let details = `Table: ${parsed.table}`;
    if (parsed.criteria && Object.keys(parsed.criteria).length > 0) {
      details += ` | Criteria: ${JSON.stringify(parsed.criteria)}`;
    }
    if (parsed.options) details += ` | Options: ${JSON.stringify(parsed.options)}`;
    if (parsed.select) details += ` | Select: ${JSON.stringify(parsed.select)}`;
    if (parsed.where) details += ` | Where: ${JSON.stringify(parsed.where)}`;
    if (parsed.limit) details += ` | Limit: ${parsed.limit}`;
    if (parsed.offset) details += ` | Offset: ${parsed.offset}`;
    return details;
  }

  return null;
}

export class HttpClient {
  private baseURL: string;
  private timeout: number;
  private maxRetries: number;
  private retryDelayMs: number;
  private fetch: typeof fetch;
  private apiKey?: string;
  private jwt?: string;
  private onNetworkError?: NetworkErrorCallback;
  private refresher?: TokenRefresher;
  /** In-flight renewal, so concurrent 401s renew once between them. */
  private refreshing?: Promise<string | null>;
  private readonly rootLogger: Logger;
  private readonly log: Logger;

  constructor(config: HttpClientConfig) {
    this.baseURL = config.baseURL.replace(/\/$/, "");
    this.timeout = config.timeout ?? 60000;
    this.maxRetries = config.maxRetries ?? 3;
    this.retryDelayMs = config.retryDelayMs ?? 1000;
    // The platform's fetch, unless the caller supplied one. See the `fetch`
    // option for how to reach a gateway with an untrusted certificate.
    this.fetch = config.fetch ?? globalThis.fetch;
    this.onNetworkError = config.onNetworkError;
    this.rootLogger = new Logger(config.debug ?? false);
    this.log = this.rootLogger.child("HttpClient");
  }

  /**
   * A logger for another part of the SDK, sharing this client's `debug`
   * setting. Every client in a `createClient` tree is built around one
   * HttpClient, so this is how `debug: true` reaches all of them.
   */
  logger(scope: string): Logger {
    return this.rootLogger.child(scope);
  }

  /**
   * Set the network error callback
   */
  setOnNetworkError(callback: NetworkErrorCallback | undefined): void {
    this.onNetworkError = callback;
  }

  /**
   * Install the hook used to renew an expired session.
   *
   * Without it a 401 propagates to the application, which is why every
   * application built on this SDK grew its own refresh-and-retry loop around
   * every call: `AuthClient.refresh()` existed from the start and nothing ever
   * called it.
   */
  setTokenRefresher(refresher: TokenRefresher | undefined): void {
    this.refresher = refresher;
  }

  setApiKey(apiKey?: string) {
    this.apiKey = apiKey;
    // Don't clear JWT - allow both to coexist
  }

  setJwt(jwt?: string) {
    this.jwt = jwt;
    // Don't clear API key - allow both to coexist
    this.log.log(
      `JWT set: ${!!jwt}, API key still present: ${!!this.apiKey}`
    );
  }

  private getAuthHeaders(path: string): Record<string, string> {
    const headers: Record<string, string> = {};

    // Database, pubsub, and cache operations use ONLY the API key, to avoid a JWT
    // user context interfering with namespace-level authorization.
    //
    // NOTE: /v1/proxy/* is deliberately NOT in this list (bugboard #149). The
    // gateway enforces a per-user wallet (SIWE) JWT on proxy — layer-1, #148 —
    // so an API key alone is rejected 401 "requires a logged-in user". Proxy must
    // send BOTH the API key and the wallet JWT, so it falls through to the default
    // branch below — exactly like /v1/storage/*, which is the working reference.
    const isDbOperation = path.includes("/v1/rqlite/");
    const isPubSubOperation = path.includes("/v1/pubsub/");
    const isCacheOperation = path.includes("/v1/cache/");

    // For auth operations, prefer API key over JWT to ensure proper authentication
    const isAuthOperation = path.includes("/v1/auth/");

    if (isDbOperation || isPubSubOperation || isCacheOperation) {
      // For database/pubsub/proxy/cache operations: use only API key (preferred for namespace operations)
      if (this.apiKey) {
        headers["X-API-Key"] = this.apiKey;
      } else if (this.jwt) {
        // Fallback to JWT if no API key
        headers["Authorization"] = `Bearer ${this.jwt}`;
      }
    } else if (isAuthOperation) {
      // For auth operations: prefer API key over JWT (auth endpoints should use explicit API key)
      if (this.apiKey) {
        headers["X-API-Key"] = this.apiKey;
      }
      if (this.jwt) {
        headers["Authorization"] = `Bearer ${this.jwt}`;
      }
    } else {
      // For other operations: send both JWT and API key
      if (this.jwt) {
        headers["Authorization"] = `Bearer ${this.jwt}`;
      }
      if (this.apiKey) {
        headers["X-API-Key"] = this.apiKey;
      }
    }
    return headers;
  }

  private getAuthToken(): string | undefined {
    return this.jwt || this.apiKey;
  }

  getApiKey(): string | undefined {
    return this.apiKey;
  }

  /**
   * Get the base URL
   */
  getBaseURL(): string {
    return this.baseURL;
  }

  /**
   * Normalize any thrown error into a typed SDKError so callers can branch on
   * `.code`/`.httpStatus` instead of string-matching a bare platform
   * `TypeError: Network request failed` (bugboard #129).
   *
   * - SDKError (an HTTP error response) passes through unchanged.
   * - An AbortError (our own per-request timeout firing) → code "TIMEOUT".
   * - Anything else (fetch rejects with a TypeError on DNS failure, connection
   *   refused, offline, or TLS error) → code "NETWORK_ERROR".
   *
   * In every network case httpStatus is 0 (no HTTP response was received), which
   * is how the app distinguishes "couldn't reach the gateway" from a real 4xx/5xx.
   */
  private normalizeError(
    error: unknown,
    timeoutMs: number,
    abortedByCaller = false
  ): SDKError {
    if (error instanceof SDKError) {
      return error;
    }
    const name = (error as { name?: string })?.name;
    const message = error instanceof Error ? error.message : String(error);
    if (abortedByCaller) {
      return new NetworkError("request aborted by caller", "ABORTED", {
        cause: "caller-abort",
      });
    }
    if (name === "AbortError") {
      return new NetworkError(
        `request timed out after ${timeoutMs}ms`,
        "TIMEOUT",
        { cause: name }
      );
    }
    return new NetworkError(message || "network request failed", "NETWORK_ERROR", {
      cause: name,
    });
  }

  async request<T = any>(
    method: "GET" | "POST" | "PUT" | "DELETE",
    path: string,
    options: RequestOptions & { body?: any } = {}
  ): Promise<T> {
    const startTime = performance.now(); // Track request start time
    const requestTimeout = options.timeout ?? this.timeout;

    // Describing the query parses the request body, so only do it when there
    // is somewhere for the description to go.
    const queryDetails = this.log.isEnabled
      ? describeQuery(path, options.body)
      : null;

    try {
      let result: T;
      try {
        result = await this.send<T>(method, path, options, startTime);
      } catch (error) {
        // An expired session is renewed once and the request replayed, so an
        // application does not have to wrap every call in its own refresh loop.
        if (!this.canRenewFor(path, error)) {
          throw error;
        }
        const renewed = await this.renewSession();
        if (!renewed) {
          throw error;
        }
        this.log.log(`${method} ${path} retried after renewing the session`);
        result = await this.send<T>(method, path, options, startTime);
      }

      const duration = performance.now() - startTime;
      this.log.log(`${method} ${path} completed in ${duration.toFixed(2)}ms`);
      if (queryDetails) {
        this.log.log(`  ${queryDetails}`);
      }
      return result;
    } catch (error) {
      const duration = performance.now() - startTime;

      // A 404 from find-one is the documented "no such row" answer, not a
      // fault: callers branch on it. Log it as a warning, not an error.
      const is404FindOne =
        path === "/v1/rqlite/find-one" &&
        error instanceof SDKError &&
        error.httpStatus === 404;

      if (is404FindOne) {
        this.log.warn(
          `${method} ${path} returned 404 after ${duration.toFixed(
            2
          )}ms (expected for optional lookups)`
        );
      } else {
        this.log.error(
          `${method} ${path} failed after ${duration.toFixed(2)}ms:`,
          error
        );
        if (queryDetails) {
          this.log.error(`  ${queryDetails}`);
        }
      }

      // A deliberate caller cancel is not a network failure and is not
      // reported as one (bugboard #144): the application asked for it.
      const sdkError =
        error instanceof SDKError
          ? error
          : this.normalizeError(error, requestTimeout);
      if (this.onNetworkError && sdkError.code !== "ABORTED") {
        this.onNetworkError(sdkError, {
          method,
          path,
          isRetry: false,
          attempt: this.maxRetries, // All retries exhausted
        });
      }

      throw sdkError;
    }
  }

  /**
   * One attempt at a request, including its own retries for retryable
   * statuses. Headers are built here rather than by the caller so a replay
   * after a session renewal picks up the new token.
   */
  private async send<T>(
    method: "GET" | "POST" | "PUT" | "DELETE",
    path: string,
    options: RequestOptions & { body?: any },
    startTime: number
  ): Promise<T> {
    const origin = (options.baseURL ?? this.baseURL).replace(/\/$/, "");
    const url = new URL(origin + path);
    if (options.query) {
      Object.entries(options.query).forEach(([key, value]) => {
        url.searchParams.append(key, String(value));
      });
    }

    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      ...this.getAuthHeaders(path),
      ...options.headers,
    };

    // Fail before opening a connection the caller has already cancelled.
    if (options.signal?.aborted) {
      throw new NetworkError("request aborted by caller", "ABORTED", {
        cause: "caller-abort",
      });
    }

    const requestTimeout = options.timeout ?? this.timeout;
    const cancel = this.linkAbort(options.signal, requestTimeout);

    const fetchOptions: RequestInit = {
      method,
      headers,
      signal: cancel.signal,
    };

    if (options.body !== undefined) {
      fetchOptions.body = JSON.stringify(options.body);
    }

    try {
      return await this.requestWithRetry(
        url.toString(),
        fetchOptions,
        0,
        startTime
      );
    } catch (error) {
      throw this.normalizeError(error, requestTimeout, cancel.abortedByCaller());
    } finally {
      cancel.dispose();
    }
  }

  /**
   * Combine the caller's cancellation signal with this request's timeout into
   * one signal, and remember which of the two fired: a timeout may be retried,
   * a caller's cancel never is.
   */
  private linkAbort(signal: AbortSignal | undefined, timeoutMs: number) {
    const controller = new AbortController();
    let byCaller = false;

    if (signal?.aborted) {
      byCaller = true;
      controller.abort();
    }

    const onCallerAbort = () => {
      byCaller = true;
      controller.abort();
    };
    signal?.addEventListener("abort", onCallerAbort, { once: true });

    const timer = setTimeout(() => controller.abort(), timeoutMs);

    let timerCleared = false;
    const clearTimer = () => {
      if (!timerCleared) {
        clearTimeout(timer);
        timerCleared = true;
      }
    };

    return {
      signal: controller.signal,
      abortedByCaller: () => byCaller,
      /** Stop the deadline without detaching the caller's signal. */
      clearTimer,
      dispose: () => {
        clearTimer();
        signal?.removeEventListener("abort", onCallerAbort);
      },
    };
  }

  /** Whether a failure is an expired session this client can renew and replay. */
  private canRenewFor(path: string, error: unknown): boolean {
    if (!this.refresher) return false;
    // Renewing the session by calling the endpoint that renews the session
    // would recurse.
    if (path.startsWith("/v1/auth/refresh")) return false;
    return error instanceof SDKError && error.httpStatus === 401;
  }

  /**
   * Renew the session, at most once at a time. Concurrent requests that all hit
   * a 401 share one renewal instead of racing several against the gateway.
   */
  private async renewSession(): Promise<string | null> {
    if (!this.refreshing) {
      const refresher = this.refresher!;
      this.refreshing = refresher()
        .catch((error) => {
          this.log.warn("session renewal failed:", error);
          return null;
        })
        .finally(() => {
          this.refreshing = undefined;
        });
    }
    return this.refreshing;
  }

  private async requestWithRetry(
    url: string,
    options: RequestInit,
    attempt: number = 0,
    startTime?: number // Track start time for timing across retries
  ): Promise<any> {
    let retryAfterMs: number | undefined;

    try {
      const response = await this.fetch(url, options);

      if (!response.ok) {
        retryAfterMs = parseRetryAfter(response.headers.get("retry-after"));
        let body: any;
        try {
          body = await response.json();
        } catch {
          body = { error: response.statusText };
        }
        throw SDKError.fromResponse(response.status, body);
      }

      // Request succeeded - return response
      const contentType = response.headers.get("content-type");
      if (contentType?.includes("application/json")) {
        return response.json();
      }
      return response.text();
    } catch (error) {
      const isRetryableError =
        error instanceof SDKError &&
        [408, 429, 500, 502, 503, 504].includes(error.httpStatus);

      // Never retry once the request's signal has been aborted (caller cancel
      // or timeout) — bugboard #144. A retry would ignore a user's Cancel.
      const aborted = (options as RequestInit).signal?.aborted === true;

      // Retry on same gateway for retryable HTTP errors
      if (isRetryableError && attempt < this.maxRetries && !aborted) {
        const delay = this.retryDelay(attempt, retryAfterMs);
        this.log.warn(
          `Retrying request in ${delay}ms (attempt ${attempt + 1}/${this.maxRetries})`
        );
        await new Promise((resolve) => setTimeout(resolve, delay));
        return this.requestWithRetry(url, options, attempt + 1, startTime);
      }

      // All retries exhausted - throw error for app to handle
      throw error;
    }
  }

  /**
   * How long to wait before the next attempt.
   *
   * A `Retry-After` from the gateway wins: it is the server saying when it will
   * be ready, and ignoring it — which this did — turns a rate limit into a
   * burst of three more rejected requests.
   *
   * Otherwise the delay grows with the attempt and carries up to 25% jitter.
   * Without jitter every client that failed against the same gateway at the
   * same moment comes back at the same moment, which is how a recovering
   * gateway is knocked over again.
   */
  private retryDelay(attempt: number, retryAfterMs?: number): number {
    if (retryAfterMs !== undefined) {
      return Math.min(retryAfterMs, MAX_RETRY_DELAY_MS);
    }
    const base = this.retryDelayMs * (attempt + 1);
    const jitter = base * RETRY_JITTER_RATIO * Math.random();
    return Math.min(Math.round(base + jitter), MAX_RETRY_DELAY_MS);
  }

  async get<T = any>(path: string, options?: RequestOptions): Promise<T> {
    return this.request<T>("GET", path, options);
  }

  async post<T = any>(
    path: string,
    body?: any,
    options?: RequestOptions
  ): Promise<T> {
    return this.request<T>("POST", path, { ...options, body });
  }

  async put<T = any>(
    path: string,
    body?: any,
    options?: RequestOptions
  ): Promise<T> {
    return this.request<T>("PUT", path, { ...options, body });
  }

  async delete<T = any>(path: string, options?: RequestOptions): Promise<T> {
    return this.request<T>("DELETE", path, options);
  }

  /**
   * Upload a file using multipart/form-data
   * This is a special method for file uploads that bypasses JSON serialization
   */
  async uploadFile<T = any>(
    path: string,
    formData: FormData,
    options?: Pick<RequestOptions, "timeout" | "signal">
  ): Promise<T> {
    const startTime = performance.now(); // Track upload start time
    const url = new URL(this.baseURL + path);
    const headers: Record<string, string> = {
      ...this.getAuthHeaders(path),
      // Don't set Content-Type - browser will set it with boundary
    };

    // Fail fast if the caller already aborted before we even start.
    if (options?.signal?.aborted) {
      throw new NetworkError("upload aborted by caller", "ABORTED", {
        cause: "caller-abort",
      });
    }

    const requestTimeout = options?.timeout ?? this.timeout * 5; // 5x timeout for uploads
    const cancel = this.linkAbort(options?.signal, requestTimeout);

    const fetchOptions: RequestInit = {
      method: "POST",
      headers,
      body: formData,
      signal: cancel.signal,
    };

    try {
      const result = await this.requestWithRetry(
        url.toString(),
        fetchOptions,
        0,
        startTime
      );
      const duration = performance.now() - startTime;
      this.log.log(
        `POST ${path} (upload) completed in ${duration.toFixed(2)}ms`
      );
      return result;
    } catch (error) {
      const duration = performance.now() - startTime;

      // A deliberate caller cancel is a distinct, non-retryable outcome — never
      // a timeout, and not surfaced through the network-error callback (it's not
      // a network failure, it's a user action).
      if (cancel.abortedByCaller()) {
        this.log.log(
          `POST ${path} (upload) aborted by caller after ${duration.toFixed(
            2
          )}ms`
        );
        throw new NetworkError("upload aborted by caller", "ABORTED", {
          cause: "caller-abort",
        });
      }

      this.log.error(
        `POST ${path} (upload) failed after ${duration.toFixed(2)}ms:`,
        error
      );

      // Normalize an internal-timeout AbortError to a TIMEOUT error; a real
      // HTTP SDKError passes through unchanged.
      const normalized = this.normalizeError(error, requestTimeout);
      if (this.onNetworkError) {
        this.onNetworkError(normalized, {
          method: "POST",
          path,
          isRetry: false,
          attempt: this.maxRetries,
        });
      }

      throw normalized;
    } finally {
      cancel.dispose();
    }
  }

  /**
   * Get a binary response (returns Response object for streaming)
   */
  async getBinary(
    path: string,
    options?: Pick<RequestOptions, "timeout" | "signal">
  ): Promise<Response> {
    const url = new URL(this.baseURL + path);
    const headers: Record<string, string> = {
      ...this.getAuthHeaders(path),
    };

    const requestTimeout = options?.timeout ?? this.timeout * 5; // 5x timeout for downloads
    const cancel = this.linkAbort(options?.signal, requestTimeout);

    const fetchOptions: RequestInit = {
      method: "GET",
      headers,
      signal: cancel.signal,
    };

    try {
      const response = await this.fetch(url.toString(), fetchOptions);

      // The timeout covers reaching the gateway, not draining the body. It
      // used to be left armed on the success path, so a download still
      // arriving was aborted mid-stream once it passed the deadline. The
      // caller's own signal stays attached, so cancelling a download still
      // works.
      cancel.clearTimer();

      if (!response.ok) {
        cancel.dispose();
        const errorBody = await response.json().catch(() => ({
          error: response.statusText,
        }));
        throw SDKError.fromResponse(response.status, errorBody);
      }
      return response;
    } catch (error) {
      cancel.dispose();
      const normalized = this.normalizeError(
        error,
        requestTimeout,
        cancel.abortedByCaller()
      );
      if (this.onNetworkError && normalized.code !== "ABORTED") {
        this.onNetworkError(normalized, {
          method: "GET",
          path,
          isRetry: false,
          attempt: 0,
        });
      }
      throw normalized;
    }
  }

  getToken(): string | undefined {
    return this.getAuthToken();
  }
}
