/**
 * Retry configuration
 */
export interface RetryConfig {
  /**
   * Maximum number of retry attempts
   */
  maxAttempts: number;

  /**
   * Function to calculate backoff delay in milliseconds
   */
  backoffMs: (attempt: number) => number;

  /**
   * Function to determine if error should trigger retry
   */
  shouldRetry: (error: any) => boolean;
}

/**
 * Retry an operation with exponential backoff
 * @param operation - The async operation to retry
 * @param config - Retry configuration
 * @returns Promise resolving to operation result
 * @throws Last error if all retries exhausted
 */
export async function retryWithBackoff<T>(
  operation: () => Promise<T>,
  config: RetryConfig
): Promise<T> {
  let lastError: Error | null = null;

  for (let attempt = 1; attempt <= config.maxAttempts; attempt++) {
    try {
      return await operation();
    } catch (error: any) {
      lastError = error instanceof Error ? error : new Error(String(error));

      // Check if we should retry this error
      if (!config.shouldRetry(error)) {
        throw error;
      }

      // If this was the last attempt, throw
      if (attempt === config.maxAttempts) {
        throw error;
      }

      // Wait before next attempt
      const delay = config.backoffMs(attempt);
      await new Promise((resolve) => setTimeout(resolve, delay));
    }
  }

  // Fallback (should never reach here)
  throw lastError || new Error("Retry failed");
}
