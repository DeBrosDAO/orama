import { describe, expect, it, vi } from 'vitest';
import { createWorkloadClient } from '../../src/workload';

/**
 * A deployment used to carry a key somebody had pasted into it — a namespace
 * key, so an application compromise was a namespace takeover. It reads a
 * short-lived token of its own from the file the platform staged for it.
 */

const TOKEN = 'header.workload.signature';

function gateway() {
  const seen: { headers: Record<string, string>[] } = { headers: [] };
  const fetchImpl = vi.fn(async (url: any, init: any) => {
    seen.headers.push(init?.headers ?? {});
    if (String(url).endsWith('/v1/auth/renew')) {
      return new Response(
        JSON.stringify({ access_token: 'header.renewed.signature', expires_in: 3600 }),
        { status: 200, headers: { 'content-type': 'application/json' } }
      );
    }
    return new Response(JSON.stringify({ ok: true }), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    });
  });
  return { fetchImpl, seen };
}

describe('a deployment reads the credential the platform staged for it', () => {
  it('uses the token from the file, and never a key', async () => {
    const { fetchImpl, seen } = gateway();

    const client = await createWorkloadClient({
      baseURL: 'https://ns-acme.dbrs.space',
      tokenFile: '/run/credentials/orama_token',
      readFile: async () => `${TOKEN}\n`,
      fetch: fetchImpl as any,
      maxRetries: 0,
    });
    await client.cache.get('k').catch(() => undefined);

    expect(seen.headers.some((h) => h['Authorization'] === `Bearer ${TOKEN}`)).toBe(true);
    expect(seen.headers.some((h) => 'X-API-Key' in h)).toBe(false);
  });

  it('renews with the token it is holding', async () => {
    const { fetchImpl } = gateway();

    const client = await createWorkloadClient({
      baseURL: 'https://ns-acme.dbrs.space',
      tokenFile: '/run/credentials/orama_token',
      readFile: async () => TOKEN,
      fetch: fetchImpl as any,
      maxRetries: 0,
    });

    const renewed = await client.auth.renew();
    expect(renewed.access_token).toBe('header.renewed.signature');

    const renewCall = (fetchImpl.mock.calls as any[]).find(([url]) =>
      String(url).endsWith('/v1/auth/renew')
    );
    expect(renewCall?.[1]?.headers?.['Authorization']).toBe(`Bearer ${TOKEN}`);
  });
});

describe('running outside a deployment', () => {
  it('says so rather than starting with no credential', async () => {
    await expect(
      createWorkloadClient({ baseURL: 'https://gw.example', tokenFile: '', readFile: async () => '' })
    ).rejects.toMatchObject({ code: 'WORKLOAD_NO_TOKEN' });

    await expect(
      createWorkloadClient({ baseURL: '', tokenFile: '/x', readFile: async () => 't' })
    ).rejects.toMatchObject({ code: 'WORKLOAD_NO_GATEWAY' });
  });

  it('refuses an empty credential file rather than sending nothing', async () => {
    await expect(
      createWorkloadClient({
        baseURL: 'https://gw.example',
        tokenFile: '/run/credentials/orama_token',
        readFile: async () => '   \n',
      })
    ).rejects.toMatchObject({ code: 'WORKLOAD_EMPTY_TOKEN' });
  });
});
