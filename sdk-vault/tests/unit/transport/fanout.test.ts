import { describe, it, expect } from 'vitest';
import { withTimeout } from '../../../src/transport/fanout';

describe('withTimeout', () => {
  it('resolves when promise completes before timeout', async () => {
    const result = await withTimeout(Promise.resolve('ok'), 1000);
    expect(result).toBe('ok');
  });

  it('rejects when timeout expires', async () => {
    const slow = new Promise<string>((resolve) => setTimeout(() => resolve('late'), 500));
    await expect(withTimeout(slow, 50)).rejects.toThrow('timeout after 50ms');
  });

  it('propagates original error', async () => {
    const failing = Promise.reject(new Error('original'));
    await expect(withTimeout(failing, 1000)).rejects.toThrow('original');
  });

  /**
   * The timer used to be left running after the race settled. Every read of a
   * secret calls this once per guardian, so a CLI that fetched one secret held
   * the event loop open for the full ten-second timeout before it could exit.
   */
  it('clears its timer once the race settles', async () => {
    const cleared: unknown[] = [];
    const realClear = globalThis.clearTimeout;
    globalThis.clearTimeout = ((handle: never) => {
      cleared.push(handle);
      return realClear(handle);
    }) as typeof clearTimeout;

    try {
      await withTimeout(Promise.resolve('ok'), 10_000);
      await withTimeout(Promise.reject(new Error('nope')), 10_000).catch(() => undefined);
    } finally {
      globalThis.clearTimeout = realClear;
    }

    expect(cleared).toHaveLength(2);
    expect(cleared.every((handle) => handle !== undefined)).toBe(true);
  });
});
