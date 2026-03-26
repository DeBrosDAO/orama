/**
 * Request context for authentication
 */
export interface RequestContext {
  path: string;
  method: string;
}

/**
 * Authentication strategy interface
 * Provides abstraction for different authentication header strategies
 */
export interface IAuthStrategy {
  /**
   * Get authentication headers for a request
   */
  getHeaders(context: RequestContext): Record<string, string>;

  /**
   * Set API key
   */
  setApiKey(apiKey?: string): void;

  /**
   * Set JWT token
   */
  setJwt(jwt?: string): void;
}
