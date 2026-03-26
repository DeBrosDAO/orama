/**
 * Request logger for debugging HTTP operations
 */
export class RequestLogger {
  private readonly debug: boolean;

  constructor(debug: boolean = false) {
    this.debug = debug;
  }

  /**
   * Log successful request
   */
  logSuccess(
    method: string,
    path: string,
    duration: number,
    queryDetails?: string
  ): void {
    if (typeof console === "undefined") return;

    const logMessage = `[HttpClient] ${method} ${path} completed in ${duration.toFixed(2)}ms`;

    if (queryDetails && this.debug) {
      console.log(logMessage);
      console.log(`[HttpClient]   ${queryDetails}`);
    } else {
      console.log(logMessage);
    }
  }

  /**
   * Log failed request
   */
  logError(
    method: string,
    path: string,
    duration: number,
    error: any,
    queryDetails?: string
  ): void {
    if (typeof console === "undefined") return;

    // Special handling for 404 on find-one (expected behavior)
    const is404FindOne =
      path === "/v1/rqlite/find-one" &&
      error?.httpStatus === 404;

    if (is404FindOne) {
      console.warn(
        `[HttpClient] ${method} ${path} returned 404 after ${duration.toFixed(2)}ms (expected for optional lookups)`
      );
      return;
    }

    const errorMessage = `[HttpClient] ${method} ${path} failed after ${duration.toFixed(2)}ms:`;
    console.error(errorMessage, error);

    if (queryDetails && this.debug) {
      console.error(`[HttpClient]   ${queryDetails}`);
    }
  }

  /**
   * Extract query details from request for logging
   */
  extractQueryDetails(path: string, body?: any): string | null {
    if (!this.debug) return null;

    const isRqliteOperation = path.includes("/v1/rqlite/");
    if (!isRqliteOperation || !body) return null;

    try {
      const parsedBody = typeof body === "string" ? JSON.parse(body) : body;

      // Direct SQL query
      if (parsedBody.sql) {
        let details = `SQL: ${parsedBody.sql}`;
        if (parsedBody.args && parsedBody.args.length > 0) {
          details += ` | Args: [${parsedBody.args
            .map((a: any) => (typeof a === "string" ? `"${a}"` : a))
            .join(", ")}]`;
        }
        return details;
      }

      // Table-based query
      if (parsedBody.table) {
        let details = `Table: ${parsedBody.table}`;
        if (parsedBody.criteria && Object.keys(parsedBody.criteria).length > 0) {
          details += ` | Criteria: ${JSON.stringify(parsedBody.criteria)}`;
        }
        if (parsedBody.options) {
          details += ` | Options: ${JSON.stringify(parsedBody.options)}`;
        }
        if (parsedBody.select) {
          details += ` | Select: ${JSON.stringify(parsedBody.select)}`;
        }
        if (parsedBody.where) {
          details += ` | Where: ${JSON.stringify(parsedBody.where)}`;
        }
        if (parsedBody.limit) {
          details += ` | Limit: ${parsedBody.limit}`;
        }
        if (parsedBody.offset) {
          details += ` | Offset: ${parsedBody.offset}`;
        }
        return details;
      }
    } catch {
      // Failed to parse, ignore
    }

    return null;
  }
}
