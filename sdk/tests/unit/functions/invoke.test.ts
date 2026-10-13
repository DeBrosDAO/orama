import { describe, expect, it, vi } from 'vitest';
import { FunctionsClient } from '../../../src/functions/client';
import { NetworkClient } from '../../../src/network/client';
import { HttpClient } from '../../../src/core/http';
import { SDKError } from '../../../src/errors';

function gateway(handler: (url: string) => Response) {
  const urls: string[] = [];
  const fetchImpl = vi.fn(async (url: any) => {
    urls.push(String(url));
    return handler(String(url));
  });
  const http = new HttpClient({ baseURL: 'http://localhost:10104', maxRetries: 0, fetch: fetchImpl as any });
  return { http, urls };
}

function json(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}

describe('invoking a function', () => {
  it('calls the client gateway when no functions gateway is configured', async () => {
    const { http, urls } = gateway(() => json(200, { result: 42 }));
    const functions = new FunctionsClient(http, { namespace: 'anchat' });

    await expect(functions.invoke('add', { a: 1 })).resolves.toEqual({ result: 42 });
    expect(urls[0]).toBe('http://localhost:10104/v1/invoke/anchat/add');
  });

  /**
   * `gatewayURL` was glued to the front of the path, and the path is appended
   * to the client's base URL — so the request went to
   * `http://localhost:10104https://functions.example/v1/invoke/…`, which is not
   * a URL at all. Every invoke against a separate functions gateway failed.
   */
  it('sends to the functions gateway when one is configured', async () => {
    const { http, urls } = gateway(() => json(200, { result: 7 }));
    const functions = new FunctionsClient(http, {
      namespace: 'anchat',
      gatewayURL: 'https://functions.example',
    });

    await expect(functions.invoke('add', { a: 1 })).resolves.toEqual({ result: 7 });
    expect(urls[0]).toBe('https://functions.example/v1/invoke/anchat/add');
  });

  it('does not produce a doubled URL', async () => {
    const { http, urls } = gateway(() => json(200, {}));
    const functions = new FunctionsClient(http, {
      namespace: 'ns',
      gatewayURL: 'https://functions.example',
    });

    await functions.invoke('fn', {});
    expect(urls[0]).not.toContain('localhost:10104https');
  });

  it('tolerates a trailing slash on the configured gateway', async () => {
    const { http, urls } = gateway(() => json(200, {}));
    const functions = new FunctionsClient(http, {
      namespace: 'ns',
      gatewayURL: 'https://functions.example/',
    });

    await functions.invoke('fn', {});
    expect(urls[0]).toBe('https://functions.example/v1/invoke/ns/fn');
  });

  it('passes a gateway failure through untouched', async () => {
    const { http } = gateway(() => json(500, { error: 'function crashed', code: 'FUNCTION_EXECUTION_FAILED' }));
    const functions = new FunctionsClient(http, { namespace: 'ns' });

    const error = await functions.invoke('fn', {}).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(SDKError);
    expect((error as SDKError).code).toBe('FUNCTION_EXECUTION_FAILED');
  });

  /**
   * A wrapped failure used to be built as `new SDKError(message, 500,
   * error.message)` — the third argument is the code, so every wrapped error
   * arrived with a code like "fetch failed" and no classification at all.
   */
  it('gives a wrapped failure a code, not the message', async () => {
    const http = new HttpClient({ baseURL: 'http://localhost:10104' });
    // A transport that throws something that is not an SDKError.
    vi.spyOn(http, 'post').mockRejectedValue(new Error('kaboom'));
    const functions = new FunctionsClient(http, { namespace: 'ns' });

    const error = await functions.invoke('fn', {}).catch((e: unknown) => e);
    expect((error as SDKError).code).toBe('FUNCTION_INVOKE_FAILED');
    expect((error as SDKError).message).toContain('kaboom');
    expect((error as SDKError).details).toMatchObject({ function: 'fn', namespace: 'ns' });
    vi.restoreAllMocks();
  });

  it('can be cancelled', async () => {
    const fetchImpl = vi.fn(
      (_url: any, init: any) =>
        new Promise<Response>((_resolve, reject) => {
          init.signal.addEventListener('abort', () => {
            const error = new Error('aborted');
            error.name = 'AbortError';
            reject(error);
          });
        }),
    );
    const http = new HttpClient({ baseURL: 'http://localhost:10104', fetch: fetchImpl as any });
    const functions = new FunctionsClient(http, { namespace: 'ns' });
    const controller = new AbortController();

    const inFlight = functions.invoke('fn', {}, { signal: controller.signal });
    controller.abort();

    await expect(inFlight).rejects.toMatchObject({ code: 'ABORTED' });
  });
});

describe('the anonymity proxy', () => {
  /**
   * The proxy answers 200 with an `error` field when the upstream request
   * failed, so this is where the failure surfaces. It was the one failure in
   * the SDK raised as a bare `Error`, with no `code` or `httpStatus` to branch
   * on.
   */
  it('raises a typed error when the upstream request failed', async () => {
    const { http } = gateway(() => json(200, { error: 'connection refused', status_code: 0 }));
    const network = new NetworkClient(http);

    const error = await network.proxyAnon({ url: 'https://example.onion' }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(SDKError);
    expect((error as SDKError).code).toBe('PROXY_FAILED');
    expect((error as SDKError).httpStatus).toBe(502);
    expect((error as SDKError).details.upstream_error).toBe('connection refused');
  });

  it('returns the response when the upstream request worked', async () => {
    const { http } = gateway(() => json(200, { status_code: 200, body: 'hello' }));
    const network = new NetworkClient(http);

    await expect(network.proxyAnon({ url: 'https://example.onion' })).resolves.toMatchObject({
      status_code: 200,
      body: 'hello',
    });
  });
});
