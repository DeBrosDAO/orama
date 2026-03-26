import { GuardianClient, GuardianError } from './guardian';
import type { GuardianEndpoint, GuardianErrorCode, FanOutResult } from './types';

/**
 * Fan out an operation to multiple guardians in parallel.
 * Returns results from all guardians (both successes and failures).
 */
export async function fanOut<T>(
  guardians: GuardianEndpoint[],
  operation: (client: GuardianClient) => Promise<T>,
): Promise<FanOutResult<T>[]> {
  const results = await Promise.allSettled(
    guardians.map(async (endpoint) => {
      const client = new GuardianClient(endpoint);
      const result = await operation(client);
      return { endpoint, result, error: null } as FanOutResult<T>;
    }),
  );

  return results.map((r, i) => {
    if (r.status === 'fulfilled') return r.value;
    const reason = r.reason as Error;
    const errorCode: GuardianErrorCode | undefined = reason instanceof GuardianError ? reason.code : undefined;
    return {
      endpoint: guardians[i]!,
      result: null,
      error: reason.message,
      errorCode,
    };
  });
}

/**
 * Fan out an indexed operation to multiple guardians in parallel.
 * The operation receives the index so each guardian can get a different share.
 */
export async function fanOutIndexed<T>(
  guardians: GuardianEndpoint[],
  operation: (client: GuardianClient, index: number) => Promise<T>,
): Promise<FanOutResult<T>[]> {
  const results = await Promise.allSettled(
    guardians.map(async (endpoint, i) => {
      const client = new GuardianClient(endpoint);
      const result = await operation(client, i);
      return { endpoint, result, error: null } as FanOutResult<T>;
    }),
  );

  return results.map((r, i) => {
    if (r.status === 'fulfilled') return r.value;
    const reason = r.reason as Error;
    const errorCode: GuardianErrorCode | undefined = reason instanceof GuardianError ? reason.code : undefined;
    return {
      endpoint: guardians[i]!,
      result: null,
      error: reason.message,
      errorCode,
    };
  });
}

/**
 * Race a promise against a timeout.
 */
export function withTimeout<T>(promise: Promise<T>, ms: number): Promise<T> {
  return Promise.race([
    promise,
    new Promise<never>((_, reject) =>
      setTimeout(() => reject(new Error(`timeout after ${ms}ms`)), ms),
    ),
  ]);
}

/**
 * Retry a function with exponential backoff.
 * Does not retry auth or not-found errors.
 */
export async function withRetry<T>(fn: () => Promise<T>, attempts = 3): Promise<T> {
  let lastError: Error | undefined;
  for (let i = 0; i < attempts; i++) {
    try {
      return await fn();
    } catch (err) {
      lastError = err as Error;
      if (err instanceof GuardianError && (err.code === 'AUTH' || err.code === 'NOT_FOUND')) {
        throw err;
      }
      if (i < attempts - 1) {
        await new Promise((r) => setTimeout(r, 200 * Math.pow(2, i)));
      }
    }
  }
  throw lastError!;
}
