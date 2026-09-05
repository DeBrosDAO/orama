import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { HttpClient } from '../../../src/core/http';

/**
 * A library that prints on every request is unusable in a server process: one
 * console line per HTTP call, per WebSocket event, and per pubsub message, with
 * no switch. The SDK now prints only when the caller asked for it with
 * `debug: true`.
 */

function okFetch() {
  return vi.fn(
    async () =>
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
  );
}

function failingFetch(status: number) {
  return vi.fn(
    async () =>
      new Response(JSON.stringify({ error: 'nope' }), {
        status,
        headers: { 'content-type': 'application/json' },
      }),
  );
}

describe('HttpClient debug logging', () => {
  let log: ReturnType<typeof vi.spyOn>;
  let warn: ReturnType<typeof vi.spyOn>;
  let error: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    log = vi.spyOn(console, 'log').mockImplementation(() => {});
    warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    error = vi.spyOn(console, 'error').mockImplementation(() => {});
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('says nothing on a successful request by default', async () => {
    const client = new HttpClient({ baseURL: 'https://gw.example', fetch: okFetch() as any });

    await client.post('/v1/rqlite/query', { sql: 'SELECT 1' });

    expect(log).not.toHaveBeenCalled();
    expect(warn).not.toHaveBeenCalled();
    expect(error).not.toHaveBeenCalled();
  });

  it('says nothing on a failed request by default — the error is thrown, not printed', async () => {
    const client = new HttpClient({
      baseURL: 'https://gw.example',
      maxRetries: 0,
      fetch: failingFetch(500) as any,
    });

    await expect(client.get('/v1/network/status')).rejects.toThrow();

    expect(log).not.toHaveBeenCalled();
    expect(error).not.toHaveBeenCalled();
  });

  it('says nothing when a token is set — it used to announce every JWT change', async () => {
    const client = new HttpClient({ baseURL: 'https://gw.example', fetch: okFetch() as any });

    client.setJwt('eyJhbGciOi.wallet.jwt');

    expect(log).not.toHaveBeenCalled();
  });

  it('reports each request once when debug is on', async () => {
    const client = new HttpClient({
      baseURL: 'https://gw.example',
      debug: true,
      fetch: okFetch() as any,
    });

    await client.get('/v1/network/status');

    expect(log).toHaveBeenCalledTimes(1);
    expect(String(log.mock.calls[0][0])).toMatch(
      /^\[HttpClient\] GET \/v1\/network\/status completed in /,
    );
  });

  it('includes the SQL and its arguments only under debug', async () => {
    const quiet = new HttpClient({ baseURL: 'https://gw.example', fetch: okFetch() as any });
    await quiet.post('/v1/rqlite/query', { sql: 'SELECT * FROM t WHERE id = ?', args: ['x'] });
    expect(log).not.toHaveBeenCalled();

    const loud = new HttpClient({
      baseURL: 'https://gw.example',
      debug: true,
      fetch: okFetch() as any,
    });
    await loud.post('/v1/rqlite/query', { sql: 'SELECT * FROM t WHERE id = ?', args: ['x'] });

    const printed = log.mock.calls.map((call) => String(call[0])).join('\n');
    expect(printed).toContain('SQL: SELECT * FROM t WHERE id = ?');
    expect(printed).toContain('Args: ["x"]');
  });

  it('describes a table query under debug', async () => {
    const client = new HttpClient({
      baseURL: 'https://gw.example',
      debug: true,
      fetch: okFetch() as any,
    });

    await client.post('/v1/rqlite/find', { table: 'messages', criteria: { room: 'a' }, limit: 10 });

    const printed = log.mock.calls.map((call) => String(call[0])).join('\n');
    expect(printed).toContain('Table: messages');
    expect(printed).toContain('Criteria: {"room":"a"}');
    expect(printed).toContain('Limit: 10');
  });

  it('does not describe a non-rqlite request', async () => {
    const client = new HttpClient({
      baseURL: 'https://gw.example',
      debug: true,
      fetch: okFetch() as any,
    });

    await client.post('/v1/storage/pin', { cid: 'bafy', sql: 'not a query' });

    const printed = log.mock.calls.map((call) => String(call[0])).join('\n');
    expect(printed).not.toContain('SQL:');
  });

  it('reports a find-one 404 as a warning, not an error — it is an expected answer', async () => {
    const client = new HttpClient({
      baseURL: 'https://gw.example',
      debug: true,
      maxRetries: 0,
      fetch: failingFetch(404) as any,
    });

    await expect(client.post('/v1/rqlite/find-one', { table: 't' })).rejects.toThrow();

    expect(error).not.toHaveBeenCalled();
    expect(String(warn.mock.calls[0][0])).toContain('expected for optional lookups');
  });

  it('reports any other failure as an error under debug', async () => {
    const client = new HttpClient({
      baseURL: 'https://gw.example',
      debug: true,
      maxRetries: 0,
      fetch: failingFetch(500) as any,
    });

    await expect(client.get('/v1/network/status')).rejects.toThrow();

    expect(String(error.mock.calls[0][0])).toContain('GET /v1/network/status failed after');
  });
});
