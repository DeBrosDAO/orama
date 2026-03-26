/**
 * TLS Configuration for development/staging environments
 *
 * WARNING: Only use this in development/testing environments!
 * DO NOT disable certificate validation in production.
 */
export class TLSConfiguration {
  /**
   * Create fetch function with proper TLS configuration
   */
  static createFetchWithTLSConfig(): typeof fetch {
    // Only allow insecure TLS in development
    if (this.shouldAllowInsecure()) {
      this.configureInsecureTLS();
    }

    return globalThis.fetch;
  }

  /**
   * Check if insecure TLS should be allowed
   */
  private static shouldAllowInsecure(): boolean {
    // Check if we're in Node.js environment
    if (typeof process === "undefined" || !process.versions?.node) {
      return false;
    }

    // Only allow in non-production with explicit flag
    const isProduction = process.env.NODE_ENV === "production";
    const allowInsecure = process.env.DEBROS_ALLOW_INSECURE_TLS === "true";

    return !isProduction && allowInsecure;
  }

  /**
   * Configure Node.js to allow insecure TLS
   * WARNING: Only call in development!
   */
  private static configureInsecureTLS(): void {
    if (typeof process !== "undefined" && process.env) {
      // Allow self-signed/staging certificates for development
      process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";

      if (typeof console !== "undefined") {
        console.warn(
          "[TLSConfiguration] WARNING: TLS certificate validation disabled for development. " +
          "DO NOT use in production!"
        );
      }
    }
  }
}
