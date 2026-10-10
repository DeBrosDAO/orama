import { readFileSync } from 'node:fs';
import { createServer as createHttpServer, type IncomingHttpHeaders, type Server as HttpServer } from 'node:http';
import { createServer as createHttpsServer, type Server as HttpsServer } from 'node:https';
import type { AddressInfo } from 'node:net';
import { connect as netConnect } from 'node:net';
import { join } from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { WebSocketServer } from 'ws';
import { HttpClient } from '../../../src/core/http';
import {
  FetchCapError,
  NetworkError,
  NotFoundError,
  RelayCode,
  RelayError,
} from '../../../src/errors';
import { StorageClient } from '../../../src/storage/client';
import type { FetchCapability } from '../../../src/storage/fetch-transport';
import { pickRelay, RelayedFetch } from '../../../src/storage/relay-transport';

const NS_HOST = 'ns-test.relay-test.local';
const CID = 'bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi';
const CAP: FetchCapability = { id: 'cap-1', token: 'tok.en-1', revokeKey: 'rk-1', expiresAt: 2_000_000_000 };
const fixture = (name: string) => readFileSync(join(__dirname, 'fixtures', name));

interface Seen {
  relayRequests: Array<{ url: string; headers: IncomingHttpHeaders }>;
  storageRequests: Array<{ line: string; headers: IncomingHttpHeaders }>;
}

let storage: HttpsServer;
let relayHttp: HttpServer;
let wss: WebSocketServer;
let relayUrl: string;
let seen: Seen;
/** What the storage node answers; swapped per test. */
let respond: (path: string, headers: IncomingHttpHeaders, res: import('node:http').ServerResponse) => void;
/** When set, the relay refuses the upgrade with this status before upgrading. */
let refuse: { status: number; headers?: Record<string, string>; body?: string } | undefined;

beforeEach(async () => {
  seen = { relayRequests: [], storageRequests: [] };
  refuse = undefined;
  respond = (_p, _h, res) => {
    res.setHeader('content-length', '5');
    res.end('hello');
  };

  storage = createHttpsServer({ key: fixture('ns.key'), cert: fixture('ns.crt') }, (req, res) => {
    seen.storageRequests.push({ line: `${req.method} ${req.url}`, headers: req.headers });
    respond(req.url ?? '', req.headers, res);
  });
  await new Promise<void>((r) => storage.listen(0, '127.0.0.1', r));
  const storagePort = (storage.address() as AddressInfo).port;

  // The fake relay pipes each WebSocket to the local TLS server, as the real
  // one pipes it to <host>:443 through Tor: bytes only, no TLS of its own.
  relayHttp = createHttpServer();
  wss = new WebSocketServer({ noServer: true });
  relayHttp.on('upgrade', (req, socket, head) => {
    seen.relayRequests.push({ url: req.url ?? '', headers: req.headers });
    if (refuse) {
      const body = refuse.body ?? '';
      const extra = Object.entries(refuse.headers ?? {}).map(([k, v]) => `${k}: ${v}\r\n`).join('');
      socket.end(`HTTP/1.1 ${refuse.status} X\r\nContent-Type: application/json\r\n${extra}Content-Length: ${Buffer.byteLength(body)}\r\nConnection: close\r\n\r\n${body}`);
      return;
    }
    wss.handleUpgrade(req, socket, head, (ws) => {
      const upstream = netConnect(storagePort, '127.0.0.1');
      upstream.on('data', (d) => ws.send(d, { binary: true }));
      upstream.on('close', () => ws.close());
      upstream.on('error', () => ws.terminate());
      ws.on('message', (d) => upstream.write(d as Buffer));
      ws.on('close', () => upstream.destroy());
    });
  });
  await new Promise<void>((r) => relayHttp.listen(0, '127.0.0.1', r));
  relayUrl = `http://127.0.0.1:${(relayHttp.address() as AddressInfo).port}`;
});

afterEach(async () => {
  vi.restoreAllMocks();
  wss.close();
  relayHttp.closeAllConnections();
  storage.closeAllConnections();
  await Promise.all([
    new Promise((r) => relayHttp.close(r)),
    new Promise((r) => storage.close(r)),
  ]);
});

