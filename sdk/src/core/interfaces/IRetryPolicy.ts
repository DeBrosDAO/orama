/**
 * Retry policy interface
 * Provides abstraction for retry logic and backoff strategies
 */
export interface IRetryPolicy {
  /**
   * Determine if request should be retried
   */
  shouldRetry(error: any, attempt: number): boolean;

  /**
   * Get delay before next retry attempt (in milliseconds)
   */
  getDelay(attempt: number): number;

  /**
   * Get maximum number of retry attempts
   */
  getMaxRetries(): number;
}
