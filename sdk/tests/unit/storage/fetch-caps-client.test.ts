import { describe, expect, it, vi } from 'vitest';
import { HttpClient } from '../../../src/core/http';
import { RelayCode } from '../../../src/errors';
import { StorageClient } from '../../../src/storage/client';

function storage() {
  const http = new HttpClient({ baseURL: 'https://gw.example' });
  const post = vi.spyOn(http, 'post');
  const del = vi.spyOn(http, 'delete').mockResolvedValue({ revoked: 'x' });
  return { client: new StorageClient(http), post, del };
}

describe('mintFetchCaps', () => {
  it('returns the revoke key beside each token', async () => {
    const { client, post } = storage();
    post.mockResolvedValue({
      namespace: 'anchat',
      cid: 'bafy',
      caps: [
        { id: 'a'.repeat(32), token: 't1', revoke_key: 'k1', expires_at: 1_900_000_000 },
        { id: 'b'.repeat(32), token: 't2', revoke_key: 'k2', expires_at: 1_900_000_000 },
      ],
    });
    const { caps } = await client.mintFetchCaps('bafy', { count: 2, ttlSeconds: 3600 });
    expect(caps).toEqual([
      { id: 'a'.repeat(32), token: 't1', revokeKey: 'k1', expiresAt: 1_900_000_000 },
      { id: 'b'.repeat(32), token: 't2', revokeKey: 'k2', expiresAt: 1_900_000_000 },
    ]);
  });
});

describe('revokeFetchCap', () => {
  it('sends the revoke key in X-Orama-Revoke-Key', async () => {
    const { client, del } = storage();
    await client.revokeFetchCap('a'.repeat(32), 'the-key');
    expect(del).toHaveBeenCalledWith(`/v1/storage/fetch-caps/${'a'.repeat(32)}`, {
      headers: { 'X-Orama-Revoke-Key': 'the-key' },
    });
  });

  it.each(['', undefined as unknown as string])('refuses %j without calling the gateway', async (key) => {
    const { client, del } = storage();
    await expect(client.revokeFetchCap('a'.repeat(32), key)).rejects.toMatchObject({ code: 'VALIDATION_FAILED' });
    expect(del).not.toHaveBeenCalled();
  });

  it('names the gateway code for a wrong key', () => {
    expect(RelayCode.FetchCapRevokeKeyInvalid).toBe('FETCH_CAP_REVOKE_KEY_INVALID');
  });
});