const transport = (extra: Partial<ConstructorParameters<typeof RelayedFetch>[0]> = {}) =>
  new RelayedFetch({ relays: [relayUrl], namespaceHost: NS_HOST, ca: fixture('ns.crt'), ...extra });

describe('RelayedFetch through a fake relay (TLS over the WebSocket)', () => {
  it('downloads the body with TLS verified against the namespace host', async () => {
    const body = await transport().fetch(CID, CAP);
    expect(Buffer.from(body).toString()).toBe('hello');
  });

  it('writes exactly the documented request and no credential at the relay', async () => {
    await transport().fetch(CID, CAP);

    expect(seen.storageRequests).toHaveLength(1);
    expect(seen.storageRequests[0].line).toBe(`GET /v1/storage/relayed/${CID}`);
    expect(seen.storageRequests[0].headers).toEqual({
      host: NS_HOST,
      'x-orama-fetch-cap': CAP.token,
      connection: 'close',
    });

    const relayReq = seen.relayRequests[0];
    expect(relayReq.url).toBe(`/v1/proxy/relay?host=${NS_HOST}&port=443`);
    for (const h of ['authorization', 'cookie', 'x-api-key', 'x-orama-fetch-cap']) {
      expect(relayReq.headers[h]).toBeUndefined();
    }
    // The relay must never learn the CID.
    expect(JSON.stringify(relayReq)).not.toContain(CID);
  });

  it('asks the relay for a reusable circuit only when told to', async () => {
    await transport({ circuit: 'session' }).fetch(CID, CAP);
    expect(seen.relayRequests[0].url).toContain('&circuit=session');
  });

  it('decodes a chunked response', async () => {
    respond = (_p, _h, res) => {
      res.write('he');
      res.write('llo ');
      res.end('chunked');
    };
    const body = await transport().fetch(CID, CAP);
    expect(Buffer.from(body).toString()).toBe('hello chunked');
  });

  it('returns a large binary body byte for byte', async () => {
    const big = Buffer.alloc(300_000);
    for (let i = 0; i < big.length; i++) big[i] = (i * 31) & 0xff;
    respond = (_p, _h, res) => {
      res.setHeader('content-length', String(big.length));
      res.end(big);
    };
    const body = await transport().fetch(CID, CAP);
    expect(Buffer.from(body).equals(big)).toBe(true);
  });

  it('fails the TLS handshake when the namespace certificate is not trusted', async () => {
    const t = new RelayedFetch({ relays: [relayUrl], namespaceHost: NS_HOST });
    await expect(t.fetch(CID, CAP)).rejects.toMatchObject({ code: RelayCode.ConnectFailed });
    expect(seen.storageRequests).toHaveLength(0);
  });

  it('fails when the certificate is for another host', async () => {
    const t = new RelayedFetch({ relays: [relayUrl], namespaceHost: 'other.relay-test.local', ca: fixture('ns.crt') });
    await expect(t.fetch(CID, CAP)).rejects.toBeInstanceOf(RelayError);
    expect(seen.storageRequests).toHaveLength(0);
  });

  it('waits a random delay up to jitterMs before sending', async () => {
    const t = transport({ jitterMs: 200, random: () => 0.5 });
    const start = Date.now();
    await t.fetch(CID, CAP);
    expect(Date.now() - start).toBeGreaterThanOrEqual(95);
  });

  it('truncated response is an error, not a short body', async () => {
    respond = (_p, _h, res) => {
      res.writeHead(200, { 'content-length': '100' });
      res.write('only a little');
      res.socket?.destroy();
    };
    await expect(transport().fetch(CID, CAP)).rejects.toBeInstanceOf(RelayError);
  });

  it('enforces maxBodyBytes', async () => {
    await expect(transport({ maxBodyBytes: 3 }).fetch(CID, CAP)).rejects.toMatchObject({
      code: RelayCode.TooLarge,
    });
  });
});

