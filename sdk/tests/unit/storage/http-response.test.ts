import { describe, expect, it, vi } from 'vitest';
import { RelayCode, RelayError } from '../../../src/errors';
import { Http1ResponseParser } from '../../../src/storage/http-response';

const enc = (s: string) => Buffer.from(s, 'latin1');
const text = (b: Uint8Array) => Buffer.from(b).toString('utf8');

function parse(chunks: Array<string | Buffer>, cap = 1 << 20) {
  const p = new Http1ResponseParser(cap);
  for (const c of chunks) p.push(typeof c === 'string' ? enc(c) : c);
  return p;
}

describe('Http1ResponseParser', () => {
  it('reads a Content-Length body split across arbitrary chunks', () => {
    const raw = 'HTTP/1.1 200 OK\r\nContent-Length: 11\r\nX-A: b\r\n\r\nhello world';
    for (let cut = 1; cut < raw.length; cut += 3) {
      const p = parse([raw.slice(0, cut), raw.slice(cut)]);
      const r = p.finish();
      expect(r.status).toBe(200);
      expect(r.headers['x-a']).toBe('b');
      expect(text(r.body)).toBe('hello world');
    }
  });

  it('reads a chunked body, including chunk extensions and trailers', () => {
    const p = parse([
      'HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n',
      '5;ext=1\r\nhello\r\n',
      '6\r\n world\r\n0\r\nTrailer-X: y\r\n\r\n',
    ]);
    expect(p.done).toBe(true);
    expect(text(p.finish().body)).toBe('hello world');
  });

  it('reads a chunked body with no trailers', () => {
    const p = parse(['HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n3\r\nabc\r\n0\r\n\r\n']);
    expect(p.done).toBe(true);
    expect(text(p.finish().body)).toBe('abc');
  });

  it('reads an error page delimited by the connection closing', () => {
    const p = parse(['HTTP/1.1 502 Bad Gateway\r\n\r\npart one ', 'part two']);
    expect(p.done).toBe(false);
    expect(text(p.finish().body)).toBe('part one part two');
  });

  it('refuses a 200 delimited only by the connection closing', () => {
    let caught: unknown;
    try {
      parse(['HTTP/1.1 200 OK\r\n\r\nan object, or the start of one']);
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(RelayError);
    expect((caught as RelayError).code).toBe(RelayCode.ProtocolError);
    expect((caught as RelayError).message).toMatch(/neither Content-Length nor chunked/);
  });

  it('keeps binary bytes intact', () => {
    const bin = Buffer.from([0, 255, 13, 10, 13, 10, 7]);
    const p = parse([enc(`HTTP/1.1 200 OK\r\nContent-Length: ${bin.length}\r\n\r\n`), bin]);
    expect(Buffer.from(p.finish().body).equals(bin)).toBe(true);
  });

  it('handles an empty body and skips an interim 100 response', () => {
    const p = parse(['HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n']);
    expect(p.finish()).toMatchObject({ status: 200, body: new Uint8Array(0) });
  });

  it('reports a response that ends before its Content-Length', () => {
    const p = parse(['HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nshort']);
    expect(() => p.finish()).toThrowError(RelayError);
    expect(() => p.finish()).toThrow(/closed before/);
  });

  it('reports a chunked body that ends mid-stream', () => {
    const p = parse(['HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhel']);
    expect(() => p.finish()).toThrowError(RelayError);
  });

  it.each([
    ['a non-HTTP status line', 'SSH-2.0-OpenSSH\r\n\r\n'],
    ['a bad header line', 'HTTP/1.1 200 OK\r\nnocolon\r\n\r\n'],
    ['a bad Content-Length', 'HTTP/1.1 200 OK\r\nContent-Length: -1\r\n\r\n'],
    ['a bad chunk size', 'HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\n'],
    ['an unterminated chunk', 'HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nabXX'],
  ])('rejects %s as a protocol error', (_name, raw) => {
    let caught: unknown;
    try {
      parse([raw]);
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(RelayError);
    expect((caught as RelayError).code).toBe(RelayCode.ProtocolError);
  });

  it('enforces the body cap from Content-Length and from chunks', () => {
    expect(() => parse(['HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\n'], 10)).toThrow(/limit/);
    expect(() =>
      parse(['HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n8\r\n12345678\r\n8\r\n'], 10),
    ).toThrow(/limit/);
    expect(() => parse(['HTTP/1.1 502 X\r\n\r\n', 'x'.repeat(20)], 10)).toThrow(/limit/);
  });

  it('refuses a head over the limit even when it arrives in one piece', () => {
    const head = 'HTTP/1.1 200 OK\r\nContent-Length: 0\r\nX-Pad: ' + 'a'.repeat(70 << 10) + '\r\n\r\n';
    let caught: unknown;
    try {
      parse([head]);
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(RelayError);
    expect((caught as RelayError).code).toBe(RelayCode.ProtocolError);
    expect((caught as RelayError).message).toMatch(/header too large/);
  });

  it('refuses a head that grows without ever ending', () => {
    const p = new Http1ResponseParser(1 << 20);
    p.push(enc('HTTP/1.1 200 OK\r\nX-Pad: '));
    expect(() => {
      for (let i = 0; i < 80; i++) p.push(enc('a'.repeat(1024)));
    }).toThrow(/header too large/);
  });

  it('finds a head terminator that straddles two pieces', () => {
    const raw = 'HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok';
    const at = raw.indexOf('\r\n\r\n') + 2; // between the two CRLFs
    const p = parse([raw.slice(0, at), raw.slice(at)]);
    expect(text(p.finish().body)).toBe('ok');
  });

  it('reads a body fed one byte at a time', () => {
    const raw = 'HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\nT: v\r\n\r\n';
    const p = new Http1ResponseParser(1 << 20);
    for (const byte of Buffer.from(raw, 'latin1')) p.push(Buffer.from([byte]));
    expect(p.done).toBe(true);
    expect(text(p.finish().body)).toBe('hello');
  });

  it('does not copy the accumulated input on every push', () => {
    // One large chunk delivered in small pieces used to be re-concatenated on every
    // piece, which is quadratic in the chunk size.
    const concat = vi.spyOn(Buffer, 'concat');
    try {
      const size = 512 << 10;
      const p = new Http1ResponseParser(size * 2);
      p.push(enc(`HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n${size.toString(16)}\r\n`));
      const piece = Buffer.alloc(512, 7);
      for (let sent = 0; sent < size; sent += piece.length) p.push(piece);
      p.push(enc('\r\n0\r\n\r\n'));
      expect(p.finish().body.length).toBe(size);
      expect(concat.mock.calls.length).toBeLessThanOrEqual(2);
    } finally {
      concat.mockRestore();
    }
  });
});
