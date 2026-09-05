import { describe, it, expect } from 'vitest';
import { GuardianClient, GuardianError } from '../../../src/transport/guardian';

describe('GuardianClient', () => {
  it('constructs with endpoint', () => {
    const client = new GuardianClient({ address: '127.0.0.1', port: 7500 });
    expect(client).toBeDefined();
  });

  it('session token management', () => {
    const client = new GuardianClient({ address: '127.0.0.1', port: 7500 });
    expect(client.getSessionToken()).toBeNull();

    client.setSessionToken('test-token');
    expect(client.getSessionToken()).toBe('test-token');

    client.clearSessionToken();
    expect(client.getSessionToken()).toBeNull();
  });

  it('GuardianError has code and message', () => {
    const err = new GuardianError('TIMEOUT', 'request timed out');
    expect(err.code).toBe('TIMEOUT');
    expect(err.message).toBe('request timed out');
    expect(err.name).toBe('GuardianError');
    expect(err instanceof Error).toBe(true);
  });

  it('putSecret throws without session token', async () => {
    const client = new GuardianClient({ address: '127.0.0.1', port: 7500 });
    await expect(client.putSecret('test', new Uint8Array([1]), 1)).rejects.toThrow('No session token');
  });

  it('getSecret throws without session token', async () => {
    const client = new GuardianClient({ address: '127.0.0.1', port: 7500 });
    await expect(client.getSecret('test')).rejects.toThrow('No session token');
  });

  it('deleteSecret throws without session token', async () => {
    const client = new GuardianClient({ address: '127.0.0.1', port: 7500 });
    await expect(client.deleteSecret('test')).rejects.toThrow('No session token');
  });

  it('listSecrets throws without session token', async () => {
    const client = new GuardianClient({ address: '127.0.0.1', port: 7500 });
    await expect(client.listSecrets()).rejects.toThrow('No session token');
  });
});
