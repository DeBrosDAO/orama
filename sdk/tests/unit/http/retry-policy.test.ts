import { afterEach, describe, expect, it, vi } from 'vitest';
import { HttpClient } from '../../../src/core/http';

/**
 * The retry was `retryDelayMs * (attempt + 1)`: no jitter, and `Retry-After`
 * ignored. A gateway that answered 429 with "come back in 30 seconds" was
 * asked again a second later, twice more, and then reported as failed — and
 * every client that failed at the same moment came back at the same moment,
 * which is how a recovering gateway is knocked over a second time.
 */

/**
 * Captures how long each retry waited, without waiting.
 *
 * The request deadline is scheduled through the same `setTimeout`, so it is
 * recognised by its length (the client's 60s timeout) and left to run its full
 * course — collapsing it would abort the request instead of retrying it.
 */
const REQUEST_TIMEOUT_MS = 60_000;

function captureDelays() {
  const waits: number[] = [];
  const real = globalThis.setTimeout;
  vi.spyOn(globalThis, 'setTimeout').mockImplementation(((fn: () => void, ms?: number) => {
    if (ms !== undefined && ms > 0 && ms < REQUEST_TIMEOUT_MS) {
      waits.push(ms);
      return real(fn, 0);
    }
    return real(fn, ms);
  }) as typeof setTimeout);
  return waits;
}

function failing(status: number, headers: Record<string, string> = {}) {
  return vi.fn(
    async () =>
      new Response(JSON.stringify({ error: 'later' }), {
        status,
        headers: { 'content-type': 'application/json', ...headers },
      }),
  );
}

function client(fetchImpl: any, maxRetries = 3) {
  return new HttpClient({
    baseURL: 'https://gw.example',
    maxRetries,
    retryDelayMs: 1000,
    timeout: 60_000,
    fetch: fetchImpl,
  });
}

describe('retry backoff', () => {
  afterEach(() => vi.restoreAllMocks());

  it('waits what the gateway asked for, in seconds', async () => {
    const waits = captureDelays();
    const fetchImpl = failing(429, { 'retry-after': '5' });

    await expect(client(fetchImpl, 1).get('/v1/x')).rejects.toMatchObject({ httpStatus: 429 });

    expect(waits).toEqual([5000]);
  });

  it('understands a Retry-After given as a date', async () => {
    const waits = captureDelays();
    const when = new Date(Date.now() + 4000).toUTCString();
    const fetchImpl = failing(503, { 'retry-after': when });

    await expect(client(fetchImpl, 1).get('/v1/x')).rejects.toMatchObject({ httpStatus: 503 });

    const backoff = waits;
    expect(backoff).toHaveLength(1);
    // Allow a little slack for the clock between building and reading it.
    expect(backoff[0]).toBeGreaterThan(3000);
    expect(backoff[0]).toBeLessThanOrEqual(4000);
  });

  it('ignores a Retry-After it cannot read', async () => {
    const waits = captureDelays();
    const fetchImpl = failing(500, { 'retry-after': 'whenever' });

    await expect(client(fetchImpl, 1).get('/v1/x')).rejects.toMatchObject({ httpStatus: 500 });

    const backoff = waits;
    expect(backoff).toHaveLength(1);
    // Falls back to the computed delay: 1000ms plus up to 25% jitter.
    expect(backoff[0]).toBeGreaterThanOrEqual(1000);
    expect(backoff[0]).toBeLessThanOrEqual(1250);
  });

  it('caps a gateway that asks for an unreasonable wait', async () => {
    const waits = captureDelays();
    const fetchImpl = failing(429, { 'retry-after': '3600' });

    await expect(client(fetchImpl, 1).get('/v1/x')).rejects.toMatchObject({ httpStatus: 429 });

    expect(waits).toEqual([30_000]);
  });

  it('grows the delay with each attempt and keeps it within the jitter band', async () => {
    const waits = captureDelays();
    const fetchImpl = failing(503);

    await expect(client(fetchImpl, 3).get('/v1/x')).rejects.toMatchObject({ httpStatus: 503 });

    const backoff = waits;
    expect(backoff).toHaveLength(3);
    expect(backoff[0]).toBeGreaterThanOrEqual(1000);
    expect(backoff[0]).toBeLessThanOrEqual(1250);
    expect(backoff[1]).toBeGreaterThanOrEqual(2000);
    expect(backoff[1]).toBeLessThanOrEqual(2500);
    expect(backoff[2]).toBeGreaterThanOrEqual(3000);
    expect(backoff[2]).toBeLessThanOrEqual(3750);
  });

  /**
   * Jitter is the point: two clients that failed together must not return
   * together. Over enough draws the delays cannot all be identical.
   */
  it('does not send every client back at the same instant', async () => {
    const seen = new Set<number>();

    for (let i = 0; i < 12; i++) {
      const waits = captureDelays();
      await expect(client(failing(500), 1).get('/v1/x')).rejects.toBeDefined();
      seen.add(waits[0]);
      vi.restoreAllMocks();
    }

    expect(seen.size).toBeGreaterThan(1);
  });

  it('does not retry a status that will not change', async () => {
    const fetchImpl = failing(400);
    await expect(client(fetchImpl, 3).get('/v1/x')).rejects.toMatchObject({ httpStatus: 400 });
    expect(fetchImpl).toHaveBeenCalledTimes(1);
  });

  it('retries the statuses that can change', async () => {
    for (const status of [408, 429, 500, 502, 503, 504]) {
      const waits = captureDelays();
      const fetchImpl = failing(status);
      await expect(client(fetchImpl, 1).get('/v1/x')).rejects.toMatchObject({ httpStatus: status });
      expect(fetchImpl, `status ${status} was not retried`).toHaveBeenCalledTimes(2);
      expect(waits.length).toBeGreaterThan(0);
      vi.restoreAllMocks();
    }
  });
});
