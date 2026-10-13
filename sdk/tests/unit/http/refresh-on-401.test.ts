import { describe, expect, it, vi } from 'vitest';
import { HttpClient } from '../../../src/core/http';
import { AuthError } from '../../../src/errors';

/**
 * `AuthClient.refresh()` existed from the first release and nothing called it.
 * The retry policy covered 408/429/5xx, so an expired session came back to the
 * application as a bare 401 and every application grew the same
 * refresh-and-retry loop around every call.
 */

/** A fetch that answers 401 until the token changes, then 200. */
function gateway(options: { validToken: string; body?: unknown } = { validToken: 'fresh' }) {
  const calls: Array<{ path: string; authorization?: string }> = [];
  const fetchImpl = vi.fn(async (url: any, init: any) => {
    const headers = (init?.headers ?? {}) as Record<string, string>;
    calls.push({ path: new URL(String(url)).pathname, authorization: headers.Authorization });

    const ok = headers.Authorization === `Bearer ${options.validToken}`;
    return new Response(JSON.stringify(ok ? (options.body ?? { ok: true }) : { error: 'token expired' }), {
      status: ok ? 200 : 401,
      headers: { 'content-type': 'application/json' },
    });
  });
  return { fetchImpl, calls };
}

function client(fetchImpl: any) {
  const http = new HttpClient({
    baseURL: 'https://gw.example',
    maxRetries: 0,
    fetch: fetchImpl,
  });
  http.setJwt('stale');
  return http;
}

describe('an expired session is renewed once and the request replayed', () => {
  it('renews, retries, and returns the result', async () => {
    const { fetchImpl, calls } = gateway();
    const http = client(fetchImpl);

    const refresh = vi.fn(async () => {
      http.setJwt('fresh');
      return 'fresh';
    });
    http.setTokenRefresher(refresh);

    await expect(http.get('/v1/db/thing')).resolves.toEqual({ ok: true });

    expect(refresh).toHaveBeenCalledTimes(1);
    expect(calls).toHaveLength(2);
    expect(calls[0].authorization).toBe('Bearer stale');
    // The replay carries the renewed token, so headers are rebuilt rather than
    // reused from the first attempt.
    expect(calls[1].authorization).toBe('Bearer fresh');
  });

  it('gives up after one renewal rather than looping', async () => {
    const { fetchImpl, calls } = gateway();
    const http = client(fetchImpl);
    // A renewal that does not actually change the token.
    http.setTokenRefresher(async () => 'still-stale');

    await expect(http.get('/v1/db/thing')).rejects.toBeInstanceOf(AuthError);
    expect(calls).toHaveLength(2);
  });

  it('raises the original 401 when there is no refresher', async () => {
    const { fetchImpl, calls } = gateway();
    const http = client(fetchImpl);

    await expect(http.get('/v1/db/thing')).rejects.toMatchObject({ httpStatus: 401 });
    expect(calls).toHaveLength(1);
  });

  it('raises the original 401 when the session cannot be renewed', async () => {
    const { fetchImpl, calls } = gateway();
    const http = client(fetchImpl);
    http.setTokenRefresher(async () => null);

    await expect(http.get('/v1/db/thing')).rejects.toMatchObject({ httpStatus: 401 });
    expect(calls).toHaveLength(1);
  });

  it('raises the original 401 when renewal itself fails', async () => {
    const { fetchImpl } = gateway();
    const http = client(fetchImpl);
    http.setTokenRefresher(async () => {
      throw new Error('refresh endpoint is down');
    });

    await expect(http.get('/v1/db/thing')).rejects.toMatchObject({ httpStatus: 401 });
  });

  /**
   * Ten calls in flight when a token expires must not fire ten renewals at the
   * gateway, each rotating the refresh token out from under the others.
   */
  it('renews once for many requests that fail together', async () => {
    const { fetchImpl } = gateway();
    const http = client(fetchImpl);

    let renewals = 0;
    http.setTokenRefresher(async () => {
      renewals += 1;
      await new Promise((resolve) => setTimeout(resolve, 5));
      http.setJwt('fresh');
      return 'fresh';
    });

    const results = await Promise.all([
      http.get('/v1/a'),
      http.get('/v1/b'),
      http.get('/v1/c'),
      http.get('/v1/d'),
    ]);

    expect(results).toHaveLength(4);
    expect(renewals).toBe(1);
  });

  it('never tries to renew a session by calling the renewal endpoint', async () => {
    const { fetchImpl, calls } = gateway();
    const http = client(fetchImpl);
    const refresh = vi.fn(async () => 'fresh');
    http.setTokenRefresher(refresh);

    await expect(http.post('/v1/auth/refresh', {})).rejects.toMatchObject({ httpStatus: 401 });

    expect(refresh).not.toHaveBeenCalled();
    expect(calls).toHaveLength(1);
  });

  it('leaves other failures alone', async () => {
    const fetchImpl = vi.fn(
      async () =>
        new Response(JSON.stringify({ error: 'nope' }), {
          status: 403,
          headers: { 'content-type': 'application/json' },
        }),
    );
    const http = client(fetchImpl);
    const refresh = vi.fn(async () => 'fresh');
    http.setTokenRefresher(refresh);

    await expect(http.get('/v1/x')).rejects.toMatchObject({ httpStatus: 403 });
    expect(refresh).not.toHaveBeenCalled();
  });
});