describe('storage node refusals map to typed errors', () => {
  const refusal = (status: number, code: string) => {
    respond = (_p, _h, res) => {
      const body = JSON.stringify({ error: `refused ${code}`, code });
      res.writeHead(status, { 'content-type': 'application/json', 'content-length': Buffer.byteLength(body) });
      res.end(body);
    };
  };

  it.each([
    [403, 'FETCH_CAP_INVALID'],
    [403, 'FETCH_CAP_REVOKED'],
    [401, 'FETCH_CAP_MISSING'],
  ])('%i %s becomes FetchCapError carrying the code', async (status, code) => {
    refusal(status, code);
    const err = await transport().fetch(CID, CAP).catch((e) => e);
    expect(err).toBeInstanceOf(FetchCapError);
    expect(err.code).toBe(code);
    expect(err.httpStatus).toBe(status);
    expect(err.message).toBe(`refused ${code}`);
  });

  it('keeps the gateway retryable verdict on a 404', async () => {
    respond = (_p, _h, res) => {
      const body = JSON.stringify({ ok: false, error: { code: 'NOT_FOUND', message: 'gone', retryable: false } });
      res.writeHead(404, { 'content-length': Buffer.byteLength(body) });
      res.end(body);
    };
    const err = await transport().fetch(CID, CAP).catch((e) => e);
    expect(err).toBeInstanceOf(NotFoundError);
    expect(err.retryable).toBe(false);
  });

  it('survives a non-JSON error body', async () => {
    respond = (_p, _h, res) => {
      res.writeHead(502, { 'content-length': '3' });
      res.end('bad');
    };
    await expect(transport().fetch(CID, CAP)).rejects.toMatchObject({ httpStatus: 502, code: 'HTTP_502' });
  });
});

describe('relay pre-upgrade refusals map to RelayError', () => {
  it.each([
    [400, undefined, RelayCode.DestinationNotAllowed],
    [429, undefined, RelayCode.RateLimited],
    [503, undefined, RelayCode.Unavailable],
    [502, undefined, RelayCode.Refused],
    [400, 'RELAY_DESTINATION_NOT_ALLOWED', 'RELAY_DESTINATION_NOT_ALLOWED'],
  ])('%i (code %s) -> %s', async (status, bodyCode, expected) => {
    refuse = {
      status,
      body: JSON.stringify(bodyCode ? { error: 'no', code: bodyCode } : { error: 'no' }),
    };
    const err = await transport().fetch(CID, CAP).catch((e) => e);
    expect(err).toBeInstanceOf(RelayError);
    expect(err.code).toBe(expected);
    expect(err.httpStatus).toBe(status);
    expect(err.message).toBe('no');
    expect(seen.storageRequests).toHaveLength(0);
  });

  it('exposes Retry-After on a 429', async () => {
    refuse = { status: 429, headers: { 'Retry-After': '7' }, body: '{"error":"slow down"}' };
    const err = await transport().fetch(CID, CAP).catch((e) => e);
    expect(err.retryAfterSeconds).toBe(7);
  });

  it('an unreachable relay is RELAY_CONNECT_FAILED', async () => {
    const t = transport({ relays: ['http://127.0.0.1:1'] });
    await expect(t.fetch(CID, CAP)).rejects.toMatchObject({ code: RelayCode.ConnectFailed, httpStatus: 0 });
  });
});

describe('abort', () => {
  it('rejects at once when the signal is already aborted, opening nothing', async () => {
    const ctl = new AbortController();
    ctl.abort();
    await expect(transport().fetch(CID, CAP, { signal: ctl.signal })).rejects.toMatchObject({ code: 'ABORTED' });
    expect(seen.relayRequests).toHaveLength(0);
  });

  it('abandons a fetch that is waiting on the storage node', async () => {
    respond = () => {
      /* never answers */
    };
    const ctl = new AbortController();
    const pending = transport().fetch(CID, CAP, { signal: ctl.signal });
    await vi.waitFor(() => expect(seen.storageRequests).toHaveLength(1));
    ctl.abort();
    const err = await pending.catch((e) => e);
    expect(err).toBeInstanceOf(NetworkError);
    expect(err.code).toBe('ABORTED');
  });

  it('abandons a fetch during the jitter wait, before the request is sent', async () => {
    const ctl = new AbortController();
    const pending = transport({ jitterMs: 60_000, random: () => 1 }).fetch(CID, CAP, { signal: ctl.signal });
    await vi.waitFor(() => expect(seen.relayRequests).toHaveLength(1));
    ctl.abort();
    await expect(pending).rejects.toMatchObject({ code: 'ABORTED' });
    expect(seen.storageRequests).toHaveLength(0);
  });

  it('times out', async () => {
    respond = () => {
      /* never answers */
    };
    await expect(transport({ timeoutMs: 150 }).fetch(CID, CAP)).rejects.toMatchObject({ code: 'TIMEOUT' });
  });
});

