import { SDKError } from "../errors";

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
 * Create a fetch function with proper TLS configuration for staging certificates
 * In Node.js, we need to configure TLS to accept Let's Encrypt staging certificates
 */
function createFetchWithTLSConfig(): typeof fetch {
  // Check if we're in a Node.js environment
  if (typeof process !== "undefined" && process.versions?.node) {
    // For testing/staging/development: allow staging certificates
    // Let's Encrypt staging certificates are self-signed and not trusted by default
    const isDevelopmentOrStaging =
      process.env.NODE_ENV !== "production" ||
      process.env.DEBROS_ALLOW_STAGING_CERTS === "true" ||
      process.env.DEBROS_USE_HTTPS === "true";

    if (isDevelopmentOrStaging) {
      // Allow self-signed/staging certificates
      // WARNING: Only use this in development/testing environments
      process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
    }
  }
  return globalThis.fetch;
}

export class HttpClient {
  private baseURL: string;
  private timeout: number;
  private maxRetries: number;
  private retryDelayMs: number;
  private fetch: typeof fetch;
  private apiKey?: string;
  private jwt?: string;
  private debug: boolean;
  private onNetworkError?: NetworkErrorCallback;

  constructor(config: HttpClientConfig) {
    this.baseURL = config.baseURL.replace(/\/$/, "");
    this.timeout = config.timeout ?? 60000;
    this.maxRetries = config.maxRetries ?? 3;
    this.retryDelayMs = config.retryDelayMs ?? 1000;
    // Use provided fetch or create one with proper TLS configuration for staging certificates
    this.fetch = config.fetch ?? createFetchWithTLSConfig();
    this.debug = config.debug ?? false;
    this.onNetworkError = config.onNetworkError;
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
    if (typeof console !== "undefined") {
      console.log(
        "[HttpClient] JWT set:",
        !!jwt,
        "API key still present:",
        !!this.apiKey
      );
    }
  }

  private getAuthHeaders(path: string): Record<string, string> {
    const headers: Record<string, string> = {};

    // For database, pubsub, proxy, and cache operations, ONLY use API key to avoid JWT user context
    // interfering with namespace-level authorization
    const isDbOperation = path.includes("/v1/rqlite/");
    const isPubSubOperation = path.includes("/v1/pubsub/");
    const isProxyOperation = path.includes("/v1/proxy/");
    const isCacheOperation = path.includes("/v1/cache/");

    // For auth operations, prefer API key over JWT to ensure proper authentication
    const isAuthOperation = path.includes("/v1/auth/");

    if (
      isDbOperation ||
      isPubSubOperation ||
      isProxyOperation ||
      isCacheOperation
    ) {
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

    // Extract and log SQL query details for rqlite operations
    const isRqliteOperation = path.includes("/v1/rqlite/");
    let queryDetails: string | null = null;
    if (isRqliteOperation && options.body) {
      try {
        const body =
          typeof options.body === "string"
            ? JSON.parse(options.body)
            : options.body;

        if (body.sql) {
          // Direct SQL query (query/exec endpoints)
          queryDetails = `SQL: ${body.sql}`;
          if (body.args && body.args.length > 0) {
            queryDetails += ` | Args: [${body.args
              .map((a: any) => (typeof a === "string" ? `"${a}"` : a))
              .join(", ")}]`;
          }
        } else if (body.table) {
          // Table-based query (find/find-one/select endpoints)
          queryDetails = `Table: ${body.table}`;
          if (body.criteria && Object.keys(body.criteria).length > 0) {
            queryDetails += ` | Criteria: ${JSON.stringify(body.criteria)}`;
          }
          if (body.options) {
            queryDetails += ` | Options: ${JSON.stringify(body.options)}`;
          }
          if (body.select) {
            queryDetails += ` | Select: ${JSON.stringify(body.select)}`;
          }
          if (body.where) {
            queryDetails += ` | Where: ${JSON.stringify(body.where)}`;
          }
          if (body.limit) {
            queryDetails += ` | Limit: ${body.limit}`;
          }
          if (body.offset) {
            queryDetails += ` | Offset: ${body.offset}`;
          }
        }
      } catch (e) {
        // Failed to parse body, ignore
      }
    }

    try {
      const result = await this.requestWithRetry(
        url.toString(),
        fetchOptions,
        0,
        startTime
      );
      const duration = performance.now() - startTime;
      if (typeof console !== "undefined") {
        const logMessage = `[HttpClient] ${method} ${path} completed in ${duration.toFixed(
          2
        )}ms`;
        if (queryDetails && this.debug) {
          console.log(logMessage);
          console.log(`[HttpClient]   ${queryDetails}`);
        } else {
          console.log(logMessage);
        }
      }
      return result;
    } catch (error) {
      const duration = performance.now() - startTime;
      if (typeof console !== "undefined") {
        // For 404 errors on find-one calls, log at warn level (not error) since "not found" is expected
        // Application layer handles these cases in try-catch blocks
        const is404FindOne =
          path === "/v1/rqlite/find-one" &&
          error instanceof SDKError &&
          error.httpStatus === 404;

        if (is404FindOne) {
          // Log as warning for visibility, but not as error since it's expected behavior
          console.warn(
            `[HttpClient] ${method} ${path} returned 404 after ${duration.toFixed(
              2
            )}ms (expected for optional lookups)`
          );
        } else {
          const errorMessage = `[HttpClient] ${method} ${path} failed after ${duration.toFixed(
            2
          )}ms:`;
          console.error(errorMessage, error);
          if (queryDetails && this.debug) {
            console.error(`[HttpClient]   ${queryDetails}`);
          }
        }
      }

      // Call the network error callback if configured
      // This allows the app to trigger gateway failover
      if (this.onNetworkError) {
        // Convert native errors (TypeError, AbortError) to SDKError for the callback
        const sdkError =
          error instanceof SDKError
            ? error
            : new SDKError(
                error instanceof Error ? error.message : String(error),
                0, // httpStatus 0 indicates network-level failure
                "NETWORK_ERROR"
              );
        this.onNetworkError(sdkError, {
          method,
          path,
          isRetry: false,
          attempt: this.maxRetries, // All retries exhausted
        });
      }

      throw error;
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

      // Retry on same gateway for retryable HTTP errors
      if (isRetryableError && attempt < this.maxRetries) {
        if (typeof console !== "undefined") {
          console.warn(
            `[HttpClient] Retrying request (attempt ${attempt + 1}/${this.maxRetries})`
          );
        }
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
    }
  ): Promise<T> {
    const startTime = performance.now(); // Track upload start time
    const url = new URL(this.baseURL + path);
    const headers: Record<string, string> = {
      ...this.getAuthHeaders(path),
      // Don't set Content-Type - browser will set it with boundary
    };

    const controller = new AbortController();
    const requestTimeout = options?.timeout ?? this.timeout * 5; // 5x timeout for uploads
    const timeoutId = setTimeout(() => controller.abort(), requestTimeout);

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
      if (typeof console !== "undefined") {
        console.log(
          `[HttpClient] POST ${path} (upload) completed in ${duration.toFixed(
            2
          )}ms`
        );
      }
      return result;
    } catch (error) {
      const duration = performance.now() - startTime;
      if (typeof console !== "undefined") {
        console.error(
          `[HttpClient] POST ${path} (upload) failed after ${duration.toFixed(
            2
          )}ms:`,
          error
        );
      }

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
          method: "POST",
          path,
          isRetry: false,
          attempt: this.maxRetries,
        });
      }

      throw error;
    } finally {
      clearTimeout(timeoutId);
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
