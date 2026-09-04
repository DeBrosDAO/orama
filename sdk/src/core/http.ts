import { SDKError } from "../errors";
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
  private normalizeError(error: unknown, timeoutMs: number): SDKError {
    if (error instanceof SDKError) {
      return error;
    }
    const name = (error as { name?: string })?.name;
    const message = error instanceof Error ? error.message : String(error);
    if (name === "AbortError") {
      return new SDKError(
        `request timed out after ${timeoutMs}ms`,
        0,
        "TIMEOUT",
        { cause: name }
      );
    }
    return new SDKError(
      message || "network request failed",
      0,
      "NETWORK_ERROR",
      { cause: name }
    );
  }

  async request<T = any>(
    method: "GET" | "POST" | "PUT" | "DELETE",
    path: string,
    options: {
      body?: any;
      headers?: Record<string, string>;
      query?: Record<string, string | number | boolean>;
      timeout?: number; // Per-request timeout override
    } = {}
  ): Promise<T> {
    const startTime = performance.now(); // Track request start time
    const url = new URL(this.baseURL + path);
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

    const controller = new AbortController();
    const requestTimeout = options.timeout ?? this.timeout; // Use override or default
    const timeoutId = setTimeout(() => controller.abort(), requestTimeout);

    const fetchOptions: RequestInit = {
      method,
      headers,
      signal: controller.signal,
    };

    if (options.body !== undefined) {
      fetchOptions.body = JSON.stringify(options.body);
    }

    // Describing the query parses the request body, so only do it when there
    // is somewhere for the description to go.
    const queryDetails = this.log.isEnabled
      ? describeQuery(path, options.body)
      : null;

    try {
      const result = await this.requestWithRetry(
        url.toString(),
        fetchOptions,
        0,
        startTime
      );
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

      // Normalize native errors (TypeError, AbortError) into a typed SDKError
      // so the app gets a stable `.code`/`.httpStatus` instead of a bare
      // platform "Network request failed" (bugboard #129).
      const sdkError = this.normalizeError(error, requestTimeout);

      // Call the network error callback if configured. This allows the app to
      // trigger gateway failover.
      if (this.onNetworkError) {
        this.onNetworkError(sdkError, {
          method,
          path,
          isRetry: false,
          attempt: this.maxRetries, // All retries exhausted
        });
      }

      throw sdkError;
    } finally {
      clearTimeout(timeoutId);
    }
  }

  private async requestWithRetry(
    url: string,
    options: RequestInit,
    attempt: number = 0,
    startTime?: number // Track start time for timing across retries
  ): Promise<any> {
    try {
      const response = await this.fetch(url, options);

      if (!response.ok) {
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
        this.log.warn(
          `Retrying request (attempt ${attempt + 1}/${this.maxRetries})`
        );
        await new Promise((resolve) =>
          setTimeout(resolve, this.retryDelayMs * (attempt + 1))
        );
        return this.requestWithRetry(url, options, attempt + 1, startTime);
      }

      // All retries exhausted - throw error for app to handle
      throw error;
    }
  }

  async get<T = any>(
    path: string,
    options?: Omit<Parameters<typeof this.request>[2], "body">
  ): Promise<T> {
    return this.request<T>("GET", path, options);
  }

  async post<T = any>(
    path: string,
    body?: any,
    options?: Omit<Parameters<typeof this.request>[2], "body">
  ): Promise<T> {
    return this.request<T>("POST", path, { ...options, body });
  }

  async put<T = any>(
    path: string,
    body?: any,
    options?: Omit<Parameters<typeof this.request>[2], "body">
  ): Promise<T> {
    return this.request<T>("PUT", path, { ...options, body });
  }

  async delete<T = any>(
    path: string,
    options?: Omit<Parameters<typeof this.request>[2], "body">
  ): Promise<T> {
    return this.request<T>("DELETE", path, options);
  }

  /**
   * Upload a file using multipart/form-data
   * This is a special method for file uploads that bypasses JSON serialization
   */
  async uploadFile<T = any>(
    path: string,
    formData: FormData,
    options?: {
      timeout?: number;
      /**
       * Optional caller AbortSignal (bugboard #144). When it fires, the
       * in-flight upload is terminated at the socket and the promise rejects
       * with an SDKError whose code is "ABORTED" — distinct from an internal
       * timeout ("TIMEOUT") — and it is never retried. Lets a UI Cancel button
       * actually stop the bytes going out.
       */
      signal?: AbortSignal;
    }
  ): Promise<T> {
    const startTime = performance.now(); // Track upload start time
    const url = new URL(this.baseURL + path);
    const headers: Record<string, string> = {
      ...this.getAuthHeaders(path),
      // Don't set Content-Type - browser will set it with boundary
    };

    // Fail fast if the caller already aborted before we even start.
    if (options?.signal?.aborted) {
      throw new SDKError("upload aborted by caller", 0, "ABORTED", {
        cause: "caller-abort",
      });
    }

    const controller = new AbortController();
    const requestTimeout = options?.timeout ?? this.timeout * 5; // 5x timeout for uploads
    const timeoutId = setTimeout(() => controller.abort(), requestTimeout);

    // Forward a caller abort to the in-flight request (socket-level) and record
    // that the cancel was caller-initiated so it is distinguishable from the
    // internal timeout (which may retry; a caller abort must NOT).
    let abortedByCaller = false;
    const onCallerAbort = () => {
      abortedByCaller = true;
      controller.abort();
    };
    options?.signal?.addEventListener("abort", onCallerAbort, { once: true });

    const fetchOptions: RequestInit = {
      method: "POST",
      headers,
      body: formData,
      signal: controller.signal,
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
      if (abortedByCaller) {
        this.log.log(
          `POST ${path} (upload) aborted by caller after ${duration.toFixed(
            2
          )}ms`
        );
        throw new SDKError("upload aborted by caller", 0, "ABORTED", {
          cause: "caller-abort",
        });
      }

      this.log.error(
        `POST ${path} (upload) failed after ${duration.toFixed(2)}ms:`,
        error
      );

      // Normalize an internal-timeout AbortError to a TIMEOUT SDKError; a real
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
      clearTimeout(timeoutId);
      options?.signal?.removeEventListener("abort", onCallerAbort);
    }
  }

  /**
   * Get a binary response (returns Response object for streaming)
   */
  async getBinary(path: string): Promise<Response> {
    const url = new URL(this.baseURL + path);
    const headers: Record<string, string> = {
      ...this.getAuthHeaders(path),
    };

    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), this.timeout * 5); // 5x timeout for downloads

    const fetchOptions: RequestInit = {
      method: "GET",
      headers,
      signal: controller.signal,
    };

    try {
      const response = await this.fetch(url.toString(), fetchOptions);
      if (!response.ok) {
        clearTimeout(timeoutId);
        const errorBody = await response.json().catch(() => ({
          error: response.statusText,
        }));
        throw SDKError.fromResponse(response.status, errorBody);
      }
      return response;
    } catch (error) {
      clearTimeout(timeoutId);

      // Call the network error callback if configured
      if (this.onNetworkError) {
        const sdkError =
          error instanceof SDKError
            ? error
            : new SDKError(
                error instanceof Error ? error.message : String(error),
                0,
                "NETWORK_ERROR"
              );
        this.onNetworkError(sdkError, {
          method: "GET",
          path,
          isRetry: false,
          attempt: 0,
        });
      }

      throw error;
    }
  }

  getToken(): string | undefined {
    return this.getAuthToken();
  }
}
