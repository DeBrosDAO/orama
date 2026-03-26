import type { IAuthStrategy, RequestContext } from "../interfaces/IAuthStrategy";

/**
 * Authentication type for different operations
 */
type AuthType = "api-key-only" | "api-key-preferred" | "jwt-preferred" | "both";

/**
 * Path-based authentication strategy
 * Determines which auth credentials to use based on the request path
 */
export class PathBasedAuthStrategy implements IAuthStrategy {
  private apiKey?: string;
  private jwt?: string;

  /**
   * Mapping of path patterns to auth types
   */
  private readonly authRules: Array<{ pattern: string; type: AuthType }> = [
    // Database, PubSub, Proxy, Cache: prefer API key
    { pattern: "/v1/rqlite/", type: "api-key-only" },
    { pattern: "/v1/pubsub/", type: "api-key-only" },
    { pattern: "/v1/proxy/", type: "api-key-only" },
    { pattern: "/v1/cache/", type: "api-key-only" },
    // Auth operations: prefer API key
    { pattern: "/v1/auth/", type: "api-key-preferred" },
  ];

  constructor(apiKey?: string, jwt?: string) {
    this.apiKey = apiKey;
    this.jwt = jwt;
  }

  /**
   * Get authentication headers for a request
   */
  getHeaders(context: RequestContext): Record<string, string> {
    const headers: Record<string, string> = {};
    const authType = this.detectAuthType(context.path);

    switch (authType) {
      case "api-key-only":
        if (this.apiKey) {
          headers["X-API-Key"] = this.apiKey;
        } else if (this.jwt) {
          // Fallback to JWT if no API key
          headers["Authorization"] = `Bearer ${this.jwt}`;
        }
        break;

      case "api-key-preferred":
        if (this.apiKey) {
          headers["X-API-Key"] = this.apiKey;
        }
        if (this.jwt) {
          headers["Authorization"] = `Bearer ${this.jwt}`;
        }
        break;

      case "jwt-preferred":
        if (this.jwt) {
          headers["Authorization"] = `Bearer ${this.jwt}`;
        }
        if (this.apiKey) {
          headers["X-API-Key"] = this.apiKey;
        }
        break;

      case "both":
        if (this.jwt) {
          headers["Authorization"] = `Bearer ${this.jwt}`;
        }
        if (this.apiKey) {
          headers["X-API-Key"] = this.apiKey;
        }
        break;
    }

    return headers;
  }

  /**
   * Set API key
   */
  setApiKey(apiKey?: string): void {
    this.apiKey = apiKey;
  }

  /**
   * Set JWT token
   */
  setJwt(jwt?: string): void {
    this.jwt = jwt;
  }

  /**
   * Detect auth type based on path
   */
  private detectAuthType(path: string): AuthType {
    for (const rule of this.authRules) {
      if (path.includes(rule.pattern)) {
        return rule.type;
      }
    }
    // Default: send both if available
    return "both";
  }
}
