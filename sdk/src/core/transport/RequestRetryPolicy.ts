import type { IRetryPolicy } from "../interfaces/IRetryPolicy";
import { SDKError } from "../../errors";

/**
 * Exponential backoff retry policy
 * Retries failed requests with increasing delays
 */
export class ExponentialBackoffRetryPolicy implements IRetryPolicy {
  private readonly maxRetries: number;
  private readonly baseDelayMs: number;

  /**
   * HTTP status codes that should trigger a retry
   */
  private readonly retryableStatusCodes = [408, 429, 500, 502, 503, 504];

  constructor(maxRetries: number = 3, baseDelayMs: number = 1000) {
    this.maxRetries = maxRetries;
    this.baseDelayMs = baseDelayMs;
  }

  /**
   * Determine if request should be retried
   */
  shouldRetry(error: any, attempt: number): boolean {
    // Don't retry if max attempts reached
    if (attempt >= this.maxRetries) {
      return false;
    }

    // Retry on retryable HTTP errors
    if (error instanceof SDKError) {
      return this.retryableStatusCodes.includes(error.httpStatus);
    }

    // Don't retry other errors
    return false;
  }

  /**
   * Get delay before next retry (exponential backoff)
   */
  getDelay(attempt: number): number {
    return this.baseDelayMs * (attempt + 1);
  }

  /**
   * Get maximum number of retry attempts
   */
  getMaxRetries(): number {
    return this.maxRetries;
  }
}
