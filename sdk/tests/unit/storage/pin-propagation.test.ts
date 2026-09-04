import { afterEach, describe, expect, it, vi } from 'vitest';
import { HttpClient } from '../../../src/core/http';
import { SDKError } from '../../../src/errors';
import { StorageClient } from '../../../src/storage/client';

/**
 * `get` and `getBinary` each carried a character-for-character copy of the same
 * eight-attempt retry loop, down to a comment that added its own backoff wrong
 * (it claimed 21 seconds for a schedule that waits 18). They now share one
 * private helper, so the propagation window is stated once.
 */

/** A StorageClient whose transport is a stub, with the calls it received. */
function storageWith(responses: Array<Response | Error>) {
  const calls: string[] = [];
  const http = new HttpClient({ baseURL: 'https://gw.example' });
  vi.spyOn(http, 'getBinary').mockImplementation(async (path: string) => {
    calls.push(path);
    const next = responses[calls.length - 1] ?? responses[responses.length - 1];
    if (next instanceof Error) throw next;
    return next;
  });
  return { storage: new StorageClient(http), calls };
}

function notFound() {
  return new SDKError('not found', 404, 'HTTP_404');
}

function body(text: string) {
  return new Response(text, { status: 200 });
}

/** Runs `promise` with every pending timer fired, so backoff costs no real time. */
async function withoutWaiting<T>(run: () => Promise<T>): Promise<T> {
  vi.useFakeTimers();
  try {
    const promise = run();
    // Settle the microtask queue and then release each scheduled backoff.
    const drain = async () => {
      for (let i = 0; i < 32; i++) {
        await vi.advanceTimersByTimeAsync(3000);
      }
    };
    const [result] = await Promise.all([promise, drain()]);
    return result;
  } finally {
    vi.useRealTimers();
  }
}

describe('StorageClient retries only while the pin is still propagating', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('returns the first response when the cluster already has the CID', async () => {
    const { storage, calls } = storageWith([body('hello')]);

    const response = await storage.getBinary('bafyone');

    expect(await response.text()).toBe('hello');
    expect(calls).toEqual(['/v1/storage/get/bafyone']);
  });

  it('re-asks after a 404 and returns the content once it lands', async () => {
    const { storage, calls } = storageWith([notFound(), notFound(), body('late')]);

    const response = await withoutWaiting(() => storage.getBinary('bafylate'));

    expect(await response.text()).toBe('late');
    expect(calls).toHaveLength(3);
  });

  it('gives up after eight attempts and rethrows the last 404', async () => {
    const { storage, calls } = storageWith([notFound()]);

    await expect(withoutWaiting(() => storage.getBinary('bafygone'))).rejects.toMatchObject({
      httpStatus: 404,
    });
    expect(calls).toHaveLength(8);
  });

  it('does not retry a failure that is not a 404', async () => {
    const { storage, calls } = storageWith([new SDKError('gateway down', 503, 'HTTP_503')]);

    await expect(storage.getBinary('bafyerr')).rejects.toMatchObject({ httpStatus: 503 });
    expect(calls).toHaveLength(1);
  });

  it('does not retry a network failure that never reached the gateway', async () => {
    const { storage, calls } = storageWith([new SDKError('connection refused', 0, 'NETWORK_ERROR')]);

    await expect(storage.getBinary('bafynet')).rejects.toMatchObject({ code: 'NETWORK_ERROR' });
    expect(calls).toHaveLength(1);
  });

  it('treats a transport that reports 404 only in text as not-found', async () => {
    const { storage, calls } = storageWith([new Error('404 not found'), body('ok')]);

    const response = await withoutWaiting(() => storage.getBinary('bafytext'));

    expect(await response.text()).toBe('ok');
    expect(calls).toHaveLength(2);
  });

  it('get() returns the body stream through the same retry path', async () => {
    const { storage, calls } = storageWith([notFound(), body('streamed')]);

    const stream = await withoutWaiting(() => storage.get('bafystream'));

    expect(await new Response(stream).text()).toBe('streamed');
    expect(calls).toHaveLength(2);
  });

  it('get() reports an empty body as a typed error rather than a bare Error', async () => {
    const { storage } = storageWith([new Response(null, { status: 204 })]);

    await expect(storage.get('bafyempty')).rejects.toBeInstanceOf(SDKError);
    await expect(storage.get('bafyempty')).rejects.toMatchObject({ code: 'EMPTY_BODY' });
  });

  it('waits 1s, 2s, then 3s between attempts, capped', async () => {
    const { storage } = storageWith([notFound()]);
    const waits: number[] = [];
    vi.useFakeTimers();
    const timeout = vi
      .spyOn(globalThis, 'setTimeout')
      .mockImplementation(((fn: () => void, ms?: number) => {
        waits.push(ms ?? 0);
        fn();
        return 0 as unknown as ReturnType<typeof setTimeout>;
      }) as typeof setTimeout);

    try {
      await expect(storage.getBinary('bafyslow')).rejects.toMatchObject({ httpStatus: 404 });
    } finally {
      timeout.mockRestore();
      vi.useRealTimers();
    }

    // Seven waits for eight attempts: the last failure is returned, not slept on.
    expect(waits).toEqual([1000, 2000, 3000, 3000, 3000, 3000, 3000]);
  });
});
