import { describe, it, expect } from 'vitest';
import { VaultClient } from '../../../src/vault/client';

describe('VaultClient', () => {
  it('constructs with config', () => {
    const client = new VaultClient({
      guardians: [{ address: '127.0.0.1', port: 7500 }],
      hmacKey: new Uint8Array(32),
      identityHex: 'a'.repeat(64),
    });
    expect(client).toBeDefined();
  });

  it('clearSessions does not throw', () => {
    const client = new VaultClient({
      guardians: [],
      hmacKey: new Uint8Array(32),
      identityHex: 'b'.repeat(64),
    });
    expect(() => client.clearSessions()).not.toThrow();
  });
});
