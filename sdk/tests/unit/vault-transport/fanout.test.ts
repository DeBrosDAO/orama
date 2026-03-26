import { describe, it, expect } from 'vitest';
import { withTimeout } from '../../../src/vault/transport/fanout';

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
});