describe('input validation', () => {
  it.each([
    ['a CID that could split the request', `${CID}\r\nX: y`],
    ['a CID with a path', '../etc'],
    ['an empty CID', ''],
  ])('rejects %s before opening anything', async (_n, cid) => {
    await expect(transport().fetch(cid, CAP)).rejects.toMatchObject({ code: 'VALIDATION_FAILED' });
    expect(seen.relayRequests).toHaveLength(0);
  });

  it('rejects a token containing a line break', async () => {
    await expect(transport().fetch(CID, { ...CAP, token: 'a\r\nHost: evil' })).rejects.toMatchObject({
      code: 'VALIDATION_FAILED',
    });
    expect(seen.relayRequests).toHaveLength(0);
  });
});

describe('namespaceHost validation', () => {
  it.each([
    ['a host with a port', `${NS_HOST}:443`],
    ['a host with a path', `${NS_HOST}/x`],
    ['a host with userinfo', `evil@${NS_HOST}`],
    ['a host with a line break', `${NS_HOST}\r\nX: y`],
    ['a host with a space', `ns test.example`],
    ['a non-ASCII host', 'ns-\u212A.example.com'],
    ['a host with an underscore', 'ns_a.example.com'],
    ['an empty host', ''],
    ['a label starting with a hyphen', '-ns.example.com'],
    ['a label of 64 characters', `${'a'.repeat(64)}.example.com`],
    ['a bracketed IPv6 literal', '[::1]'],
  ])('refuses %s at construction', (_n, host) => {
    expect(() => new RelayedFetch({ relays: [relayUrl], namespaceHost: host })).toThrowError(
      expect.objectContaining({ code: 'VALIDATION_FAILED' }),
    );
  });

  it.each(['ns-a.example.com', 'NS-A.Example.COM', 'xn--9ca.example.com', 'localhost', NS_HOST])(
    'accepts %s',
    (host) => {
      expect(() => new RelayedFetch({ relays: [relayUrl], namespaceHost: host })).not.toThrow();
    },
  );
});

describe('relay selection', () => {
  it('never picks the namespace host, even when it is listed', () => {
    const relays = ['https://relay-a.example', `https://${NS_HOST}`, 'https://relay-b.example'];
    for (let i = 0; i < 200; i++) {
      expect(pickRelay(relays, NS_HOST)).not.toContain(NS_HOST);
    }
  });

  it('matches the namespace host case-insensitively and ignores port and path', () => {
    const relays = [`https://${NS_HOST.toUpperCase()}:8443/x`, 'relay-a.example'];
    for (let i = 0; i < 50; i++) expect(pickRelay(relays, NS_HOST)).toBe('relay-a.example');
  });

  it('is roughly uniform over the usable relays', () => {
    const relays = ['https://a.example', 'https://b.example', 'https://c.example', 'https://d.example'];
    const counts: Record<string, number> = {};
    const n = 8000;
    for (let i = 0; i < n; i++) {
      const r = pickRelay(relays, NS_HOST);
      counts[r] = (counts[r] ?? 0) + 1;
    }
    for (const r of relays) {
      expect(counts[r]).toBeGreaterThan((n / 4) * 0.85);
      expect(counts[r]).toBeLessThan((n / 4) * 1.15);
    }
  });

  it('covers the whole range of the random source', () => {
    const relays = ['https://a.example', 'https://b.example'];
    expect(pickRelay(relays, NS_HOST, () => 0)).toBe('https://a.example');
    expect(pickRelay(relays, NS_HOST, () => 0.999999)).toBe('https://b.example');
  });

  it('refuses a list with nothing usable, at construction', () => {
    expect(() => new RelayedFetch({ relays: [], namespaceHost: NS_HOST })).toThrowError(RelayError);
    expect(() => new RelayedFetch({ relays: [`https://${NS_HOST}`], namespaceHost: NS_HOST })).toThrow(
      expect.objectContaining({ code: RelayCode.NoRelay }),
    );
  });

  it('picks again for every fetch', async () => {
    const t = new RelayedFetch({
      relays: [relayUrl, 'http://127.0.0.1:1'],
      namespaceHost: NS_HOST,
      ca: fixture('ns.crt'),
      random: vi.fn().mockReturnValueOnce(0).mockReturnValueOnce(0.9),
    });
    await expect(t.fetch(CID, CAP)).resolves.toBeInstanceOf(Uint8Array);
    await expect(t.fetch(CID, CAP)).rejects.toMatchObject({ code: RelayCode.ConnectFailed });
  });
});

