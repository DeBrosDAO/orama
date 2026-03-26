import { describe, it, expect } from 'vitest';
import { AuthClient } from '../../../src/vault/auth';

describe('AuthClient', () => {
  it('constructs with identity', () => {
    const auth = new AuthClient('a'.repeat(64));
    expect(auth.getIdentityHex()).toBe('a'.repeat(64));
  });

  it('clearSessions does not throw', () => {
    const auth = new AuthClient('b'.repeat(64));
    expect(() => auth.clearSessions()).not.toThrow();
  });

  it('authenticateAll returns empty for no endpoints', async () => {
    const auth = new AuthClient('c'.repeat(64));
    const results = await auth.authenticateAll([]);
    expect(results).toEqual([]);
  });
});
