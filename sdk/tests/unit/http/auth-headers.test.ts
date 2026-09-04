import { describe, expect, it, vi } from 'vitest';
import { HttpClient } from '../../../src/core/http';

/**
 * The complete credential matrix, asserted against the client that actually
 * sends the requests.
 *
 * These rules used to exist twice: once in `HttpClient.getAuthHeaders` and once
 * in a `PathBasedAuthStrategy` class that nothing constructed and that never
 * reached the published bundle. The tests were written against the copy that
 * did not run. The copy is gone; the assertions moved here, where a change to
 * the live rules breaks them.
 */

const KEY = 'ak_runtime:anchat-test';
const JWT = 'eyJhbGciOi.wallet.jwt';

function client(apiKey?: string, jwt?: string) {
  const seen: { headers: Record<string, string> } = { headers: {} };
  const fetchImpl = vi.fn(async (_url: any, options: any) => {
    seen.headers = options?.headers ?? {};
    return new Response(JSON.stringify({ ok: true }), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    });
  });
  const http = new HttpClient({
    baseURL: 'https://gw.example',
    maxRetries: 0,
    fetch: fetchImpl as any,
  });
  if (apiKey) http.setApiKey(apiKey);
  if (jwt) http.setJwt(jwt);
  return { http, seen };
}

async function headersFor(path: string, apiKey?: string, jwt?: string) {
  const { http, seen } = client(apiKey, jwt);
  await http.post(path, {});
  return seen.headers;
}

describe('HttpClient credential selection by path', () => {
  // Namespace data planes are authorised by the API key alone. A user JWT
  // riding along would add a user context the gateway then has to reconcile
  // with namespace-level authorisation.
  it.each(['/v1/rqlite/query', '/v1/pubsub/publish', '/v1/cache/get'])(
    'sends only the API key on %s',
    async (path) => {
      const headers = await headersFor(path, KEY, JWT);
      expect(headers['X-API-Key']).toBe(KEY);
      expect(headers['Authorization']).toBeUndefined();
    },
  );

  // Without an API key those same paths have nothing else to offer, so the JWT
  // goes instead of nothing.
  it.each(['/v1/rqlite/query', '/v1/pubsub/publish', '/v1/cache/get'])(
    'falls back to the JWT on %s when no API key is set',
    async (path) => {
      const headers = await headersFor(path, undefined, JWT);
      expect(headers['X-API-Key']).toBeUndefined();
      expect(headers['Authorization']).toBe(`Bearer ${JWT}`);
    },
  );

  // Bugboard #148/#149: the gateway enforces a per-user wallet JWT on proxy.
  // An API key alone is rejected 401 "requires a logged-in user".
  it.each(['/v1/proxy/anon', '/v1/storage/upload', '/v1/push/devices', '/v1/webrtc/signal'])(
    'sends both credentials on %s',
    async (path) => {
      const headers = await headersFor(path, KEY, JWT);
      expect(headers['X-API-Key']).toBe(KEY);
      expect(headers['Authorization']).toBe(`Bearer ${JWT}`);
    },
  );

  it('sends both credentials on auth endpoints', async () => {
    const headers = await headersFor('/v1/auth/whoami', KEY, JWT);
    expect(headers['X-API-Key']).toBe(KEY);
    expect(headers['Authorization']).toBe(`Bearer ${JWT}`);
  });

  it('degrades to the API key alone when no JWT has been set', async () => {
    const headers = await headersFor('/v1/proxy/anon', KEY, undefined);
    expect(headers['X-API-Key']).toBe(KEY);
    expect(headers['Authorization']).toBeUndefined();
  });

  it('sends no credential header when neither is set', async () => {
    const headers = await headersFor('/v1/network/status');
    expect(headers['X-API-Key']).toBeUndefined();
    expect(headers['Authorization']).toBeUndefined();
  });

  it('keeps both credentials when each is set in turn', async () => {
    const { http, seen } = client();
    http.setApiKey(KEY);
    http.setJwt(JWT);
    await http.post('/v1/storage/upload', {});
    expect(seen.headers['X-API-Key']).toBe(KEY);
    expect(seen.headers['Authorization']).toBe(`Bearer ${JWT}`);
    expect(http.getApiKey()).toBe(KEY);
    // getToken prefers the JWT: it is the more specific identity.
    expect(http.getToken()).toBe(JWT);
  });
});
