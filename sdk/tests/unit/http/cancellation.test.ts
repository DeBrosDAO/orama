import { describe, expect, it, vi } from 'vitest';
import { HttpClient } from '../../../src/core/http';
import { NetworkError } from '../../../src/errors';

/**
 * `AbortSignal` reached only `uploadFile` (bugboard #144). Every other request
 * ran to completion whatever the application did, so a screen that navigated
 * away, a search box that typed another character, or a user pressing Cancel
 * left the work in flight and its result arriving into nothing.
 */

/** A fetch that never answers until its signal fires. */
function hangingFetch() {
  const started: AbortSignal[] = [];
  const fetchImpl = vi.fn(
    (_url: any, init: any) =>
      new Promise<Response>((_resolve, reject) => {
        const signal: AbortSignal = init.signal;
        started.push(signal);
        signal.addEventListener('abort', () => {
          const error = new Error('The operation was aborted.');
          error.name = 'AbortError';
          reject(error);
        });
      }),
  );
  return { fetchImpl, started };
}

function client(fetchImpl: any, onNetworkError?: any) {
  return new HttpClient({
    baseURL: 'https://gw.example',
    maxRetries: 0,
    timeout: 60_000,
    fetch: fetchImpl,
    onNetworkError,
  });
}

describe('a caller can cancel any request', () => {
  it.each(['get', 'delete'] as const)('%s honours the signal', async (verb) => {
    const { fetchImpl } = hangingFetch();
    const http = client(fetchImpl);
    const controller = new AbortController();

    const inFlight = http[verb]('/v1/thing', { signal: controller.signal });
    controller.abort();

    await expect(inFlight).rejects.toMatchObject({ code: 'ABORTED' });
  });

  it.each(['post', 'put'] as const)('%s honours the signal', async (verb) => {
    const { fetchImpl } = hangingFetch();
    const http = client(fetchImpl);
    const controller = new AbortController();

    const inFlight = http[verb]('/v1/thing', { some: 'body' }, { signal: controller.signal });
    controller.abort();

    await expect(inFlight).rejects.toMatchObject({ code: 'ABORTED' });
  });

  it('rejects immediately when the signal has already fired', async () => {
    const { fetchImpl } = hangingFetch();
    const http = client(fetchImpl);
    const controller = new AbortController();
    controller.abort();

    await expect(http.get('/v1/thing', { signal: controller.signal })).rejects.toMatchObject({
      code: 'ABORTED',
    });
  });

  it('raises a NetworkError, so a cancel is catchable like any other failure', async () => {
    const { fetchImpl } = hangingFetch();
    const http = client(fetchImpl);
    const controller = new AbortController();

    const inFlight = http.get('/v1/thing', { signal: controller.signal });
    controller.abort();

    const error = await inFlight.catch((e: unknown) => e);
    expect(error).toBeInstanceOf(NetworkError);
    expect((error as NetworkError).httpStatus).toBe(0);
  });

  /**
   * A cancel is a user action, not a failure of the network. Reporting it
   * through `onNetworkError` would make an application fail over to another
   * gateway every time someone pressed Cancel.
   */
  it('does not report a cancel as a network error', async () => {
    const { fetchImpl } = hangingFetch();
    const onNetworkError = vi.fn();
    const http = client(fetchImpl, onNetworkError);
    const controller = new AbortController();

    const inFlight = http.get('/v1/thing', { signal: controller.signal });
    controller.abort();
    await inFlight.catch(() => undefined);

    expect(onNetworkError).not.toHaveBeenCalled();
  });

  it('still reports a real failure through onNetworkError', async () => {
    const fetchImpl = vi.fn(async () => {
      throw new TypeError('fetch failed');
    });
    const onNetworkError = vi.fn();
    const http = client(fetchImpl, onNetworkError);

    await expect(http.get('/v1/thing')).rejects.toMatchObject({ code: 'NETWORK_ERROR' });
    expect(onNetworkError).toHaveBeenCalledTimes(1);
  });

  it('tells a timeout apart from a cancel', async () => {
    const { fetchImpl } = hangingFetch();
    const http = new HttpClient({
      baseURL: 'https://gw.example',
      maxRetries: 0,
      timeout: 5,
      fetch: fetchImpl,
    });

    await expect(http.get('/v1/thing')).rejects.toMatchObject({ code: 'TIMEOUT' });
  });

  it('passes the signal down to the actual fetch', async () => {
    const { fetchImpl, started } = hangingFetch();
    const http = client(fetchImpl);
    const controller = new AbortController();

    const inFlight = http.get('/v1/thing', { signal: controller.signal });
    await Promise.resolve();
    expect(started).toHaveLength(1);
    expect(started[0].aborted).toBe(false);

    controller.abort();
    await inFlight.catch(() => undefined);
    expect(started[0].aborted).toBe(true);
  });

  it('does not leave a timer holding the event loop after a request settles', async () => {
    const cleared: unknown[] = [];
    const realClear = globalThis.clearTimeout;
    globalThis.clearTimeout = ((handle: never) => {
      cleared.push(handle);
      return realClear(handle);
    }) as typeof clearTimeout;

    try {
      const fetchImpl = vi.fn(
        async () =>
          new Response(JSON.stringify({ ok: true }), {
            status: 200,
            headers: { 'content-type': 'application/json' },
          }),
      );
      await client(fetchImpl).get('/v1/thing');
    } finally {
      globalThis.clearTimeout = realClear;
    }

    expect(cleared.length).toBeGreaterThan(0);
  });
});

describe('binary downloads', () => {
  it('can be cancelled too', async () => {
    const { fetchImpl } = hangingFetch();
    const http = client(fetchImpl);
    const controller = new AbortController();

    const inFlight = http.getBinary('/v1/storage/get/bafy', { signal: controller.signal });
    controller.abort();

    await expect(inFlight).rejects.toMatchObject({ code: 'ABORTED' });
  });

  /**
   * The deadline used to stay armed after the response headers arrived, so a
   * download still streaming was aborted mid-body once it passed five times the
   * request timeout — however healthy the transfer was.
   */
  it('stops the deadline once the response has arrived', async () => {
    let capturedSignal: AbortSignal | undefined;
    const fetchImpl = vi.fn(async (_url: any, init: any) => {
      capturedSignal = init.signal;
      return new Response('body', { status: 200 });
    });

    const http = new HttpClient({
      baseURL: 'https://gw.example',
      timeout: 4,
      fetch: fetchImpl as any,
    });

    const response = await http.getBinary('/v1/storage/get/bafy');
    expect(response.status).toBe(200);

    // Well past 5 x 4ms; the body must still be readable.
    await new Promise((resolve) => setTimeout(resolve, 40));
    expect(capturedSignal?.aborted).toBe(false);
    expect(await response.text()).toBe('body');
  });
});