describe('no direct fallback', () => {
  function storageClient() {
    const directFetch = vi.fn(async () => new Response('direct', { status: 200 }));
    const http = new HttpClient({ baseURL: 'https://gw.example', fetch: directFetch as unknown as typeof fetch });
    const getBinary = vi.spyOn(http, 'getBinary');
    const globalFetch = vi.spyOn(globalThis, 'fetch');
    return { client: new StorageClient(http), directFetch, getBinary, globalFetch };
  }

  it.each([
    ['the relay is down', () => undefined, 'http://127.0.0.1:1'],
    ['the relay refuses with 503', () => { refuse = { status: 503, body: '{"error":"down"}' }; }, undefined],
    ['the relay rate-limits', () => { refuse = { status: 429, body: '{"error":"slow"}' }; }, undefined],
    ['the tunnel TLS fails', () => undefined, undefined, true],
  ])('%s: the error surfaces and no direct request is made', async (_n, setup, relayOverride, untrusted) => {
    setup();
    const { client, directFetch, getBinary, globalFetch } = storageClient();
    const t = new RelayedFetch({
      relays: [relayOverride ?? relayUrl],
      namespaceHost: NS_HOST,
      ...(untrusted ? {} : { ca: fixture('ns.crt') }),
    });

    await expect(client.fetchWith(t, CID, CAP)).rejects.toBeInstanceOf(RelayError);

    expect(directFetch).not.toHaveBeenCalled();
    expect(getBinary).not.toHaveBeenCalled();
    expect(globalFetch).not.toHaveBeenCalled();
  });

  it('a cap refusal is thrown, not retried direct', async () => {
    respond = (_p, _h, res) => {
      const body = JSON.stringify({ error: 'revoked', code: 'FETCH_CAP_REVOKED' });
      res.writeHead(403, { 'content-length': Buffer.byteLength(body) });
      res.end(body);
    };
    const { client, directFetch, getBinary } = storageClient();
    await expect(client.fetchWith(transport(), CID, CAP)).rejects.toMatchObject({ code: 'FETCH_CAP_REVOKED' });
    expect(seen.storageRequests).toHaveLength(1); // not retried either
    expect(directFetch).not.toHaveBeenCalled();
    expect(getBinary).not.toHaveBeenCalled();
  });

  it('pin-propagation retries stay on the relay', async () => {
    let n = 0;
    respond = (_p, _h, res) => {
      if (++n < 3) {
        const body = JSON.stringify({ error: 'not found' });
        res.writeHead(404, { 'content-length': Buffer.byteLength(body) });
        res.end(body);
        return;
      }
      res.setHeader('content-length', '2');
      res.end('ok');
    };
    const { client, directFetch, getBinary } = storageClient();
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'], shouldAdvanceTime: true });
    try {
      const promise = client.fetchWith(transport(), CID, CAP);
      await vi.advanceTimersByTimeAsync(10_000);
      expect(Buffer.from(await promise).toString()).toBe('ok');
    } finally {
      vi.useRealTimers();
    }
    expect(seen.relayRequests).toHaveLength(3);
    expect(directFetch).not.toHaveBeenCalled();
    expect(getBinary).not.toHaveBeenCalled();
  });
});
