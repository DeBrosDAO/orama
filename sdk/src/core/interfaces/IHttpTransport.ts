/**
 * HTTP Request options
 */
export interface RequestOptions {
  headers?: Record<string, string>;
  query?: Record<string, string | number | boolean>;
  timeout?: number;
}

/**
 * HTTP Transport abstraction interface
 * Provides a testable abstraction layer for HTTP operations
 */
export interface IHttpTransport {
  /**
   * Perform GET request
   */
  get<T = any>(path: string, options?: RequestOptions): Promise<T>;

  /**
   * Perform POST request
   */
  post<T = any>(path: string, body?: any, options?: RequestOptions): Promise<T>;

  /**
   * Perform PUT request
   */
  put<T = any>(path: string, body?: any, options?: RequestOptions): Promise<T>;

  /**
   * Perform DELETE request
   */
  delete<T = any>(path: string, options?: RequestOptions): Promise<T>;

  /**
   * Upload file using multipart/form-data
   */
  uploadFile<T = any>(
    path: string,
    formData: FormData,
    options?: { timeout?: number }
  ): Promise<T>;

  /**
   * Get binary response (returns Response object for streaming)
   */
  getBinary(path: string): Promise<Response>;

  /**
   * Get base URL
   */
  getBaseURL(): string;

  /**
   * Get API key
   */
  getApiKey(): string | undefined;

  /**
   * Get current token (JWT or API key)
   */
  getToken(): string | undefined;

  /**
   * Set API key for authentication
   */
  setApiKey(apiKey?: string): void;

  /**
   * Set JWT token for authentication
   */
  setJwt(jwt?: string): void;
}
