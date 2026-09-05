import { describe, expect, it, vi } from 'vitest';
import { AuthClient } from '../../../src/auth/client';
import { MemoryStorage } from '../../../src/auth/types';
import { HttpClient } from '../../../src/core/http';

/** A gateway stub that answers each path from a table. */
function gateway(routes: Record<string, () => Response>) {
  const calls: string[] = [];
  const fetchImpl = vi.fn(async (url: any) => {
    const path = new URL(String(url)).pathname;
    calls.push(path);
    // A client holding a key exchanges it for a token before its first
    // request, so every stub has to answer that. It is the only request the
    // SDK makes that the caller did not ask for.
    if (path === '/v1/auth/token' && !routes[path]) {
      return json(200, { access_token: 'header.exchanged.signature', expires_in: 900 });
    }
    const route = routes[path];
    if (!route) {
      return new Response(JSON.stringify({ error: 'no route' }), {
        status: 404,
        headers: { 'content-type': 'application/json' },
      });
    }
    return route();
  });
  return { fetchImpl, calls };
}

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

function build(fetchImpl: any, storage = new MemoryStorage()) {
  const http = new HttpClient({
    baseURL: 'https://gw.example',
    maxRetries: 0,
    fetch: fetchImpl,
  });
  const auth = new AuthClient({ httpClient: http, storage, apiKey: 'ak_test:ns' });
  return { http, auth, storage };
}

describe('whoami', () => {
  /**
   * It used to catch everything and answer "not authenticated", so a gateway
   * that was down was indistinguishable from a rejected key. Applications
   * showed users a login screen for a network outage.
   */
  it('answers "not authenticated" when the credential is rejected', async () => {
    const { fetchImpl } = gateway({
      '/v1/auth/whoami': () => json(401, { error: 'invalid api key' }),
    });
    const { auth } = build(fetchImpl);

    await expect(auth.whoami()).resolves.toEqual({ authenticated: false });
  });

  it('raises a gateway failure instead of calling it a logged-out user', async () => {
    const { fetchImpl } = gateway({
      '/v1/auth/whoami': () => json(500, { error: 'database unavailable' }),
    });
    const { auth } = build(fetchImpl);

    await expect(auth.whoami()).rejects.toMatchObject({ httpStatus: 500 });
  });

  it('raises an unreachable gateway instead of calling it a logged-out user', async () => {
    const fetchImpl = vi.fn(async () => {
      throw new TypeError('fetch failed');
    });
    const { auth } = build(fetchImpl);

    await expect(auth.whoami()).rejects.toMatchObject({ code: 'NETWORK_ERROR' });
  });

  it('returns what the gateway said when the credential is good', async () => {
    const { fetchImpl } = gateway({
      '/v1/auth/whoami': () =>
        json(200, {
          authenticated: true,
          method: 'jwt',
          subject: '0xWALLET',
          namespace: 'anchat',
        }),
    });
    const { auth } = build(fetchImpl);

    await expect(auth.whoami()).resolves.toMatchObject({
      authenticated: true,
      method: 'jwt',
      subject: '0xWALLET',
    });
  });
});

describe('logout', () => {
  function logoutGateway() {
    return gateway({
      '/v1/auth/logout': () => json(200, { ok: true }),
    });
  }

  it('tells the gateway and clears everything by default', async () => {
    const { fetchImpl, calls } = logoutGateway();
    const { http, auth, storage } = build(fetchImpl);
    auth.setJwt('a.jwt');

    await auth.logout();

    expect(calls).toContain('/v1/auth/logout');
    expect(http.getToken()).toBeUndefined();
    expect(await storage.get('apiKey')).toBeNull();
  });

  it('keeps the API key when asked, and the next request carries a token from it', async () => {
    const { fetchImpl } = logoutGateway();
    const { http, auth } = build(fetchImpl);
    auth.setJwt('a.jwt');

    await auth.logout({ keepApiKey: true });

    // The key is still the client's credential; what goes on the wire is a
    // token exchanged from it, which is why getToken() is empty until the next
    // request rather than holding the key itself.
    expect(http.getApiKey()).toBe('ak_test:ns');
    expect(http.getToken()).toBeUndefined();

    // The route itself does not matter; what matters is that the exchange
    // happened and the token is what the client now holds.
    await http.request('GET', '/v1/auth/whoami', {}).catch(() => undefined);
    expect(http.getToken()).toBe('header.exchanged.signature');
  });

  it('can clear local state without telling the gateway', async () => {
    const { fetchImpl, calls } = logoutGateway();
    const { http, auth } = build(fetchImpl);
    auth.setJwt('a.jwt');

    await auth.logout({ server: false });

    expect(calls).not.toContain('/v1/auth/logout');
    expect(http.getToken()).toBeUndefined();
  });

  it('does not call the gateway when there is no session to end', async () => {
    const { fetchImpl, calls } = logoutGateway();
    const { auth } = build(fetchImpl);

    await auth.logout();

    expect(calls).not.toContain('/v1/auth/logout');
  });

  it('completes the local cleanup even when the gateway refuses', async () => {
    const { fetchImpl } = gateway({
      '/v1/auth/logout': () => json(503, { error: 'unavailable' }),
    });
    const { http, auth } = build(fetchImpl);
    auth.setJwt('a.jwt');

    await expect(auth.logout()).resolves.toBeUndefined();
    expect(http.getToken()).toBeUndefined();
  });

  // The three old methods still work, so nothing that calls them breaks.
  it('logoutUser keeps the API key, as it always did', async () => {
    const { fetchImpl } = logoutGateway();
    const { http, auth } = build(fetchImpl);
    auth.setJwt('a.jwt');

    await auth.logoutUser();

    expect(http.getApiKey()).toBe('ak_test:ns');
  });

  it('clear resets locally without a gateway call, as it always did', async () => {
    const { fetchImpl, calls } = logoutGateway();
    const { http, auth } = build(fetchImpl);
    auth.setJwt('a.jwt');

    await auth.clear();

    expect(calls).not.toContain('/v1/auth/logout');
    expect(http.getToken()).toBeUndefined();
  });
});

describe('the client renews its own session', () => {
  /**
   * `AuthClient` installs the renewal hook on the HTTP client, so an expired
   * JWT is renewed and the request replayed without the application seeing the
   * 401. Nothing called `refresh()` before this.
   */
  it('renews on a 401 and replays the request', async () => {
    const storage = new MemoryStorage();
    await storage.set('refreshToken', 'rt_1');
    await storage.set('namespace', 'anchat');

    let jwt = 'stale';
    const fetchImpl = vi.fn(async (url: any, init: any) => {
      const path = new URL(String(url)).pathname;
      if (path === '/v1/auth/refresh') {
        jwt = 'fresh';
        return json(200, { access_token: 'fresh', refresh_token: 'rt_2' });
      }
      const auth = (init?.headers ?? {}).Authorization;
      return auth === `Bearer ${jwt}` && jwt === 'fresh'
        ? json(200, { rows: [] })
        : json(401, { error: 'expired' });
    });

    const http = new HttpClient({ baseURL: 'https://gw.example', maxRetries: 0, fetch: fetchImpl as any });
    const auth = new AuthClient({ httpClient: http, storage, jwt: 'stale' });
    expect(auth).toBeDefined();

    await expect(http.post('/v1/rqlite/query', { sql: 'SELECT 1' })).resolves.toEqual({ rows: [] });
    // The rotated refresh token was stored.
    expect(await storage.get('refreshToken')).toBe('rt_2');
  });

  it('does not try to renew when there is no refresh token', async () => {
    const fetchImpl = vi.fn(async () => json(401, { error: 'expired' }));
    const http = new HttpClient({ baseURL: 'https://gw.example', maxRetries: 0, fetch: fetchImpl as any });
    const auth = new AuthClient({ httpClient: http, storage: new MemoryStorage(), jwt: 'stale' });
    expect(auth).toBeDefined();

    await expect(http.get('/v1/thing')).rejects.toMatchObject({ httpStatus: 401 });
    // One attempt: no refresh call, no replay.
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });
});
